package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"net/netip"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	NewUserBenefitStatusAvailable           = "available"
	NewUserBenefitStatusIneligible          = "ineligible"
	NewUserBenefitStatusPending             = "pending"
	NewUserBenefitStatusClaimed             = "claimed"
	NewUserBenefitStatusVerificationPending = "verification_pending"
	NewUserBenefitStatusRewardDenied        = "reward_denied"
	LedgerReasonNewUserTopUp                = "new_user_topup"
)

var (
	ErrNewUserBenefitUnavailable          = errors.New("New user benefit is unavailable")
	ErrNewUserBenefitIneligible           = errors.New("New user benefit is available before your first successful online top-up")
	ErrNewUserBenefitClaimed              = errors.New("New user benefit has already been claimed")
	ErrNewUserBenefitOrderConflict        = errors.New("New user benefit payment order does not match")
	ErrNewUserBenefitPaymentRequired      = errors.New("New user benefit requires verified payment")
	ErrNewUserBenefitEmailRequired        = errors.New("Verify your current email before requesting the new user benefit")
	ErrNewUserBenefitIdentityUsed         = errors.New("This verified email has already been used for the new user benefit")
	ErrNewUserBenefitLimitReached         = errors.New("New user benefit admission limit has been reached")
	ErrNewUserBenefitVerificationRequired = errors.New("Complete human verification before requesting the new user benefit")
	ErrNewUserBenefitInvalidNetwork       = errors.New("A valid client network is required for the new user benefit")
)

// NewUserBenefitOrder is the immutable promise made before releasing an Epay
// payment form. One account has one order for its lifetime, including across
// configuration changes. TopUp owns payment completion; an optional reward
// review records the captured payer condition after principal is credited.
type NewUserBenefitOrder struct {
	PaymentGateway              string `gorm:"type:varchar(1024);not null;default:''"`
	RequireVerifiedEmail        bool   `gorm:"not null;default:false"`
	RequireTurnstile            bool   `gorm:"not null;default:false"`
	NetworkLimitEnabled         bool   `gorm:"not null;default:false"`
	PaymentIdentityCheckEnabled bool   `gorm:"not null;default:false"`
	RiskPolicyVersion           int    `gorm:"not null;default:0"`
	RewardStatus                string `gorm:"-"`
	RewardReason                string `gorm:"-"`
	UserID                      int    `gorm:"primaryKey;autoIncrement:false"`
	TradeNo                     string `gorm:"type:varchar(32);not null;uniqueIndex"`
	PaymentProvider             string `gorm:"type:varchar(32);not null"`
	PaymentMethod               string `gorm:"type:varchar(50);not null"`
	MerchantScope               string `gorm:"type:varchar(128);not null"`
	PayAmountCents              int64  `gorm:"not null"`
	CreditAmountCents           int64  `gorm:"not null"`
	ConfigVersion               int64  `gorm:"not null"`
	USDExchangeRate             string `gorm:"type:varchar(64);not null"`
	QuotaPerUnit                string `gorm:"type:varchar(64);not null"`
	PaidQuota                   int    `gorm:"type:bigint;not null"`
	BonusQuota                  int    `gorm:"type:bigint;not null"`
	// Nullable hashes preserve old promises and represent disabled controls.
	// Enabled checks capture their identity permanently; rebinding an email or
	// deleting an account cannot release a captured benefit identity.
	EmailIdentityHash *string `gorm:"type:varchar(64);uniqueIndex"`
	NetworkHash       *string `gorm:"type:varchar(64);index:idx_new_user_benefit_network_time,priority:1"`
	CreatedAt         int64   `gorm:"not null;index;index:idx_new_user_benefit_network_time,priority:2"`
	Status            string  `gorm:"-"`
	GrantedAt         int64   `gorm:"-"`
}

func (*NewUserBenefitOrder) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (*NewUserBenefitOrder) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

type NewUserBenefitState struct {
	Config        *NewUserBenefitConfig
	Order         *NewUserBenefitOrder
	Eligible      bool
	Status        string
	PaidQuota     int
	BonusQuota    int
	BlockedReason string
}

