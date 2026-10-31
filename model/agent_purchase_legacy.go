package model

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type AgentLegacyRedemptionDeleteRequest struct {
	RequestID   string `json:"request_id"`
	IDs         []int  `json:"ids"`
	InvalidOnly bool   `json:"invalid_only"`
	AgentUserID int    `json:"agent_user_id"`
}
type AgentLegacyRedemptionUpdateRequest struct {
	RedemptionUpdateRequest
	StatusOnly bool `json:"status_only"`
}
type agentRedemptionAuthorizationSnapshot struct {
	ID        int   `json:"id"`
	Owner     int   `json:"owner"`
	Quota     int   `json:"quota"`
	Status    int   `json:"status"`
	Version   int   `json:"version"`
	ExpiresAt int64 `json:"expires_at"`
	Customer  int   `json:"customer"`
	Delivery  int   `json:"delivery"`
	Protected bool  `json:"protected"`
}

func agentRedemptionSnapshot(code *Redemption) agentRedemptionAuthorizationSnapshot {
	return agentRedemptionAuthorizationSnapshot{code.Id, code.BudgetAgentID, code.Quota, code.Status, code.BudgetVersion, code.ExpiredTime, code.CustomerUserID, code.DeliveryID, code.SourceProtected}
}
func legacyRedemptionAction(scope RedemptionScope, operation string) string {
	if scope.IsAdmin() {
		return "redemption.admin." + operation
	}
	return "redemption." + operation
}

func buildAgentAllocationContextTx(tx *gorm.DB, policy *AgentPolicy, account *AgentRedemptionAccount, request AgentQuotaAllocationRequest) (AgentTransactionContext, error) {
	quota := request.Delta
	if quota < 0 {
		quota = -quota
	}
	return agentBusinessContextTx(tx, policy, request.AgentUserID, "redemption.allocate", request.RequestID, "", "0", quota, "agent_account", strconv.Itoa(request.AgentUserID), 0, struct {
		Request   AgentQuotaAllocationRequest `json:"request"`
		Available int                         `json:"available"`
		Reserved  int                         `json:"reserved"`
		Spent     int                         `json:"spent"`
		Allocated int                         `json:"allocated"`
	}{request, account.AvailableQuota, account.ReservedQuota, account.SpentQuota, account.AllocatedQuota})
}
func buildAgentLegacyCreateContextTx(tx *gorm.DB, policy *AgentPolicy, scope RedemptionScope, request RedemptionCreateRequest) (AgentTransactionContext, error) {
	if request.Count <= 0 || request.Count > 100 || request.Quota <= 0 || request.Quota > common.MaxWalletQuota/request.Count {
		return AgentTransactionContext{}, ErrRedemptionInvalidRequest
	}
	return agentBusinessContextTx(tx, policy, scope.UserID, legacyRedemptionAction(scope, "create"), request.RequestID, "", "0", request.Quota*request.Count, "redemption_request", request.RequestID, 0, request)
}
func buildAgentLegacyUpdateContextTx(tx *gorm.DB, policy *AgentPolicy, scope RedemptionScope, code *Redemption, request RedemptionUpdateRequest, statusOnly bool) (AgentTransactionContext, error) {
	if request.Version != int64(code.BudgetVersion) {
		return AgentTransactionContext{}, ErrAgentQuotaConflict
	}
	quota := request.Quota
	if statusOnly {
		quota = code.Quota
	}
	if quota < 0 || quota > common.MaxWalletQuota {
		return AgentTransactionContext{}, ErrRedemptionInvalidRequest
	}
	return agentBusinessContextTx(tx, policy, scope.UserID, legacyRedemptionAction(scope, "update"), request.RequestID, "", "0", quota, "redemption", strconv.Itoa(code.Id), int64(code.BudgetVersion), struct {
		Request    RedemptionUpdateRequest              `json:"request"`
		StatusOnly bool                                 `json:"status_only"`
		Code       agentRedemptionAuthorizationSnapshot `json:"code"`
	}{request, statusOnly, agentRedemptionSnapshot(code)})
}
func buildAgentLegacyDeleteContextTx(tx *gorm.DB, policy *AgentPolicy, scope RedemptionScope, rows []Redemption, request AgentLegacyRedemptionDeleteRequest) (AgentTransactionContext, error) {
	snapshots := make([]agentRedemptionAuthorizationSnapshot, 0, len(rows))
	total := 0
	for i := range rows {
		code := &rows[i]
		snapshots = append(snapshots, agentRedemptionSnapshot(code))
		if code.BudgetReserved {
			if code.Quota > common.MaxWalletQuota-total {
				return AgentTransactionContext{}, ErrAgentQuotaInvalid
			}
			total += code.Quota
		}
	}
	return agentBusinessContextTx(tx, policy, scope.UserID, legacyRedemptionAction(scope, "delete"), request.RequestID, "", "0", total, "redemption_selection", request.RequestID, 0, struct {
		Request AgentLegacyRedemptionDeleteRequest     `json:"request"`
		Codes   []agentRedemptionAuthorizationSnapshot `json:"codes"`
	}{request, snapshots})
}

func PrepareAgentLegacyRedemptionOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	scope, err := ResolveRedemptionScope(identity.UserID)
	if err != nil {
		return nil, err
	}
	scope.Identity = &identity
	var context AgentTransactionContext
	err = DB.Transaction(func(tx *gorm.DB) error {
		policy, err := ReadCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		switch action {
		case "redemption.allocate":
			if !scope.IsAdmin() {
				return ErrAgentRedemptionPermission
			}
			var request AgentQuotaAllocationRequest
			if err := common.DecodeJsonStrict(bytes.NewReader(payload), &request); err != nil {
				return err
			}
			request.RequestID = requestID
			request.Reason = strings.TrimSpace(request.Reason)
			account := AgentRedemptionAccount{AgentUserID: request.AgentUserID}
			err := tx.Where("agent_user_id = ?", request.AgentUserID).First(&account).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
			context, err = buildAgentAllocationContextTx(tx, policy, &account, request)
			if err != nil {
				return err
			}
		case "redemption.create", "redemption.admin.create":
			if action != legacyRedemptionAction(scope, "create") {
				return ErrAgentRedemptionPermission
			}
			var request RedemptionCreateRequest
			if err := common.DecodeJsonStrict(bytes.NewReader(payload), &request); err != nil {
				return err
			}
			request.RequestID = requestID
			context, err = buildAgentLegacyCreateContextTx(tx, policy, scope, request)
			if err != nil {
				return err
			}
		case "redemption.update", "redemption.admin.update":
			if action != legacyRedemptionAction(scope, "update") {
				return ErrAgentRedemptionPermission
			}
			var request AgentLegacyRedemptionUpdateRequest
			if err := common.DecodeJsonStrict(bytes.NewReader(payload), &request); err != nil {
				return err
			}
			request.RequestID = requestID
			var code Redemption
			if err := scope.query(tx).First(&code, request.ID).Error; err != nil {
				return err
			}
			context, err = buildAgentLegacyUpdateContextTx(tx, policy, scope, &code, request.RedemptionUpdateRequest, request.StatusOnly)
			if err != nil {
				return err
			}
		case "redemption.delete", "redemption.admin.delete":
			if action != legacyRedemptionAction(scope, "delete") {
				return ErrAgentRedemptionPermission
			}
			var request AgentLegacyRedemptionDeleteRequest
			if err := common.DecodeJsonStrict(bytes.NewReader(payload), &request); err != nil {
				return err
			}
			request.RequestID = requestID
			query := scope.query(tx)
			if scope.IsAdmin() && request.AgentUserID > 0 {
				query = query.Where("budget_agent_id = ?", request.AgentUserID)
			}
			if request.InvalidOnly {
				query = query.Where("customer_user_id = 0 AND delivery_id = 0 AND source_protected = false").Where("(status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?))", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, common.GetTimestamp()).Limit(MaxRedemptionDeleteBatchSize)
			} else {
				if len(request.IDs) == 0 || len(request.IDs) > MaxRedemptionDeleteBatchSize {
					return ErrRedemptionInvalidRequest
				}
				query = query.Where("id IN ?", request.IDs)
			}
			var rows []Redemption
			if err := query.Order("id ASC").Find(&rows).Error; err != nil {
				return err
			}
			context, err = buildAgentLegacyDeleteContextTx(tx, policy, scope, rows, request)
			if err != nil {
				return err
			}
		default:
			return ErrRedemptionInvalidRequest
		}
		capability := AgentCapabilitySales
		if scope.IsAdmin() {
			capability = AgentCapabilityAdmin
		}
		_, err = RequireAgentCapabilityTx(tx, identity, context.AgentUserID, context.ProfileVersion, capability)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &AgentPreparedOperation{VerificationContext: context, Summary: AgentOperationSummary{Action: action, AgentUserID: context.AgentUserID, AmountMinor: "0", FeeMinor: "0", NetMinor: "0", Quota: context.Quota, TargetLabel: context.TargetType + " #" + context.TargetID, PolicyVersion: context.PolicyVersion}, StatusURL: "/api/agent-platform/operations/" + requestID}, nil
}
