package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(value int) *int {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func findPricingByName(t *testing.T, pricings []Pricing, modelName string) Pricing {
	t.Helper()
	for _, p := range pricings {
		if p.ModelName == modelName {
			return p
		}
	}
	require.FailNowf(t, "pricing entry not found", "model %q missing from pricing catalog", modelName)
	return Pricing{}
}

func TestModelMetadataInsertPersistsAndUpdateClears(t *testing.T) {
	resetPricingEndpointTestTables(t)
	require.NoError(t, DB.AutoMigrate(&Option{}))

	mi := &Model{
		ModelName:       "metadata-store-model",
		Status:          1,
		ContextLength:   intPtr(128000),
		MaxOutputTokens: intPtr(16384),
		KnowledgeCutoff: "2024-10",
		ReleaseDate:     "2024-04",
		ParameterCount:  "8B",
		InputModalities: `["text","image"]`,
		Capabilities:    `["vision","function_calling"]`,
		BonusEligible:   boolPtr(true),
	}
	require.NoError(t, mi.Insert())

	var stored Model
	require.NoError(t, DB.First(&stored, "model_name = ?", mi.ModelName).Error)
	require.NotNil(t, stored.ContextLength)
	assert.Equal(t, 128000, *stored.ContextLength)
	require.NotNil(t, stored.MaxOutputTokens)
	assert.Equal(t, 16384, *stored.MaxOutputTokens)
	assert.Equal(t, "2024-10", stored.KnowledgeCutoff)
	assert.Equal(t, "2024-04", stored.ReleaseDate)
	assert.Equal(t, "8B", stored.ParameterCount)
	assert.Equal(t, `["text","image"]`, stored.InputModalities)
	assert.Equal(t, `["vision","function_calling"]`, stored.Capabilities)
	require.NotNil(t, stored.BonusEligible)
	assert.True(t, *stored.BonusEligible)

	// Update 必须能明确清空：nil 指针落库为 NULL，字符串落库为空串
	mi.ContextLength = nil
	mi.MaxOutputTokens = nil
	mi.KnowledgeCutoff = ""
	mi.ReleaseDate = ""
	mi.ParameterCount = ""
	mi.InputModalities = ""
	mi.OutputModalities = ""
	mi.Capabilities = ""
	mi.BonusEligible = nil
	require.NoError(t, mi.Update())

	var cleared Model
	require.NoError(t, DB.First(&cleared, "model_name = ?", mi.ModelName).Error)
	assert.Nil(t, cleared.ContextLength)
	assert.Nil(t, cleared.MaxOutputTokens)
	assert.Nil(t, cleared.BonusEligible)
	assert.Empty(t, cleared.KnowledgeCutoff)
	assert.Empty(t, cleared.ReleaseDate)
	assert.Empty(t, cleared.ParameterCount)
	assert.Empty(t, cleared.InputModalities)
	assert.Empty(t, cleared.OutputModalities)
	assert.Empty(t, cleared.Capabilities)

	// Update 必须能明确写入零值：显式 false 不能被忽略
	mi.ContextLength = intPtr(0)
	mi.BonusEligible = boolPtr(false)
	require.NoError(t, mi.Update())

	var zeroed Model
	require.NoError(t, DB.First(&zeroed, "model_name = ?", mi.ModelName).Error)
	require.NotNil(t, zeroed.ContextLength)
	assert.Equal(t, 0, *zeroed.ContextLength)
	require.NotNil(t, zeroed.BonusEligible)
	assert.False(t, *zeroed.BonusEligible)
}

func TestMetadataSyncRetainsAbsentFieldsAndAppliesExplicitZeroValues(t *testing.T) {
	resetPricingEndpointTestTables(t)
	require.NoError(t, DB.AutoMigrate(&Option{}))
	local := Model{ModelName: "metadata-sync-optional", Status: 1, SyncOfficial: 1,
		ContextLength: intPtr(128000), Capabilities: `["vision"]`, BonusEligible: boolPtr(true)}
	require.NoError(t, local.Insert())
	fields := []string{"context_length", "capabilities", "bonus_eligible"}
	selection := MetadataSyncSelection{ModelName: local.ModelName, RecordVersion: MetadataRecordVersion(&local, nil, nil), Fields: fields}
	_, err := ApplyMetadataSync([]MetadataSyncUpdate{{MetadataSyncSelection: selection, Values: MetadataValues{Status: 1}}}, nil)
	require.NoError(t, err)
	require.NoError(t, DB.First(&local, local.Id).Error)
	assert.Equal(t, intPtr(128000), local.ContextLength)
	assert.Equal(t, `["vision"]`, local.Capabilities)
	assert.Equal(t, boolPtr(true), local.BonusEligible)
	selection.RecordVersion = MetadataRecordVersion(&local, nil, nil)
	_, err = ApplyMetadataSync([]MetadataSyncUpdate{{MetadataSyncSelection: selection, Values: MetadataValues{
		Status: 1, ContextLength: intPtr(0), Capabilities: []string{}, BonusEligible: boolPtr(false),
	}}}, nil)
	require.NoError(t, err)
	require.NoError(t, DB.First(&local, local.Id).Error)
	assert.Equal(t, intPtr(0), local.ContextLength)
	assert.Equal(t, `[]`, local.Capabilities)
	assert.Equal(t, boolPtr(false), local.BonusEligible)
}

func TestPricingPropagatesModelMetadata(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 501, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 501, "metadata-exact-model")

	require.NoError(t, DB.Create(&Model{
		ModelName:        "metadata-exact-model",
		Status:           1,
		NameRule:         NameRuleExact,
		ContextLength:    intPtr(200000),
		MaxOutputTokens:  intPtr(32768),
		KnowledgeCutoff:  "2025-06",
		ReleaseDate:      "2025-02",
		ParameterCount:   "70B",
		InputModalities:  `["text","image"]`,
		OutputModalities: `["text"]`,
		Capabilities:     `["vision","reasoning"]`,
		BonusEligible:    boolPtr(true),
	}).Error)

	pricing := findPricingByName(t, GetPricing(), "metadata-exact-model")
	require.NotNil(t, pricing.ContextLength)
	assert.Equal(t, 200000, *pricing.ContextLength)
	require.NotNil(t, pricing.MaxOutputTokens)
	assert.Equal(t, 32768, *pricing.MaxOutputTokens)
	assert.Equal(t, "2025-06", pricing.KnowledgeCutoff)
	assert.Equal(t, "2025-02", pricing.ReleaseDate)
	assert.Equal(t, "70B", pricing.ParameterCount)
	assert.Equal(t, []string{"text", "image"}, pricing.InputModalities)
	assert.Equal(t, []string{"text"}, pricing.OutputModalities)
	assert.Equal(t, []string{"vision", "reasoning"}, pricing.Capabilities)
	require.NotNil(t, pricing.BonusEligible)
	assert.True(t, *pricing.BonusEligible)

	// /api/pricing 的 JSON 输出必须包含数组而非字符串，bonus_eligible 必须为 true/false 而非 1/0
	raw, err := common.Marshal(pricing)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"input_modalities":["text","image"]`)
	assert.Contains(t, string(raw), `"capabilities":["vision","reasoning"]`)
	assert.Contains(t, string(raw), `"bonus_eligible":true`)
	assert.NotContains(t, string(raw), `"bonus_eligible":1`)
}

func TestPricingPropagatesMetadataFromRuleMatchedModel(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 502, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 502, "metadata-rule-gpt-4o")

	require.NoError(t, DB.Create(&Model{
		ModelName:       "metadata-rule-",
		Status:          1,
		NameRule:        NameRulePrefix,
		ContextLength:   intPtr(128000),
		InputModalities: `["text","image"]`,
		BonusEligible:   boolPtr(false),
	}).Error)

	pricing := findPricingByName(t, GetPricing(), "metadata-rule-gpt-4o")
	require.NotNil(t, pricing.ContextLength)
	assert.Equal(t, 128000, *pricing.ContextLength)
	assert.Equal(t, []string{"text", "image"}, pricing.InputModalities)
	require.NotNil(t, pricing.BonusEligible)
	assert.False(t, *pricing.BonusEligible)

	// /api/pricing 的 JSON 输出必须为 false 而非 0
	raw, err := common.Marshal(pricing)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"bonus_eligible":false`)
	assert.NotContains(t, string(raw), `"bonus_eligible":0`)
}

