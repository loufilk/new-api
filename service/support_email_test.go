package service

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

type supportSMTPMessage struct{ Recipient, Data string }
type supportSMTPFixture struct {
	listener    net.Listener
	messages    chan supportSMTPMessage
	failures    atomic.Int32
	quitFailure bool
	connections atomic.Int32
	wg          sync.WaitGroup
}

func newSupportSMTPFixture(t *testing.T, failures int, quitFailure bool) *supportSMTPFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fixture := &supportSMTPFixture{listener: listener, messages: make(chan supportSMTPMessage, 20), quitFailure: quitFailure}
	fixture.failures.Store(int32(failures))
	fixture.wg.Go(func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			fixture.connections.Add(1)
			fixture.wg.Go(func() { fixture.handle(connection) })
		}
	})
	t.Cleanup(func() { _ = listener.Close(); fixture.wg.Wait() })
	return fixture
}
func (f *supportSMTPFixture) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := textproto.NewReader(bufio.NewReader(conn))
	fmt.Fprint(conn, "220 local test SMTP\r\n")
	recipient := ""
	for {
		line, err := reader.ReadLine()
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			fmt.Fprint(conn, "250 localhost\r\n")
		case strings.HasPrefix(line, "MAIL FROM:"):
			fmt.Fprint(conn, "250 ok\r\n")
		case strings.HasPrefix(line, "RCPT TO:"):
			recipient = strings.TrimPrefix(line, "RCPT TO:")
			fmt.Fprint(conn, "250 ok\r\n")
		case line == "DATA":
			fmt.Fprint(conn, "354 continue\r\n")
			data, err := reader.ReadDotBytes()
			if err != nil {
				return
			}
			f.messages <- supportSMTPMessage{recipient, string(data)}
			if f.failures.Add(-1) >= 0 {
				fmt.Fprint(conn, "451 synthetic temporary failure with secret-provider-text\r\n")
			} else {
				fmt.Fprint(conn, "250 accepted\r\n")
			}
		case line == "QUIT":
			if !f.quitFailure {
				fmt.Fprint(conn, "221 bye\r\n")
			}
			return
		default:
			fmt.Fprint(conn, "250 ok\r\n")
		}
	}
}

func setupSupportEmailService(t *testing.T, fixture *supportSMTPFixture) *model.User {
	t.Helper()
	require.NotEmpty(t, os.Getenv("TEST_SERVICE_POSTGRES_DSN"), "only owned PostgreSQL")
	require.NoError(t, model.DB.AutoMigrate(&model.SupportTicket{}, &model.SupportTicketMessage{}, &model.SupportTicketAttachment{}, &model.SupportTicketSubmission{}, &model.SupportEmailSettings{}, &model.SupportEmailPreference{}, &model.SupportEmailOutbox{}, &model.SupportEmailRate{}))
	for _, table := range []string{"support_ticket_attachments", "support_ticket_submissions", "support_ticket_messages", "support_tickets", "support_email_settings", "support_email_preferences", "support_email_outboxes", "support_email_rates"} {
		require.NoError(t, model.DB.Exec("DELETE FROM "+table).Error)
	}
	require.NoError(t, model.DB.Unscoped().Where("username = ?", "support-mail-fixture").Delete(&model.User{}).Error)
	u := &model.User{Username: "support-mail-fixture", AffCode: "support-mail-fixture", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Email: "customer@example.invalid", EmailVerifiedAddress: "customer@example.invalid", EmailVerificationVersion: 1, EmailVerifiedAt: time.Now().Unix()}
	require.NoError(t, model.DB.Create(u).Error)
	_, port, err := net.SplitHostPort(fixture.listener.Addr().String())
	require.NoError(t, err)
	for key, value := range map[string]string{"EmailProvider": "smtp", "SMTPServer": "127.0.0.1", "SMTPPort": port, "SMTPFrom": "notify@example.invalid", "SMTPAccount": "", "SMTPToken": "", "ResendApiKey": "", "ResendFrom": "", "SMTPSSLEnabled": "false", "SMTPStartTLSEnabled": "false", "SMTPInsecureSkipVerify": "false"} {
		require.NoError(t, model.DB.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.Option{Key: key, Value: value}).Error)
	}
	return u
}