// NewUserBenefitRiskContext contains server-established admission evidence,
// never JSON fields accepted from the customer.
type NewUserBenefitRiskContext struct {
	PaymentGateway string
	ClientIP       string
	HumanVerified  bool
}

// NewUserBenefitUsage includes every committed promise, paid or unpaid, across
// all configuration versions. The Epay integration cannot safely revoke forms,
// so neither elapsed time nor disabling the offer releases the total budget.
type NewUserBenefitUsage struct {
	TotalOrders     int64
	TotalBonusCents int64
	Orders24h       int64 `gorm:"column:orders_24h"`
}

func newUserBenefitUsageTx(tx *gorm.DB, now int64) (*NewUserBenefitUsage, error) {
	var totals struct {
		NewUserBenefitUsage `gorm:"embedded"`
		InvalidOrders       int64
	}
	err := tx.Model(&NewUserBenefitOrder{}).
		Select("COUNT(*) AS total_orders, COALESCE(SUM(credit_amount_cents - pay_amount_cents), 0) AS total_bonus_cents, COALESCE(SUM(CASE WHEN created_at > ? THEN 1 ELSE 0 END), 0) AS orders_24h, COALESCE(SUM(CASE WHEN pay_amount_cents <= 0 OR credit_amount_cents < pay_amount_cents OR credit_amount_cents > ? THEN 1 ELSE 0 END), 0) AS invalid_orders", now-24*60*60, MaxNewUserBenefitCents).
		Scan(&totals).Error
	if err != nil {
		return nil, err
	}
	if totals.InvalidOrders != 0 || totals.TotalOrders < 0 || totals.TotalBonusCents < 0 || totals.Orders24h < 0 {
		return nil, ErrNewUserBenefitOrderConflict
	}
	return &totals.NewUserBenefitUsage, nil
}

func GetNewUserBenefitUsage(ctx context.Context) (*NewUserBenefitUsage, error) {
	return newUserBenefitUsageTx(DB.WithContext(ctx), common.GetTimestamp())
}

func newUserBenefitLimitReached(config *NewUserBenefitConfig, usage *NewUserBenefitUsage) bool {
	bonusCents := config.CreditAmountCents - config.PayAmountCents
	return usage.TotalOrders >= config.MaxTotalOrders || usage.Orders24h >= config.MaxOrdersPer24h ||
		usage.TotalBonusCents > config.TotalBonusBudgetCents || bonusCents > config.TotalBonusBudgetCents-usage.TotalBonusCents
}

func newUserBenefitDigest(scope, identity string) string {
	digest := sha256.Sum256([]byte("new-user-benefit:" + scope + ":" + identity))
	return hex.EncodeToString(digest[:])
}

func newUserBenefitEmailIdentity(email string) string {
	email = NormalizeEmail(email)
	local, domain, ok := strings.Cut(email, "@")
	if ok && (domain == "gmail.com" || domain == "googlemail.com") {
		// Personal Gmail and googlemail.com share addresses and ignore dots
		// and +tags. This benefit-specific rule does not change Workspace,
		// custom-domain identities or login addresses.
		local, _, _ = strings.Cut(local, "+")
		return strings.ReplaceAll(local, ".", "") + "@gmail.com"
	}
	return email
}

func newUserBenefitEmailIdentityTx(tx *gorm.DB, user *User) (string, error) {
	if !VerifiedSupportEmail(user) {
		return "", ErrNewUserBenefitEmailRequired
	}
	email := newUserBenefitEmailIdentity(user.Email)
	digest := newUserBenefitDigest("email", email)
	var count int64
	err := tx.Model(&NewUserBenefitOrder{}).Where("email_identity_hash = ?", digest).Count(&count).Error
	if err != nil {
		return "", err
	}
	if count > 0 {
		return "", ErrNewUserBenefitIdentityUsed
	}
	// Legacy orders did not capture an email. Also check their current account
	// association, including soft-deleted users, rather than treating a missing
	// hash as proof that the verified email has never participated.
	legacyOrders := tx.Model(&NewUserBenefitOrder{}).Select("user_id").Where("email_identity_hash IS NULL AND risk_policy_version = 0")
	legacyOwners := tx.Unscoped().Model(&User{}).Where("id IN (?)", legacyOrders)
	if strings.HasSuffix(email, "@gmail.com") {
		legacyOwners = legacyOwners.Where("LOWER(TRIM(email)) LIKE ? OR LOWER(TRIM(email)) LIKE ?", "%@gmail.com", "%@googlemail.com")
	} else {
		legacyOwners = legacyOwners.Where("LOWER(TRIM(email)) = ?", email)
	}
	var legacyEmails []string
	if err := legacyOwners.Pluck("email", &legacyEmails).Error; err != nil {
		return "", err
	}
	for _, legacyEmail := range legacyEmails {
		if newUserBenefitEmailIdentity(legacyEmail) == email {
			return "", ErrNewUserBenefitIdentityUsed
		}
	}
	return digest, nil
}

