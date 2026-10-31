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
	ErrAgentDeliveryProtected            = errors.New("Delivered or financially backed codes require the protected delivery workflow")
	ErrAgentDeliveryConfirmationRequired = errors.New("Claim this delivery and explicitly confirm redemption before crediting the wallet")
	ErrAgentDeliveryState                = errors.New("Agent delivery state changed; refresh and try again")
	ErrAgentDeliveryResponsibility       = errors.New("Cancellation requires explicit confirmation and evidence that all customer obligations are resolved")
)

type AgentDelivery struct {
	ID                   int    `json:"id" gorm:"primaryKey"`
	OperationID          int    `json:"-" gorm:"not null;uniqueIndex"`
	AgentUserID          int    `json:"agent_user_id" gorm:"not null;index"`
	RedemptionID         int    `json:"redemption_id" gorm:"not null;uniqueIndex"`
	Name                 string `json:"name" gorm:"type:varchar(80);not null"`
	Quota                int    `json:"quota" gorm:"type:bigint;not null"`
	State                string `json:"state" gorm:"type:varchar(24);not null;index"`
	Version              int64  `json:"version" gorm:"not null"`
	CustomerUserID       int    `json:"customer_user_id" gorm:"not null;index"`
	ExpiresAt            int64  `json:"expires_at"`
	ClaimedAt            int64  `json:"claimed_at"`
	RedeemedAt           int64  `json:"redeemed_at"`
	CancellationEvidence string `json:"cancellation_evidence" gorm:"type:varchar(255)"`
	CreatedAt            int64  `json:"created_at" gorm:"autoCreateTime"`
	ClaimCode            string `json:"claim_code,omitempty" gorm:"-"`
}

type AgentDeliveryCommand struct {
	RequestID                   string `json:"request_id"`
	DeliveryID                  int    `json:"delivery_id"`
	RedemptionID                int    `json:"redemption_id"`
	Version                     int64  `json:"version"`
	Name                        string `json:"name"`
	Quota                       int    `json:"quota"`
	ExpiresAt                   int64  `json:"expires_at"`
	ClaimCode                   string `json:"claim_code,omitempty"`
	CustomerObligationsResolved bool   `json:"customer_obligations_resolved"`
	EvidenceReference           string `json:"evidence_reference"`
}

func buildAgentDeliveryCreateContextTx(tx *gorm.DB, policy *AgentPolicy, owner int, request AgentDeliveryCommand) (AgentTransactionContext, error) {
	if !policy.DeliveryEnabled || !policy.RiskPolicyApproved {
		return AgentTransactionContext{}, ErrAgentFeatureDisabled
	}
	if err := validateRedemptionValue(request.Name, request.Quota, request.ExpiresAt); err != nil {
		return AgentTransactionContext{}, err
	}
	if request.ExpiresAt != 0 && request.ExpiresAt < common.GetTimestamp() {
		return AgentTransactionContext{}, ErrRedemptionInvalidRequest
	}
	return agentBusinessContextTx(tx, policy, owner, "delivery.create", request.RequestID, "", "0", request.Quota, "delivery_request", request.RequestID, 0, request)
}

