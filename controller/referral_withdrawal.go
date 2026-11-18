package controller

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func referralWithdrawalError(c *gin.Context, err error) {
	status, message := http.StatusServiceUnavailable, "Withdrawals are temporarily unavailable. Try again."
	switch {
	case errors.Is(err, model.ErrReferralWithdrawalInvalid):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, model.ErrReferralWithdrawalConflict), errors.Is(err, model.ErrReferralWithdrawalIneligible), errors.Is(err, model.ErrReferralWithdrawalReceipt):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, model.ErrReferralWithdrawalDisabled), errors.Is(err, model.ErrReferralWithdrawalForbidden):
		status, message = http.StatusForbidden, err.Error()
	case errors.Is(err, model.ErrUserSessionInactive):
		status, message = http.StatusUnauthorized, "Your session has expired. Please sign in again."
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, message = http.StatusNotFound, "Withdrawal not found"
	}
	common.ApiErrorStatus(c, status, common.NewMessage(message))
}

func referralWithdrawalIdentity(c *gin.Context, permission *authz.Permission) (model.AuthSessionIdentity, bool) {
	c.Header("Cache-Control", "no-store")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	_, pat := model.ParseAccessTokenSessionID(identity.SessionID)
	if !ok || pat || c.GetBool("use_access_token") || identity.UserID != c.GetInt("id") {
		referralWithdrawalError(c, model.ErrUserSessionInactive)
		return identity, false
	}
	actor, err := model.GetUserById(identity.UserID, false)
	if err != nil || actor.Status != common.UserStatusEnabled || !common.IsValidateRole(actor.Role) || permission != nil && (actor.Role < common.RoleAdminUser || !authz.Can(actor.Id, actor.Role, *permission)) {
		referralWithdrawalError(c, model.ErrReferralWithdrawalForbidden)
		return identity, false
	}
	return identity, true
}

func referralWithdrawalBody(c *gin.Context, target any) bool {
	content, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024))
	if err != nil || common.Unmarshal(content, target) != nil {
		referralWithdrawalError(c, model.ErrReferralWithdrawalInvalid)
		return false
	}
	return true
}

func referralWithdrawalPermission(permission *authz.Permission) model.ReferralWithdrawalPermissionCheck {
	return func(tx *gorm.DB, actor *model.User) error {
		if permission == nil {
			return model.ErrReferralWithdrawalForbidden
		}
		allowed, err := authz.CanInTx(tx, actor.Id, actor.Role, *permission)
		if err != nil {
			return err
		}
		if !allowed {
			return model.ErrReferralWithdrawalForbidden
		}
		return nil
	}
}

