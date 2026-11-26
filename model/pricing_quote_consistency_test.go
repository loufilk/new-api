package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestPricingCatalogueModifierUsesSameConfiguredQuote(t *testing.T) {
	resetPricingEndpointTestTables(t)
	oldPrices, oldRatios := ratio_setting.ModelPrice2JSONString(), ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios))
	})
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"audit-fixed":0.625}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"audit-text":1.75,"audit-text@thinking:on":2}`))
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(k, v string) error { saved[k] = v; return nil }))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(saved)) })
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"audit-expr":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"audit-expr":"p * 2 + c * 6"}`,
	}))
	insertPricingEndpointChannel(t, 901, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	for _, name := range []string{"audit-fixed@temperature:0.2", "audit-text@temperature:0.5", "audit-text@effort:high", "audit-expr@temperature:0.9"} {
		insertPricingEndpointAbility(t, 901, name)
	}
	InitChannelCache()
	rows := pricingByModel(GetPricing())
	fixed := rows["audit-fixed@temperature:0.2"]
	require.Equal(t, 1, fixed.QuotaType)
	require.Equal(t, 0.625, fixed.ModelPrice)
	require.Equal(t, 1.75, rows["audit-text@temperature:0.5"].ModelRatio)
	require.Equal(t, 2.0, rows["audit-text@effort:high"].ModelRatio)
	require.Equal(t, "tiered_expr", rows["audit-expr@temperature:0.9"].BillingMode)
	require.Equal(t, "p * 2 + c * 6", rows["audit-expr@temperature:0.9"].BillingExpr)
	for name, row := range rows {
		require.Equal(t, common.GetPointer(true), row.OrdinaryPriceAvailable, name)
	}
}

func TestPricingCatalogueOrdinaryQuoteAvailabilityByScope(t *testing.T) {
	resetPricingEndpointTestTables(t)
	saved := config.GlobalConfig.ExportAllConfigs()
	savedPrices, savedRatios := ratio_setting.ModelPrice2JSONString(), ratio_setting.ModelRatio2JSONString()
	savedSelfUse := operation_setting.SelfUseModeEnabled
	t.Cleanup(func() {
		operation_setting.SelfUseModeEnabled = savedSelfUse
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatios))
	})
	operation_setting.SelfUseModeEnabled = true
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"test-zero-price":0,"test-invalid-expr":0.1,"test-scoped-task":0.3}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-explicit-37":37.5}`))
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":        `{"test-invalid-expr":"tiered_expr","test-task-usage":"tiered_expr","test-standalone-task":"tiered_expr"}`,
		"billing_setting.billing_expr":        `{"test-invalid-expr":"p +","test-task-usage":"u(\"seconds\")","test-standalone-task":"u(\"seconds\")"}`,
		"billing_setting.plugin_billing_expr": `{"audit-quote-scope::test-plugin-only":"2","audit-quote-scope::test-scoped-task":"3"}`,
	}))
	_, err := jsplugin.DefaultRegistry.Register(`
export const meta = {
  apiVersion:1,key:"audit-quote-scope",name:"Quote scope fixture",version:"1.0.0",author:{name:"Test"},
  models:["test-plugin-only","test-scoped-task","test-standalone-task"],fetchMode:"per_task",
  usageSchema:{seconds:{type:"number",unit:"second"}}
};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("audit-quote-scope") })

	insertPricingEndpointChannel(t, 903, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	for _, name := range []string{"test-zero-price", "test-unconfigured", "test-explicit-37", "test-invalid-expr", "test-task-usage"} {
		insertPricingEndpointAbility(t, 903, name)
	}
	require.NoError(t, DB.Create(&Ability{Group: "ordinary-private", Model: "test-scoped-task", ChannelId: 903, Enabled: true}).Error)
	insertPricingEndpointChannel(t, 904, constant.ChannelTypeTaskPlugin, dto.ChannelOtherSettings{})
	for _, name := range []string{"test-plugin-only", "test-standalone-task", "test-scoped-task"} {
		insertPricingEndpointAbility(t, 904, name)
	}
	InitChannelCache()
	snapshot := GetPricingCatalogSnapshot()
	public := pricingByModel(FilterPricingByUsableGroups(snapshot.Rows, map[string]string{"default": "Public"}))
	private := pricingByModel(FilterPricingByUsableGroups(snapshot.Rows, map[string]string{"ordinary-private": "Private"}))
	for _, name := range []string{"test-unconfigured", "test-invalid-expr", "test-task-usage", "test-plugin-only", "test-scoped-task"} {
		require.Equal(t, common.GetPointer(false), public[name].OrdinaryPriceAvailable, name)
	}
	require.Equal(t, common.GetPointer(true), public["test-zero-price"].OrdinaryPriceAvailable)
	require.Equal(t, common.GetPointer(true), public["test-explicit-37"].OrdinaryPriceAvailable)
	require.Equal(t, public["test-unconfigured"].ModelRatio, public["test-explicit-37"].ModelRatio, "real rates cannot be inferred from the fallback number")
	require.Nil(t, public["test-standalone-task"].OrdinaryPriceAvailable, "leave the original standalone task-price contract intact")
	require.Equal(t, `u("seconds")`, public["test-standalone-task"].BillingExpr)
	require.Equal(t, common.GetPointer(true), private["test-scoped-task"].OrdinaryPriceAvailable)
	require.Equal(t, common.GetPointer(true), pricingByModel(snapshot.Rows)["test-scoped-task"].OrdinaryPriceAvailable, "viewer filtering must not mutate the cache")
}

func TestPricingCatalogueSnapshotRetainsOneGeneration(t *testing.T) {
	resetPricingEndpointTestTables(t)
	insertPricingEndpointChannel(t, 902, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 902, "audit-snapshot")
	const vendorID = 1902
	require.NoError(t, DB.Create(&Vendor{Id: vendorID, Name: "audit-old", Status: 1}).Error)
	require.NoError(t, DB.Create(&Model{ModelName: "audit-snapshot", Description: "old", VendorID: vendorID, NameRule: NameRuleExact, Status: 1, Endpoints: `{"audit-protocol":"/v1/old"}`}).Error)
	InitChannelCache()
	old := GetPricingCatalogSnapshot()
	require.NoError(t, DB.Model(&Vendor{}).Where("id = ?", vendorID).Update("name", "audit-new").Error)
	require.NoError(t, DB.Model(&Model{}).Where("model_name = ?", "audit-snapshot").Updates(map[string]any{"description": "new", "endpoints": `{"audit-protocol":"/v1/new"}`}).Error)
	RefreshPricing()
	current := GetPricingCatalogSnapshot()
	for _, tc := range []struct {
		snapshot PricingCatalogSnapshot
		version  string
	}{{old, "old"}, {current, "new"}} {
		rows := pricingByModel(tc.snapshot.Rows)
		row, ok := rows["audit-snapshot"]
		require.True(t, ok)
		require.Equal(t, tc.version, row.Description)
		require.Equal(t, vendorID, row.VendorID)
		found := false
		for _, v := range tc.snapshot.Vendors {
			if v.ID == row.VendorID {
				require.Equal(t, "audit-"+tc.version, v.Name)
				found = true
				break
			}
		}
		require.True(t, found)
		require.Equal(t, "/v1/"+tc.version, tc.snapshot.Endpoints["audit-protocol"].Path)
	}
}
