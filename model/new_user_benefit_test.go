package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newUserBenefitFixture(t *testing.T) *NewUserBenefitConfig {
	t.Helper()
	db := newUserBenefitConfigFixture(t)
	models := append([]any{&User{}, &TopUp{}, &NewUserBenefitOrder{}, &SubscriptionOrder{}, &PaymentReceipt{},
		&BalanceLedgerOperation{}, &BalanceLedger{}, &Log{}, &AgentCommissionOutbox{}, &ReferralRewardOutbox{}}, BonusSourceModels()...)
	for range 2 {
		require.NoError(t, db.AutoMigrate(models...))
	}
	previousLog, previousRedis := LOG_DB, common.RedisEnabled
	LOG_DB, common.RedisEnabled = db, false
	common.OptionMapRWMutex.Lock()
	previousRate, previousUnit := operation_setting.USDExchangeRate, common.QuotaPerUnit
	operation_setting.USDExchangeRate, common.QuotaPerUnit = 10, 500000
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		LOG_DB, common.RedisEnabled = previousLog, previousRedis
		common.OptionMapRWMutex.Lock()
		operation_setting.USDExchangeRate, common.QuotaPerUnit = previousRate, previousUnit
		common.OptionMapRWMutex.Unlock()
	})
	config, err := UpdateNewUserBenefitConfig(t.Context(), 0, 7, NewUserBenefitConfig{Enabled: true, PayAmountCents: 10, CreditAmountCents: 100})
	require.NoError(t, err)
	return config
}

func createNewUserBenefitTestUser(t *testing.T, id int) {
	t.Helper()
	require.NoError(t, DB.Create(&User{Id: id, Username: fmt.Sprintf("benefit-user-%d", id), AffCode: fmt.Sprintf("benefit-aff-%d", id), Status: common.UserStatusEnabled}).Error)
}

func reserveNewUserBenefitTestOrder(t *testing.T, config *NewUserBenefitConfig, userID int) *NewUserBenefitOrder {
	t.Helper()
	createNewUserBenefitTestUser(t, userID)
	order, err := ReserveNewUserBenefit(t.Context(), userID, config.Version, "alipay", "benefit-merchant", fmt.Sprintf("BENEFIT%d", userID))
	require.NoError(t, err)
	return order
}

func newUserBenefitTestFacts(order *NewUserBenefitOrder) VerifiedPaymentFacts {
	return VerifiedPaymentFacts{Provider: PaymentProviderEpay, ExternalReference: "financial-" + order.TradeNo,
		MerchantScope: order.MerchantScope, Environment: "live", Currency: "CNY", CurrencyExponent: 2, PaidMinor: order.PayAmountCents}
}

func benefitTableCount(t *testing.T, model any) int64 {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(model).Count(&count).Error)
	return count
}

