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

type AgentCommissionRefundRequest struct {
	RequestID               string `json:"request_id"`
	TopUpReferenceID        string `json:"topup_reference_id"`
	RefundReference         string `json:"refund_reference"`
	CumulativeRefundedMinor int64  `json:"cumulative_refunded_minor,string"`
	EvidenceReference       string `json:"evidence_reference"`
}
type AgentCommissionReserveRequest struct {
	RequestID   string `json:"request_id"`
	UserID      int    `json:"user_id"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor,string"`
	Reason      string `json:"reason"`
}
type AgentCommissionRetryRequest struct {
	RequestID string `json:"request_id"`
	EventID   int64  `json:"event_id"`
	Reason    string `json:"reason"`
}
type AgentLegacyCommissionRequest struct {
	RequestID string `json:"request_id"`
	Quota     int    `json:"quota"`
}

func agentCommissionRefundContext(tx *gorm.DB, policy *AgentPolicy, input AgentCommissionRefundRequest) (AgentTransactionContext, *AgentCommissionRefundFact, error) {
	if validateAgentRequestID(input.RequestID) != nil || input.CumulativeRefundedMinor < 0 || input.RefundReference == "" || len(input.RefundReference) > 255 || input.EvidenceReference == "" || len(input.EvidenceReference) > 255 {
		return AgentTransactionContext{}, nil, ErrAgentCommissionInvalid
	}
	var topup TopUp
	if err := tx.Where("trade_no = ?", input.TopUpReferenceID).First(&topup).Error; err != nil {
		return AgentTransactionContext{}, nil, err
	}
	if topup.PaymentReceiptID <= 0 || topup.Status != common.TopUpStatusSuccess {
		return AgentTransactionContext{}, nil, ErrAgentCommissionUnavailable
	}
	var receipt AgentPaymentReceipt
	if err := tx.First(&receipt, topup.PaymentReceiptID).Error; err != nil {
		return AgentTransactionContext{}, nil, err
	}
	if input.CumulativeRefundedMinor > receipt.PaidMinor {
		return AgentTransactionContext{}, nil, ErrAgentCommissionInvalid
	}
	version, err := agentCommissionProfileVersionTx(tx, topup.UserId)
	if err != nil {
		return AgentTransactionContext{}, nil, err
	}
	hash, err := agentCommissionHash([]any{input, receipt.ID, receipt.Currency, receipt.PaidMinor})
	if err != nil {
		return AgentTransactionContext{}, nil, err
	}
	context := AgentTransactionContext{Action: "commission.refund.record", RequestID: input.RequestID, AgentUserID: topup.UserId, ProfileVersion: version, PolicyVersion: policy.ID, Currency: receipt.Currency, AmountMinor: strconv.FormatInt(input.CumulativeRefundedMinor, 10), TargetType: "topup_refund", TargetID: strconv.Itoa(topup.Id), TargetVersion: receipt.ID, ParametersHash: hash}
	fact := &AgentCommissionRefundFact{TopUpReferenceID: topup.TradeNo, Provider: receipt.Provider, Environment: receipt.Environment, MerchantScope: receipt.MerchantScope, PaymentReference: receipt.ProviderReference, RefundReference: input.RefundReference, Currency: receipt.Currency, CurrencyExponent: receipt.CurrencyExponent, CumulativeRefundedMinor: input.CumulativeRefundedMinor, EvidenceReference: input.EvidenceReference}
	return context, fact, nil
}

func reserveReleaseContext(tx *gorm.DB, policy *AgentPolicy, input AgentCommissionReserveRequest) (AgentTransactionContext, error) {
	if validateAgentRequestID(input.RequestID) != nil || input.UserID <= 0 || input.AmountMinor <= 0 || input.AmountMinor > MaxAgentMoneyMinor || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 255 {
		return AgentTransactionContext{}, ErrAgentCommissionInvalid
	}
	var account AgentCommissionAccount
	if err := tx.Where("user_id = ? AND currency = ?", input.UserID, input.Currency).First(&account).Error; err != nil {
		return AgentTransactionContext{}, err
	}
	if account.ReservedMinor < input.AmountMinor {
		return AgentTransactionContext{}, ErrAgentCommissionInsufficient
	}
	version, err := agentCommissionProfileVersionTx(tx, input.UserID)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	hash, err := agentCommissionHash(input)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	return AgentTransactionContext{Action: "commission.reserve.release", RequestID: input.RequestID, AgentUserID: input.UserID, ProfileVersion: version, PolicyVersion: policy.ID, Currency: input.Currency, AmountMinor: strconv.FormatInt(input.AmountMinor, 10), TargetType: "commission_account", TargetID: strconv.FormatInt(account.ID, 10), TargetVersion: account.Version, ParametersHash: hash}, nil
}

