package service

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLaneWalletObservationUsesActualDebitOrder(t *testing.T) {
	for _, tc := range []struct {
		name                string
		order               []model.LedgerAccount
		paid, bonus, reward int
		want                string
	}{
		{"paid snapshot", []model.LedgerAccount{model.AccountReward, model.AccountPaid}, 100, 0, 0, "paid_wallet_snapshot"},
		{"ineligible bonus is not spendable", []model.LedgerAccount{model.AccountReward, model.AccountPaid}, 100, 100, 0, "paid_wallet_snapshot"},
		{"reward still precedes paid", []model.LedgerAccount{model.AccountReward, model.AccountPaid}, 100, 0, 1, "non_paid_wallet"},
		{"eligible bonus", []model.LedgerAccount{model.AccountBonus, model.AccountReward, model.AccountPaid}, 100, 1, 0, "non_paid_wallet"},
		{"empty order", nil, 100, 0, 0, "unresolved"},
		{"unknown account", []model.LedgerAccount{"unknown", model.AccountPaid}, 100, 0, 0, "unresolved"},
		{"negative balance", []model.LedgerAccount{model.AccountPaid}, -1, 0, 0, "unresolved"},
		{"empty paid", []model.LedgerAccount{model.AccountPaid}, 0, 100, 0, "unresolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, laneWalletFundingClass(tc.order, tc.paid, tc.bonus, tc.reward))
		})
	}
}

