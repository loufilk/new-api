package model

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Cutover authority is an immutable observation of an ALREADY IN-FLIGHT
// obligation, never permission to originate a new refund or reprice a payment.
// No public endpoint creates this row. Only absent-carry historical baseline
// initialization can capture pending/UNKNOWN obligations and their held source.
type AgentFundLegacyRefundAuthority struct {
	RefundID                int    `json:"refund_id" gorm:"primaryKey;autoIncrement:false"`
	BatchID                 int    `json:"batch_id" gorm:"index;not null"`
	AgentUserID             int    `json:"agent_user_id" gorm:"index;not null"`
	PaymentReceiptID        int64  `json:"payment_receipt_id"`
	PurchaseOrderID         int    `json:"purchase_order_id"`
	Quota                   int    `json:"quota" gorm:"type:bigint"`
	PrincipalMinor          int64  `json:"principal_minor,string"`
	FeeMinor                int64  `json:"fee_minor,string"`
	NetMinor                int64  `json:"net_minor,string"`
	Currency                string `json:"currency" gorm:"type:varchar(3)"`
	CurrencyExponent        int    `json:"currency_exponent"`
	BusinessKey             string `json:"business_key" gorm:"type:varchar(96);uniqueIndex"`
	CapturedState           string `json:"captured_state" gorm:"type:varchar(32)"`
	RequestedBy             int    `json:"requested_by"`
	ApprovedBy              int    `json:"approved_by"`
	SubmittedBy             int    `json:"submitted_by"`
	SubmittedAt             int64  `json:"submitted_at"`
	PolicyVersion           int64  `json:"policy_version"`
	PolicyHash              string `json:"policy_hash" gorm:"type:varchar(64)"`
	OriginalRecipientDigest string `json:"original_recipient_digest" gorm:"type:varchar(64)"`
	SourceSnapshot          string `json:"source_snapshot" gorm:"type:text;not null"`
	CreatedAt               int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentFundLegacyRefundAuthority) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentFundLegacyRefundAuthority) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type agentLegacyRefundInterval struct {
	ID        int   `json:"id"`
	Start     int   `json:"start"`
	Quota     int   `json:"quota"`
	Principal int64 `json:"principal,string"`
}

func captureAgentLegacyInflightRefundTx(tx *gorm.DB, b *AgentFundBatch, r *AgentPurchaseRefund) error {
	if r.State != "payment_pending" && r.State != "payment_unknown" {
		return nil
	}
	gross, eg := agentCreditMinor(r.PrincipalMinor, true)
	fee, ef := agentCreditMinor(r.FeeMinor, false)
	net, en := agentCreditMinor(r.NetMinor, true)
	if eg != nil || ef != nil || en != nil || fee > gross || net != gross-fee || r.AgentUserID != b.AgentUserID || r.BatchID != b.ID || r.PaymentReceiptID != b.PaymentReceiptID || r.PurchaseOrderID != b.PurchaseOrderID || r.Currency != b.Currency || r.CurrencyExponent != b.CurrencyExponent || r.PolicyVersion != b.PolicyVersion || r.PolicySnapshot != b.PolicySnapshot || r.Quota <= 0 || r.RequestedBy != b.AgentUserID || r.ApprovedBy <= 0 || r.ApprovedBy == b.AgentUserID || r.SubmittedBy <= 0 || !validAgentFinanceText(r.BusinessKey, 96) {
		// A corrupt record is still reported as a permanent baseline exception, but
		// cannot mint an exception authority with invented principal/source facts.
		return nil
	}
	var slices []AgentFundSlice
	if err := tx.Where("refund_id = ? AND state = ?", r.ID, AgentSliceRefundFrozen).Order("start_quota").Find(&slices).Error; err != nil {
		return err
	}
	intervals := []agentLegacyRefundInterval{}
	q := 0
	var total int64
	for _, s := range slices {
		cost, err := AgentFundSliceCost(b, s.StartQuota, s.Quota)
		if err != nil || s.AgentUserID != b.AgentUserID || s.BatchID != b.ID || s.Quota > r.Quota-q || cost > gross-total {
			return nil
		}
		q += s.Quota
		total += cost
		intervals = append(intervals, agentLegacyRefundInterval{s.ID, s.StartQuota, s.Quota, cost})
	}
	if q != r.Quota || total != gross {
		return nil
	}
	snapshot, err := common.Marshal(intervals)
	if err != nil {
		return err
	}
	policyHash, err := AgentTransactionParametersHash(r.PolicySnapshot)
	if err != nil {
		return err
	}
	return tx.Create(&AgentFundLegacyRefundAuthority{RefundID: r.ID, BatchID: b.ID, AgentUserID: b.AgentUserID, PaymentReceiptID: r.PaymentReceiptID, PurchaseOrderID: r.PurchaseOrderID, Quota: r.Quota, PrincipalMinor: gross, FeeMinor: fee, NetMinor: net, Currency: r.Currency, CurrencyExponent: r.CurrencyExponent, BusinessKey: r.BusinessKey, CapturedState: r.State, RequestedBy: r.RequestedBy, ApprovedBy: r.ApprovedBy, SubmittedBy: r.SubmittedBy, SubmittedAt: r.SubmittedAt, PolicyVersion: r.PolicyVersion, PolicyHash: policyHash, OriginalRecipientDigest: r.RecipientDigest, SourceSnapshot: string(snapshot)}).Error
}

