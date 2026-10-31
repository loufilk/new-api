package model

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const callInterceptionTestActor = 99201
const callInterceptionTestTarget = 99202

func setupUserCallInterceptionModelTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL TEST_MODEL_POSTGRES_DSN")
	}
	require.True(t, common.UsingMainDatabase(common.DatabaseTypePostgreSQL))
	require.NoError(t, DB.AutoMigrate(&UserCallInterception{}, &UserCallInterceptionHit{}, &UserCallInterceptionAudit{}))
	users := []User{
		{Id: callInterceptionTestActor, Username: "interception-admin", AffCode: "interception-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled},
		{Id: callInterceptionTestTarget, Username: "interception-user", AffCode: "interception-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: -25, QuotaPaid: -25, UsedQuota: 42, RequestCount: 7},
	}
	require.NoError(t, DB.Create(&users).Error)
	t.Cleanup(func() {
		for _, row := range []any{&UserCallInterception{}, &UserCallInterceptionHit{}, &UserCallInterceptionAudit{}} {
			require.NoError(t, DB.Where("user_id = ?", callInterceptionTestTarget).Delete(row).Error)
		}
		require.NoError(t, DB.Unscoped().Where("id IN ?", []int{callInterceptionTestActor, callInterceptionTestTarget}).Delete(&User{}).Error)
	})
}

func TestUserCallInterceptionValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		reply   string
		err     error
	}{
		{"disabled empty", false, "", nil},
		{"enabled blank", true, " \n\t\u3000", ErrCallInterceptionReplyRequired},
		{"invisible-only reply", true, "\u200b\u200d", ErrCallInterceptionReplyRequired},
		{"null within enabled text", true, "Visible text\x00", ErrCallInterceptionReplyNull},
		{"null in disabled draft", false, "\x00", ErrCallInterceptionReplyNull},
		{"bilingual multiline", true, "请联系管理员\nPlease contact the administrator.", nil},
		{"exact byte limit", true, strings.Repeat("界", 5333) + "a", nil},
		{"over byte limit", false, strings.Repeat("界", 5334), ErrCallInterceptionReplyTooLong},
		{"invalid utf8", false, string([]byte{0xff}), ErrCallInterceptionInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, ValidateUserCallInterceptionReply(tc.enabled, tc.reply), tc.err)
		})
	}
}

func TestUserCallInterceptionPersistsEditsDisableAndRepeatMigrationWithoutUserChanges(t *testing.T) {
	setupUserCallInterceptionModelTest(t)
	var before User
	require.NoError(t, DB.First(&before, callInterceptionTestTarget).Error)
	initial, err := GetUserCallInterception(callInterceptionTestTarget)
	require.NoError(t, err)
	assert.False(t, initial.Enabled)
	assert.Zero(t, initial.Revision)
	firstReply := "这是合成测试通知\nThis is a synthetic notice."
	config, err := SaveUserCallInterception(callInterceptionTestActor, callInterceptionTestTarget, UserCallInterceptionUpdate{Enabled: true, Reply: firstReply, RequestID: "enable-test"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, config.Revision)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&UserCallInterception{}, &UserCallInterceptionHit{}, &UserCallInterceptionAudit{}))
	}
	stored, err := GetUserCallInterception(callInterceptionTestTarget)
	require.NoError(t, err)
	assert.Equal(t, config, stored)
	config, err = SaveUserCallInterception(callInterceptionTestActor, callInterceptionTestTarget, UserCallInterceptionUpdate{Enabled: true, Reply: "Updated synthetic notice", ExpectedRevision: 1, RequestID: "edit-test"})
	require.NoError(t, err)
	assert.EqualValues(t, 2, config.Revision)
	config, err = SaveUserCallInterception(callInterceptionTestActor, callInterceptionTestTarget, UserCallInterceptionUpdate{Enabled: false, Reply: "Updated synthetic notice", ExpectedRevision: 2, RequestID: "disable-test"})
	require.NoError(t, err)
	stored, err = GetUserCallInterception(callInterceptionTestTarget)
	require.NoError(t, err)
	assert.Equal(t, config, stored)
	assert.False(t, stored.Enabled)
	var after User
	require.NoError(t, DB.First(&after, callInterceptionTestTarget).Error)
	assert.Equal(t, before, after, "policy edits must not alter identity, balances, counters or login state")
	audits, total, err := GetUserCallInterceptionAudits(UserCallInterceptionLogFilter{UserID: callInterceptionTestTarget}, 0, 100)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Len(t, audits, 3)
	assert.False(t, audits[0].ReplyChanged)
	assert.True(t, audits[0].PreviousEnabled)
	assert.False(t, audits[0].Enabled)
	encoded, err := common.Marshal(audits)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), firstReply)
	assert.NotContains(t, string(encoded), "Updated synthetic notice")
	assert.NotContains(t, string(encoded), `"reply":`)
	for _, index := range []string{"idx_uci_hit_user_time", "idx_uci_hit_request"} {
		assert.True(t, DB.Migrator().HasIndex(&UserCallInterceptionHit{}, index))
	}
	for _, index := range []string{"idx_uci_audit_user_time", "idx_uci_audit_request"} {
		assert.True(t, DB.Migrator().HasIndex(&UserCallInterceptionAudit{}, index))
	}
}

