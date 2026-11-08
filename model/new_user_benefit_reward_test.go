package model

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func paymentIdentityBenefitOrder(t *testing.T, config *NewUserBenefitConfig, id int) *NewUserBenefitOrder {
	t.Helper()
	createNewUserBenefitTestUser(t, id)
	risk := newUserBenefitTestRisk(id)
	risk.PaymentGateway = "https://pay.example.test"
	order, err := ReserveNewUserBenefit(t.Context(), id, config.Version, "alipay", "benefit-merchant", fmt.Sprintf("IDENTITY%d", id), risk)
	require.NoError(t, err)
	return order
}

func TestNewUserBenefitIndependentAdmissionPoliciesPostgres(t *testing.T) {
	for _, disabled := range []string{"email", "turnstile", "network", "all"} {
		t.Run(disabled, func(t *testing.T) {
			config := newUserBenefitFixture(t)
			next := *config
			next.RequireVerifiedEmail = disabled != "email" && disabled != "all"
			next.RequireTurnstile = disabled != "turnstile" && disabled != "all"
			next.NetworkLimitEnabled = disabled != "network" && disabled != "all"
			config, err := UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
			require.NoError(t, err)
			createNewUserBenefitTestUser(t, 1)
			risk := newUserBenefitTestRisk(1)
			if !config.RequireVerifiedEmail {
				require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Updates(map[string]any{"email": "", "email_verified_at": 0}).Error)
			}
			if !config.RequireTurnstile {
				risk.HumanVerified = false
			}
			if !config.NetworkLimitEnabled {
				risk.ClientIP = "invalid-network"
			}
			order, err := ReserveNewUserBenefit(t.Context(), 1, config.Version, "alipay", "benefit-merchant", "SWITCHES", risk)
			require.NoError(t, err)
			assert.Equal(t, config.RequireVerifiedEmail, order.RequireVerifiedEmail)
			assert.Equal(t, config.RequireTurnstile, order.RequireTurnstile)
			assert.Equal(t, config.NetworkLimitEnabled, order.NetworkLimitEnabled)
			assert.Equal(t, config.RequireVerifiedEmail, order.EmailIdentityHash != nil)
			assert.Equal(t, config.NetworkLimitEnabled, order.NetworkHash != nil)
			if !config.RequireVerifiedEmail {
				next.RequireVerifiedEmail = true
			}
			if !config.RequireTurnstile {
				next.RequireTurnstile = true
			}
			if !config.NetworkLimitEnabled {
				next.NetworkLimitEnabled = true
			}
			_, err = UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
			require.NoError(t, err)
			resumed, err := ReserveNewUserBenefit(t.Context(), 1, config.Version, "alipay", "benefit-merchant", "REPLAY", risk)
			require.NoError(t, err)
			assert.Equal(t, order, resumed, "policy changes preserve the existing promise")
		})
	}
}