func CreateAgentDelivery(auth AgentTransactionAuthorization, request AgentDeliveryCommand) (*AgentDelivery, error) {
	var delivery AgentDelivery
	err := DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		owner := auth.Identity().UserID
		operation, replay, err := claimAgentRedemptionOperation(tx, "delivery_create", request.RequestID, RedemptionScope{UserID: owner}, owner, request)
		if err != nil {
			return err
		}
		if replay {
			if _, err := requireCurrentAgentCapabilityTx(tx, auth.Identity(), owner, AgentCapabilityHistory); err != nil {
				return err
			}
			if err := tx.Where("operation_id = ? AND agent_user_id = ?", operation.ID, owner).First(&delivery).Error; err != nil {
				return err
			}
			var code Redemption
			if err := tx.First(&code, delivery.RedemptionID).Error; err != nil {
				return err
			}
			if delivery.State == "created" && delivery.CustomerUserID == 0 && code.CustomerUserID == 0 && code.UsedUserId == 0 && code.RedeemedTime == 0 && code.Status == common.RedemptionCodeStatusEnabled {
				delivery.ClaimCode = code.Key
			}
			return nil
		}
		context, err := buildAgentDeliveryCreateContextTx(tx, policy, owner, request)
		if err != nil {
			return err
		}
		account, err := lockAgentRedemptionAccount(tx, owner, true)
		if err != nil {
			return err
		}
		if err := ensureAgentFundSourcesTx(tx, account); err != nil {
			return err
		}
		code := Redemption{UserId: owner, Name: request.Name, Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(), Quota: request.Quota, ExpiredTime: request.ExpiresAt, BudgetAgentID: owner, BudgetReserved: true, BudgetVersion: 1, CreationOperationID: operation.ID, SourceProtected: true}
		if err := tx.Create(&code).Error; err != nil {
			return err
		}
		if err := reserveRedemptionFundSourcesTx(tx, &code, operation.OperationKey); err != nil {
			return err
		}
		delivery = AgentDelivery{OperationID: operation.ID, AgentUserID: owner, RedemptionID: code.Id, Name: request.Name, Quota: request.Quota, State: "created", Version: 1, ExpiresAt: request.ExpiresAt}
		if err := tx.Create(&delivery).Error; err != nil {
			return err
		}
		if err := tx.Model(&code).Update("delivery_id", delivery.ID).Error; err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		if err := ConsumeAgentGovernanceRiskTx(tx, owner, AgentGovernanceRiskOperationID("delivery.create", request.RequestID), "issuance", int64(request.Quota)); err != nil {
			return err
		}
		if err := applyAgentQuotaChangeTx(tx, account, AgentRedemptionLedger{OperationKey: operation.OperationKey, ActorUserID: owner, RedemptionID: code.Id, Kind: "delivery_reserve", Delta: -request.Quota, ReservedDelta: request.Quota, RequestID: request.RequestID, Reason: "Quota reserved for protected customer delivery"}); err != nil {
			return err
		}
		delivery.ClaimCode = code.Key
		return storeAgentOperationResultTx(tx, operation, "delivery", delivery.ID, delivery.State)
	})
	return &delivery, err
}

// A claim binds ownership but does not credit a wallet, confirm an off-platform
// sale, or change invitation attribution. Only confirm performs redemption.
func ClaimAgentDelivery(identity AuthSessionIdentity, request AgentDeliveryCommand) (*AgentDelivery, error) {
	if len(request.ClaimCode) != 32 || validateAgentRequestID(request.RequestID) != nil {
		return nil, ErrRedemptionInvalidRequest
	}
	var delivery AgentDelivery
	err := DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		var code Redemption
		if err := tx.Where(commonKeyCol+" = ? AND delivery_id > 0", request.ClaimCode).First(&code).Error; err != nil {
			return ErrAgentDeliveryState
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "delivery_claim", request.RequestID, RedemptionScope{UserID: identity.UserID}, code.BudgetAgentID, struct {
			DeliveryID int
			CustomerID int
		}{code.DeliveryID, identity.UserID})
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&delivery, code.DeliveryID).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&code, delivery.RedemptionID).Error; err != nil {
			return err
		}
		if err := requireDeliveryCustomerTx(tx, identity); err != nil {
			return err
		}
		if replay && delivery.CustomerUserID == identity.UserID {
			return nil
		}
		if identity.UserID == delivery.AgentUserID {
			return ErrAgentDeliveryConfirmationRequired
		}
		if delivery.State != "created" || code.CustomerUserID != 0 || code.Status != common.RedemptionCodeStatusEnabled || !code.BudgetReserved || (delivery.ExpiresAt != 0 && delivery.ExpiresAt < common.GetTimestamp()) {
			return ErrAgentDeliveryState
		}
		delivery.CustomerUserID = identity.UserID
		delivery.State = "claimed"
		delivery.Version++
		delivery.ClaimedAt = common.GetTimestamp()
		if err := tx.Model(&code).Update("customer_user_id", identity.UserID).Error; err != nil {
			return err
		}
		if err := tx.Model(&delivery).Select("customer_user_id", "state", "version", "claimed_at").Updates(&delivery).Error; err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "delivery", delivery.ID, delivery.State)
	})
	return &delivery, err
}