func supportServiceOutbox(t *testing.T) []model.SupportEmailOutbox {
	t.Helper()
	var rows []model.SupportEmailOutbox
	require.NoError(t, model.DB.Order("id ASC").Find(&rows).Error)
	return rows
}
func supportMessageID(data string) string {
	for line := range strings.SplitSeq(data, "\n") {
		if strings.HasPrefix(line, "Message-ID:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Message-ID:"))
		}
	}
	return ""
}

func TestSupportEmailWorkerPostgresSMTPRetryStableIDAndMinimalContent(t *testing.T) {
	smtp := newSupportSMTPFixture(t, 1, false)
	u := setupSupportEmailService(t, smtp)
	require.NoError(t, model.SaveSupportEmailSettings(t.Context(), true, []string{"one@example.invalid", "two@example.invalid"}, "https://trusted.example.invalid"))
	ticket, err := CreateSupportTicket(u.Id, "SECRET title \r\nBcc: leak@example.invalid", "SECRET token sk-private IP 192.0.2.1 <script>alert(1)</script>")
	require.NoError(t, err)
	assert.Zero(t, smtp.connections.Load(), "HTTP-side write never waits on SMTP")
	handled, err := RunSupportEmailOnce(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	first := <-smtp.messages
	assert.Equal(t, "<one@example.invalid>", first.Recipient)
	message, err := mail.ReadMessage(strings.NewReader(first.Data))
	require.NoError(t, err)
	_, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	require.NoError(t, err)
	part, err := multipart.NewReader(message.Body, params["boundary"]).NextPart()
	require.NoError(t, err)
	decoded, err := io.ReadAll(part)
	require.NoError(t, err)
	assert.Contains(t, string(decoded), fmt.Sprintf("https://trusted.example.invalid/support?ticket=%d", ticket.Ticket.ID))
	for _, secret := range []string{"SECRET", "sk-private", "192.0.2.1", "<script>", "two@example.invalid", "Bcc:"} {
		assert.NotContains(t, first.Data, secret)
	}
	assert.Contains(t, first.Data, "text/plain")
	assert.Contains(t, first.Data, "text/html")
	assert.Contains(t, first.Data, "Auto-Submitted: auto-generated")
	rows := supportServiceOutbox(t)
	assert.Equal(t, "retry", rows[0].Status)
	assert.Equal(t, "smtp_temporary", rows[0].LastErrorCode)
	assert.NotContains(t, rows[0].LastErrorCode, "secret-provider-text")
	// Other recipient receives a separate envelope and independent notification.
	handled, err = RunSupportEmailOnce(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	second := <-smtp.messages
	assert.Equal(t, "<two@example.invalid>", second.Recipient)
	assert.NotContains(t, second.Data, "one@example.invalid")
	require.NoError(t, model.DB.Model(&model.SupportEmailOutbox{}).Where("id = ?", rows[0].ID).Update("next_attempt_at", time.Now().Unix()).Error)
	require.NoError(t, model.DB.Model(&model.SupportEmailRate{}).Where(map[string]any{"key": rows[0].RecipientKey}).Update("next_at", 0).Error)
	handled, err = RunSupportEmailOnce(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	retry := <-smtp.messages
	assert.NotEmpty(t, supportMessageID(first.Data))
	assert.Equal(t, supportMessageID(first.Data), supportMessageID(retry.Data))
	assert.Equal(t, "sent", supportServiceOutbox(t)[0].Status)
	detail, err := GetSupportTicket(u.Id, false, ticket.Ticket.ID)
	require.NoError(t, err)
	require.Len(t, detail.Messages, 1)
	assert.EqualValues(t, 3, smtp.connections.Load())
}

func TestSupportEmailWorkerPostgresAcceptedDATAIgnoresQUITFailure(t *testing.T) {
	smtp := newSupportSMTPFixture(t, 0, true)
	u := setupSupportEmailService(t, smtp)
	require.NoError(t, model.SaveSupportEmailSettings(t.Context(), true, []string{"admin@example.invalid"}, "https://trusted.example.invalid"))
	_, err := CreateSupportTicket(u.Id, "subject", "message")
	require.NoError(t, err)
	_, err = RunSupportEmailOnce(t.Context())
	require.NoError(t, err)
	<-smtp.messages
	assert.Equal(t, "sent", supportServiceOutbox(t)[0].Status)
	handled, err := RunSupportEmailOnce(t.Context())
	require.NoError(t, err)
	assert.False(t, handled)
	assert.EqualValues(t, 1, smtp.connections.Load())
}

func TestSupportEmailWorkerPostgresDisabledAndChangedAccountNeverDial(t *testing.T) {
	smtp := newSupportSMTPFixture(t, 0, false)
	u := setupSupportEmailService(t, smtp)
	ticket, err := CreateSupportTicket(u.Id, "subject", "message")
	require.NoError(t, err)
	handled, err := RunSupportEmailOnce(t.Context())
	require.NoError(t, err)
	assert.False(t, handled)
	require.NoError(t, model.SaveSupportEmailSettings(t.Context(), true, []string{"admin@example.invalid"}, "https://trusted.example.invalid"))
	_, err = ReplySupportTicket(999, true, ticket.Ticket.ID, "reply")
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(u).Update("email", "changed@example.invalid").Error)
	_, err = RunSupportEmailOnce(t.Context())
	require.NoError(t, err)
	assert.Zero(t, smtp.connections.Load())
	assert.Equal(t, "cancelled", supportServiceOutbox(t)[0].Status)
}

func TestSupportEmailWorkerPostgresResendSelectionDoesNotFallbackToSMTP(t *testing.T) {
	smtp := newSupportSMTPFixture(t, 0, false)
	u := setupSupportEmailService(t, smtp)
	require.NoError(t, model.SaveSupportEmailSettings(t.Context(), true, []string{"admin@example.invalid"}, "https://trusted.example.invalid"))
	_, err := CreateSupportTicket(u.Id, "subject", "message")
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.Option{}).Where(map[string]any{"key": "EmailProvider"}).Update("value", "resend").Error)
	_, err = RunSupportEmailOnce(t.Context())
	require.NoError(t, err)
	assert.Zero(t, smtp.connections.Load())
	assert.Equal(t, "resend_not_configured", supportServiceOutbox(t)[0].LastErrorCode)
}

