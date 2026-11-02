package model

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSupportEmailPostgres(t *testing.T) *User {
	t.Helper()
	require.NotEmpty(t, os.Getenv("TEST_MODEL_POSTGRES_DSN"), "only owned PostgreSQL is permitted")
	setupGoogleIdentityPostgres(t)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&SupportTicket{}, &SupportTicketMessage{}, &SupportTicketAttachment{}, &SupportTicketSubmission{}, &SupportEmailSettings{}, &SupportEmailPreference{}, &SupportEmailOutbox{}, &SupportEmailRate{}))
	}
	for _, table := range []string{"support_ticket_attachments", "support_ticket_submissions", "support_ticket_messages", "support_tickets", "support_email_settings", "support_email_preferences", "support_email_outboxes", "support_email_rates"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	u := &User{Username: "support-owner", AffCode: "support-owner", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Email: "owner@example.invalid", EmailVerifiedAddress: "owner@example.invalid", EmailVerificationVersion: 1, EmailVerifiedAt: time.Now().Unix()}
	require.NoError(t, DB.Create(u).Error)
	return u
}

func createSupportEmailTicket(t *testing.T, u *User) (*SupportTicket, *SupportTicketMessage) {
	t.Helper()
	now := time.Now().Unix()
	ticket := &SupportTicket{UserID: u.Id, Subject: "PRIVATE subject token IP", Status: SupportTicketStatusProcessing, LastMessageAt: now, CreatedAt: now, UpdatedAt: now}
	message := &SupportTicketMessage{AuthorUserID: u.Id, AuthorRole: SupportTicketAuthorUser, Content: "PRIVATE body <script> secret", CreatedAt: now}
	require.NoError(t, CreateSupportTicket(ticket, message))
	return ticket, message
}

func enableSupportEmail(t *testing.T) {
	t.Helper()
	require.NoError(t, SaveSupportEmailSettings(t.Context(), true, []string{"admin@example.invalid"}, "https://console.example.invalid"))
}
func supportEmailRows(t *testing.T) []SupportEmailOutbox {
	t.Helper()
	var rows []SupportEmailOutbox
	require.NoError(t, DB.Order("id ASC").Find(&rows).Error)
	return rows
}

func TestSupportEmailPostgresDisabledAndMinimalAtomicDeduplicatedEvents(t *testing.T) {
	u := setupSupportEmailPostgres(t)
	createSupportEmailTicket(t, u)
	assert.Empty(t, supportEmailRows(t))
	require.NoError(t, SaveSupportEmailSettings(t.Context(), true, []string{"Admin@example.invalid", "other@example.invalid"}, "https://console.example.invalid"))
	ticket, message := createSupportEmailTicket(t, u)
	rows := supportEmailRows(t)
	require.Len(t, rows, 2)
	for _, row := range rows {
		assert.Equal(t, "created", row.Event)
		assert.Equal(t, "pending", row.Status)
		assert.NotContains(t, fmt.Sprint(row), "PRIVATE")
	}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return enqueueSupportEmail(tx, ticket, message, true) }))
	assert.Len(t, supportEmailRows(t), 2)
	_, err := AppendSupportTicketMessage(ticket.ID, u.Id, false, "secret followup", SupportTicketAuthorUser, SupportTicketStatusProcessing, time.Now().Unix())
	require.NoError(t, err)
	rows = supportEmailRows(t)
	require.Len(t, rows, 4)
	assert.Equal(t, "coalesced", rows[2].Status)
	assert.Equal(t, "coalesced", rows[3].Status)
	_, err = AppendSupportTicketMessage(ticket.ID, 999, true, "private admin reply", SupportTicketAuthorAdmin, SupportTicketStatusAwaitingUser, time.Now().Unix())
	require.NoError(t, err)
	rows = supportEmailRows(t)
	require.Len(t, rows, 5)
	assert.Equal(t, u.Email, rows[4].Recipient)
	assert.EqualValues(t, 1, rows[4].EmailVersion)
	note := &SupportTicketMessage{ID: 987654, AuthorRole: "internal", CreatedAt: time.Now().Unix()}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return enqueueSupportEmail(tx, ticket, note, false) }))
	assert.Len(t, supportEmailRows(t), 5)
	// Real DB constraint failure proves that ticket + message + event are atomic.
	require.NoError(t, DB.Exec("ALTER TABLE support_email_outboxes ADD CONSTRAINT fixture_reject_created CHECK (event <> 'created') NOT VALID").Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("ALTER TABLE support_email_outboxes DROP CONSTRAINT fixture_reject_created").Error)
	})
	var beforeTickets, beforeMessages int64
	require.NoError(t, DB.Model(&SupportTicket{}).Count(&beforeTickets).Error)
	require.NoError(t, DB.Model(&SupportTicketMessage{}).Count(&beforeMessages).Error)
	failed := &SupportTicket{UserID: u.Id, Subject: "atomic", Status: SupportTicketStatusProcessing}
	failedMessage := &SupportTicketMessage{AuthorRole: SupportTicketAuthorUser, Content: "atomic", CreatedAt: time.Now().Unix()}
	require.Error(t, CreateSupportTicket(failed, failedMessage))
	var afterTickets, afterMessages int64
	require.NoError(t, DB.Model(&SupportTicket{}).Count(&afterTickets).Error)
	require.NoError(t, DB.Model(&SupportTicketMessage{}).Count(&afterMessages).Error)
	assert.Equal(t, beforeTickets, afterTickets)
	assert.Equal(t, beforeMessages, afterMessages)
}

