//go:build enterprise_lane_lab

package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnterpriseLaneStartupRealDrainProjectionPostgres(t *testing.T) {
	id := enterpriseLaneFixture(t)
	t.Setenv("LOG_SQL_DSN", "")
	channel := Channel{Name: id.LaneID, Key: "synthetic-startup-fixture"}
	require.NoError(t, DB.Create(&channel).Error)
	t.Cleanup(func() {
		for _, row := range []any{&EnterpriseLaneUsageProjection{}, &Log{}} {
			require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Where("user_id = ?", id.UserID).Delete(row).Error)
		}
		require.NoError(t, DB.Unscoped().Delete(&channel).Error)
	})
	_, err := CreateEnterpriseLane(t.Context(), DB, id, 200, 100)
	require.NoError(t, err)
	require.ErrorIs(t, CheckEnterpriseLaneGatewayDisabled(t.Context(), DB), ErrEnterpriseLaneState)
	rid := id.LaneID + "-startup"
	_, err = ReserveEnterpriseLane(t.Context(), DB, id, rid, 40)
	require.NoError(t, err)
	_, err = MarkEnterpriseLaneDispatched(t.Context(), DB, id, rid)
	require.NoError(t, err)
	usage := EnterpriseLaneUsage{ChannelID: channel.Id, PromptTokens: 90, CompletionTokens: 10, Model: "fixture", Group: "fixture", ObservedAt: common.GetTimestamp()}
	_, err = SettleEnterpriseLaneWithUsage(t.Context(), DB, id, rid, 30, usage)
	require.NoError(t, err)
	_, err = StopEnterpriseLane(t.Context(), DB, id)
	require.NoError(t, err)
	_, err = DrainEnterpriseLane(t.Context(), DB, id)
	require.NoError(t, err)
	checkLaneBaseAccounts(t, id, 970, 970, 130)
	require.ErrorIs(t, CheckEnterpriseLaneGatewayDisabled(t.Context(), DB), ErrEnterpriseLaneState, "financial drain cannot strand the usage outbox")
	projected, err := ProjectEnterpriseLaneUsageBatch(t.Context(), DB, 8)
	require.NoError(t, err)
	require.Equal(t, 1, projected)
	for range 2 {
		require.NoError(t, CheckEnterpriseLaneGatewayDisabled(t.Context(), DB))
		checkLaneBaseAccounts(t, id, 970, 970, 130)
	}
}
