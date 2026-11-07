package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSmartTokenCreateRequiresAuthorizedRoutingScope(t *testing.T) {
	configureTokenAutoGroupsTest(t, "1", `[]`)
	user := setupTokenAutoGroupsControllerTest(t)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	request := baseAutoTokenRequest("smart-create")
	request["group"] = "default"
	request["smart_routing"], request["no_retry_within_group"] = true, true
	request["routing_strategy"], request["project_label"] = "price", "  项目 A  "
	ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPost, "/api/token/", request, user.Id)
	AddToken(ctx)
	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var token model.Token
	require.NoError(t, model.DB.Where("name = ?", "smart-create").First(&token).Error)
	assert.True(t, token.SmartRouting)
	assert.True(t, token.NoRetryWithinGroup)
	assert.Equal(t, "price", token.RoutingStrategy)
	assert.Equal(t, "项目 A", token.ProjectLabel)
	assert.Equal(t, "default", token.Group)
	assert.True(t, token.CrossGroupRetry)
	// A new smart key still needs an authorized explicit routing selection.
	request["name"], request["group"] = "unavailable-auto", "auto"
	ctx, recorder = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPost, "/api/token/", request, user.Id)
	AddToken(ctx)
	assert.False(t, decodeAPIResponse(t, recorder).Success)
	request["name"], request["auto_groups"] = "authorized-manual-auto", []string{"vip"}
	ctx, recorder = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPost, "/api/token/", request, user.Id)
	AddToken(ctx)
	response = decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	// Smart routing does not turn an arbitrary named manual group into a grant.
	request["name"], request["group"] = "forbidden-create", "private"
	ctx, recorder = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPost, "/api/token/", request, user.Id)
	AddToken(ctx)
	assert.False(t, decodeAPIResponse(t, recorder).Success)
}

func TestSmartTokenUpdatePreservesOmittedFieldsAndStatusOnly(t *testing.T) {
	configureTokenAutoGroupsTest(t, "5", `["default","vip"]`)
	user := setupTokenAutoGroupsControllerTest(t)
	token := seedToken(t, model.DB, user.Id, "smart-update", "synthetic-smart-update")
	token.Group, token.AutoGroups = "retired", `["retired","default"]`
	token.SmartRouting, token.NoRetryWithinGroup, token.CrossGroupRetry = true, true, true
	token.RoutingStrategy, token.ProjectLabel = "success_rate", "Project A"
	require.NoError(t, model.DB.Save(token).Error)
	request := baseAutoTokenRequest("smart-updated")
	delete(request, "group")
	delete(request, "cross_group_retry")
	request["id"] = token.Id
	request["auto_groups"] = []string{"retired", "default"}
	ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id)
	UpdateToken(ctx)
	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var updated model.Token
	require.NoError(t, model.DB.First(&updated, token.Id).Error)
	assert.Equal(t, "retired", updated.Group)
	assert.JSONEq(t, token.AutoGroups, updated.AutoGroups)
	assert.True(t, updated.SmartRouting)
	assert.True(t, updated.NoRetryWithinGroup)
	assert.True(t, updated.CrossGroupRetry)
	assert.Equal(t, "success_rate", updated.RoutingStrategy)
	assert.Equal(t, "Project A", updated.ProjectLabel)
	// Extra policy input to status-only cannot alter routing, even if invalid.
	ctx, recorder = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/?status_only=true", map[string]any{
		"id": token.Id, "status": common.TokenStatusDisabled,
		"smart_routing": false, "no_retry_within_group": false, "routing_strategy": "invalid", "project_label": "ignored",
	}, user.Id)
	UpdateToken(ctx)
	require.True(t, decodeAPIResponse(t, recorder).Success)
	require.NoError(t, model.DB.First(&updated, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, updated.Status)
	assert.True(t, updated.SmartRouting)
	assert.True(t, updated.NoRetryWithinGroup)
	assert.Equal(t, "success_rate", updated.RoutingStrategy)
	assert.Equal(t, "Project A", updated.ProjectLabel)
	// Disabling smart routing must revalidate the retained dormant group.
	request["smart_routing"] = false
	ctx, recorder = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id)
	UpdateToken(ctx)
	assert.False(t, decodeAPIResponse(t, recorder).Success)
	request["group"], request["auto_groups"] = "auto", []string{"vip"}
	request["no_retry_within_group"], request["project_label"] = false, ""
	request["routing_strategy"] = "speed"
	ctx, recorder = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id)
	UpdateToken(ctx)
	response = decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	require.NoError(t, model.DB.First(&updated, token.Id).Error)
	assert.False(t, updated.SmartRouting)
	assert.False(t, updated.NoRetryWithinGroup)
	assert.Empty(t, updated.ProjectLabel)
	assert.Equal(t, "speed", updated.RoutingStrategy)
}