func TestNewUserBenefitReserveResumeAndImmutableSnapshotPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	createNewUserBenefitTestUser(t, 1)
	state, err := GetNewUserBenefitState(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, NewUserBenefitStatusAvailable, state.Status)
	assert.True(t, state.Eligible)
	assert.Equal(t, 5000, state.PaidQuota)
	assert.Equal(t, 45000, state.BonusQuota)
	_, err = ReserveNewUserBenefit(t.Context(), 1, 0, "alipay", "benefit-merchant", "STALE")
	require.ErrorIs(t, err, ErrNewUserBenefitConfigChanged)
	assert.Zero(t, benefitTableCount(t, &TopUp{}))
	order, err := ReserveNewUserBenefit(t.Context(), 1, config.Version, "alipay", "benefit-merchant", "BENEFIT1")
	require.NoError(t, err)
	assert.Equal(t, "10", order.USDExchangeRate)
	assert.Equal(t, "500000", order.QuotaPerUnit)
	require.ErrorIs(t, DB.Model(order).Update("bonus_quota", 90000).Error, ErrLedgerImmutable)
	require.ErrorIs(t, DB.Delete(order).Error, ErrLedgerImmutable)
	_, err = UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, NewUserBenefitConfig{Enabled: false, PayAmountCents: 100, CreditAmountCents: 500})
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	operation_setting.USDExchangeRate, common.QuotaPerUnit = 7.3, 1000000
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, DB.Create(&TopUp{UserId: 1, Amount: 1, Money: 10, TradeNo: "ORDINARYAFTER", PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusSuccess}).Error)
	resumed, err := ReserveNewUserBenefit(t.Context(), 1, 0, "alipay", "benefit-merchant", "DONOTCREATE")
	require.NoError(t, err)
	assert.Equal(t, order, resumed)
	assert.EqualValues(t, 1, benefitTableCount(t, &NewUserBenefitOrder{}))
	assert.EqualValues(t, 2, benefitTableCount(t, &TopUp{}))
	state, err = GetNewUserBenefitState(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, NewUserBenefitStatusPending, state.Status)
	assert.False(t, state.Config.Enabled)
	assert.Equal(t, 5000, state.PaidQuota)
	assert.Equal(t, 45000, state.BonusQuota)
	_, err = ReserveNewUserBenefit(t.Context(), 1, 0, "wxpay", "benefit-merchant", "OTHER")
	require.ErrorIs(t, err, ErrNewUserBenefitOrderConflict)
	_, err = ReserveNewUserBenefit(t.Context(), 1, 0, "alipay", "changed-merchant", "OTHER")
	require.ErrorIs(t, err, ErrNewUserBenefitOrderConflict)
	createNewUserBenefitTestUser(t, 2)
	_, err = ReserveNewUserBenefit(t.Context(), 2, config.Version+1, "alipay", "benefit-merchant", "NEWWHILEOFF")
	require.ErrorIs(t, err, ErrNewUserBenefitUnavailable)
}

func TestNewUserBenefitEligibilityAllHistoryAndPaymentKindsPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	for index, provider := range []string{PaymentProviderEpay, PaymentProviderStripe, PaymentProviderCreem, PaymentProviderWaffo, PaymentProviderWaffoPancake, ""} {
		t.Run("paid_"+provider, func(t *testing.T) {
			id := index + 1
			createNewUserBenefitTestUser(t, id)
			require.NoError(t, DB.Create(&TopUp{UserId: id, Amount: 1, Money: 1, TradeNo: fmt.Sprintf("OLD%d", id), PaymentMethod: "alipay", PaymentProvider: provider, Status: common.TopUpStatusSuccess, CreateTime: time.Now().Add(-365 * 24 * time.Hour).Unix()}).Error)
			state, err := GetNewUserBenefitState(t.Context(), id)
			require.NoError(t, err)
			assert.False(t, state.Eligible)
			assert.Equal(t, NewUserBenefitStatusIneligible, state.Status)
			_, err = ReserveNewUserBenefit(t.Context(), id, config.Version, "alipay", "benefit-merchant", fmt.Sprintf("NEW%d", id))
			require.ErrorIs(t, err, ErrNewUserBenefitIneligible)
		})
	}
	for index, kind := range []string{"pending", "failed", "expired", "subscription", "agent", "balance", "signup_bonus", "admin_credit"} {
		t.Run("eligible_"+kind, func(t *testing.T) {
			id := index + 100
			createNewUserBenefitTestUser(t, id)
			topUp := &TopUp{UserId: id, Amount: 1, Money: 1, TradeNo: fmt.Sprintf("HISTORY%d", id), PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusSuccess}
			switch kind {
			case "pending", "failed", "expired":
				topUp.Status = kind
			case "subscription":
				topUp.Amount = 0
				require.NoError(t, DB.Create(&SubscriptionOrder{UserId: id, PlanId: 1, TradeNo: topUp.TradeNo, Money: 1, Status: common.TopUpStatusSuccess, PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay}).Error)
			case "agent":
				topUp.AgentSimpleTopUp = true
			case "balance":
				topUp.PaymentMethod, topUp.PaymentProvider = PaymentMethodBalance, PaymentProviderBalance
			case "signup_bonus":
				_, err := CreditQuota(LedgerEntry{UserId: id, Account: AccountBonus, Delta: 100, Reason: LedgerReasonSignupBonus, ReferenceType: "signup", ReferenceId: fmt.Sprintf("%d", id)})
				require.NoError(t, err)
			case "admin_credit":
				_, err := CreditQuota(LedgerEntry{UserId: id, Account: AccountPaid, Delta: 100, Reason: LedgerReasonAdminTopup, ReferenceType: ReferralReferenceTopUp, ReferenceId: topUp.TradeNo})
				require.NoError(t, err)
			}
			if kind != "signup_bonus" {
				require.NoError(t, DB.Create(topUp).Error)
			}
			state, err := GetNewUserBenefitState(t.Context(), id)
			require.NoError(t, err)
			assert.True(t, state.Eligible)
			_, err = ReserveNewUserBenefit(t.Context(), id, config.Version, "alipay", "benefit-merchant", fmt.Sprintf("NEW%d", id))
			require.NoError(t, err)
		})
	}
}

func TestNewUserBenefitConcurrentReservationPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	createNewUserBenefitTestUser(t, 1)
	start := make(chan struct{})
	type result struct {
		order *NewUserBenefitOrder
		err   error
	}
	results := make(chan result, 8)
	var workers sync.WaitGroup
	for index := range 8 {
		workers.Go(func() {
			<-start
			order, err := ReserveNewUserBenefit(t.Context(), 1, config.Version, "alipay", "benefit-merchant", fmt.Sprintf("CONCURRENT%d", index))
			results <- result{order, err}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	tradeNo := ""
	for result := range results {
		require.NoError(t, result.err)
		if tradeNo == "" {
			tradeNo = result.order.TradeNo
		}
		assert.Equal(t, tradeNo, result.order.TradeNo)
	}
	assert.EqualValues(t, 1, benefitTableCount(t, &NewUserBenefitOrder{}))
	assert.EqualValues(t, 1, benefitTableCount(t, &TopUp{}))
	assert.Zero(t, benefitTableCount(t, &BalanceLedger{}))
}

func TestNewUserBenefitConcurrentVerifiedSettlementAndReplayPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	order := reserveNewUserBenefitTestOrder(t, config, 1)
	facts := newUserBenefitTestFacts(order)
	_, err := UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, NewUserBenefitConfig{Enabled: false, PayAmountCents: 100, CreditAmountCents: 500})
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	operation_setting.USDExchangeRate, common.QuotaPerUnit = 7.3, 1000000
	common.OptionMapRWMutex.Unlock()
	start := make(chan struct{})
	type result struct {
		replayed bool
		err      error
	}
	results := make(chan result, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			<-start
			replayed, err := RechargeEpay(order.TradeNo, "alipay", "0.10", "127.0.0.1", facts)
			results <- result{replayed, err}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	fresh := 0
	for result := range results {
		require.NoError(t, result.err)
		if !result.replayed {
			fresh++
		}
	}
	assert.Equal(t, 1, fresh)
	user := getUserBalances(t, 1)
	assert.Equal(t, 5000, user.QuotaPaid)
	assert.Equal(t, 45000, user.QuotaBonus)
	assert.Equal(t, 50000, user.Quota)
	assertQuotaInvariant(t, user)
	assert.EqualValues(t, 1, benefitTableCount(t, &PaymentReceipt{}))
	assert.EqualValues(t, 1, benefitTableCount(t, &BalanceLedgerOperation{}))
	assert.EqualValues(t, 2, benefitTableCount(t, &BalanceLedger{}))
	assert.EqualValues(t, 1, benefitTableCount(t, &BonusSourceLot{}))
	var lot BonusSourceLot
	require.NoError(t, DB.First(&lot).Error)
	assert.Equal(t, LedgerReasonNewUserTopUp, lot.Kind)
	assert.Equal(t, 45000, lot.GrantQuota)
	assert.Zero(t, benefitTableCount(t, &AgentCommissionOutbox{}))
	assert.Zero(t, benefitTableCount(t, &ReferralRewardOutbox{}))
	state, err := GetNewUserBenefitState(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, NewUserBenefitStatusClaimed, state.Status)
	assert.False(t, state.Eligible)
	assert.Positive(t, state.Order.GrantedAt)
	_, err = ReserveNewUserBenefit(t.Context(), 1, 0, "alipay", "benefit-merchant", "AGAIN")
	require.ErrorIs(t, err, ErrNewUserBenefitClaimed)
	changed := facts
	changed.ExternalReference = "another-financial-payment"
	_, err = RechargeEpay(order.TradeNo, "alipay", "0.10", "127.0.0.1", changed)
	require.ErrorIs(t, err, ErrPaymentReceiptConflict)
	_, err = RechargeEpay(order.TradeNo, "alipay", "0.10", "127.0.0.1")
	require.ErrorIs(t, err, ErrNewUserBenefitPaymentRequired)
}

func TestNewUserBenefitRejectsMismatchedPaymentEvidencePostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	order := reserveNewUserBenefitTestOrder(t, config, 1)
	valid := newUserBenefitTestFacts(order)
	for _, test := range []struct {
		name   string
		mutate func(*VerifiedPaymentFacts)
	}{
		{"provider", func(f *VerifiedPaymentFacts) { f.Provider = PaymentProviderStripe }},
		{"merchant", func(f *VerifiedPaymentFacts) { f.MerchantScope = "another-merchant" }},
		{"environment", func(f *VerifiedPaymentFacts) { f.Environment = "test" }},
		{"currency", func(f *VerifiedPaymentFacts) { f.Currency = "USD" }},
		{"exponent", func(f *VerifiedPaymentFacts) { f.CurrencyExponent = 3 }},
		{"amount", func(f *VerifiedPaymentFacts) { f.PaidMinor++ }},
		{"missing_reference", func(f *VerifiedPaymentFacts) { f.ExternalReference = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fact := valid
			test.mutate(&fact)
			_, err := RechargeEpay(order.TradeNo, "alipay", "0.10", "127.0.0.1", fact)
			require.Error(t, err)
		})
	}
	_, err := RechargeEpay(order.TradeNo, "alipay", "0.10", "127.0.0.1")
	require.ErrorIs(t, err, ErrNewUserBenefitPaymentRequired)
	_, err = RechargeEpay(order.TradeNo, "alipay", "0.10", "127.0.0.1", valid, valid)
	require.ErrorIs(t, err, ErrNewUserBenefitPaymentRequired)
	_, err = RechargeEpay(order.TradeNo, "wxpay", "0.10", "127.0.0.1", valid)
	require.ErrorIs(t, err, ErrNewUserBenefitPaymentRequired)
	_, err = RechargeEpay(order.TradeNo, "alipay", "0.11", "127.0.0.1", valid)
	require.ErrorIs(t, err, ErrPaymentAmountMismatch)
	assert.Zero(t, benefitTableCount(t, &PaymentReceipt{}))
	assert.Zero(t, benefitTableCount(t, &BalanceLedger{}))
	assert.Zero(t, getUserBalances(t, 1).Quota)
	assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(order.TradeNo).Status)
	_, err = RechargeEpay(order.TradeNo, "alipay", "0.10", "127.0.0.1", valid)
	require.NoError(t, err)
	second := reserveNewUserBenefitTestOrder(t, config, 2)
	_, err = RechargeEpay(second.TradeNo, "alipay", "0.10", "127.0.0.1", valid)
	require.ErrorIs(t, err, ErrPaymentReceiptConflict)
	assert.Zero(t, getUserBalances(t, 2).Quota)
	assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(second.TradeNo).Status)
}

