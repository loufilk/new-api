package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 推荐返利重新启用后受“每位受邀用户前三次成功充值”上限保护，选项必须可以
// 正常开启与关闭：UpdateOption 需同时更新数据库、内存开关与 OptionMap。
func TestAffiliateRewardEnabledOptionUpdatesBothDirections(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Option{}))

	originalMap := common.OptionMap
	originalEnabled := common.AffiliateRewardEnabled
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		common.AffiliateRewardEnabled = originalEnabled
		common.OptionMap = originalMap
		DB.Where(&Option{Key: "AffiliateRewardEnabled"}).Delete(&Option{})
	})

	require.NoError(t, UpdateOption("AffiliateRewardEnabled", "true"))
	assert.True(t, common.AffiliateRewardEnabled)
	assert.Equal(t, "true", common.OptionMap["AffiliateRewardEnabled"])

	var option Option
	require.NoError(t, DB.Where(&Option{Key: "AffiliateRewardEnabled"}).First(&option).Error)
	assert.Equal(t, "true", option.Value)

	require.NoError(t, UpdateOption("AffiliateRewardEnabled", "false"))
	assert.False(t, common.AffiliateRewardEnabled)
	assert.Equal(t, "false", common.OptionMap["AffiliateRewardEnabled"])
}
