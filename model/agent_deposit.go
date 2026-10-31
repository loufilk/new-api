package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var agentDepositActionFields = map[string]string{
	"deposit.receipt.record":  "agent_user_id amount_minor currency currency_exponent provider merchant_scope environment external_reference paid_at evidence_reference payer_reference",
	"deposit.receipt.confirm": "resource_id version evidence_reference payer_reference",
	"deposit.offset.request":  "deposit_id amount_minor allocations reason",
	"deposit.refund.request":  "deposit_id amount_minor reason",
	"deposit.offset.approve":  "resource_id version reason evidence_reference",
	"deposit.offset.reject":   "resource_id version reason evidence_reference",
	"deposit.refund.approve":  "resource_id version reason evidence_reference",
	"deposit.refund.reject":   "resource_id version reason evidence_reference",
	"deposit.refund.submit":   "resource_id version business_key evidence_reference",
	"deposit.refund.resolve":  "resource_id version business_key outcome evidence_reference provider merchant_scope environment external_reference paid_at no_payment_confirmed",
}

func agentDepositAdminAction(action string) bool {
	return action != "deposit.offset.request" && action != "deposit.refund.request"
}
func validateAgentDepositCommand(action string, c AgentDepositCommand) error {
	allowed, ok := agentDepositActionFields[action]
	if !ok || validateAgentRequestID(c.RequestID) != nil || c.AgentUserID < 0 || c.ResourceID < 0 || c.DepositID < 0 || c.Version < 0 {
		return ErrAgentDepositInvalid
	}
	if err := validateAgentFinanceFields(c, allowed); err != nil {
		return err
	}
	for _, v := range []string{c.Reason, c.EvidenceReference, c.MerchantScope, c.ExternalReference, c.BusinessKey} {
		if v != "" && !validAgentFinanceText(v, 255) {
			return ErrAgentDepositInvalid
		}
	}
	return nil
}
func decodeAgentDepositCommand(action, requestID string, payload []byte) (AgentDepositCommand, error) {
	var c AgentDepositCommand
	if err := common.DecodeJsonStrict(bytes.NewReader(payload), &c); err != nil {
		return c, ErrAgentDepositInvalid
	}
	if c.RequestID != "" && c.RequestID != requestID {
		return c, ErrAgentDepositInvalid
	}
	c.RequestID = requestID
	return c, validateAgentDepositCommand(action, c)
}
func validateAgentDepositAccount(a *AgentDepositAccount) error {
	if a.AgentUserID <= 0 || a.Version <= 0 || !validAgentCurrency(a.Currency, a.CurrencyExponent) || a.ReceivedMinor < 0 || a.ReceivedMinor > int64(common.MaxWalletQuota) {
		return ErrAgentDepositInvariant
	}
	left := a.ReceivedMinor
	for _, v := range []int64{a.AvailableMinor, a.HeldMinor, a.OffsetMinor, a.RefundedMinor} {
		if v < 0 || v > left {
			return ErrAgentDepositInvariant
		}
		left -= v
	}
	if left != 0 {
		return ErrAgentDepositInvariant
	}
	return nil
}
func validateAgentDepositReceipt(r *AgentDepositReceipt) error {
	digest := AgentDepositPayerDigest(r.PayerReference)
	if !validAgentDossierReference(r.PayerReference) || r.PayerDigest != digest {
		return ErrAgentDepositInvariant
	}
	if r.AmountMinor <= 0 || r.AmountMinor > int64(common.MaxWalletQuota) || !validAgentCurrency(r.Currency, r.CurrencyExponent) || r.PaymentReceiptID <= 0 {
		return ErrAgentDepositInvariant
	}
	total := r.AmountMinor
	if r.State == "recorded" {
		total = 0
	} else if r.State != "confirmed" {
		return ErrAgentDepositInvariant
	}
	for _, v := range []int64{r.AvailableMinor, r.HeldMinor, r.OffsetMinor, r.RefundedMinor} {
		if v < 0 || v > total {
			return ErrAgentDepositInvariant
		}
		total -= v
	}
	if total != 0 {
		return ErrAgentDepositInvariant
	}
	return nil
}
func lockAgentDepositAccountTx(tx *gorm.DB, owner int, currency string, exponent int, create bool) (*AgentDepositAccount, error) {
	if owner <= 0 || !validAgentCurrency(currency, exponent) {
		return nil, ErrAgentDepositInvalid
	}
	if create {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&AgentDepositAccount{AgentUserID: owner, Currency: currency, CurrencyExponent: exponent, Version: 1}).Error; err != nil {
			return nil, err
		}
	}
	var a AgentDepositAccount
	if err := lockForUpdate(tx).Where("agent_user_id = ?", owner).First(&a).Error; err != nil {
		return nil, err
	}
	if a.Currency != currency || a.CurrencyExponent != exponent {
		return nil, ErrAgentDepositInvariant
	}
	return &a, validateAgentDepositAccount(&a)
}
func appendAgentDepositLedgerTx(tx *gorm.DB, a *AgentDepositAccount, r *AgentDepositReceipt, e AgentDepositLedger) error {
	if err := validateAgentDepositAccount(a); err != nil {
		return err
	}
	a.ReceivedMinor += e.ReceivedDelta
	a.AvailableMinor += e.AvailableDelta
	a.HeldMinor += e.HeldDelta
	a.OffsetMinor += e.OffsetDelta
	a.RefundedMinor += e.RefundedDelta
	a.Version++
	r.AvailableMinor += e.AvailableDelta
	r.HeldMinor += e.HeldDelta
	r.OffsetMinor += e.OffsetDelta
	r.RefundedMinor += e.RefundedDelta
	r.Version++
	if err := validateAgentDepositAccount(a); err != nil {
		return err
	}
	if err := validateAgentDepositReceipt(r); err != nil {
		return err
	}
	e.AgentUserID = a.AgentUserID
	e.DepositID = r.ID
	e.Currency = a.Currency
	e.CurrencyExponent = a.CurrencyExponent
	e.ReceivedAfter = a.ReceivedMinor
	e.AvailableAfter = a.AvailableMinor
	e.HeldAfter = a.HeldMinor
	e.OffsetAfter = a.OffsetMinor
	e.RefundedAfter = a.RefundedMinor
	if err := tx.Create(&e).Error; err != nil {
		return err
	}
	if err := tx.Model(a).Select("received_minor", "available_minor", "held_minor", "offset_minor", "refunded_minor", "version").Updates(a).Error; err != nil {
		return err
	}
	return tx.Model(r).Select("available_minor", "held_minor", "offset_minor", "refunded_minor", "version", "state", "confirmed_by").Updates(r).Error
}