func TestNewUserBenefitSettlementRollbackAndWalletLimitPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	order := reserveNewUserBenefitTestOrder(t, config, 1)
	injected := errors.New("injected benefit bonus source failure")
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register("benefit_source_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "bonus_source_states" {
			tx.AddError(injected)
		}
	}))
	_, err := RechargeEpay(order.TradeNo, "alipay", "0.10", "127.0.0.1", newUserBenefitTestFacts(order))
	require.ErrorIs(t, err, injected)
	require.NoError(t, DB.Callback().Create().Remove("benefit_source_failure"))
	for _, table := range []any{&PaymentReceipt{}, &BalanceLedgerOperation{}, &BalanceLedger{}, &BonusSourceLot{}, &BonusSourceState{}, &BonusSourceAccount{}} {
		assert.Zero(t, benefitTableCount(t, table))
	}
	assert.Zero(t, getUserBalances(t, 1).Quota)
	assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(order.TradeNo).Status)
	_, err = RechargeEpay(order.TradeNo, "alipay", "0.10", "127.0.0.1", newUserBenefitTestFacts(order))
	require.NoError(t, err)
	second := reserveNewUserBenefitTestOrder(t, config, 2)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Updates(map[string]any{"quota": common.MaxWalletQuota - 10, "quota_paid": common.MaxWalletQuota - 10}).Error)
	_, err = RechargeEpay(second.TradeNo, "alipay", "0.10", "127.0.0.1", newUserBenefitTestFacts(second))
	require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	assert.EqualValues(t, 1, benefitTableCount(t, &PaymentReceipt{}))
	assert.EqualValues(t, 1, benefitTableCount(t, &BalanceLedgerOperation{}))
	assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(second.TradeNo).Status)
}

