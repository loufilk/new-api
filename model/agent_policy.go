package model

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAgentPolicyInvalid   = errors.New("Invalid agent policy")
	ErrAgentPolicyChanged   = errors.New("Agent policy changed; prepare the operation again")
	ErrAgentFeatureDisabled = errors.New("This agent financial feature is disabled")
)

// AgentPolicy is an immutable commercial snapshot. A zero-value policy enables
// no new financial product and does not alter existing wallet or code rights.
// Money is stored as integral minor units; its JSON representation is a decimal
// string so clients never silently round an external settlement amount.
type AgentPolicy struct {
	ID                                    int64  `json:"id" gorm:"primaryKey"`
	RequestID                             string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	CreatedBy                             int    `json:"created_by" gorm:"not null"`
	CreatedAt                             int64  `json:"created_at" gorm:"autoCreateTime"`
	Reason                                string `json:"reason" gorm:"type:varchar(255);not null"`
	RiskPolicyApproved                    bool   `json:"risk_policy_approved"`
	LegacyPurchaseReclassificationEnabled bool   `json:"legacy_purchase_reclassification_enabled"`
	PurchaseEnabled                       bool   `json:"purchase_enabled"`
	DeliveryEnabled                       bool   `json:"delivery_enabled"`
	PurchaseRefundTermsApproved           bool   `json:"purchase_refund_terms_approved"`
	PurchaseRefundEnabled                 bool   `json:"purchase_refund_enabled"`
	ProcurementSelfUseEnabled             bool   `json:"procurement_self_use_enabled"`
	PurchaseCurrency                      string `json:"purchase_currency" gorm:"type:varchar(3)"`
	PurchaseCurrencyExponent              int    `json:"purchase_currency_exponent"`
	PurchaseRateBPS                       int64  `json:"purchase_rate_bps"`
	PurchaseQuotaNumerator                int64  `json:"purchase_quota_numerator"`
	PurchaseQuotaDenominator              int64  `json:"purchase_quota_denominator"`
	PurchaseMinimumQuota                  int    `json:"purchase_minimum_quota"`
	PurchaseMaximumQuota                  int    `json:"purchase_maximum_quota"`
	QuoteLifetimeSeconds                  int64  `json:"quote_lifetime_seconds"`
	RefundWaitingSeconds                  int64  `json:"refund_waiting_seconds"`
	RefundFeeBPS                          int64  `json:"refund_fee_bps"`
	CommissionEnabled                     bool   `json:"commission_enabled"`
	CommissionCutoverAt                   int64  `json:"commission_cutover_at"`
	CommissionCurrency                    string `json:"commission_currency" gorm:"type:varchar(3)"`
	CommissionCurrencyExponent            int    `json:"commission_currency_exponent"`
	CommissionUserRateBPS                 int64  `json:"commission_user_rate_bps"`
	CommissionAgentRateBPS                int64  `json:"commission_agent_rate_bps"`
	CommissionReferralCapMinor            int64  `json:"commission_referral_cap_minor,string"`
	CommissionMaturitySeconds             int64  `json:"commission_maturity_seconds"`
	CommissionReserveBPS                  int64  `json:"commission_reserve_bps"`
	WithdrawalEnabled                     bool   `json:"withdrawal_enabled"`
	CommissionConversionEnabled           bool   `json:"commission_conversion_enabled"`
	WithdrawalMinimumMinor                int64  `json:"withdrawal_minimum_minor,string"`
	WithdrawalFeeBPS                      int64  `json:"withdrawal_fee_bps"`
	WithdrawalIntervalSeconds             int64  `json:"withdrawal_interval_seconds"`
	WithdrawalCalendarMonthLimit          int    `json:"withdrawal_calendar_month_limit"`
	ConversionQuotaNumerator              int64  `json:"conversion_quota_numerator"`
	ConversionQuotaDenominator            int64  `json:"conversion_quota_denominator"`
}

// AgentPolicyState is only a pointer to an immutable version, not a balance.
// Every financial mutation locks it BEFORE its business/fund/user rows. This
// serializes policy changes with an operation's final authorization check.
type AgentPolicyState struct {
	ID                  int   `json:"-" gorm:"primaryKey;autoIncrement:false"`
	CurrentPolicyID     int64 `json:"current_policy_id"`
	CommissionCutoverAt int64 `json:"commission_cutover_at"`
	UpdatedAt           int64 `json:"updated_at"`
}

func (*AgentPolicy) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentPolicy) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

// AgentCurrencyExponent is the explicit minor-unit catalog supported by the
// new cash products. Unknown codes are refused, never assigned a guessed scale.
// Existing wallet quota and historical immutable evidence are not converted.
func AgentCurrencyExponent(currency string) (int, bool) {
	switch currency {
	case "USD", "CNY", "EUR", "GBP", "HKD", "CAD", "AUD", "SGD", "NZD", "CHF":
		return 2, true
	case "JPY", "KRW":
		return 0, true
	case "BHD", "JOD", "KWD", "OMR", "TND":
		return 3, true
	default:
		return 0, false
	}
}

