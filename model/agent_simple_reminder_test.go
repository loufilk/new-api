package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentSimpleDailyRemindersAreVisibleScopedAndAcknowledgedPerRecipient(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	now := time.Now().In(agentSimpleLocation)
	day := now.Format("2006-01-02")
	rows := []AgentSimpleReminder{
		{AgentUserID: agent.UserID, Day: day, Audience: "agent", Quota: 1000},
		{AgentUserID: agent.UserID, Day: day, Audience: "root", Quota: 1000},
		{AgentUserID: agentTestOtherID, Day: day, Audience: "agent", Quota: 2000},
		{AgentUserID: agent.UserID, Day: now.AddDate(0, 0, -1).Format("2006-01-02"), Audience: "agent", Quota: 1000},
	}
	require.NoError(t, DB.Create(&rows).Error)
	bill := AgentSimpleBill{AgentUserID: agent.UserID, WeekStart: now.AddDate(0, 0, -7).Unix(), Quota: 1000, DueAt: now.Unix() - 1}
	require.NoError(t, DB.Create(&bill).Error)
	for range 2 {
		unread, err := ListUnreadAgentSimpleReminders(agent)
		require.NoError(t, err)
		require.Len(t, unread, 1)
		assert.Equal(t, rows[0].ID, unread[0].ID)
	}
	require.ErrorIs(t, AcknowledgeAgentSimpleReminders(agent, []int64{rows[0].ID, rows[2].ID}), ErrAgentSimplePermission)
	var count int64
	require.NoError(t, DB.Model(&AgentSimpleReminderReceipt{}).Count(&count).Error)
	assert.Zero(t, count, "a mixed unauthorized batch cannot acknowledge any reminder")
	for range 2 {
		require.NoError(t, AcknowledgeAgentSimpleReminders(agent, []int64{rows[0].ID}))
	}
	unread, err := ListUnreadAgentSimpleReminders(agent)
	require.NoError(t, err)
	assert.Empty(t, unread)
	unread, err = ListUnreadAgentSimpleReminders(root)
	require.NoError(t, err)
	require.Len(t, unread, 1)
	assert.Equal(t, rows[1].ID, unread[0].ID)
	require.NoError(t, AcknowledgeAgentSimpleReminders(root, []int64{rows[1].ID}))
	require.NoError(t, DB.Create(&User{Id: 78220, Username: "a14-other-root", AffCode: "a14-other-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}).Error)
	otherRoot := agentSimpleIdentity(t, 78220, "a14-other-root")
	unread, err = ListUnreadAgentSimpleReminders(otherRoot)
	require.NoError(t, err)
	require.Len(t, unread, 1, "each root receives their own daily notice")
	require.NoError(t, DB.Model(&bill).Update("paid_quota", bill.Quota).Error)
	unread, err = ListUnreadAgentSimpleReminders(otherRoot)
	require.NoError(t, err)
	assert.Empty(t, unread, "settled debt no longer produces an actionable popup")
	require.NoError(t, DB.Model(&AgentSimpleReminderReceipt{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}

func TestAgentSimpleReminderAcknowledgementRejectsAdminPATAndExpiredSessions(t *testing.T) {
	_, agent := simpleAccountingFixture(t)
	row := AgentSimpleReminder{AgentUserID: agent.UserID, Day: time.Now().In(agentSimpleLocation).Format("2006-01-02"), Audience: "agent", Quota: 1000}
	require.NoError(t, DB.Create(&row).Error)
	admin := agentSimpleIdentity(t, agentTestAdminID, "a14-admin-session")
	pat := agent
	pat.SessionID = AccessTokenSessionID(7)
	for _, identity := range []AuthSessionIdentity{admin, pat} {
		_, err := ListUnreadAgentSimpleReminders(identity)
		require.ErrorIs(t, err, ErrAgentSimplePermission)
		require.ErrorIs(t, AcknowledgeAgentSimpleReminders(identity, []int64{row.ID}), ErrAgentSimplePermission)
	}
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", agent.SessionID).Update("expires_at", common.GetTimestamp()-1).Error)
	require.ErrorIs(t, AcknowledgeAgentSimpleReminders(agent, []int64{row.ID}), ErrAgentSimplePermission)
	var count int64
	require.NoError(t, DB.Model(&AgentSimpleReminderReceipt{}).Count(&count).Error)
	assert.Zero(t, count)
}
