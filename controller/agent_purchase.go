package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func agentPurchaseIdentity(c *gin.Context) (model.AuthSessionIdentity, bool) {
	c.Header("Cache-Control", "no-store")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || c.GetBool("use_access_token") {
		writeAgentPurchaseError(c, model.ErrAgentRedemptionPermission)
		return model.AuthSessionIdentity{}, false
	}
	if err := model.ValidateAgentPurchaseReadIdentity(identity); err != nil {
		writeAgentPurchaseError(c, err)
		return model.AuthSessionIdentity{}, false
	}
	return identity, true
}

func agentPurchaseAuthorization(c *gin.Context, action, requestID string, command any) (model.AgentTransactionAuthorization, bool) {
	identity, ok := agentPurchaseIdentity(c)
	if !ok {
		return model.AgentTransactionAuthorization{}, false
	}
	payload, err := common.Marshal(command)
	if err != nil {
		writeAgentPurchaseError(c, model.ErrAgentPurchaseInvalid)
		return model.AgentTransactionAuthorization{}, false
	}
	replay, found, err := model.ReplayAgentPurchaseOperation(identity, action, requestID, payload)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return model.AgentTransactionAuthorization{}, false
	}
	if found {
		common.ApiSuccess(c, replay)
		return model.AgentTransactionAuthorization{}, false
	}
	prepared, err := model.PrepareAgentPurchaseOperation(identity, action, requestID, payload)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return model.AgentTransactionAuthorization{}, false
	}
	if prepared == nil {
		writeAgentPurchaseError(c, model.ErrAgentPurchaseInvalid)
		return model.AgentTransactionAuthorization{}, false
	}
	auth, err := service.PrepareAgentTransactionAuthorization(strings.TrimSpace(c.GetHeader("X-Security-Proof")), identity, prepared.VerificationContext)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return model.AgentTransactionAuthorization{}, false
	}
	return auth, true
}

func writeAgentPurchaseError(c *gin.Context, err error) {
	status, code, message := http.StatusBadRequest, "AGENT_REQUEST_INVALID", "Invalid agent purchase request"
	switch {
	case errors.Is(err, model.ErrAgentRedemptionPermission), errors.Is(err, model.ErrAgentPurchaseReview):
		status, code, message = http.StatusForbidden, "AGENT_PERMISSION_DENIED", err.Error()
	case errors.Is(err, model.ErrAgentFeatureDisabled):
		status, code, message = http.StatusForbidden, "AGENT_FEATURE_DISABLED", err.Error()
	case errors.Is(err, model.ErrAgentPolicyChanged), errors.Is(err, model.ErrAgentQuotaConflict), errors.Is(err, model.ErrAgentPurchaseState), errors.Is(err, model.ErrAgentRefundState), errors.Is(err, model.ErrAgentDeliveryState):
		status, code, message = http.StatusConflict, "AGENT_STATE_CHANGED", err.Error()
	case errors.Is(err, model.ErrAgentPurchaseInvalid), errors.Is(err, model.ErrAgentPurchaseExpired), errors.Is(err, model.ErrAgentRefundInvalid), errors.Is(err, model.ErrAgentRefundWaiting), errors.Is(err, model.ErrAgentRefundUnknown), errors.Is(err, model.ErrAgentDeliveryProtected), errors.Is(err, model.ErrAgentDeliveryConfirmationRequired), errors.Is(err, model.ErrAgentDeliveryResponsibility), errors.Is(err, model.ErrAgentQuotaInvalid), errors.Is(err, model.ErrAgentQuotaInsufficient), errors.Is(err, model.ErrWalletQuotaLimitExceeded), errors.Is(err, model.ErrAgentPaymentReceiptConflict), errors.Is(err, model.ErrAgentOutboundPaymentConflict), errors.Is(err, model.ErrRedemptionInvalidRequest):
		message = err.Error()
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, code, message = http.StatusNotFound, "AGENT_RESOURCE_NOT_FOUND", "Agent resource not found"
	case errors.Is(err, model.ErrAgentFundInvariant), errors.Is(err, model.ErrAgentQuotaInvariant):
		status, code, message = http.StatusConflict, "AGENT_RECONCILIATION_REQUIRED", "Agent funds require reconciliation; no balance was changed"
	default:
		writeAgentProfileError(c, err)
		return
	}
	common.ApiErrorStatus(c, status, common.NewMessage(message), gin.H{"code": code})
}

