package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func legacyRefundSession(t *testing.T, request string, sourceHold, tokenHold int) *BillingSession {
	t.Helper()
	truncate(t)
	seedBonusUser(t, 31201, 0, 1000)
	seedToken(t, 31202, 31201, "synthetic-original-refund-token", 1000)
	funding := &WalletFunding{userId: 31201, requestId: request, debitOrder: []model.LedgerAccount{model.AccountPaid}}
	require.NoError(t, funding.PreConsume(sourceHold))
	require.NoError(t, model.DecreaseTokenQuota(31202, "synthetic-original-refund-token", tokenHold))
	return &BillingSession{relayInfo: &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "synthetic-original-refund-token", RequestId: request}, funding: funding, preConsumedQuota: sourceHold, tokenConsumed: tokenHold}
}

// currentRefundSession uses the actual reservation API and its held_v2 owner.
// legacyRefundSession above remains an explicit historical/manual fixture; it
// must not be made to masquerade as a freshly atomically reserved request.
func currentRefundSession(t *testing.T, request string, amount int) *BillingSession {
	t.Helper()
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	seedBonusUser(t, 31201, 0, 1000)
	seedToken(t, 31202, 31201, "synthetic-original-refund-token", 1000)
	seedBonusEligibleModel(t, "current-refund-model", boolPtr(false))
	setting := operation_setting.GetQuotaSetting()
	oldTrust := setting.TrustQuotaUSD
	setting.TrustQuotaUSD = 0
	t.Cleanup(func() { setting.TrustQuotaUSD = oldTrust })
	c := testGinContext(t)
	c.Set("token_quota", 1000)
	info := &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "synthetic-original-refund-token", RequestId: request, OriginModelName: "current-refund-model", ForcePreConsume: true, UserSetting: dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningEnabled: boolPtr(false)}}
	session, apiErr := NewBillingSession(c, info, amount)
	require.Nil(t, apiErr)
	info.Billing = session
	return session
}

func waitRefundCallback(t *testing.T, _ *BillingSession) {
	t.Helper()
	// These consumers run in an owned child containing no notification daemon.
	// gopool decrements WorkerCount only after callbacks return, including nested
	// token-cache work; refunded and final SQL are not callback-completion signals.
	require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond)
}

func TestPolicyPreservingRefundSeparateHeldAmounts(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	// This historical split hold has no atomically created v2 owner. Never
	// synthesize authority from these in-memory amounts to authorize a credit.
	s := legacyRefundSession(t, "separate-source-token", 100, 70)
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	var user model.User
	require.NoError(t, model.DB.First(&user, 31201).Error)
	assert.Equal(t, 900, user.QuotaPaid, "missing held authority cannot credit source")
	assert.Equal(t, 900, user.Quota)
	assert.Zero(t, user.QuotaBonus)
	assert.Zero(t, user.QuotaReward)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	assert.Equal(t, 930, token.RemainQuota)
	assert.Equal(t, 70, token.UsedQuota)
	var count int64
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Count(&count).Error)
	assert.Zero(t, count, "no new owner is fabricated for an old manual hold")
	assert.True(t, s.NeedsRefund(), "unknown historical delivery remains for review")
}

func TestPolicyPreservingSettlementFinalMoreLessAndReplay(t *testing.T) {
	for _, actual := range []int{0, 100, 180} {
		t.Run(string(rune('a'+actual)), func(t *testing.T) {
			s := currentRefundSession(t, "current-final-adjustment", 100)
			require.NoError(t, s.Settle(actual))
			require.NoError(t, s.Settle(actual))
			require.Error(t, s.Settle(actual+1))
			s.Refund(testGinContext(t))
			require.False(t, s.NeedsRefund())
			assertUserBalances(t, 31201, 0, 1000-actual, 0)
			var token model.Token
			require.NoError(t, model.DB.First(&token, 31202).Error)
			require.Equal(t, 1000-actual, token.RemainQuota)
		})
	}
}

