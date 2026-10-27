package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type turnstileRoundTripper func(*http.Request) (*http.Response, error)

func (f turnstileRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func configureTurnstilePublicSite(t *testing.T, address string) {
	t.Helper()
	previous := system_setting.ServerAddress
	system_setting.ServerAddress = address
	t.Cleanup(func() { system_setting.ServerAddress = previous })
}

func TestTurnstileCheckRequiresMatchingActionAndHostname(t *testing.T) {
	configureTurnstilePublicSite(t, "https://chuanapi.com")
	previousEnabled := common.TurnstileCheckEnabled
	previousSecret := common.TurnstileSecretKey
	previousClient := turnstileHTTPClient
	common.TurnstileCheckEnabled = true
	common.TurnstileSecretKey = "test-secret"
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = previousEnabled
		common.TurnstileSecretKey = previousSecret
		turnstileHTTPClient = previousClient
	})

	tests := []struct {
		name       string
		response   string
		wantStatus int
	}{
		{
			name:       "matching action and hostname passes",
			response:   `{"success":true,"action":"login","hostname":"chuanapi.com"}`,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "mismatched action is rejected",
			response:   `{"success":true,"action":"signup","hostname":"chuanapi.com"}`,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "mismatched hostname is rejected",
			response:   `{"success":true,"action":"login","hostname":"example.com"}`,
			wantStatus: http.StatusForbidden,
		},
	}

	gin.SetMode(gin.TestMode)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			turnstileHTTPClient = &http.Client{Transport: turnstileRoundTripper(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, request.Method)
				require.NoError(t, request.ParseForm())
				assert.Equal(t, "test-secret", request.Form.Get("secret"))
				assert.Equal(t, "test-token", request.Form.Get("response"))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(test.response)),
					Header:     make(http.Header),
				}, nil
			})}

			router := gin.New()
			router.GET("/verify", TurnstileCheck("login"), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/verify?turnstile=test-token", nil)
			router.ServeHTTP(recorder, request)

			assert.Equal(t, test.wantStatus, recorder.Code)
		})
	}
}

func TestTurnstileCheckFailsClosedWithoutConfiguredPublicHostname(t *testing.T) {
	previousEnabled := common.TurnstileCheckEnabled
	previousSecret := common.TurnstileSecretKey
	previousClient := turnstileHTTPClient
	common.TurnstileCheckEnabled = true
	common.TurnstileSecretKey = "test-secret"
	configureTurnstilePublicSite(t, "")
	called := false
	turnstileHTTPClient = &http.Client{Transport: turnstileRoundTripper(func(_ *http.Request) (*http.Response, error) {
		called = true
		return nil, assert.AnError
	})}
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = previousEnabled
		common.TurnstileSecretKey = previousSecret
		turnstileHTTPClient = previousClient
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/verify", TurnstileCheck("login"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "https://attacker.example/verify?turnstile=test-token", nil)
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.False(t, called, "invalid public hostname must fail before calling Turnstile")
}

