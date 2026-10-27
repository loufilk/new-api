package middleware

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrontendAssetCacheSecurityContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "static", "js"), 0700))
	script := []byte("export const fixture = 'synthetic';\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "static", "js", "app.deadbeef.js"), script, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><html></html>"), 0600))
	engine := gin.New()
	engine.NoRoute(Cache(), ServeFrontendFiles(static.LocalFile(dir, false)))
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method+"_existing_hash", func(t *testing.T) {
			res := performFrontendRequest(engine, method, "/static/js/app.deadbeef.js", "identity")
			assert.Equal(t, http.StatusOK, res.Code)
			assert.Contains(t, res.Header().Get("Content-Type"), "javascript")
			assert.Contains(t, res.Header().Get("Cache-Control"), "max-age=31536000")
			assert.Contains(t, res.Header().Get("Cache-Control"), "immutable")
			assert.Equal(t, "nosniff", res.Header().Get("X-Content-Type-Options"))
			if method == http.MethodGet {
				assert.Equal(t, script, res.Body.Bytes())
			} else {
				assert.Empty(t, res.Body.Bytes())
			}
		})
		t.Run(method+"_index_redirect", func(t *testing.T) {
			res := performFrontendRequest(engine, method, "/index.html", "identity")
			assert.Equal(t, http.StatusMovedPermanently, res.Code)
			assert.Equal(t, "./", res.Header().Get("Location"))
			assert.Contains(t, res.Header().Get("Cache-Control"), "no-store")
			assert.Equal(t, "nosniff", res.Header().Get("X-Content-Type-Options"))
		})
	}
}
