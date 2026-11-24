//go:build lane_billing_integration_dev

package channel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	laneFixtureModel          = "gpt-4o-mini"
	laneFixtureWire           = `{"model":"gpt-4o-mini","stream":true,"max_completion_tokens":17,"messages":[{"role":"user","content":"synthetic"}],"stream_options":{"include_usage":true}}`
	laneFixtureUsage          = `{"prompt_tokens":11,"completion_tokens":2,"total_tokens":13}`
	laneFixtureTerminalPrefix = "data: {\"model\":\"gpt-4o-mini\",\"choices\":[],\"usage\":"
	laneFixtureTerminalSuffix = "}\n\ndata: [DONE]\n\n"
	laneFixturePaid           = 2000
	laneFixtureGrant          = 1000
	laneFixtureKeyQuota       = 5000
)

type laneFixtureAdaptor struct {
	Adaptor
	url string
}

func (a laneFixtureAdaptor) GetRequestURL(*relaycommon.RelayInfo) (string, error) { return a.url, nil }
func (laneFixtureAdaptor) SetupRequestHeader(_ *gin.Context, h *http.Header, _ *relaycommon.RelayInfo) error {
	h.Set("Content-Type", "application/json")
	return nil
}

type laneFixturePromptBound struct{ route service.LaneNativeChatRoute }

func (f laneFixturePromptBound) VerifyNativeChatPromptBound(_ context.Context, route service.LaneNativeChatRoute, body []byte, completion int) (int, error) {
	if route != f.route || string(body) != laneFixtureWire || completion != 17 {
		return 0, errors.New("request outside exact synthetic verifier scope")
	}
	return 13, nil
}

type laneFixtureBody struct {
	io.Reader
	proof service.LaneNativeChatProof
}

func (b laneFixtureBody) Size() int64 { return b.proof.BodySize() }
func (b laneFixtureBody) NewReader() (io.ReadCloser, error) {
	return io.NopCloser(b.proof.OpenBody()), nil
}

type laneFixtureExecution struct {
	t                         *testing.T
	child                     *model.LaneBillingChildDev
	proof                     service.LaneNativeChatProof
	measurement               service.LaneNativeChatMeasurement
	measured                  []service.LaneNativeChatMeasured
	claims, unknown, ordinary int
}

var _ relaycommon.BillingSettler = (*laneFixtureExecution)(nil)
var _ service.LaneNativeChatObservedExecution = (*laneFixtureExecution)(nil)

func (s *laneFixtureExecution) Settle(int) error {
	s.ordinary++
	return errors.New("fixture cannot settle through ordinary billing")
}
func (s *laneFixtureExecution) Refund(*gin.Context)      { s.ordinary++ }
func (s *laneFixtureExecution) NeedsRefund() bool        { return false }
func (s *laneFixtureExecution) GetPreConsumedQuota() int { return s.proof.Evidence().QuotaCap }
func (s *laneFixtureExecution) Reserve(int) error {
	s.ordinary++
	return errors.New("fixture cap is immutable")
}
func (s *laneFixtureExecution) ReserveBeforeSubmit(n int) error { return s.Reserve(n) }
func (s *laneFixtureExecution) ClaimBillingHTTPDispatch(ctx context.Context) error {
	s.claims++
	return s.child.ClaimDispatch(ctx, s.proof.Evidence().FinalWireSHA256)
}
func (s *laneFixtureExecution) NativeChatMeasurement() service.LaneNativeChatMeasurement {
	return s.measurement
}
func (s *laneFixtureExecution) SettleNativeChatMeasured(ctx context.Context, m service.LaneNativeChatMeasured) error {
	if m.Binding != s.proof.Evidence() {
		s.t.Error("observed owner changed exact proof")
		return errors.New("observed owner changed exact proof")
	}
	err := s.child.SettleMeasured(ctx, model.LaneBillingMeasuredDev{
		RequestID: m.Binding.RequestID, ChannelID: m.Binding.ChannelID, Model: m.Binding.Model, Group: m.Binding.Group,
		PromptTokens: m.PromptTokens, CompletionTokens: m.CompletionTokens, CacheReadTokens: m.CacheReadTokens,
		CacheWriteTokens: m.CacheWriteTokens, CacheWrite12hTokens: m.CacheWrite12hTokens, ActualQuota: m.ActualQuota,
		UsageSHA256: m.UsageSHA256,
	})
	if err == nil {
		s.measured = append(s.measured, m)
	} else {
		s.t.Errorf("synthetic settle: %v", err)
	}
	return err
}
func (s *laneFixtureExecution) PreserveNativeChatDeliveryUnknown(ctx context.Context) error {
	s.unknown++
	err := s.child.PreserveUnknown(ctx)
	if err != nil {
		s.t.Errorf("synthetic unknown preservation: %v", err)
	}
	return err
}

func laneFixtureDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_LANE_INTEGRATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable loopback PostgreSQL TEST_LANE_INTEGRATION_POSTGRES_DSN")
	}
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	address, err := netip.ParseAddr(cfg.Host)
	require.NoError(t, err)
	require.True(t, address.IsLoopback())
	require.Empty(t, cfg.Fallbacks, "no fallback database addresses")
	require.Equal(t, "owner_treasury_child_test", cfg.Database, "only this dedicated database")
	schema := "lane_billing_dev_" + common.GetUUID()[:8]
	require.Regexp(t, `^lane_billing_dev_[a-f0-9]{8}$`, schema)
	cfg.RuntimeParams["search_path"] = schema
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	sqlDB := stdlib.OpenDB(*cfg)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	var version int
	require.NoError(t, db.Raw("SELECT current_setting('server_version_num')::integer").Scan(&version).Error)
	require.Equal(t, 180006, version, "match rehearsed PostgreSQL runtime")
	require.NoError(t, db.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)).Error)
	t.Cleanup(func() { require.NoError(t, db.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error) })
	origMain, origLog := common.MainDatabaseType(), common.LogDatabaseType()
	prevDB, prevLogDB := model.DB, model.LOG_DB
	redisEnabled, batchEnabled := common.RedisEnabled, common.BatchUpdateEnabled
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.BatchUpdateEnabled = false, false
	t.Cleanup(func() {
		common.SetDatabaseTypes(origMain, origLog)
		model.DB, model.LOG_DB = prevDB, prevLogDB
		common.RedisEnabled, common.BatchUpdateEnabled = redisEnabled, batchEnabled
	})
	fixtures := append([]any{&model.BillingFinalization{}, &model.User{}, &model.Token{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.Log{}}, model.SharedWalletTreasuryModels()...)
	fixtures = append(fixtures, model.BonusSourceModels()...)
	require.NoError(t, db.AutoMigrate(fixtures...))
	require.NoError(t, model.InstallLaneBillingSyntheticDevSchema(t.Context(), db))
	return db
}

func laneFixtureSeed(t *testing.T, db *gorm.DB, b model.LaneBillingChildDevBinding) {
	t.Helper()
	user := model.User{Id: b.Owner.UserID, Username: "synthetic-native-lane", AffCode: "native-fixture", Status: common.UserStatusEnabled, Quota: laneFixturePaid, QuotaPaid: laneFixturePaid, UsedQuota: 17, RequestCount: 2}
	key := model.Token{Id: b.Owner.TokenID, UserId: user.Id, Key: "synthetic-original-existing-key", Name: "synthetic existing finite key", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: laneFixtureKeyQuota, UsedQuota: 17}
	require.Equal(t, model.BillingTokenFingerprint(key.Key), b.Owner.TokenFingerprint)
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&key).Error)
	require.NoError(t, db.Create(&model.BillingFinalization{RequestID: "synthetic-existing-ordinary-history", UserID: user.Id, TokenID: key.Id,
		TokenFingerprint: b.Owner.TokenFingerprint, Source: "wallet", State: model.BillingSettled, ActualQuota: 17}).Error)
	grant, replay, err := model.ApplySharedWalletTransfer(t.Context(), db, model.SharedWalletTransfer{UserID: user.Id, Epoch: b.WalletEpoch, Slot: b.WalletSlot,
		OperationID: "synthetic-native-wallet-grant", Direction: "grant", Amount: laneFixtureGrant})
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, laneFixtureGrant, grant.GrantedAfter)
	keyID := laneFixtureKeyID(b)
	require.NoError(t, model.EnrollLaneBillingFiniteKeyDev(t.Context(), db, keyID))
	keyGrant, replay, err := model.ApplyLaneBillingFiniteKeyDevTransfer(t.Context(), db, model.LaneBillingFiniteKeyTransfer{
		Identity: keyID, OperationID: "synthetic-native-key-grant", Slot: b.KeySlot, Direction: "grant", Amount: laneFixtureGrant,
	})
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, laneFixtureKeyQuota, keyGrant.RawBefore)
	require.Equal(t, laneFixtureKeyQuota-laneFixtureGrant, keyGrant.RawAfter)
}

