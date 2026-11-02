package controller

import (
	"bytes"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
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
	_, userID := setupModelTestsPostgres(t)
	for _, raw := range []string{`1`, `null`, `{}`, `[]`, `"TRUE"`, `""`} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", userID)
		c.Set("session_id", "synthetic-validation-session")
		c.Set("auth_version", int64(1))
		c.Set("session_version", int64(1))
		c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"ModelTestEnabled","value":`+raw+`}`))
		UpdateOption(c)
		assert.Contains(t, recorder.Body.String(), `"success":false`, raw)
		assert.NotContains(t, recorder.Body.String(), "AUTH_SESSION_REQUIRED")
	}
}

func TestModelTestSwitchPostgresOnlyLiveRootSessionCanChangeCapability(t *testing.T) {
	db, rootID := setupModelTestsPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.UserSession{}, &model.UserAccessToken{}, &model.AuditLog{}))
	require.NoError(t, i18n.Init())
	oldMemory, oldSecret := common.MemoryCacheEnabled, common.SessionSecret
	oldOptions := maps.Clone(common.OptionMap)
	common.MemoryCacheEnabled = false
	common.SessionSecret = "synthetic-model-test-settings-session-secret"
	t.Cleanup(func() {
		common.MemoryCacheEnabled, common.SessionSecret = oldMemory, oldSecret
		common.OptionMap = oldOptions
	})
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	legacy := "synthetic-model-test-root-pat"
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", rootID).Updates(map[string]any{"role": common.RoleRootUser, "auth_version": 1, "access_token": legacy}).Error)
	_, rootSession := createAccessTokenTestSession(t, rootID, "synthetic-model-test-root-session")
	scoped, _ := createScopedAccessToken(t, rootID, 0, "option:write")
	users := []model.User{
		{Username: "settings-ordinary", AffCode: "settings-ordinary", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1},
		{Username: "settings-admin", AffCode: "settings-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1},
	}
	require.NoError(t, db.Create(&users).Error)
	_, ordinarySession := createAccessTokenTestSession(t, users[0].Id, "synthetic-model-test-ordinary-session")
	_, adminSession := createAccessTokenTestSession(t, users[1].Id, "synthetic-model-test-admin-session")
	require.NoError(t, db.Create(&model.Option{Key: model.AccountPoolEnabledKey, Value: "true"}).Error)
	engine := gin.New()
	engine.PUT("/api/option/", middleware.RootAuth(), UpdateOption)
	engine.GET("/api/status", GetStatus)
	for _, enabled := range []string{"false", "true", "false"} {
		body := `{"key":"ModelTestEnabled","value":` + enabled + `}`
		response := accessTokenRequest(engine, http.MethodPut, "/api/option/", rootSession, "", body)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `"success":true`)
		status := httptest.NewRecorder()
		engine.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/status", nil))
		assert.Contains(t, status.Body.String(), `"model_test_enabled":`+enabled)
		assert.Contains(t, status.Body.String(), `"model_test_settings_available":true`)
		for _, denied := range []struct {
			name, token, code string
			status            int
		}{
			{"root scoped PAT", scoped, "AUTH_SESSION_REQUIRED", http.StatusForbidden},
			{"root legacy PAT", legacy, "AUTH_SESSION_REQUIRED", http.StatusForbidden},
			{"ordinary session", ordinarySession, "AUTH_INSUFFICIENT_PRIVILEGE", http.StatusForbidden},
			{"admin session", adminSession, "AUTH_INSUFFICIENT_PRIVILEGE", http.StatusForbidden},
			{"unauthenticated", "", "", http.StatusUnauthorized},
		} {
			t.Run(enabled+"/"+denied.name, func(t *testing.T) {
				opposite := "true"
				if enabled == "true" {
					opposite = "false"
				}
				response := accessTokenRequest(engine, http.MethodPut, "/api/option/", denied.token, "", `{"key":"ModelTestEnabled","value":`+opposite+`}`)
				assert.Equal(t, denied.status, response.Code, response.Body.String())
				if denied.code != "" {
					assert.Contains(t, response.Body.String(), denied.code)
				}
				var stored model.Option
				require.NoError(t, db.Where(model.Option{Key: model.ModelTestEnabledKey}).Take(&stored).Error)
				assert.Equal(t, enabled, stored.Value)
			})
		}
		poolEnabled, err := model.AccountPoolEnabled(t.Context())
		require.NoError(t, err)
		assert.True(t, poolEnabled, "model testing is independent of the account pool")
	}
	// The narrow session-only capability must not revoke existing management
	// permissions for unrelated options.
	response := accessTokenRequest(engine, http.MethodPut, "/api/option/", scoped, "", `{"key":"ModelTestSyntheticUnrelatedOption","value":"preserved"}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"success":true`)
	for _, alias := range []string{"modeltestenabled", "MODELTESTENABLED", "ModelTestEnabled ", " ModelTestEnabled", "\tModelTestEnabled\n"} {
		body, err := common.Marshal(map[string]any{"key": alias, "value": true})
		require.NoError(t, err)
		for _, token := range []string{scoped, legacy} {
			denied := accessTokenRequest(engine, http.MethodPut, "/api/option/", token, "", string(body))
			assert.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
			assert.Contains(t, denied.Body.String(), "AUTH_SESSION_REQUIRED")
		}
		rejected := accessTokenRequest(engine, http.MethodPut, "/api/option/", rootSession, "", string(body))
		assert.Equal(t, http.StatusBadRequest, rejected.Code, rejected.Body.String())
		var aliasCount int64
		require.NoError(t, db.Model(&model.Option{}).Where(model.Option{Key: alias}).Count(&aliasCount).Error)
		assert.Zero(t, aliasCount, "noncanonical option key must never be persisted")
		current, err := model.ModelTestEnabled(t.Context())
		require.NoError(t, err)
		assert.False(t, current)
	}
	revoked, err := model.RevokeUserSession(rootID, "synthetic-model-test-root-session", "synthetic-revocation")
	require.NoError(t, err)
	require.True(t, revoked)
	response = accessTokenRequest(engine, http.MethodPut, "/api/option/", rootSession, "", `{"key":"ModelTestEnabled","value":true}`)
	assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	enabled, err := model.ModelTestEnabled(t.Context())
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestModelTestSwitchPostgresStatusDistinguishesDisabledFromUnavailable(t *testing.T) {
	db, _ := setupModelTestsPostgres(t)
	engine := gin.New()
	engine.GET("/api/status", GetStatus)
	for _, value := range []string{"true", "false"} {
		require.NoError(t, db.Model(&model.Option{}).Where(model.Option{Key: model.ModelTestEnabledKey}).Update("value", value).Error)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))
		require.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"model_test_enabled":`+value)
		assert.Contains(t, response.Body.String(), `"model_test_settings_available":true`)
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
	require.NoError(t, db.Migrator().RenameTable(&model.Option{}, "model_test_settings_unavailable_options"))
	t.Cleanup(func() {
		require.NoError(t, db.Migrator().RenameTable("model_test_settings_unavailable_options", &model.Option{}))
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	require.Equal(t, http.StatusOK, response.Code, "one unavailable capability must not break public site status")
	assert.Contains(t, response.Body.String(), `"model_test_enabled":false`)
	assert.Contains(t, response.Body.String(), `"model_test_settings_available":false`)
	assert.NotContains(t, response.Body.String(), "SQL")
	assert.NotContains(t, response.Body.String(), "model_test_settings_unavailable_options")
}