func TestNewUserBenefitReservationRollbackAndCompletionBypassPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	createNewUserBenefitTestUser(t, 1)
	injected := errors.New("injected benefit promise failure")
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register("benefit_order_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "new_user_benefit_orders" {
			tx.AddError(injected)
		}
	}))
	_, err := ReserveNewUserBenefit(t.Context(), 1, config.Version, "alipay", "benefit-merchant", "ROLLBACK")
	require.ErrorIs(t, err, injected)
	require.NoError(t, DB.Callback().Create().Remove("benefit_order_failure"))
	assert.Zero(t, benefitTableCount(t, &TopUp{}))
	assert.Zero(t, benefitTableCount(t, &NewUserBenefitOrder{}))
	order, err := ReserveNewUserBenefit(t.Context(), 1, config.Version, "alipay", "benefit-merchant", "ROLLBACK")
	require.NoError(t, err)
	require.ErrorIs(t, ManualCompleteTopUp(order.TradeNo, "127.0.0.1"), ErrNewUserBenefitPaymentRequired)
	require.ErrorIs(t, UpdatePendingTopUpStatus(order.TradeNo, PaymentProviderEpay, common.TopUpStatusExpired), ErrNewUserBenefitPaymentRequired)
	err = DB.Transaction(func(tx *gorm.DB) error {
		_, err := creditTopUpQuota(tx, LedgerEntry{UserId: 1, Account: AccountPaid, Delta: 50000, Reason: LedgerReasonTopup, ReferenceType: ReferralReferenceTopUp, ReferenceId: order.TradeNo})
		return err
	})
	require.ErrorIs(t, err, ErrNewUserBenefitPaymentRequired)
	require.EqualError(t, Recharge(order.TradeNo, "", "127.0.0.1"), "top-up failed")
	assert.Zero(t, benefitTableCount(t, &BalanceLedger{}))
	assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(order.TradeNo).Status)
}

func TestNewUserBenefitQuotaBoundsAndZeroBonusPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	createNewUserBenefitTestUser(t, 1)
	for _, rate := range []float64{0, -1, math.NaN(), math.Inf(1), 1e-15} {
		common.OptionMapRWMutex.Lock()
		operation_setting.USDExchangeRate = rate
		common.OptionMapRWMutex.Unlock()
		_, err := ReserveNewUserBenefit(t.Context(), 1, config.Version, "alipay", "benefit-merchant", "BADRATE")
		require.ErrorIs(t, err, ErrNewUserBenefitInvalidConfig)
	}
	assert.Zero(t, benefitTableCount(t, &TopUp{}))
	common.OptionMapRWMutex.Lock()
	operation_setting.USDExchangeRate = 7.3
	common.OptionMapRWMutex.Unlock()
	quote, err := GetNewUserBenefitState(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, 6849, quote.PaidQuota)
	assert.Equal(t, 68493, quote.PaidQuota+quote.BonusQuota)
	config, err = UpdateNewUserBenefitConfig(t.Context(), config.Version, 7, NewUserBenefitConfig{Enabled: true, PayAmountCents: 10, CreditAmountCents: 10})
	require.NoError(t, err)
	order, err := ReserveNewUserBenefit(t.Context(), 1, config.Version, "alipay", "benefit-merchant", "ZEROBONUS")
	require.NoError(t, err)
	assert.Zero(t, order.BonusQuota)
	_, err = RechargeEpay(order.TradeNo, "alipay", "0.10", "127.0.0.1", newUserBenefitTestFacts(order))
	require.NoError(t, err)
	assert.Equal(t, order.PaidQuota, getUserBalances(t, 1).QuotaPaid)
	assert.Zero(t, getUserBalances(t, 1).QuotaBonus)
	assert.EqualValues(t, 1, benefitTableCount(t, &BalanceLedger{}))
	assert.Zero(t, benefitTableCount(t, &BonusSourceLot{}))
}

