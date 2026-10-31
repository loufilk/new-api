package model

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var (
	ErrAgentPurchaseInvalid = errors.New("Invalid agent purchase request")
	ErrAgentPurchaseState   = errors.New("Agent purchase state changed; refresh and prepare again")
	ErrAgentPurchaseReview  = errors.New("Payment verification and final review require different independent finance users")
	ErrAgentPurchaseExpired = errors.New("Agent purchase quote has expired")
)

type AgentPurchaseQuote struct {
	PriceSnapshot    string `json:"price_snapshot" gorm:"type:text"`
	ProfileVersion   int64  `json:"profile_version"`
	ID               int    `json:"id" gorm:"primaryKey"`
	OperationID      int    `json:"-" gorm:"not null;uniqueIndex"`
	AgentUserID      int    `json:"agent_user_id" gorm:"not null;index"`
	Quota            int    `json:"quota" gorm:"type:bigint;not null"`
	PaidMinor        string `json:"paid_minor" gorm:"type:varchar(32);not null"`
	Currency         string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent int    `json:"currency_exponent"`
	RateBPS          int64  `json:"rate_bps"`
	PolicyVersion    int64  `json:"policy_version" gorm:"not null"`
	PolicySnapshot   string `json:"-" gorm:"type:text;not null"`
	ExpiresAt        int64  `json:"expires_at" gorm:"not null"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentPurchaseQuote) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentPurchaseQuote) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentPurchaseOrder struct {
	PayerReference       string `json:"-" gorm:"type:varchar(128)"`
	PayerDigest          string `json:"payer_digest" gorm:"type:varchar(64)"`
	PriceSnapshot        string `json:"price_snapshot" gorm:"type:text"`
	ProfileVersion       int64  `json:"profile_version"`
	ID                   int    `json:"id" gorm:"primaryKey"`
	OperationID          int    `json:"-" gorm:"not null;uniqueIndex"`
	AgentUserID          int    `json:"agent_user_id" gorm:"not null;index"`
	QuoteID              int    `json:"quote_id" gorm:"not null;uniqueIndex"`
	Quota                int    `json:"quota" gorm:"type:bigint;not null"`
	PaidMinor            string `json:"paid_minor" gorm:"type:varchar(32);not null"`
	Currency             string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent     int    `json:"currency_exponent"`
	RateBPS              int64  `json:"rate_bps"`
	PolicyVersion        int64  `json:"policy_version" gorm:"not null"`
	PolicySnapshot       string `json:"-" gorm:"type:text;not null"`
	Status               string `json:"status" gorm:"type:varchar(32);not null;index"`
	Version              int64  `json:"version" gorm:"not null"`
	EvidenceReference    string `json:"evidence_reference" gorm:"type:varchar(255)"`
	VerificationEvidence string `json:"verification_evidence" gorm:"type:varchar(255)"`
	PaymentReceiptID     int64  `json:"payment_receipt_id"`
	VerifiedBy           int    `json:"verified_by"`
	ReviewedBy           int    `json:"reviewed_by"`
	FundBatchID          int    `json:"fund_batch_id"`
	CreatedAt            int64  `json:"created_at" gorm:"autoCreateTime"`
	CreditedAt           int64  `json:"credited_at"`
}

type AgentPurchaseQuoteRequest struct {
	RequestID string `json:"request_id"`
	Quota     int    `json:"quota"`
}
type AgentPurchaseCreateRequest struct {
	RequestID string `json:"request_id"`
	QuoteID   int    `json:"quote_id"`
}

// AgentPurchaseCommand is shared by preparation and execution; the server
// derives amount, owner and target version from the locked persisted order.
type AgentPurchaseCommand struct {
	PayerReference    string `json:"payer_reference,omitempty"`
	RequestID         string `json:"request_id"`
	OrderID           int    `json:"order_id"`
	Version           int64  `json:"version"`
	EvidenceReference string `json:"evidence_reference"`
	Provider          string `json:"provider"`
	MerchantScope     string `json:"merchant_scope"`
	Environment       string `json:"environment"`
	ExternalReference string `json:"external_reference"`
	PaidMinor         string `json:"paid_minor"`
	Currency          string `json:"currency"`
	PaidAt            int64  `json:"paid_at"`
}

func agentBusinessContextTx(tx *gorm.DB, policy *AgentPolicy, owner int, action, requestID, currency, minor string, quota int, targetType, targetID string, targetVersion int64, parameters any) (AgentTransactionContext, error) {
	profile, err := readAgentProfile(tx, owner, false)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	profileVersion := int64(0)
	if profile != nil {
		profileVersion = profile.Version
	}
	return NewAgentTransactionContext(action, requestID, owner, profileVersion, policy.ID, currency, minor, int64(quota), targetType, targetID, targetVersion, parameters)
}

func agentPurchaseQuotePrice(policy *AgentPolicy, quota int) (string, error) {
	if !policy.PurchaseEnabled || !policy.RiskPolicyApproved {
		return "", ErrAgentFeatureDisabled
	}
	if quota < policy.PurchaseMinimumQuota || quota > policy.PurchaseMaximumQuota || quota <= 0 || quota > common.MaxWalletQuota || policy.PurchaseQuotaNumerator <= 0 || policy.PurchaseQuotaDenominator <= 0 || policy.PurchaseRateBPS <= 0 {
		return "", ErrAgentPurchaseInvalid
	}
	amount, err := agentPriceMinor(quota, policy.PurchaseQuotaNumerator, policy.PurchaseQuotaDenominator, policy.PurchaseRateBPS)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(amount, 10), nil
}

func CreateAgentPurchaseQuote(identity AuthSessionIdentity, request AgentPurchaseQuoteRequest) (*AgentPurchaseQuote, error) {
	var quote AgentPurchaseQuote
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "purchase_quote", request.RequestID, RedemptionScope{UserID: identity.UserID}, identity.UserID, request)
		if err != nil {
			return err
		}
		capability := AgentCapabilitySales
		if replay {
			capability = AgentCapabilityHistory
		}
		if _, err := requireCurrentAgentCapabilityTx(tx, identity, identity.UserID, capability); err != nil {
			return err
		}
		if replay {
			return tx.Where("operation_id = ?", operation.ID).First(&quote).Error
		}
		price, err := ResolveAgentPurchasePriceTx(tx, policy, identity.UserID, request.Quota, common.GetTimestamp())
		if err != nil {
			return err
		}
		snapshot, err := common.Marshal(policy)
		if err != nil {
			return err
		}
		priceJSON, err := common.Marshal(price)
		if err != nil {
			return err
		}
		quote = AgentPurchaseQuote{PriceSnapshot: string(priceJSON), ProfileVersion: price.ProfileVersion, OperationID: operation.ID, AgentUserID: identity.UserID, Quota: request.Quota, PaidMinor: price.PaidMinor, Currency: policy.PurchaseCurrency, CurrencyExponent: policy.PurchaseCurrencyExponent, RateBPS: price.RateBPS, PolicyVersion: policy.ID, PolicySnapshot: string(snapshot), ExpiresAt: common.GetTimestamp() + policy.QuoteLifetimeSeconds}
		return tx.Create(&quote).Error
	})
	return &quote, err
}

func CreateAgentPurchaseOrder(identity AuthSessionIdentity, request AgentPurchaseCreateRequest) (*AgentPurchaseOrder, error) {
	var order AgentPurchaseOrder
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "purchase_order", request.RequestID, RedemptionScope{UserID: identity.UserID}, identity.UserID, request)
		if err != nil {
			return err
		}
		var quote AgentPurchaseQuote
		if err := lockForUpdate(tx).Where("id = ? AND agent_user_id = ?", request.QuoteID, identity.UserID).First(&quote).Error; err != nil {
			return err
		}
		capability := AgentCapabilitySales
		if replay {
			capability = AgentCapabilityHistory
		}
		if _, err := requireCurrentAgentCapabilityTx(tx, identity, identity.UserID, capability); err != nil {
			return err
		}
		if replay {
			return tx.Where("operation_id = ?", operation.ID).First(&order).Error
		}
		if !policy.PurchaseEnabled || !policy.RiskPolicyApproved {
			return ErrAgentFeatureDisabled
		}
		if quote.ExpiresAt <= common.GetTimestamp() {
			return ErrAgentPurchaseExpired
		}
		if quote.PolicyVersion != policy.ID {
			return ErrAgentPolicyChanged
		}
		if quote.PriceSnapshot != "" {
			price, err := ResolveAgentPurchasePriceTx(tx, policy, identity.UserID, quote.Quota, common.GetTimestamp())
			if err != nil {
				return err
			}
			current, err := common.Marshal(price)
			if err != nil {
				return err
			}
			if string(current) != quote.PriceSnapshot {
				return ErrAgentAgreementConflict
			}
		}
		order = AgentPurchaseOrder{PriceSnapshot: quote.PriceSnapshot, ProfileVersion: quote.ProfileVersion, OperationID: operation.ID, AgentUserID: identity.UserID, QuoteID: quote.ID, Quota: quote.Quota, PaidMinor: quote.PaidMinor, Currency: quote.Currency, CurrencyExponent: quote.CurrencyExponent, RateBPS: quote.RateBPS, PolicyVersion: quote.PolicyVersion, PolicySnapshot: quote.PolicySnapshot, Status: "awaiting_payment", Version: 1}
		return tx.Create(&order).Error
	})
	order.VerificationEvidence = ""
	return &order, err
}

func SubmitAgentPurchaseEvidence(identity AuthSessionIdentity, request AgentPurchaseCommand) (*AgentPurchaseOrder, error) {
	if len(strings.TrimSpace(request.EvidenceReference)) == 0 || len(request.EvidenceReference) > 255 {
		return nil, ErrAgentPurchaseInvalid
	}
	var order AgentPurchaseOrder
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		_, replay, err := claimAgentRedemptionOperation(tx, "purchase_evidence", request.RequestID, RedemptionScope{UserID: identity.UserID}, identity.UserID, request)
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("id = ? AND agent_user_id = ?", request.OrderID, identity.UserID).First(&order).Error; err != nil {
			return err
		}
		if _, err := requireCurrentAgentCapabilityTx(tx, identity, identity.UserID, AgentCapabilityHistory); err != nil {
			return err
		}
		if replay {
			return nil
		}
		if order.Version != request.Version || (order.Status != "awaiting_payment" && order.Status != "evidence_submitted") {
			return ErrAgentPurchaseState
		}
		order.EvidenceReference = request.EvidenceReference
		order.Status = "evidence_submitted"
		order.Version++
		return tx.Model(&order).Select("evidence_reference", "status", "version").Updates(&order).Error
	})
	order.VerificationEvidence = ""
	return &order, err
}

func buildAgentPurchaseContextTx(tx *gorm.DB, policy *AgentPolicy, order *AgentPurchaseOrder, action string, request AgentPurchaseCommand) (AgentTransactionContext, error) {
	if request.OrderID != order.ID || request.Version != order.Version {
		return AgentTransactionContext{}, ErrAgentPurchaseState
	}
	if action != "purchase.payment.record" && action != "purchase.payment.confirm" {
		return AgentTransactionContext{}, ErrAgentPurchaseInvalid
	}
	return agentBusinessContextTx(tx, policy, order.AgentUserID, action, request.RequestID, order.Currency, order.PaidMinor, order.Quota, "purchase", strconv.Itoa(order.ID), order.Version, request)
}

func PrepareAgentPurchaseContext(identity AuthSessionIdentity, action string, request AgentPurchaseCommand) (AgentTransactionContext, error) {
	var context AgentTransactionContext
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := ReadCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		var order AgentPurchaseOrder
		if err := tx.First(&order, request.OrderID).Error; err != nil {
			return err
		}
		if identity.UserID == order.AgentUserID {
			return ErrAgentPurchaseReview
		}
		if _, err := requireCurrentAgentCapabilityTx(tx, identity, order.AgentUserID, AgentCapabilityAdmin); err != nil {
			return err
		}
		context, err = buildAgentPurchaseContextTx(tx, policy, &order, action, request)
		return err
	})
	return context, err
}

func RecordAgentPurchasePayment(auth AgentTransactionAuthorization, request AgentPurchaseCommand) (*AgentPurchaseOrder, error) {
	var order AgentPurchaseOrder
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if err := tx.First(&order, request.OrderID).Error; err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "purchase_record", request.RequestID, RedemptionScope{UserID: auth.Identity().UserID}, order.AgentUserID, request)
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&order, request.OrderID).Error; err != nil {
			return err
		}
		if replay {
			_, err := requireCurrentAgentCapabilityTx(tx, auth.Identity(), order.AgentUserID, AgentCapabilityAdmin)
			return err
		}
		if auth.Identity().UserID == order.AgentUserID {
			return ErrAgentPurchaseReview
		}
		context, err := buildAgentPurchaseContextTx(tx, policy, &order, "purchase.payment.record", request)
		if err != nil {
			return err
		}
		if order.Status != "evidence_submitted" || order.EvidenceReference == "" {
			return ErrAgentPurchaseState
		}
		paid, err := strconv.ParseInt(request.PaidMinor, 10, 64)
		if request.Environment != "live" || err != nil || strconv.FormatInt(paid, 10) != request.PaidMinor || request.PaidMinor != order.PaidMinor || request.Currency != order.Currency || request.PaidAt < order.CreatedAt || request.PaidAt > common.GetTimestamp() || len(strings.TrimSpace(request.EvidenceReference)) == 0 || len(request.EvidenceReference) > 255 {
			return ErrAgentPurchaseInvalid
		}
		if request.PayerReference != "" && !validAgentDossierReference(request.PayerReference) {
			return ErrAgentPurchaseInvalid
		}
		if policy.ManualReconciliationEnabled && request.Provider == "bank_transfer" && request.PayerReference == "" {
			return ErrAgentPurchaseInvalid
		}
		payerDigest := ""
		if request.PayerReference != "" {
			payerDigest = agentStatementDigest([]byte(request.PayerReference))
		}
		digest, _ := AgentTransactionParametersHash(request.EvidenceReference)
		receipt := AgentPaymentReceipt{Provider: request.Provider, ProviderReference: request.ExternalReference, MerchantScope: request.MerchantScope, Environment: request.Environment, Purpose: "purchase", LocalOrderKey: "purchase:" + strconv.Itoa(order.ID), OwnerUserID: order.AgentUserID, Currency: order.Currency, CurrencyExponent: order.CurrencyExponent, PaidMinor: paid, VerifiedAt: common.GetTimestamp(), VerifiedBy: auth.Identity().UserID, EvidenceHash: digest, PaidAt: request.PaidAt, CounterpartyDigest: payerDigest}
		claimed, err := ClaimAgentPaymentReceiptTx(tx, &receipt)
		if err != nil {
			return err
		}
		if _, err := RequireAgentCapabilityTx(tx, auth.Identity(), order.AgentUserID, context.ProfileVersion, AgentCapabilityAdmin); err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		order.PayerReference = request.PayerReference
		order.PayerDigest = payerDigest
		order.PaymentReceiptID = claimed.ID
		order.VerifiedBy = auth.Identity().UserID
		order.VerificationEvidence = request.EvidenceReference
		order.Status = "verified"
		order.Version++
		if err := tx.Model(&order).Select("payer_reference", "payer_digest", "payment_receipt_id", "verified_by", "verification_evidence", "status", "version").Updates(&order).Error; err != nil {
			return err
		}
		if err := appendAgentPurchaseAuditTx(tx, AgentPurchaseAudit{EventKey: operation.OperationKey, ResourceType: "purchase", ResourceID: order.ID, AgentUserID: order.AgentUserID, ActorUserID: auth.Identity().UserID, Action: "purchase.payment.record", FromState: "evidence_submitted", ToState: order.Status, Currency: order.Currency, AmountMinor: order.PaidMinor, Quota: order.Quota, EvidenceReference: request.EvidenceReference}); err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "purchase", order.ID, order.Status)
	})
	return &order, err
}

func ConfirmAgentPurchasePayment(auth AgentTransactionAuthorization, request AgentPurchaseCommand) (*AgentPurchaseOrder, error) {
	var order AgentPurchaseOrder
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if err := tx.First(&order, request.OrderID).Error; err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "purchase_confirm", request.RequestID, RedemptionScope{UserID: auth.Identity().UserID}, order.AgentUserID, request)
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&order, request.OrderID).Error; err != nil {
			return err
		}
		if replay {
			_, err := requireCurrentAgentCapabilityTx(tx, auth.Identity(), order.AgentUserID, AgentCapabilityAdmin)
			return err
		}
		context, err := buildAgentPurchaseContextTx(tx, policy, &order, "purchase.payment.confirm", request)
		if err != nil {
			return err
		}
		if order.Status != "verified" || order.VerifiedBy == 0 || order.PaymentReceiptID == 0 {
			return ErrAgentPurchaseState
		}
		actor := auth.Identity().UserID
		if actor == order.AgentUserID || actor == order.VerifiedBy {
			return ErrAgentPurchaseReview
		}
		var receipt AgentPaymentReceipt
		if err := tx.First(&receipt, order.PaymentReceiptID).Error; err != nil {
			return err
		}
		if receipt.Environment != "live" || receipt.Purpose != "purchase" || receipt.LocalOrderKey != "purchase:"+strconv.Itoa(order.ID) || receipt.OwnerUserID != order.AgentUserID || receipt.Currency != order.Currency || strconv.FormatInt(receipt.PaidMinor, 10) != order.PaidMinor || receipt.VerifiedBy != order.VerifiedBy || receipt.PaidAt < order.CreatedAt {
			return ErrAgentPaymentReceiptConflict
		}
		if order.PayerDigest != "" && (request.PayerReference != order.PayerReference || agentStatementDigest([]byte(request.PayerReference)) != order.PayerDigest || receipt.CounterpartyDigest != order.PayerDigest) {
			return ErrAgentPaymentReceiptConflict
		}
		account, err := lockAgentRedemptionAccount(tx, order.AgentUserID, true)
		if err != nil {
			return err
		}
		if err := ensureAgentFundSourcesTx(tx, account); err != nil {
			return err
		}
		batch := AgentFundBatch{SourceKey: "purchase:" + strconv.Itoa(order.ID), AgentUserID: order.AgentUserID, SourceKind: AgentFundPurchase, PaymentReceiptID: order.PaymentReceiptID, PurchaseOrderID: order.ID, OriginalQuota: order.Quota, OriginalPaidMinor: order.PaidMinor, Currency: order.Currency, CurrencyExponent: order.CurrencyExponent, WalletAccount: AccountPaid, CashRefundable: true, PolicyVersion: order.PolicyVersion, PolicySnapshot: order.PolicySnapshot}
		if err := createAgentFundBatchTx(tx, &batch); err != nil {
			return err
		}
		if _, err := RequireAgentCapabilityTx(tx, auth.Identity(), order.AgentUserID, context.ProfileVersion, AgentCapabilityAdmin); err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		if err := applyAgentQuotaChangeTx(tx, account, AgentRedemptionLedger{OperationKey: batch.SourceKey, ActorUserID: actor, Kind: "purchase", Delta: order.Quota, AllocatedDelta: order.Quota, RequestID: request.RequestID, Reason: "Independently verified prepaid agent purchase"}); err != nil {
			return err
		}
		order.FundBatchID = batch.ID
		order.ReviewedBy = actor
		order.Status = "credited"
		order.CreditedAt = common.GetTimestamp()
		order.Version++
		if err := tx.Model(&order).Select("fund_batch_id", "reviewed_by", "status", "credited_at", "version").Updates(&order).Error; err != nil {
			return err
		}
		if err := appendAgentPurchaseAuditTx(tx, AgentPurchaseAudit{EventKey: operation.OperationKey, ResourceType: "purchase", ResourceID: order.ID, AgentUserID: order.AgentUserID, ActorUserID: auth.Identity().UserID, Action: "purchase.payment.confirm", FromState: "verified", ToState: order.Status, Currency: order.Currency, AmountMinor: order.PaidMinor, Quota: order.Quota, EvidenceReference: request.EvidenceReference}); err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "purchase", order.ID, order.Status)
	})
	return &order, err
}

func requireCurrentAgentCapabilityTx(tx *gorm.DB, identity AuthSessionIdentity, target int, capability string) (*AgentProfile, error) {
	profile, err := readAgentProfile(tx, target, false)
	if err != nil {
		return nil, err
	}
	version := int64(0)
	if profile != nil {
		version = profile.Version
	}
	return RequireAgentCapabilityTx(tx, identity, target, version, capability)
}

type AgentProcurementSelfUseRequest struct {
	RequestID string `json:"request_id"`
	BatchID   int    `json:"batch_id"`
	Quota     int    `json:"quota"`
}
type AgentProcurementSelfUseResult struct {
	BatchID       int                    `json:"batch_id"`
	Quota         int                    `json:"quota"`
	WalletAccount LedgerAccount          `json:"wallet_account"`
	CostMinor     string                 `json:"cost_minor"`
	Account       AgentRedemptionAccount `json:"account"`
	Replayed      bool                   `json:"replayed"`
}

func buildAgentSelfUseContextTx(tx *gorm.DB, policy *AgentPolicy, batch *AgentFundBatch, request AgentProcurementSelfUseRequest) (AgentTransactionContext, int64, error) {
	if !policy.ProcurementSelfUseEnabled || !policy.RiskPolicyApproved {
		return AgentTransactionContext{}, 0, ErrAgentFeatureDisabled
	}
	if batch.SourceKind != AgentFundPurchase && batch.SourceKind != AgentFundCommission && batch.SourceKind != AgentFundCredit {
		return AgentTransactionContext{}, 0, ErrAgentPurchaseInvalid
	}
	if batch.SourceKind == AgentFundCredit && !policy.CreditSelfUseEnabled {
		return AgentTransactionContext{}, 0, ErrAgentFeatureDisabled
	}
	selection, cost, err := previewAgentFundSelectionTx(tx, batch, request.Quota)
	if err != nil {
		return AgentTransactionContext{}, 0, err
	}
	var economics *AgentFundEconomicPreview
	version := int64(0)
	if batch.SourceKind == AgentFundPurchase || batch.SourceKind == AgentFundCredit {
		economics, err = PreviewAgentFundEconomicSpendTx(tx, batch, request.Quota)
		if err != nil {
			return AgentTransactionContext{}, 0, err
		}
		cost = economics.ChargedDeltaMinor
		version = economics.Version
	}
	// Earned commission remains a noncash reward source, not refundable cash
	// principal or a credit receivable. Preserve its historical book valuation.
	context, err := agentBusinessContextTx(tx, policy, batch.AgentUserID, "procurement.self_use", request.RequestID, batch.Currency, strconv.FormatInt(cost, 10), request.Quota, "fund_batch", strconv.Itoa(batch.ID), version, struct {
		Request       AgentProcurementSelfUseRequest `json:"request"`
		Selection     []agentFundSelection           `json:"selection"`
		WalletAccount LedgerAccount                  `json:"wallet_account"`
		Economic      *AgentFundEconomicPreview      `json:"economic"`
	}{request, selection, batch.WalletAccount, economics})
	return context, cost, err
}

func AgentProcurementSelfUse(auth AgentTransactionAuthorization, request AgentProcurementSelfUseRequest) (*AgentProcurementSelfUseResult, error) {
	var result AgentProcurementSelfUseResult
	walletAfter := 0
	credited := false
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		owner := auth.Identity().UserID
		operation, replay, err := claimAgentRedemptionOperation(tx, "procurement_self_use", request.RequestID, RedemptionScope{UserID: owner}, owner, request)
		if err != nil {
			return err
		}
		if replay {
			if _, err := requireCurrentAgentCapabilityTx(tx, auth.Identity(), owner, AgentCapabilityHistory); err != nil {
				return err
			}
			if err := common.UnmarshalJsonStr(operation.Result, &result); err != nil {
				return err
			}
			result.Replayed = true
			return nil
		}
		var batch AgentFundBatch
		if err := tx.Where("id = ? AND agent_user_id = ?", request.BatchID, owner).First(&batch).Error; err != nil {
			return err
		}
		account, err := lockAgentRedemptionAccount(tx, owner, false)
		if err != nil {
			return err
		}
		context, cost, err := buildAgentSelfUseContextTx(tx, policy, &batch, request)
		if err != nil {
			return err
		}
		if _, err := takeAgentFundSlicesTx(tx, owner, request.Quota, batch.ID, nil, AgentSliceSpent, operation.OperationKey, 0, 0); err != nil {
			return err
		}
		if _, err := RequireAgentCapabilityTx(tx, auth.Identity(), owner, context.ProfileVersion, AgentCapabilitySettlement); err != nil {
			return err
		}

		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		if err := ConsumeAgentGovernanceRiskTx(tx, owner, AgentGovernanceRiskOperationID("procurement.self_use", request.RequestID), "transfer", int64(request.Quota)); err != nil {
			return err
		}
		if err := applyAgentQuotaChangeTx(tx, account, AgentRedemptionLedger{OperationKey: operation.OperationKey, ActorUserID: owner, Kind: "self_use", Delta: -request.Quota, SpentDelta: request.Quota, RequestID: request.RequestID, Reason: "Explicit procurement quota transfer to consumption wallet"}); err != nil {
			return err
		}
		credit, err := CreditQuotaTx(tx, LedgerEntry{UserId: owner, Account: batch.WalletAccount, Delta: request.Quota, Reason: "agent_procurement_self_use", ReferenceType: "agent_self_use", ReferenceId: strconv.Itoa(operation.ID)})
		if err != nil {
			return err
		}
		if credit.Replayed {
			return ErrAgentFundInvariant
		}
		walletAfter = credit.TotalAfter
		credited = true
		result = AgentProcurementSelfUseResult{BatchID: batch.ID, Quota: request.Quota, WalletAccount: batch.WalletAccount, CostMinor: strconv.FormatInt(cost, 10), Account: *account}
		encoded, err := common.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Model(operation).Update("result", string(encoded)).Error
	})
	if err == nil && credited {
		RefreshUserQuotaCache(auth.Identity().UserID, walletAfter)
	}
	return &result, err
}

// Financial statements can contain private proof-document and provider references.
// Database error/slow-query logging must never copy request values into server logs.
func agentFinancialDB() *gorm.DB {
	return DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)})
}