func ConfirmAgentDelivery(identity AuthSessionIdentity, request AgentDeliveryCommand) (*AgentDelivery, error) {
	var delivery AgentDelivery
	totalAfter := 0
	credited := false
	err := DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := tx.Where("id = ? AND customer_user_id = ?", request.DeliveryID, identity.UserID).First(&delivery).Error; err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "delivery_confirm", request.RequestID, RedemptionScope{UserID: identity.UserID}, delivery.AgentUserID, request)
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&delivery, request.DeliveryID).Error; err != nil {
			return err
		}
		var code Redemption
		if err := lockForUpdate(tx).First(&code, delivery.RedemptionID).Error; err != nil {
			return err
		}
		if replay {
			return requireDeliveryCustomerTx(tx, identity)
		}
		if delivery.CustomerUserID != identity.UserID || code.CustomerUserID != identity.UserID || delivery.State != "claimed" || delivery.Version != request.Version || code.Status != common.RedemptionCodeStatusEnabled || !code.BudgetReserved {
			return ErrAgentDeliveryState
		}
		if err := updateRedemptionBudgetTx(tx, &code, identity.UserID, 0, true, "redeem"); err != nil {
			return err
		}
		if err := requireDeliveryCustomerTx(tx, identity); err != nil {
			return err
		}
		result, err := creditAgentRedemptionWalletTx(tx, &code, identity.UserID)
		if err != nil {
			return err
		}
		if result.Replayed {
			return ErrAgentFundInvariant
		}
		totalAfter = result.TotalAfter
		credited = true
		code.Status = common.RedemptionCodeStatusUsed
		code.UsedUserId = identity.UserID
		code.RedeemedTime = common.GetTimestamp()
		if err := tx.Model(&code).Select("status", "used_user_id", "redeemed_time", "budget_reserved", "budget_version").Updates(&code).Error; err != nil {
			return err
		}
		delivery.State = "redeemed"
		delivery.Version++
		delivery.RedeemedAt = code.RedeemedTime
		if err := tx.Model(&delivery).Select("state", "version", "redeemed_at").Updates(&delivery).Error; err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "delivery", delivery.ID, delivery.State)
	})
	if err == nil && credited {
		RefreshUserQuotaCache(identity.UserID, totalAfter)
	}
	return &delivery, err
}

func requireDeliveryCustomerTx(tx *gorm.DB, identity AuthSessionIdentity) error {
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat || identity.SessionID == "" {
		return ErrAgentRedemptionPermission
	}
	var user User
	if err := lockForUpdate(tx).Where("id = ?", identity.UserID).First(&user).Error; err != nil {
		return err
	}
	if user.Status != common.UserStatusEnabled {
		return ErrAgentRedemptionPermission
	}
	return ValidateAuthSessionWithTx(tx, identity)
}

func buildAgentDeliveryCancelContextTx(tx *gorm.DB, policy *AgentPolicy, code *Redemption, delivery *AgentDelivery, request AgentDeliveryCommand) (AgentTransactionContext, error) {
	if !request.CustomerObligationsResolved || strings.TrimSpace(request.EvidenceReference) == "" || len(request.EvidenceReference) > 255 {
		return AgentTransactionContext{}, ErrAgentDeliveryResponsibility
	}
	if code.CustomerUserID > 0 || code.UsedUserId > 0 || code.RedeemedTime > 0 || code.Status == common.RedemptionCodeStatusUsed {
		return AgentTransactionContext{}, ErrAgentDeliveryProtected
	}
	targetType := "redemption"
	targetID := code.Id
	version := int64(code.BudgetVersion)
	if delivery != nil {
		if delivery.State != "created" || delivery.CustomerUserID > 0 {
			return AgentTransactionContext{}, ErrAgentDeliveryProtected
		}
		targetType = "delivery"
		targetID = delivery.ID
		version = delivery.Version
	}
	if request.Version != version {
		return AgentTransactionContext{}, ErrAgentDeliveryState
	}
	return agentBusinessContextTx(tx, policy, code.BudgetAgentID, "delivery.cancel", request.RequestID, "", "0", code.Quota, targetType, strconv.Itoa(targetID), version, request)
}

