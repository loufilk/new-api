package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelTestUnboundedBodyOmitsOutputFields(t *testing.T) {
	var body map[string]any
	require.NoError(t, common.Unmarshal(modelTestBody(modelTestSelection{Model: "synthetic", Group: "default"}, "challenge"), &body))
	assert.Equal(t, "synthetic", body["model"])
	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig"} {
		assert.NotContains(t, body, key)
	}
}

func TestModelTestUnboundedConfirmationRejectsLegacyTickets(t *testing.T) {
	selection := modelTestSelection{Model: "synthetic", Group: "default"}
	ticket := modelTestConfirmation{modelTestSelection: selection, UserID: 17, Version: modelTestVersion, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	_, ok := verifyModelTestConfirmation(signModelTestConfirmation(ticket), 17, selection)
	assert.False(t, ok, "old tickets do not authorize a newly unbounded request")
	ticket.OutputUnbounded = true
	_, ok = verifyModelTestConfirmation(signModelTestConfirmation(ticket), 17, selection)
	assert.True(t, ok)
	ticket.MaxOutputTokens = 2048
	_, ok = verifyModelTestConfirmation(signModelTestConfirmation(ticket), 17, selection)
	assert.False(t, ok, "a capped approval cannot be expanded")
	_, ok = verifyModelTestConfirmation(signModelTestConfirmation(ticket), 17, ticket.modelTestSelection)
	assert.False(t, ok, "new capped requests require a fresh unbounded quote")
}

func TestModelTestUnknownEstimateIsNotZero(t *testing.T) {
	zero, amount, changed := 0, 12, 13
	assert.True(t, modelTestQuotaEstimateMatches(nil, nil))
	assert.False(t, modelTestQuotaEstimateMatches(nil, &zero))
	assert.False(t, modelTestQuotaEstimateMatches(&zero, nil))
	assert.True(t, modelTestQuotaEstimateMatches(&amount, &amount))
	assert.False(t, modelTestQuotaEstimateMatches(&amount, &changed))
}

func TestModelTestPostgresReconcilesHistoricalCappedRun(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	run := &model.ModelTestRun{ID: uuid.NewString(), UserID: userID, Model: "gpt-4o-mini", Group: "default", BenchmarkID: modelTestVersion, Status: "completed", MaxOutputTokens: 2048, CreatedAt: time.Now().Unix()}
	require.NoError(t, db.Create(run).Error)
	body, err := common.Marshal(modelTestRequest{modelTestSelection: modelTestSelection{Model: run.Model, Group: run.Group, MaxOutputTokens: 2048}, Confirmed: true, RequestID: run.ID})
	require.NoError(t, err)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", userID) })
	engine.POST("/runs", PrepareModelTest, func(c *gin.Context) { t.Error("reconciliation must not dispatch") })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(string(body))))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), run.ID)
}

func TestModelTestPostgresUnknownQuoteRequiresExplicitConfirmation(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	oldPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices)) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	var token model.Token
	require.NoError(t, db.Where("user_id = ?", userID).First(&token).Error)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", userID); c.Next() })
	engine.POST("/estimate", EstimateModelTest)
	engine.POST("/runs", PrepareModelTest, func(c *gin.Context) { t.Error("unconfirmed quote dispatched") })
	input, err := common.Marshal(modelTestSelection{TokenID: token.Id, Model: "gpt-4o-mini", Group: "default"})
	require.NoError(t, err)
	quote := httptest.NewRecorder()
	engine.ServeHTTP(quote, httptest.NewRequest(http.MethodPost, "/estimate", strings.NewReader(string(input))))
	require.Equal(t, http.StatusOK, quote.Code, quote.Body.String())
	var response struct {
		Data struct {
			EstimatedQuota    *int   `json:"estimated_quota"`
			CostUnknown       bool   `json:"cost_unknown"`
			OutputUnbounded   bool   `json:"output_unbounded"`
			ConfirmationToken string `json:"confirmation_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(quote.Body.Bytes(), &response))
	assert.Nil(t, response.Data.EstimatedQuota)
	assert.True(t, response.Data.CostUnknown)
	assert.True(t, response.Data.OutputUnbounded)
	require.NotEmpty(t, response.Data.ConfirmationToken)
	request, err := common.Marshal(modelTestRequest{modelTestSelection: modelTestSelection{TokenID: token.Id, Model: "gpt-4o-mini", Group: "default"}, RequestID: uuid.NewString(), ConfirmationToken: response.Data.ConfirmationToken})
	require.NoError(t, err)
	denied := httptest.NewRecorder()
	engine.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(string(request))))
	assert.Equal(t, http.StatusBadRequest, denied.Code)
	assert.Contains(t, denied.Body.String(), "confirmation_required")
}
