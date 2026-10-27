package router

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChuanAPIAccessTokenScopeEnforcement(t *testing.T) {
	_, db := setupReferralRouteTest(t)
	engine := newAccessTokenScopeTestEngine(t)
	user := seedReferralRouteUser(t, db, 710, "", 1_000_000, common.UserStatusEnabled)
	reader := model.AccessTokenPrefix + "wallet-reader"
	seedRouteScopedToken(t, db, user.Id, reader, "wallet:read")
	for _, path := range []string{"/api/user/wallet", "/api/user/wallet/ledger", "/api/user/referral/rewards"} {
		response := performReferralRouteRequest(engine, http.MethodGet, path, "", reader)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"success":true`)
	}
	for index, tc := range []struct {
		method, path, scope, code string
	}{
		{http.MethodGet, "/api/user/wallet", "profile:read", "ACCESS_TOKEN_SCOPE_DENIED"},
		{http.MethodGet, "/api/user/wallet/ledger", "profile:read", "ACCESS_TOKEN_SCOPE_DENIED"},
		{http.MethodGet, "/api/user/referral/rewards", "profile:read", "ACCESS_TOKEN_SCOPE_DENIED"},
		{http.MethodPost, "/api/user/aff_transfer", "wallet:read", "ACCESS_TOKEN_SCOPE_DENIED"},
		{http.MethodPost, "/api/token/models", "wallet:read", "ACCESS_TOKEN_SCOPE_DENIED"},
		{http.MethodGet, "/api/user/tokens/123/models", "wallet:read", "ACCESS_TOKEN_SCOPE_DENIED"},
		{http.MethodGet, "/api/user/self/email/verification", "account_security:read", "ACCESS_TOKEN_SCOPE_DENIED"},
		{http.MethodGet, "/api/user/playground/bootstrap", "api_key:write", "AUTH_SESSION_REQUIRED"},
		{http.MethodPost, "/api/user/playground/chat", "api_key:write", "AUTH_SESSION_REQUIRED"},
		{http.MethodGet, "/api/support/tickets", "profile:read", "AUTH_SESSION_REQUIRED"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			raw := model.AccessTokenPrefix + fmt.Sprintf("scoped-route-%d", index)
			seedRouteScopedToken(t, db, user.Id, raw, tc.scope)
			response := performReferralRouteRequest(engine, tc.method, tc.path, `{}`, raw)
			assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), tc.code)
		})
	}
	assert.Zero(t, countReferralRouteLedgerRows(t, db, user.Id), "permission denial must not change funds")
	require.NoError(t, db.Model(&model.UserAccessToken{}).Where("token_hash = ?", model.AccessTokenFingerprint(reader)).Update("expires_at", common.GetTimestamp()-1).Error)
	expired := performReferralRouteRequest(engine, http.MethodGet, "/api/user/wallet", "", reader)
	assert.Equal(t, http.StatusUnauthorized, expired.Code)
	assert.Contains(t, expired.Body.String(), "ACCESS_TOKEN_EXPIRED")
}
