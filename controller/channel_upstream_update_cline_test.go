package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)


func allowPrivateClineDiscoveryTestServer(t *testing.T, rawURL string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.Port())

	fetch := system_setting.GetFetchSetting()
	original := *fetch
	t.Cleanup(func() { *fetch = original })

	// httptest necessarily binds loopback and a random high port. Opt in only
	// for this isolated fixture; production defaults remain private-IP denied
	// and port-restricted.
	fetch.EnableSSRFProtection = true
	fetch.AllowPrivateIp = true
	fetch.DomainFilterMode = false
	fetch.IpFilterMode = false
	fetch.DomainList = nil
	fetch.IpList = nil
	fetch.AllowedPorts = []string{parsed.Port()}
	fetch.ApplyIPFilterForDomain = true
}

func TestFetchChannelUpstreamModelIDsClineUsesRecommendedCatalog(t *testing.T) {
	var gotPath string
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"recommended": [{"id": "cline-free/model-b"}],
			"free": [{"id": "cline-free/model-a"}],
			"clinePass": [{"id": "cline-pass/model-a"}],
			"clineCloud": [{"id": "cline-cloud/model-a"}]
		}`))
	}))
	t.Cleanup(upstream.Close)
	allowPrivateClineDiscoveryTestServer(t, upstream.URL)

	baseURL := upstream.URL + "/api/v1"
	channel := &model.Channel{Type: constant.ChannelTypeCline, BaseURL: &baseURL, Key: "secret-cline-key"}
	models, err := fetchChannelUpstreamModelIDs(channel)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"cline-free/model-a",
		"cline-free/model-b",
		"cline-pass/model-a",
		"cline-cloud/model-a",
	}, models, "free models lead the catalog order and prefixes stay untouched")
	assert.Equal(t, "/api/v1/ai/cline/recommended-models", gotPath)
	assert.Empty(t, gotAuth, "the public catalog fetch must not carry the channel credential")
}

func TestFetchChannelUpstreamModelIDsClineSurfacesCatalogFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(upstream.Close)
	allowPrivateClineDiscoveryTestServer(t, upstream.URL)

	baseURL := upstream.URL + "/api/v1"
	channel := &model.Channel{Type: constant.ChannelTypeCline, BaseURL: &baseURL, Key: "secret-cline-key"}
	_, err := fetchChannelUpstreamModelIDs(channel)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status code 401")
	assert.NotContains(t, err.Error(), "secret-cline-key")
}