func TestPricingBonusEligibleFidelity(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 503, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 503, "bonus-unset-model")
	insertPricingEndpointAbility(t, 503, "bonus-yes-model")
	insertPricingEndpointAbility(t, 503, "bonus-no-model")

	require.NoError(t, DB.Create(&Model{ModelName: "bonus-unset-model", Status: 1}).Error)
	require.NoError(t, DB.Create(&Model{ModelName: "bonus-yes-model", Status: 1, BonusEligible: boolPtr(true)}).Error)
	require.NoError(t, DB.Create(&Model{ModelName: "bonus-no-model", Status: 1, BonusEligible: boolPtr(false)}).Error)

	pricing := GetPricing()

	// nil 表示「尚未配置」，不能被静默变成 false
	unset := findPricingByName(t, pricing, "bonus-unset-model")
	assert.Nil(t, unset.BonusEligible)

	yes := findPricingByName(t, pricing, "bonus-yes-model")
	require.NotNil(t, yes.BonusEligible)
	assert.True(t, *yes.BonusEligible)

	no := findPricingByName(t, pricing, "bonus-no-model")
	require.NotNil(t, no.BonusEligible)
	assert.False(t, *no.BonusEligible)

	// /api/pricing 的 JSON 契约：nil 字段缺失、配置后为 true/false 而非 1/0
	rawUnset, err := common.Marshal(unset)
	require.NoError(t, err)
	assert.NotContains(t, string(rawUnset), "bonus_eligible")

	rawYes, err := common.Marshal(yes)
	require.NoError(t, err)
	assert.Contains(t, string(rawYes), `"bonus_eligible":true`)

	rawNo, err := common.Marshal(no)
	require.NoError(t, err)
	assert.Contains(t, string(rawNo), `"bonus_eligible":false`)
}

func TestPricingDegradesInvalidMetadataArrays(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 504, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 504, "broken-metadata-model")

	require.NoError(t, DB.Create(&Model{
		ModelName:       "broken-metadata-model",
		Status:          1,
		InputModalities: "not-json",
		Capabilities:    "null",
	}).Error)

	pricing := findPricingByName(t, GetPricing(), "broken-metadata-model")
	assert.NotNil(t, pricing.InputModalities)
	assert.Empty(t, pricing.InputModalities)
	assert.NotNil(t, pricing.OutputModalities)
	assert.Empty(t, pricing.OutputModalities)
	assert.NotNil(t, pricing.Capabilities)
	assert.Empty(t, pricing.Capabilities)

	// 无效存量值不导致整个定价接口失败，且输出仍是 JSON 数组
	raw, err := common.Marshal(pricing)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"input_modalities":[]`)
	assert.Contains(t, string(raw), `"capabilities":[]`)
}
