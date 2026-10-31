package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func commissionIdentity(c *gin.Context) (model.AuthSessionIdentity, bool) {
	c.Header("Cache-Control", "no-store")
	identity, ok := middleware.GetStepUpIdentity(c)
	if !ok || identity.UserID != c.GetInt("id") {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Financial operations require a current browser session"))
		return model.AuthSessionIdentity{}, false
	}
	if _, pat := model.ParseAccessTokenSessionID(identity.SessionID); pat {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Financial operations require a current browser session"))
		return model.AuthSessionIdentity{}, false
	}
	return identity, true
}

func commissionAPIError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAgentProfilePermission), errors.Is(err, model.ErrAgentTransactionProof), errors.Is(err, model.ErrUserSessionInactive):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Financial authorization is invalid or no longer matches this transaction"))
	case errors.Is(err, model.ErrAgentCommissionConflict), errors.Is(err, model.ErrAgentPolicyChanged), errors.Is(err, model.ErrAgentProfileConflict), errors.Is(err, model.ErrAgentPaymentReceiptConflict), errors.Is(err, model.ErrAgentOutboundPaymentConflict):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Financial operation changed; reload and prepare it again"))
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Financial record not found"))
	case errors.Is(err, model.ErrAgentCommissionInvalid), errors.Is(err, model.ErrAgentCommissionInsufficient), errors.Is(err, model.ErrAgentCommissionDebt), errors.Is(err, model.ErrAgentCommissionUnavailable), errors.Is(err, model.ErrAgentFeatureDisabled):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(err.Error()))
	default:
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Financial operation failed; check its status before retrying"))
	}
}

func commissionBind(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	if err := common.DecodeJsonStrict(c.Request.Body, target); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid commission request"))
		return false
	}
	return true
}

func commissionAuthorization(c *gin.Context, identity model.AuthSessionIdentity, action, requestID string, payload any) (model.AgentTransactionAuthorization, bool) {
	if result, replayed, err := model.ReplayAgentCommissionOperation(identity, action, requestID, payload); err != nil {
		commissionAPIError(c, err)
		return model.AgentTransactionAuthorization{}, false
	} else if replayed {
		common.ApiSuccess(c, result)
		return model.AgentTransactionAuthorization{}, false
	}
	encoded, err := common.Marshal(payload)
	if err != nil {
		commissionAPIError(c, err)
		return model.AgentTransactionAuthorization{}, false
	}
	prepared, err := model.PrepareAgentCommissionOperation(identity, action, requestID, encoded)
	if err != nil {
		commissionAPIError(c, err)
		return model.AgentTransactionAuthorization{}, false
	}
	authorization, err := service.PrepareAgentTransactionAuthorization(c.GetHeader("X-Security-Proof"), identity, prepared.VerificationContext)
	if err != nil {
		commissionAPIError(c, model.ErrAgentTransactionProof)
		return model.AgentTransactionAuthorization{}, false
	}
	return authorization, true
}