// Exercise the actual billing factory and durable accounting, not a mock
// selector. Observer on/off must have identical funding, debits and errors.
func TestLaneObservationOrdinaryFundingParity(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("TEST_SERVICE_POSTGRES_DSN must name disposable PostgreSQL")
	}
	oldTrust := operation_setting.GetQuotaSetting().TrustQuotaUSD
	operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
	t.Cleanup(func() { operation_setting.GetQuotaSetting().TrustQuotaUSD = oldTrust })
	for _, tc := range []struct {
		name, pref, source, funding                                                             string
		bonus, reward                                                                           int
		eligible, subscription, exhausted, overflow, insufficient, unlimited, smart, otherRoute bool
	}{
		{name: "paid wallet", pref: "wallet_only", source: BillingSourceWallet, funding: "paid_wallet_snapshot"},
		{name: "eligible bonus", pref: "wallet_only", source: BillingSourceWallet, funding: "non_paid_wallet", bonus: 200, reward: 150, eligible: true},
		{name: "ineligible bonus with reward", pref: "wallet_only", source: BillingSourceWallet, funding: "non_paid_wallet", bonus: 200, reward: 150},
		{name: "ineligible bonus without reward", pref: "wallet_only", source: BillingSourceWallet, funding: "paid_wallet_snapshot", bonus: 200},
		{name: "reward wallet", pref: "wallet_only", source: BillingSourceWallet, funding: "non_paid_wallet", reward: 150},
		{name: "subscription only", pref: "subscription_only", source: BillingSourceSubscription, funding: "subscription", subscription: true},
		{name: "wallet first insufficient", pref: "wallet_first", source: BillingSourceSubscription, funding: "subscription", subscription: true, insufficient: true},
		{name: "subscription first available", pref: "subscription_first", source: BillingSourceSubscription, funding: "subscription", subscription: true},
		{name: "subscription first without overflow", pref: "subscription_first", funding: "subscription", subscription: true, exhausted: true},
		{name: "subscription first with overflow", pref: "subscription_first", source: BillingSourceWallet, funding: "paid_wallet_snapshot", subscription: true, exhausted: true, overflow: true},
		{name: "unlimited key", pref: "wallet_only", source: BillingSourceWallet, funding: "paid_wallet_snapshot", unlimited: true},
		{name: "smart route", pref: "wallet_only", source: BillingSourceWallet, funding: "paid_wallet_snapshot", smart: true},
		{name: "unsupported route", pref: "wallet_only", source: BillingSourceWallet, funding: "paid_wallet_snapshot", otherRoute: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, observe := range []bool{false, true} {
				t.Run(map[bool]string{false: "off", true: "on"}[observe], func(t *testing.T) {
					user, token, _ := seedReceiptActors(t, tc.bonus, tc.reward)
					if tc.insufficient {
						user.QuotaPaid, user.Quota = 10, 10+tc.bonus+tc.reward
						require.NoError(t, model.DB.Model(user).Updates(map[string]any{"quota_paid": user.QuotaPaid, "quota": user.Quota}).Error)
					}
					if tc.unlimited {
						require.NoError(t, model.DB.Model(token).Update("unlimited_quota", true).Error)
					}
					name := "lane-observation-" + common.GetRandomString(8)
					seedBonusEligibleModel(t, name, boolPtr(tc.eligible))
					t.Cleanup(func() {
						require.NoError(t, model.DB.Unscoped().Where("model_name = ?", name).Delete(&model.Model{}).Error)
						model.InvalidateModelBonusEligibleCache()
					})
					var sub *model.UserSubscription
					if tc.subscription {
						plan := &model.SubscriptionPlan{Title: name, TotalAmount: 1000, QuotaResetPeriod: model.SubscriptionResetNever}
						require.NoError(t, model.DB.Session(&gorm.Session{SkipHooks: true}).Create(plan).Error)
						t.Cleanup(func() {
							require.NoError(t, model.DB.Session(&gorm.Session{SkipHooks: true}).Delete(plan).Error)
							model.InvalidateSubscriptionPlanCache(plan.Id)
						})
						now := model.GetDBTimestamp()
						sub = &model.UserSubscription{UserId: user.Id, PlanId: plan.Id, AmountTotal: 1000, QuotaPeriod: 1, Status: "active", StartTime: now - 10, EndTime: now + 3600, AllowWalletOverflow: tc.overflow}
						if tc.exhausted {
							sub.AmountUsed = 1000
						}
						require.NoError(t, model.DB.Create(sub).Error)
					}
					c := testGinContext(t)
					path := "/v1/chat/completions"
					if tc.otherRoute {
						path = "/v1/images/generations"
					}
					c.Request = httptest.NewRequest("POST", path, nil)
					c.Set("token_quota", token.RemainQuota)
					common.SetContextKey(c, constant.ContextKeyTokenSmartRouting, tc.smart)
					if observe {
						BeginLaneEligibilityObservation(c)
					}
					info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: name, OriginModelName: name, TokenUnlimited: tc.unlimited, UserSetting: dto.UserSetting{BillingPreference: tc.pref}}
					session, apiErr := NewBillingSession(c, info, 100)
					observation, ok := LaneEligibilityForRequest(c)
					assert.Equal(t, observe, ok)
					if observe {
						assert.Equal(t, "observe_only", observation.Mode)
						assert.Equal(t, "production_bridge_unavailable", observation.Gate)
						assert.Equal(t, tc.pref, observation.Preference)
						assert.Equal(t, tc.funding, observation.Funding)
						assert.Equal(t, map[bool]string{false: "finite", true: "unsupported"}[tc.unlimited], observation.Key)
						assert.Equal(t, map[bool]string{false: "chat_route_family", true: "other"}[tc.smart || tc.otherRoute], observation.Route)
					}
					if tc.source == "" {
						require.Nil(t, session)
						require.NotNil(t, apiErr)
						assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
					} else {
						require.Nil(t, apiErr)
						require.NotNil(t, session)
						assert.Equal(t, tc.source, info.BillingSource)
						require.NoError(t, session.Settle(60))
						require.NoError(t, session.Settle(60), "terminal replay must not debit twice")
					}
					paid, bonus, reward := user.QuotaPaid, tc.bonus, tc.reward
					if tc.source == BillingSourceWallet {
						switch {
						case tc.eligible && bonus > 0:
							bonus -= 60
						case reward > 0:
							reward -= 60
						default:
							paid -= 60
						}
					}
					assertUserBalances(t, user.Id, bonus, paid, reward)
					var after model.Token
					require.NoError(t, model.DB.First(&after, token.Id).Error)
					wantRemain := token.RemainQuota
					// Ordinary billing tracks raw quota on unlimited Keys too;
					// unlimited changes admission, not this existing counter.
					if tc.source != "" {
						wantRemain -= 60
					}
					assert.Equal(t, wantRemain, after.RemainQuota)
					if sub != nil {
						before := sub.AmountUsed
						require.NoError(t, model.DB.First(sub, sub.Id).Error)
						if tc.source == BillingSourceSubscription {
							before += 60
						}
						assert.Equal(t, before, sub.AmountUsed)
					}
				})
			}
		})
	}
}

func TestLaneObservationReadFailureDoesNotSelectAnotherSource(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("disposable PostgreSQL required")
	}
	user, token, _ := seedReceiptActors(t, 0, 0)
	injected := errors.New("synthetic funding lookup failure")
	const callback = "lane-observation-read-failure"
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(injected)
		}
	}))
	defer func() { require.NoError(t, model.DB.Callback().Query().Remove(callback)) }()
	c := testGinContext(t)
	BeginLaneEligibilityObservation(c)
	info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: "lane-read-failure", UserSetting: dto.UserSetting{BillingPreference: "wallet_first"}}
	session, apiErr := NewBillingSession(c, info, 100)
	require.Nil(t, session)
	require.NotNil(t, apiErr)
	assert.ErrorIs(t, apiErr.Err, injected)
	observation, ok := LaneEligibilityForRequest(c)
	require.True(t, ok)
	assert.Equal(t, "unresolved", observation.Funding)
	assert.Equal(t, "production_bridge_unavailable", observation.Gate)
	var owners, ledger int64
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Where("user_id = ?", user.Id).Count(&owners).Error)
	require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Where("user_id = ?", user.Id).Count(&ledger).Error)
	assert.Zero(t, owners)
	assert.Zero(t, ledger)
}