func CreateAgentPurchaseQuote(c *gin.Context) {
	identity, ok := agentPurchaseIdentity(c)
	if !ok {
		return
	}
	var command model.AgentPurchaseQuoteRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &command); err != nil {
		writeAgentPurchaseError(c, model.ErrAgentPurchaseInvalid)
		return
	}
	result, err := model.CreateAgentPurchaseQuote(identity, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func CreateAgentPurchaseOrder(c *gin.Context) {
	identity, ok := agentPurchaseIdentity(c)
	if !ok {
		return
	}
	var command model.AgentPurchaseCreateRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &command); err != nil {
		writeAgentPurchaseError(c, model.ErrAgentPurchaseInvalid)
		return
	}
	result, err := model.CreateAgentPurchaseOrder(identity, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func agentPurchaseCommand(c *gin.Context) (model.AgentPurchaseCommand, bool) {
	var command model.AgentPurchaseCommand
	if err := common.DecodeJsonStrict(c.Request.Body, &command); err != nil {
		writeAgentPurchaseError(c, model.ErrAgentPurchaseInvalid)
		return command, false
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 || (command.OrderID != 0 && command.OrderID != id) {
		writeAgentPurchaseError(c, model.ErrAgentPurchaseInvalid)
		return command, false
	}
	command.OrderID = id
	return command, true
}
func SubmitAgentPurchaseEvidence(c *gin.Context) {
	identity, ok := agentPurchaseIdentity(c)
	if !ok {
		return
	}
	command, ok := agentPurchaseCommand(c)
	if !ok {
		return
	}
	result, err := model.SubmitAgentPurchaseEvidence(identity, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func RecordAgentPurchasePayment(c *gin.Context) {
	command, ok := agentPurchaseCommand(c)
	if !ok {
		return
	}
	auth, ok := agentPurchaseAuthorization(c, "purchase.payment.record", command.RequestID, command)
	if !ok {
		return
	}
	result, err := model.RecordAgentPurchasePayment(auth, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func ConfirmAgentPurchasePayment(c *gin.Context) {
	command, ok := agentPurchaseCommand(c)
	if !ok {
		return
	}
	auth, ok := agentPurchaseAuthorization(c, "purchase.payment.confirm", command.RequestID, command)
	if !ok {
		return
	}
	result, err := model.ConfirmAgentPurchasePayment(auth, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func GetAgentPurchaseOrders(c *gin.Context) {
	if _, ok := agentPurchaseIdentity(c); !ok {
		return
	}
	page := redemptionPage(c)
	rows, total, err := model.GetAgentPurchaseOrders(redemptionScope(c), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
func GetAgentPurchaseRefunds(c *gin.Context) {
	if _, ok := agentPurchaseIdentity(c); !ok {
		return
	}
	page := redemptionPage(c)
	rows, total, err := model.GetAgentPurchaseRefunds(redemptionScope(c), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
func RequestAgentPurchaseRefund(c *gin.Context) {
	var command model.AgentPurchaseRefundCommand
	if err := common.DecodeJsonStrict(c.Request.Body, &command); err != nil {
		writeAgentPurchaseError(c, model.ErrAgentRefundInvalid)
		return
	}
	auth, ok := agentPurchaseAuthorization(c, "purchase.refund.request", command.RequestID, command)
	if !ok {
		return
	}
	result, err := model.RequestAgentPurchaseRefund(auth, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func transitionAgentPurchaseRefund(c *gin.Context, action string) {
	var command model.AgentPurchaseRefundCommand
	if err := common.DecodeJsonStrict(c.Request.Body, &command); err != nil {
		writeAgentPurchaseError(c, model.ErrAgentRefundInvalid)
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 || (command.RefundID != 0 && command.RefundID != id) {
		writeAgentPurchaseError(c, model.ErrAgentRefundInvalid)
		return
	}
	command.RefundID = id
	if action == "purchase.refund.approve" && command.Decision == "reject" {
		action = "purchase.refund.reject"
	}
	auth, ok := agentPurchaseAuthorization(c, action, command.RequestID, command)
	if !ok {
		return
	}
	result, err := model.TransitionAgentPurchaseRefund(auth, action, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func ApproveAgentPurchaseRefund(c *gin.Context) {
	transitionAgentPurchaseRefund(c, "purchase.refund.approve")
}
func SubmitAgentPurchaseRefund(c *gin.Context) {
	transitionAgentPurchaseRefund(c, "purchase.refund.submit")
}
func ResolveAgentPurchaseRefund(c *gin.Context) {
	transitionAgentPurchaseRefund(c, "purchase.refund.resolve")
}
func AgentProcurementSelfUse(c *gin.Context) {
	var command model.AgentProcurementSelfUseRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &command); err != nil {
		writeAgentPurchaseError(c, model.ErrAgentPurchaseInvalid)
		return
	}
	auth, ok := agentPurchaseAuthorization(c, "procurement.self_use", command.RequestID, command)
	if !ok {
		return
	}
	result, err := model.AgentProcurementSelfUse(auth, command)
	if err != nil {
		writeAgentPurchaseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
