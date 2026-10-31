package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Set only by the internal Responses WebSocket request runner. It is never
// populated from a header/body flag, so callers cannot bypass protocol handling.
const ContextKeyUserCallWebSocket = "user_call_interception_websocket"

// UserCallInterception is also the reusable session-authenticated entry point.
// Register it after UserAuth/SessionAuth and before any distribution or billing.
func UserCallInterception() gin.HandlerFunc {
	return func(c *gin.Context) {
		if interceptAuthenticatedUserCall(c) {
			return
		}
		c.Next()
	}
}

func interceptAuthenticatedUserCall(c *gin.Context) bool {
	// The old billing introspection API does not invoke models. Do not prevent
	// account recovery or turn reads of account information into synthetic chat.
	if c.FullPath() == "/dashboard/billing/subscription" || c.FullPath() == "/v1/dashboard/billing/subscription" ||
		c.FullPath() == "/dashboard/billing/usage" || c.FullPath() == "/v1/dashboard/billing/usage" {
		return false
	}
	userID := c.GetInt("id") // assigned by successful server-side authentication
	policy, err := model.GetUserCallInterception(userID)
	if err != nil {
		service.WriteUserCallInterceptionUnavailable(c)
		return true
	}
	if !policy.Enabled {
		return false
	}
	// Corrupt or manually modified enabled rows fail closed, never pass upstream.
	if model.ValidateUserCallInterceptionReply(true, policy.Reply) != nil || policy.Revision <= 0 {
		service.WriteUserCallInterceptionUnavailable(c)
		return true
	}
	response := classifyUserCallReply(c)
	if response.SupportsText() {
		request, _, parseErr := getModelRequest(c)
		if parseErr != nil {
			abortWithOpenAiMessage(c, invalidRequestStatus(parseErr), i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": "invalid request body"}))
			return true
		}
		if request.Model == "" {
			abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorModelNameRequired))
			return true
		}
		if !authorizeUserCallReply(c, request) {
			return true
		}
		response.Model = request.Model
		if response.Protocol != "gemini" {
			var body struct {
				Stream bool `json:"stream"`
			}
			if common.UnmarshalBodyReusable(c, &body) != nil {
				abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": "invalid stream option"}))
				return true
			}
			response.Stream = body.Stream
		}
	}
	service.WriteUserCallInterception(c, policy, userID, c.GetInt("token_id"), response)
	return true
}

// These authorization checks precede successful local assistant replies just as
// they precede ordinary channel selection. Denied/non-text calls never succeed.
func authorizeUserCallReply(c *gin.Context, request *ModelRequest) bool {
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		value, _ := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		limits, _ := value.(map[string]bool)
		if !TokenModelLimitAllows(limits, request.Model) {
			abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorTokenModelForbidden, map[string]any{"Model": request.Model}))
			return false
		}
	}
	if relayconstant.IsPlaygroundChatPath(c.Request.URL.Path) {
		// Playground's controller disallows PATs; interception must not turn a
		// credential that would fail there into a successful assistant response.
		if c.GetBool("use_access_token") {
			abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgRelayAccessTokenUnsupported))
			return false
		}
		usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
		if request.Group != "" && request.Group != usingGroup && !service.GroupInUserUsableGroups(usingGroup, request.Group) {
			abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorGroupAccessDenied))
			return false
		}
	}
	return true
}

func classifyUserCallReply(c *gin.Context) service.UserCallReply {
	path := c.Request.URL.Path
	if c.GetBool(ContextKeyUserCallWebSocket) || strings.EqualFold(c.GetHeader("Upgrade"), "websocket") ||
		(c.Request.Method == http.MethodGet && (path == "/v1/responses" || path == "/v1/realtime")) {
		return service.UserCallReply{Protocol: "websocket"}
	}
	if c.Request.Method != http.MethodPost {
		return service.UserCallReply{Protocol: "unsupported"}
	}
	switch {
	case relayconstant.IsPlaygroundChatPath(path):
		return service.UserCallReply{Protocol: "playground"}
	case path == "/v1/chat/completions":
		return service.UserCallReply{Protocol: "chat"}
	case path == "/v1/completions":
		return service.UserCallReply{Protocol: "completions"}
	case path == "/v1/responses":
		return service.UserCallReply{Protocol: "responses"}
	case path == "/v1/messages":
		return service.UserCallReply{Protocol: "claude"}
	case (strings.HasPrefix(path, "/v1beta/models/") || strings.HasPrefix(path, "/v1/models/")) &&
		(strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent")):
		stream := strings.HasSuffix(path, ":streamGenerateContent")
		return service.UserCallReply{Protocol: "gemini", Stream: stream, JSONArray: stream && c.Query("alt") != "sse"}
	default:
		return service.UserCallReply{Protocol: "unsupported"}
	}
}
