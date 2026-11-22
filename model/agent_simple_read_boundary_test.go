package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentSimpleLegacyRedemptionReadsCannotBypassAuditedCodeReveal(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Update("cash_quota", 500_000).Error)
	code, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "a07-read-boundary", Name: "Private customer code", Quota: 500_000, Mode: AgentSimpleCodeDirect})
	require.NoError(t, err)
	legacy := Redemption{UserId: root.UserID, Key: strings.Repeat("l", 32), Name: "Existing admin code", Quota: 500_000, Status: common.RedemptionCodeStatusEnabled}
	require.NoError(t, DB.Create(&legacy).Error)
	for _, role := range []int{common.RoleRootUser, common.RoleAdminUser} {
		scope := RedemptionScope{UserID: root.UserID, Role: role}
		rows, total, err := GetRedemptionsScoped(scope, 0, "", "", 0, 20)
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		require.Len(t, rows, 1)
		assert.Equal(t, legacy.Key, rows[0].Key, "ordinary admin code management remains available")
		_, err = GetRedemptionScoped(scope, code.Id)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	}
	rows, _, err := GetAgentSimpleRecords(root, agent.UserID, 0, 20, "")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Empty(t, rows[0].Key)
	assert.NotEmpty(t, rows[0].MaskedKey)
	key, err := RevealAgentSimpleCode(root, code.Id, "Synthetic inspection")
	require.NoError(t, err)
	assert.Equal(t, code.Key, key)
}

func TestAgentSimpleLegacyCleanupIncludesUpgradedNullModeWithoutTouchingSimpleCodes(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Update("cash_quota", 500_000).Error)
	code, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "a07-cleanup-kept", Name: "Agent code", Quota: 500_000, Mode: AgentSimpleCodeDirect})
	require.NoError(t, err)
	_, err = CancelAgentSimpleCode(agent, AgentSimpleCodeCancelRequest{RequestID: "a07-cleanup-cancel", CodeID: code.Id, Version: int64(code.BudgetVersion), Reason: "Synthetic code return"})
	require.NoError(t, err)
	legacy := Redemption{UserId: root.UserID, Key: strings.Repeat("n", 32), Name: "Legacy used code", Quota: 500_000, Status: common.RedemptionCodeStatusUsed}
	require.NoError(t, DB.Create(&legacy).Error)
	require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", legacy.Id).UpdateColumn("agent_simple_mode", nil).Error)
	deleted, err := DeleteRedemptionsScoped(RedemptionScope{UserID: root.UserID, Role: common.RoleRootUser, system: true}, nil, true)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)
	var preserved Redemption
	require.NoError(t, DB.First(&preserved, code.Id).Error)
	assert.Equal(t, AgentSimpleCodeDirect, preserved.AgentSimpleMode)
}

func TestAgentSimpleLegacyPrepareCannotExposePrivateCodeMetadata(t *testing.T) {
	root, agent := simpleAccountingFixture(t)
	require.NoError(t, DB.Model(&AgentSimpleAccount{}).Where("agent_user_id = ?", agent.UserID).Update("cash_quota", 500_000).Error)
	code, err := CreateAgentSimpleCode(agent, AgentSimpleCodeRequest{RequestID: "a07-prepare-boundary", Name: "Private customer code", Quota: 500_000, Mode: AgentSimpleCodeDirect})
	require.NoError(t, err)
	legacy := Redemption{UserId: root.UserID, Key: strings.Repeat("p", 32), Name: "Existing admin code", Quota: 1234, Status: common.RedemptionCodeStatusEnabled, BudgetVersion: 1}
	require.NoError(t, DB.Create(&legacy).Error)
	admin := agentSimpleIdentity(t, agentTestAdminID, "a07-prepare-admin")
	for _, identity := range []AuthSessionIdentity{root, admin} {
		payload, err := common.Marshal(AgentLegacyRedemptionUpdateRequest{RedemptionUpdateRequest: RedemptionUpdateRequest{ID: code.Id, Version: int64(code.BudgetVersion), Status: common.RedemptionCodeStatusDisabled}, StatusOnly: true})
		require.NoError(t, err)
		prepared, err := PrepareAgentLegacyRedemptionOperation(identity, "redemption.admin.update", "a07-private-update", payload)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		assert.Nil(t, prepared)
		for _, ids := range [][]int{{code.Id}, {legacy.Id, code.Id}} {
			payload, err = common.Marshal(AgentLegacyRedemptionDeleteRequest{IDs: ids})
			require.NoError(t, err)
			prepared, err = PrepareAgentLegacyRedemptionOperation(identity, "redemption.admin.delete", "a07-private-delete", payload)
			require.ErrorIs(t, err, ErrRedemptionInvalidRequest)
			assert.Nil(t, prepared)
		}
		payload, err = common.Marshal(AgentLegacyRedemptionUpdateRequest{RedemptionUpdateRequest: RedemptionUpdateRequest{ID: legacy.Id, Version: 1, Status: common.RedemptionCodeStatusDisabled}, StatusOnly: true})
		require.NoError(t, err)
		prepared, err = PrepareAgentLegacyRedemptionOperation(identity, "redemption.admin.update", "a07-public-update", payload)
		require.NoError(t, err)
		assert.EqualValues(t, legacy.Quota, prepared.Summary.Quota)
	}
	var unchanged Redemption
	require.NoError(t, DB.First(&unchanged, code.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, unchanged.Status)
	assert.Equal(t, code.BudgetVersion, unchanged.BudgetVersion)
}
