package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyDiagnosticKeyEligibilityDeniesOtherOwnerExpiredIPAndExhausted(t *testing.T) {
	ip := "192.0.2.0/24"
	token := model.Token{UserId: 7, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100, AllowIps: &ip}
	assert.True(t, usableDiagnosticToken(&token, 7, "192.0.2.4"))
	assert.False(t, usableDiagnosticToken(&token, 8, "192.0.2.4"))
	assert.False(t, usableDiagnosticToken(&token, 7, "203.0.113.4"))
	token.ExpiredTime = time.Now().Add(-time.Minute).Unix()
	assert.False(t, usableDiagnosticToken(&token, 7, "192.0.2.4"))
	token.ExpiredTime = -1
	token.RemainQuota = 0
	assert.False(t, usableDiagnosticToken(&token, 7, "192.0.2.4"))
	token.UnlimitedQuota = true
	token.Status = common.TokenStatusDisabled
	assert.False(t, usableDiagnosticToken(&token, 7, "192.0.2.4"))
}

func TestKeyDiagnosticPostgresOneConfirmedRequestUsesOwnedKeyAndReplaysReceipt(t *testing.T) {
	db, userID := setupModelTestSwitchExecution(t)
	var token model.Token
	require.NoError(t, db.Where("user_id = ?", userID).First(&token).Error)
	require.NoError(t, db.Model(&token).Update("key", "syntheticownercredential").Error)
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "", r.Header.Get("Cookie"))
		assert.NotContains(t, r.Header.Get("Authorization"), "syntheticownercredential")
		var input struct {
			Model     string
			MaxTokens int `json:"max_tokens"`
			Messages  []struct{ Role, Content string }
		}
		require.NoError(t, common.DecodeJson(r.Body, &input))
		assert.Equal(t, "gpt-4o-mini", input.Model)
		assert.Equal(t, 8192, input.MaxTokens)
		require.Len(t, input.Messages, 1)
		assert.Contains(t, input.Messages[0].Content, "糖")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"local-response","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"eight"},"finish_reason":"stop"}],"usage":{"prompt_tokens":80,"completion_tokens":3,"total_tokens":83}}`)
	}))
	defer upstream.Close()
	require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Update("base_url", upstream.URL).Error)
	engine := gin.New()
	engine.Use(middleware.RequestId(), middleware.BodyStorageCleanup(), func(c *gin.Context) {
		c.Set("id", userID)
		c.Set("session_id", "local-browser")
		c.Set("auth_version", int64(1))
		c.Set("session_version", int64(1))
		c.Next()
	})
	engine.GET("/options", GetKeyDiagnosticOptions)
	engine.POST("/runs", PrepareKeyDiagnostic, middleware.TokenAuth(), KeyDiagnosticTokenAdmitted, middleware.ModelRequestRateLimit(), middleware.Distribute(), RunKeyDiagnostic)
	get := httptest.NewRecorder()
	engine.ServeHTTP(get, httptest.NewRequest("GET", "/options", nil))
	require.Equal(t, 200, get.Code)
	assert.NotContains(t, get.Body.String(), "syntheticownercredential")
	assert.Contains(t, get.Body.String(), "\"max_output_tokens\":32000")
	assert.Zero(t, calls.Load())
	id := uuid.NewString()
	selection := keyDiagnosticRequest{TokenID: token.Id, Model: "gpt-4o-mini", Question: "candy-logic-v1", RequestID: id, Confirmed: true}
	post := func(s keyDiagnosticRequest) *httptest.ResponseRecorder {
		data, err := common.Marshal(s)
		require.NoError(t, err)
		r := httptest.NewRequest("POST", "/runs", bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Cookie", "sentinel=not-for-provider")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, r)
		return rec
	}
	denied := selection
	denied.Confirmed = false
	assert.Equal(t, 400, post(denied).Code)
	denied = selection
	denied.Model = "not-in-group"
	assert.Equal(t, 403, post(denied).Code)
	assert.Zero(t, calls.Load())
	for range 2 {
		rec := post(selection)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), `"answer":"eight"`)
		assert.NotContains(t, rec.Body.String(), "syntheticownercredential")
	}
	assert.EqualValues(t, 1, calls.Load())
	denied = selection
	denied.Question = "pelican-svg-v1"
	assert.Equal(t, 409, post(denied).Code)
	assert.EqualValues(t, 1, calls.Load())
	var run model.ModelTestRun
	require.NoError(t, db.First(&run, "id = ?", id).Error)
	var receipt model.BillingFinalization
	require.NoError(t, db.Where("request_id = ?", run.RequestID).Take(&receipt).Error)
	assert.Equal(t, userID, receipt.UserID)
	assert.Equal(t, token.Id, receipt.TokenID)
	assert.Equal(t, model.BillingSettled, receipt.State)
}
