package model

import (
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setNewUserBenefitTestEmail(t *testing.T, userID int, email string) {
	t.Helper()
	require.NoError(t, DB.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{
		"email": email, "email_verified_address": NormalizeEmail(email),
		"email_verification_version": 1, "email_verified_at": common.GetTimestamp(),
	}).Error)
}

func TestNewUserBenefitRequiresCurrentEmailProofAndServerRiskPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	for index, mutation := range []map[string]any{
		{"email": ""},
		{"email_verification_version": 0},
		{"email_verified_at": 0},
		{"email_verified_address": "different@example.invalid"},
		{"email": "changed@example.invalid"},
	} {
		id := index + 1
		createNewUserBenefitTestUser(t, id)
		require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Updates(mutation).Error)
		state, err := GetNewUserBenefitState(t.Context(), id)
		require.NoError(t, err)
		assert.False(t, state.Eligible)
		assert.Equal(t, "email_verification_required", state.BlockedReason)
		_, err = ReserveNewUserBenefit(t.Context(), id, config.Version, "alipay", "benefit-merchant", fmt.Sprintf("UNVERIFIED%d", id), newUserBenefitTestRisk(id))
		require.ErrorIs(t, err, ErrNewUserBenefitEmailRequired)
	}
	createNewUserBenefitTestUser(t, 100)
	risk := newUserBenefitTestRisk(100)
	risk.HumanVerified = false
	_, err := ReserveNewUserBenefit(t.Context(), 100, config.Version, "alipay", "benefit-merchant", "NOHUMANPROOF", risk)
	require.ErrorIs(t, err, ErrNewUserBenefitVerificationRequired)
	for _, ip := range []string{"", "unknown", "203.0.113.1:443", "203.0.113.1, 203.0.113.2", " 203.0.113.1", "fe80::1%eth0", "0.0.0.0", "::", "::ffff:0.0.0.0", "224.0.0.1", "ff02::1", "::ffff:224.0.0.1"} {
		risk = NewUserBenefitRiskContext{ClientIP: ip, HumanVerified: true}
		_, err = ReserveNewUserBenefit(t.Context(), 100, config.Version, "alipay", "benefit-merchant", "INVALIDNETWORK", risk)
		require.ErrorIs(t, err, ErrNewUserBenefitInvalidNetwork, "client IP %q", ip)
	}
	usage, err := GetNewUserBenefitUsage(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &NewUserBenefitUsage{}, usage)
	assert.Zero(t, benefitTableCount(t, &TopUp{}))
}

func TestNewUserBenefitIdentitySurvivesRebindingAndDeletionPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	createNewUserBenefitTestUser(t, 1)
	createNewUserBenefitTestUser(t, 2)
	setNewUserBenefitTestEmail(t, 1, " First@Example.invalid ")
	setNewUserBenefitTestEmail(t, 2, "first@example.invalid")
	order, err := ReserveNewUserBenefit(t.Context(), 1, config.Version, "alipay", "benefit-merchant", "EMAILOWNER", newUserBenefitTestRisk(1))
	require.NoError(t, err)
	require.NotNil(t, order.EmailIdentityHash)
	assert.Len(t, *order.EmailIdentityHash, 64)
	_, err = ReserveNewUserBenefit(t.Context(), 2, config.Version, "alipay", "benefit-merchant", "EMAILDUPLICATE", newUserBenefitTestRisk(2))
	require.ErrorIs(t, err, ErrNewUserBenefitIdentityUsed)
	state, err := GetNewUserBenefitState(t.Context(), 2)
	require.NoError(t, err)
	assert.False(t, state.Eligible)
	assert.Equal(t, "identity_already_used", state.BlockedReason)

	setNewUserBenefitTestEmail(t, 1, "replacement@example.invalid")
	resumed, err := ReserveNewUserBenefit(t.Context(), 1, config.Version, "alipay", "benefit-merchant", "REBINDRETRY", NewUserBenefitRiskContext{})
	require.NoError(t, err)
	assert.Equal(t, order, resumed)
	_, err = ReserveNewUserBenefit(t.Context(), 2, config.Version, "alipay", "benefit-merchant", "AFTERREBIND", newUserBenefitTestRisk(2))
	require.ErrorIs(t, err, ErrNewUserBenefitIdentityUsed)

	// The immutable claim remains even if the owning user row is later removed.
	require.NoError(t, DB.Unscoped().Delete(&User{}, 1).Error)
	_, err = ReserveNewUserBenefit(t.Context(), 2, config.Version, "alipay", "benefit-merchant", "AFTERDELETE", newUserBenefitTestRisk(2))
	require.ErrorIs(t, err, ErrNewUserBenefitIdentityUsed)
	assert.EqualValues(t, 1, benefitTableCount(t, &NewUserBenefitOrder{}))
	assert.EqualValues(t, 1, benefitTableCount(t, &TopUp{}))
}