func newUserBenefitNetworkHash(clientIP string) (string, error) {
	address, err := netip.ParseAddr(clientIP)
	if err != nil || address.Zone() != "" {
		return "", ErrNewUserBenefitInvalidNetwork
	}
	address = address.Unmap()
	if address.IsUnspecified() || address.IsMulticast() {
		return "", ErrNewUserBenefitInvalidNetwork
	}
	bits := 64
	if address.Is4() {
		bits = 32
	}
	return newUserBenefitDigest("network", netip.PrefixFrom(address, bits).Masked().String()), nil
}

func newUserBenefitQuota(config *NewUserBenefitConfig, exchangeRate, quotaPerUnit decimal.Decimal) (int, int, error) {
	if config == nil || validateNewUserBenefitAmounts(config.PayAmountCents, config.CreditAmountCents) != nil || !exchangeRate.IsPositive() || !quotaPerUnit.IsPositive() {
		return 0, 0, ErrNewUserBenefitInvalidConfig
	}
	paid, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(config.PayAmountCents).Div(decimal.NewFromInt(100)).Div(exchangeRate).Mul(quotaPerUnit))
	if err != nil || paid <= 0 {
		return 0, 0, ErrNewUserBenefitInvalidConfig
	}
	total, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(config.CreditAmountCents).Div(decimal.NewFromInt(100)).Div(exchangeRate).Mul(quotaPerUnit))
	if err != nil || total < paid {
		return 0, 0, ErrNewUserBenefitInvalidConfig
	}
	return paid, total - paid, nil
}

func newUserBenefitQuote(config *NewUserBenefitConfig) (paid, bonus int, rate, unit string, err error) {
	// Option updates publish these values under the same mutex. Capture both
	// once; settlement never consults mutable display or recharge settings.
	common.OptionMapRWMutex.RLock()
	exchangeRate, quotaPerUnit := operation_setting.USDExchangeRate, common.QuotaPerUnit
	common.OptionMapRWMutex.RUnlock()
	if math.IsNaN(exchangeRate) || math.IsInf(exchangeRate, 0) || exchangeRate <= 0 || math.IsNaN(quotaPerUnit) || math.IsInf(quotaPerUnit, 0) || quotaPerUnit <= 0 {
		return 0, 0, "", "", ErrNewUserBenefitInvalidConfig
	}
	dRate, dUnit := decimal.NewFromFloat(exchangeRate), decimal.NewFromFloat(quotaPerUnit)
	paid, bonus, err = newUserBenefitQuota(config, dRate, dUnit)
	return paid, bonus, dRate.String(), dUnit.String(), err
}