func TestTurnstileCheckDoesNotTrustRequestHost(t *testing.T) {
	previousEnabled := common.TurnstileCheckEnabled
	previousSecret := common.TurnstileSecretKey
	previousClient := turnstileHTTPClient
	common.TurnstileCheckEnabled = true
	common.TurnstileSecretKey = "test-secret"
	configureTurnstilePublicSite(t, "https://trusted.example")
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = previousEnabled
		common.TurnstileSecretKey = previousSecret
		turnstileHTTPClient = previousClient
	})
	turnstileHTTPClient = &http.Client{Transport: turnstileRoundTripper(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"action":"login","hostname":"attacker.example"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/verify", TurnstileCheck("login"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "https://attacker.example/verify?turnstile=test-token", nil)
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestTurnstileCheckSkipsVerificationWhenDisabled(t *testing.T) {
	previousEnabled := common.TurnstileCheckEnabled
	common.TurnstileCheckEnabled = false
	t.Cleanup(func() { common.TurnstileCheckEnabled = previousEnabled })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/verify", TurnstileCheck("login"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/verify", nil)
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestTurnstileCheckResolvesActionFromRegisteredRoute(t *testing.T) {
	configureTurnstilePublicSite(t, "https://chuanapi.com")
	previousEnabled := common.TurnstileCheckEnabled
	previousSecret := common.TurnstileSecretKey
	previousClient := turnstileHTTPClient
	common.TurnstileCheckEnabled = true
	common.TurnstileSecretKey = "test-secret"
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = previousEnabled
		common.TurnstileSecretKey = previousSecret
		turnstileHTTPClient = previousClient
	})

	verificationCalls := 0
	action := "login"
	turnstileHTTPClient = &http.Client{Transport: turnstileRoundTripper(func(_ *http.Request) (*http.Response, error) {
		verificationCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"action":"` + action + `","hostname":"chuanapi.com"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	for _, path := range []string{"/api/user/login", "/api/user/register", "/api/unmapped-turnstile"} {
		router.POST(path, TurnstileCheck(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}
	post := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path+"?turnstile=test-token", nil))
		return recorder
	}

	t.Run("mapped route passes with its own action", func(t *testing.T) {
		action = "login"
		before := verificationCalls
		assert.Equal(t, http.StatusNoContent, post("/api/user/login").Code)
		assert.Equal(t, before+1, verificationCalls)
	})

	t.Run("mapped route rejects a different action", func(t *testing.T) {
		action = "login"
		assert.Equal(t, http.StatusForbidden, post("/api/user/register").Code)
	})

	t.Run("unmapped route fails closed without calling siteverify", func(t *testing.T) {
		action = "signup"
		before := verificationCalls
		assert.Equal(t, http.StatusForbidden, post("/api/unmapped-turnstile").Code)
		assert.Equal(t, before, verificationCalls, "an unmapped route must be rejected before any siteverify request")
	})
}

func TestTurnstileCheckCoversResetPasswordAndCheckin(t *testing.T) {
	configureTurnstilePublicSite(t, "https://chuanapi.com")
	previousEnabled := common.TurnstileCheckEnabled
	previousSecret := common.TurnstileSecretKey
	previousClient := turnstileHTTPClient
	common.TurnstileCheckEnabled = true
	common.TurnstileSecretKey = "test-secret"
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = previousEnabled
		common.TurnstileSecretKey = previousSecret
		turnstileHTTPClient = previousClient
	})

	action := "reset_password"
	verificationCalls := 0
	turnstileHTTPClient = &http.Client{Transport: turnstileRoundTripper(func(request *http.Request) (*http.Response, error) {
		verificationCalls++
		require.NoError(t, request.ParseForm())
		assert.Equal(t, "test-token", request.Form.Get("response"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"action":"` + action + `","hostname":"chuanapi.com"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/reset_password", TurnstileCheck(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.POST("/api/user/checkin", TurnstileCheck(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	do := func(method, path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
		return recorder
	}

	t.Run("reset password accepts its own action", func(t *testing.T) {
		action = "reset_password"
		assert.Equal(t, http.StatusNoContent, do(http.MethodGet, "/api/reset_password?turnstile=test-token").Code)
	})

	t.Run("reset password rejects a mismatched action", func(t *testing.T) {
		action = "login"
		assert.Equal(t, http.StatusForbidden, do(http.MethodGet, "/api/reset_password?turnstile=test-token").Code)
	})

	t.Run("checkin accepts its own action", func(t *testing.T) {
		action = "checkin"
		assert.Equal(t, http.StatusNoContent, do(http.MethodPost, "/api/user/checkin?turnstile=test-token").Code)
	})

	t.Run("checkin rejects a mismatched action", func(t *testing.T) {
		action = "login"
		assert.Equal(t, http.StatusForbidden, do(http.MethodPost, "/api/user/checkin?turnstile=test-token").Code)
	})

	t.Run("a missing token is rejected before siteverify", func(t *testing.T) {
		action = "checkin"
		before := verificationCalls
		assert.Equal(t, http.StatusForbidden, do(http.MethodPost, "/api/user/checkin").Code)
		assert.Equal(t, before, verificationCalls)
	})
}

func TestTurnstileCheckDisabledKeepsResetPasswordAndCheckinOpen(t *testing.T) {
	previousEnabled := common.TurnstileCheckEnabled
	common.TurnstileCheckEnabled = false
	t.Cleanup(func() { common.TurnstileCheckEnabled = previousEnabled })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/reset_password", TurnstileCheck(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.POST("/api/user/checkin", TurnstileCheck(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	resetRecorder := httptest.NewRecorder()
	router.ServeHTTP(resetRecorder, httptest.NewRequest(http.MethodGet, "/api/reset_password", nil))
	assert.Equal(t, http.StatusNoContent, resetRecorder.Code)

	checkinRecorder := httptest.NewRecorder()
	router.ServeHTTP(checkinRecorder, httptest.NewRequest(http.MethodPost, "/api/user/checkin", nil))
	assert.Equal(t, http.StatusNoContent, checkinRecorder.Code)
}

func TestTurnstileCheckRejectsReplayedToken(t *testing.T) {
	configureTurnstilePublicSite(t, "https://chuanapi.com")
	previousEnabled := common.TurnstileCheckEnabled
	previousSecret := common.TurnstileSecretKey
	previousClient := turnstileHTTPClient
	common.TurnstileCheckEnabled = true
	common.TurnstileSecretKey = "test-secret"
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = previousEnabled
		common.TurnstileSecretKey = previousSecret
		turnstileHTTPClient = previousClient
	})

	verificationCount := 0
	turnstileHTTPClient = &http.Client{Transport: turnstileRoundTripper(func(_ *http.Request) (*http.Response, error) {
		verificationCount++
		body := `{"success":true,"action":"signup","hostname":"chuanapi.com"}`
		if verificationCount > 1 {
			body = `{"success":false,"error-codes":["timeout-or-duplicate"]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/verify", TurnstileCheck("signup"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/verify?turnstile=single-use-token", nil))
	assert.Equal(t, http.StatusNoContent, first.Code)

	replay := httptest.NewRecorder()
	router.ServeHTTP(replay, httptest.NewRequest(http.MethodGet, "/verify?turnstile=single-use-token", nil))
	assert.Equal(t, http.StatusForbidden, replay.Code)
}