func GetReferralWithdrawalSummary(c *gin.Context) {
	identity, ok := referralWithdrawalIdentity(c, nil)
	if !ok {
		return
	}
	result, err := model.GetReferralWithdrawalSummary(c.Request.Context(), identity)
	if err != nil {
		referralWithdrawalError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func GetReferralWithdrawalConfig(c *gin.Context) {
	identity, ok := referralWithdrawalIdentity(c, &authz.ReferralWithdrawalSettingsRead)
	if !ok {
		return
	}
	result, err := model.ReadReferralWithdrawalConfig(c.Request.Context(), identity, referralWithdrawalPermission(&authz.ReferralWithdrawalSettingsRead))
	if err != nil {
		referralWithdrawalError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func UpdateReferralWithdrawalConfig(c *gin.Context) {
	identity, ok := referralWithdrawalIdentity(c, &authz.ReferralWithdrawalSettingsUpdate)
	if !ok {
		return
	}
	var input model.ReferralWithdrawalConfig
	if !referralWithdrawalBody(c, &input) {
		return
	}
	result, err := model.SaveReferralWithdrawalConfig(c.Request.Context(), identity, input, referralWithdrawalPermission(&authz.ReferralWithdrawalSettingsUpdate))
	if err != nil {
		referralWithdrawalError(c, err)
		return
	}
	markAuditLogged(c)
	common.ApiSuccess(c, result)
}

func QuoteReferralWithdrawal(c *gin.Context) {
	identity, ok := referralWithdrawalIdentity(c, nil)
	if !ok {
		return
	}
	var input model.ReferralWithdrawalInput
	if !referralWithdrawalBody(c, &input) {
		return
	}
	result, err := model.QuoteReferralWithdrawal(c.Request.Context(), identity, input)
	if err != nil {
		referralWithdrawalError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func CreateReferralWithdrawal(c *gin.Context) {
	identity, ok := referralWithdrawalIdentity(c, nil)
	if !ok {
		return
	}
	var input model.ReferralWithdrawalInput
	if !referralWithdrawalBody(c, &input) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	payload, err := common.Marshal(service.ReferralWithdrawalCreateContext{RequestKey: key, Input: input})
	if err != nil {
		referralWithdrawalError(c, err)
		return
	}
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeReferralWithdrawalCreate, Context: payload}) == nil {
		return
	}
	result, err := model.CreateReferralWithdrawal(c.Request.Context(), identity, key, input)
	if err != nil {
		referralWithdrawalError(c, err)
		return
	}
	markAuditLogged(c)
	common.ApiSuccess(c, result)
}

func ListReferralWithdrawals(c *gin.Context)      { listReferralWithdrawals(c, false) }
func ListAdminReferralWithdrawals(c *gin.Context) { listReferralWithdrawals(c, true) }
func listReferralWithdrawals(c *gin.Context, admin bool) {
	var permission *authz.Permission
	if admin {
		permission = &authz.ReferralWithdrawalRead
	}
	identity, ok := referralWithdrawalIdentity(c, permission)
	if !ok {
		return
	}
	page, size := 1, 20
	if value := c.Query("p"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			referralWithdrawalError(c, model.ErrReferralWithdrawalInvalid)
			return
		}
		page = parsed
	}
	if value := c.Query("page_size"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			referralWithdrawalError(c, model.ErrReferralWithdrawalInvalid)
			return
		}
		size = parsed
	}
	items, total, err := model.ListReferralWithdrawals(c.Request.Context(), identity, admin, c.Query("status"), page, size, referralWithdrawalPermission(permission))
	if err != nil {
		referralWithdrawalError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": total})
}

func GetReferralWithdrawal(c *gin.Context)      { getReferralWithdrawal(c, false) }
func GetAdminReferralWithdrawal(c *gin.Context) { getReferralWithdrawal(c, true) }
func getReferralWithdrawal(c *gin.Context, admin bool) {
	var permission *authz.Permission
	if admin {
		permission = &authz.ReferralWithdrawalRead
	}
	identity, ok := referralWithdrawalIdentity(c, permission)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		referralWithdrawalError(c, model.ErrReferralWithdrawalInvalid)
		return
	}
	result, err := model.GetReferralWithdrawal(c.Request.Context(), identity, id, admin, referralWithdrawalPermission(permission))
	if err != nil {
		referralWithdrawalError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func CancelReferralWithdrawal(c *gin.Context) { reviewReferralWithdrawal(c, nil) }
func ReviewReferralWithdrawal(c *gin.Context) {
	reviewReferralWithdrawal(c, &authz.ReferralWithdrawalReview)
}
func PayReferralWithdrawal(c *gin.Context) { reviewReferralWithdrawal(c, &authz.ReferralWithdrawalPay) }
func reviewReferralWithdrawal(c *gin.Context, permission *authz.Permission) {
	identity, ok := referralWithdrawalIdentity(c, permission)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		referralWithdrawalError(c, model.ErrReferralWithdrawalInvalid)
		return
	}
	var input model.ReferralWithdrawalAction
	if !referralWithdrawalBody(c, &input) {
		return
	}
	if permission == nil && input.Action != "cancel" || permission == &authz.ReferralWithdrawalReview && input.Action != "approve" && input.Action != "reject" || permission == &authz.ReferralWithdrawalPay && input.Action != "begin_payment" && input.Action != "paid" {
		referralWithdrawalError(c, model.ErrReferralWithdrawalForbidden)
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if permission != nil {
		payload, err := common.Marshal(service.ReferralWithdrawalReviewContext{WithdrawalID: id, RequestKey: key, Input: input})
		if err != nil {
			referralWithdrawalError(c, err)
			return
		}
		if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: service.VerificationScopeReferralWithdrawalReview, Context: payload}) == nil {
			return
		}
	}
	result, err := model.ReviewReferralWithdrawal(c.Request.Context(), identity, id, key, input, permission != nil, referralWithdrawalPermission(permission))
	if err != nil {
		referralWithdrawalError(c, err)
		return
	}
	markAuditLogged(c)
	common.ApiSuccess(c, result)
}
