package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptionsExposeEffectiveBillingWithoutChangingPrices(t *testing.T) {
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	saved := *settings
	savedOptions := common.OptionMap
	t.Cleanup(func() { *settings = saved; common.OptionMap = savedOptions })
	settings.BillingMode = map[string]string{"custom": "tiered_expr"}
	settings.BillingExpr = map[string]string{"custom": "p * 3 + c * 9"}
	common.OptionMap = map[string]string{"billing_setting.billing_expr": `{"stale":"p * 100"}`, "billing_setting.billing_mode": `{}`}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	GetOptions(ctx)
	require.Equal(t, 200, recorder.Code)
	var response struct {
		Success bool           `json:"success"`
		Data    []model.Option `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	values := map[string]string{}
	for _, option := range response.Data {
		if option.Key == "billing_setting.billing_expr" || option.Key == "billing_setting.billing_mode" {
			_, duplicate := values[option.Key]
			require.False(t, duplicate)
			values[option.Key] = option.Value
		}
	}
	var exprs map[string]string
	require.NoError(t, common.UnmarshalJsonStr(values["billing_setting.billing_expr"], &exprs))
	assert.Equal(t, billing_setting.GetBillingExprCopy(), exprs)
	assert.Equal(t, "p * 3 + c * 9", exprs["custom"])
	assert.NotEmpty(t, exprs["gpt-6-astra"])
	var modes map[string]string
	require.NoError(t, common.UnmarshalJsonStr(values["billing_setting.billing_mode"], &modes))
	assert.Equal(t, billing_setting.GetBillingModeCopy(), modes)
	assert.Equal(t, "tiered_expr", modes["custom"])
	assert.Equal(t, "tiered_expr", modes["gpt-6-astra"])
	assert.Equal(t, `{"stale":"p * 100"}`, common.OptionMap["billing_setting.billing_expr"])
	assert.Equal(t, `{}`, common.OptionMap["billing_setting.billing_mode"])
	assert.Equal(t, map[string]string{"custom": "tiered_expr"}, settings.BillingMode)
	assert.Equal(t, map[string]string{"custom": "p * 3 + c * 9"}, settings.BillingExpr)
}

func TestOptionsExposeBuiltinBillingWithoutPersistingDefaults(t *testing.T) {
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	saved := *settings
	savedOptions := common.OptionMap
	t.Cleanup(func() { *settings = saved; common.OptionMap = savedOptions })
	settings.BillingMode = map[string]string{}
	settings.BillingExpr = map[string]string{}
	common.OptionMap = map[string]string{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	GetOptions(ctx)
	require.Equal(t, 200, recorder.Code)
	var response struct {
		Success bool           `json:"success"`
		Data    []model.Option `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	values := map[string]string{}
	for _, option := range response.Data {
		if option.Key == "billing_setting.billing_expr" || option.Key == "billing_setting.billing_mode" {
			_, duplicate := values[option.Key]
			require.False(t, duplicate)
			values[option.Key] = option.Value
		}
	}
	require.Contains(t, values, "billing_setting.billing_mode")
	require.Contains(t, values, "billing_setting.billing_expr")
	var modes map[string]string
	var expressions map[string]string
	require.NoError(t, common.UnmarshalJsonStr(values["billing_setting.billing_mode"], &modes))
	require.NoError(t, common.UnmarshalJsonStr(values["billing_setting.billing_expr"], &expressions))
	assert.Equal(t, billing_setting.GetBillingModeCopy(), modes)
	assert.Equal(t, billing_setting.GetBillingExprCopy(), expressions)
	assert.Equal(t, "tiered_expr", modes["gpt-6-astra"])
	assert.NotEmpty(t, expressions["gpt-6-astra"])
	assert.Empty(t, common.OptionMap)
}