func TestUserCallInterceptionConcurrentSaveRejectsLostUpdate(t *testing.T) {
	setupUserCallInterceptionModelTest(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, reply := range []string{"First writer", "Second writer"} {
		workers.Go(func() {
			<-start
			_, err := SaveUserCallInterception(callInterceptionTestActor, callInterceptionTestTarget, UserCallInterceptionUpdate{Enabled: true, Reply: reply, RequestID: "concurrent-save"})
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	var successful, conflicts int
	for err := range results {
		if err == nil {
			successful++
			continue
		}
		require.ErrorIs(t, err, ErrCallInterceptionConflict)
		conflicts++
	}
	assert.Equal(t, 1, successful)
	assert.Equal(t, 1, conflicts)
	_, total, err := GetUserCallInterceptionAudits(UserCallInterceptionLogFilter{UserID: callInterceptionTestTarget}, 0, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
}

func TestUserCallInterceptionAuditFailureRollsBackConfiguration(t *testing.T) {
	setupUserCallInterceptionModelTest(t)
	before, err := SaveUserCallInterception(callInterceptionTestActor, callInterceptionTestTarget, UserCallInterceptionUpdate{Enabled: true, Reply: "Original synthetic notice", RequestID: "save-before-failure"})
	require.NoError(t, err)
	// Exercise an actual PostgreSQL write error, not a mocked save result.
	require.NoError(t, DB.Migrator().DropTable(&UserCallInterceptionAudit{}))
	t.Cleanup(func() { require.NoError(t, DB.AutoMigrate(&UserCallInterceptionAudit{})) })
	_, err = SaveUserCallInterception(callInterceptionTestActor, callInterceptionTestTarget, UserCallInterceptionUpdate{Enabled: false, Reply: "Must roll back", ExpectedRevision: 1, RequestID: "failed-audit"})
	require.Error(t, err)
	after, err := GetUserCallInterception(callInterceptionTestTarget)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	require.NoError(t, DB.AutoMigrate(&UserCallInterceptionAudit{}))
}

func TestUserCallInterceptionSaveRechecksPersistedAdministratorAndTarget(t *testing.T) {
	setupUserCallInterceptionModelTest(t)
	for _, tc := range []struct {
		name                               string
		actorRole, actorStatus, targetRole int
	}{
		{"ordinary account", common.RoleCommonUser, common.UserStatusEnabled, common.RoleCommonUser},
		{"disabled admin", common.RoleAdminUser, common.UserStatusDisabled, common.RoleCommonUser},
		{"same level target", common.RoleAdminUser, common.UserStatusEnabled, common.RoleAdminUser},
		{"root target", common.RoleAdminUser, common.UserStatusEnabled, common.RoleRootUser},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, DB.Model(&User{}).Where("id = ?", callInterceptionTestActor).Updates(map[string]any{"role": tc.actorRole, "status": tc.actorStatus}).Error)
			require.NoError(t, DB.Model(&User{}).Where("id = ?", callInterceptionTestTarget).Update("role", tc.targetRole).Error)
			_, err := SaveUserCallInterception(callInterceptionTestActor, callInterceptionTestTarget, UserCallInterceptionUpdate{Enabled: true, Reply: "Unauthorized", RequestID: "permission-test"})
			require.ErrorIs(t, err, ErrCallInterceptionPermission)
		})
	}
	config, err := GetUserCallInterception(callInterceptionTestTarget)
	require.NoError(t, err)
	assert.Zero(t, config.Revision)
}

func TestUserCallInterceptionHitPaginationFiltersAndStorageFailure(t *testing.T) {
	setupUserCallInterceptionModelTest(t)
	for _, hit := range []UserCallInterceptionHit{
		{Time: 100, UserID: callInterceptionTestTarget, ActorUserID: callInterceptionTestTarget, IdentityKind: UserCallInterceptionIdentityAPIKey, TokenID: 10, RequestID: "request-a", Interface: "POST /v1/chat/completions", Protocol: "openai_chat", Status: 200, Revision: 1},
		{Time: 101, UserID: callInterceptionTestTarget, ActorUserID: callInterceptionTestTarget, IdentityKind: UserCallInterceptionIdentityAPIKey, TokenID: 11, RequestID: "request-b", Interface: "GET /v1/responses", Protocol: "websocket", Status: 403, Revision: 2},
		{Time: 101, UserID: callInterceptionTestTarget, ActorUserID: callInterceptionTestTarget, IdentityKind: UserCallInterceptionIdentityDashboardSession, TokenID: 0, RequestID: "request-c", Interface: "POST /pg/chat/completions", Protocol: "playground", Status: 200, Revision: 2},
	} {
		require.NoError(t, RecordUserCallInterceptionHit(&hit))
	}
	rows, total, err := GetUserCallInterceptionHits(UserCallInterceptionLogFilter{UserID: callInterceptionTestTarget}, 1, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 3, total)
	assert.Equal(t, "request-b", rows[0].RequestID)
	keyID := 11
	rows, total, err = GetUserCallInterceptionHits(UserCallInterceptionLogFilter{UserID: callInterceptionTestTarget, TokenID: &keyID, RequestID: "request-b", Protocol: "websocket", Status: 403, StartTime: 101, EndTime: 101}, 0, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	_, total, err = GetUserCallInterceptionHits(UserCallInterceptionLogFilter{UserID: callInterceptionTestActor}, 0, 100)
	require.NoError(t, err)
	assert.Zero(t, total)
	_, _, err = GetUserCallInterceptionHits(UserCallInterceptionLogFilter{UserID: callInterceptionTestTarget}, 0, -1)
	require.ErrorIs(t, err, ErrCallInterceptionInvalid)
	require.ErrorIs(t, RecordUserCallInterceptionHit(nil), ErrCallInterceptionInvalid)
	require.NoError(t, DB.Migrator().DropTable(&UserCallInterceptionHit{}))
	t.Cleanup(func() { require.NoError(t, DB.AutoMigrate(&UserCallInterceptionHit{})) })
	err = RecordUserCallInterceptionHit(&UserCallInterceptionHit{UserID: callInterceptionTestTarget, ActorUserID: callInterceptionTestTarget, IdentityKind: UserCallInterceptionIdentityDashboardSession, RequestID: "failed-hit", Interface: "POST /v1/messages", Protocol: "claude", Status: 200, Revision: 1})
	require.Error(t, err)
	assert.False(t, errors.Is(err, gorm.ErrRecordNotFound))
	require.NoError(t, DB.AutoMigrate(&UserCallInterceptionHit{}))
}

func TestUserCallInterceptionHitIdentityDistinguishesAuthenticatedCallsFromArtifactOwnership(t *testing.T) {
	setupUserCallInterceptionModelTest(t)
	for _, tc := range []struct {
		kind             string
		actorID, tokenID int
	}{
		{UserCallInterceptionIdentityAPIKey, callInterceptionTestTarget, 12},
		{UserCallInterceptionIdentityDashboardSession, callInterceptionTestTarget, 0},
		{UserCallInterceptionIdentityArtifactCapability, 0, 0},
		{UserCallInterceptionIdentityPublicArtifact, 0, 0},
	} {
		hit := UserCallInterceptionHit{UserID: callInterceptionTestTarget, ActorUserID: tc.actorID, TokenID: tc.tokenID, IdentityKind: tc.kind, RequestID: "identity-" + tc.kind, Interface: "GET /v1/tasks/:key/artifacts/:artifact_key/content", Protocol: "artifact", Status: 403, Revision: 1}
		require.NoError(t, RecordUserCallInterceptionHit(&hit))
		rows, total, err := GetUserCallInterceptionHits(UserCallInterceptionLogFilter{UserID: callInterceptionTestTarget, IdentityKind: tc.kind}, 0, 100)
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		require.Len(t, rows, 1)
		assert.Equal(t, tc.actorID, rows[0].ActorUserID)
		assert.Equal(t, tc.tokenID, rows[0].TokenID)
	}
	// A dashboard administrator requesting the owner's artifact is not the owner
	// continuing to call. Queries can distinguish actor from policy target.
	require.NoError(t, RecordUserCallInterceptionHit(&UserCallInterceptionHit{UserID: callInterceptionTestTarget, ActorUserID: callInterceptionTestActor, IdentityKind: UserCallInterceptionIdentityDashboardSession, RequestID: "admin-views-owner-artifact", Interface: "GET /api/task/:id/artifacts/:artifact_key/content", Protocol: "artifact", Status: 403, Revision: 1}))
	actorID := callInterceptionTestActor
	adminRows, adminTotal, err := GetUserCallInterceptionHits(UserCallInterceptionLogFilter{UserID: callInterceptionTestTarget, ActorUserID: &actorID, IdentityKind: UserCallInterceptionIdentityDashboardSession}, 0, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, adminTotal)
	require.Len(t, adminRows, 1)
	assert.Equal(t, callInterceptionTestTarget, adminRows[0].UserID)
	assert.Equal(t, callInterceptionTestActor, adminRows[0].ActorUserID)
	for _, tc := range []struct {
		kind             string
		actorID, tokenID int
	}{
		{"", callInterceptionTestTarget, 12},
		{"client_claimed", callInterceptionTestTarget, 12},
		{UserCallInterceptionIdentityAPIKey, 0, 12},
		{UserCallInterceptionIdentityAPIKey, callInterceptionTestActor, 12},
		{UserCallInterceptionIdentityAPIKey, callInterceptionTestTarget, 0},
		{UserCallInterceptionIdentityDashboardSession, callInterceptionTestTarget, 12},
		{UserCallInterceptionIdentityDashboardSession, 0, 0},
		{UserCallInterceptionIdentityPublicArtifact, callInterceptionTestTarget, 0},
		{UserCallInterceptionIdentityArtifactCapability, 0, 12},
	} {
		err := RecordUserCallInterceptionHit(&UserCallInterceptionHit{UserID: callInterceptionTestTarget, ActorUserID: tc.actorID, TokenID: tc.tokenID, IdentityKind: tc.kind, RequestID: "invalid-attribution", Interface: "GET /artifact", Protocol: "artifact", Status: 403, Revision: 1})
		require.ErrorIs(t, err, ErrCallInterceptionInvalid)
	}
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&UserCallInterception{}, &UserCallInterceptionHit{}, &UserCallInterceptionAudit{}))
	}
	rows, total, err := GetUserCallInterceptionHits(UserCallInterceptionLogFilter{UserID: callInterceptionTestTarget}, 0, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	assert.Len(t, rows, 5)
}