// Retain the fixture entry name, but reject the former reserve-then-refund
// behavior: once actual usage is observed, only same-actual settlement can retry.
func TestPolicyPreservingFailedSettlementCanReserveThenRefund(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	s := currentRefundSession(t, "failed-settle-then-reserve", 100)
	name := "fail_settle_token"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("synthetic settlement token failure"))
		}
	}))
	require.Error(t, s.Settle(200))
	assertUserBalances(t, 31201, 0, 900, 0)
	require.NoError(t, model.DB.Callback().Update().Remove(name))
	require.ErrorIs(t, s.Reserve(150), model.ErrBillingFinalized)
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	require.False(t, s.NeedsRefund())
	assertUserBalances(t, 31201, 0, 900, 0)
	var owner model.BillingFinalization
	require.NoError(t, model.DB.First(&owner, "request_id = ?", s.relayInfo.RequestId).Error)
	require.Equal(t, model.BillingSettlementReview, owner.State)
	require.Equal(t, 200, owner.ActualQuota)
	require.Error(t, s.Settle(201))
	require.NoError(t, s.Settle(200), "only an explicit same-actual retry may finish the charge")
	require.NoError(t, s.Settle(200))
	assertUserBalances(t, 31201, 0, 800, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 800, token.RemainQuota)
}

// Deliberately no HostBillingFinalizationSource marker: extension ownership is
// not inferred from Source()==wallet, even when it implements legacy Tx settle.
type unmarkedTransactionalWallet struct{ inner *WalletFunding }

func (f *unmarkedTransactionalWallet) Source() string         { return BillingSourceWallet }
func (f *unmarkedTransactionalWallet) PreConsume(n int) error { return f.inner.PreConsume(n) }
func (f *unmarkedTransactionalWallet) Settle(n int) error     { return f.inner.Settle(n) }
func (f *unmarkedTransactionalWallet) Refund() error          { return f.inner.Refund() }
func (f *unmarkedTransactionalWallet) SettleInTx(tx *gorm.DB, n int) (moneyport.SettlementResult, error) {
	return f.inner.SettleInTx(tx, n)
}
func (f *unmarkedTransactionalWallet) ApplySettlementResult(n int, r moneyport.SettlementResult) {
	f.inner.ApplySettlementResult(n, r)
}

type unmarkedSubscriptionFunding struct{ streamFailureFunding }

func (*unmarkedSubscriptionFunding) Source() string { return BillingSourceSubscription }

type alternateBillingSettler struct{}

func (alternateBillingSettler) Settle(int) error              { return nil }
func (alternateBillingSettler) Refund(*gin.Context)           {}
func (alternateBillingSettler) NeedsRefund() bool             { return false }
func (alternateBillingSettler) GetPreConsumedQuota() int      { return 0 }
func (alternateBillingSettler) Reserve(int) error             { return nil }
func (alternateBillingSettler) ReserveBeforeSubmit(int) error { return nil }

func TestPolicyPreservingExtensionCapabilityBoundary(t *testing.T) {
	t.Run("custom_wallet_atomic_replay", func(t *testing.T) {
		s := legacyRefundSession(t, "custom-wallet-original-contract", 100, 100)
		inner := s.funding.(*WalletFunding)
		s.funding = &unmarkedTransactionalWallet{inner: inner}
		require.False(t, s.durableTerminalSource())
		require.NoError(t, s.Settle(150))
		replay := &BillingSession{relayInfo: s.relayInfo, funding: &unmarkedTransactionalWallet{inner: inner}, preConsumedQuota: 100, tokenConsumed: 100}
		require.NoError(t, replay.Settle(150))
		assertUserBalances(t, 31201, 0, 850, 0)
		var token model.Token
		require.NoError(t, model.DB.First(&token, 31202).Error)
		require.Equal(t, 850, token.RemainQuota)
		var count int64
		require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Count(&count).Error)
		require.Zero(t, count)
	})
	t.Run("custom_billing_settler_insert", func(t *testing.T) {
		truncate(t)
		task := &model.Task{TaskID: "custom-settler-task", UserId: 991, Data: []byte(`{"inline":true}`)}
		require.NoError(t, task.InsertWithContext(context.Background(), "data"))
		require.Positive(t, task.ID)
		require.NotEmpty(t, task.Data)
		var stored model.Task
		require.NoError(t, model.DB.First(&stored, task.ID).Error)
		require.Empty(t, stored.Data)
		var count int64
		require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Count(&count).Error)
		require.Zero(t, count)
	})
	t.Run("custom_subscription_delta", func(t *testing.T) {
		funding := &unmarkedSubscriptionFunding{streamFailureFunding: streamFailureFunding{outstanding: 100}}
		info := &relaycommon.RelayInfo{IsPlayground: true}
		s := &BillingSession{relayInfo: info, funding: funding, preConsumedQuota: 100}
		require.False(t, s.durableTerminalSource())
		require.NoError(t, s.Settle(60))
		require.NoError(t, s.Settle(60))
		require.EqualValues(t, -40, info.SubscriptionPostDelta)
		settleCalls, refundCalls := funding.counts()
		require.Equal(t, 1, settleCalls)
		require.Zero(t, refundCalls)
	})
	t.Run("custom_non_wallet", TestStreamFailurePartialUsageSettlesOnceAndNeverRefunds)
}

