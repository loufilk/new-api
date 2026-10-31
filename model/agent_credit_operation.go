package model

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var agentCreditActionFields = map[string]string{
	"credit.limit.request":        "agent_user_id amount_minor term_seconds expires_at expected_limit_id frozen reason evidence_reference",
	"credit.limit.approve":        "resource_id version risk_approved evidence_reference",
	"credit.limit.reject":         "resource_id version reason",
	"credit.draw":                 "agent_user_id quota expected_limit_id",
	"credit.release":              "resource_id quota",
	"credit.invoice.issue":        "resource_id version invoice_number evidence_reference",
	"credit.repayment.record":     "agent_user_id amount_minor currency currency_exponent provider merchant_scope environment external_reference paid_at evidence_reference allocations",
	"credit.repayment.confirm":    "resource_id version evidence_reference",
	"credit.repayment.reallocate": "resource_id version allocations evidence_reference",
	"credit.adjustment.request":   "resource_id version amount_minor adjustment_kind reason evidence_reference",
	"credit.adjustment.approve":   "resource_id version reason evidence_reference",
	"credit.adjustment.reject":    "resource_id version reason evidence_reference",
}

func validateAgentFinanceFields(command any, allowed string) error {
	b, err := common.Marshal(command)
	if err != nil {
		return err
	}
	var fields map[string]any
	if err := common.Unmarshal(b, &fields); err != nil {
		return err
	}
	accepted := " request_id " + allowed + " "
	for key := range fields {
		if !strings.Contains(accepted, " "+key+" ") {
			return ErrAgentCreditInvalid
		}
	}
	return nil
}
func validAgentFinanceText(value string, max int) bool {
	if value == "" || len(value) > max || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func decodeAgentCreditCommand(action, requestID string, payload []byte) (AgentCreditCommand, error) {
	var c AgentCreditCommand
	if _, ok := agentCreditActionFields[action]; !ok {
		return c, ErrAgentCreditInvalid
	}
	if err := common.DecodeJsonStrict(bytes.NewReader(payload), &c); err != nil {
		return c, ErrAgentCreditInvalid
	}
	if c.RequestID != "" && c.RequestID != requestID {
		return c, ErrAgentCreditInvalid
	}
	c.RequestID = requestID
	return c, validateAgentCreditCommand(action, c)
}
func validateAgentCreditCommand(action string, c AgentCreditCommand) error {
	fields, ok := agentCreditActionFields[action]
	if !ok || validateAgentRequestID(c.RequestID) != nil {
		return ErrAgentCreditInvalid
	}
	if err := validateAgentFinanceFields(c, fields); err != nil {
		return err
	}
	if c.AgentUserID < 0 || c.ResourceID < 0 || c.Version < 0 || c.ExpectedLimitID < 0 {
		return ErrAgentCreditInvalid
	}
	for _, v := range []string{c.Reason, c.EvidenceReference, c.MerchantScope, c.ExternalReference, c.InvoiceNumber} {
		if v != "" && !validAgentFinanceText(v, 255) {
			return ErrAgentCreditInvalid
		}
	}
	return nil
}
func agentCreditAdminAction(action string) bool {
	return action != "credit.limit.request" && action != "credit.draw" && action != "credit.release"
}
func requireAgentFinanceReaderTx(tx *gorm.DB, id AuthSessionIdentity, owner int, admin bool) error {
	cap := AgentCapabilityHistory
	if admin {
		cap = AgentCapabilityAdmin
	}
	_, err := requireCurrentAgentCapabilityTx(tx, id, owner, cap)
	return err
}
func agentFinanceOperationReplayTx(tx *gorm.DB, id AuthSessionIdentity, action, requestID string, command any) (*AgentOperationStatus, bool, error) {
	var op AgentCreditOperation
	err := tx.Where("request_id = ?", requestID).First(&op).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	hash, err := AgentTransactionParametersHash(command)
	if err != nil {
		return nil, false, err
	}
	if op.Action != action || op.ActorUserID != id.UserID || op.Signature != hash {
		return nil, false, ErrAgentQuotaConflict
	}
	admin := agentCreditAdminAction(action)
	if strings.HasPrefix(action, "deposit.") {
		admin = agentDepositAdminAction(action)
	}
	if err := requireAgentFinanceReaderTx(tx, id, op.AgentUserID, admin); err != nil {
		return nil, false, err
	}
	var result AgentOperationStatus
	if common.UnmarshalJsonStr(op.Result, &result) != nil {
		return nil, false, ErrAgentCreditInvariant
	}
	return &result, true, nil
}
func storeAgentFinanceOperationTx(tx *gorm.DB, id AuthSessionIdentity, owner int, action, requestID string, command any, result *AgentOperationStatus) error {
	hash, err := AgentTransactionParametersHash(command)
	if err != nil {
		return err
	}
	result.Action = action
	result.RequestID = requestID
	result.Status = "completed"
	encoded, err := common.Marshal(result)
	if err != nil {
		return err
	}
	p, err := ReadCurrentAgentPolicyTx(tx)
	if err != nil {
		return err
	}
	profile, err := readAgentProfile(tx, owner, false)
	if err != nil {
		return err
	}
	profileVersion := int64(0)
	if profile != nil {
		profileVersion = profile.Version
	}
	audit := AgentCreditAudit{RequestID: requestID, AgentUserID: owner, ActorUserID: id.UserID, Action: action, ResourceType: result.ResourceType, ResourceID: result.ResourceID, State: result.ResourceState, PolicyVersion: p.ID, ProfileVersion: profileVersion, ParametersHash: hash}
	evidence := ""
	switch c := command.(type) {
	case AgentCreditCommand:
		audit.Reason = c.Reason
		evidence = c.EvidenceReference
	case AgentDepositCommand:
		audit.Reason = c.Reason
		evidence = c.EvidenceReference
	}
	if evidence != "" {
		audit.EvidenceHash, _ = AgentTransactionParametersHash(evidence)
	}
	if err := tx.Create(&audit).Error; err != nil {
		return err
	}
	return tx.Create(&AgentCreditOperation{RequestID: requestID, Action: action, ActorUserID: id.UserID, AgentUserID: owner, Signature: hash, Result: string(encoded)}).Error
}

type agentCreditPlan struct {
	context      AgentTransactionContext
	owner        int
	currency     string
	exponent     int
	amount       int64
	account      *AgentCreditAccount
	limitRequest AgentCreditLimitRequest
	limit        AgentCreditLimitVersion
	draw         AgentCreditDraw
	batch        AgentFundBatch
	receivable   AgentReceivable
	repayment    AgentCreditRepayment
	adjustment   AgentReceivableAdjustment
	allocations  []AgentReceivableAllocationInput
	economics    *AgentFundEconomicPreview
	selection    []agentFundSelection
}

func buildAgentCreditPlanTx(tx *gorm.DB, p *AgentPolicy, id AuthSessionIdentity, action string, c AgentCreditCommand) (*agentCreditPlan, error) {
	if err := validateAgentCreditCommand(action, c); err != nil {
		return nil, err
	}
	plan := &agentCreditPlan{owner: c.AgentUserID, currency: p.CreditCurrency, exponent: p.CreditCurrencyExponent}
	target, version := c.ResourceID, c.Version
	switch action {
	case "credit.limit.request":
		if plan.owner == 0 {
			plan.owner = id.UserID
		}
		if !p.CreditEnabled || ValidateAgentCreditPolicy(p) != nil {
			return nil, ErrAgentFeatureDisabled
		}
		amount, err := agentCreditMinor(c.AmountMinor, true)
		if err != nil {
			return nil, err
		}
		plan.amount = amount
		if amount > p.CreditMaximumLimitMinor || c.TermSeconds <= 0 || c.TermSeconds > p.CreditMaximumTermSeconds || c.ExpiresAt <= common.GetTimestamp() || c.ExpiresAt > common.GetTimestamp()+10*366*86400 || !validAgentFinanceText(c.Reason, 255) || !validAgentFinanceText(c.EvidenceReference, 255) {
			return nil, ErrAgentCreditInvalid
		}
		a, err := agentCreditReadAccountTx(tx, plan.owner)
		if err != nil {
			return nil, err
		}
		plan.account = a
		if (a == nil && c.ExpectedLimitID != 0) || (a != nil && (a.LimitVersionID != c.ExpectedLimitID || a.Currency != p.CreditCurrency || a.CurrencyExponent != p.CreditCurrencyExponent)) {
			return nil, ErrAgentCreditState
		}
		version = c.ExpectedLimitID
	case "credit.limit.approve", "credit.limit.reject":
		if err := lockForUpdate(tx).First(&plan.limitRequest, c.ResourceID).Error; err != nil {
			return nil, err
		}
		r := &plan.limitRequest
		plan.owner = r.AgentUserID
		plan.currency = r.Currency
		plan.exponent = r.CurrencyExponent
		plan.amount = r.AmountMinor
		if c.Version != r.Version || r.State != "requested" {
			return nil, ErrAgentCreditState
		}
		if id.UserID == r.RequestedBy || id.UserID == r.AgentUserID {
			return nil, ErrAgentPurchaseReview
		}
		if action == "credit.limit.approve" {
			if !p.CreditEnabled || ValidateAgentCreditPolicy(p) != nil {
				return nil, ErrAgentFeatureDisabled
			}
			if !c.RiskApproved || !validAgentFinanceText(c.EvidenceReference, 255) || r.PolicyVersion != p.ID || r.ExpiresAt <= common.GetTimestamp() {
				return nil, ErrAgentCreditState
			}
			a, err := agentCreditReadAccountTx(tx, plan.owner)
			if err != nil {
				return nil, err
			}
			plan.account = a
			if a != nil && (a.LimitVersionID != r.ExpectedLimitID || r.AmountMinor < a.ReservedMinor+a.OutstandingMinor+a.WrittenOffMinor) {
				return nil, ErrAgentCreditRisk
			}
			if a == nil && r.ExpectedLimitID != 0 {
				return nil, ErrAgentCreditState
			}
		} else if !validAgentFinanceText(c.Reason, 255) {
			return nil, ErrAgentCreditInvalid
		}
	case "credit.draw":
		if plan.owner == 0 {
			plan.owner = id.UserID
		}
		if plan.owner != id.UserID {
			return nil, ErrAgentProfilePermission
		}
		amount, err := agentCreditDrawPrice(p, c.Quota)
		if err != nil {
			return nil, err
		}
		plan.amount = amount
		a, err := lockAgentCreditAccountTx(tx, plan.owner, p.CreditCurrency, p.CreditCurrencyExponent, false)
		if err != nil {
			return nil, err
		}
		plan.account = a
		if c.ExpectedLimitID != a.LimitVersionID {
			return nil, ErrAgentCreditState
		}
		if err := requireAgentCreditRiskTx(tx, a, amount); err != nil {
			return nil, err
		}
		if err := tx.First(&plan.limit, a.LimitVersionID).Error; err != nil {
			return nil, err
		}
		if plan.limit.AmountMinor > p.CreditMaximumLimitMinor || plan.limit.TermSeconds <= 0 || plan.limit.TermSeconds > p.CreditMaximumTermSeconds {
			return nil, ErrAgentCreditRisk
		}
		version = a.LimitVersionID
	case "credit.release":
		if err := tx.First(&plan.draw, c.ResourceID).Error; err != nil {
			return nil, err
		}
		plan.owner = plan.draw.AgentUserID
		plan.currency = plan.draw.Currency
		plan.exponent = plan.draw.CurrencyExponent
		if plan.owner != id.UserID {
			return nil, ErrAgentProfilePermission
		}
		if err := tx.First(&plan.batch, plan.draw.BatchID).Error; err != nil {
			return nil, err
		}
		selection, _, err := previewAgentFundSelectionTx(tx, &plan.batch, c.Quota)
		if err != nil {
			return nil, err
		}
		plan.selection = selection
		plan.economics, err = PreviewAgentFundEconomicReleaseTx(tx, &plan.batch, c.Quota)
		if err != nil {
			return nil, err
		}
		plan.amount = plan.economics.ReleasedDeltaMinor
		version = plan.economics.Version
	case "credit.invoice.issue", "credit.adjustment.request":
		if err := lockForUpdate(tx).First(&plan.receivable, c.ResourceID).Error; err != nil {
			return nil, err
		}
		r := &plan.receivable
		plan.owner = r.AgentUserID
		plan.currency = r.Currency
		plan.exponent = r.CurrencyExponent
		left, err := agentReceivableRemaining(r)
		if err != nil {
			return nil, err
		}
		plan.amount = left
		if c.Version != r.Version {
			return nil, ErrAgentCreditState
		}
		if id.UserID == r.AgentUserID || !validAgentFinanceText(c.EvidenceReference, 255) {
			return nil, ErrAgentPurchaseReview
		}
		if action == "credit.invoice.issue" {
			if !validAgentFinanceText(c.InvoiceNumber, 96) {
				return nil, ErrAgentCreditInvalid
			}
			var n int64
			if err := tx.Model(&AgentReceivableInvoice{}).Where("receivable_id = ?", r.ID).Count(&n).Error; err != nil {
				return nil, err
			}
			if n > 0 {
				return nil, ErrAgentCreditState
			}
			plan.amount = r.PrincipalMinor
		} else {
			amount, err := agentCreditMinor(c.AmountMinor, true)
			if err != nil {
				return nil, err
			}
			plan.amount = amount
			if !validAgentFinanceText(c.Reason, 255) {
				return nil, ErrAgentCreditInvalid
			}
			switch c.AdjustmentKind {
			case "write_off", "correction_credit":
				if amount > left {
					return nil, ErrAgentCreditState
				}
			case "reverse_write_off":
				if amount > r.WrittenOffMinor {
					return nil, ErrAgentCreditState
				}
			default:
				return nil, ErrAgentCreditInvalid
			}
		}
	case "credit.repayment.record":
		if plan.owner <= 0 || id.UserID == plan.owner {
			return nil, ErrAgentPurchaseReview
		}
		amount, err := agentCreditMinor(c.AmountMinor, true)
		if err != nil {
			return nil, err
		}
		plan.amount = amount
		plan.currency = c.Currency
		plan.exponent = c.CurrencyExponent
		if c.Provider != "bank_transfer" || c.Environment != "live" || !validAgentFinanceText(c.EvidenceReference, 255) || !validAgentFinanceText(c.ExternalReference, 255) || !validAgentFinanceText(c.MerchantScope, 128) || c.PaidAt <= 0 || c.PaidAt > common.GetTimestamp() {
			return nil, ErrAgentCreditInvalid
		}
		a, err := lockAgentCreditAccountTx(tx, plan.owner, c.Currency, c.CurrencyExponent, false)
		if err != nil {
			return nil, err
		}
		plan.account = a
		if _, err := agentCreditAllocationsTx(tx, plan.owner, c.Currency, c.CurrencyExponent, amount, c.Allocations); err != nil {
			return nil, err
		}
		plan.allocations = c.Allocations
	case "credit.repayment.confirm", "credit.repayment.reallocate":
		if err := lockForUpdate(tx).First(&plan.repayment, c.ResourceID).Error; err != nil {
			return nil, err
		}
		r := &plan.repayment
		plan.owner = r.AgentUserID
		plan.currency = r.Currency
		plan.exponent = r.CurrencyExponent
		plan.amount = r.AmountMinor
		if r.State != "recorded" || r.Version != c.Version {
			return nil, ErrAgentCreditState
		}
		if id.UserID == r.AgentUserID || (action == "credit.repayment.confirm" && id.UserID == r.RecordedBy) || !validAgentFinanceText(c.EvidenceReference, 255) {
			return nil, ErrAgentPurchaseReview
		}
		if action == "credit.repayment.reallocate" {
			if id.UserID != r.RecordedBy {
				return nil, ErrAgentPurchaseReview
			}
			plan.allocations = c.Allocations
		} else if common.UnmarshalJsonStr(r.AllocationSnapshot, &plan.allocations) != nil {
			return nil, ErrAgentCreditInvariant
		}
		a, err := lockAgentCreditAccountTx(tx, plan.owner, r.Currency, r.CurrencyExponent, false)
		if err != nil {
			return nil, err
		}
		plan.account = a
		if _, err := agentCreditAllocationsTx(tx, plan.owner, r.Currency, r.CurrencyExponent, r.AmountMinor, plan.allocations); err != nil {
			return nil, err
		}
	case "credit.adjustment.approve", "credit.adjustment.reject":
		if err := lockForUpdate(tx).First(&plan.adjustment, c.ResourceID).Error; err != nil {
			return nil, err
		}
		a := &plan.adjustment
		if err := lockForUpdate(tx).First(&plan.receivable, a.ReceivableID).Error; err != nil {
			return nil, err
		}
		r := &plan.receivable
		plan.owner = a.AgentUserID
		plan.currency = r.Currency
		plan.exponent = r.CurrencyExponent
		plan.amount = a.AmountMinor
		if a.State != "requested" || a.Version != c.Version {
			return nil, ErrAgentCreditState
		}
		if id.UserID == a.RequestedBy || id.UserID == a.AgentUserID {
			return nil, ErrAgentPurchaseReview
		}
		if !validAgentFinanceText(c.Reason, 255) || !validAgentFinanceText(c.EvidenceReference, 255) {
			return nil, ErrAgentCreditInvalid
		}
		left, err := agentReceivableRemaining(r)
		if err != nil {
			return nil, err
		}
		if action == "credit.adjustment.approve" && ((a.Kind == "reverse_write_off" && a.AmountMinor > r.WrittenOffMinor) || (a.Kind != "reverse_write_off" && a.AmountMinor > left)) {
			return nil, ErrAgentCreditState
		}
	}
	if plan.repayment.ID > 0 {
		var receipt AgentPaymentReceipt
		if err := tx.First(&receipt, plan.repayment.ReceiptID).Error; err != nil {
			return nil, err
		}
		if ValidateAgentPaymentReceipt(&receipt) != nil || receipt.Provider != "bank_transfer" || receipt.Environment != "live" || receipt.Purpose != "credit_repayment" || receipt.OwnerUserID != plan.repayment.AgentUserID || receipt.Currency != plan.repayment.Currency || receipt.CurrencyExponent != plan.repayment.CurrencyExponent || receipt.PaidMinor != plan.repayment.AmountMinor || receipt.VerifiedBy != plan.repayment.RecordedBy {
			return nil, ErrAgentCreditInvariant
		}
	}
	if action == "credit.limit.approve" {
		profile, err := readAgentProfile(tx, plan.owner, false)
		if err != nil {
			return nil, err
		}
		if !profile.Allows(AgentCapabilitySales) {
			return nil, ErrAgentProfilePermission
		}
	}
	params := struct {
		Command     AgentCreditCommand               `json:"command"`
		Selection   []agentFundSelection             `json:"selection,omitempty"`
		Allocations []AgentReceivableAllocationInput `json:"allocations,omitempty"`
		Economics   *AgentFundEconomicPreview        `json:"economics,omitempty"`
		LimitID     int64                            `json:"limit_id"`
	}{c, plan.selection, plan.allocations, plan.economics, plan.limit.ID}
	targetID := strconv.FormatInt(target, 10)
	if target == 0 {
		targetID = "new:" + c.RequestID
	}
	ctx, err := agentBusinessContextTx(tx, p, plan.owner, action, c.RequestID, plan.currency, strconv.FormatInt(plan.amount, 10), c.Quota, "credit", targetID, version, params)
	if err != nil {
		return nil, err
	}
	plan.context = ctx
	return plan, nil
}
func PrepareAgentCreditOperation(id AuthSessionIdentity, action, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	if _, ok := agentCreditActionFields[action]; !ok {
		return nil, nil
	}
	c, err := decodeAgentCreditCommand(action, requestID, payload)
	if err != nil {
		return nil, err
	}
	var out *AgentPreparedOperation
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		p, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		plan, err := buildAgentCreditPlanTx(tx, p, id, action, c)
		if err != nil {
			return err
		}
		cap := AgentCapabilitySales
		if action == "credit.release" {
			cap = AgentCapabilitySettlement
		}
		if agentCreditAdminAction(action) {
			cap = AgentCapabilityAdmin
		}
		if _, err := RequireAgentCapabilityTx(tx, id, plan.owner, plan.context.ProfileVersion, cap); err != nil {
			return err
		}
		// Authorization locks users/profile before governance records.
		if action == "credit.limit.request" || action == "credit.draw" {
			if err := RequireAgentGovernanceCreditTx(tx, plan.owner); err != nil {
				return err
			}
		}
		out = &AgentPreparedOperation{VerificationContext: plan.context, Summary: AgentOperationSummary{Action: action, AgentUserID: plan.owner, Currency: plan.currency, CurrencyExponent: plan.exponent, AmountMinor: plan.context.AmountMinor, FeeMinor: "0", NetMinor: plan.context.AmountMinor, Quota: plan.context.Quota, TargetLabel: fmt.Sprintf("Agent credit #%d", plan.owner), PolicyVersion: p.ID}, StatusURL: "/api/agent-platform/operations/" + requestID}
		return nil
	})
	return out, err
}
func ReplayAgentCreditOperation(id AuthSessionIdentity, action, requestID string, payload []byte) (any, bool, error) {
	if _, ok := agentCreditActionFields[action]; !ok {
		return nil, false, nil
	}
	c, err := decodeAgentCreditCommand(action, requestID, payload)
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
func FindAgentCreditOperation(id AuthSessionIdentity, requestID string) (*AgentOperationStatus, error) {
	if validateAgentRequestID(requestID) != nil {
		return nil, ErrAgentCreditInvalid
	}
	var result *AgentOperationStatus
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		var op AgentCreditOperation
		if err := tx.Where("request_id = ? AND actor_user_id = ?", requestID, id.UserID).First(&op).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		admin := agentCreditAdminAction(op.Action)
		if strings.HasPrefix(op.Action, "deposit.") {
			admin = agentDepositAdminAction(op.Action)
		}
		if err := requireAgentFinanceReaderTx(tx, id, op.AgentUserID, admin); err != nil {
			return err
		}
		result = &AgentOperationStatus{}
		return common.UnmarshalJsonStr(op.Result, result)
	})
	return result, err
}