func TestSupportEmailPostgresUnverifiedAndOptedOutDoNotReceive(t *testing.T) {
	for _, kind := range []string{"missing", "unverified", "mismatch", "disabled", "opted-out"} {
		t.Run(kind, func(t *testing.T) {
			u := setupSupportEmailPostgres(t)
			enableSupportEmail(t)
			switch kind {
			case "missing":
				u.Email = ""
			case "unverified":
				u.EmailVerifiedAt = 0
			case "mismatch":
				u.EmailVerifiedAddress = "other@example.invalid"
			case "disabled":
				u.Status = common.UserStatusDisabled
			case "opted-out":
				require.NoError(t, SaveSupportEmailPreference(t.Context(), u.Id, false))
			}
			require.NoError(t, DB.Save(u).Error)
			ticket, _ := createSupportEmailTicket(t, u)
			_, err := AppendSupportTicketMessage(ticket.ID, 999, true, "reply", SupportTicketAuthorAdmin, SupportTicketStatusAwaitingUser, time.Now().Unix())
			require.NoError(t, err)
			for _, row := range supportEmailRows(t) {
				assert.NotEqual(t, "admin_reply", row.Event)
			}
		})
	}
}

func TestSupportEmailPostgresClaimsLeaseRecoveryAndStaleCompletion(t *testing.T) {
	u := setupSupportEmailPostgres(t)
	enableSupportEmail(t)
	createSupportEmailTicket(t, u)
	conn, err := DB.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(12)
	t.Cleanup(func() { conn.SetMaxOpenConns(1) })
	now := time.Now().Unix()
	start := make(chan struct{})
	claims := make(chan *SupportEmailOutbox, 12)
	failures := make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			<-start
			row, err := ClaimSupportEmail(context.Background(), now)
			claims <- row
			failures <- err
		})
	}
	close(start)
	wg.Wait()
	close(claims)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var winner *SupportEmailOutbox
	count := 0
	for row := range claims {
		if row != nil {
			winner = row
			count++
		}
	}
	require.Equal(t, 1, count)
	admission, err := AdmitSupportEmail(t.Context(), winner, now)
	require.NoError(t, err)
	require.NotNil(t, admission)
	row, err := ClaimSupportEmail(t.Context(), now+119)
	require.NoError(t, err)
	assert.Nil(t, row)
	recovered, err := ClaimSupportEmail(t.Context(), now+121)
	require.NoError(t, err)
	require.NotNil(t, recovered)
	assert.NotEqual(t, winner.LeaseToken, recovered.LeaseToken)
	require.NoError(t, FinishSupportEmail(t.Context(), winner, now+122, ""))
	assert.Equal(t, "leased", supportEmailRows(t)[0].Status)
	admission, err = AdmitSupportEmail(t.Context(), recovered, now+122)
	require.NoError(t, err)
	require.NotNil(t, admission)
	assert.Equal(t, 2, recovered.Attempts)
	require.NoError(t, FinishSupportEmail(t.Context(), recovered, now+123, ""))
	assert.Equal(t, "sent", supportEmailRows(t)[0].Status)
	row, err = ClaimSupportEmail(t.Context(), now+999)
	require.NoError(t, err)
	assert.Nil(t, row)
}