func TestSmartTokenPolicyValidationAndOwnership(t *testing.T) {
	configureTokenAutoGroupsTest(t, "5", `["default","vip"]`)
	user := setupTokenAutoGroupsControllerTest(t)
	for _, tc := range []struct {
		name, strategy, label string
		want                  bool
	}{
		{"legacy default", "", "", true},
		{"unicode boundary", "auto", strings.Repeat("界", 100), true},
		{"unicode overflow", "auto", strings.Repeat("界", 101), false},
		{"invalid strategy", "balanced", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := baseAutoTokenRequest(tc.name)
			request["routing_strategy"], request["project_label"] = tc.strategy, tc.label
			ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPost, "/api/token/", request, user.Id)
			AddToken(ctx)
			response := decodeAPIResponse(t, recorder)
			assert.Equal(t, tc.want, response.Success, response.Message)
			if tc.want {
				var persisted model.Token
				require.NoError(t, model.DB.Where("name = ?", tc.name).First(&persisted).Error)
				assert.Equal(t, "auto", persisted.RoutingStrategy)
				assert.False(t, persisted.SmartRouting)
			}
		})
	}
	token := seedToken(t, model.DB, user.Id, "owner-policy", "synthetic-owner-policy")
	request := baseAutoTokenRequest("cross-owner")
	request["id"], request["smart_routing"] = token.Id, true
	ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id+1)
	UpdateToken(ctx)
	assert.False(t, decodeAPIResponse(t, recorder).Success)
	var unchanged model.Token
	require.NoError(t, model.DB.First(&unchanged, token.Id).Error)
	assert.False(t, unchanged.SmartRouting)
}

func TestSmartTokenModelPreviewUsesLiveScopeAndOwnedModelsKeepLimits(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","a":"A","b":"B"}`))
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "b", "gpt-4.1", constant.ChannelTypeOpenAI, true)
	token := seedTokenModelsToken(t, db, user.Id, "smart-models", "retired", []string{"private"}, "gpt-4o")
	require.NoError(t, db.Model(token).Update("smart_routing", true).Error)
	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/token/models", map[string]any{
		"group": "retired", "auto_groups_mode": "custom", "auto_groups": []string{"private"}, "smart_routing": true,
	}, user.Id)
	PreviewTokenModels(ctx)
	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var models []string
	require.NoError(t, common.Unmarshal(response.Data, &models))
	assert.Equal(t, []string{"gpt-4.1", "gpt-4o"}, models)
	models, response = requestTokenRoutableModels(t, token.Id, user.Id)
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"gpt-4o"}, models)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","b":"B"}`))
	models, response = requestTokenRoutableModels(t, token.Id, user.Id)
	require.True(t, response.Success, response.Message)
	assert.Empty(t, models, "smart routing never widens the model allowlist after group permission is revoked")
}

func TestSmartTokenMigrationIsAdditiveAndIdempotentPostgres(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" {
		t.Skip("requires isolated PostgreSQL test wrapper")
	}
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&legacyToken{}))
	require.NoError(t, db.Create(&legacyToken{UserId: 7, Key: strings.Repeat("k", 48), Name: "legacy", Group: "default"}).Error)
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.Token{}))
	}
	var token model.Token
	require.NoError(t, db.Where("name = ?", "legacy").First(&token).Error)
	assert.False(t, token.SmartRouting)
	assert.False(t, token.NoRetryWithinGroup)
	assert.Empty(t, token.ProjectLabel)
	assert.Equal(t, "auto", token.GetRoutingStrategy())
	assert.Empty(t, token.RoutingStrategy, "legacy database NULL remains unconfigured in storage")
	assert.Equal(t, "auto", buildMaskedTokenResponse(&token).RoutingStrategy)
	assert.Equal(t, strings.Repeat("k", 48), token.Key)
	assert.Equal(t, "default", token.Group)
}

