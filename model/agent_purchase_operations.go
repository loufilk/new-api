package model

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

func PrepareAgentPurchaseOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	if validateAgentRequestID(requestID) != nil {
		return nil, ErrAgentQuotaInvalid
	}
	var context AgentTransactionContext
	summary := AgentOperationSummary{Action: action, AmountMinor: "0", FeeMinor: "0", NetMinor: "0"}
	var err error
	switch action {
	case "purchase.payment.record", "purchase.payment.confirm":
		var command AgentPurchaseCommand
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, ErrAgentPurchaseInvalid
		}
		command.RequestID = requestID
		context, err = PrepareAgentPurchaseContext(identity, action, command)
		var order AgentPurchaseOrder
		if err == nil {
			err = DB.First(&order, command.OrderID).Error
			summary.CurrencyExponent = order.CurrencyExponent
			summary.TargetLabel = "Purchase #" + strconv.Itoa(order.ID)
		}
	case "purchase.refund.request", "purchase.refund.reject", "purchase.refund.approve", "purchase.refund.submit", "purchase.refund.resolve":
		var command AgentPurchaseRefundCommand
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, ErrAgentRefundInvalid
		}
		command.RequestID = requestID
		return prepareAgentPurchaseRefundOperation(identity, action, command)

	case "procurement.self_use":
		var command AgentProcurementSelfUseRequest
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, ErrAgentPurchaseInvalid
		}
		command.RequestID = requestID
		err = DB.Transaction(func(tx *gorm.DB) error {
			policy, err := LockCurrentAgentPolicyTx(tx)
			if err != nil {
				return err
			}
			var batch AgentFundBatch
			if err := tx.Where("id = ? AND agent_user_id = ?", command.BatchID, identity.UserID).First(&batch).Error; err != nil {
				return err
			}
			context, _, err = buildAgentSelfUseContextTx(tx, policy, &batch, command)
			if err != nil {
				return err
			}
			summary.CurrencyExponent = batch.CurrencyExponent
			summary.TargetLabel = "Consumption wallet: " + string(batch.WalletAccount)
			if _, err = RequireAgentCapabilityTx(tx, identity, batch.AgentUserID, context.ProfileVersion, AgentCapabilitySales); err != nil {
				return err
			}
			_, err = RequireAgentCapabilityTx(tx, identity, batch.AgentUserID, context.ProfileVersion, AgentCapabilitySettlement)
			return err
		})
	case "delivery.create", "delivery.cancel":
		var command AgentDeliveryCommand
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, ErrRedemptionInvalidRequest
		}
		command.RequestID = requestID
		err = DB.Transaction(func(tx *gorm.DB) error {
			policy, err := ReadCurrentAgentPolicyTx(tx)
			if err != nil {
				return err
			}
			owner := identity.UserID
			capability := AgentCapabilitySales
			if action == "delivery.create" {
				context, err = buildAgentDeliveryCreateContextTx(tx, policy, owner, command)
				summary.TargetLabel = command.Name
			} else if command.DeliveryID == 0 && command.RedemptionID > 0 {
				var code Redemption
				if err := tx.Where("id = ? AND budget_agent_id = ?", command.RedemptionID, owner).First(&code).Error; err != nil {
					return err
				}
				if code.DeliveryID != 0 || !code.BudgetReserved || code.Status != common.RedemptionCodeStatusEnabled {
					return ErrAgentDeliveryState
				}
				context, err = buildAgentDeliveryCancelContextTx(tx, policy, &code, nil, command)
				capability = AgentCapabilitySettlement
				summary.TargetLabel = "Unbound bearer code #" + strconv.Itoa(code.Id)
			} else {
				var delivery AgentDelivery
				if err := tx.Where("id = ? AND agent_user_id = ?", command.DeliveryID, owner).First(&delivery).Error; err != nil {
					return err
				}
				var code Redemption
				if err := tx.First(&code, delivery.RedemptionID).Error; err != nil {
					return err
				}
				context, err = buildAgentDeliveryCancelContextTx(tx, policy, &code, &delivery, command)
				capability = AgentCapabilitySettlement
				summary.TargetLabel = delivery.Name
			}
			if err != nil {
				return err
			}
			_, err = RequireAgentCapabilityTx(tx, identity, owner, context.ProfileVersion, capability)
			return err
		})
	case "purchase.legacy.record", "purchase.legacy.confirm":
		var command AgentFundReclassificationCommand
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, ErrAgentPurchaseInvalid
		}
		return PrepareAgentFundReclassificationOperation(identity, action, requestID, command)

	case "redemption.allocate", "redemption.create", "redemption.admin.create", "redemption.update", "redemption.admin.update", "redemption.delete", "redemption.admin.delete":
		return PrepareAgentLegacyRedemptionOperation(identity, action, requestID, payload)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := ValidateAgentTransactionContext(context); err != nil {
		return nil, err
	}
	summary.AgentUserID = context.AgentUserID
	summary.Currency = context.Currency
	summary.Quota = context.Quota
	summary.PolicyVersion = context.PolicyVersion
	if !strings.HasPrefix(action, "purchase.refund.") {
		summary.AmountMinor = context.AmountMinor
		summary.NetMinor = context.AmountMinor
	}
	return &AgentPreparedOperation{VerificationContext: context, Summary: summary, StatusURL: "/api/agent-platform/operations/" + requestID}, nil
}