func TestSupportEmailSMTPContextCancellationClosesInFlightSocket(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	_, portRaw, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portRaw)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- common.SendQueuedSMTP(ctx, common.SMTPDeliveryConfig{Server: "127.0.0.1", Port: port, From: "notify@example.invalid"}, "notice", "recipient@example.invalid", "safe", "<p>safe</p>", "<stable@test.invalid>")
	}()
	conn := <-accepted
	defer conn.Close()
	cancel()
	require.Error(t, <-done)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = conn.Read(make([]byte, 1))
	require.Error(t, err)
}

func TestSupportEmailMinimalContentEscapesHTMLAndUsesTrustedOrigin(t *testing.T) {
	row := &model.SupportEmailOutbox{ID: 1, TicketID: 42, Event: "admin_reply", RecipientKey: "safe"}
	subject, plain, markup, id := SupportEmailContent(row, &model.SupportEmailAdmission{Origin: "https://trusted.example.invalid", Language: "en"})
	assert.Contains(t, subject, "42")
	assert.Contains(t, plain, "https://trusted.example.invalid/support?ticket=42")
	assert.Contains(t, markup, "href=\"https://trusted.example.invalid/support?ticket=42\"")
	assert.NotContains(t, id, "@trusted.example.invalid")
	_, plainZH, _, _ := SupportEmailContent(row, &model.SupportEmailAdmission{Origin: "https://trusted.example.invalid", Language: "zh-CN"})
	assert.Contains(t, plainZH, "客服已回复")
}

