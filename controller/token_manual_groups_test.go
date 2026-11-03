package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManualTokenGroupsCreateReorderAndStatusWithoutVirtualAuto(t *testing.T) {
	configureTokenAutoGroupsTest(t, "2", `["default"]`)
	user := setupTokenAutoGroupsControllerTest(t)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	request := baseAutoTokenRequest("manual-ordered")
	request["auto_groups"] = []string{"vip", "default"}
	ctx, rec := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPost, "/api/token/", request, user.Id)
	AddToken(ctx)
	response := decodeAPIResponse(t, rec)
	require.True(t, response.Success, response.Message)
	var token model.Token
	require.NoError(t, model.DB.Where("name = ?", request["name"]).First(&token).Error)
	require.False(t, token.SmartRouting)
	require.Equal(t, "auto", token.Group)
	require.JSONEq(t, `["vip","default"]`, token.AutoGroups)
	request["id"] = token.Id
	request["auto_groups"] = []string{"default", "vip"}
	ctx, rec = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id)
	UpdateToken(ctx)
	response = decodeAPIResponse(t, rec)
	require.True(t, response.Success, response.Message)
	delete(request, "auto_groups")
	delete(request, "group")
	ctx, rec = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id)
	UpdateToken(ctx)
	require.True(t, decodeAPIResponse(t, rec).Success)
	for _, status := range []int{common.TokenStatusDisabled, common.TokenStatusEnabled} {
		ctx, rec = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/?status_only=true", map[string]any{"id": token.Id, "status": status, "auto_groups": []string{"private"}}, user.Id)
		UpdateToken(ctx)
		response = decodeAPIResponse(t, rec)
		require.True(t, response.Success, response.Message)
	}
	require.NoError(t, model.DB.First(&token, token.Id).Error)
	assert.JSONEq(t, `["default","vip"]`, token.AutoGroups)
	assert.False(t, service.GroupInUserUsableGroups(user.Group, "auto"), "manual selections never grant inherited Auto")
	ctx, rec = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id+1)
	UpdateToken(ctx)
	assert.False(t, decodeAPIResponse(t, rec).Success, "another account cannot edit this token")
}

func TestManualTokenGroupsRejectInvalidOrInheritedScopeWithoutVirtualAuto(t *testing.T) {
	configureTokenAutoGroupsTest(t, "2", `["default"]`)
	user := setupTokenAutoGroupsControllerTest(t)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1,"private":1}`))
	token := seedToken(t, model.DB, user.Id, "existing-manual", "manual-test-key")
	token.Group = "auto"
	require.NoError(t, token.SetAutoGroups([]string{"vip", "default"}))
	require.NoError(t, model.DB.Save(token).Error)
	for _, tc := range []struct {
		name   string
		groups []string
	}{
		{"inherited empty", []string{}}, {"inherited null", nil},
		{"unauthorized", []string{"default", "private"}}, {"deleted", []string{"default", "deleted"}},
		{"duplicate", []string{"vip", "vip"}}, {"over limit", []string{"vip", "default", "private"}},
		{"virtual group", []string{"auto"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := baseAutoTokenRequest(tc.name)
			request["auto_groups"] = tc.groups
			ctx, rec := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPost, "/api/token/", request, user.Id)
			AddToken(ctx)
			require.False(t, decodeAPIResponse(t, rec).Success)
			request["id"] = token.Id
			ctx, rec = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id)
			UpdateToken(ctx)
			require.False(t, decodeAPIResponse(t, rec).Success)
			var unchanged model.Token
			require.NoError(t, model.DB.First(&unchanged, token.Id).Error)
			assert.Equal(t, token.AutoGroups, unchanged.AutoGroups)
		})
	}
	request := baseAutoTokenRequest("inherited-omitted")
	ctx, rec := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPost, "/api/token/", request, user.Id)
	AddToken(ctx)
	assert.False(t, decodeAPIResponse(t, rec).Success)
}

func TestManualTokenGroupsAuthPreviewAndRevocationWithoutVirtualAuto(t *testing.T) {
	require.NoError(t, i18n.Init())
	db, user := setupTokenModelsControllerTest(t)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","a":"A","b":"B"}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default"]`))
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "b", "gpt-4.1", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "default", "gpt-4o", constant.ChannelTypeOpenAI, true)
	token := seedTokenModelsToken(t, db, user.Id, "manual-auth", "auto", []string{"b", "a"}, "gpt-4.1")
	token.Key = strings.Repeat("m", 48)
	require.NoError(t, db.Save(token).Error)
	router := gin.New()
	router.GET("/runtime", middleware.TokenAuth(), func(c *gin.Context) {
		assert.Equal(t, "auto", common.GetContextKeyString(c, constant.ContextKeyTokenGroup))
		assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyTokenSmartRouting))
		assert.True(t, c.GetBool("token_model_limit_enabled"))
		assert.Equal(t, token.GetModelLimitsMap(), c.MustGet("token_model_limit"))
		c.JSON(http.StatusOK, service.GetRequestAutoGroups(c, user.Group))
	})
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/runtime", nil)
		req.Header.Set("Authorization", "Bearer sk-"+token.Key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	preview := func() []string {
		ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/token/models", map[string]any{"group": "auto", "auto_groups_mode": "custom", "auto_groups": []string{"b", "a"}}, user.Id)
		PreviewTokenModels(ctx)
		response := decodeAPIResponse(t, rec)
		require.True(t, response.Success, response.Message)
		var models []string
		require.NoError(t, common.Unmarshal(response.Data, &models))
		return models
	}
	rec := request()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `["b","a"]`, rec.Body.String())
	assert.Equal(t, []string{"gpt-4.1", "gpt-4o"}, preview())
	models, response := requestTokenRoutableModels(t, token.Id, user.Id)
	require.True(t, response.Success)
	assert.Equal(t, []string{"gpt-4.1"}, models)
	_, response = requestTokenRoutableModels(t, token.Id, user.Id+1)
	assert.False(t, response.Success)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","a":"A"}`))
	rec = request()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `["a"]`, rec.Body.String())
	assert.Equal(t, []string{"gpt-4o"}, preview())
	models, response = requestTokenRoutableModels(t, token.Id, user.Id)
	require.True(t, response.Success)
	assert.Empty(t, models, "revocation does not widen token model allowlist")
	token.AllowIps = common.GetPointer("203.0.113.4")
	require.NoError(t, db.Save(token).Error)
	assert.Equal(t, http.StatusForbidden, request().Code)
	token.AllowIps = common.GetPointer("")
	require.NoError(t, db.Save(token).Error)
	require.NoError(t, db.Model(user).Update("status", common.UserStatusDisabled).Error)
	assert.Equal(t, http.StatusForbidden, request().Code)
	require.NoError(t, db.Model(user).Update("status", common.UserStatusEnabled).Error)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	assert.Equal(t, http.StatusForbidden, request().Code, "all selected groups revoked fails closed")
	assert.Empty(t, preview(), "must never fall back to globally configured default")
}
