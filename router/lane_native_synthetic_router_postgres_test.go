//go:build lane_billing_integration_dev && !enterprise_lane_lab

package router

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	laneRouterDevModel       = "gpt-4o-mini"
	laneRouterDevSendPath    = "/v1/chat/completions"
	laneRouterDevToken       = "syntheticnativerouterfinitekey"
	laneRouterDevUpstreamKey = "inert-synthetic-native-upstream"
	laneRouterDevPaid        = 10000
	laneRouterDevGrant       = 3000
	laneRouterDevUserID      = 801
	laneRouterDevTokenID     = 802
	laneRouterDevChannelID   = 803
	laneRouterDevWire        = `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"fixture"}],"max_completion_tokens":7,"stream":true,"stream_options":{"include_usage":true}}`

	laneRouterDevSSEPrefix = "data: {\"id\":\"chatcmpl-fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"fixture reply\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"chatcmpl-fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4o-mini\",\"choices\":[],\"usage\":"
	laneRouterDevUsage = `{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}`
)

type laneRouterDevPromptVerifier struct{}

func (laneRouterDevPromptVerifier) VerifyNativeChatPromptBound(_ context.Context, route service.LaneNativeChatRoute, final []byte, completion int) (int, error) {
	body := gjson.ParseBytes(final)
	if route.ChannelType != constant.ChannelTypeOpenAI || route.ChannelID != laneRouterDevChannelID || route.Group != "default" ||
		route.Model != laneRouterDevModel || route.Path != laneRouterDevSendPath ||
		len(body.Get("messages").Array()) != 1 || body.Get("messages.0.role").String() != "user" ||
		body.Get("messages.0.content").String() != "fixture" || completion != 6 || body.Get("max_completion_tokens").Int() != 6 {
		return 0, errors.New("outside closed synthetic prompt authority")
	}
	return 13, nil
}

func laneRouterDevTestDatabase(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	ip, err := netip.ParseAddr(u.Hostname())
	require.NoError(t, err)
	require.True(t, ip.IsLoopback())
	require.Equal(t, "postgres", u.Scheme)
	require.Equal(t, "/owner_treasury_child_test", u.Path)
	require.Equal(t, "disable", u.Query().Get("sslmode"))
	require.Len(t, u.Query(), 1, "fixture DSN cannot redirect to an unrelated server")
	bootstrap, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	pool, err := bootstrap.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	var version int
	require.NoError(t, bootstrap.Raw("SELECT current_setting('server_version_num')::int").Scan(&version).Error)
	require.Equal(t, 180006, version)
	schema := "lane_billing_dev_" + common.GetUUID()[:8]
	require.Regexp(t, `^lane_billing_dev_[a-f0-9]{8}$`, schema)
	require.NoError(t, bootstrap.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)).Error)
	t.Cleanup(func() { require.NoError(t, bootstrap.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error) })
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	for k, v := range map[string]string{"SQL_DSN": u.String(), "LOG_SQL_DSN": "", "REDIS_CONN_STRING": "", "TRUSTED_PROXIES": "none"} {
		t.Setenv(k, v)
	}
	common.IsMasterNode = false
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.BatchUpdateEnabled = false
	common.CriticalRateLimitEnable = false
	common.LogConsumeEnabled = true
	common.DataExportEnabled = false
	constant.StreamingTimeout = 30 // normal native SSE reader requires a configured positive timeout
	setting.ModelRequestRateLimitEnabled = false
	operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
	require.NoError(t, i18n.Init())
	bootScoped, err := gorm.Open(postgres.New(postgres.Config{DSN: u.String(), PreferSimpleProtocol: true}), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	require.NoError(t, bootScoped.AutoMigrate(&model.Option{}))
	bootPool, err := bootScoped.DB()
	require.NoError(t, err)
	require.NoError(t, bootPool.Close())
	require.NoError(t, model.InitDB())
	require.NoError(t, model.InitLogDB())
	t.Cleanup(func() { require.NoError(t, model.CloseDB()) })
	db := model.DB
	fixtures := []any{&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Option{}, &model.Model{}, &model.Vendor{}, &model.Log{},
		&model.SubscriptionPlan{}, &model.UserCallInterception{}, &model.UserCallInterceptionHit{}, &model.UserCallInterceptionAudit{},
		&model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}, &model.TokenRoutingMetric{},
		&model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}, &model.QuotaData{}}
	fixtures = append(fixtures, model.BonusSourceModels()...)
	fixtures = append(fixtures, model.SharedWalletTreasuryModels()...)
	require.NoError(t, db.AutoMigrate(fixtures...))
	ratio_setting.InitRatioSettings()
	model.InitOptionMap()
	registry := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(registry))
	require.NoError(t, walletmodule.RegisterFundingFactory(registry))
	require.NoError(t, service.ConfigureFundingFactoryRegistry(registry))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString("{}"))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o-mini":1}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"gpt-4o-mini":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, model.InstallLaneBillingSyntheticDevSchema(t.Context(), db))
	service.InitHttpClient()
	return db
}

func TestLaneNativeSyntheticPublicRouterPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_LANE_INTEGRATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable loopback PostgreSQL TEST_LANE_INTEGRATION_POSTGRES_DSN")
	}
	// Regular router/auth tests share process globals. Isolate them from the
	// PostgreSQL fixture, strictly without running production program startup.
	const childEnv = "LANE_NATIVE_SYNTHETIC_ROUTER_TEST_CHILD"
	if os.Getenv(childEnv) != "1" {
		binary, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), 70*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestLaneNativeSyntheticPublicRouterPostgres$", "-test.v", "-test.count=1", "-test.timeout=60s")
		cmd.Env = append(os.Environ(), childEnv+"=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		require.NotContains(t, string(out), "--- SKIP:")
		require.Contains(t, string(out), "--- PASS: TestLaneNativeSyntheticPublicRouterPostgres")
		t.Log(string(out))
		return
	}
	db := laneRouterDevTestDatabase(t, dsn)
	var calls atomic.Int32
	var omitDone atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, err := io.ReadAll(io.LimitReader(r.Body, 8193))
		assert.NoError(t, err)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, laneRouterDevSendPath, r.URL.Path)
		assert.Equal(t, "Bearer "+laneRouterDevUpstreamKey, r.Header.Get("Authorization"))
		assert.EqualValues(t, len(raw), r.ContentLength)
		assert.EqualValues(t, 6, gjson.GetBytes(raw, "max_completion_tokens").Int(), "same finalized parameter override reaches the wire")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, laneRouterDevSSEPrefix+laneRouterDevUsage+"}\n\n")
		if !omitDone.Load() {
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		}
	}))
	t.Cleanup(upstream.Close)

	user := model.User{Id: laneRouterDevUserID, Username: "synthetic-native-router", AffCode: "native-router", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: laneRouterDevPaid, QuotaPaid: laneRouterDevPaid}
	user.SetSetting(dto.UserSetting{BillingPreference: "wallet_only"})
	token := model.Token{Id: laneRouterDevTokenID, UserId: user.Id, Key: laneRouterDevToken, Status: common.TokenStatusEnabled,
		ExpiredTime: -1, RemainQuota: laneRouterDevPaid, Group: "default", UsedQuota: 11}
	patch := `{"max_completion_tokens":6}`
	ch := model.Channel{Id: laneRouterDevChannelID, Name: "synthetic-native-router", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Key: laneRouterDevUpstreamKey, Group: "default", Models: laneRouterDevModel, BaseURL: &upstream.URL, ParamOverride: &patch}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, db.Create(&ch).Error)
	require.NoError(t, ch.AddAbilities(db))
	id := model.LaneBillingFiniteKeyIdentity{UserID: user.Id, TokenID: token.Id, Epoch: 9, TokenFingerprint: model.BillingTokenFingerprint(token.Key)}
	require.NoError(t, model.EnrollLaneBillingFiniteKeyDev(t.Context(), db, id))
	_, _, err := model.ApplyLaneBillingFiniteKeyDevTransfer(t.Context(), db, model.LaneBillingFiniteKeyTransfer{
		Identity: id, OperationID: "synthetic-router-key-grant", Slot: 2, Direction: "grant", Amount: laneRouterDevGrant})
	require.NoError(t, err)
	_, _, err = model.ApplySharedWalletTransfer(t.Context(), db, model.SharedWalletTransfer{UserID: user.Id, Epoch: 17, Slot: 3,
		OperationID: "synthetic-router-paid-grant", Direction: "grant", Amount: laneRouterDevGrant})
	require.NoError(t, err)

	plan := relay.LaneNativeSyntheticPlan{
		DB: db, Group: "default", Model: laneRouterDevModel, ProviderOrigin: upstream.URL, SyntheticProviderKey: laneRouterDevUpstreamKey,
		FrozenWireHMACKey: sha256.Sum256([]byte("isolated synthetic native router HMAC")), Verifier: laneRouterDevPromptVerifier{},
		TerminalPrefix: laneRouterDevSSEPrefix,
		Owner: model.LaneBillingChildDevBinding{
			Owner:       model.LaneBillingOwnerIdentity{LaneID: "synthetic-native-router-slot-3", Epoch: 88, UserID: id.UserID, TokenID: id.TokenID, TokenFingerprint: id.TokenFingerprint},
			WalletEpoch: 17, WalletSlot: 3, KeyEpoch: id.Epoch, KeySlot: 2, ChannelID: ch.Id, FundingPolicySHA256: strings.Repeat("a", 64),
		},
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies(nil))
	var armSynthetic atomic.Bool
	engine.Use(middleware.RequestId(), func(c *gin.Context) {
		if armSynthetic.Load() {
			c.Request = c.Request.WithContext(relay.WithLaneNativeSyntheticPlan(c.Request.Context(), plan))
		}
		c.Next()
	})
	SetRelayRouter(engine)
	gateway := httptest.NewServer(engine)
	t.Cleanup(gateway.Close)
	post := func(body, key string) (requestID string, status int, response string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, gateway.URL+laneRouterDevSendPath, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer sk-"+key)
		req.Header.Set("X-Lane-Force", "true") // no client control in either direction
		resp, err := gateway.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
		require.NoError(t, err)
		return resp.Header.Get(common.RequestIdKey), resp.StatusCode, string(b)
	}
	readOwner := func(requestID string) (ordinary model.BillingFinalization, childState string) {
		require.NoError(t, db.Where("request_id = ?", requestID).Take(&ordinary).Error)
		require.NoError(t, db.Table("lane_billing_child_receipts_v1").Select("state").Where("request_id = ?", requestID).Scan(&childState).Error)
		return
	}

	armSynthetic.Store(true)
	unauthID, unauthStatus, _ := post(laneRouterDevWire, "invalid-synthetic-key")
	require.Equal(t, http.StatusUnauthorized, unauthStatus)
	var unauthRows int64
	require.NoError(t, db.Model(&model.BillingFinalization{}).Where("request_id = ?", unauthID).Count(&unauthRows).Error)
	assert.Zero(t, unauthRows, "internal intent cannot bypass normal TokenAuth")
	rejectedID, rejectedStatus, _ := post(strings.Replace(laneRouterDevWire, "fixture", "different", 1), laneRouterDevToken)
	require.Equal(t, http.StatusServiceUnavailable, rejectedStatus)
	var rejectedRows int64
	require.NoError(t, db.Model(&model.BillingFinalization{}).Where("request_id = ?", rejectedID).Count(&rejectedRows).Error)
	assert.Zero(t, rejectedRows, "failed final-byte proof binds no ordinary or lane owner")
	assert.Zero(t, calls.Load())

	passthrough := `{"pass_through_body_enabled":true}`
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", ch.Id).UpdateColumn("setting", passthrough).Error)
	unsupportedID, unsupportedStatus, _ := post(laneRouterDevWire, laneRouterDevToken)
	require.Equal(t, http.StatusServiceUnavailable, unsupportedStatus)
	require.NoError(t, db.Model(&model.BillingFinalization{}).Where("request_id = ?", unsupportedID).Count(&rejectedRows).Error)
	assert.Zero(t, rejectedRows, "passthrough is rejected before ordinary or lane owner")
	assert.Zero(t, calls.Load())
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", ch.Id).UpdateColumn("setting", nil).Error)

	requestID, status, body := post(laneRouterDevWire, laneRouterDevToken)
	require.Equal(t, http.StatusOK, status, "%s", body)
	require.Contains(t, body, "fixture reply")
	require.Contains(t, body, "[DONE]")
	owner, state := readOwner(requestID)
	assert.Equal(t, "lane_owner_v1", owner.State)
	assert.Zero(t, owner.SourceHeld)
	assert.Equal(t, "settled", state)
	assert.EqualValues(t, 1, calls.Load())
	var measured model.LaneBillingMeasuredDev
	require.NoError(t, db.Where("request_id = ?", requestID).Take(&measured).Error)
	assert.True(t, measured.Ready)
	assert.Equal(t, 3, measured.PromptTokens)
	assert.Equal(t, 2, measured.CompletionTokens)
	assert.Equal(t, 5, measured.ActualQuota)

	omitDone.Store(true)
	unknownID, unknownStatus, _ := post(laneRouterDevWire, laneRouterDevToken)
	require.NotEqual(t, http.StatusOK, unknownStatus)
	unknownOwner, unknownState := readOwner(unknownID)
	assert.Equal(t, "lane_owner_v1", unknownOwner.State)
	assert.Equal(t, "delivery_unknown", unknownState)
	assert.EqualValues(t, 2, calls.Load())
	var measuredRows int64
	require.NoError(t, db.Model(&model.LaneBillingMeasuredDev{}).Count(&measuredRows).Error)
	assert.EqualValues(t, 1, measuredRows, "incomplete usage cannot be projected as known")
	var priorWallet model.SharedWalletShare
	require.NoError(t, db.Take(&priorWallet).Error)
	assert.Equal(t, 5, priorWallet.Spent)
	assert.Positive(t, priorWallet.Held)
	assert.Equal(t, laneRouterDevGrant, priorWallet.Available+priorWallet.Spent+priorWallet.Held)
	var before model.User
	require.NoError(t, db.Take(&before, user.Id).Error)
	assert.Equal(t, laneRouterDevPaid-laneRouterDevGrant, before.QuotaPaid, "no ordinary paid reserve or settle")
	var currentKey model.Token
	require.NoError(t, db.Take(&currentKey, token.Id).Error)
	assert.Equal(t, laneRouterDevPaid-laneRouterDevGrant, currentKey.RemainQuota)
	assert.Equal(t, 11, currentKey.UsedQuota)

	// Project only the durable measured owner through the actual idempotent
	// tagged worker, including the exact shared log destination scope.
	destination, err := model.InstallLaneBillingUsageLogDevSchema(t.Context(), db)
	require.NoError(t, err)
	projected, err := model.ProjectLaneBillingUsageDev(t.Context(), db, db, destination, 10)
	require.NoError(t, err)
	require.Equal(t, 1, projected)
	projected, err = model.ProjectLaneBillingUsageDev(t.Context(), db, db, destination, 10)
	require.NoError(t, err)
	require.Zero(t, projected)
	var consume model.Log
	require.NoError(t, db.Where("request_id = ? AND type = ?", requestID, model.LogTypeConsume).Take(&consume).Error)
	assert.Equal(t, 5, consume.Quota)
	assert.Equal(t, id.UserID, consume.UserId)
	assert.Equal(t, id.TokenID, consume.TokenId)
	assert.Equal(t, ch.Id, consume.ChannelId)
	var unknownLogs int64
	require.NoError(t, db.Model(&model.Log{}).Where("request_id = ? AND type = ?", unknownID, model.LogTypeConsume).Count(&unknownLogs).Error)
	assert.Zero(t, unknownLogs)

	armSynthetic.Store(false)
	omitDone.Store(false)
	ordinaryID, ordinaryStatus, ordinaryBody := post(laneRouterDevWire, laneRouterDevToken)
	require.Equal(t, http.StatusOK, ordinaryStatus, "%s", ordinaryBody)
	ordinary, ordinaryChild := readOwner(ordinaryID)
	assert.Equal(t, model.BillingSettled, ordinary.State)
	assert.Equal(t, "wallet", ordinary.Source)
	assert.Empty(t, ordinaryChild, "client header cannot arm a dev lane")
	assert.EqualValues(t, 3, calls.Load())
	var finalWallet model.SharedWalletShare
	require.NoError(t, db.Take(&finalWallet).Error)
	assert.Equal(t, priorWallet, finalWallet, "ordinary flow cannot spend the synthetic lane pool")
}