func TestSupportEmailWorkerPostgresProviderSwitchBeforeAdmissionUsesCurrentConfiguration(t *testing.T) {
	smtp := newSupportSMTPFixture(t, 0, false)
	u := setupSupportEmailService(t, smtp)
	require.NoError(t, model.SaveSupportEmailSettings(t.Context(), true, []string{"admin@example.invalid"}, "https://trusted.example.invalid"))
	_, err := CreateSupportTicket(u.Id, "subject", "message")
	require.NoError(t, err)
	sql, err := model.DB.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(8)
	t.Cleanup(func() { sql.SetMaxOpenConns(1) })
	holder := model.DB.Begin()
	require.NoError(t, holder.Error)
	t.Cleanup(func() { holder.Rollback() })
	var id int
	require.NoError(t, holder.Raw("SELECT id FROM users WHERE id = ? FOR UPDATE", u.Id).Scan(&id).Error)
	done := make(chan error, 1)
	go func() { _, err := RunSupportEmailOnce(t.Context()); done <- err }()
	require.Eventually(t, func() bool {
		var count int64
		err := model.DB.Raw(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%FROM "users"%' AND query LIKE '%FOR UPDATE%'`).Scan(&count).Error
		return err == nil && count > 0
	}, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, model.DB.Model(&model.Option{}).Where(map[string]any{"key": "EmailProvider"}).Update("value", "resend").Error)
	require.NoError(t, holder.Commit().Error)
	require.NoError(t, <-done)
	assert.Zero(t, smtp.connections.Load())
	assert.Equal(t, "resend_not_configured", supportServiceOutbox(t)[0].LastErrorCode)
}

func TestSupportEmailSMTPExpiredAdmissionDeadlineNeverDials(t *testing.T) {
	smtp := newSupportSMTPFixture(t, 0, false)
	_, portRaw, err := net.SplitHostPort(smtp.listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portRaw)
	require.NoError(t, err)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	err = common.SendQueuedSMTP(ctx, common.SMTPDeliveryConfig{Server: "127.0.0.1", Port: port, From: "notify@example.invalid"}, "notice", "recipient@example.invalid", "safe", "<p>safe</p>", "<stable@test.invalid>")
	require.Error(t, err)
	assert.Zero(t, smtp.connections.Load())
}

func TestSupportEmailSMTPConfigPreservesExistingUnsavedPortDefault(t *testing.T) {
	smtp := newSupportSMTPFixture(t, 0, false)
	setupSupportEmailService(t, smtp)
	require.NoError(t, model.DB.Where(map[string]any{"key": "SMTPPort"}).Delete(&model.Option{}).Error)
	config, err := readSupportEmailDelivery(t.Context())
	require.NoError(t, err)
	require.True(t, config.configured)
	assert.Equal(t, 587, config.smtp.Port)
	assert.Zero(t, smtp.connections.Load())
}

type supportEmailHTTPTransport func(*http.Request) (*http.Response, error)

func (f supportEmailHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSupportEmailWorkerPostgresResendRetrySingleVerifiedRecipient(t *testing.T) {
	smtp := newSupportSMTPFixture(t, 0, false)
	user := setupSupportEmailService(t, smtp)
	contact := "reference@example.invalid"
	ticket, err := CreateSupportTicketWithOptions(user.Id, SupportTicketCreateInput{Subject: "PRIVATE title", Message: "PRIVATE initial message", ContactEmail: &contact})
	require.NoError(t, err)
	require.NoError(t, model.SaveSupportEmailSettings(t.Context(), true, []string{"admin@example.invalid"}, "https://trusted.example.invalid"))
	for k, v := range map[string]string{"EmailProvider": "resend", "ResendApiKey": "synthetic-key", "ResendFrom": "fake@example.invalid"} {
		require.NoError(t, model.DB.Model(&model.Option{}).Where(map[string]any{"key": k}).Update("value", v).Error)
	}
	var keys []string
	var bodies []string
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = supportEmailHTTPTransport(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, "https://api.resend.com/emails", request.URL.String())
		assert.Equal(t, "Bearer synthetic-key", request.Header.Get("Authorization"))
		assert.Equal(t, http.MethodPost, request.Method)
		deadline, ok := request.Context().Deadline()
		assert.True(t, ok)
		assert.LessOrEqual(t, time.Until(deadline), 20*time.Second)
		keys = append(keys, request.Header.Get("Idempotency-Key"))
		payload, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		bodies = append(bodies, string(payload))
		var msg struct {
			To         []string
			Text, HTML string
			Headers    map[string]string
		}
		require.NoError(t, common.Unmarshal(payload, &msg))
		assert.Equal(t, []string{user.Email}, msg.To)
		assert.Contains(t, msg.Text, fmt.Sprintf("/support?ticket=%d", ticket.Ticket.ID))
		assert.Contains(t, msg.HTML, fmt.Sprintf("/support?ticket=%d", ticket.Ticket.ID))
		assert.Equal(t, "auto-generated", msg.Headers["Auto-Submitted"])
		for _, secret := range []string{"PRIVATE", contact, "admin@example.invalid", "synthetic-key"} {
			assert.NotContains(t, string(payload), secret)
		}
		if len(keys) == 1 {
			return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("private-provider-text"))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"synthetic-id"}`))}, nil
	})
	_, err = ReplySupportTicket(999, true, ticket.Ticket.ID, "PRIVATE reply")
	require.NoError(t, err)
	require.Empty(t, keys)
	handled, err := RunSupportEmailOnce(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	rows := supportServiceOutbox(t)
	require.Len(t, rows, 1)
	assert.Equal(t, "retry", rows[0].Status)
	assert.Equal(t, "resend_rate_limited", rows[0].LastErrorCode)
	require.NoError(t, model.DB.Model(&rows[0]).Update("next_attempt_at", time.Now().Unix()).Error)
	require.NoError(t, model.DB.Model(&model.SupportEmailRate{}).Where(map[string]any{"key": rows[0].RecipientKey}).Update("next_at", 0).Error)
	_, err = RunSupportEmailOnce(t.Context())
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.NotEmpty(t, keys[0])
	assert.Equal(t, keys[0], keys[1])
	assert.Equal(t, bodies[0], bodies[1])
	assert.Equal(t, "sent", supportServiceOutbox(t)[0].Status)
	assert.Zero(t, smtp.connections.Load())
}

func TestSupportEmailWorkerPostgresResendNoReplayAfterIdempotencyWindow(t *testing.T) {
	smtp := newSupportSMTPFixture(t, 0, false)
	u := setupSupportEmailService(t, smtp)
	require.NoError(t, model.SaveSupportEmailSettings(t.Context(), true, []string{"admin@example.invalid"}, "https://trusted.example.invalid"))
	_, err := CreateSupportTicket(u.Id, "subject", "message")
	require.NoError(t, err)
	for k, v := range map[string]string{"EmailProvider": "resend", "ResendApiKey": "test", "ResendFrom": "fake@example.invalid"} {
		require.NoError(t, model.DB.Model(&model.Option{}).Where(map[string]any{"key": k}).Update("value", v).Error)
	}
	rows := supportServiceOutbox(t)
	require.Len(t, rows, 1)
	require.NoError(t, model.DB.Model(&rows[0]).Updates(map[string]any{"attempts": 1, "created_at": time.Now().Add(-25 * time.Hour).Unix()}).Error)
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = supportEmailHTTPTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("stale attempt must not contact the provider")
		return nil, fmt.Errorf("unexpected call")
	})
	handled, err := RunSupportEmailOnce(t.Context())
	require.NoError(t, err)
	assert.True(t, handled)
	got := supportServiceOutbox(t)[0]
	assert.Equal(t, "cancelled", got.Status)
	assert.Equal(t, model.SupportEmailReplayWindowElapsed, got.LastErrorCode)
	require.ErrorIs(t, model.ManageSupportEmail(t.Context(), got.ID, true), model.ErrSupportEmailState)
	assert.Zero(t, smtp.connections.Load())
}