func TestNewUserBenefitPersonalGmailAliasesAndCustomDomainsPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	emails := []string{"John.Smith+offer@gmail.com", "johnsmith@googlemail.com", "j.o.h.n.s.m.i.t.h+again@googlemail.com", "foo.bar@company.test", "foobar@company.test"}
	for index, email := range emails {
		id := index + 1
		createNewUserBenefitTestUser(t, id)
		setNewUserBenefitTestEmail(t, id, email)
		_, err := ReserveNewUserBenefit(t.Context(), id, config.Version, "alipay", "benefit-merchant", fmt.Sprintf("EMAILALIAS%d", id), newUserBenefitTestRisk(id))
		if id == 2 || id == 3 {
			require.ErrorIs(t, err, ErrNewUserBenefitIdentityUsed)
		} else {
			require.NoError(t, err)
		}
		var user User
		require.NoError(t, DB.First(&user, id).Error)
		assert.Equal(t, email, user.Email, "benefit identity rules must not rewrite login/profile addresses")
	}
	assert.EqualValues(t, 3, benefitTableCount(t, &NewUserBenefitOrder{}))
}

func TestNewUserBenefitNetworkLimitsCanonicalizeIPv4AndIPv6Postgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	next := *config
	next.MaxOrdersPerNetwork24h = 1
	config, err := UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
	require.NoError(t, err)
	for index, test := range []struct {
		ip      string
		blocked bool
	}{
		{"203.0.113.7", false},
		{"::ffff:203.0.113.7", true},
		{"203.0.113.8", false},
		{"2001:db8:10:20::1", false},
		{"2001:0db8:0010:0020:ffff::2", true},
		{"2001:db8:10:21::1", false},
	} {
		id := index + 1
		createNewUserBenefitTestUser(t, id)
		_, err := ReserveNewUserBenefit(t.Context(), id, config.Version, "alipay", "benefit-merchant", fmt.Sprintf("NETWORK%d", id), NewUserBenefitRiskContext{ClientIP: test.ip, HumanVerified: true})
		if test.blocked {
			require.ErrorIs(t, err, ErrNewUserBenefitLimitReached, "client IP %s", test.ip)
		} else {
			require.NoError(t, err, "client IP %s", test.ip)
		}
	}
	usage, err := GetNewUserBenefitUsage(t.Context())
	require.NoError(t, err)
	assert.EqualValues(t, 4, usage.TotalOrders)
	assert.EqualValues(t, 360, usage.TotalBonusCents)
	assert.EqualValues(t, 4, usage.Orders24h)
}