func outboxRetryContext(tx *gorm.DB, policy *AgentPolicy, input AgentCommissionRetryRequest, actorID int) (AgentTransactionContext, error) {
	if validateAgentRequestID(input.RequestID) != nil || input.EventID <= 0 || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 255 {
		return AgentTransactionContext{}, ErrAgentCommissionInvalid
	}
	var event AgentCommissionOutbox
	if err := tx.First(&event, input.EventID).Error; err != nil {
		return AgentTransactionContext{}, err
	}
	if event.Status == "done" {
		return AgentTransactionContext{}, ErrAgentCommissionConflict
	}
	owner := event.SourceUserID
	if owner <= 0 && event.Kind == "provider_refund" {
		var fact AgentVerifiedProviderRefund
		if common.UnmarshalJsonStr(event.Payload, &fact) != nil {
			return AgentTransactionContext{}, ErrAgentCommissionInvalid
		}
		var receipt AgentPaymentReceipt
		err := tx.Where("provider = ? AND provider_reference = ?", fact.Provider, fact.PaymentReference).First(&receipt).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			owner = actorID
		} else if err != nil {
			return AgentTransactionContext{}, err
		} else {
			owner = receipt.OwnerUserID
		}
	} else if owner <= 0 {
		var topup TopUp
		if err := tx.Where("trade_no = ?", event.ReferenceID).First(&topup).Error; err != nil {
			return AgentTransactionContext{}, err
		}
		owner = topup.UserId
	}
	version, err := agentCommissionProfileVersionTx(tx, owner)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	hash, err := agentCommissionHash([]any{input, event.EventKey, event.Payload, event.Status, event.Attempts})
	if err != nil {
		return AgentTransactionContext{}, err
	}
	return AgentTransactionContext{Action: "commission.outbox.retry", RequestID: input.RequestID, AgentUserID: owner, ProfileVersion: version, PolicyVersion: policy.ID, AmountMinor: "0", TargetType: "commission_outbox", TargetID: strconv.FormatInt(event.ID, 10), TargetVersion: int64(event.Attempts), ParametersHash: hash}, nil
}

func legacyCommissionContext(tx *gorm.DB, policy *AgentPolicy, identity AuthSessionIdentity, input AgentLegacyCommissionRequest) (AgentTransactionContext, error) {
	if !ValidReferralTransferIdempotencyKey(input.RequestID) || input.Quota <= 0 || input.Quota > common.MaxWalletQuota {
		return AgentTransactionContext{}, ErrAgentCommissionInvalid
	}
	version, err := agentCommissionProfileVersionTx(tx, identity.UserID)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	hash, err := agentCommissionHash(input)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	return AgentTransactionContext{Action: "commission.convert.legacy", RequestID: input.RequestID, AgentUserID: identity.UserID, ProfileVersion: version, PolicyVersion: policy.ID, AmountMinor: "0", Quota: int64(input.Quota), TargetType: "legacy_affiliate", TargetID: strconv.Itoa(identity.UserID), ParametersHash: hash}, nil
}