func TestMaskedTokenRoutingStrategyCanonicalizesOnlyLegacyDefault(t *testing.T) {
	for _, strategy := range []string{"", "auto", "success_rate", "price", "speed", "unknown"} {
		t.Run("strategy_"+strategy, func(t *testing.T) {
			token := &model.Token{
				Id: 1, Key: "synthetic-contract-token-not-a-credential", RoutingStrategy: strategy,
				Group: "restricted", SmartRouting: false, NoRetryWithinGroup: true,
				CacheRevision: 7, ModelLimitsEnabled: true, ModelLimits: "allowed-model",
			}
			before := *token
			response := buildMaskedTokenResponse(token)
			want := strategy
			if want == "" {
				want = "auto"
			}
			assert.Equal(t, want, response.RoutingStrategy)
			assert.Equal(t, before, *token, "serialization must not mutate cached or persisted policy")
			assert.Equal(t, token.GetMaskedKey(), response.Key)
			assert.False(t, response.SmartRouting, "default strategy must not enable smart routing")
			assert.True(t, response.NoRetryWithinGroup)
			assert.Equal(t, "restricted", response.Group)
			assert.True(t, response.ModelLimitsEnabled)
			assert.Equal(t, "allowed-model", response.ModelLimits)
		})
	}
}

func TestTokenRequestNormalizesDefaultWithoutChangingRoutingScope(t *testing.T) {
	for _, tc := range []struct {
		name, payload, existing, want string
	}{
		{"create omitted", `{}`, "", "auto"},
		{"create null", `{"routing_strategy":null}`, "", "auto"},
		{"explicit empty", `{"routing_strategy":""}`, "speed", "auto"},
		{"update omitted", `{}`, "speed", "speed"},
		{"update null", `{"routing_strategy":null}`, "price", "price"},
		{"explicit strategy", `{"routing_strategy":"success_rate"}`, "", "success_rate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request tokenRequest
			require.NoError(t, common.UnmarshalJsonStr(tc.payload, &request))
			token := &model.Token{Key: "synthetic-request-contract", RoutingStrategy: tc.existing,
				SmartRouting: false, Group: "restricted", AutoGroups: `["restricted"]`,
				NoRetryWithinGroup: true, RemainQuota: 73, UsedQuota: 19, CacheRevision: 3}
			want := *token
			want.RoutingStrategy = tc.want
			require.NoError(t, request.applyRoutingSettings(token))
			assert.Equal(t, want, *token)
		})
	}
}

func TestSmartTokenAuthUsesLiveConcreteScopeAndRetainsAccountRestrictions(t *testing.T) {
	require.NoError(t, i18n.Init())
	db, user := setupTokenModelsControllerTest(t)
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","a":"A"}`))
	token := seedTokenModelsToken(t, db, user.Id, "smart-auth", "retired", []string{"private"}, "gpt-4o")
	token.Key, token.SmartRouting, token.NoRetryWithinGroup = strings.Repeat("r", 48), true, true
	token.RoutingStrategy = "speed"
	require.NoError(t, db.Save(token).Error)
	router := gin.New()
	router.GET("/runtime", middleware.TokenAuth(), func(c *gin.Context) {
		assert.Equal(t, "auto", common.GetContextKeyString(c, constant.ContextKeyUsingGroup))
		assert.Equal(t, "auto", common.GetContextKeyString(c, constant.ContextKeyTokenGroup))
		assert.True(t, c.GetBool("token_model_limit_enabled"))
		assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyTokenSmartRouting))
		assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyTokenNoRetryWithinGroup))
		assert.Equal(t, "speed", common.GetContextKeyString(c, constant.ContextKeyTokenRoutingStrategy))
		c.JSON(http.StatusOK, service.GetRequestAutoGroups(c, user.Group))
	})
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/runtime", nil)
		req.Header.Set("Authorization", "Bearer sk-"+token.Key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	rec := request()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var groups []string
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &groups))
	assert.Equal(t, []string{"a", "default"}, groups)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	rec = request()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &groups))
	assert.Equal(t, []string{"default"}, groups)
	// IP policy is independent of smart routing and cannot be disabled by it.
	token.AllowIps = common.GetPointer("203.0.113.4")
	require.NoError(t, db.Save(token).Error)
	rec = request()
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	token.AllowIps = common.GetPointer("")
	require.NoError(t, db.Save(token).Error)
	require.NoError(t, db.Model(user).Update("status", common.UserStatusDisabled).Error)
	rec = request()
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}

