package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSiteFingerprintsExposeSafeManualResultsAcrossUsers(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.ModelFingerprintSiteConfig{}, &model.ModelFingerprintSiteRun{}))
	run := model.ModelTestRun{ID: "shared-manual-result", Source: "user", UserID: userID, TokenID: 991, RequestID: "private-request", Model: "gpt-fixture", Group: "default", BenchmarkID: modelTestVersion, Status: "completed", Assessment: "scored", FingerprintResult: model.JSONValue(`{"status":"scored","used_outputs":3,"api_key":"private-key","samples":["private-body"]}`), SamplesJSON: "private-samples", ArtworkHTML: "private-artwork"}
	require.NoError(t, db.Create(&run).Error)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/model-tests/site", nil)
	c.Set("id", userID+999)
	GetSiteModelFingerprints(c)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var payload struct {
		Data struct {
			UserItems []map[string]any `json:"user_items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.Len(t, payload.Data.UserItems, 1)
	row := payload.Data.UserItems[0]
	assert.Len(t, row, 10)
	assert.Equal(t, run.ID, row["id"])
	assert.Equal(t, "user", row["source"])
	assert.NotContains(t, response.Body.String(), "private-")
	for _, key := range []string{"token_id", "request_id", "user_id", "channel_id", "samples_json", "artwork_html"} {
		assert.NotContains(t, row, key)
	}
	_, err := model.GetModelTestRun(userID+999, run.ID)
	assert.Error(t, err)
}