func validateNewUserBenefitOrder(order *NewUserBenefitOrder, topUp *TopUp) error {
	if order == nil || topUp == nil || !topUp.NewUserBenefit || topUp.AgentSimpleTopUp || order.UserID <= 0 ||
		order.UserID != topUp.UserId || order.TradeNo != topUp.TradeNo || topUp.Amount != 0 ||
		order.PaymentProvider != PaymentProviderEpay || topUp.PaymentProvider != PaymentProviderEpay ||
		order.PaymentMethod == "" || order.PaymentMethod != topUp.PaymentMethod || order.MerchantScope == "" {
		return ErrNewUserBenefitOrderConflict
	}
	rate, err := decimal.NewFromString(order.USDExchangeRate)
	if err != nil {
		return ErrNewUserBenefitOrderConflict
	}
	unit, err := decimal.NewFromString(order.QuotaPerUnit)
	if err != nil {
		return ErrNewUserBenefitOrderConflict
	}
	paid, bonus, err := newUserBenefitQuota(&NewUserBenefitConfig{PayAmountCents: order.PayAmountCents, CreditAmountCents: order.CreditAmountCents}, rate, unit)
	if err != nil || paid != order.PaidQuota || bonus != order.BonusQuota ||
		math.IsNaN(topUp.Money) || math.IsInf(topUp.Money, 0) ||
		!decimal.NewFromFloat(topUp.Money).Equal(decimal.NewFromInt(order.PayAmountCents).Div(decimal.NewFromInt(100))) {
		return ErrNewUserBenefitOrderConflict
	}
	switch topUp.Status {
	case common.TopUpStatusPending:
		order.Status = NewUserBenefitStatusPending
		order.GrantedAt = 0
	case common.TopUpStatusSuccess:
		order.Status = NewUserBenefitStatusClaimed
		if order.PaymentIdentityCheckEnabled {
			order.Status = NewUserBenefitStatusVerificationPending
		}
		order.GrantedAt = topUp.CompleteTime
	default:
		return ErrNewUserBenefitOrderConflict
	}
	return nil
}

func getNewUserBenefitOrderTx(tx *gorm.DB, userID int) (*NewUserBenefitOrder, error) {
	var order NewUserBenefitOrder
	if err := tx.First(&order, userID).Error; err != nil {
		return nil, err
	}
	// No order lock here: reservation takes config -> user, while settlement
	// takes top-up -> user. Waiting for the top-up under a user lock deadlocks.
	var topUp TopUp
	if err := tx.Where("trade_no = ?", order.TradeNo).First(&topUp).Error; err != nil {
		return nil, ErrNewUserBenefitOrderConflict
	}
	if err := validateNewUserBenefitOrder(&order, &topUp); err != nil {
		return nil, err
	}
	if err := hydrateNewUserBenefitRewardTx(tx, &order, &topUp); err != nil {
		return nil, err
	}
	return &order, nil
}

// The legacy Amount column contains integer USD units. Benefit credits can be
// fractional USD, so record pages receive explicit display fields from the
// immutable order instead of incorrectly showing Amount=0 as the credit.
func hydrateNewUserBenefitTopUpsTx(tx *gorm.DB, topUps []*TopUp) error {
	trades := make([]string, 0)
	for _, topUp := range topUps {
		if topUp.NewUserBenefit {
			trades = append(trades, topUp.TradeNo)
		}
	}
	if len(trades) == 0 {
		return nil
	}
	var orders []NewUserBenefitOrder
	if err := tx.Where("trade_no IN ?", trades).Find(&orders).Error; err != nil {
		return err
	}
	byTrade := make(map[string]*NewUserBenefitOrder, len(orders))
	for i := range orders {
		byTrade[orders[i].TradeNo] = &orders[i]
	}
	for _, topUp := range topUps {
		if !topUp.NewUserBenefit {
			continue
		}
		order := byTrade[topUp.TradeNo]
		if err := validateNewUserBenefitOrder(order, topUp); err != nil {
			return err
		}
		if err := hydrateNewUserBenefitRewardTx(tx, order, topUp); err != nil {
			return err
		}
		topUp.NewUserBenefitRewardStatus, topUp.NewUserBenefitRewardReason = order.RewardStatus, order.RewardReason
		if topUp.Status == common.TopUpStatusSuccess {
			topUp.NewUserBenefitCreditedQuota = order.PaidQuota
			creditedCents := order.PayAmountCents
			if order.Status == NewUserBenefitStatusClaimed {
				topUp.NewUserBenefitCreditedQuota += order.BonusQuota
				creditedCents = order.CreditAmountCents
			}
			topUp.NewUserBenefitCreditedAmountCNY = FormatNewUserBenefitCNY(creditedCents)
		}
		topUp.NewUserBenefitCreditQuota = order.PaidQuota + order.BonusQuota
		topUp.NewUserBenefitCreditAmountCNY = FormatNewUserBenefitCNY(order.CreditAmountCents)
	}
	return nil
}

