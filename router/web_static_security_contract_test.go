package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is an approval-stage contract test, not evidence of retained old assets.
// Empty BuildFS deliberately isolates the real router's missing-file behavior.
func TestWebFallbackResponseSecurityContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := common.GlobalWebRateLimitEnable
	common.GlobalWebRateLimitEnable = false
	t.Cleanup(func() { common.GlobalWebRateLimitEnable = previous })
	engine := gin.New()
	engine.GET("/api/static-contract-fixture", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"registered": true}) })
	SetWebRouter(engine, WebAssets{IndexPage: []byte("<!doctype html><html><body>synthetic SPA shell</body></html>")}, func(c *gin.Context) {
		if c.Request.URL.Path == "/vendor/static-contract-fixture" || c.Request.URL.Path == "/static/plugin-fixture.js" {
			c.String(http.StatusOK, "plugin response")
			c.Abort()
		}
	})
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	transport := &http.Transport{Proxy: nil, DisableCompression: true}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(t *testing.T, method, path string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, nil)
		require.NoError(t, err)
		res, err := client.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		return res, body
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range []string{"/", "/dashboard", "/dashboard/models", "/channels", "/system-settings", "/image?prompt=synthetic", "/nested.client/route"} {
			t.Run(method+"_SPA_"+path, func(t *testing.T) {
				res, body := request(t, method, path)
				assert.Equal(t, http.StatusOK, res.StatusCode)
				assert.Contains(t, res.Header.Get("Content-Type"), "text/html")
				assert.Contains(t, res.Header.Get("Cache-Control"), "no-store")
				assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
				if method == http.MethodGet {
					assert.Contains(t, string(body), "synthetic SPA shell")
				} else {
					assert.Empty(t, body)
				}
			})
		}
		for _, path := range []string{"/static/js/chunk.deadbeef.js", "/static/css/missing.deadbeef.css", "/static/image/logo.deadbeef.svg", "/static/js/missing.js?version=old", "/static/js/%2e%2e/missing.deadbeef.js", "/%73tatic/js/missing.deadbeef.js", "/static/../dashboard", "/%2e%2e/static/js/missing.deadbeef.js", "//static/js/missing.deadbeef.js", "/static", "/assets/missing.js", "/api/static-contract-missing", "/v1/static-contract-missing"} {
			t.Run(method+"_missing_"+path, func(t *testing.T) {
				res, body := request(t, method, path)
				assert.Equal(t, http.StatusNotFound, res.StatusCode)
				assert.NotContains(t, strings.ToLower(res.Header.Get("Content-Type")), "text/html")
				assert.Contains(t, res.Header.Get("Cache-Control"), "no-store")
				assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
				assert.NotContains(t, string(body), "synthetic SPA shell")
			})
		}
	}
	t.Run("registered_API_unchanged", func(t *testing.T) {
		res, body := request(t, http.MethodGet, "/api/static-contract-fixture")
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.JSONEq(t, `{"registered":true}`, string(body))
	})
	t.Run("plugin_dispatch_keeps_precedence", func(t *testing.T) {
		for _, path := range []string{"/vendor/static-contract-fixture", "/static/plugin-fixture.js"} {
			res, body := request(t, http.MethodGet, path)
			assert.Equal(t, http.StatusOK, res.StatusCode)
			assert.Equal(t, "plugin response", string(body))
		}
	})
}
