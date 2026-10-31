package router

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The in-memory limiter is process-wide, so repeated test runs need distinct
// authenticated user buckets even though their PostgreSQL schemas are isolated.
var agentRateLimitFixtureSequence atomic.Int64

func agentRateLimitBrowser(t *testing.T) (agentFinanceBrowser, string) {
	t.Helper()
	n := agentRateLimitFixtureSequence.Add(1)
	user := model.User{Id: 1000000 + int(n), Username: fmt.Sprintf("rate-browser-%d", n), AffCode: fmt.Sprintf("rate-aff-%d", n), Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, model.DB.Create(&user).Error)
	identity := model.AuthSessionIdentity{UserID: user.Id, SessionID: fmt.Sprintf("synthetic-rate-browser-%d", n), UserAuthVersion: 1, SessionVersion: 1}
	require.NoError(t, model.DB.Create(&model.UserSession{SID: identity.SessionID, UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: "synthetic", LoginMethod: "password"}).Error)
	require.NoError(t, model.DB.Create(&model.AgentProfile{UserID: user.Id, Status: model.AgentProfileActive, Version: 1, Reason: "Synthetic rate boundary"}).Error)
	require.NoError(t, model.DB.Create(&model.TwoFA{UserId: user.Id, IsEnabled: true, Secret: "synthetic-not-a-live-credential"}).Error)
	bearer, _, err := service.IssueAccessToken(identity)
	require.NoError(t, err)
	return agentFinanceBrowser{identity: identity, bearer: bearer}, fmt.Sprintf("198.18.%d.%d:12345", n/250, n%250+1)
}

