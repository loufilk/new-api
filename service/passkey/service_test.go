package passkey

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Passkey RP ID / Origin 与 chuanapi.com 主域对齐（P0-7）
// 目标：RPID = chuanapi.com，origin = https://chuanapi.com；
// 配置了显式 Origins 时，请求来自其他 Host 也不会被纳入信任源。
// ---------------------------------------------------------------------------

func TestResolvePasskeyRPIDAndOriginsChuanapiAutoDetect(t *testing.T) {
	settings := &system_setting.PasskeySettings{AllowInsecureOrigin: false}
	r := httptest.NewRequest(http.MethodGet, "https://chuanapi.com/sign-in", nil)

	origins, err := resolveOrigins(r, settings)
	require.NoError(t, err)
	assert.Equal(t, []string{"https://chuanapi.com"}, origins)

	rpID, err := resolveRPID(r, settings, origins)
	require.NoError(t, err)
	assert.Equal(t, "chuanapi.com", rpID)
}

func TestResolvePasskeyRPIDAndOriginsChuanapiExplicitConfig(t *testing.T) {
	settings := &system_setting.PasskeySettings{
		AllowInsecureOrigin: false,
		RPID:                "chuanapi.com",
		Origins:             "https://chuanapi.com",
	}
	// 即使请求来自其他 Host，显式配置的信任源保持不变，错误源被排除
	r := httptest.NewRequest(http.MethodGet, "https://evil.example.com/phish", nil)

	origins, err := resolveOrigins(r, settings)
	require.NoError(t, err)
	assert.Equal(t, []string{"https://chuanapi.com"}, origins)
	assert.NotContains(t, origins, "https://evil.example.com")

	rpID, err := resolveRPID(r, settings, origins)
	require.NoError(t, err)
	assert.Equal(t, "chuanapi.com", rpID)
}

func TestResolvePasskeyFromServerAddressChuanapi(t *testing.T) {
	previous := system_setting.ServerAddress
	system_setting.ServerAddress = "https://chuanapi.com"
	t.Cleanup(func() { system_setting.ServerAddress = previous })

	settings := &system_setting.PasskeySettings{AllowInsecureOrigin: false}
	// 无 Host 且代理标记 HTTPS 的请求（如内部回调）回退 ServerAddress
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = ""
	r.Header.Set("X-Forwarded-Proto", "https")

	origins, err := resolveOrigins(r, settings)
	require.NoError(t, err)
	assert.Equal(t, []string{"https://chuanapi.com"}, origins)
}

func TestResolvePasskeyRejectsInsecureOriginByDefault(t *testing.T) {
	settings := &system_setting.PasskeySettings{
		AllowInsecureOrigin: false,
		Origins:             "http://chuanapi.com",
	}
	r := httptest.NewRequest(http.MethodGet, "http://chuanapi.com/", nil)
	origins, err := resolveOrigins(r, settings)
	require.Error(t, err)
	assert.Empty(t, origins)
}

func TestResolvePasskeyAutoDetectRejectsPlainHTTPPublicHost(t *testing.T) {
	settings := &system_setting.PasskeySettings{AllowInsecureOrigin: false}
	r := httptest.NewRequest(http.MethodGet, "http://chuanapi.com/", nil)
	_, err := resolveOrigins(r, settings)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTPS")
}

func TestBuildWebAuthnChuanapiDefaults(t *testing.T) {
	previous := system_setting.ServerAddress
	previousSettings := *system_setting.GetPasskeySettings()
	system_setting.ServerAddress = "https://chuanapi.com"
	t.Cleanup(func() {
		system_setting.ServerAddress = previous
		*system_setting.GetPasskeySettings() = previousSettings
	})

	// RPID/Origins 未配置时从 ServerAddress 推导
	defaults := system_setting.GetPasskeySettings()
	defaults.RPID = ""
	defaults.Origins = ""

	r := httptest.NewRequest(http.MethodGet, "https://chuanapi.com/sign-in", nil)
	authn, err := BuildWebAuthn(r)
	require.NoError(t, err)
	require.NotNil(t, authn)
	assert.Equal(t, "chuanapi.com", authn.Config.RPID)
	require.Len(t, authn.Config.RPOrigins, 1)
	assert.Equal(t, "https://chuanapi.com", authn.Config.RPOrigins[0])
}