func TestPolicyPreservingRollbackProbeFailureDoesNotReserve(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	s := currentRefundSession(t, "probe-failure", 100)
	name := "fail_settlement_token_before_probe"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("token unavailable"))
		}
	}))
	require.Error(t, s.Settle(200))
	require.NoError(t, model.DB.Callback().Update().Remove(name))
	name = "fail_probe_authority_read"
	probeHits := 0
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == reflect.TypeOf(model.BillingFinalization{}) {
			probeHits++
			tx.AddError(errors.New("probe unavailable"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(name)) })
	require.ErrorIs(t, s.Reserve(150), model.ErrBillingFinalized)
	require.Zero(t, probeHits, "a frozen actual rejects reserve without reopening its authority")
	require.ErrorContains(t, s.Settle(200), "probe unavailable")
	require.GreaterOrEqual(t, probeHits, 1, "explicit settlement retry and its failure evidence require durable authority")
	require.NoError(t, model.DB.Callback().Query().Remove(name))
	assertUserBalances(t, 31201, 0, 900, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 900, token.RemainQuota)
	var count int64
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Count(&count).Error)
	require.EqualValues(t, 1, count, "original held owner survives the rejected reservation")
	require.ErrorIs(t, s.Reserve(150), model.ErrBillingFinalized)
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	assertUserBalances(t, 31201, 0, 900, 0)
	require.NoError(t, s.Settle(200))
	require.NoError(t, s.Settle(200))
	assertUserBalances(t, 31201, 0, 800, 0)
}

func TestPolicyPreservingRefundIndependentViolationFee(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	for _, order := range []string{"fee_before_refund", "refund_before_fee"} {
		t.Run(order, func(t *testing.T) {
			s := currentRefundSession(t, "refund-independent-fee", 100)
			s.relayInfo.Billing = s
			s.relayInfo.BillingSource = BillingSourceWallet
			s.relayInfo.ChannelMeta = &relaycommon.ChannelMeta{}
			s.relayInfo.StartTime = time.Now()
			s.relayInfo.UserQuota = 1000
			s.relayInfo.UserSetting.QuotaWarningThreshold = -1
			s.relayInfo.PriceData.GroupRatioInfo.GroupRatio = 1
			settings := model_setting.GetGrokSettings()
			oldEnabled, oldAmount := settings.ViolationDeductionEnabled, settings.ViolationDeductionAmount
			settings.ViolationDeductionEnabled = true
			settings.ViolationDeductionAmount = 50 / common.QuotaPerUnit
			t.Cleanup(func() { settings.ViolationDeductionEnabled = oldEnabled; settings.ViolationDeductionAmount = oldAmount })
			if order == "refund_before_fee" {
				s.Refund(testGinContext(t))
				waitRefundCallback(t, s)
			}
			feeErr := types.NewErrorWithStatusCode(errors.New("synthetic fee condition"), types.ErrorCodeViolationFeeGrokCSAM, 400)
			require.True(t, ChargeViolationFeeIfNeeded(testGinContext(t), s.relayInfo, feeErr))
			s.Refund(testGinContext(t))
			waitRefundCallback(t, s)
			expected := 950
			assertUserBalances(t, 31201, 0, expected, 0)
			var token model.Token
			require.NoError(t, model.DB.First(&token, 31202).Error)
			require.Equal(t, expected, token.RemainQuota)
			require.Equal(t, 1000-expected, token.UsedQuota)
		})
	}
}