func TestLaneObservationDoesNotRebindOwnedFundingOrExistingKeys(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("disposable PostgreSQL required")
	}
	oldTrust := operation_setting.GetQuotaSetting().TrustQuotaUSD
	operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
	t.Cleanup(func() { operation_setting.GetQuotaSetting().TrustQuotaUSD = oldTrust })
	user, keyA, _ := seedReceiptActors(t, 0, 0)
	keyB := &model.Token{UserId: user.Id, Key: "synthetic-lane-second-" + common.GetRandomString(8), Status: common.TokenStatusEnabled, RemainQuota: 500}
	require.NoError(t, model.DB.Create(keyB).Error)
	t.Cleanup(func() { require.NoError(t, model.DB.Unscoped().Delete(keyB).Error) })
	name := "lane-owned-" + common.GetRandomString(8)
	seedBonusEligibleModel(t, name, boolPtr(false))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Unscoped().Where("model_name = ?", name).Delete(&model.Model{}).Error)
		model.InvalidateModelBonusEligibleCache()
	})
	for i, step := range []struct {
		key    *model.Token
		actual int
	}{{keyA, 60}, {keyB, 20}, {keyA, 40}} {
		c := testGinContext(t)
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		c.Set("token_quota", 2000)
		BeginLaneEligibilityObservation(c)
		info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: step.key.Id, TokenKey: step.key.Key, RequestId: name + common.GetRandomString(8), OriginModelName: name, UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}}
		session, apiErr := NewBillingSession(c, info, 100)
		require.Nil(t, apiErr)
		require.NotNil(t, session)
		observation, ok := LaneEligibilityForRequest(c)
		require.True(t, ok)
		if i == 0 {
			assert.Equal(t, "paid_wallet_snapshot", observation.Funding)
			// Change policy and add reward after ownership. Neither changes the
			// original paid debit/refund identity or the already captured value.
			info.UserSetting.BillingPreference = "subscription_only"
			require.NoError(t, model.DB.Model(user).Updates(map[string]any{"quota_reward": 150, "quota": 1050}).Error)
		} else {
			assert.Equal(t, "non_paid_wallet", observation.Funding)
		}
		require.NoError(t, session.Settle(step.actual))
		require.NoError(t, session.Settle(step.actual))
		assert.Equal(t, BillingSourceWallet, info.BillingSource)
		afterObservation, _ := LaneEligibilityForRequest(c)
		assert.Equal(t, observation, afterObservation)
	}
	assertUserBalances(t, user.Id, 0, 940, 90)
	require.NoError(t, model.DB.First(keyA, keyA.Id).Error)
	require.NoError(t, model.DB.First(keyB, keyB.Id).Error)
	assert.Equal(t, 1900, keyA.RemainQuota)
	assert.Equal(t, 480, keyB.RemainQuota)
	var settled int64
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Where("user_id = ? AND state = ?", user.Id, model.BillingSettled).Count(&settled).Error)
	assert.EqualValues(t, 3, settled, "existing ownership history is retained, not cleared for the next request")
}

func TestLaneObservationWriteFailureDoesNotFallbackOrPartiallyDebit(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("disposable PostgreSQL required")
	}
	baseline, _ := settlementDeadlineSession(t, "lane-write-baseline", true, BillingSourceSubscription)
	before := captureLifecycleAccounting(t)
	c, info := lifecycleRequest(t, baseline, "lane-write-failure")
	info.UserSetting.BillingPreference = "wallet_first"
	BeginLaneEligibilityObservation(c)
	injected, hits, remove := failLifecycleWrite(t, &model.Token{}, false)
	session, apiErr := NewBillingSession(c, info, 100)
	require.Nil(t, session)
	require.NotNil(t, apiErr)
	assert.ErrorIs(t, apiErr.Err, injected)
	assert.EqualValues(t, 1, hits.Load())
	remove()
	assert.Equal(t, before, captureLifecycleAccounting(t), "observation may not cause partial debit or subscription fallback after a write failure")
	observation, ok := LaneEligibilityForRequest(c)
	require.True(t, ok)
	assert.Equal(t, "paid_wallet_snapshot", observation.Funding)
	assert.Equal(t, "production_bridge_unavailable", observation.Gate)
}
