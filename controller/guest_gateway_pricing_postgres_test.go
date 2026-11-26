package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	chuanconsole "github.com/QuantumNous/new-api/modules/chuanapi/console"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// A transport can forward multiple formats without the upstream model accepting
// them. Exercise the HTTP handlers, including an exclusive private source, two
// public groups which share a tier and a private path override for a common kind.
func TestGatewayMarketplaceProtocolsAndPathsPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable loopback PostgreSQL fixture")
	}
	db := modelManagementDB(t, "postgres", dsn)
	require.NoError(t, db.AutoMigrate(&model.UserGroupEntitlement{}, &model.UserGroupPricing{}, &model.QuotaData{}))
	withUsableGroupsAndRatios(t, `{"default":"Default","another-public":"Another public group","customer-secret":"Private group"}`)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"another-public":1,"customer-secret":0.012345}`))
	require.NoError(t, db.Create(&model.Option{Key: "GroupCatalogMetadata", Value: `{"customer-secret":{"exclusive":true,"label":"private customer label"}}`}).Error)
	require.NoError(t, model.LoadOptionsFromDatabase())
	t.Cleanup(func() {
		require.NoError(t, db.Where("key = ?", "GroupCatalogMetadata").Delete(&model.Option{}).Error)
		require.NoError(t, model.LoadOptionsFromDatabase())
		model.InvalidatePricingCache()
	})
	const unknown, explicit, mixed, hidden = "gemini-gateway-unconfirmed", "aliased-upstream-model", "mixed-scope-model", "private-only-model"
	seedTokenModelsAbility(t, db, "default", unknown, constant.ChannelTypeNewAPI, true)
	seedTokenModelsAbility(t, db, "default", explicit, constant.ChannelTypeSub2API, true)
	seedTokenModelsAbility(t, db, "default", mixed, constant.ChannelTypeNewAPI, true)
	seedTokenModelsAbility(t, db, "another-public", mixed, constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "customer-secret", mixed, constant.ChannelTypeGemini, true)
	seedTokenModelsAbility(t, db, "customer-secret", hidden, constant.ChannelTypeAnthropic, true)
	require.NoError(t, db.Create(&[]model.Model{
		{ModelName: explicit, Status: 1, Endpoints: `[" anthropic ","embeddings","jina-rerank"]`},
		{ModelName: hidden, Status: 1, Endpoints: `{"openai":"/customer-internal/path"}`},
	}).Error)
	require.NoError(t, db.Create(&model.User{Username: "scoped-viewer", AffCode: "scope-gateway", Group: "default", Status: common.UserStatusEnabled}).Error)
	model.InvalidatePricingCache()
	// The anonymous route must never trust a stale formerly authenticated session.
	serve := func(handler gin.HandlerFunc, member bool) ([]model.Pricing, map[string]common.EndpointInfo, string) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		path := "/public-api/pricing"
		if member {
			path = "/api/pricing"
		}
		c.Request = httptest.NewRequest(http.MethodGet, path, nil)
		var user model.User
		require.NoError(t, db.Where("username = ?", "scoped-viewer").First(&user).Error)
		c.Set("id", user.Id)
		handler(c)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var result struct {
			Success bool
			Data    []model.Pricing
			Paths   map[string]common.EndpointInfo `json:"supported_endpoint"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &result))
		require.True(t, result.Success)
		return result.Data, result.Paths, rec.Body.String()
	}
	data, paths, body := serve(chuanconsole.GetPublicPricing, false)
	rows := pricingByModelName(data)
	require.Len(t, rows, 3)
	require.Equal(t, []string{"rate-1"}, rows[unknown].EnableGroup)
	require.Equal(t, []constant.EndpointType{}, rows[unknown].SupportedEndpointTypes)
	require.Equal(t, []constant.EndpointType{}, rows[unknown].SupportedEndpointTypesByGroup["rate-1"])
	require.ElementsMatch(t, []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeEmbeddings},
		rows[explicit].SupportedEndpointTypesByGroup["rate-1"])
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, rows[mixed].SupportedEndpointTypesByGroup["rate-1"])
	require.Equal(t, "/v1/chat/completions", paths["openai"].Path)
	require.Equal(t, "/v1/messages", paths["anthropic"].Path)
	for _, forbidden := range []string{"customer-secret", "private customer label", "customer-internal", "another-public", "0.012345", hidden} {
		require.NotContains(t, body, forbidden)
	}
	// Explicit model docs remain available to signed-in viewers, but the
	// group-scoped availability does not claim unsupported private protocols.
	data, paths, body = serve(GetPricing, true)
	rows = pricingByModelName(data)
	require.Equal(t, []constant.EndpointType{}, rows[mixed].SupportedEndpointTypesByGroup["default"])
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, rows[mixed].SupportedEndpointTypesByGroup["another-public"])
	require.Equal(t, "/v1/chat/completions", paths["openai"].Path)
	require.NotContains(t, body, "customer-internal")
	// Do not change relay, Key authorizations or the underlying adapter formats.
	require.Contains(t, model.GetModelSupportEndpointTypes(unknown), constant.EndpointTypeOpenAIResponseCompact)
	require.Contains(t, model.GetModelSupportEndpointTypes(unknown), constant.EndpointTypeOpenAIAlphaSearch)
}
