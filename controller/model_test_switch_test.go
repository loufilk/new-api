package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelTestSwitchPostgresMissingOptionDeniesQuoteAndNewRun(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	require.NoError(t, db.Where(model.Option{Key: "ModelTestEnabled"}).Delete(&model.Option{}).Error)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", userID) })
	engine.POST("/estimate", EstimateModelTest)
	engine.POST("/runs", PrepareModelTest, func(c *gin.Context) { t.Error("disabled test reached execution") })
	for _, request := range []struct{ path, body string }{
		{"/estimate", `{"model":"gpt-4o-mini","group":"default","max_output_tokens":2048}`},
		{"/runs", modelTestRequestBody(t, userID, uuid.NewString())},
	} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, request.path, strings.NewReader(request.body)))
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), "model_tests_disabled")
	}
	var count int64
	require.NoError(t, db.Model(&model.ModelTestRun{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestModelTestSwitchPostgresIssuedConfirmationCannotStartAfterDisable(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", userID) })
	engine.POST("/estimate", EstimateModelTest)
	engine.POST("/runs", PrepareModelTest, func(c *gin.Context) { t.Error("old confirmation reached execution") })
	quote := httptest.NewRecorder()
	engine.ServeHTTP(quote, httptest.NewRequest(http.MethodPost, "/estimate", strings.NewReader(`{"model":"gpt-4o-mini","group":"default","max_output_tokens":2048}`)))
	require.Equal(t, 200, quote.Code, quote.Body.String())
	var response struct {
		Data struct {
			ConfirmationToken string `json:"confirmation_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(quote.Body.Bytes(), &response))
	require.NotEmpty(t, response.Data.ConfirmationToken)
	require.NoError(t, db.Model(&model.Option{}).Where(model.Option{Key: model.ModelTestEnabledKey}).Update("value", "false").Error)
	body, err := common.Marshal(modelTestRequest{modelTestSelection: modelTestSelection{Model: "gpt-4o-mini", Group: "default", MaxOutputTokens: 2048}, ConfirmationToken: response.Data.ConfirmationToken, Confirmed: true, RequestID: uuid.NewString()})
	require.NoError(t, err)
	for range 2 {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body)))
		assert.Equal(t, 403, recorder.Code, recorder.Body.String())
		assert.Contains(t, recorder.Body.String(), "model_tests_disabled")
	}
	var count int64
	require.NoError(t, db.Model(&model.ModelTestRun{}).Count(&count).Error)
	assert.Zero(t, count)
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	assert.Equal(t, 1_000_000, user.QuotaPaid)
}

func TestModelTestSwitchPostgresHistoryCancellationAndReconciliationRemainOwnerOnly(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	run := &model.ModelTestRun{ID: uuid.NewString(), UserID: userID, Model: "gpt-4o-mini", Group: "default", BenchmarkID: modelTestVersion, Status: "running", MaxOutputTokens: 2048, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix()}
	_, err := model.CreateModelTestRun(run)
	require.NoError(t, err)
	replayBody := modelTestRequestBody(t, userID, run.ID)
	require.NoError(t, db.Model(&model.Option{}).Where(model.Option{Key: model.ModelTestEnabledKey}).Update("value", "false").Error)
	for _, owner := range []bool{true, false} {
		engine := gin.New()
		engine.Use(func(c *gin.Context) {
			if owner {
				c.Set("id", userID)
			} else {
				c.Set("id", userID+999)
			}
		})
		engine.GET("/history", GetModelTests)
		engine.GET("/runs/:id", GetModelTest)
		engine.POST("/runs/:id/cancel", CancelModelTest)
		engine.POST("/runs", PrepareModelTest, func(c *gin.Context) { t.Error("reconciliation started new execution") })
		history := httptest.NewRecorder()
		engine.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/history", nil))
		require.Equal(t, 200, history.Code, history.Body.String())
		assert.Contains(t, history.Body.String(), `"enabled":false`)
		assert.Contains(t, history.Body.String(), `"eligible_models_by_group":{}`)
		if owner {
			assert.Contains(t, history.Body.String(), run.ID)
		} else {
			assert.NotContains(t, history.Body.String(), run.ID)
		}
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			path := "/runs/" + run.ID
			if method == http.MethodPost {
				path += "/cancel"
			}
			for range 2 {
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
				if owner {
					assert.Equal(t, 200, recorder.Code)
				} else {
					assert.Equal(t, 404, recorder.Code)
					assert.NotContains(t, recorder.Body.String(), run.ID)
				}
			}
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(replayBody)))
		if owner {
			assert.Equal(t, 200, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"cancel_requested":true`)
		} else {
			assert.Equal(t, 403, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), run.ID)
		}
	}
	var count int64
	require.NoError(t, db.Model(&model.ModelTestRun{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestModelTestSwitchPostgresDatabaseFaultDeniesNewWorkButPreservesHistory(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	body := modelTestRequestBody(t, userID, uuid.NewString())
	require.NoError(t, db.Migrator().RenameTable(&model.Option{}, "model_test_unavailable_options"))
	t.Cleanup(func() {
		require.NoError(t, db.Migrator().RenameTable("model_test_unavailable_options", &model.Option{}))
	})
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", userID) })
	engine.POST("/estimate", EstimateModelTest)
	engine.POST("/runs", PrepareModelTest, func(c *gin.Context) { t.Error("DB fault allowed new execution") })
	engine.GET("/history", GetModelTests)
	for _, path := range []string{"/estimate", "/runs"} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		assert.Equal(t, 503, recorder.Code, recorder.Body.String())
		assert.Contains(t, recorder.Body.String(), "model_test_settings_unavailable")
		assert.NotContains(t, recorder.Body.String(), "SQL")
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/history", nil))
	assert.Equal(t, 200, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"enabled":false`)
	assert.Contains(t, recorder.Body.String(), `"availability_error":"model_test_settings_unavailable"`)
	var count int64
	require.NoError(t, db.Model(&model.ModelTestRun{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestModelTestSwitchPostgresRejectsNonBooleanSettings(t *testing.T) {
	_, _ = setupModelTestsPostgres(t)
	for _, raw := range []string{`1`, `null`, `{}`, `[]`, `"TRUE"`, `""`} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"ModelTestEnabled","value":`+raw+`}`))
		UpdateOption(c)
		assert.Contains(t, recorder.Body.String(), `"success":false`, raw)
	}
}
