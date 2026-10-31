package model

import (
	"errors"
	"strconv"

	"gorm.io/gorm"
)

// These are controlled registry references, not bank account details. This
// structure is returned only by the current-finance browser-only instructions
// endpoint, never an agent list, operation recovery response, or audit log.
type AgentManualRefundInstruction struct {
	BusinessKey              string `json:"business_key"`
	State                    string `json:"state"`
	Version                  int64  `json:"version"`
	Currency                 string `json:"currency"`
	CurrencyExponent         int    `json:"currency_exponent"`
	AmountMinor              string `json:"amount_minor"`
	Provider                 string `json:"provider"`
	MerchantScope            string `json:"merchant_scope"`
	Environment              string `json:"environment"`
	RecipientKind            string `json:"recipient_kind"`
	RecipientDigest          string `json:"recipient_digest"`
	PayerDossierReference    string `json:"payer_dossier_reference,omitempty"`
	OriginalPaymentReference string `json:"original_payment_reference,omitempty"`
	ManualOnly               bool   `json:"manual_only"`
	ReconciliationOnly       bool   `json:"reconciliation_only"`
}

// A provider's immutable original payment identity can address an exact
// original-route reversal. It is not a bank beneficiary dossier and must never
// be used as a fallback for a bank transfer or an arbitrary new destination.
func originalAgentPurchaseRefundTargetTx(tx *gorm.DB, receiptID int64) (*AgentManualRefundInstruction, error) {
	var receipt AgentPaymentReceipt
	if err := tx.First(&receipt, receiptID).Error; err != nil {
		return nil, err
	}
	if ValidateAgentPaymentReceipt(&receipt) != nil || receipt.Purpose != "purchase" || receipt.Environment != "live" || receipt.VerifiedBy <= 0 || receipt.EvidenceHash == "" {
		return nil, ErrAgentPaymentReceiptConflict
	}
	result := &AgentManualRefundInstruction{Provider: receipt.Provider, MerchantScope: receipt.MerchantScope, Environment: receipt.Environment, ManualOnly: true}
	if receipt.Provider != "bank_transfer" {
		result.RecipientKind = "original_provider_payment"
		result.OriginalPaymentReference = receipt.ProviderReference
		digest, err := AgentTransactionParametersHash(struct {
			Kind, Provider, Merchant, Environment, OriginalPayment, Currency string
		}{result.RecipientKind, receipt.Provider, receipt.MerchantScope, receipt.Environment, receipt.ProviderReference, receipt.Currency})
		result.RecipientDigest = digest
		return result, err
	}
	digest, err := ResolveAgentPaymentCounterpartyDigestTx(tx, receipt.ID)
	if err != nil {
		return nil, err
	}
	result.RecipientKind, result.RecipientDigest = "original_payer_dossier", digest
	var order AgentPurchaseOrder
	err = tx.Where("payment_receipt_id = ?", receipt.ID).First(&order).Error
	if err == nil && order.PayerDigest == digest && validAgentDossierReference(order.PayerReference) && AgentDepositPayerDigest(order.PayerReference) == digest {
		result.PayerDossierReference = order.PayerReference
		return result, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	supplement, err := confirmedAgentStatementEvidenceTx(tx, "incoming_receipt", receipt.ID)
	if err != nil {
		return nil, err
	}
	if supplement == nil || supplement.CounterpartyDigest != digest || !validAgentDossierReference(supplement.DossierReference) || AgentDepositPayerDigest(supplement.DossierReference) != digest {
		return nil, ErrAgentStatementUnresolved
	}
	result.PayerDossierReference = supplement.DossierReference
	return result, nil
}

func GetAgentManualRefundInstruction(identity AuthSessionIdentity, kind string, id int64) (*AgentManualRefundInstruction, error) {
	var result *AgentManualRefundInstruction
	if id <= 0 || kind != "purchase" && kind != "deposit" {
		return nil, ErrAgentRefundInvalid
	}
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := validateAgentProfileOperationReaderTx(tx, identity); err != nil {
			return err
		}
		if kind == "purchase" {
			var refund AgentPurchaseRefund
			if err := tx.First(&refund, id).Error; err != nil {
				return err
			}
			if identity.UserID == refund.AgentUserID || refund.State != "approved" && refund.State != "payment_pending" && refund.State != "payment_unknown" {
				return ErrAgentProfilePermission
			}
			// Legacy in-flight instructions support original-payment investigation,
			// never a new submit or replacement business number.
			if err := ValidateAgentFundRefundQuoteTx(tx, &refund, refund.State == "payment_pending" || refund.State == "payment_unknown"); err != nil {
				return err
			}
			var err error
			result, err = originalAgentPurchaseRefundTargetTx(tx, refund.PaymentReceiptID)
			if err != nil {
				return err
			}
			if refund.RecipientDigest != "" && refund.RecipientDigest != result.RecipientDigest {
				return ErrAgentRefundState
			}
			var economics AgentFundEconomics
			if err := tx.First(&economics, "batch_id = ?", refund.BatchID).Error; err != nil {
				return err
			}
			result.ReconciliationOnly = economics.Scheme != AgentFundEconomicCumulative
			result.BusinessKey, result.State, result.Version = refund.BusinessKey, refund.State, refund.Version
			result.Currency, result.CurrencyExponent, result.AmountMinor = refund.Currency, refund.CurrencyExponent, refund.NetMinor
			return nil
		}
		var request AgentDepositOperationRequest
		if err := tx.First(&request, id).Error; err != nil {
			return err
		}
		if identity.UserID == request.AgentUserID || request.Kind != "refund" || request.State != "approved" && request.State != "payment_pending" && request.State != "payment_unknown" {
			return ErrAgentProfilePermission
		}
		var deposit AgentDepositReceipt
		if err := tx.First(&deposit, request.DepositID).Error; err != nil {
			return err
		}
		if deposit.AgentUserID != request.AgentUserID || deposit.State != "confirmed" || deposit.PayerDigest != request.RecipientDigest || !validAgentDossierReference(deposit.PayerReference) || AgentDepositPayerDigest(deposit.PayerReference) != request.RecipientDigest {
			return ErrAgentDepositInvariant
		}
		var original AgentPaymentReceipt
		if err := tx.First(&original, deposit.PaymentReceiptID).Error; err != nil {
			return err
		}
		if original.Purpose != "deposit" || original.OwnerUserID != request.AgentUserID || original.CounterpartyDigest != request.RecipientDigest || original.Provider != "bank_transfer" || original.Environment != "live" {
			return ErrAgentDepositInvariant
		}
		result = &AgentManualRefundInstruction{BusinessKey: request.BusinessKey, State: request.State, Version: request.Version, Currency: request.Currency, CurrencyExponent: request.CurrencyExponent, AmountMinor: strconv.FormatInt(request.AmountMinor, 10), Provider: original.Provider, MerchantScope: original.MerchantScope, Environment: original.Environment, RecipientKind: "original_payer_dossier", RecipientDigest: request.RecipientDigest, PayerDossierReference: deposit.PayerReference, ManualOnly: true}
		return nil
	})
	return result, err
}