func TestNewUserBenefitPaymentIdentityPrincipalAndRecoveryPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	legacy := reserveNewUserBenefitTestOrder(t, config, 1)
	next := *config
	next.PaymentIdentityCheckEnabled = true
	config, err := UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
	require.NoError(t, err)
	order := paymentIdentityBenefitOrder(t, config, 2)
	_, err = RechargeEpay(legacy.TradeNo, "alipay", "0.10", "", newUserBenefitTestFacts(legacy))
	require.NoError(t, err)
	assert.Equal(t, 45000, getUserBalances(t, 1).QuotaBonus, "new rules cannot hold a previously promised reward")
	for range 2 {
		_, err = RechargeEpay(order.TradeNo, "alipay", "0.10", "", newUserBenefitTestFacts(order))
		require.NoError(t, err)
	}
	user := getUserBalances(t, 2)
	assert.Equal(t, 5000, user.QuotaPaid)
	assert.Zero(t, user.QuotaBonus)
	assertQuotaInvariant(t, user)
	state, err := GetNewUserBenefitState(t.Context(), 2)
	require.NoError(t, err)
	assert.Equal(t, NewUserBenefitStatusVerificationPending, state.Status)
	assert.Equal(t, "payment_identity_pending", state.BlockedReason)
	assert.False(t, state.Eligible)
	_, err = ReserveNewUserBenefit(t.Context(), 2, config.Version, "alipay", "benefit-merchant", "NOSECOND", newUserBenefitTestRisk(2))
	require.ErrorIs(t, err, ErrNewUserBenefitClaimed)
	for _, reason := range []string{"query_unavailable", "order_mismatch", "identity_unavailable"} {
		require.NoError(t, FinalizeNewUserBenefitReward(t.Context(), order.TradeNo, "", reason, VerifiedPaymentFacts{}))
		state, err = GetNewUserBenefitState(t.Context(), 2)
		require.NoError(t, err)
		assert.Equal(t, reason, state.Order.RewardReason)
		assert.Equal(t, user, getUserBalances(t, 2))
	}
	// A disabling administrator edit cannot release the captured check.
	next.PaymentIdentityCheckEnabled = false
	_, err = UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
	require.NoError(t, err)
	for _, badBuyer := range []string{"", "2088********1234", "buyer@example.test", "oWechatOpenId", "20880000000000001"} {
		require.ErrorIs(t, FinalizeNewUserBenefitReward(t.Context(), order.TradeNo, badBuyer, "", newUserBenefitTestFacts(order)), ErrNewUserBenefitPaymentRequired)
	}
	badFacts := newUserBenefitTestFacts(order)
	badFacts.ExternalReference = "different-financial-payment"
	require.ErrorIs(t, FinalizeNewUserBenefitReward(t.Context(), order.TradeNo, "2088000000000001", "", badFacts), ErrNewUserBenefitPaymentRequired)
	require.NoError(t, FinalizeNewUserBenefitReward(t.Context(), order.TradeNo, "2088000000000001", "", newUserBenefitTestFacts(order)))
	state, err = GetNewUserBenefitState(t.Context(), 2)
	require.NoError(t, err)
	assert.Equal(t, NewUserBenefitStatusClaimed, state.Status)
	assert.Equal(t, 45000, getUserBalances(t, 2).QuotaBonus)
	assert.EqualValues(t, 1, benefitTableCount(t, &NewUserBenefitPaymentIdentity{}))
	for range 2 {
		require.NoError(t, FinalizeNewUserBenefitReward(t.Context(), order.TradeNo, "", "query_unavailable", VerifiedPaymentFacts{}))
		_, err = RechargeEpay(order.TradeNo, "alipay", "0.10", "", newUserBenefitTestFacts(order))
		require.NoError(t, err)
	}
	assert.Equal(t, 50000, getUserBalances(t, 2).Quota)
	assert.EqualValues(t, 2, benefitTableCount(t, &PaymentReceipt{}))
	assert.EqualValues(t, 3, benefitTableCount(t, &BalanceLedgerOperation{}))
	assert.EqualValues(t, 4, benefitTableCount(t, &BalanceLedger{}))
}

func TestNewUserBenefitSamePayerConcurrentAccountsPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	next := *config
	next.PaymentIdentityCheckEnabled = true
	config, err := UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
	require.NoError(t, err)
	orders := make([]*NewUserBenefitOrder, 4)
	for i := range orders {
		orders[i] = paymentIdentityBenefitOrder(t, config, i+1)
		_, err = RechargeEpay(orders[i].TradeNo, "alipay", "0.10", "", newUserBenefitTestFacts(orders[i]))
		require.NoError(t, err)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for _, order := range orders {
		for range 2 {
			workers.Go(func() {
				<-start
				results <- FinalizeNewUserBenefitReward(t.Context(), order.TradeNo, "2088000000000010", "", newUserBenefitTestFacts(order))
			})
		}
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	granted, denied := 0, 0
	for _, order := range orders {
		state, err := GetNewUserBenefitState(t.Context(), order.UserID)
		require.NoError(t, err)
		user := getUserBalances(t, order.UserID)
		assert.Equal(t, 5000, user.QuotaPaid)
		assertQuotaInvariant(t, user)
		if state.Status == NewUserBenefitStatusClaimed {
			granted++
			assert.Equal(t, 45000, user.QuotaBonus)
		} else {
			denied++
			assert.Equal(t, NewUserBenefitStatusRewardDenied, state.Status)
			assert.Equal(t, "payment_identity_used", state.BlockedReason)
			assert.Zero(t, user.QuotaBonus)
		}
	}
	assert.Equal(t, 1, granted)
	assert.Equal(t, 3, denied)
	assert.EqualValues(t, 4, benefitTableCount(t, &PaymentReceipt{}))
	assert.EqualValues(t, 5, benefitTableCount(t, &BalanceLedger{}))
	assert.EqualValues(t, 1, benefitTableCount(t, &BonusSourceLot{}))
	assert.EqualValues(t, 1, benefitTableCount(t, &NewUserBenefitPaymentIdentity{}))
}

func TestNewUserBenefitRewardFailureKeepsPrincipalAndCanRecoverPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	next := *config
	next.PaymentIdentityCheckEnabled = true
	config, err := UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
	require.NoError(t, err)
	order := paymentIdentityBenefitOrder(t, config, 1)
	_, err = RechargeEpay(order.TradeNo, "alipay", "0.10", "", newUserBenefitTestFacts(order))
	require.NoError(t, err)
	injected := errors.New("injected reward provenance failure")
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register("benefit_reward_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "bonus_source_states" {
			tx.AddError(injected)
		}
	}))
	err = FinalizeNewUserBenefitReward(t.Context(), order.TradeNo, "2088000000000001", "", newUserBenefitTestFacts(order))
	require.ErrorIs(t, err, injected)
	require.NoError(t, DB.Callback().Create().Remove("benefit_reward_failure"))
	assert.Equal(t, 5000, getUserBalances(t, 1).QuotaPaid)
	assert.Zero(t, getUserBalances(t, 1).QuotaBonus)
	assert.Zero(t, benefitTableCount(t, &NewUserBenefitPaymentIdentity{}))
	assert.EqualValues(t, 1, benefitTableCount(t, &PaymentReceipt{}))
	assert.EqualValues(t, 1, benefitTableCount(t, &BalanceLedger{}))
	assert.Equal(t, common.TopUpStatusSuccess, GetTopUpByTradeNo(order.TradeNo).Status)
	state, err := GetNewUserBenefitState(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, NewUserBenefitStatusVerificationPending, state.Status)
	require.NoError(t, FinalizeNewUserBenefitReward(t.Context(), order.TradeNo, "2088000000000001", "", newUserBenefitTestFacts(order)))
	assert.Equal(t, 50000, getUserBalances(t, 1).Quota)
	assert.EqualValues(t, 1, benefitTableCount(t, &NewUserBenefitPaymentIdentity{}))
}

// These are the released schema before independently configurable controls.
// Keeping their shape explicit catches a populated-table upgrade, including the
// nullable identity index, rather than just recreating the latest fresh schema.
type newUserBenefitPrePolicyConfig struct {
	Legacy                 newUserBenefitPreRiskConfig `gorm:"embedded"`
	MaxTotalOrders         int64                       `gorm:"not null;default:1000"`
	MaxOrdersPer24h        int64                       `gorm:"column:max_orders_per_24h;not null;default:100"`
	MaxOrdersPerNetwork24h int64                       `gorm:"column:max_orders_per_network_24h;not null;default:3"`
	TotalBonusBudgetCents  int64                       `gorm:"not null;default:90000"`
}

type newUserBenefitPrePolicyOrder struct {
	Legacy            newUserBenefitPreRiskOrder `gorm:"embedded"`
	EmailIdentityHash *string                    `gorm:"type:varchar(64);uniqueIndex"`
	NetworkHash       *string                    `gorm:"type:varchar(64);index:idx_new_user_benefit_network_time,priority:1"`
}

