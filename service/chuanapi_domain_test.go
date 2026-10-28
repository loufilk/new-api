package service

import (
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

// ---------------------------------------------------------------------------
// P0-7：refresh/session Cookie 必须按同源主域生效（chuanapi.com），
// 不得写 Domain 到 api.chuanapi.com，也不得泄漏到子域。
// ---------------------------------------------------------------------------

func TestWriteRefreshCookieChuanapiDomainNoSubdomainLeak(t *testing.T) {
	previousSecure := common.SessionCookieSecure
	common.SessionCookieSecure = true
	t.Cleanup(func() { common.SessionCookieSecure = previousSecure })

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "https://chuanapi.com/api/user/auth/refresh", nil)

	WriteRefreshCookie(c, "not-a-valid-token")

	cookies := w.Result().Cookies()
	require.NotEmpty(t, cookies)
	var refresh *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == RefreshCookieName && cookie.Path == "/api/user" {
			refresh = cookie
		}
	}
	require.NotNil(t, refresh, "refresh cookie must be written at Path=/api/user")
	assert.Empty(t, refresh.Domain, "cookie must be host-only (no Domain) so it works on chuanapi.com and not api.chuanapi.com")
	assert.Equal(t, "/api/user", refresh.Path)
	assert.True(t, refresh.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, refresh.SameSite)
	assert.True(t, refresh.Secure)

	// 旧路径清理：/api/user/auth 下的过期 cookie 仍在（兼容旧会话）
	foundLegacyCleanup := false
	for _, cookie := range cookies {
		if cookie.Name == RefreshCookieName && cookie.Path == "/api/user/auth" && cookie.MaxAge < 0 {
			foundLegacyCleanup = true
		}
	}
	assert.True(t, foundLegacyCleanup, "legacy narrow-path cookie must be expired")
}

func TestWriteRefreshCookiePathIsConsoleOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "https://chuanapi.com/api/user/auth/refresh", nil)

	WriteRefreshCookie(c, "not-a-valid-token")

	var paths []string
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == RefreshCookieName {
			paths = append(paths, cookie.Path)
		}
	}
	assert.ElementsMatch(t, []string{"/api/user", "/api/user/auth"}, paths,
		"refresh cookie must not be written to root or other paths")
}

// ---------------------------------------------------------------------------
// P0-7：公共服务基址统一由 ServerAddress 生成（目标值 https://chuanapi.com）
// ---------------------------------------------------------------------------

func TestPaymentReturnURLCanonicalChuanapi(t *testing.T) {
	previous := system_setting.ServerAddress
	system_setting.ServerAddress = "https://chuanapi.com"
	t.Cleanup(func() { system_setting.ServerAddress = previous })

	assert.Equal(t, "https://chuanapi.com/wallet", PaymentReturnURL("/wallet"))
	assert.Equal(t, "https://chuanapi.com/", PaymentReturnURL("/"))
	// 尾斜杠归一化：ServerAddress 带 / 不应产生 //wallet
	system_setting.ServerAddress = "https://chuanapi.com/"
	assert.Equal(t, "https://chuanapi.com/wallet", PaymentReturnURL("/wallet"))
	assert.NotContains(t, PaymentReturnURL("/wallet"), "//wallet")
	assert.False(t, strings.Contains(PaymentReturnURL("/wallet"), "api.chuanapi.com"),
		"payment return URL must not point at the legacy api subdomain")
}