func FindAgentPurchaseOperation(identity AuthSessionIdentity, requestID string) (*AgentOperationStatus, error) {
	if validateAgentRequestID(requestID) != nil {
		return nil, ErrAgentQuotaInvalid
	}
	if err := DB.Transaction(func(tx *gorm.DB) error { return requireDeliveryCustomerTx(tx, identity) }); err != nil {
		return nil, err
	}
	kinds := map[string]string{"create": "redemption.create", "allocation": "redemption.allocate", "legacy_update": "redemption.update", "legacy_delete": "redemption.delete", "legacy_code_cancel": "delivery.cancel", "legacy_record": "purchase.legacy.record", "legacy_confirm": "purchase.legacy.confirm", "purchase_quote": "purchase.quote", "purchase_order": "purchase.create", "purchase_evidence": "purchase.evidence", "purchase_record": "purchase.payment.record", "purchase_confirm": "purchase.payment.confirm", "purchase_refund": "purchase.refund.request", "refund_reject": "purchase.refund.reject", "refund_approve": "purchase.refund.approve", "refund_submit": "purchase.refund.submit", "refund_resolve": "purchase.refund.resolve", "procurement_self_use": "procurement.self_use", "delivery_create": "delivery.create", "delivery_claim": "delivery.claim", "delivery_confirm": "delivery.confirm", "delivery_cancel": "delivery.cancel"}
	keys := make([]string, 0, len(kinds))
	for kind := range kinds {
		keys = append(keys, kind+":"+requestID)
	}
	var operations []AgentRedemptionOperation
	if err := DB.Where("operation_key IN ? AND actor_user_id = ?", keys, identity.UserID).Find(&operations).Error; err != nil {
		return nil, err
	}
	if len(operations) == 0 {
		return nil, nil
	}
	if len(operations) > 1 {
		return nil, ErrAgentQuotaConflict
	}
	operation := operations[0]
	var actor User
	if err := DB.First(&actor, identity.UserID).Error; err != nil {
		return nil, err
	}
	admin := actor.Role == common.RoleAdminUser || actor.Role == common.RoleRootUser
	if actor.Status != common.UserStatusEnabled || (!admin && operation.AgentUserID != identity.UserID && operation.Kind != "delivery_claim" && operation.Kind != "delivery_confirm") {
		return nil, ErrAgentRedemptionPermission
	}

	result := &AgentOperationStatus{RequestID: requestID, Action: kinds[operation.Kind], Status: "completed"}
	var stored agentStoredOperationResult
	if operation.Result != "" {
		_ = common.UnmarshalJsonStr(operation.Result, &stored)
	}
	switch operation.Kind {
	case "create":
		var rows []Redemption
		if err := DB.Unscoped().Where("creation_operation_id = ? AND user_id = ?", operation.ID, identity.UserID).Order("id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		if operation.ActorRole == common.RoleAdminUser || operation.ActorRole == common.RoleRootUser || (operation.ActorRole == 0 && operation.AgentUserID == 0) {
			result.Action = "redemption.admin.create"
		}
		result.ResourceType = "redemption_batch"
		result.ResourceID = strconv.Itoa(operation.ID)
		result.ResourceState = "created"
		result.Resource = rows
	case "allocation":
		var account AgentRedemptionAccount
		if err := common.UnmarshalJsonStr(operation.Result, &account); err != nil {
			return nil, err
		}
		result.ResourceType = "agent_account"
		result.ResourceID = strconv.Itoa(account.AgentUserID)
		result.ResourceState = "allocated"
		result.Resource = account
	case "legacy_update":
		var row Redemption
		if err := DB.Unscoped().First(&row, stored.ResourceID).Error; err != nil {
			return nil, err
		}
		result.ResourceType = "redemption"
		result.ResourceID = strconv.Itoa(row.Id)
		result.ResourceState = "updated"
		result.Resource = row
		if operation.ActorRole == common.RoleAdminUser || operation.ActorRole == common.RoleRootUser {
			result.Action = "redemption.admin.update"
		}
	case "legacy_delete":
		count, err := strconv.ParseInt(operation.Result, 10, 64)
		if err != nil {
			return nil, err
		}
		result.ResourceType = "redemption_selection"
		result.ResourceID = strconv.Itoa(operation.ID)
		result.ResourceState = "deleted"
		result.Resource = map[string]any{"deleted": count}
		if operation.ActorRole == common.RoleAdminUser || operation.ActorRole == common.RoleRootUser {
			result.Action = "redemption.admin.delete"
		}

	case "purchase_quote":
		var resource AgentPurchaseQuote
		if err := DB.Where("operation_id = ?", operation.ID).First(&resource).Error; err != nil {
			return nil, err
		}
		result.ResourceType = "purchase_quote"
		result.ResourceID = strconv.Itoa(resource.ID)
		result.Resource = resource
	case "purchase_order", "purchase_evidence", "purchase_record", "purchase_confirm":
		var resource AgentPurchaseOrder
		query := DB.Where("id = ?", stored.ResourceID)
		if operation.Kind == "purchase_order" {
			query = DB.Where("operation_id = ?", operation.ID)
		}
		if operation.Kind == "purchase_evidence" {
			return &AgentOperationStatus{RequestID: requestID, Action: result.Action, Status: "completed", ResourceType: "purchase"}, nil
		}
		if err := query.First(&resource).Error; err != nil {
			return nil, err
		}
		result.ResourceType = "purchase"
		result.ResourceID = strconv.Itoa(resource.ID)
		result.ResourceState = resource.Status
		result.Resource = resource
	case "procurement_self_use":
		var resource AgentProcurementSelfUseResult
		if err := common.UnmarshalJsonStr(operation.Result, &resource); err != nil {
			return nil, err
		}
		result.ResourceType = "fund_batch"
		result.ResourceID = strconv.Itoa(resource.BatchID)
		result.ResourceState = "spent"
		result.Resource = resource
	default:
		result.ResourceType = stored.ResourceType
		result.ResourceID = strconv.Itoa(stored.ResourceID)
		if stored.ResourceType == "purchase_refund" {
			var resource AgentPurchaseRefund
			if err := DB.First(&resource, stored.ResourceID).Error; err != nil {
				return nil, err
			}
			result.ResourceState = resource.State
			result.Resource = resource
		}
		if stored.ResourceType == "fund_reclassification" {
			var resource AgentFundReclassification
			if err := DB.First(&resource, stored.ResourceID).Error; err != nil {
				return nil, err
			}
			result.ResourceState = resource.State
			result.Resource = resource
		}
		if stored.ResourceType == "delivery" {
			var resource AgentDelivery
			if err := DB.First(&resource, stored.ResourceID).Error; err != nil {
				return nil, err
			}
			if operation.Kind == "delivery_create" && resource.AgentUserID == identity.UserID {
				profile, err := GetAgentProfile(identity.UserID)
				if err != nil {
					return nil, err
				}
				if !profile.Allows(AgentCapabilityHistory) {
					return nil, ErrAgentRedemptionPermission
				}
				if resource.State == "created" && resource.CustomerUserID == 0 {
					var code Redemption
					if err := DB.First(&code, resource.RedemptionID).Error; err != nil {
						return nil, err
					}
					if code.CustomerUserID == 0 && code.UsedUserId == 0 && code.RedeemedTime == 0 && code.Status == common.RedemptionCodeStatusEnabled {
						resource.ClaimCode = code.Key
					}
				}
			}
			result.ResourceState = resource.State
			result.Resource = resource
		}
	}
	if !admin {
		switch resource := result.Resource.(type) {
		case AgentPurchaseOrder:
			resource.VerificationEvidence = ""
			result.Resource = resource
		case AgentPurchaseRefund:
			resource.EvidenceReference = ""
			resource.ExternalReference = ""
			result.Resource = resource
		case AgentFundReclassification:
			resource.EvidenceReference = ""
			resource.ReviewEvidence = ""
			result.Resource = resource
		}
	}

	return result, nil
}

