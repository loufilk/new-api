package service

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// ShouldCheckRequestSensitive uses only authenticated routing context, never a
// request body/header/query group. An exemption applies to the actual selected
// channel and group and is revalidated on every attempt (including Auto retries).
func ShouldCheckRequestSensitive(c *gin.Context) bool {
	if !setting.ShouldCheckPromptSensitive() {
		return false
	}
	if c == nil {
		return true
	}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "auto" {
		group = common.GetContextKeyString(c, constant.ContextKeyAutoGroup)
	}
	if !setting.IsSensitiveCheckExemptGroup(group) {
		return true
	}
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	if userGroup == "" || !IsUserSelectableGroup(userGroup, group) {
		return true
	}
	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	tokenGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	if usingGroup == "auto" || tokenGroup == "auto" {
		if !slices.Contains(GetRequestAutoGroups(c, userGroup), group) {
			return true
		}
	} else if tokenGroup != "" && tokenGroup != group {
		return true
	}
	// A fixed administrative channel or stale Auto context cannot borrow an
	// exemption from an unrelated group. Failure to establish eligibility is
	// deliberately a normal keyword check, not an authorization change.
	return !model.IsChannelEnabledForGroupModel(group,
		common.GetContextKeyString(c, constant.ContextKeyOriginalModel),
		common.GetContextKeyInt(c, constant.ContextKeyChannelId))
}

// CheckRequestSensitiveText is the host's local keyword policy, not provider
// moderation or semantic analysis of images, audio, video, or generated output.
func CheckRequestSensitiveText(c *gin.Context, text string) *types.NewAPIError {
	if !ShouldCheckRequestSensitive(c) {
		return nil
	}
	if contains, _ := CheckSensitiveText(text); !contains {
		return nil
	}
	RequestPolicy(c).AddEvent(PolicyEvent{
		ErrorCode: string(types.ErrorCodeSensitiveWordsDetected), ErrorSource: "local",
		Decision: PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "global"}, Health: "unchanged",
	})
	return types.NewErrorWithStatusCode(errors.New("user sensitive words detected"),
		types.ErrorCodeSensitiveWordsDetected, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

// CheckTaskRequestSensitiveText runs before driver hooks (which may fetch
// upstream data) and billing. Decode only host-supported request formats and
// inspect textual prompt fields; do not inspect uploaded files or credentials.
func CheckTaskRequestSensitiveText(c *gin.Context) *types.NewAPIError {
	if !ShouldCheckRequestSensitive(c) {
		return nil
	}
	var text strings.Builder
	// Declarative protocols may normalize vendor-specific fields into prompt
	// text. Read the host's decoded values, never file bodies or JS objects.
	if request, exists := c.Get("task_request"); exists {
		appendTaskRequestText(&text, request, false)
	}
	if request, ok := c.Get(jsplugin.ContextKeyRouteRequest); ok {
		if route, ok := request.(jsplugin.RouteRequestContext); ok {
			appendTaskRequestText(&text, route.Body, false)
			return CheckRequestSensitiveText(c, text.String())
		}
	}
	var body map[string]any
	var err error
	mediaType, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if strings.HasSuffix(mediaType, "+json") {
		// Legacy requests have no canonical plugin context. The plugin body
		// contract also accepts +json, unlike UnmarshalBodyReusable.
		var storage common.BodyStorage
		storage, err = common.GetBodyStorage(c)
		if err == nil {
			err = common.DecodeJson(storage, &body)
			_, seekErr := storage.Seek(0, io.SeekStart)
			c.Request.Body = io.NopCloser(storage)
			if err == nil {
				err = seekErr
			}
		}
	} else {
		err = common.UnmarshalBodyReusable(c, &body)
	}
	if err != nil {
		status := http.StatusBadRequest
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, status, types.ErrOptionWithSkipRetry())
	}
	appendTaskRequestText(&text, body, false)
	return CheckRequestSensitiveText(c, text.String())
}

func appendTaskRequestText(text *strings.Builder, value any, textual bool) {
	switch value := value.(type) {
	case string:
		if textual {
			text.WriteString(value)
			text.WriteByte('\n')
		}
	case []string:
		if textual {
			for _, item := range value {
				appendTaskRequestText(text, item, true)
			}
		}
	case []any:
		for _, item := range value {
			appendTaskRequestText(text, item, textual)
		}
	case map[string][]string:
		// Canonical form and multipart contexts preserve repeated text fields.
		for key, item := range value {
			appendTaskRequestText(text, map[string]any{key: item}, false)
		}
	case map[string]any:
		for key, item := range value {
			switch key {
			case "prompt", "negative_prompt", "text", "input", "instructions", "system", "content", "messages":
				appendTaskRequestText(text, item, true)
			default:
				// Nested vendor request envelopes can contain supported text
				// fields, but arbitrary scalar metadata is not prompt text.
				appendTaskRequestText(text, item, false)
			}
		}
	}
}
