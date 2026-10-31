package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Exact HTTP replays are checked before reconstructing a now-changed draft.
// A receipt is not transferable authorization: actor, action and every canonical
// input must match, and the actor must still have a live browser identity and
// current permission to read that result. No new proof is consumed or issued.
func ReplayAgentPurchaseOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (any, bool, error) {
	if validateAgentRequestID(requestID) != nil {
		return nil, false, ErrAgentQuotaInvalid
	}
	var canonical any
	kind := ""
	administrator := false
	switch action {
	case "purchase.payment.record", "purchase.payment.confirm":
		var command AgentPurchaseCommand
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, false, ErrAgentPurchaseInvalid
		}
		command.RequestID = requestID
		canonical = command
		administrator = true
		if action == "purchase.payment.record" {
			kind = "purchase_record"
		} else {
			kind = "purchase_confirm"
		}
	case "purchase.refund.request", "purchase.refund.approve", "purchase.refund.reject", "purchase.refund.submit", "purchase.refund.resolve":
		var command AgentPurchaseRefundCommand
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, false, ErrAgentRefundInvalid
		}
		command.RequestID = requestID
		canonical = command
		kind = map[string]string{"purchase.refund.request": "purchase_refund", "purchase.refund.approve": "refund_approve", "purchase.refund.reject": "refund_reject", "purchase.refund.submit": "refund_submit", "purchase.refund.resolve": "refund_resolve"}[action]
		administrator = action != "purchase.refund.request"
	case "procurement.self_use":
		var command AgentProcurementSelfUseRequest
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, false, ErrAgentPurchaseInvalid
		}
		command.RequestID = requestID
		canonical = command
		kind = "procurement_self_use"
	case "delivery.create", "delivery.cancel":
		var command AgentDeliveryCommand
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, false, ErrRedemptionInvalidRequest
		}
		command.RequestID = requestID
		canonical = command
		kind = "delivery_create"
		if action == "delivery.cancel" {
			kind = "delivery_cancel"
			if command.DeliveryID == 0 {
				kind = "legacy_code_cancel"
			}
		}
	case "purchase.legacy.record", "purchase.legacy.confirm":
		var command AgentFundReclassificationCommand
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, false, ErrAgentPurchaseInvalid
		}
		command.RequestID = requestID
		canonical = command
		administrator = true
		kind = "legacy_record"
		if action == "purchase.legacy.confirm" {
			kind = "legacy_confirm"
		}
	case "redemption.allocate":
		var command AgentQuotaAllocationRequest
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, false, ErrAgentQuotaInvalid
		}
		command.RequestID = requestID
		command.Reason = strings.TrimSpace(command.Reason)
		canonical = command
		kind = "allocation"
		administrator = true
	case "redemption.create", "redemption.admin.create":
		var command RedemptionCreateRequest
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, false, ErrRedemptionInvalidRequest
		}
		command.RequestID = requestID
		canonical = command
		kind = "create"
		administrator = strings.Contains(action, ".admin.")
	case "redemption.update", "redemption.admin.update":
		var command AgentLegacyRedemptionUpdateRequest
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, false, ErrRedemptionInvalidRequest
		}
		command.RequestID = requestID
		canonical = command
		kind = "legacy_update"
		administrator = strings.Contains(action, ".admin.")
	case "redemption.delete", "redemption.admin.delete":
		var command AgentLegacyRedemptionDeleteRequest
		if err := common.DecodeJsonStrict(bytes.NewReader(payload), &command); err != nil {
			return nil, false, ErrRedemptionInvalidRequest
		}
		command.RequestID = requestID
		canonical = command
		kind = "legacy_delete"
		administrator = strings.Contains(action, ".admin.")
	default:
		return nil, false, nil
	}
	encoded, err := common.Marshal(canonical)
	if err != nil {
		return nil, false, err
	}
	digest := sha256.Sum256(encoded)
	signature := hex.EncodeToString(digest[:])
	var result any
	found := false
	err = DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := requireDeliveryCustomerTx(tx, identity); err != nil {
			return err
		}
		var operation AgentRedemptionOperation
		err := tx.Where("operation_key = ?", kind+":"+requestID).First(&operation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if operation.ActorUserID != identity.UserID || operation.Kind != kind || operation.Signature != signature {
			return ErrAgentQuotaConflict
		}
		var actor User
		if err := tx.First(&actor, identity.UserID).Error; err != nil {
			return err
		}
		admin := actor.Role == common.RoleAdminUser || actor.Role == common.RoleRootUser
		if administrator && !admin {
			return ErrAgentRedemptionPermission
		}
		if kind == "create" || kind == "legacy_update" || kind == "legacy_delete" {
			originallyAdmin := operation.ActorRole == common.RoleAdminUser || operation.ActorRole == common.RoleRootUser || (operation.ActorRole == 0 && kind == "create" && operation.AgentUserID == 0)
			if originallyAdmin != administrator {
				return ErrAgentQuotaConflict
			}
		}
		if !administrator {
			if operation.AgentUserID != identity.UserID {
				return ErrAgentRedemptionPermission
			}
			profile, err := readAgentProfile(tx, identity.UserID, false)
			if err != nil {
				return err
			}
			if !profile.Allows(AgentCapabilityHistory) {
				return ErrAgentRedemptionPermission
			}
		}
		var stored agentStoredOperationResult
		switch kind {
		case "allocation":
			var account AgentRedemptionAccount
			if err := common.UnmarshalJsonStr(operation.Result, &account); err != nil {
				return err
			}
			result = AgentQuotaAllocationResult{Account: account, Replayed: true}
		case "procurement_self_use":
			var value AgentProcurementSelfUseResult
			if err := common.UnmarshalJsonStr(operation.Result, &value); err != nil {
				return err
			}
			value.Replayed = true
			result = value
		case "create":
			var rows []Redemption
			if err := tx.Unscoped().Where("creation_operation_id = ? AND user_id = ?", operation.ID, identity.UserID).Order("id ASC").Find(&rows).Error; err != nil {
				return err
			}
			keys := make([]string, 0, len(rows))
			for _, row := range rows {
				keys = append(keys, row.Key)
			}
			result = keys
		case "legacy_delete":
			count, err := strconv.ParseInt(operation.Result, 10, 64)
			if err != nil {
				return err
			}
			result = count
		default:
			if err := common.UnmarshalJsonStr(operation.Result, &stored); err != nil {
				return err
			}
			switch stored.ResourceType {
			case "purchase":
				var value AgentPurchaseOrder
				if err := tx.First(&value, stored.ResourceID).Error; err != nil {
					return err
				}
				if !admin {
					value.VerificationEvidence = ""
				}
				result = value
			case "purchase_refund":
				var value AgentPurchaseRefund
				if err := tx.First(&value, stored.ResourceID).Error; err != nil {
					return err
				}
				if !admin {
					value.EvidenceReference = ""
					value.ExternalReference = ""
				}
				result = value
			case "delivery":
				var value AgentDelivery
				if err := tx.First(&value, stored.ResourceID).Error; err != nil {
					return err
				}
				if kind == "delivery_create" && value.State == "created" && value.CustomerUserID == 0 {
					var code Redemption
					if err := tx.Unscoped().First(&code, value.RedemptionID).Error; err != nil {
						return err
					}
					if code.CustomerUserID == 0 && code.UsedUserId == 0 && code.RedeemedTime == 0 && code.Status == common.RedemptionCodeStatusEnabled {
						value.ClaimCode = code.Key
					}
				}
				result = value
			case "redemption":
				var value Redemption
				if err := tx.Unscoped().First(&value, stored.ResourceID).Error; err != nil {
					return err
				}
				result = value
			case "fund_reclassification":
				var value AgentFundReclassification
				if err := tx.First(&value, stored.ResourceID).Error; err != nil {
					return err
				}
				result = value
			default:
				return ErrAgentFundInvariant
			}
		}
		found = true
		return nil
	})
	return result, found, err
}
