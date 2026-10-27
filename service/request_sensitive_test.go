package service

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRequestSensitivePolicy(t *testing.T) {
	t.Helper()
	db := setupChannelSelectAutoGroupsTest(t)
	enabled, promptEnabled := setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled
	words, groups := setting.SensitiveWordsToString(), setting.SensitiveCheckExemptGroupsToString()
	setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled = true, true
	setting.SensitiveWordsFromString("blocked")
	setting.LoadSensitiveCheckExemptGroups("破甲")
	t.Cleanup(func() {
		setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled = enabled, promptEnabled
		setting.SensitiveWordsFromString(words)
		setting.LoadSensitiveCheckExemptGroups(groups)
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","破甲":"Exempt"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"破甲":1}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["破甲","default"]`))
	createChannelSelectAutoGroupsChannel(t, db, 901, "破甲", "policy-model")
	createChannelSelectAutoGroupsChannel(t, db, 902, "default", "policy-model")
	model.InitChannelCache()
}

func requestSensitiveContext(group string, channel int) *gin.Context {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, group)
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, group)
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "policy-model")
	common.SetContextKey(ctx, constant.ContextKeyChannelId, channel)
	return ctx
}

func TestRequestSensitiveExemptionRequiresAuthorizedActualRoute(t *testing.T) {
	setupRequestSensitivePolicy(t)
	tests := []struct {
		name       string
		group      string
		channel    int
		autoGroup  string
		autoGroups []string
		blocked    bool
	}{
		{"configured authorized group", "破甲", 901, "", nil, false},
		{"other group", "default", 902, "", nil, true},
		{"stale auto cannot override concrete group", "default", 902, "破甲", nil, true},
		{"auto selected exempt", "auto", 901, "破甲", []string{"破甲", "default"}, false},
		{"auto selected other", "auto", 902, "default", []string{"破甲", "default"}, true},
		{"unresolved auto", "auto", 901, "", []string{"破甲"}, true},
		{"outside token auto scope", "auto", 901, "破甲", []string{"default"}, true},
		{"empty token auto scope does not inherit", "auto", 901, "破甲", []string{}, true},
		{"fixed channel belongs to different group", "破甲", 902, "", nil, true},
		{"unselected route", "破甲", 0, "", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := requestSensitiveContext(tc.group, tc.channel)
			common.SetContextKey(ctx, constant.ContextKeyAutoGroup, tc.autoGroup)
			if tc.autoGroups != nil {
				common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, tc.autoGroups)
			}
			err := CheckRequestSensitiveText(ctx, "Contains BLOCKED text")
			if !tc.blocked {
				require.Nil(t, err)
				return
			}
			require.NotNil(t, err)
			assert.Equal(t, types.ErrorCodeSensitiveWordsDetected, err.GetErrorCode())
			assert.Equal(t, http.StatusBadRequest, err.StatusCode)
			assert.Equal(t, "unchanged", RequestPolicy(ctx).Events()[0].Health)
		})
	}
	ctx := requestSensitiveContext("破甲", 901)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	require.NotNil(t, CheckRequestSensitiveText(ctx, "blocked"), "revoked permission is never an exemption")
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "")
	require.NotNil(t, CheckRequestSensitiveText(ctx, "blocked"), "unknown user authorization fails closed")
}

func TestRequestSensitiveRechecksAutoCrossGroupRetry(t *testing.T) {
	setupRequestSensitivePolicy(t)
	ctx := requestSensitiveContext("auto", 0)
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"破甲", "default"})
	common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)
	retry := &RetryParam{Ctx: ctx, TokenGroup: "auto", ModelName: "policy-model", Retry: common.GetPointer(0)}
	first, selected, err := CacheGetRandomSatisfiedChannel(retry)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, "破甲", selected)
	common.SetContextKey(ctx, constant.ContextKeyChannelId, first.Id)
	require.Nil(t, CheckRequestSensitiveText(ctx, "blocked"))
	retry.IncreaseRetry()
	second, selected, err := CacheGetRandomSatisfiedChannel(retry)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, "default", selected)
	common.SetContextKey(ctx, constant.ContextKeyChannelId, second.Id)
	require.NotNil(t, CheckRequestSensitiveText(ctx, "blocked"), "previous exempt attempt must not cache a bypass")
}