func agentRateLimitRequest(t *testing.T, engine *gin.Engine, browser agentFinanceBrowser, remote, method, path string, input any, proof string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := common.Marshal(input)
	require.NoError(t, err)
	request := httptest.NewRequest(method, "https://console.example/api/agent-platform"+path, bytes.NewReader(body))
	request.RemoteAddr = remote
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://console.example")
	if browser.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+browser.bearer)
	}
	request.Header.Set("X-Security-Proof", proof)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestAgentPlatformReadTrafficPreservesWriteRateLimitPostgres(t *testing.T) {
	// Reuse only the isolated real PostgreSQL/browser fixture, then rebuild the
	// real host router with both production-default limits explicitly enabled.
	agentFinanceRouterFixture(t)
	// Generic operation recovery also consults the pre-existing redemption
	// operation table, which the commission-only fixture does not require.
	require.NoError(t, model.DB.AutoMigrate(&model.AgentRedemptionOperation{}))
	oldCritical, oldCriticalNum, oldCriticalDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	oldGlobal, oldGlobalNum, oldGlobalDuration := common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 20, 1200
	common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = true, 360, 180
	t.Cleanup(func() {
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = oldCritical, oldCriticalNum, oldCriticalDuration
		common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = oldGlobal, oldGlobalNum, oldGlobalDuration
	})
	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies(nil))
	require.NoError(t, RegisterAgentPlatformRoutes(SetApiRouter(engine)))
	owner, remote := agentRateLimitBrowser(t)

	t.Run("reads_do_not_spend_writes_and_recovery_survives_write_exhaustion", func(t *testing.T) {
		for i := range 25 {
			response := agentRateLimitRequest(t, engine, owner, remote, http.MethodGet, "/self", nil, "")
			require.Equal(t, http.StatusOK, response.Code, "read %d: %s", i+1, response.Body.String())
			assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
		}
		input := model.AgentCommissionRecipientRequest{RequestID: "rate-limit-recipient", Label: "Synthetic recipient", RecipientReference: "controlled-synthetic-registry-entry"}
		// A read-budget fix must not bypass transaction MFA. This rejected write
		// still counts against the unchanged user critical budget (write 1).
		response := agentRateLimitRequest(t, engine, owner, remote, http.MethodPost, "/commission-recipients", input, "")
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		var count int64
		require.NoError(t, model.DB.Model(&model.AgentCommissionRecipient{}).Where("user_id = ?", owner.identity.UserID).Count(&count).Error)
		assert.Zero(t, count)
		// Writes 2 and 3 exercise actual routed prepare and mutation. The proof
		// helper isolates limiter wiring; the real ceremonies have separate tests.
		var prepared model.AgentPreparedOperation
		response = agentRateLimitRequest(t, engine, owner, remote, http.MethodPost, "/operations/prepare", map[string]any{"action": "recipient.change", "request_id": input.RequestID, "payload": input}, "")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var envelope struct {
			Success bool              `json:"success"`
			Data    common.RawMessage `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
		require.True(t, envelope.Success)
		require.NoError(t, common.Unmarshal(envelope.Data, &prepared))
		hash, err := model.AgentTransactionContextHash(prepared.VerificationContext)
		require.NoError(t, err)
		proof, _, err := model.IssueAgentTransactionProof(owner.identity, hash, "2fa")
		require.NoError(t, err)
		response = agentRateLimitRequest(t, engine, owner, remote, http.MethodPost, "/commission-recipients", input, proof)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		for i := range 17 {
			next := input
			next.RequestID = fmt.Sprintf("rate-prepare-%d", i)
			response = agentRateLimitRequest(t, engine, owner, remote, http.MethodPost, "/operations/prepare", map[string]any{"action": "recipient.change", "request_id": next.RequestID, "payload": next}, "")
			require.Equal(t, http.StatusOK, response.Code, "write %d: %s", i+4, response.Body.String())
		}
		for _, methodPath := range []struct{ method, path string }{{http.MethodPost, "/operations/prepare"}, {http.MethodPut, "/commission-recipients/synthetic"}, {http.MethodPatch, "/profiles/1"}} {
			response = agentRateLimitRequest(t, engine, owner, remote, methodPath.method, methodPath.path, input, "")
			require.Equal(t, http.StatusTooManyRequests, response.Code, methodPath.method)
			assert.Equal(t, "1200", response.Header().Get("Retry-After"))
			assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
		}
		response = agentRateLimitRequest(t, engine, owner, "198.19.255.1:12345", http.MethodPost, "/operations/prepare", input, "")
		require.Equal(t, http.StatusTooManyRequests, response.Code, "rotating IP must not reset the authenticated-user write bucket")
		response = agentRateLimitRequest(t, engine, owner, remote, http.MethodGet, "/operations/"+input.RequestID, nil, "")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
		require.True(t, envelope.Success)
		var recovered model.AgentOperationStatus
		require.NoError(t, common.Unmarshal(envelope.Data, &recovered))
		assert.Equal(t, "completed", recovered.Status)
		assert.Equal(t, "recipient.change", recovered.Action)
		assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	})

	t.Run("safe_reads_keep_global_ip_limit", func(t *testing.T) {
		reader, readIP := agentRateLimitBrowser(t)
		for i := range 360 {
			response := agentRateLimitRequest(t, engine, reader, readIP, http.MethodGet, "/policy/current", nil, "")
			require.Equal(t, http.StatusOK, response.Code, "global read %d: %s", i+1, response.Body.String())
		}
		response := agentRateLimitRequest(t, engine, reader, readIP, http.MethodGet, "/policy/current", nil, "")
		require.Equal(t, http.StatusTooManyRequests, response.Code)
		assert.Equal(t, "180", response.Header().Get("Retry-After"))
	})

	t.Run("reads_still_require_browser_live_session_and_owner_scope", func(t *testing.T) {
		other, otherIP := agentRateLimitBrowser(t)
		for _, route := range []struct{ method, path string }{{http.MethodGet, "/self"}, {http.MethodPost, "/operations/prepare"}} {
			response := agentRateLimitRequest(t, engine, agentFinanceBrowser{}, otherIP, route.method, route.path, nil, "")
			require.Equal(t, http.StatusUnauthorized, response.Code)
			assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
		}
		pat := model.AccessTokenPrefix + "synthetic-rate-boundary-" + strconv.Itoa(other.identity.UserID)
		seedRouteScopedToken(t, model.DB, other.identity.UserID, pat, "profile:read")
		response := agentRateLimitRequest(t, engine, agentFinanceBrowser{bearer: pat}, otherIP, http.MethodGet, "/self", nil, "")
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
		withdrawal := model.AgentCommissionWithdrawal{BusinessNo: "synthetic-private-rate-withdrawal", UserID: owner.identity.UserID, RequestedBy: owner.identity.UserID, RequestID: "synthetic-private-rate-request", QuoteID: "synthetic-private-rate-quote", Currency: "USD", CurrencyExponent: 2, AmountMinor: 100, NetMinor: 100, Status: "applied", Version: 1}
		require.NoError(t, model.DB.Create(&withdrawal).Error)
		response = agentRateLimitRequest(t, engine, other, otherIP, http.MethodGet, fmt.Sprintf("/withdrawals/%d", withdrawal.ID), nil, "")
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), withdrawal.BusinessNo)
		require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", other.identity.SessionID).Update("status", model.UserSessionStatusRevoked).Error)
		response = agentRateLimitRequest(t, engine, other, otherIP, http.MethodGet, "/self", nil, "")
		require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
		assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	})
}