func TestSupportEmailPostgresAuthoritativeChangesCancelBeforeAdmission(t *testing.T) {
	for _, kind := range []string{"email", "email-version", "disabled", "deleted", "duplicate-email", "preference", "settings", "recipient", "origin"} {
		t.Run(kind, func(t *testing.T) {
			u := setupSupportEmailPostgres(t)
			enableSupportEmail(t)
			ticket, _ := createSupportEmailTicket(t, u)
			require.NoError(t, DB.Model(&SupportEmailOutbox{}).Where("event = ?", "created").Update("status", "sent").Error)
			_, err := AppendSupportTicketMessage(ticket.ID, 999, true, "reply", SupportTicketAuthorAdmin, SupportTicketStatusAwaitingUser, time.Now().Unix())
			require.NoError(t, err)
			now := time.Now().Unix()
			row, err := ClaimSupportEmail(t.Context(), now)
			require.NoError(t, err)
			require.NotNil(t, row)
			switch kind {
			case "email":
				require.NoError(t, DB.Model(u).Update("email", "new@example.invalid").Error)
			case "email-version":
				require.NoError(t, DB.Model(u).Update("email_verification_version", 2).Error)
			case "disabled":
				require.NoError(t, DB.Model(u).Update("status", common.UserStatusDisabled).Error)
			case "deleted":
				require.NoError(t, DB.Delete(u).Error)
			case "duplicate-email":
				require.NoError(t, DB.Create(&User{Username: "legacy-duplicate", AffCode: "legacy-duplicate", Email: u.Email, Status: common.UserStatusEnabled}).Error)
			case "preference":
				require.NoError(t, SaveSupportEmailPreference(t.Context(), u.Id, false))
				require.NoError(t, SaveSupportEmailPreference(t.Context(), u.Id, true))
			case "settings":
				require.NoError(t, SaveSupportEmailSettings(t.Context(), false, []string{"admin@example.invalid"}, "https://console.example.invalid"))
				require.NoError(t, SaveSupportEmailSettings(t.Context(), true, []string{"admin@example.invalid"}, "https://console.example.invalid"))
			case "recipient":
				require.NoError(t, SaveSupportEmailSettings(t.Context(), true, []string{"new-admin@example.invalid"}, "https://console.example.invalid"))
			case "origin":
				require.NoError(t, SaveSupportEmailSettings(t.Context(), true, []string{"admin@example.invalid"}, "https://new.example.invalid"))
			}
			admission, err := AdmitSupportEmail(t.Context(), row, now)
			require.NoError(t, err)
			assert.Nil(t, admission)
			rows := supportEmailRows(t)
			assert.Equal(t, "cancelled", rows[len(rows)-1].Status)
		})
	}
}

func TestSupportEmailPostgresSendingAdmissionCannotBeRetracted(t *testing.T) {
	u := setupSupportEmailPostgres(t)
	enableSupportEmail(t)
	createSupportEmailTicket(t, u)
	now := time.Now().Unix()
	row, err := ClaimSupportEmail(t.Context(), now)
	require.NoError(t, err)
	admission, err := AdmitSupportEmail(t.Context(), row, now)
	require.NoError(t, err)
	require.NotNil(t, admission)
	require.ErrorIs(t, ManageSupportEmail(t.Context(), row.ID, false), ErrSupportEmailState)
	require.NoError(t, SaveSupportEmailSettings(t.Context(), false, []string{"admin@example.invalid"}, "https://console.example.invalid"))
	assert.Equal(t, "sending", supportEmailRows(t)[0].Status)
	require.NoError(t, FinishSupportEmail(t.Context(), row, now, ""))
	assert.Equal(t, "sent", supportEmailRows(t)[0].Status)
}