func TestRequestSensitiveDisabledSwitchesRemainDisabled(t *testing.T) {
	setupRequestSensitivePolicy(t)
	ctx := requestSensitiveContext("default", 902)
	setting.CheckSensitiveEnabled = false
	assert.Nil(t, CheckRequestSensitiveText(ctx, "blocked"))
	setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled = true, false
	assert.Nil(t, CheckRequestSensitiveText(ctx, "blocked"))
}

func TestTaskSensitivePolicyChecksExtractedTextAndPreservesBody(t *testing.T) {
	setupRequestSensitivePolicy(t)
	for _, tc := range []struct {
		name, body string
		blocked    bool
	}{
		{"spoofed group cannot bypass", `{"model":"policy-model","group":"破甲","prompt":"blocked"}`, true},
		{"nested text", `{"input":{"messages":[{"role":"user","content":[{"type":"text","text":"blocked"}]}]}}`, true},
		{"instructions", `{"instructions":"BLOCKED","input":"safe"}`, true},
		{"media and metadata are not textual prompts", `{"model":"blocked","metadata":{"name":"blocked"},"input":[{"type":"input_image","image_url":"https://blocked/image"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := requestSensitiveContext("default", 902)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos?group=破甲", strings.NewReader(tc.body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			t.Cleanup(func() { common.CleanupBodyStorage(ctx) })
			err := CheckTaskRequestSensitiveText(ctx)
			assert.Equal(t, tc.blocked, err != nil)
			body, readErr := io.ReadAll(ctx.Request.Body)
			require.NoError(t, readErr)
			assert.JSONEq(t, tc.body, string(body))
		})
	}
	t.Run("json suffix preserves the request body", func(t *testing.T) {
		ctx := requestSensitiveContext("default", 902)
		body := `{"prompt":"blocked"}`
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/problem+json; charset=utf-8")
		t.Cleanup(func() { common.CleanupBodyStorage(ctx) })
		require.NotNil(t, CheckTaskRequestSensitiveText(ctx))
		preserved, err := io.ReadAll(ctx.Request.Body)
		require.NoError(t, err)
		assert.JSONEq(t, body, string(preserved))
	})
	t.Run("canonical protocol and form text is checked before driver hooks", func(t *testing.T) {
		for _, useNormalized := range []bool{false, true} {
			ctx := requestSensitiveContext("default", 902)
			ctx.Set(jsplugin.ContextKeyRouteRequest, jsplugin.RouteRequestContext{
				Body: map[string]any{"kind": "multipart", "fields": map[string][]string{"prompt": {"safe", "blocked"}}},
			})
			if useNormalized {
				ctx.Set(jsplugin.ContextKeyRouteRequest, jsplugin.RouteRequestContext{
					Body: map[string]any{"kind": "json", "value": map[string]any{"vendor_prompt": "blocked"}},
				})
				ctx.Set("task_request", map[string]any{"prompt": "blocked"})
			}
			require.NotNil(t, CheckTaskRequestSensitiveText(ctx))
		}
	})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("prompt", "blocked"))
	file, err := writer.CreateFormFile("image", "test.png")
	require.NoError(t, err)
	_, err = file.Write([]byte("blocked file bytes are not prompt text"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	ctx := requestSensitiveContext("default", 902)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	ctx.Request.Header.Set("Content-Type", writer.FormDataContentType())
	t.Cleanup(func() { common.CleanupBodyStorage(ctx) })
	require.NotNil(t, CheckTaskRequestSensitiveText(ctx), "multipart prompt is checked before plugin hooks")
	form, err := common.ParseMultipartFormReusable(ctx)
	require.NoError(t, err)
	defer form.RemoveAll()
	assert.Equal(t, []string{"blocked"}, form.Value["prompt"])
	require.Len(t, form.File["image"], 1)
	preservedFile, err := form.File["image"][0].Open()
	require.NoError(t, err)
	defer preservedFile.Close()
	preservedBytes, err := io.ReadAll(preservedFile)
	require.NoError(t, err)
	assert.Equal(t, "blocked file bytes are not prompt text", string(preservedBytes))
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "破甲")
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "破甲")
	common.SetContextKey(ctx, constant.ContextKeyChannelId, 901)
	assert.Nil(t, CheckTaskRequestSensitiveText(ctx), "authorized exempt multipart request is not consumed")
}