func agentLegacyRefundAuthorityTx(tx *gorm.DB, b *AgentFundBatch, r *AgentPurchaseRefund, whole bool) (*AgentFundLegacyRefundAuthority, []agentLegacyRefundInterval, error) {
	var a AgentFundLegacyRefundAuthority
	if err := tx.Where("refund_id = ?", r.ID).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrAgentFundEconomicReview
		}
		return nil, nil, err
	}
	hash, err := AgentTransactionParametersHash(r.PolicySnapshot)
	if err != nil {
		return nil, nil, err
	}
	if (r.State != "payment_pending" && r.State != "payment_unknown") || (a.CapturedState != "payment_pending" && a.CapturedState != "payment_unknown") || a.BatchID != b.ID || a.BatchID != r.BatchID || a.AgentUserID != b.AgentUserID || a.AgentUserID != r.AgentUserID || a.PaymentReceiptID != b.PaymentReceiptID || a.PaymentReceiptID != r.PaymentReceiptID || a.PurchaseOrderID != r.PurchaseOrderID || a.Quota != r.Quota || strconv.FormatInt(a.PrincipalMinor, 10) != r.PrincipalMinor || strconv.FormatInt(a.FeeMinor, 10) != r.FeeMinor || strconv.FormatInt(a.NetMinor, 10) != r.NetMinor || a.Currency != b.Currency || a.Currency != r.Currency || a.CurrencyExponent != b.CurrencyExponent || a.CurrencyExponent != r.CurrencyExponent || a.BusinessKey != r.BusinessKey || a.PolicyVersion != r.PolicyVersion || a.PolicyHash != hash || a.RequestedBy != r.RequestedBy || a.ApprovedBy != r.ApprovedBy || a.SubmittedBy != r.SubmittedBy || a.SubmittedAt != r.SubmittedAt || (a.OriginalRecipientDigest != "" && a.OriginalRecipientDigest != r.RecipientDigest) {
		return nil, nil, ErrAgentFundEconomicReview
	}
	var intervals []agentLegacyRefundInterval
	if common.UnmarshalJsonStr(a.SourceSnapshot, &intervals) != nil || len(intervals) == 0 {
		return nil, nil, ErrAgentFundInvariant
	}
	q := 0
	var total int64
	for _, s := range intervals {
		cost, err := AgentFundSliceCost(b, s.Start, s.Quota)
		if err != nil || cost != s.Principal || s.Quota <= 0 || s.Quota > a.Quota-q || cost > a.PrincipalMinor-total {
			return nil, nil, ErrAgentFundInvariant
		}
		q += s.Quota
		total += cost
	}
	if q != a.Quota || total != a.PrincipalMinor || a.FeeMinor < 0 || a.FeeMinor > a.PrincipalMinor || a.NetMinor != a.PrincipalMinor-a.FeeMinor {
		return nil, nil, ErrAgentFundInvariant
	}
	if whole {
		var current []AgentFundSlice
		if err := tx.Where("refund_id = ? AND state = ?", r.ID, AgentSliceRefundFrozen).Order("start_quota").Find(&current).Error; err != nil {
			return nil, nil, err
		}
		if len(current) != len(intervals) {
			return nil, nil, ErrAgentFundEconomicReview
		}
		for i, s := range current {
			frozen := intervals[i]
			if s.ID != frozen.ID || s.StartQuota != frozen.Start || s.Quota != frozen.Quota || s.AgentUserID != a.AgentUserID || s.BatchID != a.BatchID {
				return nil, nil, ErrAgentFundEconomicReview
			}
		}
	}
	return &a, intervals, nil
}