// Prepare performs no financial mutation. Execution reconstructs the same
// context under the policy/business locks and atomically consumes its proof.
func PrepareAgentCommissionOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	var prepared AgentPreparedOperation
	err := DB.Transaction(func(tx *gorm.DB) error {
		policy, err := ReadCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		var context AgentTransactionContext
		var summary AgentOperationSummary
		admin := false
		switch action {
		case "commission.withdraw.request", "commission.convert.wallet", "commission.convert.procurement":
			var input AgentCommissionExecuteRequest
			if common.DecodeJsonStrict(bytes.NewReader(payload), &input) != nil {
				return ErrAgentCommissionInvalid
			}
			if input.RequestID != "" && input.RequestID != requestID {
				return ErrAgentCommissionConflict
			}
			input.RequestID = requestID
			var quote AgentCommissionQuote
			if err := tx.Where("id = ? AND user_id = ?", input.QuoteID, identity.UserID).First(&quote).Error; err != nil {
				return err
			}
			if quote.RequestID != requestID || quote.Action != action || quote.PolicyVersion != policy.ID || quote.ExpiresAt <= common.GetTimestamp() {
				return ErrAgentCommissionConflict
			}
			context = agentCommissionQuoteContext(&quote)
			summary = AgentOperationSummary{CurrencyExponent: quote.CurrencyExponent, FeeMinor: strconv.FormatInt(quote.FeeMinor, 10), NetMinor: strconv.FormatInt(quote.NetMinor, 10), TargetLabel: quote.Target}
			if quote.RecipientID != "" {
				var recipient AgentCommissionRecipient
				if err := tx.Where("id = ? AND version = ? AND user_id = ?", quote.RecipientID, quote.RecipientVersion, identity.UserID).First(&recipient).Error; err != nil {
					return err
				}
				summary.RecipientLabel = recipient.Label + " " + recipient.ReferenceMasked
			}
		case "recipient.change":
			var input AgentCommissionRecipientRequest
			if common.DecodeJsonStrict(bytes.NewReader(payload), &input) != nil {
				return ErrAgentCommissionInvalid
			}
			if input.RequestID != "" && input.RequestID != requestID {
				return ErrAgentCommissionConflict
			}
			input.RequestID = requestID
			context, err = recipientChangeContext(tx, identity, policy, input)
			summary.RecipientLabel = input.Label
			summary.TargetLabel = "Versioned settlement recipient"
		case "commission.withdraw.approve", "commission.withdraw.reject", "commission.withdraw.submit", "commission.withdraw.resolve":
			admin = true
			var input AgentCommissionWithdrawalAction
			if common.DecodeJsonStrict(bytes.NewReader(payload), &input) != nil {
				return ErrAgentCommissionInvalid
			}
			if input.RequestID != "" && input.RequestID != requestID {
				return ErrAgentCommissionConflict
			}
			input.RequestID = requestID
			var w AgentCommissionWithdrawal
			if err := tx.First(&w, input.WithdrawalID).Error; err != nil {
				return err
			}
			if identity.UserID == w.RequestedBy || identity.UserID == w.UserID {
				return ErrAgentProfilePermission
			}
			context, err = withdrawalActionContext(tx, policy, &w, action, input)
			summary = AgentOperationSummary{CurrencyExponent: w.CurrencyExponent, FeeMinor: strconv.FormatInt(w.FeeMinor, 10), NetMinor: strconv.FormatInt(w.NetMinor, 10), TargetLabel: w.BusinessNo, RecipientLabel: w.RecipientLabel + " " + w.RecipientMasked}
		case "commission.refund.record":
			admin = true
			var input AgentCommissionRefundRequest
			if common.DecodeJsonStrict(bytes.NewReader(payload), &input) != nil {
				return ErrAgentCommissionInvalid
			}
			if input.RequestID != "" && input.RequestID != requestID {
				return ErrAgentCommissionConflict
			}
			input.RequestID = requestID
			var fact *AgentCommissionRefundFact
			context, fact, err = agentCommissionRefundContext(tx, policy, input)
			if fact != nil {
				summary.CurrencyExponent = fact.CurrencyExponent
			}
			summary.TargetLabel = "Record verified payment refund"
		case "commission.reserve.release":
			admin = true
			var input AgentCommissionReserveRequest
			if common.DecodeJsonStrict(bytes.NewReader(payload), &input) != nil {
				return ErrAgentCommissionInvalid
			}
			if input.RequestID != "" && input.RequestID != requestID {
				return ErrAgentCommissionConflict
			}
			input.RequestID = requestID
			context, err = reserveReleaseContext(tx, policy, input)
			summary.TargetLabel = "Release commission risk reserve"
		case "commission.outbox.retry":
			admin = true
			var input AgentCommissionRetryRequest
			if common.DecodeJsonStrict(bytes.NewReader(payload), &input) != nil {
				return ErrAgentCommissionInvalid
			}
			if input.RequestID != "" && input.RequestID != requestID {
				return ErrAgentCommissionConflict
			}
			input.RequestID = requestID
			context, err = outboxRetryContext(tx, policy, input, identity.UserID)
			summary.TargetLabel = "Retry immutable commission event"
		case "commission.convert.legacy":
			var input AgentLegacyCommissionRequest
			if common.DecodeJsonStrict(bytes.NewReader(payload), &input) != nil {
				return ErrAgentCommissionInvalid
			}
			if input.RequestID != "" && input.RequestID != requestID {
				return ErrAgentCommissionConflict
			}
			input.RequestID = requestID
			context, err = legacyCommissionContext(tx, policy, identity, input)
			summary.TargetLabel = "Legacy invitation reward to consumption wallet"
		default:
			return ErrAgentCommissionInvalid
		}
		if err != nil {
			return err
		}
		if err := requireAgentCommissionReaderTx(tx, identity, context.AgentUserID, admin); err != nil {
			return err
		}
		if action == "commission.refund.record" || action == "commission.reserve.release" || action == "commission.outbox.retry" {
			var actor User
			if err := tx.First(&actor, identity.UserID).Error; err != nil {
				return err
			}
			if actor.Role != common.RoleRootUser {
				return ErrAgentProfilePermission
			}
		}
		if err := ValidateAgentTransactionContext(context); err != nil {
			return err
		}
		summary.Action = action
		summary.AgentUserID = context.AgentUserID
		summary.Currency = context.Currency
		summary.AmountMinor = context.AmountMinor
		summary.Quota = context.Quota
		summary.PolicyVersion = context.PolicyVersion
		if summary.FeeMinor == "" {
			summary.FeeMinor = "0"
		}
		if summary.NetMinor == "" {
			summary.NetMinor = context.AmountMinor
		}
		prepared = AgentPreparedOperation{VerificationContext: context, Summary: summary, StatusURL: "/api/agent-platform/operations/" + requestID}
		return nil
	})
	return &prepared, err
}

