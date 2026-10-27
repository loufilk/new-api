package controller

import (
	"net/http"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelMetadataPresencePostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN for the PostgreSQL metadata presence contract")
	}
	db := modelManagementDB(t, "postgres", dsn)
	original := model.Model{ModelName: "synthetic-metadata-presence", Status: 1, SyncOfficial: 1,
		Description: "editable", ContextLength: common.GetPointer(128000), MaxOutputTokens: common.GetPointer(8192),
		KnowledgeCutoff: "2025-01", ReleaseDate: "2025-06", ParameterCount: "8B",
		InputModalities: `["text","image"]`, OutputModalities: `["text"]`, Capabilities: `["vision"]`,
		BonusEligible: common.GetPointer(false), DisplayCategories: model.ModelDisplayCategories{"decision"},
	}
	require.NoError(t, db.Create(&original).Error)
	legacy := map[string]any{"id": original.Id, "model_name": original.ModelName, "description": "edited", "status": 1, "sync_official": 1}
	var response struct {
		Success bool
		Data    model.Model
		Message string
	}
	modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", legacy, &response)
	require.True(t, response.Success, response.Message)
	var loaded model.Model
	require.NoError(t, db.First(&loaded, original.Id).Error)
	want := original
	want.Description = "edited"
	want.UpdatedTime = loaded.UpdatedTime
	assert.Equal(t, want, loaded, "legacy clients cannot erase omitted catalog facts or bonus eligibility")
	assert.Equal(t, original.ContextLength, response.Data.ContextLength)
	assert.Equal(t, original.BonusEligible, response.Data.BonusEligible)
	// A stale editor that omits catalog facts must preserve a newer official sync.
	_, err := model.ApplyMetadataSync([]model.MetadataSyncUpdate{{MetadataSyncSelection: model.MetadataSyncSelection{
		ModelName: original.ModelName, RecordVersion: model.MetadataRecordVersion(&loaded, nil, nil),
		Fields: []string{"context_length", "capabilities", "bonus_eligible"},
	}, Values: model.MetadataValues{ContextLength: common.GetPointer(256000), Capabilities: []string{"reasoning"}, BonusEligible: common.GetPointer(true)}}}, nil)
	require.NoError(t, err)
	legacy["description"] = "edited after sync"
	modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", legacy, &response)
	require.True(t, response.Success, response.Message)
	loaded = model.Model{}
	require.NoError(t, db.First(&loaded, original.Id).Error)
	assert.Equal(t, common.GetPointer(256000), loaded.ContextLength)
	assert.Equal(t, `["reasoning"]`, loaded.Capabilities)
	assert.Equal(t, common.GetPointer(true), loaded.BonusEligible)
	assert.Equal(t, original.DisplayCategories, loaded.DisplayCategories)
	// false, null and omission are distinct. Explicit clears remain supported.
	for _, value := range []any{true, false, nil} {
		legacy["bonus_eligible"] = value
		modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", legacy, &response)
		require.True(t, response.Success, response.Message)
		loaded = model.Model{}
		require.NoError(t, db.First(&loaded, original.Id).Error)
		if value == nil {
			assert.Nil(t, loaded.BonusEligible)
		} else {
			require.NotNil(t, loaded.BonusEligible)
			assert.Equal(t, value, *loaded.BonusEligible)
		}
	}
	for _, key := range []string{"context_length", "max_output_tokens", "knowledge_cutoff", "release_date", "parameter_count", "input_modalities", "output_modalities", "capabilities"} {
		legacy[key] = nil
	}
	legacy["display_categories"] = []string{}
	modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", legacy, &response)
	require.True(t, response.Success, response.Message)
	loaded = model.Model{}
	require.NoError(t, db.First(&loaded, original.Id).Error)
	assert.Nil(t, loaded.ContextLength)
	assert.Nil(t, loaded.MaxOutputTokens)
	assert.Nil(t, loaded.BonusEligible)
	assert.Empty(t, loaded.KnowledgeCutoff)
	assert.Empty(t, loaded.ReleaseDate)
	assert.Empty(t, loaded.ParameterCount)
	assert.Empty(t, loaded.InputModalities)
	assert.Empty(t, loaded.OutputModalities)
	assert.Empty(t, loaded.Capabilities)
	assert.Empty(t, loaded.DisplayCategories)
	legacy["context_length"] = 0
	legacy["max_output_tokens"] = 0
	legacy["bonus_eligible"] = false
	modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", legacy, &response)
	require.True(t, response.Success, response.Message)
	loaded = model.Model{}
	require.NoError(t, db.First(&loaded, original.Id).Error)
	require.NotNil(t, loaded.ContextLength)
	assert.Zero(t, *loaded.ContextLength)
	require.NotNil(t, loaded.MaxOutputTokens)
	assert.Zero(t, *loaded.MaxOutputTokens)
	require.NotNil(t, loaded.BonusEligible)
	assert.False(t, *loaded.BonusEligible)
}
