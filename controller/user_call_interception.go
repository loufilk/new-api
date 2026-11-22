package controller

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func callInterceptionTarget(c *gin.Context, permission authz.Permission) (int, bool) {
	c.Header("Cache-Control", "no-store")
	actor, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil || actor.Status != common.UserStatusEnabled || actor.Role < common.RoleAdminUser || !common.IsValidateRole(actor.Role) || !authz.Can(actor.Id, actor.Role, permission) {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Unauthorized, insufficient privileges"))
		return 0, false
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return 0, false
	}
	target, err := model.GetUserById(id, false)
	if err != nil {
		callInterceptionAPIError(c, err)
		return 0, false
	}
	if !canManageTargetRole(actor.Role, target.Role) {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to access users of same or higher level"))
		return 0, false
	}
	return id, true
}

func callInterceptionAPIError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrCallInterceptionConflict):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage(model.ErrCallInterceptionConflict.Error()))
	case errors.Is(err, model.ErrCallInterceptionPermission):
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage(model.ErrCallInterceptionPermission.Error()))
	case errors.Is(err, model.ErrCallInterceptionInvalid), errors.Is(err, model.ErrCallInterceptionReplyRequired), errors.Is(err, model.ErrCallInterceptionReplyTooLong), errors.Is(err, model.ErrCallInterceptionReplyNull):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(err.Error()))
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("User not found"))
	default:
		// A database error can embed SQL values, including the custom reply.
		// Neither return nor log the raw error from these persistence operations.
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Call interception operation failed, please try again later"))
	}
}

func GetUserCallInterception(c *gin.Context) {
	userID, ok := callInterceptionTarget(c, authz.UserCallInterceptionRead)
	if !ok {
		return
	}
	config, err := model.GetUserCallInterception(userID)
	if err != nil {
		callInterceptionAPIError(c, err)
		return
	}
	common.ApiSuccess(c, config)
}

func UpdateUserCallInterception(c *gin.Context) {
	userID, ok := callInterceptionTarget(c, authz.UserCallInterceptionUpdate)
	if !ok {
		return
	}
	// Dedicated request DTO: user_id, role, quota and every other User field are
	// deliberately not bindable. Pointers distinguish missing fields from false/0.
	var request struct {
		Enabled          *bool   `json:"enabled"`
		Reply            *string `json:"reply"`
		ExpectedRevision *int64  `json:"expected_revision"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024))
	if err != nil || common.Unmarshal(body, &request) != nil || request.Enabled == nil || request.Reply == nil || request.ExpectedRevision == nil {
		callInterceptionAPIError(c, model.ErrCallInterceptionInvalid)
		return
	}
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		requestID = common.NewRequestId()
	}
	config, err := model.SaveUserCallInterception(c.GetInt("id"), userID, model.UserCallInterceptionUpdate{Enabled: *request.Enabled, Reply: *request.Reply, ExpectedRevision: *request.ExpectedRevision, RequestID: requestID})
	if err != nil {
		callInterceptionAPIError(c, err)
		return
	}
	// The primary-DB audit already committed atomically. Avoid duplicate rows in
	// consumption/management storage; failed attempts retain the safe route-only
	// AdminAuth fallback audit, which never copies this request body.
	markAuditLogged(c)
	common.ApiSuccess(c, config)
}

func callInterceptionLogFilter(c *gin.Context, userID int) (model.UserCallInterceptionLogFilter, *common.PageInfo, error) {
	filter := model.UserCallInterceptionLogFilter{UserID: userID, RequestID: c.Query("request_id"), Protocol: c.Query("protocol"), IdentityKind: c.Query("identity_kind")}
	switch filter.IdentityKind {
	case "", model.UserCallInterceptionIdentityAPIKey, model.UserCallInterceptionIdentityDashboardSession, model.UserCallInterceptionIdentityArtifactCapability, model.UserCallInterceptionIdentityPublicArtifact:
	default:
		return filter, nil, model.ErrCallInterceptionInvalid
	}
	for name, destination := range map[string]*int64{"start_time": &filter.StartTime, "end_time": &filter.EndTime} {
		if raw := c.Query(name); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed < 0 {
				return filter, nil, model.ErrCallInterceptionInvalid
			}
			*destination = parsed
		}
	}
	for name, destination := range map[string]**int{"token_id": &filter.TokenID, "actor_id": &filter.ActorID, "actor_user_id": &filter.ActorUserID} {
		if raw := c.Query(name); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 {
				return filter, nil, model.ErrCallInterceptionInvalid
			}
			*destination = &parsed
		}
	}
	if raw := c.Query("status"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 100 || parsed > 599 {
			return filter, nil, model.ErrCallInterceptionInvalid
		}
		filter.Status = parsed
	}
	if len(filter.RequestID) > 128 || len(filter.Protocol) > 32 || strings.ContainsFunc(filter.RequestID+filter.Protocol, unicode.IsControl) || (filter.EndTime > 0 && filter.StartTime > filter.EndTime) {
		return filter, nil, model.ErrCallInterceptionInvalid
	}
	page := common.GetPageQuery(c)
	if raw := c.Query("page"); raw != "" && c.Query("p") == "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return filter, nil, model.ErrCallInterceptionInvalid
		}
		page.Page = parsed
	}
	page.Page = min(max(page.Page, 1), 1_000_000)
	page.PageSize = min(max(page.PageSize, 1), 100)
	return filter, page, nil
}

func GetUserCallInterceptionHits(c *gin.Context) {
	userID, ok := callInterceptionTarget(c, authz.UserCallInterceptionRead)
	if !ok {
		return
	}
	filter, page, err := callInterceptionLogFilter(c, userID)
	if err != nil {
		callInterceptionAPIError(c, err)
		return
	}
	rows, total, err := model.GetUserCallInterceptionHits(filter, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		callInterceptionAPIError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func GetUserCallInterceptionAudits(c *gin.Context) {
	userID, ok := callInterceptionTarget(c, authz.UserCallInterceptionRead)
	if !ok {
		return
	}
	filter, page, err := callInterceptionLogFilter(c, userID)
	if err != nil {
		callInterceptionAPIError(c, err)
		return
	}
	rows, total, err := model.GetUserCallInterceptionAudits(filter, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		callInterceptionAPIError(c, err)
		return
	}
	page.SetItems(rows)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
