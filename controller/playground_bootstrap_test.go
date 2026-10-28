package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type playgroundBootstrapResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Models        []string            `json:"models"`
		ModelsByGroup map[string][]string `json:"models_by_group"`
	} `json:"data"`
}

func decodePlaygroundBootstrap(t *testing.T, recorder *httptest.ResponseRecorder) playgroundBootstrapResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code)
	var payload playgroundBootstrapResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	return payload
}

func withUsableGroupsAndRatios(t *testing.T, groupsJSON string) {
	t.Helper()
	originalUsable := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(groupsJSON))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})
}

func TestPlaygroundBootstrapOnlyReturnsPricingCatalogModelsPerUsableGroup(t *testing.T) {
	withUsableGroupsAndRatios(t, `{"default":"默认分组","vip":"VIP 分组"}`)

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.Channel{
		Id:     901,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "playground-channel-key",
		Status: common.ChannelStatusEnabled,
		Name:   "playground-channel",
	}).Error)
	require.NoError(t, db.Create(&model.User{
		Id:       9001,
		Username: "playground-bootstrap-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)

	// default 分组：一个在定价目录内、一个元数据被禁用（须排除）、一个能力被禁用（须排除）
	// vip 分组：仅 vip 可路由（须保留在 vip 分组内）
	// xenon 分组：非当前用户可用分组（任何地方都不允许出现）
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-pg-catalog-model", ChannelId: 901, Enabled: true},
		{Group: "default", Model: "zz-pg-disabled-meta-model", ChannelId: 901, Enabled: true},
		{Group: "default", Model: "zz-pg-disabled-ability-model", ChannelId: 901, Enabled: false},
		{Group: "vip", Model: "zz-pg-vip-only-model", ChannelId: 901, Enabled: true},
		{Group: "xenon", Model: "zz-pg-other-group-model", ChannelId: 901, Enabled: true},
	}).Error)
	require.NoError(t, db.Create(&model.Model{
		ModelName: "zz-pg-catalog-model",
		Status:    1,
	}).Error)
	// Insert() 保留 status 零值（raw Create 会被 gorm default:1 覆盖）
	require.NoError(t, (&model.Model{
		ModelName: "zz-pg-disabled-meta-model",
		Status:    0,
	}).Insert())

	model.InvalidatePricingCache()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/playground/bootstrap", nil)
	ctx.Set("id", 9001)

	PlaygroundBootstrap(ctx)

	payload := decodePlaygroundBootstrap(t, recorder)
	require.Equal(t, []string{"zz-pg-catalog-model", "zz-pg-vip-only-model"}, payload.Data.Models)
	require.Equal(t, []string{"zz-pg-catalog-model"}, payload.Data.ModelsByGroup["default"])
	require.Equal(t, []string{"zz-pg-vip-only-model"}, payload.Data.ModelsByGroup["vip"])

	// 定价目录外的模型（元数据被禁用）与不可用分组模型都不泄露
	assert.NotContains(t, payload.Data.Models, "zz-pg-disabled-meta-model")
	assert.NotContains(t, payload.Data.Models, "zz-pg-disabled-ability-model")
	assert.NotContains(t, payload.Data.Models, "zz-pg-other-group-model")
	assert.NotContains(t, payload.Data.ModelsByGroup["vip"], "zz-pg-catalog-model")
	assert.Empty(t, payload.Data.ModelsByGroup["xenon"])
}

func TestPlaygroundBootstrapDeduplicatesAndSortsModelLists(t *testing.T) {
	withUsableGroupsAndRatios(t, `{"default":"默认分组"}`)

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.Channel{
		Id:     902,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "playground-channel-key-2",
		Status: common.ChannelStatusEnabled,
		Name:   "playground-channel-2",
	}).Error)
	require.NoError(t, db.Create(&model.User{
		Id:       9002,
		Username: "playground-bootstrap-user-2",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)

	// 同一模型通过两个渠道启用：分组列表与全局列表都必须去重
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-pg-zeta-model", ChannelId: 902, Enabled: true},
		{Group: "default", Model: "zz-pg-zeta-model", ChannelId: 903, Enabled: true},
		{Group: "default", Model: "zz-pg-alpha-model", ChannelId: 902, Enabled: true},
	}).Error)

	model.InvalidatePricingCache()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/playground/bootstrap", nil)
	ctx.Set("id", 9002)

	PlaygroundBootstrap(ctx)

	payload := decodePlaygroundBootstrap(t, recorder)
	// 去重 + 排序
	require.Equal(t, []string{"zz-pg-alpha-model", "zz-pg-zeta-model"}, payload.Data.Models)
	require.Equal(t, []string{"zz-pg-alpha-model", "zz-pg-zeta-model"}, payload.Data.ModelsByGroup["default"])
	require.Equal(t, fmt.Sprintf("%v", payload.Data.Models), fmt.Sprintf("%v", payload.Data.ModelsByGroup["default"]))
}
