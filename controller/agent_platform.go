package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func agentPlatformIdentity(c *gin.Context) (model.AuthSessionIdentity, bool) {
	c.Header("Cache-Control", "no-store")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || identity.UserID != c.GetInt("id") || c.GetBool("use_access_token") {
		return model.AuthSessionIdentity{}, false
	}
	if _, pat := model.ParseAccessTokenSessionID(identity.SessionID); pat {
		return model.AuthSessionIdentity{}, false
	}
	return identity, true
}

func writeAgentPlatformError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAgentInvitationPolicy), errors.Is(err, model.ErrAgentPolicyInvalid), errors.Is(err, model.ErrAgentQuotaInvalid), errors.Is(err, model.ErrAgentTransactionContext):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(err.Error()))
	case errors.Is(err, model.ErrAgentPolicyChanged), errors.Is(err, model.ErrAgentQuotaConflict), errors.Is(err, model.ErrAgentPaymentReceiptConflict), errors.Is(err, model.ErrAgentQuotaInsufficient):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage(err.Error()))
	case errors.Is(err, model.ErrAgentInvitationPermission), errors.Is(err, model.ErrAgentFeatureDisabled):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage(err.Error()))
	default:
		writeAgentProfileError(c, err)
	}
}

func PrepareAgentPlatformOperation(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentPlatformError(c, model.ErrAgentProfilePermission)
		return
	}
	var request struct {
		Action    string          `json:"action"`
		RequestID string          `json:"request_id"`
		Payload   json.RawMessage `json:"payload"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if common.DecodeJsonStrict(c.Request.Body, &request) != nil || len(request.Payload) == 0 {
		writeAgentPlatformError(c, model.ErrAgentTransactionContext)
		return
	}
	var prepared *model.AgentPreparedOperation
	var err error
	writeError := writeAgentPlatformError
	switch {
	case request.Action == "policy.publish":
		prepared, err = model.PrepareAgentPolicyOperation(identity, request.Action, request.RequestID, request.Payload)
	case request.Action == "profile.update", request.Action == "invitation.policy.update":
		prepared, err = model.PrepareAgentProfileOperation(identity, request.Action, request.RequestID, request.Payload)
	case strings.HasPrefix(request.Action, "commission."), request.Action == "recipient.change":
		writeError = commissionAPIError
		prepared, err = model.PrepareAgentCommissionOperation(identity, request.Action, request.RequestID, request.Payload)
	default:
		writeError = writeAgentPurchaseError
		prepared, err = model.PrepareAgentPurchaseOperation(identity, request.Action, request.RequestID, request.Payload)
	}
	if err != nil {
		writeError(c, err)
		return
	}
	common.ApiSuccess(c, prepared)
}

func GetAgentPlatformOperation(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentPlatformError(c, model.ErrAgentProfilePermission)
		return
	}
	if err := agentPlatformReadSession(identity); err != nil {
		writeAgentPlatformError(c, err)
		return
	}
	requestID := c.Param("request_id")
	canonical := isCanonicalAgentPlatformRequestID(requestID)
	if !canonical && !model.ValidReferralTransferIdempotencyKey(requestID) {
		writeAgentPlatformError(c, model.ErrAgentTransactionContext)
		return
	}
	// Historical aff transfers used a shorter/dotted idempotency-key grammar.
	// They recover from their existing wallet ledger, not a second money ledger.
	if !canonical {
		status, err := model.FindAgentCommissionOperation(identity, requestID)
		if err != nil {
			writeAgentPlatformError(c, err)
			return
		}
		if status != nil {
			common.ApiSuccess(c, status)
		} else {
			common.ApiSuccess(c, model.AgentOperationStatus{RequestID: requestID, Status: "not_observed"})
		}
		return
	}
	var found *model.AgentOperationStatus
	for _, lookup := range []struct {
		find       func(model.AuthSessionIdentity, string) (*model.AgentOperationStatus, error)
		writeError func(*gin.Context, error)
	}{{model.FindAgentPolicyOperation, writeAgentPlatformError}, {model.FindAgentProfileOperation, writeAgentPlatformError}, {model.FindAgentPurchaseOperation, writeAgentPurchaseError}, {model.FindAgentCommissionOperation, commissionAPIError}} {
		status, err := lookup.find(identity, requestID)
		if err != nil {
			lookup.writeError(c, err)
			return
		}
		if status != nil {
			if found != nil {
				writeAgentPlatformError(c, model.ErrAgentQuotaConflict)
				return
			}
			found = status
		}
	}
	if found != nil {
		common.ApiSuccess(c, found)
		return
	}
	common.ApiSuccess(c, model.AgentOperationStatus{RequestID: requestID, Status: "not_observed"})

}

func GetCurrentAgentPlatformPolicy(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	policy, err := model.GetCurrentAgentPolicy()
	if err != nil {
		writeAgentPlatformError(c, err)
		return
	}
	// Public business terms do not disclose internal audit reasons or employee IDs.
	if c.GetInt("role") < common.RoleAdminUser {
		policy.Reason = ""
		policy.RequestID = ""
		policy.CreatedBy = 0
	}
	common.ApiSuccess(c, policy)
}

func GetAgentPlatformPolicies(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentPlatformError(c, model.ErrAgentProfilePermission)
		return
	}
	var actor model.User
	if err := model.DB.Select("id", "role", "status").First(&actor, identity.UserID).Error; err != nil {
		writeAgentPlatformError(c, err)
		return
	}
	if actor.Status != common.UserStatusEnabled || actor.Role < common.RoleAdminUser {
		writeAgentPlatformError(c, model.ErrAgentProfilePermission)
		return
	}
	page := redemptionPage(c)
	var rows []model.AgentPolicy
	var total int64
	if err := model.DB.Model(&model.AgentPolicy{}).Count(&total).Error; err != nil {
		writeAgentPlatformError(c, err)
		return
	}
	if err := model.DB.Order("id DESC").Offset(page.GetStartIdx()).Limit(page.GetPageSize()).Find(&rows).Error; err != nil {
		writeAgentPlatformError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func PublishAgentPlatformPolicy(c *gin.Context) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentPlatformError(c, model.ErrAgentProfilePermission)
		return
	}
	var request model.AgentPolicyPublishRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if common.DecodeJsonStrict(c.Request.Body, &request) != nil {
		writeAgentPlatformError(c, model.ErrAgentPolicyInvalid)
		return
	}
	encoded, err := common.Marshal(request)
	if err != nil {
		writeAgentPlatformError(c, err)
		return
	}
	prepared, err := model.PrepareAgentPolicyOperation(identity, "policy.publish", request.RequestID, encoded)
	if err != nil {
		// Exact response recovery does not require the old pointer to remain current.
		status, lookupErr := model.FindAgentPolicyOperation(identity, request.RequestID)
		if lookupErr == nil && status != nil && errors.Is(err, model.ErrAgentPolicyChanged) {
			// The model still verifies the original request signature below. Build
			// only the static opaque authorization; this is not a proof bypass.
			context := model.AgentTransactionContext{Action: "policy.publish", RequestID: request.RequestID, AgentUserID: identity.UserID, PolicyVersion: request.ExpectedVersion, AmountMinor: "0", TargetType: "agent_policy", TargetID: "current", TargetVersion: request.ExpectedVersion}
			context.ParametersHash, _ = model.AgentTransactionParametersHash(request)
			prepared = &model.AgentPreparedOperation{VerificationContext: context}
		} else {
			writeAgentPlatformError(c, err)
			return
		}
	}
	authorization, err := service.PrepareAgentTransactionAuthorization(strings.TrimSpace(c.GetHeader("X-Security-Proof")), identity, prepared.VerificationContext)
	if err != nil {
		writeAgentPlatformError(c, err)
		return
	}
	policy, replayed, err := model.PublishAgentPolicy(identity, request, authorization)
	if err != nil {
		writeAgentPlatformError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"policy": policy, "replayed": replayed})
}

// agentPlatformReadSession is used by sensitive read endpoints that need a
// fresh durable session check, not only a cached role from an earlier request.
func agentPlatformReadSession(identity model.AuthSessionIdentity) error {
	return model.DB.Transaction(func(tx *gorm.DB) error { return model.ValidateAuthSessionWithTx(tx, identity) })
}

func isCanonicalAgentPlatformRequestID(requestID string) bool {
	if len(requestID) < 8 || len(requestID) > 96 {
		return false
	}
	for _, ch := range requestID {
		if !(ch >= 'a' && ch <= 'z') && !(ch >= 'A' && ch <= 'Z') && !(ch >= '0' && ch <= '9') && ch != '-' && ch != '_' && ch != ':' {
			return false
		}
	}
	return true
}
