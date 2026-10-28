package router

import (
	"testing"

	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestCriticalAuthRoutesHaveDedicatedRateLimitScopes proves the 22 scoped
// authentication routes stay registered and keep their 12 shared buckets:
// OAuth bind/login, 2FA, and both passkey aliases must not silently fall back
// to the generic CT bucket.
func TestCriticalAuthRoutesHaveDedicatedRateLimitScopes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	registered := make(map[string]bool)
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	scopes := middleware.CriticalRateLimitRouteScopes()
	require.Len(t, scopes, 22)
	for route, scope := range scopes {
		require.True(t, registered[route], "critical auth route %s must stay registered", route)
		require.NotEmpty(t, scope, "critical auth route %s must keep a dedicated bucket", route)
	}

	unique := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		unique[scope] = struct{}{}
	}
	require.Len(t, unique, 12, "the shared bucket consolidation must stay at 12 scopes")

	require.Equal(t, scopes["POST /api/user/login/2fa"], scopes["POST /api/user/login/verify"])
	require.Equal(t, scopes["POST /api/user/login/passkey/begin"], scopes["POST /api/user/passkey/login/begin"])
	require.Equal(t, scopes["POST /api/user/login/passkey/finish"], scopes["POST /api/user/passkey/login/finish"])
	require.Equal(t, scopes["GET /api/oauth/wechat"], scopes["GET /api/oauth/:provider"])
	require.Equal(t, scopes["POST /api/oauth/email/bind/start"], scopes["POST /api/oauth/email/bind"])
	require.NotEqual(t, scopes["POST /api/user/login"], scopes["POST /api/user/register"])
	require.NotEqual(t, scopes["POST /api/oauth/state"], scopes["POST /api/oauth/email/bind"])
}