func TestNewUserBenefitConcurrentCrossAccountAdmissionPostgres(t *testing.T) {
	for _, kind := range []string{"total_orders", "rolling_24h", "bonus_budget", "network", "email_identity"} {
		t.Run(kind, func(t *testing.T) {
			config := newUserBenefitFixture(t)
			next := *config
			wantOrders := 2
			wantError := ErrNewUserBenefitLimitReached
			switch kind {
			case "total_orders":
				next.MaxTotalOrders = 2
			case "rolling_24h":
				next.MaxOrdersPer24h = 2
			case "bonus_budget":
				next.TotalBonusBudgetCents = 180
			case "network":
				next.MaxOrdersPerNetwork24h = 2
			case "email_identity":
				wantOrders, wantError = 1, ErrNewUserBenefitIdentityUsed
			}
			config, err := UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
			require.NoError(t, err)
			for id := 1; id <= 8; id++ {
				createNewUserBenefitTestUser(t, id)
				if kind == "email_identity" {
					domain := "gmail.com"
					if id%2 == 0 {
						domain = "googlemail.com"
					}
					setNewUserBenefitTestEmail(t, id, fmt.Sprintf("same.person+%d@%s", id, domain))
				}
			}
			start := make(chan struct{})
			results := make(chan error, 8)
			var workers sync.WaitGroup
			for index := range 8 {
				workers.Go(func() {
					<-start
					id := index + 1
					risk := newUserBenefitTestRisk(id)
					if kind == "network" {
						risk.ClientIP = "203.0.113.7"
					}
					_, err := ReserveNewUserBenefit(t.Context(), id, config.Version, "alipay", "benefit-merchant", fmt.Sprintf("LIMITRACE%d", id), risk)
					results <- err
				})
			}
			close(start)
			workers.Wait()
			close(results)
			admitted, rejected := 0, 0
			for err := range results {
				if err == nil {
					admitted++
				} else {
					require.ErrorIs(t, err, wantError)
					rejected++
				}
			}
			assert.Equal(t, wantOrders, admitted)
			assert.Equal(t, 8-wantOrders, rejected)
			usage, err := GetNewUserBenefitUsage(t.Context())
			require.NoError(t, err)
			assert.EqualValues(t, wantOrders, usage.TotalOrders)
			assert.EqualValues(t, 90*wantOrders, usage.TotalBonusCents)
			assert.EqualValues(t, wantOrders, usage.Orders24h)
			assert.EqualValues(t, wantOrders, benefitTableCount(t, &TopUp{}))
			assert.Zero(t, benefitTableCount(t, &BalanceLedger{}))
		})
	}
}

