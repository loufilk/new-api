package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGooglePoolSettingsRemainIndependentAndPreserveAnnouncements(t *testing.T) {
	announcement := "Existing notice\n\n![Schedule](https://images.example.invalid/schedule.png)"
	announcements, err := common.Marshal([]map[string]any{{
		"content": announcement, "publishDate": "2026-11-01T12:00:00Z", "type": "default",
	}})
	require.NoError(t, err)
	for _, state := range []struct {
		name          string
		update        string
		googleBefore  bool
		poolBefore    bool
		googleEnabled bool
		poolEnabled   bool
	}{
		{name: "missing settings deny both features"},
		{name: "Google enable leaves pool disabled", update: `{"key":"GoogleOAuthEnabled","value":true}`, googleEnabled: true},
		{name: "pool enable preserves Google", update: `{"key":"AccountPoolEnabled","value":true}`, googleBefore: true, googleEnabled: true, poolEnabled: true},
		{name: "Google disable preserves pool", update: `{"key":"GoogleOAuthEnabled","value":false}`, googleBefore: true, poolBefore: true, poolEnabled: true},
		{name: "pool disable leaves Google disabled", update: `{"key":"AccountPoolEnabled","value":false}`, poolBefore: true},
		{name: "pool disable preserves Google", update: `{"key":"AccountPoolEnabled","value":false}`, googleBefore: true, poolBefore: true, googleEnabled: true},
	} {
		t.Run(state.name, func(t *testing.T) {
			fixture := setupGoogleOAuthTest(t)
			common.GoogleOAuthEnabled = state.googleBefore
			if state.poolBefore {
				require.NoError(t, model.UpdateOption(model.AccountPoolEnabledKey, "true"))
			}
			previousConsole := *console_setting.GetConsoleSetting()
			t.Cleanup(func() { *console_setting.GetConsoleSetting() = previousConsole })
			console_setting.GetConsoleSetting().Announcements = string(announcements)
			console_setting.GetConsoleSetting().AnnouncementsEnabled = true
			if state.update != "" {
				updated := securityEnrollmentRequest(http.MethodPut, "/api/option/", state.update, "", fixture.identity, UpdateOption)
				require.Contains(t, updated.Body.String(), `"success":true`)
			}
			response := securityEnrollmentRequest(http.MethodGet, "/api/status", "", "", service.AuthIdentity{}, GetStatus)
			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			var status struct {
				Success bool `json:"success"`
				Data    struct {
					GoogleEnabled        bool             `json:"google_oauth"`
					GoogleConfigured     bool             `json:"google_oauth_configured"`
					PoolEnabled          bool             `json:"account_pool_enabled"`
					AnnouncementsEnabled bool             `json:"announcements_enabled"`
					Announcements        []map[string]any `json:"announcements"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &status))
			require.True(t, status.Success)
			assert.Equal(t, state.googleEnabled, status.Data.GoogleEnabled)
			assert.Equal(t, state.googleEnabled, status.Data.GoogleConfigured)
			assert.Equal(t, state.poolEnabled, status.Data.PoolEnabled)
			assert.True(t, status.Data.AnnouncementsEnabled)
			require.Len(t, status.Data.Announcements, 1)
			assert.Equal(t, announcement, status.Data.Announcements[0]["content"])
			assert.NotContains(t, response.Body.String(), "google-client-secret")
			if state.update == "" {
				var count int64
				require.NoError(t, model.DB.Model(&model.Option{}).Where("key IN ?", []string{"GoogleOAuthEnabled", model.AccountPoolEnabledKey}).Count(&count).Error)
				assert.Zero(t, count, "status reads must not persist opt-in defaults")
			}
			fixture.mutex.Lock()
			defer fixture.mutex.Unlock()
			assert.Zero(t, fixture.exchanges, "independent settings and status requests never exchange a Google code")
		})
	}
}