func RecordAgentCommissionRefund(authorization AgentTransactionAuthorization, input AgentCommissionRefundRequest) (*AgentCommissionRefundFact, error) {
	actor := authorization.Identity()
	var result *AgentCommissionRefundFact
	err := DB.Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		op, replayed, err := claimAgentCommissionOperationTx(tx, actor.UserID, "commission.refund.record", input.RequestID, input)
		if err != nil {
			return err
		}
		if replayed {
			if err := requireAgentCommissionOperationReaderTx(tx, actor, op); err != nil {
				return err
			}
			result = &AgentCommissionRefundFact{}
			return tx.First(result, op.ResourceID).Error
		}
		context, fact, err := agentCommissionRefundContext(tx, policy, input)
		if err != nil {
			return err
		}
		if actor.UserID == context.AgentUserID {
			return ErrAgentProfilePermission
		}
		var ownAwards int64
		if err := tx.Model(&AgentCommissionAward{}).Where("reference_id = ? AND user_id = ?", input.TopUpReferenceID, actor.UserID).Count(&ownAwards).Error; err != nil {
			return err
		}
		if ownAwards > 0 {
			return ErrAgentProfilePermission
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, context); err != nil {
			return err
		}
		fact.RecordedBy = actor.UserID
		result, err = RecordAgentCommissionRefundTx(tx, *fact)
		if err != nil {
			return err
		}
		op.ResourceType = "commission_refund"
		op.ResourceID = strconv.FormatInt(result.ID, 10)
		return tx.Save(op).Error
	})
	return result, err
}