func GetAgentCommissionSummary(c *gin.Context) {
	identity, ok := commissionIdentity(c)
	if !ok {
		return
	}
	owner, _ := strconv.Atoi(c.Query("user_id"))
	result, err := model.GetAgentCommissionSummary(identity, owner)
	if err != nil {
		commissionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func AgentCommissionListHandler(kind string, admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := commissionIdentity(c)
		if !ok {
			return
		}
		page := redemptionPage(c)
		owner, _ := strconv.Atoi(c.Query("user_id"))
		listKind := kind
		if kind == "recipients" && c.Query("current_only") == "true" {
			listKind = "recipients_current"
		}
		rows, total, err := model.ListAgentCommissionRecords(identity, listKind, owner, page.Page, page.PageSize, c.Query("request_id"), admin)
		if err != nil {
			commissionAPIError(c, err)
			return
		}
		page.SetItems(rows)
		page.SetTotal(int(total))
		common.ApiSuccess(c, page)
	}
}

func GetAgentCommissionWithdrawal(c *gin.Context) {
	identity, ok := commissionIdentity(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		commissionAPIError(c, model.ErrAgentCommissionInvalid)
		return
	}
	result, err := model.GetAgentCommissionWithdrawal(identity, id)
	if err != nil {
		commissionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func CreateAgentCommissionWithdrawalQuote(c *gin.Context) {
	createAgentCommissionQuote(c, "withdrawal")
}
func CreateAgentCommissionConversionQuote(c *gin.Context) { createAgentCommissionQuote(c, "") }
func createAgentCommissionQuote(c *gin.Context, target string) {
	identity, ok := commissionIdentity(c)
	if !ok {
		return
	}
	var input model.AgentCommissionQuoteRequest
	if !commissionBind(c, &input) {
		return
	}
	if target != "" {
		if input.Target != "" && input.Target != target {
			commissionAPIError(c, model.ErrAgentCommissionInvalid)
			return
		}
		input.Target = target
	}
	result, err := model.CreateAgentCommissionQuote(identity, input)
	if err != nil {
		commissionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func RequestAgentCommissionWithdrawal(c *gin.Context) {
	identity, ok := commissionIdentity(c)
	if !ok {
		return
	}
	var input model.AgentCommissionExecuteRequest
	if !commissionBind(c, &input) {
		return
	}
	authorization, ok := commissionAuthorization(c, identity, "commission.withdraw.request", input.RequestID, input)
	if !ok {
		return
	}
	result, err := model.RequestAgentCommissionWithdrawal(authorization, input)
	if err != nil {
		commissionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func ExecuteAgentCommissionConversion(c *gin.Context) {
	identity, ok := commissionIdentity(c)
	if !ok {
		return
	}
	var input model.AgentCommissionExecuteRequest
	if !commissionBind(c, &input) {
		return
	}
	// The quote owns the destination. The user cannot turn a wallet quote into
	// procurement or change its locked conversion rate at execution time.
	var quote model.AgentCommissionQuote
	if err := model.DB.Where("id = ? AND user_id = ?", input.QuoteID, identity.UserID).First(&quote).Error; err != nil {
		commissionAPIError(c, err)
		return
	}
	authorization, ok := commissionAuthorization(c, identity, quote.Action, input.RequestID, input)
	if !ok {
		return
	}
	result, err := model.ExecuteAgentCommissionConversion(authorization, input)
	if err != nil {
		commissionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func ChangeAgentCommissionRecipient(c *gin.Context) {
	identity, ok := commissionIdentity(c)
	if !ok {
		return
	}
	var input model.AgentCommissionRecipientRequest
	if !commissionBind(c, &input) {
		return
	}
	if id := c.Param("id"); id != "" {
		if input.RecipientID != "" && input.RecipientID != id {
			commissionAPIError(c, model.ErrAgentCommissionInvalid)
			return
		}
		input.RecipientID = id
	}
	authorization, ok := commissionAuthorization(c, identity, "recipient.change", input.RequestID, input)
	if !ok {
		return
	}
	result, err := model.ChangeAgentCommissionRecipient(authorization, input)
	if err != nil {
		commissionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func AgentCommissionWithdrawalActionHandler(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := commissionIdentity(c)
		if !ok {
			return
		}
		var input model.AgentCommissionWithdrawalAction
		if !commissionBind(c, &input) {
			return
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 || (input.WithdrawalID != 0 && input.WithdrawalID != id) {
			commissionAPIError(c, model.ErrAgentCommissionInvalid)
			return
		}
		input.WithdrawalID = id
		authorization, ok := commissionAuthorization(c, identity, action, input.RequestID, input)
		if !ok {
			return
		}
		result, err := model.ActOnAgentCommissionWithdrawal(authorization, action, input)
		if err != nil {
			commissionAPIError(c, err)
			return
		}
		common.ApiSuccess(c, result)
	}
}

func RecordAgentCommissionRefund(c *gin.Context) {
	identity, ok := commissionIdentity(c)
	if !ok {
		return
	}
	var input model.AgentCommissionRefundRequest
	if !commissionBind(c, &input) {
		return
	}
	authorization, ok := commissionAuthorization(c, identity, "commission.refund.record", input.RequestID, input)
	if !ok {
		return
	}
	result, err := model.RecordAgentCommissionRefund(authorization, input)
	if err != nil {
		commissionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func ReleaseAgentCommissionReserve(c *gin.Context) {
	identity, ok := commissionIdentity(c)
	if !ok {
		return
	}
	var input model.AgentCommissionReserveRequest
	if !commissionBind(c, &input) {
		return
	}
	authorization, ok := commissionAuthorization(c, identity, "commission.reserve.release", input.RequestID, input)
	if !ok {
		return
	}
	result, err := model.ReleaseAgentCommissionReserve(authorization, input)
	if err != nil {
		commissionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func RetryAgentCommissionOutbox(c *gin.Context) {
	identity, ok := commissionIdentity(c)
	if !ok {
		return
	}
	var input model.AgentCommissionRetryRequest
	if !commissionBind(c, &input) {
		return
	}
	authorization, ok := commissionAuthorization(c, identity, "commission.outbox.retry", input.RequestID, input)
	if !ok {
		return
	}
	result, err := model.RetryAgentCommissionOutbox(authorization, input)
	if err != nil {
		commissionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func ReconcileAgentCommissions(c *gin.Context) {
	identity, ok := commissionIdentity(c)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(c.Query("after_id"), 10, 64)
	result, err := model.ReconcileAgentCommissions(identity, after)
	if err != nil {
		commissionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func GetAgentCommissionPayoutInstructions(c *gin.Context) {
	identity, ok := commissionIdentity(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		commissionAPIError(c, model.ErrAgentCommissionInvalid)
		return
	}
	instruction, err := model.GetAgentCommissionPayoutInstruction(identity, id)
	if err != nil {
		commissionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, instruction)
}