func TestPolicyPreservingRealtimeIncrementAndTerminal(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	// Initialize dialect quoting exactly as production startup, without opening a
	// separate log DSN or changing the current disposable database.
	t.Setenv("LOG_SQL_DSN", "")
	oldLogDB, oldMaster := model.LOG_DB, common.IsMasterNode
	common.IsMasterNode = false
	err := model.InitLogDB()
	common.IsMasterNode = oldMaster
	require.NoError(t, err)
	t.Cleanup(func() { model.LOG_DB = oldLogDB })
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))

	for _, terminal := range []string{"final_settle", "refund_initial_hold", "failed_final_then_refund"} {
		t.Run(terminal, func(t *testing.T) {
			truncate(t)
			seedBonusEligibleModel(t, "terminal-wss", boolPtr(false))
			seedBonusUser(t, 31201, 0, 1000)
			seedToken(t, 31202, 31201, "synthetic-original-refund-token", 1000)
			quotaSetting := operation_setting.GetQuotaSetting()
			oldTrust := quotaSetting.TrustQuotaUSD
			quotaSetting.TrustQuotaUSD = 0
			t.Cleanup(func() { quotaSetting.TrustQuotaUSD = oldTrust })
			oldRatios := ratio_setting.ModelRatio2JSONString()
			ratios := ratio_setting.GetModelRatioCopy()
			ratios["terminal-wss"] = 1
			encoded, err := common.Marshal(ratios)
			require.NoError(t, err)
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(encoded)))
			t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios)) })
			info := &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "synthetic-original-refund-token", RequestId: "realtime-" + terminal, ForcePreConsume: true, OriginModelName: "terminal-wss", UsingGroup: "default", UserGroup: "default", StartTime: time.Now(), ChannelMeta: &relaycommon.ChannelMeta{}, UserSetting: dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningThreshold: -1, QuotaWarningEnabled: boolPtr(false)}}
			info.PriceData.GroupRatioInfo.GroupRatio = 1
			c := testGinContext(t)
			c.Set("token_quota", 1000)
			s, apiErr := NewBillingSession(c, info, 100)
			require.Nil(t, apiErr)
			info.Billing = s
			usage := &dto.RealtimeUsage{TotalTokens: 20, InputTokens: 20, InputTokenDetails: dto.InputTokenDetails{TextTokens: 20}}
			require.NoError(t, PreWssConsumeQuota(c, info, usage))
			require.EqualValues(t, 1, info.WssPostConsumeSequence)
			assertUserBalances(t, 31201, 0, 880, 0)
			if terminal == "failed_final_then_refund" {
				name := "fail_realtime_final_token"
				hits := 0
				require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == reflect.TypeOf(model.Token{}) {
						hits++
						tx.AddError(errors.New("realtime final token failed"))
					}
				}))
				require.Error(t, s.Settle(20))
				require.Equal(t, 1, hits)
				require.NoError(t, model.DB.Callback().Update().Remove(name))
				assertUserBalances(t, 31201, 0, 880, 0)
			}
			expected := 980
			if terminal == "final_settle" {
				// The observed segment is an absolute held reservation, not an
				// independent charge on top of the final aggregate usage.
				PostWssConsumeQuota(c, info, "terminal-wss", usage, nil)
			} else {
				s.Refund(c)
				waitRefundCallback(t, s)
				if terminal == "failed_final_then_refund" {
					assertUserBalances(t, 31201, 0, 880, 0)
					require.ErrorIs(t, s.Reserve(150), model.ErrBillingFinalized)
					var review model.BillingFinalization
					require.NoError(t, model.DB.First(&review, "request_id = ?", info.RequestId).Error)
					require.Equal(t, model.BillingSettlementReview, review.State)
					require.Equal(t, 20, review.ActualQuota)
					require.NoError(t, s.Settle(20), "Refund must not retry already frozen realtime usage")
				}
			}
			require.False(t, s.NeedsRefund())

			assertUserBalances(t, 31201, 0, expected, 0)
			var token model.Token
			require.NoError(t, model.DB.First(&token, 31202).Error)
			require.Equal(t, expected, token.RemainQuota)
			require.Equal(t, 20, token.UsedQuota)
			var owner model.BillingFinalization
			require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).First(&owner).Error)
			require.Equal(t, model.BillingSettled, owner.State)
			require.Equal(t, 20, owner.ActualQuota)
			PostWssConsumeQuota(c, info, "terminal-wss", usage, nil)
			s.Refund(c)
			waitRefundCallback(t, s)
			assertUserBalances(t, 31201, 0, expected, 0)
		})
	}
}

func TestPolicyPreservingZeroTokenDeltaRetainsReplacementCounter(t *testing.T) {
	for _, hold := range []int{0, 100} {
		for _, change := range []string{"key", "owner"} {
			t.Run(fmt.Sprintf("hold_%d/%s", hold, change), func(t *testing.T) {
				s := currentRefundSession(t, "zero-delta-original-identity", hold)
				update := map[string]any{"remain_quota": 777, "used_quota": 23}
				if change == "key" {
					update["key"] = "synthetic-replacement"
				} else {
					update["user_id"] = 39999
				}
				require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 31202).Updates(update).Error)
				require.NoError(t, s.Settle(hold))
				require.NoError(t, s.Settle(hold))
				require.False(t, s.NeedsRefund())
				assertUserBalances(t, 31201, 0, 1000-hold, 0)
				var token model.Token
				require.NoError(t, model.DB.First(&token, 31202).Error)
				require.Equal(t, 777, token.RemainQuota)
				require.Equal(t, 23, token.UsedQuota)
			})
		}
	}
}
