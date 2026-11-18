package controller

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func commerceError(c *gin.Context, err error) {
	status, message := http.StatusServiceUnavailable, "Applications are temporarily unavailable. Try again."
	switch {
	case errors.Is(err, model.ErrCommerceInvalid):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, model.ErrCommerceConflict), errors.Is(err, model.ErrInvoiceIneligible):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, model.ErrCommerceForbidden):
		status, message = http.StatusForbidden, err.Error()
	case errors.Is(err, model.ErrCommerceDisabled):
		status, message = http.StatusForbidden, err.Error()
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, message = http.StatusNotFound, "Application not found"
	}
	common.ApiErrorStatus(c, status, common.NewMessage(message), gin.H{"code": "commerce_request_failed"})
}

func commerceActor(c *gin.Context, admin bool, permission authz.Permission) bool {
	c.Header("Cache-Control", "no-store")
	actor, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil || actor.Status != common.UserStatusEnabled || !common.IsValidateRole(actor.Role) || admin && (actor.Role < common.RoleAdminUser || !authz.Can(actor.Id, actor.Role, permission)) {
		commerceError(c, model.ErrCommerceForbidden)
		return false
	}
	return true
}

func commercePermission(permission authz.Permission) model.AdminUserPermissionCheck {
	return func(tx *gorm.DB, actor *model.User) error {
		allowed, err := authz.CanInTx(tx, actor.Id, actor.Role, permission)
		if err != nil {
			return err
		}
		if !allowed {
			return model.ErrCommerceForbidden
		}
		return nil
	}
}

func commerceBody(c *gin.Context, target any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024))
	if err != nil || common.Unmarshal(body, target) != nil {
		commerceError(c, model.ErrCommerceInvalid)
		return false
	}
	return true
}

func commerceID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		commerceError(c, model.ErrCommerceInvalid)
		return 0, false
	}
	return id, true
}

