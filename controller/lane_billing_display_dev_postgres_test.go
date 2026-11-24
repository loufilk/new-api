//go:build lane_billing_integration_dev

package controller

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLaneBillingDisplayDevHTTPPostgres(t *testing.T) {
	dsn, redisDSN := os.Getenv("TEST_LANE_DISPLAY_POSTGRES_DSN"), os.Getenv("TEST_LANE_DISPLAY_REDIS_DSN")
	if dsn == "" || redisDSN == "" {
		t.Skip("requires disposable loopback PostgreSQL 18.6 and Redis 8.8.0")
	}
	const subprocess = "LANE_DISPLAY_POSTGRES_TEST_CHILD"
	if os.Getenv(subprocess) != "1" {
		binary, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestLaneBillingDisplayDevHTTPPostgres$", "-test.v", "-test.count=1", "-test.timeout=50s")
		cmd.Env = append(os.Environ(), subprocess+"=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		require.NotContains(t, string(out), "--- SKIP:")
		require.Contains(t, string(out), "--- PASS: TestLaneBillingDisplayDevHTTPPostgres")
		t.Log(string(out))
		return
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "postgres", u.Scheme)
	require.True(t, net.ParseIP(u.Hostname()).IsLoopback())
	require.Equal(t, "/lane_display_test", u.Path)
	require.Equal(t, url.Values{"sslmode": {"disable"}}, u.Query())
	admin, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	adminPool, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminPool.Close()) })
	var ver int
	require.NoError(t, admin.Raw("SHOW server_version_num").Scan(&ver).Error)
	require.Equal(t, 180006, ver)
	schema := "lane_billing_dev_" + common.GetUUID()[:8]
	require.Regexp(t, `^lane_billing_dev_[a-f0-9]{8}$`, schema)
	require.NoError(t, admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)).Error) })
	u.RawQuery = url.Values{"sslmode": {"disable"}, "search_path": {schema}}.Encode()
	t.Setenv("SQL_DSN", u.String())
	db, err := model.OpenPrimaryForMaintenance(false)
	require.NoError(t, err)
	db = db.Session(&gorm.Session{Logger: logger.Discard})
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.MemoryCacheEnabled, common.LogConsumeEnabled = false, false, true
	require.NoError(t, i18n.Init())
	release := model.LaneStoreReleaseV1{StoreID: strings.Repeat("a", 32), ReleaseID: strings.Repeat("b", 32),
		BinarySHA256: strings.Repeat("c", 64), Epoch: 1}
	require.NoError(t, model.InstallLaneStoreAuthorityV1(t.Context(), db, release))
	fixtures := []any{&model.User{}, &model.Token{}, &model.Option{}, &model.Channel{}, &model.Log{}, &model.AgentProfile{}, &model.AgentInvitation{}, &model.UserSession{},
		&model.SubscriptionPlan{}, &model.UserCallInterception{}, &model.UserCallInterceptionHit{}, &model.UserCallInterceptionAudit{},
		&model.BillingFinalization{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}}
	fixtures = append(fixtures, model.SharedWalletTreasuryModels()...)
	fixtures = append(fixtures, model.BonusSourceModels()...)
	require.NoError(t, db.AutoMigrate(fixtures...))
	require.NoError(t, model.InstallLaneBillingSyntheticDevSchema(t.Context(), db))
	destination, err := model.InstallLaneBillingUsageLogDevSchema(t.Context(), db)
	require.NoError(t, err)
	owner := model.User{Id: 701, Username: "lane-display-owner", AffCode: "display-owner", Role: common.RoleCommonUser,
		Group: "default", Status: common.UserStatusEnabled, AuthVersion: 5, Quota: 500, QuotaPaid: 500, UsedQuota: 8, RequestCount: 2}
	peer := model.User{Id: 702, Username: "lane-display-peer", AffCode: "display-peer", Role: common.RoleCommonUser,
		Group: "default", Status: common.UserStatusEnabled, AuthVersion: 5, Quota: 900, QuotaPaid: 900}
	key := model.Token{Id: 711, UserId: owner.Id, Key: "syntheticlogicaldisplay", Status: common.TokenStatusEnabled, ExpiredTime: -1,
		RemainQuota: 150, UsedQuota: 10, Group: "default", AllowUserQuotaQuery: true}
	otherKey := model.Token{Id: 712, UserId: peer.Id, Key: "syntheticpeerdisplay", Status: common.TokenStatusEnabled, ExpiredTime: -1,
		RemainQuota: 40, Group: "default", AllowUserQuotaQuery: true}
	siblingKey := model.Token{Id: 713, UserId: owner.Id, Key: "syntheticsiblingdisplay", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, RemainQuota: 60, Group: "default", AllowUserQuotaQuery: true}
	for _, row := range []any{&owner, &peer, &key, &otherKey, &siblingKey, &model.Channel{Id: 731, Name: "synthetic-display-channel"}} {
		require.NoError(t, db.Create(row).Error)
	}
	ownerSession, err := service.CreateLoginSessionAtAuthVersion(owner.Id, owner.AuthVersion, "synthetic-display", "127.0.0.1", "test")
	require.NoError(t, err)
	peerSession, err := service.CreateLoginSessionAtAuthVersion(peer.Id, peer.AuthVersion, "synthetic-display", "127.0.0.1", "test")
	require.NoError(t, err)
	id := model.LaneBillingFiniteKeyIdentity{UserID: owner.Id, TokenID: key.Id, TokenFingerprint: model.BillingTokenFingerprint(key.Key), Epoch: 3}
	walletEpoch := int64(5)
	require.NoError(t, model.EnrollLaneBillingFiniteKeyDev(t.Context(), db, id))
	_, _, err = model.ApplySharedWalletTransfer(t.Context(), db, model.SharedWalletTransfer{
		UserID: owner.Id, Epoch: walletEpoch, Slot: 4, Direction: "grant", Amount: 200, OperationID: "display-wallet-grant"})
	require.NoError(t, err)
	_, _, err = model.ApplyLaneBillingFiniteKeyDevTransfer(t.Context(), db, model.LaneBillingFiniteKeyTransfer{
		Identity: id, Slot: 2, Direction: "grant", Amount: 100, OperationID: "display-key-grant"})
	require.NoError(t, err)
	bind := model.LaneBillingChildDevBinding{
		Owner: model.LaneBillingOwnerIdentity{RequestID: "display-settled", LaneID: "display-lane", Epoch: release.Epoch, UserID: owner.Id,
			TokenID: key.Id, TokenFingerprint: id.TokenFingerprint},
		WalletEpoch: walletEpoch, WalletSlot: 4, KeyEpoch: id.Epoch, KeySlot: 2, ChannelID: 731,
		FundingPolicySHA256: strings.Repeat("a", 64), FrozenPriceSHA256: strings.Repeat("b", 64),
		RouteSHA256: strings.Repeat("c", 64), FinalWireSHA256: strings.Repeat("d", 64),
	}
	s, err := model.ReserveLaneBillingChildDev(t.Context(), db, bind, 40)
	require.NoError(t, err)
	require.NoError(t, s.ClaimDispatch(t.Context(), bind.FinalWireSHA256))
	require.NoError(t, s.SettleMeasured(t.Context(), model.LaneBillingMeasuredDev{
		RequestID: bind.Owner.RequestID, ChannelID: bind.ChannelID, Group: "default", Model: "fixture",
		PromptTokens: 21, CompletionTokens: 4, ActualQuota: 25, UsageSHA256: strings.Repeat("4", 64),
	}))
	bind.Owner.RequestID = "display-outstanding"
	_, err = model.ReserveLaneBillingChildDev(t.Context(), db, bind, 20)
	require.NoError(t, err)
	display, err := model.NewLaneBillingDisplayDev(t.Context(), db, release, id, walletEpoch)
	require.NoError(t, err)
	wrong := release
	wrong.Epoch++
	_, err = model.NewLaneBillingDisplayDev(t.Context(), db, wrong, id, walletEpoch)
	require.Error(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/api/usage/token/", middleware.TokenAuthReadOnly(), func(c *gin.Context) { getTokenUsage(c, display) })
	router.GET("/ordinary/api/usage/token/", middleware.TokenAuthReadOnly(), GetTokenUsage)
	// Direct compatibility probes set a synthetic identity after auth.
	router.GET("/ordinary/dashboard/billing/subscription", func(c *gin.Context) {
		c.Set("id", owner.Id)
		GetSubscription(c)
	})
	router.GET("/ordinary/dashboard/billing/usage", GetUsage)
	router.GET("/dashboard/billing/subscription", middleware.TokenAuth(), func(c *gin.Context) { getSubscription(c, display) })
	router.GET("/dashboard/billing/usage", middleware.TokenAuth(), func(c *gin.Context) { getUsage(c, display) })
	router.GET("/api/user/self", middleware.DisableCache(), middleware.UserAuth(), func(c *gin.Context) { getSelf(c, display) })
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)
	get := func(path string, key string) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, gateway.URL+path, nil)
		require.NoError(t, err)
		if path == "/api/user/self" {
			req.Header.Set("Authorization", "Bearer "+key)
		} else {
			req.Header.Set("Authorization", "Bearer sk-"+key)
		}
		req.Header.Set("X-Lane-Force", "true")
		res, err := gateway.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		body, err := io.ReadAll(io.LimitReader(res.Body, 16385))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode, string(body))
		require.NotContains(t, string(body), "syntheticlogicaldisplay")
		require.NotContains(t, string(body), "syntheticpeerdisplay")
		return body
	}
	expect := func(body []byte, path string, value int64) {
		t.Helper()
		f := gjson.GetBytes(body, path)
		require.True(t, f.Exists(), "%s absent: %s", path, body)
		require.Equal(t, value, f.Int(), "%s: %s", path, body)
	}
	expectToken := func(accountUsed int64) {
		t.Helper()
		body := get("/api/usage/token/", key.Key)
		expect(body, "data.total_available", 105)
		expect(body, "data.total_used", 55)
		expect(body, "data.total_granted", 160)
		expect(body, "data.user_quota.total_available", 455)
		expect(body, "data.user_quota.total_used", accountUsed)
	}
	expectSelf := func(accountUsed, requests int64) {
		t.Helper()
		body := get("/api/user/self", ownerSession.AccessToken)
		expect(body, "data.quota", 455)
		expect(body, "data.used_quota", accountUsed)
		expect(body, "data.request_count", requests)
	}
	expectToken(8)
	expectSelf(8, 2)
	expect(get("/api/user/self", peerSession.AccessToken), "data.quota", 900)
	expect(get("/ordinary/api/usage/token/", key.Key), "data.user_quota.total_available", 300)
	expect(get("/api/usage/token/", otherKey.Key), "data.user_quota.total_available", 900)
	require.False(t, gjson.GetBytes(get("/api/usage/token/", siblingKey.Key), "data.total_available").Exists(),
		"a same-wallet Key with no exact epoch cannot fall back to raw account balances")
	common.DisplayTokenStatEnabled = true
	oldType := operation_setting.GetGeneralSetting().QuotaDisplayType
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	defer func() { operation_setting.GetGeneralSetting().QuotaDisplayType = oldType }()
	expect(get("/dashboard/billing/subscription", key.Key), "hard_limit_usd", 160)
	expect(get("/dashboard/billing/usage", key.Key), "total_usage", 5500)
	common.DisplayTokenStatEnabled = false
	expect(get("/dashboard/billing/subscription", key.Key), "hard_limit_usd", 463)
	expect(get("/dashboard/billing/usage", key.Key), "total_usage", 800)

	// A projection ACK adds 25 to the account exactly once. No change to
	// balance or Key quota occurs, and the separate hold is not actual usage.
	for _, n := range []int{1, 0} {
		done, err := model.ProjectLaneBillingUsageDev(t.Context(), db, db, destination, 1)
		require.NoError(t, err)
		require.Equal(t, n, done)
		expectToken(33)
		expectSelf(33, 3)
	}
	expect(get("/dashboard/billing/usage", key.Key), "total_usage", 3300)

	redisOpts, err := redis.ParseURL(redisDSN)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(redisOpts.Addr)
	require.NoError(t, err)
	require.True(t, net.ParseIP(host).IsLoopback())
	rdb := redis.NewClient(redisOpts)
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	info, err := rdb.InfoMap(t.Context(), "server").Result()
	require.NoError(t, err)
	require.Equal(t, "8.8.0", info["Server"]["redis_version"])
	size, err := rdb.DBSize(t.Context()).Result()
	require.NoError(t, err)
	require.Zero(t, size)
	common.RDB, common.RedisEnabled = rdb, true
	cached, err := model.GetTokenByKey(key.Key, false)
	require.NoError(t, err)
	require.True(t, cached.AllowUserQuotaQuery)
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", key.Id).UpdateColumn("allow_user_quota_query", false).Error)
	stale, err := model.GetTokenByKey(key.Key, false)
	require.NoError(t, err)
	require.True(t, stale.AllowUserQuotaQuery)
	body := get("/api/usage/token/", key.Key)
	expect(body, "data.total_available", 105)
	require.False(t, gjson.GetBytes(body, "data.user_quota").Exists())
	require.True(t, gjson.GetBytes(get("/dashboard/billing/usage", key.Key), "error").Exists())

	// Corrupted persisted authority is a hard read error, not an ordinary
	// fallback. Other owners continue on their legacy route.
	require.NoError(t, db.Exec("UPDATE lane_store_authority_v1 SET state = 'revoked'").Error)
	require.False(t, gjson.GetBytes(get("/api/usage/token/", key.Key), "data.total_available").Exists())
	require.False(t, gjson.GetBytes(get("/api/user/self", ownerSession.AccessToken), "data.quota").Exists())
	expect(get("/api/usage/token/", otherKey.Key), "data.user_quota.total_available", 900)

	// The legacy subscription's second DB read controls its error. This
	// compatibility path has no authority and does not change scoped denial.
	common.RedisEnabled = false
	common.DisplayTokenStatEnabled = false
	require.NoError(t, db.Exec("ALTER TABLE users RENAME COLUMN quota TO synthetic_unreadable_quota").Error)
	expect(get("/ordinary/dashboard/billing/subscription", key.Key), "hard_limit_usd", 33)
	require.NoError(t, db.Exec("ALTER TABLE users RENAME COLUMN synthetic_unreadable_quota TO quota").Error)

	// A miswired handler without an authenticated Key must return its
	// existing error envelope rather than dereferencing a nil DB result.
	common.DisplayTokenStatEnabled = true
	require.Equal(t, "upstream_error", gjson.GetBytes(get("/ordinary/dashboard/billing/subscription", key.Key), "error.type").String())
	require.Equal(t, "new_api_error", gjson.GetBytes(get("/ordinary/dashboard/billing/usage", key.Key), "error.type").String())
}