func TestNewUserBenefitTopUpHistoryHydrationPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	order := reserveNewUserBenefitTestOrder(t, config, 1)
	page := &common.PageInfo{Page: 1, PageSize: 10}
	queries := map[string]func() ([]*TopUp, int64, error){
		"user":  func() ([]*TopUp, int64, error) { return GetUserTopUps(1, page) },
		"admin": func() ([]*TopUp, int64, error) { return GetAllTopUps(page) },
		// Search uses exact matching without an explicit % wildcard.
		"user_search":  func() ([]*TopUp, int64, error) { return SearchUserTopUps(1, order.TradeNo, page) },
		"admin_search": func() ([]*TopUp, int64, error) { return SearchAllTopUps(order.TradeNo, page) },
	}
	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			rows, count, err := query()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.EqualValues(t, 1, count)
			assert.Zero(t, rows[0].Amount)
			assert.Equal(t, 50000, rows[0].NewUserBenefitCreditQuota)
			assert.Equal(t, "1.00", rows[0].NewUserBenefitCreditAmountCNY)
		})
	}
	// A missing immutable promise must produce an explicit error, never a
	// plausible zero credit. Raw SQL here simulates a damaged database.
	require.NoError(t, DB.Exec("DELETE FROM new_user_benefit_orders WHERE user_id = ?", order.UserID).Error)
	for _, query := range queries {
		_, _, err := query()
		require.ErrorIs(t, err, ErrNewUserBenefitOrderConflict)
	}
}

func TestNewUserBenefitAdmissionSerializesWithOrdinaryWalletPaymentPostgres(t *testing.T) {
	config := newUserBenefitFixture(t)
	createNewUserBenefitTestUser(t, 1)
	locked := make(chan struct{})
	release := make(chan struct{})
	committed := make(chan error, 1)
	go func() {
		committed <- DB.Transaction(func(tx *gorm.DB) error {
			var user User
			if err := lockForUpdate(tx).First(&user, 1).Error; err != nil {
				return err
			}
			close(locked)
			<-release
			return tx.Create(&TopUp{UserId: 1, Amount: 1, Money: 1, TradeNo: "ORDINARYFIRST", PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusSuccess}).Error
		})
	}()
	<-locked
	started := make(chan struct{})
	reserved := make(chan error, 1)
	go func() {
		close(started)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		_, err := ReserveNewUserBenefit(ctx, 1, config.Version, "alipay", "benefit-merchant", "RACINGBENEFIT")
		reserved <- err
	}()
	<-started
	close(release)
	require.NoError(t, <-committed)
	require.ErrorIs(t, <-reserved, ErrNewUserBenefitIneligible)
	assert.Zero(t, benefitTableCount(t, &NewUserBenefitOrder{}))
}

// The production top-up shape immediately before the benefit extension. The
// fixture is deliberately independent of TopUp so a future field addition
// cannot silently turn an upgrade test into another fresh-schema test.
type newUserBenefitLegacyTopUp struct {
	AgentSimpleTopUp         bool   `gorm:"not null;default:false;index"`
	AgentSimpleKind          string `gorm:"type:varchar(16);not null;default:''"`
	AgentSimpleQuota         int64  `gorm:"type:bigint;not null;default:0"`
	AgentSimpleDeferredQuota int64  `gorm:"type:bigint;not null;default:0"`
	PaymentReceiptID         int64
	PaidProviderReference    string `gorm:"type:varchar(255)"`
	PaidMerchantScope        string `gorm:"type:varchar(128)"`
	PaidEnvironment          string `gorm:"type:varchar(16)"`
	PaidCurrency             string `gorm:"type:varchar(3)"`
	PaidCurrencyExponent     int
	PaidAmountMinor          int64
	Id                       int
	UserId                   int `gorm:"index"`
	Amount                   int64
	Money                    float64
	TradeNo                  string `gorm:"unique;type:varchar(255);index"`
	PaymentMethod            string `gorm:"type:varchar(50)"`
	PaymentProvider          string `gorm:"type:varchar(50);default:''"`
	CreateTime               int64
	CompleteTime             int64
	Status                   string
}

