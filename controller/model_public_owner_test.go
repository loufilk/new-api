package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicModelsShareOwnerAndVisibility(t *testing.T) {
	owner := setting.GetPublicModelOwnedBy()
	setting.LoadPublicModelOwnedBy("")
	t.Cleanup(func() { setting.LoadPublicModelOwnedBy(owner) })
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "gpt-4o", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "custom-public-model", ChannelId: 1, Enabled: true},
		{Group: "private", Model: "private-model", ChannelId: 2, Enabled: true},
	}).Error)

	contextForModel := func(id string) (*gin.Context, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models/"+id, nil)
		ctx.Params = gin.Params{{Key: "model", Value: id}}
		common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "default")
		return ctx, recorder
	}

	ctx, recorder := contextForModel("")
	ListModels(ctx, constant.ChannelTypeOpenAI)
	list := decodeListModelsPayload(t, recorder)
	require.Len(t, list.Data, 2)
	for _, entry := range list.Data {
		assert.Equal(t, "chuanapi", entry.OwnedBy)
		ctx, recorder = contextForModel(entry.Id)
		RetrieveModel(ctx, constant.ChannelTypeOpenAI)
		require.Equal(t, http.StatusOK, recorder.Code)
		var detail dto.OpenAIModels
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &detail))
		assert.Equal(t, entry, detail)
	}

	for _, hidden := range []string{"private-model", "gpt-4", "missing-model"} {
		ctx, recorder = contextForModel(hidden)
		RetrieveModel(ctx, constant.ChannelTypeOpenAI)
		assert.Equal(t, http.StatusNotFound, recorder.Code, hidden)
		assert.Contains(t, recorder.Body.String(), "model_not_found")
		assert.NotContains(t, recorder.Body.String(), "owned_by")
	}

	ctx, recorder = contextForModel("gpt-4o")
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{"custom-public-model": true})
	RetrieveModel(ctx, constant.ChannelTypeOpenAI)
	assert.Equal(t, http.StatusNotFound, recorder.Code)

	setting.LoadPublicModelOwnedBy("  Public brand  ")
	ctx, recorder = contextForModel("")
	ListModels(ctx, constant.ChannelTypeOpenAI)
	for _, entry := range decodeListModelsPayload(t, recorder).Data {
		assert.Equal(t, "Public brand", entry.OwnedBy)
		ctx, recorder = contextForModel(entry.Id)
		RetrieveModel(ctx, constant.ChannelTypeOpenAI)
		assert.Contains(t, recorder.Body.String(), `"owned_by":"Public brand"`)
	}
	setting.LoadPublicModelOwnedBy("bad\u202eowner")
	ctx, recorder = contextForModel("gpt-4o")
	RetrieveModel(ctx, constant.ChannelTypeOpenAI)
	assert.Contains(t, recorder.Body.String(), `"owned_by":"chuanapi"`)
}

func TestPublicModelDetailPreservesNativeSchemasAndBillingVisibility(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t, map[string]string{"priced-public-model": "tiered_expr"}, map[string]string{"priced-public-model": `tier("request", fixed(0.01))`})
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "priced-public-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "unpriced-public-model", ChannelId: 1, Enabled: true},
	}).Error)
	for _, modelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeAnthropic, constant.ChannelTypeGemini} {
		for _, modelName := range []string{"priced-public-model", "unpriced-public-model"} {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models/"+modelName, nil)
			ctx.Params = gin.Params{{Key: "model", Value: modelName}}
			common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "default")
			RetrieveModel(ctx, modelType)
			if modelName == "unpriced-public-model" {
				assert.Equal(t, http.StatusNotFound, recorder.Code)
				continue
			}
			require.Equal(t, http.StatusOK, recorder.Code)
			var body map[string]any
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
			switch modelType {
			case constant.ChannelTypeAnthropic:
				assert.Equal(t, "model", body["type"])
				assert.Equal(t, modelName, body["display_name"])
				assert.Contains(t, body, "created_at")
				assert.NotContains(t, body, "owned_by")
			case constant.ChannelTypeGemini:
				assert.Equal(t, modelName, body["name"])
				assert.Equal(t, modelName, body["displayName"])
				assert.NotContains(t, body, "owned_by")
			default:
				assert.Equal(t, modelName, body["id"])
				assert.Contains(t, body, "owned_by")
			}
		}
	}
}