func TestTokenGroupMetadataIsAuthorizedWeightedAndUnknownSafe(t *testing.T) {
	configureTokenAutoGroupsTest(t, "1", `["metadata-vip"]`)
	user := setupTokenAutoGroupsControllerTest(t)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"metadata-default":1,"metadata-vip":2,"metadata-private":9}`))
	// Hot metric buckets outlive database fixtures; use an owned group namespace
	// so unrelated relay outcomes cannot contaminate this weighted-rate test.
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"metadata-default":"Default","metadata-vip":"VIP","auto":"Auto"}`))
	require.NoError(t, model.DB.Model(user).Update("group", "metadata-default").Error)
	seedTokenModelsAbility(t, model.DB, "metadata-default", "gpt-4o", constant.ChannelTypeOpenAI, true)
	now := time.Now().Unix()
	hour := now - now%3600
	require.NoError(t, model.DB.Create(&[]model.PerfMetric{
		{ModelName: "gpt-4o", Group: "metadata-default", BucketTs: hour, RequestCount: 9, SuccessCount: 9},
		{ModelName: "gpt-4.1", Group: "metadata-default", BucketTs: hour, RequestCount: 1, SuccessCount: 0},
		{ModelName: "old", Group: "metadata-default", BucketTs: hour - 24*3600, RequestCount: 1000, SuccessCount: 0},
		{ModelName: "private-model", Group: "metadata-private", BucketTs: hour, RequestCount: 1, SuccessCount: 1},
	}).Error)
	originalEnabled := perf_metrics_setting.GetSetting().Enabled
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"perf_metrics_setting.enabled": strconv.FormatBool(originalEnabled)}))
	})
	for _, enabled := range []bool{true, false} {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"perf_metrics_setting.enabled": strconv.FormatBool(enabled)}))
		ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodGet, "/api/token/auto-groups", nil, user.Id)
		common.SetContextKey(ctx, constant.ContextKeyUserGroup, "metadata-default")
		GetTokenAutoGroups(ctx)
		response := decodeAPIResponse(t, recorder)
		require.True(t, response.Success, response.Message)
		var data struct {
			Groups               []string `json:"groups"`
			SelectableGroups     []string `json:"selectable_groups"`
			MetricsAvailable     bool     `json:"metrics_available"`
			HistoryBucketSeconds int64    `json:"history_bucket_seconds"`
			GroupDetails         []struct {
				Name        string     `json:"name"`
				Ratio       float64    `json:"ratio"`
				Available   bool       `json:"available"`
				SuccessRate *float64   `json:"success_rate"`
				History     []*float64 `json:"history"`
			} `json:"group_details"`
		}
		require.NoError(t, common.Unmarshal(response.Data, &data))
		assert.Equal(t, []string{"metadata-vip"}, data.Groups)
		assert.Equal(t, []string{"metadata-default", "metadata-vip"}, data.SelectableGroups)
		assert.Equal(t, enabled, data.MetricsAvailable)
		assert.Equal(t, int64(3600), data.HistoryBucketSeconds)
		require.Len(t, data.GroupDetails, 2)
		first, second := data.GroupDetails[0], data.GroupDetails[1]
		assert.True(t, first.Available)
		assert.False(t, second.Available)
		assert.Equal(t, float64(2), second.Ratio)
		assert.Nil(t, second.SuccessRate)
		require.Len(t, first.History, 24)
		for _, bucket := range second.History {
			assert.Nil(t, bucket)
		}
		if enabled {
			require.NotNil(t, first.SuccessRate)
			assert.Equal(t, float64(90), *first.SuccessRate)
			require.NotNil(t, first.History[23])
			assert.Equal(t, float64(90), *first.History[23])
		} else {
			assert.Nil(t, first.SuccessRate)
			for _, bucket := range first.History {
				assert.Nil(t, bucket)
			}
		}
	}
}
