package model

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrAgentRefundInvalid = errors.New("Invalid agent purchase refund request")
	ErrAgentRefundState   = errors.New("Agent refund state changed; refresh and prepare again")
	ErrAgentRefundWaiting = errors.New("The original purchase refund waiting period has not ended")
	ErrAgentRefundUnknown = errors.New("Unknown payment results remain frozen and must be reconciled under the same business number")
)

type AgentPurchaseRefund struct {
	PaymentReceiptID  int64  `json:"payment_receipt_id" gorm:"index"`
	ID                int    `json:"id" gorm:"primaryKey"`
	RequestID         string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	AgentUserID       int    `json:"agent_user_id" gorm:"not null;index"`
	BatchID           int    `json:"batch_id" gorm:"not null;index"`
	PurchaseOrderID   int    `json:"purchase_order_id" gorm:"not null;index"`
	Quota             int    `json:"quota" gorm:"type:bigint;not null"`
	Currency          string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent  int    `json:"currency_exponent"`
	PrincipalMinor    string `json:"principal_minor" gorm:"type:varchar(32);not null"`
	FeeMinor          string `json:"fee_minor" gorm:"type:varchar(32);not null"`
	NetMinor          string `json:"net_minor" gorm:"type:varchar(32);not null"`
	PolicyVersion     int64  `json:"policy_version" gorm:"not null"`
	PolicySnapshot    string `json:"-" gorm:"type:text;not null"`
	State             string `json:"state" gorm:"type:varchar(32);not null;index"`
	Version           int64  `json:"version" gorm:"not null"`
	BusinessKey       string `json:"business_key" gorm:"type:varchar(96);uniqueIndex"`
	RequestedBy       int    `json:"requested_by"`
	ApprovedBy        int    `json:"approved_by"`
	SubmittedBy       int    `json:"submitted_by"`
	ResolvedBy        int    `json:"resolved_by"`
	EvidenceReference string `json:"evidence_reference" gorm:"type:varchar(255)"`
	ExternalReference string `json:"external_reference" gorm:"type:varchar(255)"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt         int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

type AgentPurchaseRefundCommand struct {
	RequestID          string `json:"request_id"`
	BatchID            int    `json:"batch_id"`
	Quota              int    `json:"quota"`
	RefundID           int    `json:"refund_id"`
	Version            int64  `json:"version"`
	Decision           string `json:"decision"`
	BusinessKey        string `json:"business_key"`
	Outcome            string `json:"outcome"`
	EvidenceReference  string `json:"evidence_reference"`
	ExternalReference  string `json:"external_reference"`
	NoPaymentConfirmed bool   `json:"no_payment_confirmed"`
}

type agentFundSelection struct {
	StartQuota int    `json:"start_quota"`
	Quota      int    `json:"quota"`
	CostMinor  string `json:"cost_minor"`
}

func previewAgentFundSelectionTx(tx *gorm.DB, batch *AgentFundBatch, quota int) ([]agentFundSelection, int64, error) {
	if quota <= 0 || quota > common.MaxWalletQuota {
		return nil, 0, ErrAgentQuotaInvalid
	}
	var available []AgentFundSlice
	if err := tx.Where("batch_id = ? AND agent_user_id = ? AND state = ?", batch.ID, batch.AgentUserID, AgentSliceAvailable).Order("start_quota ASC").Find(&available).Error; err != nil {
		return nil, 0, err
	}
	selection := make([]agentFundSelection, 0)
	remaining := quota
	total := int64(0)
	for _, slice := range available {
		if remaining == 0 {
			break
		}
		take := min(remaining, slice.Quota)
		cost, err := AgentFundSliceCost(batch, slice.StartQuota, take)
		if err != nil {
			return nil, 0, err
		}
		if cost > int64(common.MaxWalletQuota)-total {
			return nil, 0, ErrAgentFundInvariant
		}
		selection = append(selection, agentFundSelection{StartQuota: slice.StartQuota, Quota: take, CostMinor: strconv.FormatInt(cost, 10)})
		total += cost
		remaining -= take
	}
	if remaining != 0 {
		return nil, 0, ErrAgentQuotaInsufficient
	}
	return selection, total, nil
}

func buildAgentRefundRequestContextTx(tx *gorm.DB, policy *AgentPolicy, batch *AgentFundBatch, request AgentPurchaseRefundCommand) (AgentTransactionContext, int64, int64, error) {
	if !policy.PurchaseRefundEnabled || !policy.RiskPolicyApproved {
		return AgentTransactionContext{}, 0, 0, ErrAgentFeatureDisabled
	}
	if batch.SourceKind != AgentFundPurchase || !batch.CashRefundable || batch.PaymentReceiptID <= 0 {
		return AgentTransactionContext{}, 0, 0, ErrAgentRefundInvalid
	}
	var original AgentPolicy
	if err := common.UnmarshalJsonStr(batch.PolicySnapshot, &original); err != nil {
		return AgentTransactionContext{}, 0, 0, ErrAgentFundInvariant
	}
	if !original.PurchaseRefundTermsApproved || !original.RiskPolicyApproved {
		return AgentTransactionContext{}, 0, 0, ErrAgentFeatureDisabled
	}
	if common.GetTimestamp() < batch.CreatedAt+original.RefundWaitingSeconds {
		return AgentTransactionContext{}, 0, 0, ErrAgentRefundWaiting
	}
	selection, gross, err := previewAgentFundSelectionTx(tx, batch, request.Quota)
	if err != nil {
		return AgentTransactionContext{}, 0, 0, err
	}
	fee := int64(0)
	for _, slice := range selection {
		portion, err := AgentFundSliceRefundFee(batch, slice.StartQuota, slice.Quota, original.RefundFeeBPS)
		if err != nil {
			return AgentTransactionContext{}, 0, 0, err
		}
		fee += portion
	}
	if gross-fee <= 0 {
		return AgentTransactionContext{}, 0, 0, ErrAgentRefundInvalid
	}
	context, err := agentBusinessContextTx(tx, policy, batch.AgentUserID, "purchase.refund.request", request.RequestID, batch.Currency, strconv.FormatInt(gross-fee, 10), request.Quota, "fund_batch", strconv.Itoa(batch.ID), 1, struct {
		Request        AgentPurchaseRefundCommand `json:"request"`
		Selection      []agentFundSelection       `json:"selection"`
		OriginalPolicy int64                      `json:"original_policy"`
		Fee            string                     `json:"fee_minor"`
	}{request, selection, batch.PolicyVersion, strconv.FormatInt(fee, 10)})
	// The recipient is not user-editable: it is the verified original purchase.
	context.RecipientVersion = 1
	context.RecipientDigest, _ = AgentTransactionParametersHash(struct {
		ReceiptID int64  `json:"payment_receipt_id"`
		Currency  string `json:"currency"`
	}{batch.PaymentReceiptID, batch.Currency})
	return context, gross, fee, err
}

func buildAgentRefundContextTx(tx *gorm.DB, policy *AgentPolicy, refund *AgentPurchaseRefund, action string, request AgentPurchaseRefundCommand) (AgentTransactionContext, error) {
	if request.RefundID != refund.ID || request.Version != refund.Version {
		return AgentTransactionContext{}, ErrAgentRefundState
	}
	context, err := agentBusinessContextTx(tx, policy, refund.AgentUserID, action, request.RequestID, refund.Currency, refund.NetMinor, refund.Quota, "purchase_refund", strconv.Itoa(refund.ID), refund.Version, struct {
		Request     AgentPurchaseRefundCommand `json:"request"`
		State       string                     `json:"state"`
		BusinessKey string                     `json:"business_key"`
		Fee         string                     `json:"fee_minor"`
	}{request, refund.State, refund.BusinessKey, refund.FeeMinor})
	context.RecipientVersion = 1
	context.RecipientDigest, _ = AgentTransactionParametersHash(struct {
		ReceiptID int64  `json:"payment_receipt_id"`
		Currency  string `json:"currency"`
	}{refund.PaymentReceiptID, refund.Currency})
	return context, err
}

func RequestAgentPurchaseRefund(auth AgentTransactionAuthorization, request AgentPurchaseRefundCommand) (*AgentPurchaseRefund, error) {
	var refund AgentPurchaseRefund
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "purchase_refund", request.RequestID, RedemptionScope{UserID: auth.Identity().UserID}, auth.Identity().UserID, request)
		if err != nil {
			return err
		}
		if replay {
			if err := tx.Where("request_id = ? AND agent_user_id = ?", request.RequestID, auth.Identity().UserID).First(&refund).Error; err != nil {
				return err
			}
			_, err := requireCurrentAgentCapabilityTx(tx, auth.Identity(), refund.AgentUserID, AgentCapabilityHistory)
			return err
		}
		var batch AgentFundBatch
		if err := tx.Where("id = ? AND agent_user_id = ?", request.BatchID, auth.Identity().UserID).First(&batch).Error; err != nil {
			return err
		}
		account, err := lockAgentRedemptionAccount(tx, batch.AgentUserID, false)
		if err != nil {
			return err
		}
		context, gross, fee, err := buildAgentRefundRequestContextTx(tx, policy, &batch, request)
		if err != nil {
			return err
		}
		refund = AgentPurchaseRefund{RequestID: request.RequestID, AgentUserID: batch.AgentUserID, BatchID: batch.ID, PaymentReceiptID: batch.PaymentReceiptID, PurchaseOrderID: batch.PurchaseOrderID, Quota: request.Quota, Currency: batch.Currency, CurrencyExponent: batch.CurrencyExponent, PrincipalMinor: strconv.FormatInt(gross, 10), FeeMinor: strconv.FormatInt(fee, 10), NetMinor: strconv.FormatInt(gross-fee, 10), PolicyVersion: batch.PolicyVersion, PolicySnapshot: batch.PolicySnapshot, State: "requested", Version: 1, RequestedBy: auth.Identity().UserID, BusinessKey: "pending:" + request.RequestID}
		if err := tx.Create(&refund).Error; err != nil {
			return err
		}
		refund.BusinessKey = "agent-purchase-refund:" + strconv.Itoa(refund.ID)
		if err := tx.Model(&refund).Update("business_key", refund.BusinessKey).Error; err != nil {
			return err
		}
		if _, err := takeAgentFundSlicesTx(tx, batch.AgentUserID, request.Quota, batch.ID, []string{AgentFundPurchase}, AgentSliceRefundFrozen, operation.OperationKey, 0, refund.ID); err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		if err := applyAgentQuotaChangeTx(tx, account, AgentRedemptionLedger{OperationKey: operation.OperationKey, ActorUserID: auth.Identity().UserID, Kind: "refund_freeze", Delta: -request.Quota, ReservedDelta: request.Quota, RequestID: request.RequestID, Reason: "Original purchase quota frozen for independent refund review"}); err != nil {
			return err
		}
		if err := appendAgentPurchaseAuditTx(tx, AgentPurchaseAudit{EventKey: operation.OperationKey, ResourceType: "purchase_refund", ResourceID: refund.ID, AgentUserID: refund.AgentUserID, ActorUserID: auth.Identity().UserID, Action: "purchase.refund.request", ToState: refund.State, Currency: refund.Currency, AmountMinor: refund.NetMinor, Quota: refund.Quota, BusinessKey: refund.BusinessKey}); err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "purchase_refund", refund.ID, refund.State)
	})
	refund.EvidenceReference = ""
	refund.ExternalReference = ""
	return &refund, err
}

func TransitionAgentPurchaseRefund(auth AgentTransactionAuthorization, action string, request AgentPurchaseRefundCommand) (*AgentPurchaseRefund, error) {
	if action != "purchase.refund.reject" && action != "purchase.refund.approve" && action != "purchase.refund.submit" && action != "purchase.refund.resolve" {
		return nil, ErrAgentRefundInvalid
	}
	var refund AgentPurchaseRefund
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if err := tx.First(&refund, request.RefundID).Error; err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, map[string]string{"purchase.refund.reject": "refund_reject", "purchase.refund.approve": "refund_approve", "purchase.refund.submit": "refund_submit", "purchase.refund.resolve": "refund_resolve"}[action], request.RequestID, RedemptionScope{UserID: auth.Identity().UserID}, refund.AgentUserID, request)
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&refund, request.RefundID).Error; err != nil {
			return err
		}
		actor := auth.Identity().UserID
		if actor == refund.AgentUserID || actor == refund.RequestedBy {
			return ErrAgentPurchaseReview
		}
		if replay {
			_, err := requireCurrentAgentCapabilityTx(tx, auth.Identity(), refund.AgentUserID, AgentCapabilityAdmin)
			return err
		}
		context, err := buildAgentRefundContextTx(tx, policy, &refund, action, request)
		if err != nil {
			return err
		}
		account, err := lockAgentRedemptionAccount(tx, refund.AgentUserID, false)
		if err != nil {
			return err
		}
		previousState := refund.State
		release := false
		success := false
		switch action {
		case "purchase.refund.approve", "purchase.refund.reject":
			if refund.State != "requested" {
				return ErrAgentRefundState
			}
			if request.Decision == "approve" && action == "purchase.refund.approve" {
				refund.State = "approved"
			} else if request.Decision == "reject" && action == "purchase.refund.reject" {
				refund.State = "rejected"
				release = true
			} else {
				return ErrAgentRefundInvalid
			}
			if len(strings.TrimSpace(request.EvidenceReference)) == 0 || len(request.EvidenceReference) > 255 {
				return ErrAgentRefundInvalid
			}
			refund.ApprovedBy = actor
			refund.EvidenceReference = request.EvidenceReference
		case "purchase.refund.submit":
			if refund.State == "payment_unknown" || refund.State == "payment_pending" {
				return ErrAgentRefundUnknown
			}
			if refund.State != "approved" || refund.ApprovedBy == 0 || request.BusinessKey != refund.BusinessKey {
				return ErrAgentRefundState
			}
			refund.State = "payment_pending"
			refund.SubmittedBy = actor
		case "purchase.refund.resolve":
			if (refund.State != "payment_pending" && refund.State != "payment_unknown") || request.BusinessKey != refund.BusinessKey {
				return ErrAgentRefundState
			}
			if len(strings.TrimSpace(request.EvidenceReference)) == 0 || len(request.EvidenceReference) > 255 || len(request.ExternalReference) > 255 {
				return ErrAgentRefundInvalid
			}
			switch request.Outcome {
			case "success":
				if strings.TrimSpace(request.ExternalReference) == "" {
					return ErrAgentRefundInvalid
				}
				refund.State = "succeeded"
				success = true
			case "explicit_failed":
				if !request.NoPaymentConfirmed {
					return ErrAgentRefundUnknown
				}
				refund.State = "failed"
				release = true
			case "unknown":
				refund.State = "payment_unknown"
			default:
				return ErrAgentRefundInvalid
			}
			refund.ResolvedBy = actor
			refund.EvidenceReference = request.EvidenceReference
			refund.ExternalReference = request.ExternalReference
		}
		if success {
			var original AgentPaymentReceipt
			if err := tx.First(&original, refund.PaymentReceiptID).Error; err != nil {
				return err
			}
			net, err := strconv.ParseInt(refund.NetMinor, 10, 64)
			if err != nil {
				return ErrAgentFundInvariant
			}
			digest, err := AgentTransactionParametersHash(request.EvidenceReference)
			if err != nil {
				return err
			}
			if _, err := ClaimAgentOutboundPaymentReceiptTx(tx, &AgentOutboundPaymentReceipt{Provider: original.Provider, ProviderReference: request.ExternalReference, MerchantScope: original.MerchantScope, Environment: original.Environment, Purpose: "purchase_refund", BusinessKey: refund.BusinessKey, OwnerUserID: refund.AgentUserID, Currency: refund.Currency, CurrencyExponent: refund.CurrencyExponent, PaidMinor: net, VerifiedBy: actor, EvidenceHash: digest}); err != nil {
				return err
			}
		}

		if release || success {
			destination := AgentSliceAvailable
			if success {
				destination = AgentSliceRefunded
			}
			quota, cost, err := transitionAgentFundSlicesTx(tx, refund.AgentUserID, 0, refund.ID, AgentSliceRefundFrozen, destination, operation.OperationKey)
			if err != nil {
				return err
			}
			if quota != refund.Quota || strconv.FormatInt(cost, 10) != refund.PrincipalMinor {
				return ErrAgentFundInvariant
			}
			entry := AgentRedemptionLedger{OperationKey: operation.OperationKey, ActorUserID: actor, Kind: "refund_release", ReservedDelta: -quota, Delta: quota, RequestID: request.RequestID, Reason: "Refund rejected or explicitly confirmed unpaid; original slices restored"}
			if success {
				entry.Kind = "refund_success"
				entry.Delta = 0
				entry.AllocatedDelta = -quota
				entry.Reason = "Original purchase refund payment confirmed"
			}
			if err := applyAgentQuotaChangeTx(tx, account, entry); err != nil {
				return err
			}
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		refund.Version++
		if err := tx.Model(&refund).Select("state", "version", "approved_by", "submitted_by", "resolved_by", "evidence_reference", "external_reference").Updates(&refund).Error; err != nil {
			return err
		}
		if err := appendAgentPurchaseAuditTx(tx, AgentPurchaseAudit{EventKey: operation.OperationKey, ResourceType: "purchase_refund", ResourceID: refund.ID, AgentUserID: refund.AgentUserID, ActorUserID: actor, Action: action, FromState: previousState, ToState: refund.State, Currency: refund.Currency, AmountMinor: refund.NetMinor, Quota: refund.Quota, BusinessKey: refund.BusinessKey, EvidenceReference: request.EvidenceReference, ExternalReference: request.ExternalReference}); err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "purchase_refund", refund.ID, refund.State)
	})
	return &refund, err
}

type agentStoredOperationResult struct {
	ResourceType  string `json:"resource_type"`
	ResourceID    int    `json:"resource_id"`
	ResourceState string `json:"resource_state"`
}

func storeAgentOperationResultTx(tx *gorm.DB, operation *AgentRedemptionOperation, resourceType string, id int, state string) error {
	result, err := common.Marshal(agentStoredOperationResult{resourceType, id, state})
	if err != nil {
		return err
	}
	return tx.Model(operation).Update("result", string(result)).Error
}

func PrepareAgentPurchaseRefundContext(identity AuthSessionIdentity, action string, request AgentPurchaseRefundCommand) (AgentTransactionContext, error) {
	prepared, err := prepareAgentPurchaseRefundOperation(identity, action, request)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	return prepared.VerificationContext, nil
}

func prepareAgentPurchaseRefundOperation(identity AuthSessionIdentity, action string, request AgentPurchaseRefundCommand) (*AgentPreparedOperation, error) {
	var context AgentTransactionContext
	summary := AgentOperationSummary{Action: action, RecipientLabel: "Original verified payment channel"}
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := ReadCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		owner := 0
		capability := AgentCapabilityAdmin
		if action == "purchase.refund.request" {
			var batch AgentFundBatch
			if err := tx.Where("id = ? AND agent_user_id = ?", request.BatchID, identity.UserID).First(&batch).Error; err != nil {
				return err
			}
			owner = batch.AgentUserID
			capability = AgentCapabilitySettlement
			var gross, fee int64
			context, gross, fee, err = buildAgentRefundRequestContextTx(tx, policy, &batch, request)
			summary.AmountMinor = strconv.FormatInt(gross, 10)
			summary.FeeMinor = strconv.FormatInt(fee, 10)
			summary.NetMinor = strconv.FormatInt(gross-fee, 10)
			summary.CurrencyExponent = batch.CurrencyExponent
			summary.TargetLabel = "Original payment receipt #" + strconv.FormatInt(batch.PaymentReceiptID, 10)
		} else {
			var refund AgentPurchaseRefund
			if err := tx.First(&refund, request.RefundID).Error; err != nil {
				return err
			}
			owner = refund.AgentUserID
			if identity.UserID == owner {
				return ErrAgentPurchaseReview
			}
			context, err = buildAgentRefundContextTx(tx, policy, &refund, action, request)
			summary.AmountMinor = refund.PrincipalMinor
			summary.FeeMinor = refund.FeeMinor
			summary.NetMinor = refund.NetMinor
			summary.CurrencyExponent = refund.CurrencyExponent
			summary.TargetLabel = refund.BusinessKey
		}
		if err != nil {
			return err
		}
		_, err = RequireAgentCapabilityTx(tx, identity, owner, context.ProfileVersion, capability)
		return err
	})
	if err != nil {
		return nil, err
	}
	summary.AgentUserID = context.AgentUserID
	summary.Currency = context.Currency
	summary.Quota = context.Quota
	summary.PolicyVersion = context.PolicyVersion
	return &AgentPreparedOperation{VerificationContext: context, Summary: summary, StatusURL: "/api/agent-platform/operations/" + request.RequestID}, nil
}