func ReleaseAgentCommissionReserve(authorization AgentTransactionAuthorization, input AgentCommissionReserveRequest) (*AgentCommissionAccount, error) {
	actor := authorization.Identity()
	var result *AgentCommissionAccount
	err := DB.Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		op, replayed, err := claimAgentCommissionOperationTx(tx, actor.UserID, "commission.reserve.release", input.RequestID, input)
		if err != nil {
			return err
		}
		if replayed {
			if err := requireAgentCommissionOperationReaderTx(tx, actor, op); err != nil {
				return err
			}
			result = &AgentCommissionAccount{}
			return tx.Where("user_id = ? AND currency = ?", input.UserID, input.Currency).First(result).Error
		}
		var account AgentCommissionAccount
		if err := lockForUpdate(tx).Where("user_id = ? AND currency = ?", input.UserID, input.Currency).First(&account).Error; err != nil {
			return err
		}
		context, err := reserveReleaseContext(tx, policy, input)
		if err != nil {
			return err
		}
		before := account
		remaining := input.AmountMinor
		var awards []AgentCommissionAward
		if err := lockForUpdate(tx).Where("user_id = ? AND currency = ? AND reserved_minor > 0", input.UserID, input.Currency).Order("id").Find(&awards).Error; err != nil {
			return err
		}
		for i := range awards {
			take := min(remaining, awards[i].ReservedMinor)
			if take == 0 {
				break
			}
			available, err := recoverAgentCommissionDebtTx(tx, &account, take)
			if err != nil {
				return err
			}
			awards[i].ReservedMinor -= take
			awards[i].AvailableMinor += available
			awards[i].SpentMinor += take - available
			account.AvailableMinor += available
			awards[i].Version++
			remaining -= take
			if err := tx.Save(&awards[i]).Error; err != nil {
				return err
			}
		}
		if remaining != 0 {
			return ErrAgentCommissionInvariant
		}
		account.ReservedMinor -= input.AmountMinor
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, context); err != nil {
			return err
		}
		if err := saveAgentCommissionAccount(tx, before, &account, "reserve_release", "operation", strconv.FormatInt(op.ID, 10), input.RequestID, actor.UserID); err != nil {
			return err
		}
		op.ResourceType = "commission_account"
		op.ResourceID = strconv.FormatInt(account.ID, 10)
		result = &account
		return tx.Save(op).Error
	})
	return result, err
}

func RetryAgentCommissionOutbox(authorization AgentTransactionAuthorization, input AgentCommissionRetryRequest) (*AgentCommissionOutbox, error) {
	actor := authorization.Identity()
	var result AgentCommissionOutbox
	err := DB.Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		op, replayed, err := claimAgentCommissionOperationTx(tx, actor.UserID, "commission.outbox.retry", input.RequestID, input)
		if err != nil {
			return err
		}
		if replayed {
			if err := requireAgentCommissionOperationReaderTx(tx, actor, op); err != nil {
				return err
			}
			return tx.First(&result, input.EventID).Error
		}
		if err := lockForUpdate(tx).First(&result, input.EventID).Error; err != nil {
			return err
		}
		context, err := outboxRetryContext(tx, policy, input, actor.UserID)
		if err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, context); err != nil {
			return err
		}
		result.NextAttemptAt = common.GetTimestamp()
		result.Status = "pending"
		if err := tx.Save(&result).Error; err != nil {
			return err
		}
		op.ResourceType = "commission_outbox"
		op.ResourceID = strconv.FormatInt(result.ID, 10)
		return tx.Save(op).Error
	})
	return &result, err
}

func requireAgentCommissionOperationReaderTx(tx *gorm.DB, identity AuthSessionIdentity, op *AgentCommissionOperation) error {
	if op.UserID != identity.UserID {
		return ErrAgentProfilePermission
	}
	root := op.Action == "commission.refund.record" || op.Action == "commission.reserve.release" || op.Action == "commission.outbox.retry"
	admin := root || strings.HasPrefix(op.Action, "commission.withdraw.")
	if err := requireAgentCommissionReaderTx(tx, identity, identity.UserID, admin); err != nil {
		return err
	}
	if root {
		var user User
		if err := tx.Select("id", "role").First(&user, identity.UserID).Error; err != nil {
			return err
		}
		if user.Role != common.RoleRootUser {
			return ErrAgentProfilePermission
		}
	}
	return nil
}