func TestNewUserBenefitBudgetSurvivesWindowsRepricingAndDisablePostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	next := *config
	next.MaxTotalOrders, next.MaxOrdersPer24h, next.TotalBonusBudgetCents = 3, 1, 180
	config, err := UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
	require.NoError(t, err)
	first := reserveNewUserBenefitTestOrder(t, config, 1)
	createNewUserBenefitTestUser(t, 2)
	_, err = ReserveNewUserBenefit(t.Context(), 2, config.Version, "alipay", "benefit-merchant", "SAMEWINDOW", newUserBenefitTestRisk(2))
	require.ErrorIs(t, err, ErrNewUserBenefitLimitReached)
	// Age the test-owned promise without changing its pending payment status.
	require.NoError(t, DB.Table("new_user_benefit_orders").Where("user_id = ?", 1).UpdateColumn("created_at", common.GetTimestamp()-25*60*60).Error)
	usage, err := GetNewUserBenefitUsage(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &NewUserBenefitUsage{TotalOrders: 1, TotalBonusCents: 90, Orders24h: 0}, usage)
	second, err := ReserveNewUserBenefit(t.Context(), 2, config.Version, "alipay", "benefit-merchant", "NEXTWINDOW", newUserBenefitTestRisk(2))
	require.NoError(t, err)
	createNewUserBenefitTestUser(t, 3)
	next = *config
	next.CreditAmountCents, next.MaxOrdersPer24h, next.TotalBonusBudgetCents = 20, 3, 189
	config, err = UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
	require.NoError(t, err)
	_, err = ReserveNewUserBenefit(t.Context(), 3, config.Version, "alipay", "benefit-merchant", "ONECENTSHORT", newUserBenefitTestRisk(3))
	require.ErrorIs(t, err, ErrNewUserBenefitLimitReached)
	state, err := GetNewUserBenefitState(t.Context(), 3)
	require.NoError(t, err)
	assert.Equal(t, "limit_reached", state.BlockedReason)
	next = *config
	next.TotalBonusBudgetCents = 190
	config, err = UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
	require.NoError(t, err)
	third, err := ReserveNewUserBenefit(t.Context(), 3, config.Version, "alipay", "benefit-merchant", "EXACTBUDGET", newUserBenefitTestRisk(3))
	require.NoError(t, err)
	assert.EqualValues(t, 20, third.CreditAmountCents)
	usage, err = GetNewUserBenefitUsage(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &NewUserBenefitUsage{TotalOrders: 3, TotalBonusCents: 190, Orders24h: 2}, usage)

	// Lowering or disabling blocks new commitments without revoking prior ones.
	next = *config
	next.Enabled, next.MaxTotalOrders, next.TotalBonusBudgetCents = false, 1, 1
	config, err = UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Updates(map[string]any{"email": "changed@example.invalid", "email_verified_at": 0}).Error)
	resumed, err := ReserveNewUserBenefit(t.Context(), 2, 0, "alipay", "benefit-merchant", "KEEPOLDPROMISE", NewUserBenefitRiskContext{})
	require.NoError(t, err)
	assert.Equal(t, second, resumed)
	state, err = GetNewUserBenefitState(t.Context(), 2)
	require.NoError(t, err)
	assert.Equal(t, NewUserBenefitStatusPending, state.Status)
	assert.True(t, state.Eligible)
	assert.Empty(t, state.BlockedReason)
	_, err = RechargeEpay(first.TradeNo, "alipay", "0.10", "127.0.0.1", newUserBenefitTestFacts(first))
	require.NoError(t, err)
	_, err = RechargeEpay(second.TradeNo, "alipay", "0.10", "127.0.0.1", newUserBenefitTestFacts(second))
	require.NoError(t, err)
	assert.Equal(t, 45000, getUserBalances(t, 1).QuotaBonus)
	assert.Equal(t, 45000, getUserBalances(t, 2).QuotaBonus)
	next = *config
	next.Enabled = true
	config, err = UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, next)
	require.NoError(t, err)
	createNewUserBenefitTestUser(t, 4)
	_, err = ReserveNewUserBenefit(t.Context(), 4, config.Version, "alipay", "benefit-merchant", "REENABLEDOESNOTRESET", newUserBenefitTestRisk(4))
	require.ErrorIs(t, err, ErrNewUserBenefitLimitReached)
	assert.EqualValues(t, 3, benefitTableCount(t, &NewUserBenefitOrder{}))
}

// These independent shapes match the published feature before risk admission
// fields existed. They exercise ALTER TABLE against real existing promises.
type newUserBenefitPreRiskConfig struct {
	ID                int   `gorm:"primaryKey;autoIncrement:false"`
	Enabled           bool  `gorm:"not null"`
	PayAmountCents    int64 `gorm:"not null"`
	CreditAmountCents int64 `gorm:"not null"`
	Version           int64 `gorm:"not null"`
	UpdatedAt         int64 `gorm:"not null"`
	UpdatedBy         int   `gorm:"not null"`
}

type newUserBenefitPreRiskAudit struct {
	ID                int64 `gorm:"primaryKey"`
	Version           int64 `gorm:"not null;uniqueIndex"`
	Enabled           bool  `gorm:"not null"`
	PayAmountCents    int64 `gorm:"not null"`
	CreditAmountCents int64 `gorm:"not null"`
	ActorID           int   `gorm:"not null"`
	CreatedAt         int64 `gorm:"not null"`
}

