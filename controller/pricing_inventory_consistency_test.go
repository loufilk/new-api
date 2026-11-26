package controller

import (
	"net/http"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	chuanconsole "github.com/QuantumNous/new-api/modules/chuanapi/console"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedLiveInventoryChannels(t *testing.T, db *gorm.DB, ids ...int) {
	t.Helper()
	for _, id := range ids {
		require.NoError(t, db.Create(&model.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Key: "synthetic-inventory-channel", Status: common.ChannelStatusEnabled}).Error)
	}
}

func TestPricingEndpointsDistinguishUnconfiguredAndFreeOrdinaryQuotesPostgres(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" || os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires disposable PostgreSQL fixture")
	}
	db, user := setupTokenModelsControllerTest(t)
	require.NoError(t, db.AutoMigrate(&model.Model{}, &model.Vendor{}, &model.QuotaData{}))
	previousPrices := ratio_setting.ModelPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"inventory-quote-free":0}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
		model.InvalidatePricingCache()
	})
	seedTokenModelsAbility(t, db, "default", "inventory-quote-free", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "default", "inventory-quote-unconfigured", constant.ChannelTypeOpenAI, true)
	model.InvalidatePricingCache()
	for _, spec := range []struct {
		path    string
		userID  int
		handler gin.HandlerFunc
	}{
		{"/api/pricing", user.Id, GetPricing},
		{"/public-api/pricing", 0, chuanconsole.GetPublicPricing},
	} {
		t.Run(spec.path, func(t *testing.T) {
			ctx, recorder := newAuthenticatedContext(t, http.MethodGet, spec.path, nil, spec.userID)
			spec.handler(ctx)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var payload struct {
				Success bool            `json:"success"`
				Data    []model.Pricing `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
			require.True(t, payload.Success, recorder.Body.String())
			require.Len(t, payload.Data, 2, recorder.Body.String())
			byName := make(map[string]model.Pricing)
			for _, row := range payload.Data {
				byName[row.ModelName] = row
			}
			free := byName["inventory-quote-free"]
			require.Equal(t, 1, free.QuotaType)
			require.Zero(t, free.ModelPrice)
			require.Equal(t, common.GetPointer(true), free.OrdinaryPriceAvailable)
			unknown := byName["inventory-quote-unconfigured"]
			require.Equal(t, common.GetPointer(false), unknown.OrdinaryPriceAvailable)
		})
	}
}

func TestPricingAndKeyInventoryUseLiveChannelsAndCanonicalPricePostgres(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" || os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires disposable PostgreSQL fixture")
	}
	db, user := setupTokenModelsControllerTest(t)
	withSelfUseModeDisabled(t)
	require.NoError(t, db.AutoMigrate(&model.Model{}, &model.Vendor{}))
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"inventory-model":2,"inventory-offline":1,"inventory-orphan":1}`))
	const alias = "inventory-model@thinking:off"
	seedTokenModelsAbility(t, db, "a", alias, constant.ChannelTypeOpenAI, true)
	offline := seedTokenModelsAbility(t, db, "a", "inventory-offline", constant.ChannelTypeOpenAI, true)
	require.NoError(t, db.Model(offline).Update("status", common.ChannelStatusManuallyDisabled).Error)
	// A stale ability must not contribute an entire price card or a phantom group
	// for a model that also remains available on a different live channel.
	require.NoError(t, db.Create(&model.Ability{Group: "b", Model: alias, ChannelId: offline.Id, Enabled: true}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "a", Model: "inventory-orphan", ChannelId: 99999, Enabled: true}).Error)

	t.Run("catalogue", func(t *testing.T) {
		rows := model.GetPricing()
		require.Len(t, rows, 1, "catalogue should advertise only a backed, enabled channel")
		require.Equal(t, alias, rows[0].ModelName)
		require.Equal(t, []string{"a"}, rows[0].EnableGroup)

	})
	t.Run("playground groups", func(t *testing.T) {
		prices := map[string]struct{}{alias: {}, "inventory-offline": {}, "inventory-orphan": {}}
		require.Equal(t, []string{alias}, playgroundModelsForGroup("a", prices))
		require.Empty(t, playgroundModelsForGroup("b", prices))
	})
	t.Run("token selector", func(t *testing.T) {
		models, res := requestTokenModelScope(t, user.Id, "a", nil)
		require.True(t, res.Success, res.Message)
		require.Equal(t, []string{alias}, models, "keep the advertised alias; it bills at the configured base model")
	})
	t.Run("owned key", func(t *testing.T) {
		token := seedTokenModelsToken(t, db, user.Id, "inventory-allow", "a", nil, alias)
		models, res := requestTokenRoutableModels(t, token.Id, user.Id)
		require.True(t, res.Success, res.Message)
		require.Equal(t, []string{alias}, models)
	})
}