func laneFixtureKeyID(b model.LaneBillingChildDevBinding) model.LaneBillingFiniteKeyIdentity {
	return model.LaneBillingFiniteKeyIdentity{UserID: b.Owner.UserID, TokenID: b.Owner.TokenID,
		Epoch: b.KeyEpoch, TokenFingerprint: b.Owner.TokenFingerprint}
}

// Only this loopback fixture's exact SSE envelope is recognized. This is not
// the general provider stream parser. The shared service still performs the
// real strict usage object decoding and exact pricing/owner checks.
func laneFixtureReadTerminal(resp *http.Response, p service.LaneNativeChatProof) (service.LaneNativeChatMeasurement, *dto.Usage, error) {
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		return service.LaneNativeChatMeasurement{}, nil, errors.New("wrong synthetic stream authority")
	}
	wire, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(wire) > 4096 || !bytes.HasPrefix(wire, []byte(laneFixtureTerminalPrefix)) || !bytes.HasSuffix(wire, []byte(laneFixtureTerminalSuffix)) {
		return service.LaneNativeChatMeasurement{}, nil, errors.New("incomplete synthetic terminal frame")
	}
	raw := wire[len(laneFixtureTerminalPrefix) : len(wire)-len(laneFixtureTerminalSuffix)]
	measurement, err := p.VerifyTerminalUsage(raw)
	if err != nil {
		return service.LaneNativeChatMeasurement{}, nil, err
	}
	var usage dto.Usage
	if err := common.Unmarshal(raw, &usage); err != nil {
		return service.LaneNativeChatMeasurement{}, nil, err
	}
	return measurement, &usage, nil
}

