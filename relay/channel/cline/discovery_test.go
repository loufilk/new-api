package cline

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func allowPrivateClineDiscoveryForTest(t *testing.T) {
	t.Helper()
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() { *fetchSetting = original })

	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = true
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = nil
	fetchSetting.ApplyIPFilterForDomain = true
}

func TestRecommendedModelsURL(t *testing.T) {
	for _, tc := range []struct {
		name     string
		baseURL  string
		expected string
		wantErr  bool
	}{
		{name: "default", baseURL: "", expected: "https://api.cline.bot/api/v1/ai/cline/recommended-models"},
		{name: "explicit", baseURL: DefaultBaseURL, expected: "https://api.cline.bot/api/v1/ai/cline/recommended-models"},
		{name: "trailing slash", baseURL: "https://api.cline.bot/api/v1/", expected: "https://api.cline.bot/api/v1/ai/cline/recommended-models"},
		{name: "custom prefix", baseURL: "https://cline-proxy.example/base/", expected: "https://cline-proxy.example/base/ai/cline/recommended-models"},
		{name: "drops query", baseURL: "https://proxy.example/api/v1?token=leak", expected: "https://proxy.example/api/v1/ai/cline/recommended-models"},
		{name: "invalid scheme", baseURL: "ftp://api.cline.bot", wantErr: true},
		{name: "missing host", baseURL: "https://", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RecommendedModelsURL(tc.baseURL)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestFetchRecommendedModelIDsMergesAllGroupsInOrder(t *testing.T) {
	allowPrivateClineDiscoveryForTest(t)
	var gotPath string
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"recommended": [{"id": "cline-free/deepseek-v4.1-flash"}, {"id": "recommended-model"}, {"id": ""}],
			"free": [{"id": "cline-free/deepseek-v4.1-flash"}, {"id": "free-model"}],
			"clinePass": [{"id": "cline-pass/deepseek-v4.1-flash"}, {"id": " free-model "}],
			"clineCloud": [{"id": "cline-cloud/deepseek-v4.1-flash"}, {"id": "cline-pass/deepseek-v4.1-flash"}]
		}`))
	}))
	t.Cleanup(upstream.Close)

	models, err := FetchRecommendedModelIDs(upstream.URL+"/api/v1", "")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"cline-free/deepseek-v4.1-flash",
		"free-model",
		"recommended-model",
		"cline-pass/deepseek-v4.1-flash",
		"cline-cloud/deepseek-v4.1-flash",
	}, models)
	assert.Equal(t, "/api/v1/ai/cline/recommended-models", gotPath)
	assert.Empty(t, gotAuth, "the public catalog must not receive channel credentials")
}

func TestFetchRecommendedModelIDsErrors(t *testing.T) {
	allowPrivateClineDiscoveryForTest(t)
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		expectedErr string
	}{
		{name: "non 200", status: http.StatusUnauthorized, body: `{"error":"nope"}`, expectedErr: "status code 401"},
		{name: "malformed", status: http.StatusOK, body: `{not json`, expectedErr: "invalid cline recommended-models response"},
		{name: "wrong shape", status: http.StatusOK, body: `[]`, expectedErr: "invalid cline recommended-models response"},
		{name: "empty groups", status: http.StatusOK, body: `{"recommended":[],"free":[],"clinePass":[],"clineCloud":[]}`, expectedErr: "contains no model IDs"},
		{name: "blank ids", status: http.StatusOK, body: `{"recommended":[{"id":" "}],"free":[{"id":""}]}`, expectedErr: "contains no model IDs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(upstream.Close)
			_, err := FetchRecommendedModelIDs(upstream.URL, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

func TestFetchRecommendedModelIDsRejectsOversizedCatalog(t *testing.T) {
	allowPrivateClineDiscoveryForTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"free":[{"id":"` + strings.Repeat("a", recommendedModelsMaxBytes) + `"}]}`))
	}))
	t.Cleanup(upstream.Close)
	_, err := FetchRecommendedModelIDs(upstream.URL, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestFetchRecommendedModelIDsRejectsCrossHostRedirect(t *testing.T) {
	allowPrivateClineDiscoveryForTest(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"free":[{"id":"stolen"}]}`))
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	_, err := FetchRecommendedModelIDs(redirector.URL, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redirect to a different host")
}

func TestFetchRecommendedModelIDsRejectsPrivateTargetByDefault(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() { *fetchSetting = original })
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = nil
	fetchSetting.ApplyIPFilterForDomain = true

	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"free":[{"id":"must-not-be-read"}]}`))
	}))
	t.Cleanup(upstream.Close)

	_, err := FetchRecommendedModelIDs(upstream.URL, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "private IP address not allowed")
	assert.False(t, called, "blocked private discovery must fail before contacting the target")
}