func TestNewUserBenefitUpgradePreservesOrdinaryTopUpsAndWalletPostgres(t *testing.T) {
	newUserBenefitFixture(t)
	// These tables belong to this test's randomly named disposable schema.
	require.NoError(t, DB.Migrator().DropTable(&TopUp{}, &NewUserBenefitOrder{}, &NewUserBenefitConfigAudit{}, &NewUserBenefitConfig{}))
	require.NoError(t, DB.Table("top_ups").AutoMigrate(&newUserBenefitLegacyTopUp{}))
	require.False(t, DB.Migrator().HasColumn(&TopUp{}, "NewUserBenefit"))
	createNewUserBenefitTestUser(t, 1)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Updates(map[string]any{"quota": 123, "quota_paid": 123}).Error)
	now := common.GetTimestamp()
	oldRows := []newUserBenefitLegacyTopUp{
		{Id: 1, UserId: 1, Amount: 2, Money: 14.6, TradeNo: "LEGACYCOMPLETE", PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, CreateTime: now - 60, CompleteTime: now - 30, Status: common.TopUpStatusSuccess},
		{Id: 2, UserId: 1, Amount: 1, Money: 7.3, TradeNo: "LEGACYPENDING", PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay, CreateTime: now, Status: common.TopUpStatusPending},
	}
	require.NoError(t, DB.Table("top_ups").Create(&oldRows).Error)
	beforeWallet := getUserBalances(t, 1)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&TopUp{}, &NewUserBenefitConfig{}, &NewUserBenefitConfigAudit{}, &NewUserBenefitOrder{}))
	}
	require.True(t, DB.Migrator().HasColumn(&TopUp{}, "NewUserBenefit"))
	assert.Equal(t, beforeWallet, getUserBalances(t, 1))
	for _, previous := range oldRows {
		current := GetTopUpByTradeNo(previous.TradeNo)
		require.NotNil(t, current)
		assert.False(t, current.NewUserBenefit)
		assert.Equal(t, previous.Amount, current.Amount)
		assert.Equal(t, previous.Money, current.Money)
		assert.Equal(t, previous.Status, current.Status)
		assert.Equal(t, previous.CompleteTime, current.CompleteTime)
		assert.Zero(t, current.NewUserBenefitCreditQuota)
	}
	settings, err := GetNewUserBenefitConfig(t.Context())
	require.NoError(t, err)
	assert.False(t, settings.Enabled)
	assert.Zero(t, settings.Version)
	fact, valid := NewVerifiedPaymentFacts(PaymentProviderEpay, "legacy-financial-payment", "benefit-merchant", "live", "CNY", "7.30", false)
	require.True(t, valid)
	replayed, err := RechargeEpay("LEGACYPENDING", "alipay", "7.30", "127.0.0.1", fact)
	require.NoError(t, err)
	assert.False(t, replayed)
	afterWallet := getUserBalances(t, 1)
	assert.Equal(t, 500123, afterWallet.QuotaPaid)
	assert.Zero(t, afterWallet.QuotaBonus)
	assertQuotaInvariant(t, afterWallet)
	assert.Equal(t, common.TopUpStatusSuccess, GetTopUpByTradeNo("LEGACYPENDING").Status)
	replayed, err = RechargeEpay("LEGACYPENDING", "alipay", "7.30", "127.0.0.1", fact)
	require.NoError(t, err)
	assert.True(t, replayed)
	assert.Equal(t, afterWallet, getUserBalances(t, 1))
	assert.Zero(t, benefitTableCount(t, &NewUserBenefitOrder{}))
	assert.EqualValues(t, 1, benefitTableCount(t, &PaymentReceipt{}))
}