func TestLaneNativeChatSyntheticNormalTransportPostgres(t *testing.T) {
	original := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o-mini":1.5}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(original)) })
	for _, tc := range []struct {
		name     string
		complete bool
	}{
		{"strict terminal usage, exact settled replay", true},
		{"missing DONE retains original hold, no projection", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := laneFixtureDB(t)
			ctx := t.Context()
			c := tokenTransportTestContext(false)
			c.Request = c.Request.WithContext(ctx)
			info := &relaycommon.RelayInfo{RequestId: "synthetic-native-chat-1", UserId: 101, TokenId: 501, IsStream: true, UsingGroup: "default", UserGroup: "default",
				OriginModelName: laneFixtureModel, RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI,
				BillingSource: service.BillingSourceWallet, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 43, ChannelType: constant.ChannelTypeOpenAI}}
			price, err := helper.ModelPriceHelper(c, info, 13, &types.TokenCountMeta{MaxTokens: 17})
			require.NoError(t, err)
			require.Equal(t, price.ModelRatio, info.PriceData.ModelRatio)
			require.Positive(t, price.ModelRatio)
			// Fixture has no cache/tool addends, integer token counts and
			// pinned normal group pricing. Compute independently of settlement.
			wantActual := int(math.Round((11 + 2*price.CompletionRatio) * price.ModelRatio * price.GroupRatioInfo.GroupRatio))
			prepared, err := service.PrepareLaneNativeChatPrice(info, 7)
			require.NoError(t, err)
			route := service.LaneNativeChatRoute{ChannelID: info.ChannelId, ChannelType: info.ChannelType, Group: info.UsingGroup, Model: info.OriginModelName,
				Path: "/v1/chat/completions", AuthoritySHA256: strings.Repeat("a", 64)}
			originalBody := []byte(laneFixtureWire)
			proof, err := prepared.VerifyFinalWire(ctx, route, originalBody, sha256.Sum256([]byte("synthetic fixture key, not credentials")), laneFixturePromptBound{route})
			require.NoError(t, err)
			originalBody[0] = '!' // only the frozen body reaches the shared sender
			evidence := proof.Evidence()
			require.Positive(t, evidence.QuotaCap)
			require.Less(t, evidence.QuotaCap, laneFixtureGrant)
			binding := model.LaneBillingChildDevBinding{Owner: model.LaneBillingOwnerIdentity{RequestID: evidence.RequestID, LaneID: "synthetic-wallet-slot-4",
				Epoch: 11, UserID: info.UserId, TokenID: info.TokenId, TokenFingerprint: model.BillingTokenFingerprint("synthetic-original-existing-key")},
				WalletEpoch: 7, WalletSlot: 4, KeyEpoch: 19, KeySlot: 5, ChannelID: evidence.ChannelID, FundingPolicySHA256: strings.Repeat("b", 64),
				FrozenPriceSHA256: evidence.FrozenPriceSHA256, RouteSHA256: evidence.RouteSHA256, FinalWireSHA256: evidence.FinalWireSHA256}
			laneFixtureSeed(t, db, binding)
			child, err := model.ReserveLaneBillingChildDev(ctx, db, binding, evidence.QuotaCap)
			require.NoError(t, err)
			execution := &laneFixtureExecution{t: t, child: child, proof: proof}
			info.Billing = execution
			var sends atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				got, err := io.ReadAll(io.LimitReader(r.Body, 4097))
				assert.NoError(t, err)
				assert.Equal(t, laneFixtureWire, string(got))
				assert.Equal(t, proof.BodySize(), r.ContentLength)
				assert.Equal(t, route.Path, r.URL.Path)
				assert.Equal(t, http.MethodPost, r.Method)
				w.Header().Set("Content-Type", "text/event-stream")
				terminal := laneFixtureTerminalPrefix + laneFixtureUsage + laneFixtureTerminalSuffix
				if !tc.complete {
					terminal = strings.TrimSuffix(terminal, "data: [DONE]\n\n")
				}
				_, _ = io.WriteString(w, terminal)
			}))
			defer server.Close()
			useTokenTransportTestClient(t, server.Client())
			adaptor := laneFixtureAdaptor{url: server.URL + route.Path}
			send := func() (*http.Response, error) {
				return DoApiRequest(adaptor, c, info, laneFixtureBody{proof.OpenBody(), proof})
			}
			resp, err := send()
			require.NoError(t, err)
			require.NotNil(t, resp)
			measurement, usage, err := laneFixtureReadTerminal(resp, proof)
			require.NoError(t, resp.Body.Close())
			if tc.complete {
				require.NoError(t, err)
				execution.measurement = measurement
				service.PostTextConsumeQuota(c, info, usage, nil)
				service.PostTextConsumeQuota(c, info, usage, nil) // persisted replay, not double spend
				require.Len(t, execution.measured, 2)
				require.Equal(t, execution.measured[0], execution.measured[1])
				assert.Zero(t, execution.unknown)
			} else {
				require.Error(t, err)
				service.PostTextConsumeQuota(c, info, nil, nil) // no verified measurement, not zero usage
				assert.Equal(t, 1, execution.unknown)
				assert.Empty(t, execution.measured)
			}
			retry, err := send()
			if retry != nil {
				_ = retry.Body.Close()
			}
			require.Error(t, err)
			assert.EqualValues(t, 1, sends.Load())
			assert.Equal(t, 1, execution.claims)
			assert.Zero(t, execution.ordinary)
			replayed, err := model.ReserveLaneBillingChildDev(ctx, db, binding, evidence.QuotaCap)
			require.Error(t, err)
			require.Nil(t, replayed)
			var wallet model.SharedWalletShare
			require.NoError(t, db.Take(&wallet).Error)
			var keyHold struct{ Available, Held, Spent, Returned int }
			const keySlotColumns = "available,held,spent,returned"
			require.NoError(t, db.Table("lane_billing_key_shares_v1").Select(keySlotColumns).Take(&keyHold).Error)
			var receipts []model.LaneBillingMeasuredDev
			require.NoError(t, db.Find(&receipts).Error)
			var storedState string
			require.NoError(t, db.Table("lane_billing_child_receipts_v1").Select("state").Where("request_id = ?", binding.Owner.RequestID).Scan(&storedState).Error)
			if tc.complete {
				require.Len(t, receipts, 1)
				measured := execution.measured[0]
				assert.Equal(t, 11, receipts[0].PromptTokens)
				assert.Equal(t, 2, receipts[0].CompletionTokens)
				assert.Equal(t, measured.UsageSHA256, receipts[0].UsageSHA256)
				assert.True(t, receipts[0].Ready)
				assert.Equal(t, measured.ActualQuota, receipts[0].ActualQuota)
				assert.Equal(t, "settled", storedState)
				assert.Equal(t, wantActual, measured.ActualQuota)
				assert.Less(t, measured.ActualQuota, evidence.QuotaCap)
				assert.Equal(t, measured.ActualQuota, wallet.Spent)
				assert.Equal(t, laneFixtureGrant-measured.ActualQuota, wallet.Available)
				assert.Zero(t, wallet.Held)
			} else {
				assert.Empty(t, receipts)
				assert.Equal(t, "delivery_unknown", storedState)
				assert.Zero(t, wallet.Spent)
				assert.Equal(t, evidence.QuotaCap, wallet.Held)
				assert.Equal(t, laneFixtureGrant-evidence.QuotaCap, wallet.Available)
			}
			assert.Equal(t, wallet.Available, keyHold.Available)
			assert.Equal(t, wallet.Held, keyHold.Held)
			assert.Equal(t, wallet.Spent, keyHold.Spent)
			assert.Equal(t, laneFixtureGrant, wallet.Available+wallet.Held+wallet.Spent)
			var user model.User
			var key model.Token
			require.NoError(t, db.Take(&user, binding.Owner.UserID).Error)
			require.NoError(t, db.Take(&key, binding.Owner.TokenID).Error)
			assert.Equal(t, laneFixturePaid-laneFixtureGrant, user.QuotaPaid)
			assert.Equal(t, laneFixturePaid-laneFixtureGrant, user.Quota)
			assert.Equal(t, 17, user.UsedQuota)
			assert.Equal(t, 2, user.RequestCount)
			assert.Equal(t, 17, key.UsedQuota)
			assert.Equal(t, laneFixtureKeyQuota-laneFixtureGrant, key.RemainQuota)
			// Close fresh admission and reclaim only idle balances. An unknown
			// delivery keeps both original holds; a measured spend stays spent.
			keyID := laneFixtureKeyID(binding)
			require.NoError(t, model.ChangeLaneBillingFiniteKeyDevState(ctx, db, keyID, model.LaneBillingFiniteKeyPaused))
			idle := wallet.Available
			returnedKey, replay, err := model.ApplyLaneBillingFiniteKeyDevTransfer(ctx, db, model.LaneBillingFiniteKeyTransfer{
				Identity: keyID, OperationID: "synthetic-native-key-return", Slot: binding.KeySlot, Direction: "reclaim", Amount: idle,
			})
			require.NoError(t, err)
			require.False(t, replay)
			assert.Equal(t, laneFixtureKeyQuota-laneFixtureGrant+idle, returnedKey.RawAfter)
			returnedWallet, replay, err := model.ApplySharedWalletTransfer(ctx, db, model.SharedWalletTransfer{
				UserID: binding.Owner.UserID, Epoch: binding.WalletEpoch, Slot: binding.WalletSlot,
				OperationID: "synthetic-native-wallet-return", Direction: "reclaim", Amount: idle,
			})
			require.NoError(t, err)
			require.False(t, replay)
			assert.Equal(t, idle, returnedWallet.ReturnedAfter)
			require.NoError(t, db.Take(&user, binding.Owner.UserID).Error)
			require.NoError(t, db.Take(&key, binding.Owner.TokenID).Error)
			var afterWallet model.SharedWalletShare
			var afterKey struct{ Available, Held, Spent, Returned int }
			require.NoError(t, db.Take(&afterWallet).Error)
			require.NoError(t, db.Table("lane_billing_key_shares_v1").Select(keySlotColumns).Take(&afterKey).Error)
			assert.Zero(t, afterWallet.Available)
			assert.Zero(t, afterKey.Available)
			assert.Equal(t, wallet.Held, afterWallet.Held)
			assert.Equal(t, keyHold.Held, afterKey.Held)
			assert.Equal(t, wallet.Spent, afterWallet.Spent)
			assert.Equal(t, keyHold.Spent, afterKey.Spent)
			assert.Equal(t, idle, afterWallet.Returned)
			assert.Equal(t, idle, afterKey.Returned)
			assert.Equal(t, laneFixturePaid, user.QuotaPaid+afterWallet.Held+afterWallet.Spent)
			assert.Equal(t, laneFixtureKeyQuota, key.RemainQuota+afterKey.Held+afterKey.Spent)
			assert.Equal(t, 17, key.UsedQuota)

			var logs int64
			require.NoError(t, db.Model(&model.Log{}).Count(&logs).Error)
			assert.Zero(t, logs, "ordinary log cannot be duplicated")
			_, ordinaryProjection := common.GetContextKey(c, constant.ContextKeyConsumeLog)
			assert.False(t, ordinaryProjection)
		})
	}
}
