package chuanapi_test

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func supportImageMultipart(t *testing.T, fields map[string]string, imageCount int) ([]byte, string) {
	t.Helper()
	var body, encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 3, 2))))
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		require.NoError(t, writer.WriteField(key, value))
	}
	for range imageCount {
		part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="images"; filename="private-screenshot.png"`}, "Content-Type": {"image/png"}})
		require.NoError(t, err)
		_, err = part.Write(encoded.Bytes())
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}

func supportMultipartRequest(fixture supportHTTPFixture, path string, userID int, body []byte, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.RemoteAddr = fmt.Sprintf("[2001:db8:%x::77]:12345", fixture.namespace)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Authorization", "Bearer "+fixture.tokens[userID])
	response := httptest.NewRecorder()
	fixture.engine.ServeHTTP(response, request)
	return response
}

func TestSupportTicketAttachmentsPostgresOwnershipMetadataAndIdempotency(t *testing.T) {
	require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
	fixture := newSupportHTTPFixture(t, supportHTTPOptions{globalLimitNum: 1000, criticalLimitNum: 1000, users: []supportHTTPUser{{id: 1, role: common.RoleCommonUser}, {id: 2, role: common.RoleCommonUser}, {id: 3, role: common.RoleAdminUser}}})
	require.NoError(t, fixture.db.Model(&model.User{}).Where("id = ?", fixture.userIDs[1]).Update("email", "login@example.invalid").Error)
	require.NoError(t, model.SaveSupportEmailSettings(t.Context(), true, []string{"admin@example.invalid"}, "https://console.example.invalid"))
	fields := map[string]string{"subject": "Image support", "message": "PRIVATE screenshot context", "category": "api_model", "request_id": uuid.NewString()}
	body, media := supportImageMultipart(t, fields, 4)
	response := supportMultipartRequest(fixture, "/api/support/tickets", 1, body, media)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var created struct {
		Success bool                        `json:"success"`
		Data    service.SupportTicketDetail `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &created))
	require.True(t, created.Success, response.Body.String())
	require.NotNil(t, created.Data.Ticket)
	assert.Equal(t, "api_model", created.Data.Ticket.Category)
	assert.Equal(t, "login@example.invalid", created.Data.Ticket.ContactEmail)
	require.Len(t, created.Data.Messages, 1)
	require.Len(t, created.Data.Messages[0].Attachments, 4)
	assert.NotContains(t, response.Body.String(), "private-screenshot")
	assert.NotContains(t, response.Body.String(), `"Data"`)
	for _, attachment := range created.Data.Messages[0].Attachments {
		path := fmt.Sprintf("/api/support/tickets/%d/attachments/%s", created.Data.Ticket.ID, attachment.ID)
		own := supportHTTPRequest(t, fixture, http.MethodGet, path, "1", 1, nil)
		require.Equal(t, http.StatusOK, own.Code)
		assert.Equal(t, "image/png", own.Header().Get("Content-Type"))
		assert.Equal(t, "nosniff", own.Header().Get("X-Content-Type-Options"))
		assert.Contains(t, own.Header().Get("Cache-Control"), "no-store")
		_, _, err := image.Decode(bytes.NewReader(own.Body.Bytes()))
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, supportHTTPRequest(t, fixture, http.MethodGet, path, "1", 2, nil).Code)
		assert.Equal(t, http.StatusUnauthorized, supportHTTPRequest(t, fixture, http.MethodGet, path, "1", 0, nil).Code)
		adminPath := fmt.Sprintf("/api/admin/support/tickets/%d/attachments/%s", created.Data.Ticket.ID, attachment.ID)
		assert.Equal(t, http.StatusForbidden, supportHTTPRequest(t, fixture, http.MethodGet, adminPath, "1", 1, nil).Code)
		assert.Equal(t, http.StatusOK, supportHTTPRequest(t, fixture, http.MethodGet, adminPath, "1", 3, nil).Code)
		wrongTicket := fmt.Sprintf("/api/support/tickets/%d/attachments/%s", created.Data.Ticket.ID+1, attachment.ID)
		assert.Equal(t, http.StatusNotFound, supportHTTPRequest(t, fixture, http.MethodGet, wrongTicket, "1", 1, nil).Code)
	}
	response = supportMultipartRequest(fixture, "/api/support/tickets", 1, body, media)
	require.Equal(t, http.StatusOK, response.Code)
	var replay struct {
		Data service.SupportTicketDetail `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &replay))
	require.NotNil(t, replay.Data.Ticket)
	assert.Equal(t, created.Data.Ticket.ID, replay.Data.Ticket.ID)
	fields["message"] = "Changed after an uncertain attempt"
	changed, changedMedia := supportImageMultipart(t, fields, 4)
	assert.Equal(t, http.StatusConflict, supportMultipartRequest(fixture, "/api/support/tickets", 1, changed, changedMedia).Code)
	var tickets, messages, attachments, submissions int64
	require.NoError(t, fixture.db.Model(&model.SupportTicket{}).Count(&tickets).Error)
	require.NoError(t, fixture.db.Model(&model.SupportTicketMessage{}).Count(&messages).Error)
	require.NoError(t, fixture.db.Model(&model.SupportTicketAttachment{}).Count(&attachments).Error)
	require.NoError(t, fixture.db.Model(&model.SupportTicketSubmission{}).Count(&submissions).Error)
	assert.EqualValues(t, 1, tickets)
	assert.EqualValues(t, 1, messages)
	assert.EqualValues(t, 4, attachments)
	assert.EqualValues(t, 1, submissions)
	var outboxes []model.SupportEmailOutbox
	require.NoError(t, fixture.db.Find(&outboxes).Error)
	require.Len(t, outboxes, 1, "same submission never queues duplicate email")
	assert.NotContains(t, fmt.Sprint(outboxes[0]), "PRIVATE")
	assert.NotContains(t, fmt.Sprint(outboxes[0]), "login@example.invalid")
	assert.NotContains(t, fmt.Sprint(outboxes[0]), "Image support")
}

func TestSupportTicketAttachmentsPostgresAtomicFailuresAndConcurrentReply(t *testing.T) {
	require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
	fixture := newSupportHTTPFixture(t, supportHTTPOptions{globalLimitNum: 1000, criticalLimitNum: 1000, users: []supportHTTPUser{{id: 1, role: common.RoleCommonUser}, {id: 2, role: common.RoleAdminUser}}})
	for _, fields := range []map[string]string{
		{"subject": "invalid category", "message": "body", "category": "admin"},
		{"subject": "invalid email", "message": "body", "contact_email": "attacker@example.invalid\r\nBcc:x"},
		{"subject": "invalid request", "message": "body", "request_id": "not-a-uuid"},
	} {
		body, media := supportImageMultipart(t, fields, 1)
		response := supportMultipartRequest(fixture, "/api/support/tickets", 1, body, media)
		assert.Contains(t, response.Body.String(), `"success":false`)
	}
	body, media := supportImageMultipart(t, map[string]string{"subject": "Too many", "message": "body"}, 5)
	assert.Equal(t, http.StatusBadRequest, supportMultipartRequest(fixture, "/api/support/tickets", 1, body, media).Code)
	var count int64
	for _, table := range []any{&model.SupportTicket{}, &model.SupportTicketMessage{}, &model.SupportTicketAttachment{}, &model.SupportTicketSubmission{}, &model.SupportEmailOutbox{}} {
		require.NoError(t, fixture.db.Model(table).Count(&count).Error)
		assert.Zero(t, count)
	}
	require.NoError(t, fixture.db.Exec("ALTER TABLE support_ticket_attachments ADD CONSTRAINT fixture_reject_support_image CHECK (size_bytes < 0) NOT VALID").Error)
	body, media = supportImageMultipart(t, map[string]string{"subject": "Atomic", "message": "body", "request_id": uuid.NewString()}, 1)
	response := supportMultipartRequest(fixture, "/api/support/tickets", 1, body, media)
	assert.Contains(t, response.Body.String(), `"success":false`)
	for _, table := range []any{&model.SupportTicket{}, &model.SupportTicketMessage{}, &model.SupportTicketAttachment{}, &model.SupportTicketSubmission{}, &model.SupportEmailOutbox{}} {
		require.NoError(t, fixture.db.Model(table).Count(&count).Error)
		assert.Zero(t, count)
	}
	require.NoError(t, fixture.db.Exec("ALTER TABLE support_ticket_attachments DROP CONSTRAINT fixture_reject_support_image").Error)
	created, err := service.CreateSupportTicket(fixture.userIDs[1], "Concurrent reply", "Original")
	require.NoError(t, err)
	body, media = supportImageMultipart(t, map[string]string{"message": "One reply only", "request_id": uuid.NewString()}, 4)
	path := fmt.Sprintf("/api/support/tickets/%d/replies", created.Ticket.ID)
	sqlDB, err := fixture.db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	var wg sync.WaitGroup
	responses := make([]*httptest.ResponseRecorder, 2)
	for i := range responses {
		wg.Go(func() { responses[i] = supportMultipartRequest(fixture, path, 1, body, media) })
	}
	wg.Wait()
	for _, response := range responses {
		assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"success":true`)
	}
	require.NoError(t, fixture.db.Model(&model.SupportTicketMessage{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	require.NoError(t, fixture.db.Model(&model.SupportTicketAttachment{}).Count(&count).Error)
	assert.EqualValues(t, 4, count)
	_, err = service.SetSupportTicketStatus(created.Ticket.ID, model.SupportTicketStatusResolved)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, supportMultipartRequest(fixture, path, 1, body, media).Code, "an acknowledged replay survives later resolution")
	body, media = supportImageMultipart(t, map[string]string{"message": "New reply after resolution", "request_id": uuid.NewString()}, 1)
	response = supportMultipartRequest(fixture, path, 1, body, media)
	assert.Contains(t, response.Body.String(), `"success":false`)
	require.NoError(t, fixture.db.Model(&model.SupportTicketAttachment{}).Count(&count).Error)
	assert.EqualValues(t, 4, count)
}

func TestSupportTicketAttachmentsPostgresContactNeverRedirectsVerifiedNotifications(t *testing.T) {
	require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
	fixture := newSupportHTTPFixture(t, supportHTTPOptions{globalLimitNum: 1000, criticalLimitNum: 1000, users: []supportHTTPUser{{id: 1, role: common.RoleCommonUser}, {id: 2, role: common.RoleAdminUser}}})
	require.NoError(t, fixture.db.Model(&model.User{}).Where("id = ?", fixture.userIDs[1]).Updates(map[string]any{"email": "verified@example.invalid", "email_verified_address": "verified@example.invalid", "email_verified_at": time.Now().Unix(), "email_verification_version": 1}).Error)
	require.NoError(t, model.SaveSupportEmailSettings(t.Context(), true, []string{"admin@example.invalid"}, "https://console.example.invalid"))
	contact := "unverified-thirdparty@example.invalid"
	created, err := service.CreateSupportTicketWithOptions(fixture.userIDs[1], service.SupportTicketCreateInput{Subject: "PRIVATE title", Message: "PRIVATE body", Category: "account_access", ContactEmail: &contact, RequestID: uuid.NewString()})
	require.NoError(t, err)
	_, err = service.ReplySupportTicket(fixture.userIDs[2], true, created.Ticket.ID, "PRIVATE response")
	require.NoError(t, err)
	var outboxes []model.SupportEmailOutbox
	require.NoError(t, fixture.db.Order("id").Find(&outboxes).Error)
	require.Len(t, outboxes, 2)
	assert.Equal(t, "admin@example.invalid", outboxes[0].Recipient)
	assert.Equal(t, "verified@example.invalid", outboxes[1].Recipient)
	for _, row := range outboxes {
		assert.NotContains(t, fmt.Sprint(row), contact)
		assert.NotContains(t, fmt.Sprint(row), "PRIVATE")
	}
	blank := ""
	without, err := service.CreateSupportTicketWithOptions(fixture.userIDs[1], service.SupportTicketCreateInput{Subject: "No contact", Message: "body", ContactEmail: &blank})
	require.NoError(t, err)
	assert.Empty(t, without.Ticket.ContactEmail)
	var unchanged model.User
	require.NoError(t, fixture.db.First(&unchanged, fixture.userIDs[1]).Error)
	assert.Equal(t, "verified@example.invalid", unchanged.Email)
	assert.True(t, model.VerifiedSupportEmail(&unchanged), "contact reference must never modify identity or proof")
}

func TestSupportTicketAttachmentsPostgresSessionOnlyAndRevokedSession(t *testing.T) {
	require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
	fixture := newSupportHTTPFixture(t, supportHTTPOptions{globalLimitNum: 1000, criticalLimitNum: 1000, users: []supportHTTPUser{{id: 1, role: common.RoleCommonUser}, {id: 2, role: common.RoleRootUser}}})
	require.NoError(t, fixture.db.AutoMigrate(&model.UserAccessToken{}))
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	body, media := supportImageMultipart(t, map[string]string{"subject": "Private", "message": "Private image"}, 1)
	response := supportMultipartRequest(fixture, "/api/support/tickets", 1, body, media)
	require.Equal(t, http.StatusOK, response.Code)
	var created struct {
		Data service.SupportTicketDetail `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &created))
	require.NotNil(t, created.Data.Ticket)
	require.Len(t, created.Data.Messages, 1)
	require.Len(t, created.Data.Messages[0].Attachments, 1)
	path := fmt.Sprintf("/api/support/tickets/%d/attachments/%s", created.Data.Ticket.ID, created.Data.Messages[0].Attachments[0].ID)
	adminPath := strings.Replace(path, "/api/support/", "/api/admin/support/", 1)
	for _, logicalID := range []int{1, 2} {
		legacy := fmt.Sprintf("synthetic-support-legacy-%d", logicalID)
		require.NoError(t, fixture.db.Model(&model.User{}).Where("id = ?", fixture.userIDs[logicalID]).Update("access_token", legacy).Error)
		suffix, err := common.GenerateRandomCharsKey(43)
		require.NoError(t, err)
		raw := model.AccessTokenPrefix + suffix
		pat := &model.UserAccessToken{Name: "synthetic support", TokenHash: model.AccessTokenFingerprint(raw), TokenHint: model.AccessTokenHint(raw)}
		require.NoError(t, pat.SetScopes([]string{"profile:read"}))
		require.NoError(t, model.CreateUserAccessToken(fixture.userIDs[logicalID], pat, service.AccessTokenMaxPerUser))
		original := fixture.tokens[logicalID]
		for _, token := range []string{legacy, raw} {
			fixture.tokens[logicalID] = token
			for _, target := range []string{path, adminPath} {
				denied := supportHTTPRequest(t, fixture, http.MethodGet, target, "1", logicalID, nil)
				assert.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
				assert.NotContains(t, denied.Body.String(), "PRIVATE")
				assert.NotEqual(t, "image/png", denied.Header().Get("Content-Type"))
			}
			denied := supportMultipartRequest(fixture, "/api/support/tickets", logicalID, body, media)
			assert.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
			for _, mailRoute := range []struct{ method, path string }{
				{http.MethodGet, "/api/support-email/preferences"}, {http.MethodPut, "/api/support-email/preferences"},
				{http.MethodGet, "/api/support-email/settings"}, {http.MethodPut, "/api/support-email/settings"},
				{http.MethodGet, "/api/support-email/outbox"}, {http.MethodPost, "/api/support-email/outbox/1/retry"}, {http.MethodPost, "/api/support-email/outbox/1/cancel"},
			} {
				deniedMail := supportHTTPRequest(t, fixture, mailRoute.method, mailRoute.path, "1", logicalID, map[string]any{"enabled": true})
				assert.Equal(t, http.StatusForbidden, deniedMail.Code, mailRoute.path+": "+deniedMail.Body.String())
				assert.Contains(t, deniedMail.Body.String(), "AUTH_SESSION_REQUIRED")
			}
		}
		fixture.tokens[logicalID] = original
	}
	revoked, err := model.RevokeUserSession(fixture.userIDs[1], fmt.Sprintf("support-session-%d", fixture.userIDs[1]), "synthetic revocation")
	require.NoError(t, err)
	require.True(t, revoked)
	assert.Equal(t, http.StatusUnauthorized, supportHTTPRequest(t, fixture, http.MethodGet, path, "1", 1, nil).Code)
}

func TestSupportTicketAttachmentsPostgresUpgradeOldRowsAndRepeatMigration(t *testing.T) {
	require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
	fixture := newSupportHTTPFixture(t, supportHTTPOptions{globalLimitNum: 1000, criticalLimitNum: 1000, users: []supportHTTPUser{{id: 1, role: common.RoleCommonUser}}})
	old, err := service.CreateSupportTicket(fixture.userIDs[1], "Original ticket", "Preserved before upgrade")
	require.NoError(t, err)
	require.NoError(t, fixture.db.Migrator().DropColumn(&model.SupportTicket{}, "Category"))
	require.NoError(t, fixture.db.Migrator().DropColumn(&model.SupportTicket{}, "ContactEmail"))
	require.NoError(t, fixture.db.Migrator().DropTable(&model.SupportTicketAttachment{}, &model.SupportTicketSubmission{}))
	for restart := 0; restart < 2; restart++ {
		require.NoError(t, fixture.db.AutoMigrate(&model.SupportTicket{}, &model.SupportTicketMessage{}, &model.SupportTicketAttachment{}, &model.SupportTicketSubmission{}))
		detail, err := service.GetSupportTicket(fixture.userIDs[1], false, old.Ticket.ID)
		require.NoError(t, err)
		assert.Equal(t, old.Ticket.Subject, detail.Ticket.Subject)
		assert.Empty(t, detail.Ticket.Category)
		assert.Empty(t, detail.Ticket.ContactEmail)
		require.Len(t, detail.Messages, 1)
		assert.Equal(t, "Preserved before upgrade", detail.Messages[0].Content)
		assert.Empty(t, detail.Messages[0].Attachments)
	}
	body, media := supportImageMultipart(t, map[string]string{"message": "After upgrade", "request_id": uuid.NewString()}, 4)
	response := supportMultipartRequest(fixture, fmt.Sprintf("/api/support/tickets/%d/replies", old.Ticket.ID), 1, body, media)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"success":true`)
	detail, err := service.GetSupportTicket(fixture.userIDs[1], false, old.Ticket.ID)
	require.NoError(t, err)
	require.Len(t, detail.Messages, 2)
	assert.Len(t, detail.Messages[1].Attachments, 4)
}

func TestSupportTicketAttachmentsPostgresRejectMalformedMultipartWithoutOrphans(t *testing.T) {
	require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
	fixture := newSupportHTTPFixture(t, supportHTTPOptions{globalLimitNum: 1000, criticalLimitNum: 1000, users: []supportHTTPUser{{id: 1, role: common.RoleCommonUser}}})
	for _, test := range []struct {
		name, filename, media string
		data                  []byte
		duplicate, unknown    bool
	}{
		{name: "svg", filename: "image.svg", media: "image/svg+xml", data: []byte(`<svg onload="alert(1)"/>`)},
		{name: "spoof", filename: "image.png", media: "image/png", data: []byte(`<html>PRIVATE</html>`)},
		{name: "too large", filename: "image.png", media: "image/png", data: make([]byte, service.SupportTicketImageMaxBytes+1)},
		{name: "duplicate field", duplicate: true}, {name: "unknown field", unknown: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			require.NoError(t, writer.WriteField("subject", "Rejected"))
			require.NoError(t, writer.WriteField("message", "No orphan"))
			if test.duplicate {
				require.NoError(t, writer.WriteField("subject", "second"))
			}
			if test.unknown {
				require.NoError(t, writer.WriteField("user_id", "999"))
			}
			if test.filename != "" {
				part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {fmt.Sprintf(`form-data; name="images"; filename="%s"`, test.filename)}, "Content-Type": {test.media}})
				require.NoError(t, err)
				_, err = part.Write(test.data)
				require.NoError(t, err)
			}
			require.NoError(t, writer.Close())
			response := supportMultipartRequest(fixture, "/api/support/tickets", 1, body.Bytes(), writer.FormDataContentType())
			assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "PRIVATE")
		})
	}
	var count int64
	for _, table := range []any{&model.SupportTicket{}, &model.SupportTicketMessage{}, &model.SupportTicketAttachment{}, &model.SupportTicketSubmission{}, &model.SupportEmailOutbox{}} {
		require.NoError(t, fixture.db.Model(table).Count(&count).Error)
		assert.Zero(t, count)
	}
}
