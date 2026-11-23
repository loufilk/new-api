package controller

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const partnerApplicationBodyLimit = 128 * 1024

func partnerApplicationError(c *gin.Context, err error) {
	status, message := http.StatusServiceUnavailable, "Partner applications are temporarily unavailable. Try again."
	switch {
	case errors.Is(err, common.ErrIM21RolloutLocked):
		im21RolloutLocked(c)
		return
	case errors.Is(err, model.ErrPartnerApplicationInvalid):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, model.ErrPartnerApplicationTooLarge):
		status, message = http.StatusRequestEntityTooLarge, err.Error()
	case errors.Is(err, model.ErrPartnerApplicationConflict):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, model.ErrPartnerApplicationForbidden):
		status, message = http.StatusForbidden, err.Error()
	case errors.Is(err, model.ErrPartnerApplicationsClosed):
		status, message = http.StatusForbidden, err.Error()
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, message = http.StatusNotFound, "Partner application not found"
	}
	common.ApiErrorStatus(c, status, common.NewMessage(message), gin.H{"code": "partner_application_failed"})
}

func partnerApplicationBody(c *gin.Context, target any) bool {
	mediaType, _, mediaErr := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		partnerApplicationError(c, model.ErrPartnerApplicationInvalid)
		return false
	}
	if c.Request.ContentLength > partnerApplicationBodyLimit {
		partnerApplicationError(c, model.ErrPartnerApplicationTooLarge)
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, partnerApplicationBodyLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			partnerApplicationError(c, model.ErrPartnerApplicationTooLarge)
		} else {
			partnerApplicationError(c, model.ErrPartnerApplicationInvalid)
		}
		return false
	}
	if common.DecodeJsonStrict(bytes.NewReader(body), target) != nil {
		partnerApplicationError(c, model.ErrPartnerApplicationInvalid)
		return false
	}
	return true
}

func partnerApplicationID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		partnerApplicationError(c, model.ErrPartnerApplicationInvalid)
		return 0, false
	}
	return id, true
}

func checkPartnerPermission(db *gorm.DB, actorID, role int, permission authz.Permission) error {
	allowed, err := authz.CanInTx(db, actorID, role, permission)
	if err != nil {
		return err
	}
	if !allowed {
		return model.ErrPartnerApplicationForbidden
	}
	return nil
}

// RequirePartnerPermission is used after AdminAuth on read endpoints.
// Mutations also revalidate the actor's permissions under the actor-row lock.
func RequirePartnerPermission(permission authz.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := checkPartnerPermission(model.DB.WithContext(c.Request.Context()), c.GetInt("id"), c.GetInt("role"), permission); err != nil {
			partnerApplicationError(c, err)
			c.Abort()
			return
		}
		c.Next()
	}
}

func partnerPermission(permission authz.Permission) model.PartnerAdminPermissionCheck {
	return func(tx *gorm.DB, actor *model.User) error {
		return checkPartnerPermission(tx, actor.Id, actor.Role, permission)
	}
}

func GetPartnerApplicationSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	settings, err := model.GetPartnerApplicationSettings(c.Request.Context())
	if err != nil {
		partnerApplicationError(c, err)
		return
	}
	common.ApiSuccess(c, settings)
}

func GetAdminPartnerApplicationSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	settings, err := model.GetPartnerApplicationSettings(c.Request.Context())
	if err != nil {
		partnerApplicationError(c, err)
		return
	}
	common.ApiSuccess(c, settings)
}

func UpdatePartnerApplicationSettings(c *gin.Context) {
	var input struct {
		Revision     *int64  `json:"revision"`
		Enabled      *bool   `json:"partner_enabled"`
		Instructions *string `json:"partner_instructions"`
	}
	if !partnerApplicationBody(c, &input) {
		return
	}
	if input.Revision == nil || input.Enabled == nil || input.Instructions == nil {
		partnerApplicationError(c, model.ErrPartnerApplicationInvalid)
		return
	}
	next := model.PartnerApplicationSettings{
		Revision:     *input.Revision,
		Enabled:      *input.Enabled,
		Instructions: *input.Instructions,
	}
	settings, err := model.SavePartnerApplicationSettings(c.Request.Context(), c.GetInt("id"), next, partnerPermission(authz.PartnerSettingsUpdate))
	if err != nil {
		partnerApplicationError(c, err)
		return
	}
	recordManageAudit(c, "partner.settings.update", map[string]any{
		"revision": settings.Revision,
		"enabled":  settings.Enabled,
	})
	common.ApiSuccess(c, settings)
}

func CreatePartnerApplication(c *gin.Context) {
	var input model.PartnerApplicationInput
	if !partnerApplicationBody(c, &input) {
		return
	}
	application, err := model.CreatePartnerApplication(c.Request.Context(), c.GetInt("id"), c.GetHeader("Idempotency-Key"), input)
	if err != nil {
		partnerApplicationError(c, err)
		return
	}
	common.ApiSuccess(c, application)
}

func ListPartnerApplications(c *gin.Context)      { listPartnerApplications(c, false) }
func ListAdminPartnerApplications(c *gin.Context) { listPartnerApplications(c, true) }

func listPartnerApplications(c *gin.Context, admin bool) {
	c.Header("Cache-Control", "no-store")
	page := common.GetPageQuery(c)
	items, total, err := model.ListPartnerApplications(c.Request.Context(), c.GetInt("id"), admin, c.Query("status"), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		partnerApplicationError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": total})
}

func GetPartnerApplication(c *gin.Context)      { getPartnerApplication(c, false) }
func GetAdminPartnerApplication(c *gin.Context) { getPartnerApplication(c, true) }

func getPartnerApplication(c *gin.Context, admin bool) {
	c.Header("Cache-Control", "no-store")
	id, ok := partnerApplicationID(c)
	if !ok {
		return
	}
	application, err := model.GetPartnerApplication(c.Request.Context(), c.GetInt("id"), id, admin)
	if err != nil {
		partnerApplicationError(c, err)
		return
	}
	common.ApiSuccess(c, application)
}

func CancelPartnerApplication(c *gin.Context) { reviewPartnerApplication(c, false) }
func ReviewPartnerApplication(c *gin.Context) { reviewPartnerApplication(c, true) }

func reviewPartnerApplication(c *gin.Context, admin bool) {
	id, ok := partnerApplicationID(c)
	if !ok {
		return
	}
	var input model.PartnerApplicationReview
	if !partnerApplicationBody(c, &input) {
		return
	}
	if !admin && input.Action != "cancel" {
		partnerApplicationError(c, model.ErrPartnerApplicationForbidden)
		return
	}
	var checks []model.PartnerAdminPermissionCheck
	if admin {
		// The reviewer receives private application detail in the result.
		checks = append(checks, partnerPermission(authz.PartnerApplicationRead), partnerPermission(authz.PartnerApplicationReview))
	}
	application, err := model.ReviewPartnerApplication(c.Request.Context(), c.GetInt("id"), id, admin, input, checks...)
	if err != nil {
		partnerApplicationError(c, err)
		return
	}
	if admin {
		// Review transitions have their own transactional per-application audit.
		markAuditLogged(c)
	}
	common.ApiSuccess(c, application)
}