func GetAgentPurchaseOrders(scope RedemptionScope, start, count int) ([]AgentPurchaseOrder, int64, error) {
	if !scope.valid() {
		return nil, 0, ErrAgentRedemptionPermission
	}
	query := DB.Model(&AgentPurchaseOrder{})
	if !scope.IsAdmin() {
		query = query.Where("agent_user_id = ?", scope.UserID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]AgentPurchaseOrder, 0)
	err := query.Order("id DESC").Offset(max(0, start)).Limit(min(max(count, 1), 100)).Find(&rows).Error
	if !scope.IsAdmin() {
		for i := range rows {
			rows[i].VerificationEvidence = ""
		}
	}
	return rows, total, err
}
func GetAgentPurchaseRefunds(scope RedemptionScope, start, count int) ([]AgentPurchaseRefund, int64, error) {
	if !scope.valid() {
		return nil, 0, ErrAgentRedemptionPermission
	}
	query := DB.Model(&AgentPurchaseRefund{})
	if !scope.IsAdmin() {
		query = query.Where("agent_user_id = ?", scope.UserID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]AgentPurchaseRefund, 0)
	err := query.Order("id DESC").Offset(max(0, start)).Limit(min(max(count, 1), 100)).Find(&rows).Error
	if !scope.IsAdmin() {
		for i := range rows {
			rows[i].EvidenceReference = ""
			rows[i].ExternalReference = ""
		}
	}
	return rows, total, err
}
func GetAgentDeliveries(identity AuthSessionIdentity, received bool, start, count int) ([]AgentDelivery, int64, error) {
	query := DB.Model(&AgentDelivery{})
	if received {
		query = query.Where("customer_user_id = ?", identity.UserID)
	} else {
		query = query.Where("agent_user_id = ?", identity.UserID)
		profile, err := GetAgentProfile(identity.UserID)
		if err != nil {
			return nil, 0, err
		}
		if profile == nil || !profile.Allows(AgentCapabilityHistory) {
			return nil, 0, ErrAgentRedemptionPermission
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]AgentDelivery, 0)
	err := query.Order("id DESC").Offset(max(0, start)).Limit(min(max(count, 1), 100)).Find(&rows).Error
	return rows, total, err
}

// Dashboard caches are not sufficient for reading financial statements or
// redeemable credentials after logout, credential rotation or account disable.
func ValidateAgentPurchaseReadIdentity(identity AuthSessionIdentity) error {
	return agentFinancialDB().Transaction(func(tx *gorm.DB) error { return requireDeliveryCustomerTx(tx, identity) })
}
