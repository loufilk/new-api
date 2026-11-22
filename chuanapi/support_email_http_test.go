package chuanapi_test

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSupportEmailHTTPPostgresRootPermissionsSettingsAndSafeDiagnostics(t *testing.T) {
	require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
	fixture := newSupportHTTPFixture(t, supportHTTPOptions{globalLimitNum: 1000, criticalLimitNum: 1000, users: []supportHTTPUser{{id: 101, role: common.RoleCommonUser}, {id: 102, role: common.RoleAdminUser}, {id: 103, role: common.RoleRootUser}}})
	for _, path := range []string{"/api/support-email/settings", "/api/support-email/outbox"} {
		assert.Equal(t, http.StatusUnauthorized, supportHTTPRequest(t, fixture, http.MethodGet, path, "1", 0, nil).Code)
		for _, id := range []int{101, 102} {
			assert.Equal(t, http.StatusForbidden, supportHTTPRequest(t, fixture, http.MethodGet, path, "1", id, nil).Code)
		}
	}
	initial := supportHTTPRequest(t, fixture, http.MethodGet, "/api/support-email/settings", "1", 103, nil)
	assert.Contains(t, initial.Body.String(), `"enabled":false`)
	assert.Contains(t, initial.Body.String(), `"smtp_configured":false`)
	settings := map[string]any{"enabled": true, "admin_recipients": []string{"admin@example.invalid"}, "site_origin": "https://trusted.example.invalid"}
	response := supportHTTPRequest(t, fixture, http.MethodPut, "/api/support-email/settings", "1", 103, settings)
	assert.Contains(t, response.Body.String(), `"success":false`)
	for key, value := range map[string]string{"SMTPServer": "127.0.0.1", "SMTPPort": "1025", "SMTPFrom": "notify@example.invalid", "SMTPToken": "private-not-real-token", "SMTPAccount": "notify@example.invalid"} {
		require.NoError(t, fixture.db.Create(&model.Option{Key: key, Value: value}).Error)
	}
	response = supportHTTPRequest(t, fixture, http.MethodPut, "/api/support-email/settings", "1", 103, settings)
	assert.Contains(t, response.Body.String(), `"success":true`, response.Body.String())
	response = supportHTTPRequest(t, fixture, http.MethodGet, "/api/support-email/settings", "1", 103, nil)
	assert.NotContains(t, response.Body.String(), "private-not-real-token")
	assert.Contains(t, response.Body.String(), `"smtp_configured":true`)
	ticket, err := service.CreateSupportTicket(fixture.userIDs[101], "private title", "private message")
	require.NoError(t, err)
	response = supportHTTPRequest(t, fixture, http.MethodGet, "/api/support-email/outbox?p=1&page_size=20", "1", 103, nil)
	assert.Contains(t, response.Body.String(), `"total":1`)
	assert.Contains(t, response.Body.String(), fmt.Sprintf(`"ticket_id":%d`, ticket.Ticket.ID))
	for _, secret := range []string{"admin@example.invalid", "recipient", "private message", "private title", "lease_token"} {
		assert.NotContains(t, response.Body.String(), secret)
	}
	var row model.SupportEmailOutbox
	require.NoError(t, fixture.db.First(&row).Error)
	response = supportHTTPRequest(t, fixture, http.MethodPost, fmt.Sprintf("/api/support-email/outbox/%d/retry", row.ID), "1", 103, nil)
	assert.Contains(t, response.Body.String(), `"success":false`)
	require.NoError(t, fixture.db.Model(&row).Update("status", "dead").Error)
	response = supportHTTPRequest(t, fixture, http.MethodPost, fmt.Sprintf("/api/support-email/outbox/%d/retry", row.ID), "1", 103, nil)
	assert.Contains(t, response.Body.String(), `"success":true`)
	response = supportHTTPRequest(t, fixture, http.MethodPost, fmt.Sprintf("/api/support-email/outbox/%d/cancel", row.ID), "1", 103, nil)
	assert.Contains(t, response.Body.String(), `"success":true`)
	var audits []model.AuditLog
	require.NoError(t, fixture.db.Where("action LIKE ?", "support_email.%").Find(&audits).Error)
	require.GreaterOrEqual(t, len(audits), 4)
	for _, audit := range audits {
		assert.NotContains(t, audit.Content, "@example.invalid")
		assert.NotContains(t, audit.Content, "private")
	}
}

func TestSupportEmailHTTPPostgresOwnPreferenceOnlyAndMalformedInput(t *testing.T) {
	require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
	fixture := newSupportHTTPFixture(t, supportHTTPOptions{globalLimitNum: 1000, criticalLimitNum: 1000, users: []supportHTTPUser{{id: 201, role: common.RoleCommonUser}, {id: 202, role: common.RoleCommonUser}, {id: 203, role: common.RoleRootUser}}})
	require.NoError(t, fixture.db.Model(&model.User{}).Where("id = ?", fixture.userIDs[201]).Updates(map[string]any{"email": "own@example.invalid", "email_verified_address": "own@example.invalid", "email_verified_at": time.Now().Unix(), "email_verification_version": 1}).Error)
	response := supportHTTPRequest(t, fixture, http.MethodGet, "/api/support-email/preferences?user_id="+fmt.Sprint(fixture.userIDs[201]), "1", 202, nil)
	assert.NotContains(t, response.Body.String(), "own@example.invalid")
	assert.Contains(t, response.Body.String(), `"email_verified":false`)
	response = supportHTTPRequest(t, fixture, http.MethodGet, "/api/support-email/preferences", "1", 201, nil)
	assert.Contains(t, response.Body.String(), `"email_verified":true`)
	response = supportHTTPRequest(t, fixture, http.MethodPut, "/api/support-email/preferences", "1", 201, map[string]any{"enabled": false, "user_id": fixture.userIDs[202]})
	assert.Contains(t, response.Body.String(), `"success":true`)
	preference, err := model.ReadSupportEmailPreference(t.Context(), fixture.userIDs[202])
	require.NoError(t, err)
	assert.False(t, preference.Disabled)
	for _, body := range []map[string]any{{}, {"enabled": "false"}, {"enabled": nil}} {
		response = supportHTTPRequest(t, fixture, http.MethodPut, "/api/support-email/preferences", "1", 201, body)
		assert.Contains(t, response.Body.String(), `"success":false`)
	}
	response = supportHTTPRequest(t, fixture, http.MethodPut, "/api/support-email/settings", "1", 203, map[string]any{"enabled": false, "admin_recipients": []string{"admin@example.invalid\r\nBcc:attacker"}, "site_origin": "https://trusted.example.invalid"})
	assert.Contains(t, response.Body.String(), `"success":false`)
}
