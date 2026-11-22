package model

import (
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type AgentReceivable struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	RecognitionKey   string `json:"recognition_key" gorm:"type:varchar(200);not null;uniqueIndex"`
	AgentUserID      int    `json:"agent_user_id" gorm:"not null;index"`
	DrawID           int64  `json:"draw_id" gorm:"not null;index"`
	BatchID          int    `json:"batch_id"`
	StartQuota       int    `json:"start_quota" gorm:"type:bigint"`
	Quota            int    `json:"quota" gorm:"type:bigint"`
	RedemptionID     int    `json:"redemption_id"`
	PrincipalMinor   int64  `json:"principal_minor,string"`
	PaidMinor        int64  `json:"paid_minor,string"`
	OffsetMinor      int64  `json:"offset_minor,string"`
	WrittenOffMinor  int64  `json:"written_off_minor,string"`
	CorrectedMinor   int64  `json:"corrected_minor,string"`
	Currency         string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent int    `json:"currency_exponent"`
	RecognizedAt     int64  `json:"recognized_at"`
	DueAt            int64  `json:"due_at" gorm:"index"`
	TermSeconds      int64  `json:"term_seconds"`
	PolicyVersion    int64  `json:"policy_version"`
	LimitVersionID   int64  `json:"limit_version_id"`
	State            string `json:"state" gorm:"type:varchar(32);not null"`
	Version          int64  `json:"version"`
}
type AgentReceivableInvoice struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	ReceivableID     int64  `json:"receivable_id" gorm:"not null;uniqueIndex"`
	InvoiceNumber    string `json:"invoice_number" gorm:"type:varchar(96);not null;uniqueIndex"`
	PrincipalMinor   int64  `json:"principal_minor,string"`
	Currency         string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent int    `json:"currency_exponent"`
	DueAt            int64  `json:"due_at"`
	IssuedBy         int    `json:"issued_by"`
	EvidenceHash     string `json:"evidence_hash" gorm:"type:varchar(64)"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentReceivableInvoice) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentReceivableInvoice) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentReceivableAdjustment struct {
	ID           int64  `json:"id" gorm:"primaryKey"`
	AgentUserID  int    `json:"agent_user_id" gorm:"index"`
	ReceivableID int64  `json:"receivable_id" gorm:"index"`
	Kind         string `json:"kind" gorm:"type:varchar(32)"`
	AmountMinor  int64  `json:"amount_minor,string"`
	Currency     string `json:"currency" gorm:"type:varchar(3)"`
	RequestedBy  int    `json:"requested_by"`
	ReviewedBy   int    `json:"reviewed_by"`
	EvidenceHash string `json:"evidence_hash" gorm:"type:varchar(64)"`
	Reason       string `json:"reason" gorm:"type:varchar(255)"`
	State        string `json:"state" gorm:"type:varchar(32)"`
	Version      int64  `json:"version"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime"`
}
type AgentReceivableAllocationInput struct {
	ReceivableID int64  `json:"receivable_id"`
	AmountMinor  string `json:"amount_minor"`
}
type AgentCreditRepayment struct {
	Allocations        []AgentReceivableAllocationInput `json:"allocations" gorm:"-"`
	ID                 int64                            `json:"id" gorm:"primaryKey"`
	AgentUserID        int                              `json:"agent_user_id" gorm:"index"`
	AmountMinor        int64                            `json:"amount_minor,string"`
	Currency           string                           `json:"currency" gorm:"type:varchar(3)"`
	CurrencyExponent   int                              `json:"currency_exponent"`
	ReceiptID          int64                            `json:"receipt_id" gorm:"not null;uniqueIndex"`
	RecordedBy         int                              `json:"recorded_by"`
	ConfirmedBy        int                              `json:"confirmed_by"`
	State              string                           `json:"state" gorm:"type:varchar(32)"`
	Version            int64                            `json:"version"`
	AllocationSnapshot string                           `json:"-" gorm:"type:text"`
	CreatedAt          int64                            `json:"created_at" gorm:"autoCreateTime"`
}
type AgentCreditRepaymentAllocation struct {
	ID           int64 `json:"id" gorm:"primaryKey"`
	RepaymentID  int64 `json:"repayment_id" gorm:"uniqueIndex:idx_credit_repayment_ar,priority:1"`
	ReceivableID int64 `json:"receivable_id" gorm:"uniqueIndex:idx_credit_repayment_ar,priority:2"`
	AmountMinor  int64 `json:"amount_minor,string"`
	CreatedAt    int64 `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCreditRepaymentAllocation) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentCreditRepaymentAllocation) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

func agentReceivableRemaining(r *AgentReceivable) (int64, error) {
	if r.PrincipalMinor <= 0 || r.PrincipalMinor > int64(common.MaxWalletQuota) || !validAgentCurrency(r.Currency, r.CurrencyExponent) || r.TermSeconds <= 0 || r.RecognizedAt <= 0 || r.DueAt != r.RecognizedAt+r.TermSeconds {
		return 0, ErrAgentCreditInvariant
	}
	left := r.PrincipalMinor
	for _, v := range []int64{r.PaidMinor, r.OffsetMinor, r.WrittenOffMinor, r.CorrectedMinor} {
		if v < 0 || v > left {
			return 0, ErrAgentCreditInvariant
		}
		left -= v
	}
	return left, nil
}
func saveAgentReceivableTx(tx *gorm.DB, r *AgentReceivable) error {
	left, err := agentReceivableRemaining(r)
	if err != nil {
		return err
	}
	r.State = "open"
	if left == 0 {
		r.State = "settled"
	}
	r.Version++
	return tx.Model(r).Select("paid_minor", "offset_minor", "written_off_minor", "corrected_minor", "state", "version").Updates(r).Error
}

// Called only inside the same authoritative source transition transaction.
// A pre-existing customer obligation may spend despite current overdue limits,
// policy disablement or business freeze. That is not a new credit grant.
func applyAgentCreditSourceTransitionTx(tx *gorm.DB, b *AgentFundBatch, s *AgentFundSlice, from, operation string, cost int64) error {
	if b.SourceKind != AgentFundCredit {
		return nil
	}
	var draw AgentCreditDraw
	if err := tx.Where("batch_id = ? AND agent_user_id = ?", b.ID, b.AgentUserID).First(&draw).Error; err != nil {
		return err
	}
	if draw.PrincipalMinor <= 0 || draw.RecognitionMode != AgentCreditRecognitionSourceSpend || draw.Currency != b.Currency || draw.CurrencyExponent != b.CurrencyExponent || draw.OriginalQuota != b.OriginalQuota || strconv.FormatInt(draw.PrincipalMinor, 10) != b.OriginalPaidMinor || b.CashRefundable || b.PaymentReceiptID != 0 {
		return ErrAgentCreditInvariant
	}
	account, err := lockAgentCreditAccountTx(tx, b.AgentUserID, draw.Currency, draw.CurrencyExponent, false)
	if err != nil {
		return err
	}
	if from == AgentSliceAvailable && (s.State == AgentSliceReserved || s.State == AgentSliceSpent) {
		policy, err := ReadCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if !policy.CreditEnabled {
			return ErrAgentFeatureDisabled
		}
		if err := requireAgentCreditRiskTx(tx, account, 0); err != nil {
			return err
		}
		if s.State == AgentSliceSpent && !policy.CreditSelfUseEnabled {
			return ErrAgentFeatureDisabled
		}
	}
	if cost < 0 || cost > draw.PrincipalMinor {
		return ErrAgentCreditInvariant
	}
	switch {
	case s.State == AgentSliceSpent && (from == AgentSliceReserved || from == AgentSliceAvailable):
		// A cumulative rounding carry may produce zero incremental debt. The
		// economic journal still records consumed quota; no zero-valued AR row.
		if cost == 0 {
			return nil
		}
		now := common.GetTimestamp()
		if draw.TermSeconds <= 0 || draw.TermSeconds > 10*366*86400 {
			return ErrAgentCreditInvariant
		}
		r := AgentReceivable{RecognitionKey: fmt.Sprintf("%s:%d:%d", operation, b.ID, s.StartQuota), AgentUserID: b.AgentUserID, DrawID: draw.ID, BatchID: b.ID, StartQuota: s.StartQuota, Quota: s.Quota, RedemptionID: s.RedemptionID, PrincipalMinor: cost, Currency: draw.Currency, CurrencyExponent: draw.CurrencyExponent, RecognizedAt: now, DueAt: now + draw.TermSeconds, TermSeconds: draw.TermSeconds, PolicyVersion: draw.PolicyVersion, LimitVersionID: draw.LimitVersionID, State: "open", Version: 1}
		if err := tx.Create(&r).Error; err != nil {
			return err
		}
		return appendAgentCreditLedgerTx(tx, account, AgentCreditLedger{EventKey: "recognize:" + r.RecognitionKey, Kind: "recognition", ReservedDelta: -cost, OutstandingDelta: cost, ReceivableID: r.ID, DrawID: draw.ID})
	case s.State == AgentSliceReclaimed && from == AgentSliceAvailable:
		return appendAgentCreditLedgerTx(tx, account, AgentCreditLedger{EventKey: fmt.Sprintf("%s:%d:%d", operation, b.ID, s.StartQuota), Kind: "release", ReservedDelta: -cost, DrawID: draw.ID})
	case (s.State == AgentSliceReserved && from == AgentSliceAvailable) || (s.State == AgentSliceAvailable && from == AgentSliceReserved):
		return nil
	default:
		return ErrAgentCreditInvariant
	}
}

func (*AgentReceivable) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

func (*AgentReceivableAdjustment) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

func (*AgentCreditRepayment) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }
