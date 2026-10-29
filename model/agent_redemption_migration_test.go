package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Exact pre-feature production Redemption shape: this fixture deliberately
// omits every agent-budget column rather than testing a zero-filled new schema.
type agentPreFeatureRedemption struct {
	Id           int            `json:"id"`
	UserId       int            `json:"user_id"`
	Key          string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status       int            `json:"status" gorm:"default:1"`
	Name         string         `json:"name" gorm:"index"`
	Quota        int            `json:"quota" gorm:"default:100"`
	CreatedTime  int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime int64          `json:"redeemed_time" gorm:"bigint"`
	Count        int            `json:"count" gorm:"-:all"`
	UsedUserId   int            `json:"used_user_id"`
	DeletedAt    gorm.DeletedAt `gorm:"index"`
	ExpiredTime  int64          `json:"expired_time" gorm:"bigint"`
}

func (agentPreFeatureRedemption) TableName() string { return "redemptions" }

func TestAgentRedemptionPostgresLegacyUpgradeRepeatedMigrationAndForwardRecovery(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires PostgreSQL 18.6 TEST_MODEL_POSTGRES_DSN")
	}
	admin, agent := setupAgentRedemptionTest(t)
	require.NoError(t, DB.Migrator().DropTable(&Redemption{}, &AgentRedemptionLedger{}, &AgentRedemptionOperation{}, &AgentRedemptionAccount{}))
	require.NoError(t, DB.AutoMigrate(&agentPreFeatureRedemption{}))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("role", common.RoleCommonUser).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agentTestRecipientID).Updates(map[string]any{"quota": 750, "quota_paid": 500, "quota_bonus": 200, "quota_reward": 50}).Error)
	walletBefore := getUserBalances(t, agentTestRecipientID)
	actorBefore := getUserBalances(t, agent.UserID)
	legacy := []agentPreFeatureRedemption{
		{UserId: admin.UserID, Key: "00000000000000000000000000000101", Status: common.RedemptionCodeStatusEnabled, Name: "legacy enabled", Quota: 111, CreatedTime: 100, ExpiredTime: 0},
		{UserId: admin.UserID, Key: "00000000000000000000000000000102", Status: common.RedemptionCodeStatusUsed, Name: "legacy used", Quota: 222, CreatedTime: 101, RedeemedTime: 150, UsedUserId: agentTestRecipientID},
		{UserId: admin.UserID, Key: "00000000000000000000000000000103", Status: common.RedemptionCodeStatusDisabled, Name: "legacy disabled", Quota: 333, CreatedTime: 102},
	}
	require.NoError(t, DB.Create(&legacy).Error)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&Redemption{}, &AgentRedemptionAccount{}, &AgentRedemptionOperation{}, &AgentRedemptionLedger{}))
		for _, old := range legacy {
			var upgraded Redemption
			require.NoError(t, DB.First(&upgraded, old.Id).Error)
			assert.Equal(t, old.UserId, upgraded.UserId)
			assert.Equal(t, old.Key, upgraded.Key)
			assert.Equal(t, old.Status, upgraded.Status)
			assert.Equal(t, old.Name, upgraded.Name)
			assert.Equal(t, old.Quota, upgraded.Quota)
			assert.Equal(t, old.CreatedTime, upgraded.CreatedTime)
			assert.Equal(t, old.RedeemedTime, upgraded.RedeemedTime)
			assert.Equal(t, old.UsedUserId, upgraded.UsedUserId)
			assert.Equal(t, old.ExpiredTime, upgraded.ExpiredTime)
			assert.Zero(t, upgraded.BudgetAgentID)
			assert.False(t, upgraded.BudgetReserved)
			assert.Zero(t, upgraded.BudgetVersion)
			assert.Zero(t, upgraded.CreationOperationID)
		}
		assert.Equal(t, walletBefore, getUserBalances(t, agentTestRecipientID), "startup migrations cannot change wallet accounts")
		assert.Equal(t, actorBefore, getUserBalances(t, agent.UserID), "startup migrations cannot silently promote old users")
		var accounts, ledgerRows int64
		require.NoError(t, DB.Model(&AgentRedemptionAccount{}).Count(&accounts).Error)
		require.NoError(t, DB.Model(&AgentRedemptionLedger{}).Count(&ledgerRows).Error)
		assert.Zero(t, accounts)
		assert.Zero(t, ledgerRows)
		duplicate := Redemption{UserId: admin.UserID, Name: "duplicate", Key: legacy[0].Key, Quota: 1}
		require.Error(t, DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Create(&duplicate).Error, "upgrade must preserve redemption credential uniqueness")
	}
	// Forward recovery after real feature use: restarting/migrating must preserve
	// receipts and may resume cleanup, never infer fresh reservations from status.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("role", common.RoleAgentUser).Error)
	allocateAgentTestQuota(t, admin, agent.UserID, 1000, "migration-feature-budget")
	request := RedemptionCreateRequest{Name: "forward recovery", Quota: 100, Count: 3, RequestID: "migration-feature-create"}
	keys, err := CreateRedemptionsScoped(agent, request)
	require.NoError(t, err)
	_, err = Redeem(keys[0], agentTestRecipientID)
	require.NoError(t, err)
	var expiring Redemption
	require.NoError(t, DB.Where("key = ?", keys[1]).First(&expiring).Error)
	require.NoError(t, DB.Model(&expiring).Update("expired_time", common.GetTimestamp()-1).Error)
	assertAgentBudget(t, agent.UserID, 700, 200, 100, 1000)
	for pass := range 2 {
		require.NoError(t, DB.AutoMigrate(&Redemption{}, &AgentRedemptionAccount{}, &AgentRedemptionOperation{}, &AgentRedemptionLedger{}))
		replayed, err := CreateRedemptionsScoped(agent, request)
		require.NoError(t, err)
		assert.Equal(t, keys, replayed)
		count, err := ReleaseExpiredAgentRedemptions(100)
		require.NoError(t, err)
		assert.EqualValues(t, 1-pass, count)
		assertAgentBudget(t, agent.UserID, 800, 100, 100, 1000)
		assert.Equal(t, walletBefore.QuotaPaid+100, getUserBalances(t, agentTestRecipientID).QuotaPaid)
		_, err = Redeem(keys[0], agentTestRecipientID)
		require.ErrorIs(t, err, ErrRedeemFailed)
	}
}