func TestNewUserBenefitPolicyUpgradeAndRepeatedMigrationPostgres(t *testing.T) {
	newUserBenefitFixture(t)
	require.NoError(t, DB.Migrator().DropTable(&NewUserBenefitOrder{}, &NewUserBenefitConfigAudit{}, &NewUserBenefitConfig{}, &NewUserBenefitReward{}, &NewUserBenefitPaymentIdentity{}))
	require.NoError(t, DB.Table("new_user_benefit_configs").AutoMigrate(&newUserBenefitPrePolicyConfig{}))
	require.NoError(t, DB.Table("new_user_benefit_config_audits").AutoMigrate(&newUserBenefitPreRiskAudit{}))
	require.NoError(t, DB.Table("new_user_benefit_orders").AutoMigrate(&newUserBenefitPrePolicyOrder{}))
	now := common.GetTimestamp()
	oldConfig := newUserBenefitPrePolicyConfig{Legacy: newUserBenefitPreRiskConfig{ID: 1, Enabled: true, PayAmountCents: 10, CreditAmountCents: 100, Version: 9, UpdatedBy: 7, UpdatedAt: now}, MaxTotalOrders: 20, MaxOrdersPer24h: 10, MaxOrdersPerNetwork24h: 2, TotalBonusBudgetCents: 1800}
	require.NoError(t, DB.Table("new_user_benefit_configs").Create(&oldConfig).Error)
	require.NoError(t, DB.Table("new_user_benefit_config_audits").Create(&newUserBenefitPreRiskAudit{Version: 9, Enabled: true, PayAmountCents: 10, CreditAmountCents: 100, ActorID: 7, CreatedAt: now}).Error)
	createNewUserBenefitTestUser(t, 1)
	hash := newUserBenefitDigest("email", "benefit-user-1@example.invalid")
	oldOrder := newUserBenefitPrePolicyOrder{Legacy: newUserBenefitPreRiskOrder{UserID: 1, TradeNo: "PREPOLICY", PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay", MerchantScope: "benefit-merchant", PayAmountCents: 10, CreditAmountCents: 100, ConfigVersion: 9, USDExchangeRate: "10", QuotaPerUnit: "500000", PaidQuota: 5000, BonusQuota: 45000, CreatedAt: now}, EmailIdentityHash: &hash}
	require.NoError(t, DB.Table("new_user_benefit_orders").Create(&oldOrder).Error)
	require.NoError(t, DB.Create(&TopUp{NewUserBenefit: true, UserId: 1, Money: 0.1, TradeNo: "PREPOLICY", PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending, CreateTime: now}).Error)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&NewUserBenefitConfig{}, &NewUserBenefitConfigAudit{}, &NewUserBenefitOrder{}, &NewUserBenefitReward{}, &NewUserBenefitPaymentIdentity{}))
	}
	config, err := GetNewUserBenefitConfig(t.Context())
	require.NoError(t, err)
	assert.True(t, config.RequireVerifiedEmail)
	assert.True(t, config.RequireTurnstile)
	assert.True(t, config.NetworkLimitEnabled)
	assert.False(t, config.PaymentIdentityCheckEnabled)
	assert.EqualValues(t, 9, config.Version)
	assert.EqualValues(t, 20, config.MaxTotalOrders)
	assert.EqualValues(t, 1800, config.TotalBonusBudgetCents)
	next := *config
	next.RequireVerifiedEmail, next.RequireTurnstile, next.NetworkLimitEnabled, next.PaymentIdentityCheckEnabled = false, false, false, true
	config, err = UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&NewUserBenefitConfig{}, &NewUserBenefitConfigAudit{}, &NewUserBenefitOrder{}, &NewUserBenefitReward{}, &NewUserBenefitPaymentIdentity{}))
	}
	reloaded, err := GetNewUserBenefitConfig(t.Context())
	require.NoError(t, err)
	assert.Equal(t, config, reloaded, "restart must not re-enable explicitly disabled rules")
	state, err := GetNewUserBenefitState(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, &hash, state.Order.EmailIdentityHash)
	assert.True(t, state.Order.RequireVerifiedEmail)
	assert.False(t, state.Order.PaymentIdentityCheckEnabled, "old promise must not acquire a post-payment condition")
	duplicate := *state.Order
	duplicate.UserID, duplicate.TradeNo = 2, "DUPLICATEEMAIL"
	require.Error(t, DB.Create(&duplicate).Error, "migration must preserve the lifetime email uniqueness index")
	_, err = RechargeEpay(state.Order.TradeNo, "alipay", "0.10", "", newUserBenefitTestFacts(state.Order))
	require.NoError(t, err)
	assert.Equal(t, 50000, getUserBalances(t, 1).Quota)
	assert.Zero(t, benefitTableCount(t, &NewUserBenefitReward{}))
	var audit NewUserBenefitConfigAudit
	require.NoError(t, DB.Where("version = ?", config.Version).First(&audit).Error)
	assert.False(t, audit.RequireVerifiedEmail)
	assert.False(t, audit.RequireTurnstile)
	assert.False(t, audit.NetworkLimitEnabled)
	assert.True(t, audit.PaymentIdentityCheckEnabled)
	assert.Equal(t, 1, audit.RiskPolicyVersion)
}
