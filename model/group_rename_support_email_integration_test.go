package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGroupRenameSupportEmailPostgresPreservesVerifiedRecipientAndPreference(t *testing.T) {
	u := setupSupportEmailPostgres(t)
	captureGroupRenameRuntimeState(t)
	require.NoError(t, DB.AutoMigrate(&GroupRenameOperation{}))
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DELETE FROM options").Error)
		require.NoError(t, DB.Exec("DELETE FROM group_rename_operations").Error)
	})
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.25}`)
	require.NoError(t, DB.Model(u).Update("group", "old").Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return UpdateGoogleBindingWithTx(tx, u.Id, "synthetic-joint-google-subject")
	}))
	require.NoError(t, DB.First(u, u.Id).Error)
	require.True(t, VerifiedSupportEmail(u))
	before := *u
	require.NoError(t, SaveSupportEmailPreference(t.Context(), u.Id, false))
	require.NoError(t, SaveSupportEmailPreference(t.Context(), u.Id, true))
	preference, err := ReadSupportEmailPreference(t.Context(), u.Id)
	require.NoError(t, err)
	enableSupportEmail(t)
	ticket, _ := createSupportEmailTicket(t, u)
	require.NoError(t, DB.Model(&SupportEmailOutbox{}).Where("event = ?", "created").Update("status", "sent").Error)
	_, err = AppendSupportTicketMessage(ticket.ID, 999, true, "synthetic public reply", SupportTicketAuthorAdmin, SupportTicketStatusAwaitingUser, time.Now().Unix())
	require.NoError(t, err)
	now := time.Now().Unix()
	row, err := ClaimSupportEmail(t.Context(), now)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "admin_reply", row.Event)
	_, err = ApplyGroupRename(GroupRenameApplyRequest{Mappings: []GroupRenameMapping{{From: "old", To: "new"}}})
	require.NoError(t, err)
	var after User
	require.NoError(t, DB.First(&after, u.Id).Error)
	assert.Equal(t, "new", after.Group)
	assert.Equal(t, before.GoogleId, after.GoogleId)
	assert.Equal(t, before.Email, after.Email)
	assert.Equal(t, before.EmailVerifiedAddress, after.EmailVerifiedAddress)
	assert.Equal(t, before.EmailVerifiedAt, after.EmailVerifiedAt)
	assert.Equal(t, before.EmailVerificationVersion, after.EmailVerificationVersion)
	assert.True(t, VerifiedSupportEmail(&after))
	afterPreference, err := ReadSupportEmailPreference(t.Context(), u.Id)
	require.NoError(t, err)
	assert.Equal(t, preference, afterPreference)
	admission, err := AdmitSupportEmail(t.Context(), row, now)
	require.NoError(t, err)
	require.NotNil(t, admission, "a group rename is not an email-proof or preference change")
	assert.Equal(t, "https://console.example.invalid", admission.Origin)
	var outbox SupportEmailOutbox
	require.NoError(t, DB.First(&outbox, row.ID).Error)
	assert.Equal(t, "sending", outbox.Status)
	assert.Equal(t, before.Email, outbox.Recipient)
	assert.Equal(t, before.EmailVerificationVersion, outbox.EmailVersion)
	assert.Equal(t, preference.Version, outbox.PreferenceVersion)
}
