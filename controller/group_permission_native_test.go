package controller

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercises real management authentication, settings persistence/invalidation,
// token save/preview and relay authentication with actual PostgreSQL and Redis.
// The final handler only reports the authorized synthetic model scope; it never
// invokes an upstream or consumes quota.
func TestGroupPermissionNativeRevocationChain(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" || os.Getenv("TEST_REDIS_DSN") == "" {
		t.Skip("requires owned PostgreSQL and Redis fixtures")
	}
	db := setupGroupRenameControllerTest(t)
	require.NoError(t, db.AutoMigrate(&model.UserSession{}))
	require.NoError(t, i18n.Init())
	options, err := redis.ParseURL(os.Getenv("TEST_REDIS_DSN"))
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(options.Addr)
	require.NoError(t, err)
	require.True(t, net.ParseIP(host).IsLoopback())
	options.DB = 12
	rdb := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	ctx := context.Background()
	require.NoError(t, rdb.Ping(ctx).Err())
	size, err := rdb.DBSize(ctx).Result()
	require.NoError(t, err)
	require.Zero(t, size, "refuse a nonempty fixture Redis database")
	previousRedis, previousRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = true, rdb
	previousSecret := common.SessionSecret
	common.SessionSecret = "synthetic-group-permission-session-key-only"
	t.Cleanup(func() {
		common.SessionSecret = previousSecret
		common.RedisEnabled, common.RDB = previousRedis, previousRDB
		require.NoError(t, rdb.FlushDB(ctx).Err())
	})
	info, err := rdb.Info(ctx, "server").Result()
	require.NoError(t, err)
	require.Contains(t, info, "redis_version:8.8.0")
	previousModels := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModels)) })
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o":1,"gpt-4.1":1}`))
	const definitions = `{"default":1,"enterprise":1,"privateA":1,"privateB":1}`
	const specialKey = "group_ratio_setting.group_special_usable_group"
	for key, value := range map[string]string{"GroupRatio": definitions, "UserUsableGroups": `{"default":"Default"}`, "AutoGroups": `["privateA","privateB"]`, specialKey: `{"enterprise":{}}`} {
		require.NoError(t, db.Create(&model.Option{Key: key, Value: value}).Error)
	}
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(definitions))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["privateA","privateB"]`))
	require.NoError(t, ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.UnmarshalJSON([]byte(`{"enterprise":{}}`)))
	ordinary := &model.User{Username: "native-ordinary", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "native-ordinary"}
	enterprise := &model.User{Username: "native-enterprise", Group: "enterprise", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "native-enterprise"}
	root := &model.User{Username: "native-root", Group: "default", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "native-root"}
	for _, user := range []*model.User{ordinary, enterprise, root} {
		require.NoError(t, db.Create(user).Error)
	}
	_, ordinaryJWT := createAccessTokenTestSession(t, ordinary.Id, "native-ordinary")
	_, enterpriseJWT := createAccessTokenTestSession(t, enterprise.Id, "native-enterprise")
	_, rootJWT := createAccessTokenTestSession(t, root.Id, "native-root")
	seedTokenModelsAbility(t, db, "privateA", "gpt-4o", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "privateB", "gpt-4.1", constant.ChannelTypeOpenAI, true)
	router := gin.New()
	router.POST("/api/option/group-rename", middleware.RootAuth(), RenameGroups)
	router.POST("/api/token/", middleware.UserAuth(), AddToken)
	router.PUT("/api/token/", middleware.UserAuth(), UpdateToken)
	router.POST("/api/token/models", middleware.UserAuth(), PreviewTokenModels)
	router.GET("/runtime", middleware.TokenAuth(), func(c *gin.Context) {
		userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
		groups := []string{group}
		if group == "auto" {
			groups = service.GetRequestAutoGroups(c, userGroup)
		}
		models, err := getRoutableModelsForGroups(&model.UserBase{Group: userGroup}, groups, nil)
		require.NoError(t, err)
		common.ApiSuccess(c, models)
	})
	request := func(method, path, bearer string, body any) *httptest.ResponseRecorder {
		raw, err := common.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	grants := `{"enterprise":{"+:privateA":"A","+:privateB":"B","+:auto":"Auto"}}`
	grantRequest := map[string]any{"options": map[string]string{specialKey: grants}}
	denied := request(http.MethodPost, "/api/option/group-rename", ordinaryJWT, map[string]any{"options": map[string]string{specialKey: `{"default":{"+:privateA":"Self-grant attempt"}}`}})
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	assert.Contains(t, denied.Body.String(), "AUTH_INSUFFICIENT_PRIVILEGE")
	var option model.Option
	require.NoError(t, db.Where(&model.Option{Key: specialKey}).First(&option).Error)
	assert.JSONEq(t, `{"enterprise":{}}`, option.Value)
	granted := request(http.MethodPost, "/api/option/group-rename", rootJWT, grantRequest)
	require.True(t, decodeAPIResponse(t, granted).Success, granted.Body.String())
	ordinaryBody := baseAutoTokenRequest("must-not-save")
	ordinaryBody["group"] = "privateA"
	denied = request(http.MethodPost, "/api/token/", ordinaryJWT, ordinaryBody)
	require.False(t, decodeAPIResponse(t, denied).Success)
	var count int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", ordinary.Id).Count(&count).Error)
	assert.Zero(t, count)
	tokens := map[string]*model.Token{}
	for _, group := range []string{"privateA", "privateB", "auto"} {
		body := baseAutoTokenRequest("native-" + group)
		body["group"] = group
		if group == "auto" {
			body["auto_groups"] = []string{"privateA", "privateB"}
		}
		saved := request(http.MethodPost, "/api/token/", enterpriseJWT, body)
		require.True(t, decodeAPIResponse(t, saved).Success, saved.Body.String())
		token := &model.Token{}
		require.NoError(t, db.Where("name = ?", "native-"+group).First(token).Error)
		tokens[group] = token
		actual := request(http.MethodGet, "/runtime", "sk-"+token.Key, nil)
		require.Equal(t, http.StatusOK, actual.Code, actual.Body.String())
		actualResponse := decodeAPIResponse(t, actual)
		require.True(t, actualResponse.Success)
		var actualModels []string
		require.NoError(t, common.Unmarshal(actualResponse.Data, &actualModels))
		wantModels := map[string][]string{"privateA": {"gpt-4o"}, "privateB": {"gpt-4.1"}, "auto": {"gpt-4o", "gpt-4.1"}}
		assert.ElementsMatch(t, wantModels[group], actualModels)
		cachedKey := "token:" + common.GenerateHMAC(token.Key)
		cachedID, err := rdb.HGet(ctx, cachedKey, "Id").Int()
		require.NoError(t, err)
		assert.Equal(t, token.Id, cachedID, "runtime must preheat a complete Redis token hash")
	}
	preview := request(http.MethodPost, "/api/token/models", enterpriseJWT, tokenModelScopeRequest{Group: "auto", AutoGroupsMode: "custom", AutoGroups: []string{"privateA", "privateB"}})
	var models []string
	response := decodeAPIResponse(t, preview)
	require.True(t, response.Success)
	require.NoError(t, common.Unmarshal(response.Data, &models))
	assert.ElementsMatch(t, []string{"gpt-4o", "gpt-4.1"}, models)
	// Observe the actual versioned invalidation, not just changed database rows.
	pubsub := rdb.Subscribe(ctx, fmt.Sprintf("new-api:group-config:invalidate:db:%d", options.DB))
	defer pubsub.Close()
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = pubsub.Receive(deadline)
	require.NoError(t, err)
	revoked := request(http.MethodPost, "/api/option/group-rename", rootJWT, map[string]any{"options": map[string]string{specialKey: `{"enterprise":{"+:privateB":"B"}}`}})
	require.True(t, decodeAPIResponse(t, revoked).Success, revoked.Body.String())
	event, err := pubsub.ReceiveMessage(deadline)
	require.NoError(t, err)
	var invalidation struct {
		Version  int64
		Mappings []model.GroupRenameMapping
	}
	require.NoError(t, common.UnmarshalJsonStr(event.Payload, &invalidation))
	assert.Positive(t, invalidation.Version)
	assert.Empty(t, invalidation.Mappings)
	for _, group := range []string{"privateA", "auto"} {
		token := tokens[group]
		cachedGroup, err := rdb.HGet(ctx, "token:"+common.GenerateHMAC(token.Key), "Group").Result()
		require.NoError(t, err)
		assert.Equal(t, group, cachedGroup)
		denied = request(http.MethodGet, "/runtime", "sk-"+token.Key, nil)
		assert.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
		assert.NotContains(t, denied.Body.String(), "gpt-")
		preview = request(http.MethodPost, "/api/token/models", enterpriseJWT, tokenModelScopeRequest{Group: group, AutoGroupsMode: "custom", AutoGroups: []string{"privateA", "privateB"}})
		response = decodeAPIResponse(t, preview)
		require.True(t, response.Success)
		require.NoError(t, common.Unmarshal(response.Data, &models))
		assert.Empty(t, models)
		body := baseAutoTokenRequest("must-not-update-after-revoke")
		body["id"], body["group"] = token.Id, token.Group
		rejectedUpdate := request(http.MethodPut, "/api/token/", enterpriseJWT, body)
		require.False(t, decodeAPIResponse(t, rejectedUpdate).Success)
		var stored model.Token
		require.NoError(t, db.First(&stored, token.Id).Error)
		assert.Equal(t, token.Name, stored.Name)
	}
	// A still-granted group stays usable; revocation must not hide legal models.
	allowed := request(http.MethodGet, "/runtime", "sk-"+tokens["privateB"].Key, nil)
	require.Equal(t, http.StatusOK, allowed.Code, allowed.Body.String())
	response = decodeAPIResponse(t, allowed)
	require.NoError(t, common.Unmarshal(response.Data, &models))
	assert.Equal(t, []string{"gpt-4.1"}, models)
	removal := request(http.MethodPost, "/api/option/group-rename", rootJWT, map[string]any{"options": map[string]string{"GroupRatio": `{"default":1,"enterprise":1,"privateA":1}`}})
	assert.Equal(t, http.StatusBadRequest, removal.Code, removal.Body.String())
	assert.Contains(t, removal.Body.String(), "GROUP_RENAME_REFERENCED")
	option = model.Option{}
	require.NoError(t, db.Where(&model.Option{Key: "GroupRatio"}).First(&option).Error)
	assert.JSONEq(t, definitions, option.Value)
}