func TestSupportEmailPostgresRetryBackoffDeadLetterManualRetryAndExpiry(t *testing.T) {
	u := setupSupportEmailPostgres(t)
	enableSupportEmail(t)
	createSupportEmailTicket(t, u)
	now := time.Now().Unix()
	for attempt := 1; attempt <= 6; attempt++ {
		row, err := ClaimSupportEmail(t.Context(), now)
		require.NoError(t, err)
		require.NotNil(t, row)
		admission, err := AdmitSupportEmail(t.Context(), row, now)
		require.NoError(t, err)
		require.NotNil(t, admission)
		require.NoError(t, FinishSupportEmail(t.Context(), row, now, "smtp_temporary"))
		stored := supportEmailRows(t)[0]
		assert.Equal(t, attempt, stored.Attempts)
		assert.Equal(t, now+(int64(30)<<uint(attempt-1)), stored.NextAttemptAt)
		now = stored.NextAttemptAt + 60
	}
	dead := supportEmailRows(t)[0]
	assert.Equal(t, "dead", dead.Status)
	require.NoError(t, ManageSupportEmail(t.Context(), dead.ID, true))
	row, err := ClaimSupportEmail(t.Context(), now)
	require.NoError(t, err)
	admission, err := AdmitSupportEmail(t.Context(), row, now)
	require.NoError(t, err)
	require.NotNil(t, admission)
	assert.Equal(t, 7, row.Attempts)
	require.NoError(t, FinishSupportEmail(t.Context(), row, now, "delivery_failed"))
	row, err = ClaimSupportEmail(t.Context(), dead.CreatedAt+int64(SupportEmailLifetime/time.Second)+1)
	require.NoError(t, err)
	require.NotNil(t, row)
	admission, err = AdmitSupportEmail(t.Context(), row, dead.CreatedAt+int64(SupportEmailLifetime/time.Second)+1)
	require.NoError(t, err)
	assert.Nil(t, admission)
	assert.Equal(t, "expired", supportEmailRows(t)[0].LastErrorCode)
}

func TestSupportEmailPostgresRateLimitAcrossTicketsAndDailyCap(t *testing.T) {
	u := setupSupportEmailPostgres(t)
	enableSupportEmail(t)
	createSupportEmailTicket(t, u)
	createSupportEmailTicket(t, u)
	now := time.Now().Unix()
	first, err := ClaimSupportEmail(t.Context(), now)
	require.NoError(t, err)
	admission, err := AdmitSupportEmail(t.Context(), first, now)
	require.NoError(t, err)
	require.NotNil(t, admission)
	require.NoError(t, FinishSupportEmail(t.Context(), first, now, ""))
	second, err := ClaimSupportEmail(t.Context(), now)
	require.NoError(t, err)
	admission, err = AdmitSupportEmail(t.Context(), second, now)
	require.NoError(t, err)
	assert.Nil(t, admission)
	assert.Equal(t, "rate_limited", supportEmailRows(t)[1].LastErrorCode)
	assert.Zero(t, supportEmailRows(t)[1].Attempts)
	require.NoError(t, DB.Model(&SupportEmailRate{}).Where(map[string]any{"key": first.RecipientKey}).Update("day_count", 120).Error)
	second, err = ClaimSupportEmail(t.Context(), now+60)
	require.NoError(t, err)
	admission, err = AdmitSupportEmail(t.Context(), second, now+60)
	require.NoError(t, err)
	assert.Nil(t, admission)
	assert.Equal(t, now+86400, supportEmailRows(t)[1].NextAttemptAt)
}

func TestSupportEmailSettingsValidationRejectsUnsafeOriginAndHeaders(t *testing.T) {
	for _, origin := range []string{"https://*.example.invalid", "https://console.example.invalid:0", "https://console.example.invalid:65536", "https://console.example.invalid:", "https://console.example.invalid#", "http://console.example.invalid", "https://user:pass@console.example.invalid", "https://console.example.invalid/", "https://console.example.invalid/path", "https://console.example.invalid?x=1", "https://console.example.invalid#x", "//evil.invalid", "https://console.example.invalid\\@evil.invalid", "https://console.example.invalid\r\nBcc:x"} {
		assert.False(t, ValidateSupportEmailOrigin(origin), origin)
	}
	assert.True(t, ValidateSupportEmailOrigin("https://console.example.invalid:8443"))
	for _, address := range []string{"Display <a@example.invalid>", "a@example.invalid;b@example.invalid", "a@example.invalid\r\nBcc:x", "a@example.invalid,b@example.invalid", "not-mail"} {
		_, ok := ValidSupportEmailAddress(address)
		assert.False(t, ok, address)
	}
}

func supportEmailCustomerRow(t *testing.T, u *User) *SupportEmailOutbox {
	t.Helper()
	enableSupportEmail(t)
	ticket, _ := createSupportEmailTicket(t, u)
	require.NoError(t, DB.Model(&SupportEmailOutbox{}).Where("event = ?", "created").Update("status", "sent").Error)
	_, err := AppendSupportTicketMessage(ticket.ID, 999, true, "reply", SupportTicketAuthorAdmin, SupportTicketStatusAwaitingUser, time.Now().Unix())
	require.NoError(t, err)
	row, err := ClaimSupportEmail(t.Context(), time.Now().Unix())
	require.NoError(t, err)
	require.NotNil(t, row)
	return row
}