func CancelAgentDelivery(auth AgentTransactionAuthorization, request AgentDeliveryCommand) (*AgentDelivery, error) {
	var delivery AgentDelivery
	err := DB.Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&delivery, request.DeliveryID).Error; err != nil {
			return err
		}
		if delivery.AgentUserID != auth.Identity().UserID {
			return ErrAgentRedemptionPermission
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "delivery_cancel", request.RequestID, RedemptionScope{UserID: auth.Identity().UserID}, delivery.AgentUserID, request)
		if err != nil {
			return err
		}
		var code Redemption
		if err := lockForUpdate(tx).First(&code, delivery.RedemptionID).Error; err != nil {
			return err
		}
		if replay {
			_, err := requireCurrentAgentCapabilityTx(tx, auth.Identity(), delivery.AgentUserID, AgentCapabilityHistory)
			return err
		}
		context, err := buildAgentDeliveryCancelContextTx(tx, policy, &code, &delivery, request)
		if err != nil {
			return err
		}
		if err := updateRedemptionBudgetTx(tx, &code, auth.Identity().UserID, 0, false, "delivery_cancel"); err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		code.Status = common.RedemptionCodeStatusDisabled
		if err := tx.Model(&code).Select("status", "budget_reserved", "budget_version").Updates(&code).Error; err != nil {
			return err
		}
		delivery.State = "cancelled"
		delivery.Version++
		delivery.CancellationEvidence = request.EvidenceReference
		if err := tx.Model(&delivery).Select("state", "version", "cancellation_evidence").Updates(&delivery).Error; err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "delivery", delivery.ID, delivery.State)
	})
	return &delivery, err
}

// CancelAgentLegacyCode is the explicit responsibility-release path for an
// unbound, financially backed bearer code. "Unused" alone is not evidence.
func CancelAgentLegacyCode(auth AgentTransactionAuthorization, request AgentDeliveryCommand) (*Redemption, error) {
	var code Redemption
	err := DB.Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		owner := auth.Identity().UserID
		operation, replay, err := claimAgentRedemptionOperation(tx, "legacy_code_cancel", request.RequestID, RedemptionScope{UserID: owner}, owner, request)
		if err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("id = ? AND budget_agent_id = ?", request.RedemptionID, owner).First(&code).Error; err != nil {
			return err
		}
		if replay {
			_, err := requireCurrentAgentCapabilityTx(tx, auth.Identity(), owner, AgentCapabilityHistory)
			return err
		}
		if code.DeliveryID != 0 || !code.BudgetReserved || code.Status != common.RedemptionCodeStatusEnabled {
			return ErrAgentDeliveryState
		}
		context, err := buildAgentDeliveryCancelContextTx(tx, policy, &code, nil, request)
		if err != nil {
			return err
		}
		if err := updateRedemptionBudgetTx(tx, &code, owner, 0, false, "explicit_cancel"); err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		code.Status = common.RedemptionCodeStatusDisabled
		if err := tx.Model(&code).Select("status", "budget_reserved", "budget_version").Updates(&code).Error; err != nil {
			return err
		}
		if err := appendAgentPurchaseAuditTx(tx, AgentPurchaseAudit{EventKey: operation.OperationKey, ResourceType: "redemption", ResourceID: code.Id, AgentUserID: owner, ActorUserID: owner, Action: "delivery.cancel", FromState: "unbound_bearer", ToState: "cancelled", Quota: code.Quota, EvidenceReference: request.EvidenceReference}); err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "redemption", code.Id, "cancelled")
	})
	return &code, err
}