func FindAgentCommissionOperation(identity AuthSessionIdentity, requestID string) (*AgentOperationStatus, error) {
	if validateAgentRequestID(requestID) != nil && !ValidReferralTransferIdempotencyKey(requestID) {
		return nil, ErrAgentCommissionInvalid
	}
	var result *AgentOperationStatus
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommissionReaderTx(tx, identity, identity.UserID, false); err != nil {
			return err
		}
		var op AgentCommissionOperation
		err := tx.Where("user_id = ? AND request_id = ?", identity.UserID, requestID).First(&op).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var legacy BalanceLedgerOperation
			legacyErr := tx.Where("user_id = ? AND idempotency_key = ?", identity.UserID, fmt.Sprintf("aff_transfer:%d:%s", identity.UserID, requestID)).First(&legacy).Error
			if errors.Is(legacyErr, gorm.ErrRecordNotFound) {
				return nil
			}
			if legacyErr != nil {
				return legacyErr
			}
			var row BalanceLedger
			if err := tx.Where("operation_id = ? AND user_id = ? AND reason = ?", legacy.Id, identity.UserID, LedgerReasonReferralTransfer).First(&row).Error; err != nil {
				return err
			}
			result = &AgentOperationStatus{RequestID: requestID, Action: "commission.convert.legacy", Status: "completed", ResourceType: "legacy_affiliate_transfer", ResourceID: strconv.Itoa(legacy.Id), ResourceState: "committed", Resource: &row}
			return nil
		}
		if err != nil {
			return err
		}
		if err := requireAgentCommissionOperationReaderTx(tx, identity, &op); err != nil {
			return err
		}
		if validateAgentRequestID(requestID) != nil && op.Action != "commission.convert.legacy" {
			return nil
		}

		result = &AgentOperationStatus{RequestID: requestID, Action: op.Action, Status: "completed", ResourceType: op.ResourceType, ResourceID: op.ResourceID, ResourceState: "committed"}
		switch op.ResourceType {
		case "commission_account":
			var row AgentCommissionAccount
			if err := tx.First(&row, op.ResourceID).Error; err != nil {
				return err
			}
			result.Resource = &row
		case "commission_withdrawal":
			var row AgentCommissionWithdrawal
			if err := tx.First(&row, op.ResourceID).Error; err != nil {
				return err
			}
			result.Resource = &row
			result.ResourceState = row.Status
		case "commission_conversion":
			var row AgentCommissionConversion
			if err := tx.First(&row, op.ResourceID).Error; err != nil {
				return err
			}
			result.Resource = &row
		case "commission_recipient":
			var row AgentCommissionRecipient
			if err := tx.Where("id = ? AND request_id = ?", op.ResourceID, requestID).First(&row).Error; err != nil {
				return err
			}
			result.Resource = &row
		case "commission_outbox":
			var row AgentCommissionOutbox
			if err := tx.First(&row, op.ResourceID).Error; err != nil {
				return err
			}
			result.Resource = &row
			result.ResourceState = row.Status
		case "commission_refund":
			var row AgentCommissionRefundFact
			if err := tx.First(&row, op.ResourceID).Error; err != nil {
				return err
			}
			result.Resource = &row
		}
		return nil
	})
	return result, err
}

// ReplayAgentCommissionOperation permits exact lost-ACK retries without asking
// the user to issue a second authorization. It never authorizes new state: the
// current browser identity, original actor, action and full payload must match.
func ReplayAgentCommissionOperation(identity AuthSessionIdentity, action, requestID string, payload any) (any, bool, error) {
	if action == "commission.withdraw.request" {
		action = "withdrawal"
	} else if action == "commission.convert.wallet" || action == "commission.convert.procurement" {
		action = "conversion"
	}
	signature, err := agentCommissionHash(payload)
	if err != nil {
		return nil, false, err
	}
	var op AgentCommissionOperation
	err = DB.Where("user_id = ? AND request_id = ?", identity.UserID, requestID).First(&op).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if op.Action != action || op.Signature != signature {
		return nil, false, ErrAgentCommissionConflict
	}
	status, err := FindAgentCommissionOperation(identity, requestID)
	if err != nil {
		return nil, false, err
	}
	if status == nil {
		return nil, false, ErrAgentCommissionInvariant
	}
	return status.Resource, true, nil
}