func TestSupportEmailPostgresIdentityChangeCommitsBeforeAdmission(t *testing.T) {
	u := setupSupportEmailPostgres(t)
	row := supportEmailCustomerRow(t, u)
	sql, err := DB.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(8)
	t.Cleanup(func() { sql.SetMaxOpenConns(1) })
	holder := DB.Begin()
	require.NoError(t, holder.Error)
	t.Cleanup(func() { holder.Rollback() })
	var locked User
	require.NoError(t, lockForUpdate(holder).First(&locked, u.Id).Error)
	type result struct {
		admission *SupportEmailAdmission
		err       error
	}
	done := make(chan result, 1)
	go func() { a, e := AdmitSupportEmail(t.Context(), row, time.Now().Unix()); done <- result{a, e} }()
	// Observe the real PostgreSQL lock wait, not an assumed timing sleep.
	require.Eventually(t, func() bool {
		var count int64
		err := DB.Raw(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%FROM "users"%' AND query LIKE '%FOR UPDATE%'`).Scan(&count).Error
		return err == nil && count > 0
	}, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, holder.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]any{"email": "changed@example.invalid", "email_verified_address": "changed@example.invalid", "email_verification_version": 2}).Error)
	require.NoError(t, holder.Commit().Error)
	completed := <-done
	require.NoError(t, completed.err)
	assert.Nil(t, completed.admission)
	rows := supportEmailRows(t)
	assert.Equal(t, "cancelled", rows[len(rows)-1].Status)
}

func TestSupportEmailPostgresAdmissionLinearizesBeforeFollowingIdentityChange(t *testing.T) {
	u := setupSupportEmailPostgres(t)
	row := supportEmailCustomerRow(t, u)
	sql, err := DB.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(8)
	t.Cleanup(func() { sql.SetMaxOpenConns(1) })
	holder := DB.Begin()
	require.NoError(t, holder.Error)
	t.Cleanup(func() { holder.Rollback() })
	var locked SupportEmailOutbox
	require.NoError(t, lockForUpdate(holder).First(&locked, row.ID).Error)
	type result struct {
		admission *SupportEmailAdmission
		err       error
	}
	admitted := make(chan result, 1)
	go func() { a, e := AdmitSupportEmail(t.Context(), row, time.Now().Unix()); admitted <- result{a, e} }()
	require.Eventually(t, func() bool {
		var count int64
		err := DB.Raw(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%FROM "support_email_outboxes"%' AND query LIKE '%FOR UPDATE%'`).Scan(&count).Error
		return err == nil && count > 0
	}, 5*time.Second, 5*time.Millisecond)
	changed := make(chan error, 1)
	go func() {
		changed <- DB.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]any{"email": "later@example.invalid", "email_verified_address": "later@example.invalid", "email_verification_version": 2}).Error
	}()
	require.Eventually(t, func() bool {
		var count int64
		err := DB.Raw(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE 'UPDATE "users"%'`).Scan(&count).Error
		return err == nil && count > 0
	}, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, holder.Commit().Error)
	completed := <-admitted
	require.NoError(t, completed.err)
	require.NotNil(t, completed.admission)
	require.NoError(t, <-changed)
	rows := supportEmailRows(t)
	assert.Equal(t, "sending", rows[len(rows)-1].Status)
}

func TestSupportEmailPostgresExpiredClaimCannotSendAndAdmissionRenewsLease(t *testing.T) {
	u := setupSupportEmailPostgres(t)
	row := supportEmailCustomerRow(t, u)
	now := time.Now().Unix()
	require.NoError(t, DB.Model(&SupportEmailOutbox{}).Where("id = ?", row.ID).Update("lease_until", now-1).Error)
	admission, err := AdmitSupportEmail(t.Context(), row, now-100)
	require.NoError(t, err)
	assert.Nil(t, admission, "lock-time wall clock, not stale caller timestamp, governs lease")
	recovered, err := ClaimSupportEmail(t.Context(), now)
	require.NoError(t, err)
	require.NotNil(t, recovered)
	require.NoError(t, DB.Model(&SupportEmailOutbox{}).Where("id = ?", row.ID).Update("lease_until", now+1).Error)
	admission, err = AdmitSupportEmail(t.Context(), recovered, now)
	require.NoError(t, err)
	require.NotNil(t, admission)
	rows := supportEmailRows(t)
	assert.GreaterOrEqual(t, rows[len(rows)-1].LeaseUntil, now+120)
}
