package model

import (
	"errors"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

type agentStatementTarget struct {
	PaidAt            int64  `json:"paid_at"`
	Direction         string `json:"direction"`
	BusinessReference string `json:"business_reference"`
	ExternalReference string `json:"external_reference"`
	AccountReference  string `json:"account_reference"`
	OwnerUserID       int    `json:"owner_user_id"`
	Currency          string `json:"currency"`
	AmountMinor       int64  `json:"amount_minor,string"`
	BeneficiaryDigest string `json:"beneficiary_digest"`
	ReceiptID         int64  `json:"receipt_id"`
	State             string `json:"state"`
	Version           int64  `json:"version"`
	Verified          bool   `json:"verified"`
}

// A business target resolves to the SAME canonical receipt purpose/key as its
// receipt target, so selecting a different endpoint cannot reconcile it twice.
func agentStatementTargetRawTx(tx *gorm.DB, kind string, id int64) (agentStatementTarget, error) {
	var target agentStatementTarget
	var incoming AgentPaymentReceipt
	var outgoing AgentOutboundPaymentReceipt
	switch kind {
	case "incoming_receipt":
		if err := tx.First(&incoming, id).Error; err != nil {
			return target, err
		}
	case "purchase":
		var order AgentPurchaseOrder
		if err := tx.First(&order, id).Error; err != nil {
			return target, err
		}
		target = agentStatementTarget{PaidAt: incoming.PaidAt, Direction: "incoming", BusinessReference: "purchase:" + strconv.Itoa(order.ID), OwnerUserID: order.AgentUserID, Currency: order.Currency, State: order.Status, Version: order.Version}
		target.AmountMinor, _ = strconv.ParseInt(order.PaidMinor, 10, 64)
		if order.PaymentReceiptID <= 0 || (order.Status != "verified" && order.Status != "credited") {
			return target, nil
		}
		if err := tx.First(&incoming, order.PaymentReceiptID).Error; err != nil {
			return target, err
		}
	case "credit_repayment":
		var r AgentCreditRepayment
		if err := tx.First(&r, id).Error; err != nil {
			return target, err
		}
		if err := tx.First(&incoming, r.ReceiptID).Error; err != nil {
			return target, err
		}
	case "deposit":
		var r AgentDepositReceipt
		if err := tx.First(&r, id).Error; err != nil {
			return target, err
		}
		if err := tx.First(&incoming, r.PaymentReceiptID).Error; err != nil {
			return target, err
		}
	case "deposit_refund":
		var r AgentDepositOperationRequest
		if err := tx.First(&r, id).Error; err != nil {
			return target, err
		}
		target = agentStatementTarget{Direction: "outgoing", BusinessReference: r.BusinessKey, OwnerUserID: r.AgentUserID, Currency: r.Currency, AmountMinor: r.AmountMinor, State: r.State, Version: r.Version}
		if r.Kind != "refund" || r.State != "succeeded" || r.PayoutReceiptID <= 0 {
			return target, nil
		}
		if err := tx.First(&outgoing, r.PayoutReceiptID).Error; err != nil {
			return target, err
		}
	case "outgoing_receipt":
		if err := tx.First(&outgoing, id).Error; err != nil {
			return target, err
		}
	case "withdrawal":
		var w AgentCommissionWithdrawal
		if err := tx.First(&w, id).Error; err != nil {
			return target, err
		}
		target = agentStatementTarget{Direction: "outgoing", BusinessReference: w.BusinessNo, OwnerUserID: w.UserID, Currency: w.Currency, AmountMinor: w.NetMinor, BeneficiaryDigest: w.RecipientDigest, State: w.Status, Version: w.Version}
		if w.PayoutReceiptID <= 0 || w.Status != "succeeded" {
			return target, nil
		}
		if err := tx.First(&outgoing, w.PayoutReceiptID).Error; err != nil {
			return target, err
		}
	case "purchase_refund":
		var refund AgentPurchaseRefund
		if err := tx.First(&refund, id).Error; err != nil {
			return target, err
		}
		target = agentStatementTarget{Direction: "outgoing", BusinessReference: refund.BusinessKey, OwnerUserID: refund.AgentUserID, Currency: refund.Currency, State: refund.State, Version: refund.Version}
		target.AmountMinor, _ = strconv.ParseInt(refund.NetMinor, 10, 64)
		if refund.State != "succeeded" {
			return target, nil
		}
		if err := tx.Where("business_key = ? AND purpose = ?", refund.BusinessKey, "purchase_refund").First(&outgoing).Error; err != nil {
			return target, err
		}
	default:
		return target, ErrAgentStatementInvalid
	}
	if incoming.ID > 0 {
		if err := ValidateAgentPaymentReceipt(&incoming); err != nil {
			return target, err
		}
		if target.BusinessReference != "" && (target.BusinessReference != incoming.LocalOrderKey || target.OwnerUserID != incoming.OwnerUserID || target.Currency != incoming.Currency || target.AmountMinor != incoming.PaidMinor) {
			return target, ErrAgentStatementConflict
		}
		target = agentStatementTarget{PaidAt: incoming.PaidAt, Direction: "incoming", BusinessReference: incoming.LocalOrderKey, ExternalReference: incoming.ProviderReference, AccountReference: incoming.MerchantScope, OwnerUserID: incoming.OwnerUserID, Currency: incoming.Currency, AmountMinor: incoming.PaidMinor, ReceiptID: incoming.ID, State: "verified", Verified: incoming.Provider == "bank_transfer" && incoming.Environment == "live" && incoming.VerifiedBy > 0 && incoming.EvidenceHash != ""}
		switch incoming.Purpose {
		case "credit_repayment":
			var r AgentCreditRepayment
			if err := tx.Where("receipt_id = ?", incoming.ID).First(&r).Error; err != nil {
				return target, err
			}
			if r.AgentUserID != incoming.OwnerUserID || r.Currency != incoming.Currency || r.CurrencyExponent != incoming.CurrencyExponent || r.AmountMinor != incoming.PaidMinor || r.RecordedBy != incoming.VerifiedBy {
				return target, ErrAgentStatementConflict
			}
			target.State = r.State
			target.Version = r.Version
			target.Verified = target.Verified && r.State == "confirmed" && r.ConfirmedBy > 0 && r.ConfirmedBy != r.RecordedBy && r.ConfirmedBy != r.AgentUserID
		case "deposit":
			var r AgentDepositReceipt
			if err := tx.Where("payment_receipt_id = ?", incoming.ID).First(&r).Error; err != nil {
				return target, err
			}
			if r.AgentUserID != incoming.OwnerUserID || r.Currency != incoming.Currency || r.CurrencyExponent != incoming.CurrencyExponent || r.AmountMinor != incoming.PaidMinor || r.RecordedBy != incoming.VerifiedBy {
				return target, ErrAgentStatementConflict
			}
			target.State = r.State
			target.Version = r.Version
			target.Verified = target.Verified && r.State == "confirmed" && r.ConfirmedBy > 0 && r.ConfirmedBy != r.RecordedBy && r.ConfirmedBy != r.AgentUserID
		case "purchase":
			if suffix, ok := strings.CutPrefix(incoming.LocalOrderKey, "legacy-reclass:"); ok {
				id, err := strconv.Atoi(suffix)
				if err != nil || id <= 0 || strconv.Itoa(id) != suffix {
					return target, ErrAgentStatementConflict
				}
				var r AgentFundReclassification
				if err := tx.Where("id = ? AND payment_receipt_id = ?", id, incoming.ID).First(&r).Error; err != nil {
					return target, err
				}
				if r.AgentUserID != incoming.OwnerUserID || r.Currency != incoming.Currency || r.CurrencyExponent != incoming.CurrencyExponent || r.PaidMinor != strconv.FormatInt(incoming.PaidMinor, 10) || r.VerifiedBy != incoming.VerifiedBy {
					return target, ErrAgentStatementConflict
				}
				target.State = r.State
				target.Version = r.Version
				target.Verified = target.Verified && r.State == "confirmed" && r.FundBatchID > 0 && r.ReviewedBy > 0 && r.ReviewedBy != r.VerifiedBy && r.ReviewedBy != r.AgentUserID
				if target.Verified {
					var batch AgentFundBatch
					if err := tx.First(&batch, r.FundBatchID).Error; err != nil {
						return target, err
					}
					if batch.SourceKind != AgentFundPurchase || batch.PaymentReceiptID != incoming.ID || batch.AgentUserID != r.AgentUserID || batch.Currency != r.Currency || batch.CurrencyExponent != r.CurrencyExponent || batch.OriginalPaidMinor != r.PaidMinor || batch.OriginalQuota != r.Quota {
						return target, ErrAgentStatementConflict
					}
				}
				break
			}
			var r AgentPurchaseOrder
			if err := tx.Where("payment_receipt_id = ?", incoming.ID).First(&r).Error; err != nil {
				return target, err
			}
			if incoming.LocalOrderKey != "purchase:"+strconv.Itoa(r.ID) || r.AgentUserID != incoming.OwnerUserID || r.Currency != incoming.Currency || r.CurrencyExponent != incoming.CurrencyExponent || r.PaidMinor != strconv.FormatInt(incoming.PaidMinor, 10) {
				return target, ErrAgentStatementConflict
			}
			target.State = r.Status
			target.Version = r.Version
			target.Verified = target.Verified && r.Status == "credited"
		default:
			// Other receipt purposes (for example personal top-ups) have no
			// agent-obligation settlement contract in this reconciler.
			target.Verified = false
		}

	}
	if outgoing.ID > 0 {
		if target.BusinessReference != "" && (target.BusinessReference != outgoing.BusinessKey || target.OwnerUserID != outgoing.OwnerUserID || target.Currency != outgoing.Currency || target.AmountMinor != outgoing.PaidMinor) {
			return target, ErrAgentStatementConflict
		}
		beneficiary := ""
		state := "unbound"
		version := int64(0)
		switch outgoing.Purpose {
		case "withdrawal":
			var w AgentCommissionWithdrawal
			if err := tx.Where("business_no = ? AND payout_receipt_id = ?", outgoing.BusinessKey, outgoing.ID).First(&w).Error; err != nil {
				return target, err
			}
			if w.UserID != outgoing.OwnerUserID || w.Currency != outgoing.Currency || w.CurrencyExponent != outgoing.CurrencyExponent || w.NetMinor != outgoing.PaidMinor {
				return target, ErrAgentStatementConflict
			}
			beneficiary = w.RecipientDigest
			state = w.Status
			version = w.Version
		case "purchase_refund":
			var r AgentPurchaseRefund
			if err := tx.Where("business_key = ?", outgoing.BusinessKey).First(&r).Error; err != nil {
				return target, err
			}
			if r.AgentUserID != outgoing.OwnerUserID || r.Currency != outgoing.Currency || r.CurrencyExponent != outgoing.CurrencyExponent || r.NetMinor != strconv.FormatInt(outgoing.PaidMinor, 10) {
				return target, ErrAgentStatementConflict
			}
			state = r.State
			version = r.Version
			var original AgentPaymentReceipt
			if err := tx.First(&original, r.PaymentReceiptID).Error; err != nil {
				return target, err
			}
			beneficiary = original.CounterpartyDigest
			if beneficiary == "" {
				supplement, err := confirmedAgentStatementEvidenceTx(tx, "incoming_receipt", original.ID)
				if err != nil {
					return target, err
				}
				if supplement != nil {
					beneficiary = supplement.CounterpartyDigest
				}
			}
		case "deposit_refund":
			var r AgentDepositOperationRequest
			if err := tx.Where("business_key = ? AND payout_receipt_id = ? AND kind = ?", outgoing.BusinessKey, outgoing.ID, "refund").First(&r).Error; err != nil {
				return target, err
			}
			if r.AgentUserID != outgoing.OwnerUserID || r.Currency != outgoing.Currency || r.CurrencyExponent != outgoing.CurrencyExponent || r.AmountMinor != outgoing.PaidMinor {
				return target, ErrAgentStatementConflict
			}
			var original AgentDepositReceipt
			if err := tx.First(&original, r.DepositID).Error; err != nil {
				return target, err
			}
			beneficiary = original.PayerDigest
			if beneficiary == "" {
				var originalReceipt AgentPaymentReceipt
				if err := tx.First(&originalReceipt, original.PaymentReceiptID).Error; err != nil {
					return target, err
				}
				beneficiary = originalReceipt.CounterpartyDigest
				if beneficiary == "" {
					supplement, err := confirmedAgentStatementEvidenceTx(tx, "incoming_receipt", originalReceipt.ID)
					if err != nil {
						return target, err
					}
					if supplement != nil {
						beneficiary = supplement.CounterpartyDigest
					}
				}
			}
			if beneficiary != "" && r.RecipientDigest != "" && beneficiary != r.RecipientDigest {
				return target, ErrAgentStatementConflict
			}
			state = r.State
			version = r.Version
		default:
			state = "unbound"
		}
		if outgoing.BeneficiaryDigest != "" && beneficiary != "" && outgoing.BeneficiaryDigest != beneficiary {
			return target, ErrAgentStatementConflict
		}
		target = agentStatementTarget{PaidAt: outgoing.PaidAt, Direction: "outgoing", BusinessReference: outgoing.BusinessKey, ExternalReference: outgoing.ProviderReference, AccountReference: outgoing.MerchantScope, OwnerUserID: outgoing.OwnerUserID, Currency: outgoing.Currency, AmountMinor: outgoing.PaidMinor, BeneficiaryDigest: beneficiary, ReceiptID: outgoing.ID, State: state, Version: version, Verified: outgoing.Provider == "bank_transfer" && outgoing.Environment == "live" && outgoing.VerifiedBy > 0 && validAgentStatementDigest(outgoing.EvidenceHash) && state == "succeeded"}
	}
	return target, nil
}

func agentStatementTargetTx(tx *gorm.DB, kind string, id int64) (agentStatementTarget, error) {
	target, err := agentStatementTargetRawTx(tx, kind, id)
	if err != nil || target.ReceiptID <= 0 {
		return target, err
	}
	receiptKind := "incoming_receipt"
	if target.Direction == "outgoing" {
		receiptKind = "outgoing_receipt"
	}
	supplement, err := confirmedAgentStatementEvidenceTx(tx, receiptKind, target.ReceiptID)
	if err != nil {
		return target, err
	}
	if target.Direction == "outgoing" {
		var receipt AgentOutboundPaymentReceipt
		if err := tx.First(&receipt, target.ReceiptID).Error; err != nil {
			return target, err
		}
		bound := receipt.BeneficiaryDigest
		if bound == "" && supplement != nil {
			bound = supplement.CounterpartyDigest
		}
		if target.BeneficiaryDigest != "" && bound != target.BeneficiaryDigest {
			target.BeneficiaryDigest = ""
		} else {
			target.BeneficiaryDigest = bound
		}
	}
	if target.PaidAt == 0 && supplement != nil {
		target.PaidAt = supplement.PaidAt
	}
	return target, nil
}
func agentStatementMismatch(line AgentStatementLine, period AgentStatementPeriod, t agentStatementTarget) string {
	if line.Direction != t.Direction {
		return "direction_conflict"
	}
	if line.BusinessReference != t.BusinessReference {
		return "business_conflict"
	}
	if line.Currency != t.Currency {
		return "currency_conflict"
	}
	if line.AmountMinor != t.AmountMinor {
		return "amount_conflict"
	}
	if !t.Verified || t.ReceiptID <= 0 {
		return "state_conflict"
	}
	if line.ExternalReference != t.ExternalReference {
		return "external_conflict"
	}
	if period.AccountDossierReference != t.AccountReference {
		return "account_conflict"
	}
	if line.Direction == "outgoing" && (t.BeneficiaryDigest == "" || line.BeneficiaryDigest != t.BeneficiaryDigest) {
		return "beneficiary_conflict"
	}
	if t.PaidAt <= 0 {
		return "date_unverified"
	}
	if line.OccurredAt/86400 != t.PaidAt/86400 {
		return "date_conflict"
	}
	return ""
}
func agentStatementObligationKey(t agentStatementTarget) string {
	return agentStatementDigest([]byte(t.Direction + "\x00" + t.BusinessReference))
}
func raiseAgentStatementExceptionTx(tx *gorm.DB, line AgentStatementLine, actor int, code string) error {
	var existing AgentStatementException
	err := tx.Where("line_id = ? AND code = ? AND state = ?", line.ID, code, "open").First(&existing).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return tx.Create(&AgentStatementException{LineID: line.ID, PeriodID: line.PeriodID, Code: code, State: "open", RaisedBy: actor}).Error
}

// Candidate discovery is exact business-reference lookup, never fuzzy payment
// guessing. Each candidate still requires a fresh, bound independent review.
type AgentStatementCandidate struct {
	TargetType        string `json:"target_type"`
	TargetID          int64  `json:"target_id"`
	BusinessReference string `json:"business_reference"`
	Currency          string `json:"currency"`
	AmountMinor       int64  `json:"amount_minor,string"`
	ExceptionCode     string `json:"exception_code"`
}

func GetAgentStatementCandidates(identity AuthSessionIdentity, lineID int64) ([]AgentStatementCandidate, error) {
	result := []AgentStatementCandidate{}
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		var line AgentStatementLine
		if err := tx.First(&line, lineID).Error; err != nil {
			return err
		}
		var period AgentStatementPeriod
		if err := tx.First(&period, line.PeriodID).Error; err != nil {
			return err
		}
		ids := []int64{}
		kind := "incoming_receipt"
		if line.Direction == "incoming" {
			if err := tx.Model(&AgentPaymentReceipt{}).Where("local_order_key = ?", line.BusinessReference).Limit(20).Pluck("id", &ids).Error; err != nil {
				return err
			}
		} else {
			kind = "outgoing_receipt"
			if err := tx.Model(&AgentOutboundPaymentReceipt{}).Where("business_key = ?", line.BusinessReference).Limit(20).Pluck("id", &ids).Error; err != nil {
				return err
			}
		}
		for _, id := range ids {
			target, err := agentStatementTargetTx(tx, kind, id)
			if err != nil {
				return err
			}
			result = append(result, AgentStatementCandidate{TargetType: kind, TargetID: id, BusinessReference: target.BusinessReference, Currency: target.Currency, AmountMinor: target.AmountMinor, ExceptionCode: agentStatementMismatch(line, period, target)})
		}
		return nil
	})
	return result, err
}
