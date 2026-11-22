package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
)

const (
	agentTestAdminID     = 78001
	agentTestOwnerID     = 78002
	agentTestOtherID     = 78003
	agentTestRecipientID = 78004
)

func setupAgentRedemptionTest(t *testing.T) (RedemptionScope, RedemptionScope) {
	t.Helper()
	setupLedgerTestState(t)
	require.NoError(t, DB.AutoMigrate(&Redemption{}, &AgentRedemptionAccount{}, &AgentRedemptionOperation{}, &AgentRedemptionLedger{}))
	for _, table := range []string{"redemptions", "agent_redemption_ledgers", "agent_redemption_operations", "agent_redemption_accounts"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() {
		for _, table := range []string{"redemptions", "agent_redemption_ledgers", "agent_redemption_operations", "agent_redemption_accounts"} {
			require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
		}
	})
	for _, user := range []User{
		{Id: agentTestAdminID, Username: "agent-budget-admin", AffCode: "agent-budget-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled},
		{Id: agentTestOwnerID, Username: "agent-budget-owner", AffCode: "agent-budget-owner", Role: common.RoleAgentUser, Status: common.UserStatusEnabled},
		{Id: agentTestOtherID, Username: "agent-budget-other", AffCode: "agent-budget-other", Role: common.RoleAgentUser, Status: common.UserStatusEnabled},
		{Id: agentTestRecipientID, Username: "agent-budget-recipient", AffCode: "agent-budget-recipient", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
	} {
		require.NoError(t, DB.Create(&user).Error)
	}

	for _, id := range []int{agentTestOwnerID, agentTestOtherID} {
		require.NoError(t, DB.Create(&AgentProfile{UserID: id, Status: AgentProfileActive, Version: 1, Reason: "test business eligibility"}).Error)
	}
	return RedemptionScope{UserID: agentTestAdminID, Role: common.RoleAdminUser}, RedemptionScope{UserID: agentTestOwnerID, Role: common.RoleAgentUser}
}
