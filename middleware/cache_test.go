package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func performCacheRequest(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Cache())
	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "index")
	})
	router.GET("/static/app.js", func(c *gin.Context) {
		c.String(http.StatusOK, "static")
	})
	router.GET("/logo.png", func(c *gin.Context) {
		c.String(http.StatusOK, "asset")
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestCacheControlByPath(t *testing.T) {
	cases := []struct {
		name     string
		target   string
		expected string
	}{
		{name: "root is not stored", target: "/", expected: "no-store"},
		{name: "root with query string is not stored", target: "/?v=123&lang=en", expected: "no-store"},
		{name: "static bundle is immutable", target: "/static/app.js", expected: "public,max-age=31536000,immutable"},
		{name: "static bundle with query is immutable", target: "/static/app.js?v=123", expected: "public,max-age=31536000,immutable"},
		{name: "other web asset uses one week", target: "/logo.png", expected: "public,max-age=604800"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := performCacheRequest(t, tc.target)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, tc.expected, recorder.Header().Get("Cache-Control"))
			require.NotEmpty(t, recorder.Header().Get("Cache-Version"))
			require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
		})
	}
}
