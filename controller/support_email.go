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
)

// Require a real dashboard session, even if a future PAT policy admits a route.
func SupportEmailSessionOnly(c *gin.Context) {
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok || c.GetBool("use_access_token") {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "A dashboard login session is required"})
		return
	}
	c.Next()
}

func supportEmailError(c *gin.Context, err error) {
	if errors.Is(err, model.ErrSupportEmailInput) {
		common.ApiErrorT(c, "Invalid support email settings")
		return
	}
	if errors.Is(err, model.ErrSupportEmailState) {
		common.ApiErrorT(c, "Support email notification is no longer eligible")
		return
	}
	common.ApiErrorT(c, "Database error, please contact the administrator")
}

func GetSupportEmailSettings(c *gin.Context) {
	settings, err := model.ReadSupportEmailSettings(c.Request.Context())
	if err != nil {
		supportEmailError(c, err)
		return
	}
	_, configured, err := service.SupportEmailSMTPConfig(c.Request.Context())
	if err != nil {
		supportEmailError(c, err)
		return
	}
	recipients := settings.AdminRecipients()
	if recipients == nil {
		recipients = []string{}
	}
	common.ApiSuccess(c, gin.H{"enabled": settings.Enabled, "admin_recipients": recipients, "site_origin": settings.SiteOrigin, "smtp_configured": configured})
}

func UpdateSupportEmailSettings(c *gin.Context) {
	var request struct {
		Enabled         *bool    `json:"enabled"`
		AdminRecipients []string `json:"admin_recipients"`
		SiteOrigin      string   `json:"site_origin"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	if c.ShouldBindJSON(&request) != nil || request.Enabled == nil {
		supportEmailError(c, model.ErrSupportEmailInput)
		return
	}
	if *request.Enabled {
		_, configured, err := service.SupportEmailSMTPConfig(c.Request.Context())
		if err != nil {
			supportEmailError(c, err)
			return
		}
		if !configured {
			common.ApiErrorT(c, "Configure secure SMTP before enabling support email notifications")
			return
		}
	}
	err := model.SaveSupportEmailSettings(c.Request.Context(), *request.Enabled, request.AdminRecipients, request.SiteOrigin)
	model.RecordAuditLog(c, model.AuditLog{UserId: c.GetInt("id"), ActorRole: c.GetInt("role"), Category: model.AuditCategoryOperation, Action: "support_email.settings", Success: err == nil})
	if err != nil {
		supportEmailError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func GetSupportEmailPreferences(c *gin.Context) {
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		supportEmailError(c, err)
		return
	}
	preference, err := model.ReadSupportEmailPreference(c.Request.Context(), user.Id)
	if err != nil {
		supportEmailError(c, err)
		return
	}
	settings, err := model.ReadSupportEmailSettings(c.Request.Context())
	if err != nil {
		supportEmailError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"enabled": !preference.Disabled, "email": user.Email, "email_verified": model.VerifiedSupportEmail(user), "system_enabled": settings.Enabled})
}

func UpdateSupportEmailPreferences(c *gin.Context) {
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	if c.ShouldBindJSON(&request) != nil || request.Enabled == nil {
		supportEmailError(c, model.ErrSupportEmailInput)
		return
	}
	err := model.SaveSupportEmailPreference(c.Request.Context(), c.GetInt("id"), *request.Enabled)
	if err != nil {
		supportEmailError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func ListSupportEmailOutbox(c *gin.Context) {
	page := common.GetPageQuery(c)
	rows := []model.SupportEmailOutbox{}
	var total int64
	query := model.DB.WithContext(c.Request.Context()).Model(&model.SupportEmailOutbox{})
	if err := query.Count(&total).Error; err != nil {
		supportEmailError(c, err)
		return
	}
	if err := query.Select("id", "ticket_id", "event", "status", "attempts", "next_attempt_at", "last_error_code", "created_at").Order("id DESC").Offset(page.GetStartIdx()).Limit(min(page.GetPageSize(), 100)).Find(&rows).Error; err != nil {
		supportEmailError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows, "total": total})
}

func RetrySupportEmail(c *gin.Context)  { manageSupportEmail(c, true) }
func CancelSupportEmail(c *gin.Context) { manageSupportEmail(c, false) }
func manageSupportEmail(c *gin.Context, retry bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		supportEmailError(c, model.ErrSupportEmailInput)
		return
	}
	err = model.ManageSupportEmail(c.Request.Context(), id, retry)
	action := "support_email.cancel"
	if retry {
		action = "support_email.retry"
	}
	model.RecordAuditLog(c, model.AuditLog{UserId: c.GetInt("id"), ActorRole: c.GetInt("role"), Category: model.AuditCategoryOperation, Action: action, Success: err == nil, Content: strconv.FormatInt(id, 10)})
	if err != nil {
		supportEmailError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
