package console

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type publicPricingPayload struct {
	Success bool `json:"success"`
	Data    []struct {
		ModelName   string   `json:"model_name"`
		EnableGroup []string `json:"enable_groups"`
	} `json:"data"`
	GroupRatio     map[string]float64 `json:"group_ratio"`
	UsableGroup    map[string]string  `json:"usable_group"`
	AutoGroups     []string           `json:"auto_groups"`
	PricingVersion string             `json:"pricing_version"`
}

func performPublicPricingRequest(t *testing.T, decorate func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/public-api/pricing", nil)
	if decorate != nil {
		decorate(context)
	}
	GetPublicPricing(context)
	require.Equal(t, http.StatusOK, recorder.Code)
	return recorder
}

func TestAnonymousCatalogsProjectOnlyPublicModelFields(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis := common.RedisEnabled
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	db, err := gorm.Open(sqlite.Open("file:console-public-pricing?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}))
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedis
		common.SetDatabaseTypes(previousMainType, previousLogType)
		model.InvalidatePricingCache()
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalGroupRatios := ratio_setting.GroupRatio2JSONString()
	originalAutoGroups := setting.AutoGroups2JsonString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","unpriced":"Missing ratio"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"private":9}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","private"]`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
	})

	require.NoError(t, db.Create(&model.Channel{
		Id: 801, Type: constant.ChannelTypeOpenAI, Key: "fixture-key",
		Status: common.ChannelStatusEnabled, Name: "fixture-channel",
		Models: "public-model,private-model,stealth/pixel-canary,unpriced-model", Group: "default,private,unpriced",
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "public-model", ChannelId: 801, Enabled: true},
		{Group: "private", Model: "public-model", ChannelId: 801, Enabled: true},
		{Group: "default", Model: "stealth/pixel-canary", ChannelId: 801, Enabled: true},
		{Group: "unpriced", Model: "unpriced-model", ChannelId: 801, Enabled: true},
		{Group: "private", Model: "private-model", ChannelId: 801, Enabled: true},
	}).Error)
	require.NoError(t, db.Create(&model.Model{ModelName: "public-model", Description: "private-supplier-note", Status: 1}).Error)
	model.InvalidatePricingCache()

	anonymous := performPublicPricingRequest(t, nil)
	var payload publicPricingPayload
	require.NoError(t, common.Unmarshal(anonymous.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Len(t, payload.Data, 1, "only public-group models may appear in the anonymous catalogue")
	assert.Equal(t, "public-model", payload.Data[0].ModelName)
	assert.Equal(t, []string{"rate-1"}, payload.Data[0].EnableGroup)
	assert.Equal(t, map[string]float64{"rate-1": 1}, payload.GroupRatio)
	assert.Equal(t, map[string]string{"rate-1": "价格档位 ×1"}, payload.UsableGroup)
	assert.Equal(t, []string{"rate-1"}, payload.AutoGroups)
	assert.Equal(t, pricingVersion, payload.PricingVersion)
	assert.NotContains(t, anonymous.Body.String(), "private")

	forged := performPublicPricingRequest(t, func(c *gin.Context) { c.Set("id", 4242) })
	assert.Equal(t, anonymous.Body.String(), forged.Body.String(),
		"an identity in the context must not personalize the anonymous catalogue")

	llmsRecorder := httptest.NewRecorder()
	llmsContext, _ := gin.CreateTestContext(llmsRecorder)
	llmsContext.Request = httptest.NewRequest(http.MethodGet, "/llms-full.txt", nil)
	GetLlmsFull(llmsContext)
	require.Equal(t, http.StatusOK, llmsRecorder.Code)
	assert.Contains(t, llmsRecorder.Body.String(), "public-model")
	assert.Contains(t, llmsRecorder.Body.String(), "rate-1")
	for _, internal := range []string{"private", "stealth/pixel-canary", "unpriced-model", "default"} {
		assert.NotContains(t, llmsRecorder.Body.String(), internal,
			"the text catalogue must use the same public projection as pricing")
	}
}

func TestPublicPricingRowsHideRoutingAndNotesWithoutMutatingSource(t *testing.T) {
	rows := []model.Pricing{{ModelName: "public-model", Description: "private-supplier", Tags: "internal-note", OwnerBy: "private-owner", EnableGroup: []string{"supplier-a", "supplier-b"}, BillingMode: "tiered_expr", BillingExpr: `tier("standard", p * 2 + c * 6)`}}
	public := publicPricingRows(rows, map[string]string{"supplier-a": "rate-1", "supplier-b": "rate-1"})
	require.Len(t, public, 1)
	payload, err := common.Marshal(public)
	require.NoError(t, err)
	for _, internal := range []string{"supplier-a", "supplier-b", "private-supplier", "private-owner", "internal-note"} {
		assert.NotContains(t, string(payload), internal)
	}
	assert.Equal(t, []string{"rate-1"}, public[0].EnableGroup)
	assert.Equal(t, rows[0].BillingExpr, public[0].BillingExpr)
	assert.Equal(t, []string{"supplier-a", "supplier-b"}, rows[0].EnableGroup)
	assert.Empty(t, publicPricingRows(rows, map[string]string{}))
}

func TestPublicPricingRowsPreserveDisplayCategoriesWithoutChangingCapabilities(t *testing.T) {
	rows := []model.Pricing{{ModelName: "synthetic-display-model", EnableGroup: []string{"private-route"},
		Description: "private-note", OutputModalities: []string{"text"}, Capabilities: []string{"reasoning"},
		SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeOpenAI},
		DisplayCategories:      model.ModelDisplayCategories{"images", "video"}}}
	public := publicPricingRows(rows, map[string]string{"private-route": "rate-1"})
	require.Len(t, public, 1)
	assert.Equal(t, rows[0].DisplayCategories, public[0].DisplayCategories)
	assert.Equal(t, rows[0].OutputModalities, public[0].OutputModalities)
	assert.Equal(t, rows[0].Capabilities, public[0].Capabilities)
	assert.Equal(t, rows[0].SupportedEndpointTypes, public[0].SupportedEndpointTypes)
	encoded, err := common.Marshal(public)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"display_categories":["images","video"]`)
	assert.NotContains(t, string(encoded), "private-route")
	assert.NotContains(t, string(encoded), "private-note")
	rows[0].DisplayCategories = nil
	public = publicPricingRows(rows, map[string]string{"private-route": "rate-1"})
	encoded, err = common.Marshal(public)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "display_categories", "legacy automatic records retain their original response shape")
}