type newUserBenefitPreRiskOrder struct {
	UserID            int    `gorm:"primaryKey;autoIncrement:false"`
	TradeNo           string `gorm:"type:varchar(32);not null;uniqueIndex"`
	PaymentProvider   string `gorm:"type:varchar(32);not null"`
	PaymentMethod     string `gorm:"type:varchar(50);not null"`
	MerchantScope     string `gorm:"type:varchar(128);not null"`
	PayAmountCents    int64  `gorm:"not null"`
	CreditAmountCents int64  `gorm:"not null"`
	ConfigVersion     int64  `gorm:"not null"`
	USDExchangeRate   string `gorm:"type:varchar(64);not null"`
	QuotaPerUnit      string `gorm:"type:varchar(64);not null"`
	PaidQuota         int    `gorm:"type:bigint;not null"`
	BonusQuota        int    `gorm:"type:bigint;not null"`
	CreatedAt         int64  `gorm:"not null"`
}

func TestNewUserBenefitRiskUpgradePreservesHistoricalPromisesPostgres(t *testing.T) {
	newUserBenefitFixture(t)
	require.NoError(t, DB.Migrator().DropTable(&NewUserBenefitOrder{}, &NewUserBenefitConfigAudit{}, &NewUserBenefitConfig{}))
	require.NoError(t, DB.Table("new_user_benefit_configs").AutoMigrate(&newUserBenefitPreRiskConfig{}))
	require.NoError(t, DB.Table("new_user_benefit_config_audits").AutoMigrate(&newUserBenefitPreRiskAudit{}))
	require.NoError(t, DB.Table("new_user_benefit_orders").AutoMigrate(&newUserBenefitPreRiskOrder{}))
	now := common.GetTimestamp()
	oldConfig := newUserBenefitPreRiskConfig{ID: 1, Enabled: true, PayAmountCents: 10, CreditAmountCents: 100, Version: 7, UpdatedBy: 9, UpdatedAt: now - 60}
	require.NoError(t, DB.Table("new_user_benefit_configs").Create(&oldConfig).Error)
	require.NoError(t, DB.Table("new_user_benefit_config_audits").Create(&newUserBenefitPreRiskAudit{Version: 7, Enabled: true, PayAmountCents: 10, CreditAmountCents: 100, ActorID: 9, CreatedAt: now - 60}).Error)
	for id := 1; id <= 2; id++ {
		createNewUserBenefitTestUser(t, id)
		oldOrder := newUserBenefitPreRiskOrder{UserID: id, TradeNo: fmt.Sprintf("BEFORERISK%d", id), PaymentProvider: PaymentProviderEpay,
			PaymentMethod: "alipay", MerchantScope: "benefit-merchant", PayAmountCents: 10, CreditAmountCents: 100,
			ConfigVersion: 7, USDExchangeRate: "10", QuotaPerUnit: "500000", PaidQuota: 5000, BonusQuota: 45000, CreatedAt: now - 30}
		require.NoError(t, DB.Table("new_user_benefit_orders").Create(&oldOrder).Error)
		require.NoError(t, DB.Create(&TopUp{NewUserBenefit: true, UserId: id, Money: 0.1, TradeNo: oldOrder.TradeNo,
			PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, CreateTime: now - 30, Status: common.TopUpStatusPending}).Error)
	}
	setNewUserBenefitTestEmail(t, 1, "Legacy.Person+first@googlemail.com")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("email_verified_at", 0).Error)
	priorPaid, err := getNewUserBenefitOrderTx(DB, 2)
	require.NoError(t, err)
	_, err = RechargeEpay(priorPaid.TradeNo, "alipay", "0.10", "127.0.0.1", newUserBenefitTestFacts(priorPaid))
	require.NoError(t, err)
	beforeWallet := getUserBalances(t, 2)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&NewUserBenefitConfig{}, &NewUserBenefitConfigAudit{}, &NewUserBenefitOrder{}))
	}
	config, err := GetNewUserBenefitConfig(t.Context())
	require.NoError(t, err)
	assert.True(t, config.Enabled)
	assert.EqualValues(t, 7, config.Version)
	assert.EqualValues(t, 1000, config.MaxTotalOrders)
	assert.EqualValues(t, 100, config.MaxOrdersPer24h)
	assert.EqualValues(t, 3, config.MaxOrdersPerNetwork24h)
	assert.EqualValues(t, 90000, config.TotalBonusBudgetCents)
	assert.Equal(t, oldConfig.UpdatedAt, config.UpdatedAt)
	var oldAudit NewUserBenefitConfigAudit
	require.NoError(t, DB.Where("version = ?", 7).First(&oldAudit).Error)
	assert.Zero(t, oldAudit.MaxTotalOrders, "historical audit must not falsely claim that new controls already existed")
	assert.EqualValues(t, 9, oldAudit.ActorID)
	assert.Equal(t, beforeWallet, getUserBalances(t, 2))
	replayed, err := RechargeEpay(priorPaid.TradeNo, "alipay", "0.10", "127.0.0.1", newUserBenefitTestFacts(priorPaid))
	require.NoError(t, err)
	assert.True(t, replayed)
	assert.Equal(t, beforeWallet, getUserBalances(t, 2))
	resumed, err := ReserveNewUserBenefit(t.Context(), 1, 0, "alipay", "benefit-merchant", "RESUMEOLD", NewUserBenefitRiskContext{})
	require.NoError(t, err)
	assert.Equal(t, "BEFORERISK1", resumed.TradeNo)
	assert.Nil(t, resumed.EmailIdentityHash)
	assert.Nil(t, resumed.NetworkHash)
	state, err := GetNewUserBenefitState(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, NewUserBenefitStatusPending, state.Status)
	assert.Empty(t, state.BlockedReason)
	_, err = RechargeEpay(resumed.TradeNo, "alipay", "0.10", "127.0.0.1", newUserBenefitTestFacts(resumed))
	require.NoError(t, err)
	assert.Equal(t, 45000, getUserBalances(t, 1).QuotaBonus)

	// Legacy accounts remain a conservative identity source even after soft
	// deletion; personal Gmail aliases must not bypass the missing old hash.
	require.NoError(t, DB.Delete(&User{}, 1).Error)
	createNewUserBenefitTestUser(t, 3)
	setNewUserBenefitTestEmail(t, 3, "legacyperson@gmail.com")
	_, err = ReserveNewUserBenefit(t.Context(), 3, config.Version, "alipay", "benefit-merchant", "LEGACYEMAILREUSE", newUserBenefitTestRisk(3))
	require.ErrorIs(t, err, ErrNewUserBenefitIdentityUsed)
	usage, err := GetNewUserBenefitUsage(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &NewUserBenefitUsage{TotalOrders: 2, TotalBonusCents: 180, Orders24h: 2}, usage)
}

func TestNewUserBenefitUsageRejectsCorruptHistoricalBudgetPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	reserveNewUserBenefitTestOrder(t, config, 1)
	// Direct fixture corruption must not reduce available subsidy and permit
	// another promise. The public order update path is immutable.
	require.NoError(t, DB.Table("new_user_benefit_orders").Where("user_id = ?", 1).UpdateColumn("credit_amount_cents", 1).Error)
	_, err := GetNewUserBenefitUsage(t.Context())
	require.ErrorIs(t, err, ErrNewUserBenefitOrderConflict)
	createNewUserBenefitTestUser(t, 2)
	_, err = ReserveNewUserBenefit(t.Context(), 2, config.Version, "alipay", "benefit-merchant", "CORRUPTBUDGET", newUserBenefitTestRisk(2))
	require.ErrorIs(t, err, ErrNewUserBenefitOrderConflict)
	assert.EqualValues(t, 1, benefitTableCount(t, &TopUp{}))
}