// Called by refund approve/submit/instructions with allowLegacyPaid=false, and
// by independently verified SUCCESS resolution with true. Reject/definite-unpaid
// failure must not call this gate: they release holds without releasing cash.
func ValidateAgentFundRefundQuoteTx(tx *gorm.DB, r *AgentPurchaseRefund, allowLegacyPaid bool) error {
	var b AgentFundBatch
	if err := tx.First(&b, r.BatchID).Error; err != nil {
		return err
	}
	if b.SourceKind != AgentFundPurchase || b.AgentUserID != r.AgentUserID || b.PaymentReceiptID != r.PaymentReceiptID || b.Currency != r.Currency || b.CurrencyExponent != r.CurrencyExponent {
		return ErrAgentFundInvariant
	}
	e, err := ensureAgentFundEconomicsTx(tx, &b)
	if err != nil {
		return err
	}
	if e.Scheme != AgentFundEconomicCumulative {
		if !allowLegacyPaid {
			return ErrAgentFundEconomicReview
		}
		_, _, err := agentLegacyRefundAuthorityTx(tx, &b, r, true)
		return err
	}
	var held []AgentFundSlice
	if err := tx.Where("refund_id = ? AND state = ?", r.ID, AgentSliceRefundFrozen).Find(&held).Error; err != nil {
		return err
	}
	q := 0
	for _, s := range held {
		if s.BatchID != b.ID || s.AgentUserID != b.AgentUserID || s.Quota <= 0 || s.Quota > r.Quota-q {
			return ErrAgentFundEconomicReview
		}
		q += s.Quota
	}
	if q != r.Quota {
		return ErrAgentFundEconomicReview
	}
	preview, err := PreviewAgentFundEconomicReleaseTx(tx, &b, r.Quota)
	if err != nil {
		return err
	}
	var p AgentPolicy
	if common.UnmarshalJsonStr(b.PolicySnapshot, &p) != nil || p.RefundFeeBPS < 0 || p.RefundFeeBPS > 10000 {
		return ErrAgentFundInvariant
	}
	before, err := AgentMoneyRatio(preview.ReleasedBeforeMinor, p.RefundFeeBPS, 10000, false)
	if err != nil {
		return err
	}
	after, err := AgentMoneyRatio(preview.ReleasedAfterMinor, p.RefundFeeBPS, 10000, false)
	if err != nil {
		return err
	}
	fee := after - before
	if strconv.FormatInt(preview.ReleasedDeltaMinor, 10) != r.PrincipalMinor || strconv.FormatInt(fee, 10) != r.FeeMinor || strconv.FormatInt(preview.ReleasedDeltaMinor-fee, 10) != r.NetMinor {
		return ErrAgentFundEconomicReview
	}
	return nil
}