type agentDepositPlan struct {
	payerDigest string
	context     AgentTransactionContext
	owner       int
	currency    string
	exponent    int
	amount      int64
	deposit     AgentDepositReceipt
	request     AgentDepositOperationRequest
	allocations []AgentReceivableAllocationInput
}

func buildAgentDepositPlanTx(tx *gorm.DB, p *AgentPolicy, id AuthSessionIdentity, action string, c AgentDepositCommand) (*agentDepositPlan, error) {
	if err := validateAgentDepositCommand(action, c); err != nil {
		return nil, err
	}
	plan := &agentDepositPlan{owner: c.AgentUserID, currency: c.Currency, exponent: c.CurrencyExponent}
	target, version := c.ResourceID, c.Version
	switch action {
	case "deposit.receipt.record":
		if !p.DepositEnabled || ValidateAgentDepositPolicy(p) != nil {
			return nil, ErrAgentFeatureDisabled
		}
		if c.AgentUserID <= 0 || c.AgentUserID == id.UserID {
			return nil, ErrAgentPurchaseReview
		}
		amount, err := agentCreditMinor(c.AmountMinor, true)
		if err != nil {
			return nil, err
		}
		plan.amount = amount
		if !validAgentDossierReference(c.PayerReference) {
			return nil, ErrAgentDepositInvalid
		}
		plan.payerDigest = AgentDepositPayerDigest(c.PayerReference)
		if c.Currency != p.DepositCurrency || c.CurrencyExponent != p.DepositCurrencyExponent || c.Provider != "bank_transfer" || c.Environment != "live" || !validAgentFinanceText(c.MerchantScope, 128) || !validAgentFinanceText(c.ExternalReference, 255) || !validAgentFinanceText(c.EvidenceReference, 255) || c.PaidAt <= 0 || c.PaidAt > common.GetTimestamp() {
			return nil, ErrAgentDepositInvalid
		}
		var account AgentDepositAccount
		err = tx.Where("agent_user_id = ?", plan.owner).First(&account).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil && (validateAgentDepositAccount(&account) != nil || account.Currency != c.Currency || account.CurrencyExponent != c.CurrencyExponent) {
			return nil, ErrAgentDepositInvariant
		}
		var pending []AgentDepositReceipt
		if err := tx.Where("agent_user_id = ? AND state = ?", plan.owner, "recorded").Find(&pending).Error; err != nil {
			return nil, err
		}
		available := p.DepositMaximumMinor - account.AvailableMinor - account.HeldMinor
		for _, r := range pending {
			if r.AmountMinor > available || validateAgentDepositReceipt(&r) != nil {
				return nil, ErrAgentDepositInvariant
			}
			available -= r.AmountMinor
		}
		if plan.amount > available {
			return nil, ErrAgentCreditRisk
		}
	case "deposit.receipt.confirm":
		if err := lockForUpdate(tx).First(&plan.deposit, c.ResourceID).Error; err != nil {
			return nil, err
		}
		r := &plan.deposit
		plan.owner = r.AgentUserID
		plan.currency = r.Currency
		plan.exponent = r.CurrencyExponent
		plan.amount = r.AmountMinor
		if !validAgentDossierReference(c.PayerReference) || c.PayerReference != r.PayerReference {
			return nil, ErrAgentDepositInvalid
		}
		plan.payerDigest = r.PayerDigest
		if r.State != "recorded" || r.Version != c.Version {
			return nil, ErrAgentCreditState
		}
		if id.UserID == r.AgentUserID || id.UserID == r.RecordedBy || !validAgentFinanceText(c.EvidenceReference, 255) {
			return nil, ErrAgentPurchaseReview
		}
		if err := validateAgentDepositReceipt(r); err != nil {
			return nil, err
		}
	case "deposit.offset.request", "deposit.refund.request":
		if err := lockForUpdate(tx).First(&plan.deposit, c.DepositID).Error; err != nil {
			return nil, err
		}
		r := &plan.deposit
		plan.owner = r.AgentUserID
		plan.currency = r.Currency
		plan.exponent = r.CurrencyExponent
		target = r.ID
		version = r.Version
		if id.UserID != r.AgentUserID {
			return nil, ErrAgentProfilePermission
		}
		if !p.DepositEnabled || !p.DepositTermsApproved || !p.RiskPolicyApproved || (action == "deposit.offset.request" && !p.DepositOffsetEnabled) || (action == "deposit.refund.request" && !p.DepositRefundEnabled) {
			return nil, ErrAgentFeatureDisabled
		}
		amount, err := agentCreditMinor(c.AmountMinor, true)
		if err != nil {
			return nil, err
		}
		plan.amount = amount
		if err := validateAgentDepositReceipt(r); err != nil {
			return nil, err
		}
		if r.State != "confirmed" || amount > r.AvailableMinor || !validAgentFinanceText(c.Reason, 255) {
			return nil, ErrAgentDepositInvalid
		}
		if action == "deposit.offset.request" {
			if _, err := agentCreditAllocationsTx(tx, plan.owner, plan.currency, plan.exponent, amount, c.Allocations); err != nil {
				return nil, err
			}
			plan.allocations = c.Allocations
		}
	default:
		if err := lockForUpdate(tx).First(&plan.request, c.ResourceID).Error; err != nil {
			return nil, err
		}
		r := &plan.request
		if err := lockForUpdate(tx).First(&plan.deposit, r.DepositID).Error; err != nil {
			return nil, err
		}
		plan.owner = r.AgentUserID
		plan.currency = r.Currency
		plan.exponent = r.CurrencyExponent
		plan.amount = r.AmountMinor
		if r.Version != c.Version || !strings.HasPrefix(action, "deposit."+r.Kind+".") {
			return nil, ErrAgentCreditState
		}
		if id.UserID == r.RequestedBy || id.UserID == r.AgentUserID {
			return nil, ErrAgentPurchaseReview
		}
		if !validAgentFinanceText(c.EvidenceReference, 255) {
			return nil, ErrAgentDepositInvalid
		}
		if err := validateAgentDepositReceipt(&plan.deposit); err != nil {
			return nil, err
		}
		if r.AmountMinor <= 0 || r.AmountMinor > plan.deposit.HeldMinor || r.Currency != plan.deposit.Currency || r.AgentUserID != plan.deposit.AgentUserID {
			return nil, ErrAgentDepositInvariant
		}
		switch action {
		case "deposit.offset.approve", "deposit.offset.reject", "deposit.refund.approve", "deposit.refund.reject":
			if r.State != "requested" || !validAgentFinanceText(c.Reason, 255) {
				return nil, ErrAgentCreditState
			}
			if action == "deposit.offset.approve" {
				if !p.DepositOffsetEnabled || !p.DepositTermsApproved || !p.RiskPolicyApproved {
					return nil, ErrAgentFeatureDisabled
				}
				if common.UnmarshalJsonStr(r.AllocationSnapshot, &plan.allocations) != nil {
					return nil, ErrAgentDepositInvariant
				}
				if _, err := agentCreditAllocationsTx(tx, r.AgentUserID, r.Currency, r.CurrencyExponent, r.AmountMinor, plan.allocations); err != nil {
					return nil, err
				}
			}
			if action == "deposit.refund.approve" && !p.DepositRefundEnabled {
				return nil, ErrAgentFeatureDisabled
			}
		case "deposit.refund.submit":
			if !p.DepositRefundEnabled {
				return nil, ErrAgentFeatureDisabled
			}
			if r.State != "approved" || c.BusinessKey != r.BusinessKey {
				return nil, ErrAgentCreditState
			}
		case "deposit.refund.resolve":
			if (r.State != "payment_pending" && r.State != "payment_unknown") || c.BusinessKey != r.BusinessKey {
				return nil, ErrAgentCreditState
			}
			if id.UserID == r.SubmittedBy {
				return nil, ErrAgentPurchaseReview
			}
			switch c.Outcome {
			case "success":
				if r.SubmittedAt <= 0 || c.PaidAt < r.SubmittedAt || c.PaidAt > common.GetTimestamp() || c.NoPaymentConfirmed || c.Provider != "bank_transfer" || c.Environment != "live" || !validAgentFinanceText(c.MerchantScope, 128) || !validAgentFinanceText(c.ExternalReference, 255) {
					return nil, ErrAgentDepositInvalid
				}
			case "failure":
				if c.PaidAt != 0 || !c.NoPaymentConfirmed || c.Provider != "" || c.Environment != "" || c.MerchantScope != "" || c.ExternalReference != "" {
					return nil, ErrAgentRefundUnknown
				}
			case "unknown":
				if c.PaidAt != 0 || c.NoPaymentConfirmed || c.Provider != "" || c.Environment != "" || c.MerchantScope != "" || c.ExternalReference != "" {
					return nil, ErrAgentDepositInvalid
				}
			default:
				return nil, ErrAgentDepositInvalid
			}
		}
	}
	if plan.deposit.ID > 0 {
		var receipt AgentPaymentReceipt
		if err := tx.First(&receipt, plan.deposit.PaymentReceiptID).Error; err != nil {
			return nil, err
		}
		if ValidateAgentPaymentReceipt(&receipt) != nil || receipt.Provider != "bank_transfer" || receipt.Environment != "live" || receipt.Purpose != "deposit" || receipt.OwnerUserID != plan.deposit.AgentUserID || receipt.Currency != plan.deposit.Currency || receipt.CurrencyExponent != plan.deposit.CurrencyExponent || receipt.PaidMinor != plan.deposit.AmountMinor || receipt.VerifiedBy != plan.deposit.RecordedBy || receipt.CounterpartyDigest != plan.deposit.PayerDigest {
			return nil, ErrAgentDepositInvariant
		}
	}
	if action == "deposit.receipt.record" || action == "deposit.offset.approve" || action == "deposit.refund.approve" || action == "deposit.refund.submit" {
		profile, err := readAgentProfile(tx, plan.owner, false)
		if err != nil {
			return nil, err
		}
		if !profile.Allows(AgentCapabilitySettlement) || (action == "deposit.receipt.record" && !profile.Allows(AgentCapabilitySales)) {
			return nil, ErrAgentProfilePermission
		}
	}
	targetID := strconv.FormatInt(target, 10)
	if target == 0 {
		targetID = "new:" + c.RequestID
	}
	context, err := agentBusinessContextTx(tx, p, plan.owner, action, c.RequestID, plan.currency, strconv.FormatInt(plan.amount, 10), 0, "deposit", targetID, version, struct {
		Command     AgentDepositCommand              `json:"command"`
		Allocations []AgentReceivableAllocationInput `json:"allocations,omitempty"`
		PayerDigest string                           `json:"payer_digest"`
		State       string                           `json:"state"`
		BusinessKey string                           `json:"business_key"`
	}{c, plan.allocations, plan.payerDigest, plan.request.State, plan.request.BusinessKey})
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(action, "deposit.refund.") {
		context.RecipientVersion = 1
		context.RecipientDigest = plan.deposit.PayerDigest
		if context.RecipientDigest == "" {
			return nil, ErrAgentDepositInvariant
		}
	}
	if plan.request.ID > 0 && plan.request.Kind == "refund" && (plan.request.RecipientVersion != context.RecipientVersion || plan.request.RecipientDigest != context.RecipientDigest) {
		return nil, ErrAgentDepositInvariant
	}
	plan.context = context
	return plan, nil
}
func PrepareAgentDepositOperation(id AuthSessionIdentity, action, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	if _, ok := agentDepositActionFields[action]; !ok {
		return nil, nil
	}
	c, err := decodeAgentDepositCommand(action, requestID, payload)
	if err != nil {
		return nil, err
	}
	var out *AgentPreparedOperation
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		p, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		plan, err := buildAgentDepositPlanTx(tx, p, id, action, c)
		if err != nil {
			return err
		}
		cap := AgentCapabilitySettlement
		if agentDepositAdminAction(action) {
			cap = AgentCapabilityAdmin
		}
		if _, err := RequireAgentCapabilityTx(tx, id, plan.owner, plan.context.ProfileVersion, cap); err != nil {
			return err
		}
		out = &AgentPreparedOperation{VerificationContext: plan.context, Summary: AgentOperationSummary{Action: action, AgentUserID: plan.owner, Currency: plan.currency, CurrencyExponent: plan.exponent, AmountMinor: plan.context.AmountMinor, NetMinor: plan.context.AmountMinor, FeeMinor: "0", TargetLabel: "Separate deposit cash liability", RecipientLabel: "Original verified deposit sender", PolicyVersion: p.ID}, StatusURL: "/api/agent-platform/operations/" + requestID}
		return nil
	})
	return out, err
}
func ReplayAgentDepositOperation(id AuthSessionIdentity, action, requestID string, payload []byte) (any, bool, error) {
	if _, ok := agentDepositActionFields[action]; !ok {
		return nil, false, nil
	}
	c, err := decodeAgentDepositCommand(action, requestID, payload)
	if err != nil {
		return nil, false, err
	}
	var result *AgentOperationStatus
	var found bool
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		var err error
		result, found, err = agentFinanceOperationReplayTx(tx, id, action, requestID, c)
		return err
	})
	return result, found, err
}
func FindAgentDepositOperation(id AuthSessionIdentity, requestID string) (*AgentOperationStatus, error) {
	return FindAgentCreditOperation(id, requestID)
}

// AgentDepositPayerDigest fingerprints an exact controlled payer dossier ID, never a bank account or a synthetic receipt identity.
func AgentDepositPayerDigest(reference string) string {
	digest := sha256.Sum256([]byte(reference))
	return hex.EncodeToString(digest[:])
}
