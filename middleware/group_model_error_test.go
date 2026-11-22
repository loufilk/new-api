package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupModelErrorProtocolAndPrivacy(t *testing.T) {
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)
	for _, locale := range []struct{ language, message string }{
		{"en", "The requested model is not supported by this token's selected group(s). Check the model name and token group, and use the same token to query /v1/models for available models."},
		{"zh-CN", "当前令牌所选分组不支持此模型。请检查模型名称和令牌分组，并使用同一令牌访问 /v1/models 查看可用模型。"},
		{"zh-TW", "目前權杖所選分組不支援此模型。請檢查模型名稱和權杖分組，並使用同一權杖存取 /v1/models 查看可用模型。"},
	} {
		for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
			t.Run(locale.language+path, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, path, nil)
				c.Request.Header.Set("Accept-Language", locale.language)
				c.Set(common.RequestIdKey, "test-request-id")
				calls := 0
				lookup := func(ctx context.Context, groups []string, name string) (bool, error) {
					calls++
					assert.Equal(t, []string{"private-effective-group"}, groups)
					assert.Equal(t, "private-routing-model", name)
					deadline, ok := ctx.Deadline()
					require.True(t, ok, "the error-path database probe must have a deadline")
					assert.LessOrEqual(t, time.Until(deadline), time.Second)
					return false, nil
				}
				require.True(t, abortIfModelUnavailableInGroup(c, "private-effective-group", "private-routing-model", lookup))
				require.Equal(t, 1, calls)
				require.Equal(t, http.StatusNotFound, recorder.Code)
				assert.True(t, c.IsAborted())
				message := locale.message + " (request id: test-request-id)"
				if path == "/v1/messages" {
					assert.JSONEq(t, fmt.Sprintf(`{"type":"error","error":{"type":"not_found_error","code":"model_not_available_in_group","message":%q},"request_id":"test-request-id"}`, message), recorder.Body.String())
				} else {
					assert.JSONEq(t, fmt.Sprintf(`{"error":{"type":"new_api_error","code":"model_not_available_in_group","message":%q}}`, message), recorder.Body.String())
				}
				assert.NotContains(t, recorder.Body.String(), "private-effective-group")
				assert.NotContains(t, recorder.Body.String(), "private-routing-model")
			})
		}
	}
}

func TestGroupModelErrorKeepsUncertainAndTemporaryFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		group     string
		supported bool
		err       error
		native    bool
		wantCalls int
	}{
		{name: "disabled or filtered configuration", group: "selected", supported: true, wantCalls: 1},
		{name: "database error", group: "selected", err: errors.New("private database failure"), wantCalls: 1},
		{name: "deadline", group: "selected", err: context.DeadlineExceeded, wantCalls: 1},
		{name: "unresolved group"},
		{name: "auto without user scope", group: "auto"},
		{name: "native plugin owns error protocol", group: "selected", native: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tc.native {
				c.Set(jsplugin.ContextKeyRouteRequest, struct{}{})
			}
			calls := 0
			lookup := func(context.Context, []string, string) (bool, error) {
				calls++
				return tc.supported, tc.err
			}
			assert.False(t, abortIfModelUnavailableInGroup(c, tc.group, "client-model", lookup))
			assert.Equal(t, tc.wantCalls, calls)
			assert.False(t, c.IsAborted())
			assert.False(t, c.Writer.Written(), "leave the existing unavailable response untouched")
		})
	}
}

func TestGroupModelErrorChecksWholeAuthorizedAutoScope(t *testing.T) {
	require.NoError(t, i18n.Init())
	originalGroups := setting.AutoGroups2JsonString()
	originalUsable := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	originalLimit := setting.GetMaxTokenAutoGroups()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprint(originalLimit)))
	})
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","fallback","global-only"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","fallback":"Fallback","global-only":"Global"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"fallback":1,"global-only":1}`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))

	for _, tc := range []struct {
		name       string
		scoped     []string
		wantGroups []string
		configured bool
		wantAbort  bool
	}{
		{name: "global inherited scope is not truncated", wantGroups: []string{"default", "fallback", "global-only"}, configured: true},
		{name: "fallback configuration prevents wrong-group claim", scoped: []string{"revoked", "default", "fallback"}, wantGroups: []string{"default", "fallback"}, configured: true},
		{name: "all scoped groups absent", scoped: []string{"default", "fallback"}, wantGroups: []string{"default", "fallback"}, wantAbort: true},
		{name: "empty explicit scope is not a mismatch", scoped: []string{}},
		{name: "revoked scope is not a mismatch", scoped: []string{"revoked"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
			common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, 2) // Selection already exhausted its candidates.
			if tc.scoped != nil {
				common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, tc.scoped)
			}
			calls := 0
			lookup := func(_ context.Context, groups []string, _ string) (bool, error) {
				calls++
				assert.Equal(t, tc.wantGroups, groups)
				return tc.configured, nil
			}
			assert.Equal(t, tc.wantAbort, abortIfModelUnavailableInGroup(c, "auto", "client-model", lookup))
			if len(tc.wantGroups) == 0 {
				assert.Zero(t, calls)
			} else {
				assert.Equal(t, 1, calls)
			}
		})
	}
}

func TestGroupModelErrorPreservesTokenWhitelistForbidden(t *testing.T) {
	require.NoError(t, i18n.Init())
	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "selected")
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"allowed-model": true})
	}, Distribute(), func(c *gin.Context) {
		t.Error("a forbidden model must not reach relay")
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"forbidden-model"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "model_not_available_in_group")
}