func validAgentCurrency(currency string, exponent int) bool {
	expected, ok := AgentCurrencyExponent(currency)
	return ok && exponent == expected
}

func ValidateAgentPolicy(policy *AgentPolicy) error {
	if policy == nil || len(strings.TrimSpace(policy.Reason)) == 0 || len(policy.Reason) > 255 {
		return ErrAgentPolicyInvalid
	}
	for _, value := range []int64{policy.PurchaseRateBPS, policy.RefundFeeBPS, policy.CommissionUserRateBPS, policy.CommissionAgentRateBPS, policy.CommissionReserveBPS, policy.WithdrawalFeeBPS} {
		if value < 0 || value > 10_000 {
			return ErrAgentPolicyInvalid
		}
	}
	for _, value := range []int64{policy.PurchaseQuotaNumerator, policy.PurchaseQuotaDenominator, policy.CommissionReferralCapMinor, policy.WithdrawalMinimumMinor, policy.ConversionQuotaNumerator, policy.ConversionQuotaDenominator} {
		if value < 0 || value > int64(common.MaxWalletQuota) {
			return ErrAgentPolicyInvalid
		}
	}
	for _, value := range []int64{policy.RefundWaitingSeconds, policy.CommissionMaturitySeconds, policy.WithdrawalIntervalSeconds} {
		if value < 0 || value > 10*366*24*60*60 {
			return ErrAgentPolicyInvalid
		}
	}
	if policy.WithdrawalCalendarMonthLimit < 0 || policy.WithdrawalCalendarMonthLimit > 1000 || policy.PurchaseMinimumQuota < 0 || policy.PurchaseMaximumQuota < 0 || policy.PurchaseMaximumQuota > common.MaxWalletQuota || policy.CommissionCutoverAt < 0 {
		return ErrAgentPolicyInvalid
	}
	if policy.PurchaseCurrency != "" && !validAgentCurrency(policy.PurchaseCurrency, policy.PurchaseCurrencyExponent) {
		return ErrAgentPolicyInvalid
	}
	if policy.CommissionCurrency != "" && !validAgentCurrency(policy.CommissionCurrency, policy.CommissionCurrencyExponent) {
		return ErrAgentPolicyInvalid
	}
	if policy.LegacyPurchaseReclassificationEnabled || policy.PurchaseEnabled || policy.DeliveryEnabled || policy.PurchaseRefundEnabled || policy.ProcurementSelfUseEnabled || policy.CommissionEnabled || policy.WithdrawalEnabled || policy.CommissionConversionEnabled {
		if !policy.RiskPolicyApproved {
			return ErrAgentPolicyInvalid
		}
	}
	if (policy.LegacyPurchaseReclassificationEnabled || policy.PurchaseEnabled || policy.PurchaseRefundEnabled) && !policy.PurchaseRefundTermsApproved {
		return ErrAgentPolicyInvalid
	}
	if policy.PurchaseEnabled {
		if !validAgentCurrency(policy.PurchaseCurrency, policy.PurchaseCurrencyExponent) || policy.PurchaseRateBPS <= 0 || policy.PurchaseQuotaNumerator <= 0 || policy.PurchaseQuotaDenominator <= 0 || policy.PurchaseMinimumQuota <= 0 || policy.PurchaseMaximumQuota < policy.PurchaseMinimumQuota || policy.QuoteLifetimeSeconds < 60 || policy.QuoteLifetimeSeconds > 3600 {
			return ErrAgentPolicyInvalid
		}
	}
	if policy.CommissionEnabled {
		if !validAgentCurrency(policy.CommissionCurrency, policy.CommissionCurrencyExponent) || policy.CommissionReferralCapMinor <= 0 || policy.CommissionUserRateBPS+policy.CommissionAgentRateBPS == 0 {
			return ErrAgentPolicyInvalid
		}
	}
	if policy.WithdrawalEnabled || policy.CommissionConversionEnabled {
		if !validAgentCurrency(policy.CommissionCurrency, policy.CommissionCurrencyExponent) {
			return ErrAgentPolicyInvalid
		}
	}
	if policy.WithdrawalEnabled && (policy.WithdrawalMinimumMinor <= 0 || policy.WithdrawalCalendarMonthLimit <= 0) {
		return ErrAgentPolicyInvalid
	}
	if policy.CommissionConversionEnabled && (policy.ConversionQuotaNumerator <= 0 || policy.ConversionQuotaDenominator <= 0) {
		return ErrAgentPolicyInvalid
	}
	return nil
}