func GetCommerceConfig(c *gin.Context) {
	if !commerceActor(c, false, authz.Permission{}) {
		return
	}
	result, err := model.GetCommerceConfig(c.Request.Context())
	if err != nil {
		commerceError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func UpdateCommerceConfig(c *gin.Context) {
	if !commerceActor(c, true, authz.CommerceSettingsUpdate) {
		return
	}
	var input model.CommerceConfig
	if !commerceBody(c, &input) {
		return
	}
	result, err := model.SaveCommerceConfig(c.Request.Context(), c.GetInt("id"), input, commercePermission(authz.CommerceSettingsUpdate))
	if err != nil {
		commerceError(c, err)
		return
	}
	markAuditLogged(c)
	common.ApiSuccess(c, result)
}

func GetEligibleInvoiceOrders(c *gin.Context) {
	if !commerceActor(c, false, authz.Permission{}) {
		return
	}
	items, config, err := model.EligibleInvoiceOrders(c.Request.Context(), c.GetInt("id"))
	if err != nil {
		commerceError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "config": config})
}

func CreateInvoiceApplication(c *gin.Context) {
	if !commerceActor(c, false, authz.Permission{}) {
		return
	}
	var input model.InvoiceCreate
	if !commerceBody(c, &input) {
		return
	}
	result, err := model.CreateInvoiceApplication(c.Request.Context(), c.GetInt("id"), c.GetHeader("Idempotency-Key"), input)
	if err != nil {
		commerceError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func ListInvoiceApplications(c *gin.Context)      { listInvoices(c, false) }
func ListAdminInvoiceApplications(c *gin.Context) { listInvoices(c, true) }

func listInvoices(c *gin.Context, admin bool) {
	if !commerceActor(c, admin, authz.CommerceInvoiceRead) {
		return
	}
	page := common.GetPageQuery(c)
	items, total, err := model.ListInvoiceApplications(c.Request.Context(), c.GetInt("id"), admin, c.Query("status"), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		commerceError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": total})
}

func GetInvoiceApplication(c *gin.Context)      { getInvoice(c, false) }
func GetAdminInvoiceApplication(c *gin.Context) { getInvoice(c, true) }

func getInvoice(c *gin.Context, admin bool) {
	if !commerceActor(c, admin, authz.CommerceInvoiceRead) {
		return
	}
	id, ok := commerceID(c)
	if !ok {
		return
	}
	result, err := model.GetInvoiceApplication(c.Request.Context(), c.GetInt("id"), id, admin)
	if err != nil {
		commerceError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func CancelInvoiceApplication(c *gin.Context) { reviewInvoice(c, false) }
func ReviewInvoiceApplication(c *gin.Context) { reviewInvoice(c, true) }

func reviewInvoice(c *gin.Context, admin bool) {
	if !commerceActor(c, admin, authz.CommerceInvoiceReview) {
		return
	}
	id, ok := commerceID(c)
	if !ok {
		return
	}
	var input model.CommerceReview
	if !commerceBody(c, &input) {
		return
	}
	if !admin && input.Action != "cancel" {
		commerceError(c, model.ErrCommerceForbidden)
		return
	}
	var checks []model.AdminUserPermissionCheck
	if admin {
		checks = append(checks, commercePermission(authz.CommerceInvoiceReview))
	}
	result, err := model.ReviewInvoiceApplication(c.Request.Context(), c.GetInt("id"), id, admin, input, checks...)
	if err != nil {
		commerceError(c, err)
		return
	}
	markAuditLogged(c)
	common.ApiSuccess(c, result)
}

func CreatePartnerApplication(c *gin.Context) {
	if !commerceActor(c, false, authz.Permission{}) {
		return
	}
	var input model.PartnerCreate
	if !commerceBody(c, &input) {
		return
	}
	result, err := model.CreatePartnerApplication(c.Request.Context(), c.GetInt("id"), c.GetHeader("Idempotency-Key"), input)
	if err != nil {
		commerceError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func ListPartnerApplications(c *gin.Context)      { listPartners(c, false) }
func ListAdminPartnerApplications(c *gin.Context) { listPartners(c, true) }

func listPartners(c *gin.Context, admin bool) {
	if !commerceActor(c, admin, authz.CommercePartnerRead) {
		return
	}
	page := common.GetPageQuery(c)
	items, total, err := model.ListPartnerApplications(c.Request.Context(), c.GetInt("id"), admin, c.Query("status"), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		commerceError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": total})
}

func GetPartnerApplication(c *gin.Context)      { getPartner(c, false) }
func GetAdminPartnerApplication(c *gin.Context) { getPartner(c, true) }

func getPartner(c *gin.Context, admin bool) {
	if !commerceActor(c, admin, authz.CommercePartnerRead) {
		return
	}
	id, ok := commerceID(c)
	if !ok {
		return
	}
	result, err := model.GetPartnerApplication(c.Request.Context(), c.GetInt("id"), id, admin)
	if err != nil {
		commerceError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func CancelPartnerApplication(c *gin.Context) { reviewPartner(c, false) }
func ReviewPartnerApplication(c *gin.Context) { reviewPartner(c, true) }

func reviewPartner(c *gin.Context, admin bool) {
	if !commerceActor(c, admin, authz.CommercePartnerReview) {
		return
	}
	id, ok := commerceID(c)
	if !ok {
		return
	}
	var input model.CommerceReview
	if !commerceBody(c, &input) {
		return
	}
	if !admin && input.Action != "cancel" {
		commerceError(c, model.ErrCommerceForbidden)
		return
	}
	var checks []model.AdminUserPermissionCheck
	if admin {
		checks = append(checks, commercePermission(authz.CommercePartnerReview))
	}
	result, err := model.ReviewPartnerApplication(c.Request.Context(), c.GetInt("id"), id, admin, input, checks...)
	if err != nil {
		commerceError(c, err)
		return
	}
	markAuditLogged(c)
	common.ApiSuccess(c, result)
}
