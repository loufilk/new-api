package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestTurnstileProtectedRoutesKeepLoginAndChallengeRequirements protects the
// route wiring: the anonymous reset-password endpoint rejects a missing
// challenge when Turnstile is enabled, while check-in stays behind UserAuth.
func TestTurnstileProtectedRoutesKeepLoginAndChallengeRequirements(t *testing.T) {
	previousEnabled := common.TurnstileCheckEnabled
	common.TurnstileCheckEnabled = true
	t.Cleanup(func() { common.TurnstileCheckEnabled = previousEnabled })

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	resetRecorder := httptest.NewRecorder()
	engine.ServeHTTP(resetRecorder, httptest.NewRequest(
		http.MethodGet,
		"/api/reset_password?email=user@example.com",
		nil,
	))
	require.Equal(t, http.StatusForbidden, resetRecorder.Code)

	// The challenge check must not weaken the existing login requirement.
	checkinRecorder := httptest.NewRecorder()
	engine.ServeHTTP(checkinRecorder, httptest.NewRequest(http.MethodPost, "/api/user/checkin", nil))
	require.Equal(t, http.StatusUnauthorized, checkinRecorder.Code)
}