// LockCurrentAgentPolicyTx must be the first lock acquired by financial writers.
// A missing pointer means the legacy-only, all-new-entrypoints-disabled policy.
func LockCurrentAgentPolicyTx(tx *gorm.DB) (*AgentPolicy, error) {
	state := AgentPolicyState{ID: 1}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).First(&state, 1).Error; err != nil {
		return nil, err
	}
	if state.CurrentPolicyID == 0 {
		return &AgentPolicy{}, nil
	}
	var policy AgentPolicy
	if err := tx.First(&policy, state.CurrentPolicyID).Error; err != nil {
		return nil, err
	}
	return &policy, nil
}

func GetCurrentAgentPolicy() (*AgentPolicy, error) {
	return ReadCurrentAgentPolicyTx(DB)
}

// ReadCurrentAgentPolicyTx freezes the immutable version observed by a payment
// outbox event without acquiring the policy lock after a principal wallet lock.
func ReadCurrentAgentPolicyTx(tx *gorm.DB) (*AgentPolicy, error) {
	var state AgentPolicyState
	if err := tx.First(&state, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &AgentPolicy{}, nil
		}
		return nil, err
	}
	if state.CurrentPolicyID == 0 {
		return &AgentPolicy{}, nil
	}
	var policy AgentPolicy
	if err := tx.First(&policy, state.CurrentPolicyID).Error; err != nil {
		return nil, err
	}
	return &policy, nil
}

// AgentMoneyRatio computes a bounded exact product/division without an
// intermediate overflow. roundUp is used for a price quote, never commission.
func AgentMoneyRatio(amount, numerator, denominator int64, roundUp bool) (int64, error) {
	if amount < 0 || numerator < 0 || denominator <= 0 || amount > int64(common.MaxWalletQuota) || numerator > int64(common.MaxWalletQuota) || denominator > int64(common.MaxWalletQuota) {
		return 0, ErrAgentPolicyInvalid
	}
	product := new(big.Int).Mul(big.NewInt(amount), big.NewInt(numerator))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(product, big.NewInt(denominator), remainder)
	if roundUp && remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() || quotient.Sign() < 0 || quotient.Int64() > int64(common.MaxWalletQuota) {
		return 0, ErrWalletQuotaLimitExceeded
	}
	return quotient.Int64(), nil
}

// AgentPolicyChange binds a publication request to its exact proposed policy.
// It is separate from the immutable version so an exact replay still succeeds
// after the current pointer advances again.
type AgentPolicyChange struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	RequestID   string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	ActorUserID int    `json:"actor_user_id" gorm:"not null"`
	Signature   string `json:"-" gorm:"type:char(64);not null"`
	PolicyID    int64  `json:"policy_id" gorm:"not null"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentPolicyChange) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentPolicyChange) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentPolicyPublishRequest struct {
	RequestID       string      `json:"request_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Policy          AgentPolicy `json:"policy"`
}

func normalizeAgentPolicyPublication(request AgentPolicyPublishRequest) (AgentPolicyPublishRequest, error) {
	if validateAgentRequestID(request.RequestID) != nil || request.ExpectedVersion < 0 {
		return request, ErrAgentPolicyInvalid
	}
	request.Policy.ID = 0
	request.Policy.RequestID = request.RequestID
	request.Policy.CreatedAt = 0
	request.Policy.CreatedBy = 0
	request.Policy.Reason = strings.TrimSpace(request.Policy.Reason)
	if err := ValidateAgentPolicy(&request.Policy); err != nil {
		return request, err
	}
	return request, nil
}

func agentPolicyPublicationContext(tx *gorm.DB, identity AuthSessionIdentity, current *AgentPolicy, request AgentPolicyPublishRequest) (AgentTransactionContext, error) {
	var actor User
	if err := tx.Select("id", "role", "status").First(&actor, identity.UserID).Error; err != nil {
		return AgentTransactionContext{}, err
	}
	if actor.Role != common.RoleRootUser || actor.Status != common.UserStatusEnabled {
		return AgentTransactionContext{}, ErrAgentProfilePermission
	}
	if current.ID != request.ExpectedVersion {
		return AgentTransactionContext{}, ErrAgentPolicyChanged
	}
	var state AgentPolicyState
	if err := tx.First(&state, 1).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentTransactionContext{}, err
	}
	if request.Policy.CommissionCutoverAt != 0 && request.Policy.CommissionCutoverAt != state.CommissionCutoverAt {
		return AgentTransactionContext{}, ErrAgentPolicyInvalid
	}
	profile, err := readAgentProfile(tx, identity.UserID, false)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	version := int64(0)
	if profile != nil {
		version = profile.Version
	}
	hash, err := AgentTransactionParametersHash(request)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	return AgentTransactionContext{Action: "policy.publish", RequestID: request.RequestID, AgentUserID: identity.UserID, ProfileVersion: version, PolicyVersion: current.ID, AmountMinor: "0", TargetType: "agent_policy", TargetID: "current", TargetVersion: current.ID, ParametersHash: hash}, nil
}

func PrepareAgentPolicyOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	if action != "policy.publish" {
		return nil, ErrAgentTransactionContext
	}
	var request AgentPolicyPublishRequest
	if err := common.DecodeJsonStrict(bytes.NewReader(payload), &request); err != nil {
		return nil, ErrAgentPolicyInvalid
	}
	request.RequestID = requestID
	request, err := normalizeAgentPolicyPublication(request)
	if err != nil {
		return nil, err
	}
	current, err := ReadCurrentAgentPolicyTx(DB)
	if err != nil {
		return nil, err
	}
	context, err := agentPolicyPublicationContext(DB, identity, current, request)
	if err != nil {
		return nil, err
	}
	return &AgentPreparedOperation{VerificationContext: context, Summary: AgentOperationSummary{Action: action, AgentUserID: identity.UserID, AmountMinor: "0", FeeMinor: "0", NetMinor: "0", TargetLabel: "Agent policy version", PolicyVersion: current.ID}, StatusURL: "/api/agent-platform/operations/" + requestID}, nil
}

func PublishAgentPolicy(identity AuthSessionIdentity, request AgentPolicyPublishRequest, authorization AgentTransactionAuthorization) (*AgentPolicy, bool, error) {
	request, err := normalizeAgentPolicyPublication(request)
	if err != nil {
		return nil, false, err
	}
	if authorization.Identity() != identity {
		return nil, false, ErrAgentTransactionProof
	}
	signature, err := AgentTransactionParametersHash(request)
	if err != nil {
		return nil, false, err
	}
	var result AgentPolicy
	replayed := false
	err = DB.Transaction(func(tx *gorm.DB) error {
		current, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		var existing AgentPolicyChange
		err = tx.Where("request_id = ?", request.RequestID).First(&existing).Error
		if err == nil {
			if existing.ActorUserID != identity.UserID || existing.Signature != signature {
				return ErrAgentQuotaConflict
			}
			var actor User
			if err := lockForUpdate(tx).First(&actor, identity.UserID).Error; err != nil {
				return err
			}
			if actor.Role != common.RoleRootUser || actor.Status != common.UserStatusEnabled {
				return ErrAgentProfilePermission
			}
			if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
				return err
			}
			replayed = true
			return tx.First(&result, existing.PolicyID).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		context, err := agentPolicyPublicationContext(tx, identity, current, request)
		if err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, context); err != nil {
			return err
		}
		var state AgentPolicyState
		if err := tx.First(&state, 1).Error; err != nil {
			return err
		}
		result = request.Policy
		result.CreatedBy = identity.UserID
		result.CreatedAt = common.GetTimestamp()
		result.CommissionCutoverAt = state.CommissionCutoverAt
		if result.CommissionEnabled && result.CommissionCutoverAt == 0 {
			result.CommissionCutoverAt = result.CreatedAt
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if err := tx.Create(&AgentPolicyChange{RequestID: request.RequestID, ActorUserID: identity.UserID, Signature: signature, PolicyID: result.ID}).Error; err != nil {
			return err
		}
		updated := tx.Model(&AgentPolicyState{}).Where("id = ? AND current_policy_id = ?", 1, current.ID).Updates(map[string]any{"current_policy_id": result.ID, "commission_cutover_at": result.CommissionCutoverAt, "updated_at": result.CreatedAt})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrAgentPolicyChanged
		}
		return nil
	})
	return &result, replayed, err
}

func FindAgentPolicyOperation(identity AuthSessionIdentity, requestID string) (*AgentOperationStatus, error) {
	var result *AgentOperationStatus
	err := DB.Transaction(func(tx *gorm.DB) error {
		var change AgentPolicyChange
		if err := tx.Where("request_id = ? AND actor_user_id = ?", requestID, identity.UserID).First(&change).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		// The actor's historical receipt does not grant perpetual access to private
		// financial policy audit data after administrator/root privileges are lost.
		var actor User
		if err := lockForUpdate(tx).First(&actor, identity.UserID).Error; err != nil {
			return err
		}
		if actor.Status != common.UserStatusEnabled || actor.Role != common.RoleRootUser {
			return ErrAgentProfilePermission
		}
		if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
			return err
		}
		var policy AgentPolicy
		if err := tx.First(&policy, change.PolicyID).Error; err != nil {
			return err
		}
		result = &AgentOperationStatus{RequestID: requestID, Action: "policy.publish", Status: "completed", ResourceType: "agent_policy", ResourceID: fmt.Sprint(policy.ID), ResourceState: "published", Resource: &policy}
		return nil
	})
	return result, err
}