func hasSuccessfulNewUserBenefitTopUpTx(tx *gorm.DB, userID int) (bool, error) {
	// All history, rather than the wallet page's 30-day window. Subscription
	// display rows, balance purchases and agent cash operations are not wallet
	// top-ups. Admin-created credit and signup gifts are not online payment.
	var count int64
	err := qualifyingTopUpOrderScope(tx.Model(&TopUp{})).Where("top_ups.user_id = ?", userID).
		Where("NOT EXISTS (SELECT 1 FROM balance_ledger WHERE balance_ledger.reason = ? AND balance_ledger.reference_type = ? AND balance_ledger.reference_id = top_ups.trade_no)", LedgerReasonAdminTopup, ReferralReferenceTopUp).
		Count(&count).Error
	return count > 0, err
}

func GetNewUserBenefitState(ctx context.Context, userID int) (*NewUserBenefitState, error) {
	if userID <= 0 {
		return nil, ErrNewUserBenefitIneligible
	}
	config, err := GetNewUserBenefitConfig(ctx)
	if err != nil {
		return nil, err
	}
	state := &NewUserBenefitState{Config: config, Status: NewUserBenefitStatusIneligible}
	db := DB.WithContext(ctx)
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		return nil, err
	}
	if user.Status != common.UserStatusEnabled {
		return state, nil
	}
	order, err := getNewUserBenefitOrderTx(db, userID)
	if err == nil {
		state.Order, state.Status = order, order.Status
		state.Eligible = order.Status == NewUserBenefitStatusPending
		state.PaidQuota, state.BonusQuota = order.PaidQuota, order.BonusQuota
		if order.Status == NewUserBenefitStatusVerificationPending {
			state.BlockedReason = "payment_identity_pending"
		}
		if order.Status == NewUserBenefitStatusRewardDenied {
			state.BlockedReason = "payment_identity_used"
		}
		return state, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if !config.Enabled {
		return state, nil
	}
	priorTopUp, err := hasSuccessfulNewUserBenefitTopUpTx(db, userID)
	if err != nil {
		return nil, err
	}
	if priorTopUp {
		return state, nil
	}
	if config.RequireVerifiedEmail {
		if _, err = newUserBenefitEmailIdentityTx(db, &user); err != nil {
			switch {
			case errors.Is(err, ErrNewUserBenefitEmailRequired):
				state.BlockedReason = "email_verification_required"
			case errors.Is(err, ErrNewUserBenefitIdentityUsed):
				state.BlockedReason = "identity_already_used"
			default:
				return nil, err
			}
			return state, nil
		}
	}
	usage, err := newUserBenefitUsageTx(db, common.GetTimestamp())
	if err != nil {
		return nil, err
	}
	if newUserBenefitLimitReached(config, usage) {
		state.BlockedReason = "limit_reached"
		return state, nil
	}
	state.PaidQuota, state.BonusQuota, _, _, err = newUserBenefitQuote(config)
	if err != nil {
		return nil, err
	}
	state.Eligible, state.Status = true, NewUserBenefitStatusAvailable
	return state, nil
}

func validNewUserBenefitIdentity(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength || strings.TrimSpace(value) != value {
		return false
	}
	for _, ch := range value {
		if ch < 32 || ch == 127 {
			return false
		}
	}
	return true
}

