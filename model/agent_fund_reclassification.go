package model

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type AgentFundReclassification struct {
	ID                int    `json:"id" gorm:"primaryKey"`
	RequestID         string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	AgentUserID       int    `json:"agent_user_id" gorm:"not null;index"`
	LegacyBatchID     int    `json:"legacy_batch_id" gorm:"not null;index"`
	Quota             int    `json:"quota" gorm:"type:bigint;not null"`
	PaidMinor         string `json:"paid_minor" gorm:"type:varchar(32);not null"`
	Currency          string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent  int    `json:"currency_exponent"`
	PaymentReceiptID  int64  `json:"payment_receipt_id"`
	PolicyVersion     int64  `json:"policy_version"`
	PolicySnapshot    string `json:"-" gorm:"type:text;not null"`
	State             string `json:"state" gorm:"type:varchar(24);not null;index"`
	Version           int64  `json:"version" gorm:"not null"`
	VerifiedBy        int    `json:"verified_by"`
	ReviewedBy        int    `json:"reviewed_by"`
	FundBatchID       int    `json:"fund_batch_id"`
	EvidenceReference string `json:"evidence_reference" gorm:"type:varchar(255);not null"`
	ReviewEvidence    string `json:"review_evidence" gorm:"type:varchar(255)"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
}

type AgentFundReclassificationCommand struct {
	HistoricalProcurementConfirmed bool   `json:"historical_procurement_confirmed"`
	NotWalletTopupConfirmed        bool   `json:"not_wallet_topup_confirmed"`
	RequestID                      string `json:"request_id"`
	ReclassificationID             int    `json:"reclassification_id"`
	Version                        int64  `json:"version"`
	AgentUserID                    int    `json:"agent_user_id"`
	LegacyBatchID                  int    `json:"legacy_batch_id"`
	Quota                          int    `json:"quota"`
	PaidMinor                      string `json:"paid_minor"`
	Currency                       string `json:"currency"`
	CurrencyExponent               int    `json:"currency_exponent"`
	Provider                       string `json:"provider"`
	MerchantScope                  string `json:"merchant_scope"`
	Environment                    string `json:"environment"`
	ExternalReference              string `json:"external_reference"`
	PaidAt                         int64  `json:"paid_at"`
	EvidenceReference              string `json:"evidence_reference"`
	Decision                       string `json:"decision"`
}

func buildAgentReclassificationRecordContextTx(tx *gorm.DB, policy *AgentPolicy, batch *AgentFundBatch, request AgentFundReclassificationCommand) (AgentTransactionContext, error) {
	if !policy.LegacyPurchaseReclassificationEnabled || !policy.RiskPolicyApproved || !policy.PurchaseRefundTermsApproved {
		return AgentTransactionContext{}, ErrAgentFeatureDisabled
	}
	paid, err := strconv.ParseInt(request.PaidMinor, 10, 64)
	if request.Environment != "live" || !request.HistoricalProcurementConfirmed || !request.NotWalletTopupConfirmed || err != nil || paid <= 0 || paid > int64(common.MaxWalletQuota) || strconv.FormatInt(paid, 10) != request.PaidMinor || !validAgentCurrency(request.Currency, request.CurrencyExponent) || request.AgentUserID != batch.AgentUserID || request.LegacyBatchID != batch.ID || batch.SourceKind != AgentFundLegacy || request.PaidAt <= 0 || request.PaidAt > common.GetTimestamp() || strings.TrimSpace(request.EvidenceReference) == "" || len(request.EvidenceReference) > 255 {
		return AgentTransactionContext{}, ErrAgentPurchaseInvalid
	}
	var knownTopups int64
	if err := tx.Model(&TopUp{}).Where("payment_provider = ? AND paid_provider_reference = ?", request.Provider, request.ExternalReference).Count(&knownTopups).Error; err != nil {
		return AgentTransactionContext{}, err
	}
	if knownTopups > 0 {
		return AgentTransactionContext{}, ErrAgentPaymentReceiptConflict
	}

	selection, _, err := previewAgentFundSelectionTx(tx, batch, request.Quota)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	return agentBusinessContextTx(tx, policy, batch.AgentUserID, "purchase.legacy.record", request.RequestID, request.Currency, request.PaidMinor, request.Quota, "legacy_fund_batch", strconv.Itoa(batch.ID), 1, struct {
		Request   AgentFundReclassificationCommand `json:"request"`
		Selection []agentFundSelection             `json:"selection"`
	}{request, selection})
}
func buildAgentReclassificationConfirmContextTx(tx *gorm.DB, policy *AgentPolicy, record *AgentFundReclassification, request AgentFundReclassificationCommand) (AgentTransactionContext, error) {
	if request.Version != record.Version || request.ReclassificationID != record.ID || record.State != "verified" {
		return AgentTransactionContext{}, ErrAgentPurchaseState
	}
	if request.Decision != "approve" && request.Decision != "reject" {
		return AgentTransactionContext{}, ErrAgentPurchaseInvalid
	}
	if strings.TrimSpace(request.EvidenceReference) == "" || len(request.EvidenceReference) > 255 {
		return AgentTransactionContext{}, ErrAgentPurchaseInvalid
	}
	if request.Decision == "approve" && (!policy.LegacyPurchaseReclassificationEnabled || !policy.RiskPolicyApproved) {
		return AgentTransactionContext{}, ErrAgentFeatureDisabled
	}
	return agentBusinessContextTx(tx, policy, record.AgentUserID, "purchase.legacy.confirm", request.RequestID, record.Currency, record.PaidMinor, record.Quota, "fund_reclassification", strconv.Itoa(record.ID), record.Version, request)
}

func RecordAgentFundReclassification(auth AgentTransactionAuthorization, request AgentFundReclassificationCommand) (*AgentFundReclassification, error) {
	var record AgentFundReclassification
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		actor := auth.Identity().UserID
		if actor == request.AgentUserID {
			return ErrAgentPurchaseReview
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "legacy_record", request.RequestID, RedemptionScope{UserID: actor}, request.AgentUserID, request)
		if err != nil {
			return err
		}
		if replay {
			if _, err := requireCurrentAgentCapabilityTx(tx, auth.Identity(), request.AgentUserID, AgentCapabilityAdmin); err != nil {
				return err
			}
			return tx.Where("request_id = ?", request.RequestID).First(&record).Error
		}
		account, err := lockAgentRedemptionAccount(tx, request.AgentUserID, false)
		if err != nil {
			return err
		}
		if err := ensureAgentFundSourcesTx(tx, account); err != nil {
			return err
		}
		var batch AgentFundBatch
		if err := tx.Where("id = ? AND agent_user_id = ?", request.LegacyBatchID, request.AgentUserID).First(&batch).Error; err != nil {
			return err
		}
		context, err := buildAgentReclassificationRecordContextTx(tx, policy, &batch, request)
		if err != nil {
			return err
		}
		snapshot, err := common.Marshal(policy)
		if err != nil {
			return err
		}
		record = AgentFundReclassification{RequestID: request.RequestID, AgentUserID: request.AgentUserID, LegacyBatchID: batch.ID, Quota: request.Quota, PaidMinor: request.PaidMinor, Currency: request.Currency, CurrencyExponent: request.CurrencyExponent, PolicyVersion: policy.ID, PolicySnapshot: string(snapshot), State: "verified", Version: 1, VerifiedBy: actor, EvidenceReference: request.EvidenceReference}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		paid, _ := strconv.ParseInt(request.PaidMinor, 10, 64)
		digest, _ := AgentTransactionParametersHash(request.EvidenceReference)
		receipt, err := ClaimAgentPaymentReceiptTx(tx, &AgentPaymentReceipt{Provider: request.Provider, ProviderReference: request.ExternalReference, MerchantScope: request.MerchantScope, Environment: request.Environment, Purpose: "purchase", LocalOrderKey: "legacy-reclass:" + strconv.Itoa(record.ID), OwnerUserID: record.AgentUserID, Currency: record.Currency, CurrencyExponent: record.CurrencyExponent, PaidMinor: paid, PaidAt: request.PaidAt, VerifiedAt: common.GetTimestamp(), VerifiedBy: actor, EvidenceHash: digest})
		if err != nil {
			return err
		}
		record.PaymentReceiptID = receipt.ID
		if err := tx.Model(&record).Update("payment_receipt_id", receipt.ID).Error; err != nil {
			return err
		}
		if _, err := takeAgentFundSlicesTx(tx, record.AgentUserID, record.Quota, batch.ID, []string{AgentFundLegacy}, AgentSliceReclassFrozen, operation.OperationKey, 0, 0, record.ID); err != nil {
			return err
		}
		if _, err := RequireAgentCapabilityTx(tx, auth.Identity(), record.AgentUserID, context.ProfileVersion, AgentCapabilitySettlement); err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		if err := applyAgentQuotaChangeTx(tx, account, AgentRedemptionLedger{OperationKey: operation.OperationKey, ActorUserID: actor, Kind: "legacy_reclass_freeze", Delta: -record.Quota, ReservedDelta: record.Quota, RequestID: request.RequestID, Reason: "Existing legacy quota frozen for independent historical payment reclassification"}); err != nil {
			return err
		}
		if err := appendAgentPurchaseAuditTx(tx, AgentPurchaseAudit{EventKey: operation.OperationKey, ResourceType: "fund_reclassification", ResourceID: record.ID, AgentUserID: record.AgentUserID, ActorUserID: actor, Action: "purchase.legacy.record", ToState: record.State, Currency: record.Currency, AmountMinor: record.PaidMinor, Quota: record.Quota, EvidenceReference: request.EvidenceReference}); err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "fund_reclassification", record.ID, record.State)
	})
	return &record, err
}

func ConfirmAgentFundReclassification(auth AgentTransactionAuthorization, request AgentFundReclassificationCommand) (*AgentFundReclassification, error) {
	var record AgentFundReclassification
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if err := tx.First(&record, request.ReclassificationID).Error; err != nil {
			return err
		}
		actor := auth.Identity().UserID
		if actor == record.AgentUserID || actor == record.VerifiedBy {
			return ErrAgentPurchaseReview
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "legacy_confirm", request.RequestID, RedemptionScope{UserID: actor}, record.AgentUserID, request)
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&record, request.ReclassificationID).Error; err != nil {
			return err
		}
		if replay {
			_, err := requireCurrentAgentCapabilityTx(tx, auth.Identity(), record.AgentUserID, AgentCapabilityAdmin)
			return err
		}
		account, err := lockAgentRedemptionAccount(tx, record.AgentUserID, false)
		if err != nil {
			return err
		}
		context, err := buildAgentReclassificationConfirmContextTx(tx, policy, &record, request)
		if err != nil {
			return err
		}
		destination := AgentSliceAvailable
		state := "rejected"
		if request.Decision == "approve" {
			destination = AgentSliceReclassified
			state = "confirmed"
		}
		quota, _, err := transitionAgentFundSlicesTx(tx, record.AgentUserID, 0, 0, AgentSliceReclassFrozen, destination, operation.OperationKey, record.ID)
		if err != nil {
			return err
		}
		if quota != record.Quota {
			return ErrAgentFundInvariant
		}
		if request.Decision == "approve" {
			var receipt AgentPaymentReceipt
			if err := tx.First(&receipt, record.PaymentReceiptID).Error; err != nil {
				return err
			}
			if receipt.Environment != "live" || receipt.OwnerUserID != record.AgentUserID || receipt.LocalOrderKey != "legacy-reclass:"+strconv.Itoa(record.ID) || strconv.FormatInt(receipt.PaidMinor, 10) != record.PaidMinor || receipt.Currency != record.Currency || receipt.VerifiedBy != record.VerifiedBy {
				return ErrAgentPaymentReceiptConflict
			}
			batch := AgentFundBatch{SourceKey: "legacy-reclass:" + strconv.Itoa(record.ID), AgentUserID: record.AgentUserID, SourceKind: AgentFundPurchase, OriginalQuota: record.Quota, OriginalPaidMinor: record.PaidMinor, Currency: record.Currency, CurrencyExponent: record.CurrencyExponent, WalletAccount: AccountPaid, CashRefundable: true, PolicyVersion: record.PolicyVersion, PolicySnapshot: record.PolicySnapshot, PaymentReceiptID: record.PaymentReceiptID}
			if err := createAgentFundBatchTx(tx, &batch); err != nil {
				return err
			}
			record.FundBatchID = batch.ID
			if _, err := RequireAgentCapabilityTx(tx, auth.Identity(), record.AgentUserID, context.ProfileVersion, AgentCapabilitySettlement); err != nil {
				return err
			}
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		if err := applyAgentQuotaChangeTx(tx, account, AgentRedemptionLedger{OperationKey: operation.OperationKey, ActorUserID: actor, Kind: "legacy_reclass_finish", Delta: record.Quota, ReservedDelta: -record.Quota, RequestID: request.RequestID, Reason: "Historical source reclassification completed without increasing existing allocation"}); err != nil {
			return err
		}
		record.State = state
		record.Version++
		record.ReviewedBy = actor
		record.ReviewEvidence = request.EvidenceReference
		if err := tx.Model(&record).Select("state", "version", "reviewed_by", "review_evidence", "fund_batch_id").Updates(&record).Error; err != nil {
			return err
		}
		if err := appendAgentPurchaseAuditTx(tx, AgentPurchaseAudit{EventKey: operation.OperationKey, ResourceType: "fund_reclassification", ResourceID: record.ID, AgentUserID: record.AgentUserID, ActorUserID: actor, Action: "purchase.legacy.confirm", FromState: "verified", ToState: state, Currency: record.Currency, AmountMinor: record.PaidMinor, Quota: record.Quota, EvidenceReference: request.EvidenceReference}); err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "fund_reclassification", record.ID, record.State)
	})
	return &record, err
}

func PrepareAgentFundReclassificationOperation(identity AuthSessionIdentity, action, requestID string, request AgentFundReclassificationCommand) (*AgentPreparedOperation, error) {
	request.RequestID = requestID
	var context AgentTransactionContext
	exponent := 0
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := ReadCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		owner := request.AgentUserID
		if action == "purchase.legacy.record" {
			var batch AgentFundBatch
			if err := tx.First(&batch, request.LegacyBatchID).Error; err != nil {
				return err
			}
			owner = batch.AgentUserID
			exponent = request.CurrencyExponent
			context, err = buildAgentReclassificationRecordContextTx(tx, policy, &batch, request)
		} else if action == "purchase.legacy.confirm" {
			var record AgentFundReclassification
			if err := tx.First(&record, request.ReclassificationID).Error; err != nil {
				return err
			}
			owner = record.AgentUserID
			exponent = record.CurrencyExponent
			if identity.UserID == record.VerifiedBy {
				return ErrAgentPurchaseReview
			}
			context, err = buildAgentReclassificationConfirmContextTx(tx, policy, &record, request)
		} else {
			return ErrAgentPurchaseInvalid
		}
		if identity.UserID == owner {
			return ErrAgentPurchaseReview
		}
		if err != nil {
			return err
		}
		_, err = RequireAgentCapabilityTx(tx, identity, owner, context.ProfileVersion, AgentCapabilityAdmin)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &AgentPreparedOperation{VerificationContext: context, Summary: AgentOperationSummary{Action: action, AgentUserID: context.AgentUserID, Currency: context.Currency, CurrencyExponent: exponent, AmountMinor: context.AmountMinor, FeeMinor: "0", NetMinor: context.AmountMinor, Quota: context.Quota, TargetLabel: "Historical payment source reclassification; no new quota", PolicyVersion: context.PolicyVersion}, StatusURL: "/api/agent-platform/operations/" + requestID}, nil
}
func GetAgentFundReclassifications(scope RedemptionScope, start, count int) ([]AgentFundReclassification, int64, error) {
	if !scope.valid() {
		return nil, 0, ErrAgentRedemptionPermission
	}
	query := DB.Model(&AgentFundReclassification{})
	if !scope.IsAdmin() {
		query = query.Where("agent_user_id = ?", scope.UserID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]AgentFundReclassification, 0)
	err := query.Order("id DESC").Offset(max(0, start)).Limit(min(max(count, 1), 100)).Find(&rows).Error
	if !scope.IsAdmin() {
		for i := range rows {
			rows[i].EvidenceReference = ""
			rows[i].ReviewEvidence = ""
		}
	}
	return rows, total, err
}
