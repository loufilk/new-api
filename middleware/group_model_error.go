package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// abortIfModelUnavailableInGroup refines only the initial distributor's empty
// selection. It never runs after an upstream response or changes retry/fallback
// selection. Unknown scope or failed verification leaves the existing 503.
func abortIfModelUnavailableInGroup(c *gin.Context, group, modelName string, configured func(context.Context, []string, string) (bool, error)) bool {
	if group == "" || modelName == "" {
		return false
	}
	if _, nativeRoute := c.Get(jsplugin.ContextKeyRouteRequest); nativeRoute {
		// Native plugin routes own their error protocol and sanitization.
		return false
	}
	groups := []string{group}
	if group == "auto" {
		userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		if userGroup == "" {
			return false
		}
		groups = service.GetRequestAutoGroups(c, userGroup)
		if len(groups) == 0 {
			return false
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()
	supported, err := configured(ctx, groups, modelName)
	if err != nil || supported || ctx.Err() != nil {
		return false
	}
	const code types.ErrorCode = "model_not_available_in_group"
	message := i18n.T(c, i18n.MsgDistributorModelNotAvailableInGroup)
	if c.Request.URL.Path == "/v1/messages" {
		requestID := c.GetString(common.RequestIdKey)
		c.JSON(http.StatusNotFound, gin.H{
			"type": "error",
			"error": gin.H{
				"type": "not_found_error", "code": code,
				"message": common.MessageWithRequestId(message, requestID),
			},
			"request_id": requestID,
		})
		c.Abort()
		logger.LogError(c.Request.Context(), common.LogText("user %d | %s", c.GetInt("id"), message))
	} else {
		abortWithOpenAiMessage(c, http.StatusNotFound, message, code)
	}
	return true
}