// ReserveNewUserBenefit commits the immutable promise before a payment form
// leaves the server. A pending promise is resumed even after an administrator
// disables the offer, changes its terms, or another ordinary top-up completes.
func ReserveNewUserBenefit(ctx context.Context, userID int, configVersion int64, paymentMethod, merchantScope, tradeNo string, risk NewUserBenefitRiskContext) (*NewUserBenefitOrder, error) {
	if userID <= 0 || configVersion < 0 || !validNewUserBenefitIdentity(paymentMethod, 50) ||
		!validNewUserBenefitIdentity(merchantScope, 128) || !validNewUserBenefitIdentity(tradeNo, 32) {
		return nil, ErrNewUserBenefitOrderConflict
	}
	var order *NewUserBenefitOrder
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		config, err := LockNewUserBenefitConfigTx(tx)
		if err != nil {
			return err
		}
		var user User
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			return ErrNewUserBenefitIneligible
		}
		order, err = getNewUserBenefitOrderTx(tx, userID)
		if err == nil {
			if order.Status != NewUserBenefitStatusPending {
				return ErrNewUserBenefitClaimed
			}
			if order.PaymentMethod != paymentMethod || order.MerchantScope != merchantScope || order.PaymentIdentityCheckEnabled && order.PaymentGateway != risk.PaymentGateway {
				return ErrNewUserBenefitOrderConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if config.Version != configVersion {
			return ErrNewUserBenefitConfigChanged
		}
		if !config.Enabled {
			return ErrNewUserBenefitUnavailable
		}
		if err := ValidateNewUserBenefitConfig(*config); err != nil {
			return err
		}
		if config.RequireTurnstile && !risk.HumanVerified {
			return ErrNewUserBenefitVerificationRequired
		}
		if config.PaymentIdentityCheckEnabled {
			gateway, gatewayErr := url.Parse(risk.PaymentGateway)
			if paymentMethod != "alipay" || gatewayErr != nil || gateway.Scheme != "https" || gateway.Hostname() == "" || gateway.User != nil || gateway.RawQuery != "" || gateway.ForceQuery || strings.Contains(risk.PaymentGateway, "#") {
				return ErrNewUserBenefitUnavailable
			}
		}
		var networkHash, emailHash *string
		if config.NetworkLimitEnabled {
			hash, err := newUserBenefitNetworkHash(risk.ClientIP)
			if err != nil {
				return err
			}
			networkHash = &hash
		}
		if config.RequireVerifiedEmail {
			hash, err := newUserBenefitEmailIdentityTx(tx, &user)
			if err != nil {
				return err
			}
			emailHash = &hash
		}
		priorTopUp, err := hasSuccessfulNewUserBenefitTopUpTx(tx, userID)
		if err != nil {
			return err
		}
		if priorTopUp {
			return ErrNewUserBenefitIneligible
		}
		now := common.GetTimestamp()
		usage, err := newUserBenefitUsageTx(tx, now)
		if err != nil {
			return err
		}
		if newUserBenefitLimitReached(config, usage) {
			return ErrNewUserBenefitLimitReached
		}
		if config.NetworkLimitEnabled {
			var networkOrders int64
			if err := tx.Model(&NewUserBenefitOrder{}).Where("network_hash = ? AND created_at > ?", networkHash, now-24*60*60).Count(&networkOrders).Error; err != nil {
				return err
			}
			if networkOrders >= config.MaxOrdersPerNetwork24h {
				return ErrNewUserBenefitLimitReached
			}
		}
		paid, bonus, rate, unit, err := newUserBenefitQuote(config)
		if err != nil {
			return err
		}
		if user.Quota < 0 || user.Quota > common.MaxWalletQuota-paid-bonus {
			return ErrTopUpQuotaLimitExceeded
		}
		order = &NewUserBenefitOrder{PaymentGateway: risk.PaymentGateway, UserID: userID, TradeNo: tradeNo, PaymentProvider: PaymentProviderEpay,
			PaymentMethod: paymentMethod, MerchantScope: merchantScope, PayAmountCents: config.PayAmountCents,
			CreditAmountCents: config.CreditAmountCents, ConfigVersion: config.Version, USDExchangeRate: rate,
			QuotaPerUnit: unit, PaidQuota: paid, BonusQuota: bonus, CreatedAt: now, Status: NewUserBenefitStatusPending,
			EmailIdentityHash: emailHash, NetworkHash: networkHash,
			RequireVerifiedEmail: config.RequireVerifiedEmail, RequireTurnstile: config.RequireTurnstile,
			NetworkLimitEnabled: config.NetworkLimitEnabled, PaymentIdentityCheckEnabled: config.PaymentIdentityCheckEnabled, RiskPolicyVersion: 1}
		topUp := TopUp{NewUserBenefit: true, UserId: userID, Amount: 0,
			Money:   decimal.NewFromInt(config.PayAmountCents).Div(decimal.NewFromInt(100)).InexactFloat64(),
			TradeNo: tradeNo, PaymentMethod: paymentMethod, PaymentProvider: PaymentProviderEpay,
			CreateTime: now, Status: common.TopUpStatusPending}
		if err := tx.Create(&topUp).Error; err != nil {
			return err
		}
		return tx.Create(order).Error
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

// settleNewUserBenefitTx runs under RechargeEpay's top-up lock. The receipt,
// principal and payment completion commit together. Orders without a payer
// check also receive their promised bonus; checked orders create a durable
// pending review. The user lock serializes first-top-up reservations.
func settleNewUserBenefitTx(tx *gorm.DB, topUp *TopUp, paymentMethod string, callbackMoney decimal.Decimal, facts []VerifiedPaymentFacts) (alreadyDone bool, totalQuota, totalAfter int, err error) {
	var user User
	if err = lockForUpdate(tx).First(&user, topUp.UserId).Error; err != nil {
		return
	}
	var order NewUserBenefitOrder
	if err = tx.First(&order, topUp.UserId).Error; err != nil {
		return
	}
	if err = validateNewUserBenefitOrder(&order, topUp); err != nil {
		return
	}
	if !callbackMoney.Equal(decimal.NewFromInt(order.PayAmountCents).Div(decimal.NewFromInt(100))) {
		err = ErrPaymentAmountMismatch
		return
	}
	if len(facts) != 1 || paymentMethod != order.PaymentMethod {
		err = ErrNewUserBenefitPaymentRequired
		return
	}
	fact := facts[0]
	if fact.Provider != PaymentProviderEpay || fact.Environment != "live" || fact.Currency != "CNY" ||
		fact.CurrencyExponent != 2 || fact.PaidMinor != order.PayAmountCents || fact.MerchantScope != order.MerchantScope {
		err = ErrNewUserBenefitPaymentRequired
		return
	}
	previousReceiptID := topUp.PaymentReceiptID
	if topUp.Status == common.TopUpStatusSuccess && previousReceiptID <= 0 {
		err = ErrNewUserBenefitOrderConflict
		return
	}
	if err = applyVerifiedPaymentTx(tx, topUp, facts); err != nil {
		return
	}
	if topUp.Status == common.TopUpStatusSuccess {
		if previousReceiptID != topUp.PaymentReceiptID {
			err = ErrNewUserBenefitOrderConflict
			return
		}
		alreadyDone, totalQuota, totalAfter = true, order.PaidQuota+order.BonusQuota, user.Quota
		return
	}
	shares := []LedgerShare{{Account: AccountPaid, Amount: order.PaidQuota}}
	if order.BonusQuota > 0 && !order.PaymentIdentityCheckEnabled {
		shares = append(shares, LedgerShare{Account: AccountBonus, Amount: order.BonusQuota})
	}
	entry := LedgerEntry{UserId: order.UserID, Reason: LedgerReasonNewUserTopUp, ReferenceType: "new_user_topup",
		ReferenceId: order.TradeNo, IdempotencyKey: "credit:new_user_topup:" + order.TradeNo}
	entry.SetMetadata(map[string]any{"pay_amount_cents": order.PayAmountCents, "credit_amount_cents": order.CreditAmountCents,
		"currency": "CNY", "usd_exchange_rate": order.USDExchangeRate, "quota_per_unit": order.QuotaPerUnit, "config_version": order.ConfigVersion})
	var result *LedgerResult
	result, err = CreditQuotaSharesTx(tx, entry, shares)
	if err != nil {
		return
	}
	if result.Replayed {
		// A pending order with an already committed credit cannot be produced by
		// this transaction. Fail closed on inconsistent historical state.
		err = ErrNewUserBenefitOrderConflict
		return
	}
	if order.PaymentIdentityCheckEnabled {
		if err = tx.Create(&NewUserBenefitReward{UserID: order.UserID, TradeNo: order.TradeNo, Status: NewUserBenefitRewardPending, Reason: "payment_identity_pending"}).Error; err != nil {
			return
		}
	}
	topUp.Status, topUp.CompleteTime = common.TopUpStatusSuccess, common.GetTimestamp()
	if err = tx.Save(topUp).Error; err != nil {
		return
	}
	totalQuota, totalAfter = order.PaidQuota, result.TotalAfter
	if !order.PaymentIdentityCheckEnabled {
		totalQuota += order.BonusQuota
	}
	return
}
