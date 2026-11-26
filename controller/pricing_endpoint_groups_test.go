package controller

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPricingProjectsEndpointTypesAcrossLiveAuthorizedGroupsPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires a disposable PostgreSQL fixture")
	}
	withUsableGroupsAndRatios(t, `{"default":"Default"}`)
	db := modelManagementDB(t, "postgres", dsn)
	t.Cleanup(model.InvalidatePricingCache)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"inventory-protocol-model":2}`))
	for _, group := range []string{"default", "vip"} {
		require.NoError(t, db.Create(&model.User{
			Username: "square-" + group, AffCode: "scope-" + group, Group: group, Status: common.UserStatusEnabled,
		}).Error)
	}
	const name = "inventory-protocol-model"
	seedTokenModelsAbility(t, db, "default", name, constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "vip", name, constant.ChannelTypeAnthropic, true)
	inactive := seedTokenModelsAbility(t, db, "default", name, constant.ChannelTypeGemini, true)
	require.NoError(t, db.Model(inactive).Update("status", common.ChannelStatusManuallyDisabled).Error)
	require.NoError(t, db.Create(&model.Model{
		ModelName: name, Status: 1, Endpoints: `{"documentation-only":"/v1/docs-only"}`,
	}).Error)
	model.InvalidatePricingCache()

	ordinary := pricingByModelName(modelSquareData[[]model.Pricing](t, GetPricing, "/api/pricing", "default"))[name]
	assert.Equal(t, map[string][]constant.EndpointType{"default": {constant.EndpointTypeOpenAI}}, ordinary.SupportedEndpointTypesByGroup)
	assert.ElementsMatch(t, []constant.EndpointType{constant.EndpointTypeOpenAI, "documentation-only"}, ordinary.SupportedEndpointTypes)
	member := pricingByModelName(modelSquareData[[]model.Pricing](t, GetPricing, "/api/pricing", "vip"))[name]
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, member.SupportedEndpointTypesByGroup["default"])
	assert.ElementsMatch(t, []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}, member.SupportedEndpointTypesByGroup["vip"])
	assert.NotContains(t, member.SupportedEndpointTypes, constant.EndpointTypeGemini)
	again := pricingByModelName(modelSquareData[[]model.Pricing](t, GetPricing, "/api/pricing", "default"))[name]
	assert.Equal(t, ordinary.SupportedEndpointTypesByGroup, again.SupportedEndpointTypesByGroup)
}
