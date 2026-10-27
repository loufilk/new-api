package model

import (
	"context"
	"net"
	"net/url"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupRenamePostgresRedis(t *testing.T) {
	dsn := os.Getenv("TEST_GROUP_RENAME_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated TEST_GROUP_RENAME_POSTGRES_DSN and TEST_REDIS_ADDR")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback(), "only disposable loopback fixtures are permitted")
	require.NotEmpty(t, os.Getenv("TEST_REDIS_ADDR"))
	previousDB, previousLog := DB, LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousRedis, previousRDB := common.IsMasterNode, common.RedisEnabled, common.RDB
	installDatabaseCompatRegistry(t)
	t.Setenv("SQL_DSN", dsn)
	t.Setenv("LOG_SQL_DSN", dsn)
	common.IsMasterNode = true
	require.NoError(t, InitDB())
	require.NoError(t, InitLogDB())
	rdb := redis.NewClient(&redis.Options{Addr: os.Getenv("TEST_REDIS_ADDR")})
	require.NoError(t, rdb.Ping(context.Background()).Err())
	common.RedisEnabled, common.RDB = true, rdb
	t.Cleanup(func() {
		require.NoError(t, rdb.Close())
		require.NoError(t, CloseDB())
		DB, LOG_DB = previousDB, previousLog
		common.IsMasterNode, common.RedisEnabled, common.RDB = previousMaster, previousRedis, previousRDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		initCol()
	})
	var version string
	require.NoError(t, DB.Raw("SHOW server_version").Scan(&version).Error)
	t.Logf("PostgreSQL server_version=%s", version)
	var versionNum string
	require.NoError(t, DB.Raw("SHOW server_version_num").Scan(&versionNum).Error)
	require.Equal(t, "180006", versionNum)
	for _, scenario := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"references_and_history", TestGroupRenameMigratesEveryGroupSurface},
		{"invalid_mappings", TestGroupRenameRejectsInvalidMappings},
		{"rollback", TestGroupRenameRollsBackAllWritesOnFailure},
		{"duplicate", TestGroupRenameDuplicateSubmissionIsRejectedAfterCommit},
		{"decimals", TestGroupRenamePreservesExactRatioDecimals},
		{"ability_attributes", TestGroupRenamePreservesAbilityAttributes},
		{"effective_subscriptions", TestGroupRenameMigratesOnlyActiveSubscriptions},
		{"malformed_auto_groups", TestGroupRenameRejectsMalformedAutoGroupsWithoutWrites},
		{"escaped_keys", TestGroupRenameMigratesEscapedAutoGroupKeys},
		{"atomic_options", TestGroupRenameCombinedOptionsCommitAtomically},
		{"stale_settings", TestGroupRenameRejectsStaleExpectedOptionValues},
		{"referenced_removal", TestGroupRenameRejectsReferencedGroupRemoval},
		{"ambiguous_replacement", TestGroupRenameRejectsAmbiguousReplacementWithoutMapping},
	} {
		t.Run(scenario.name, scenario.run)
	}
	t.Run("concurrent_group_writers", testGroupNamespaceConcurrentWriters)
	t.Run("subscription_plan_map_writer", testGroupNamespaceSubscriptionPlanMapWriter)
	t.Run("subscription_plan_legacy_group_edits", testSubscriptionPlanLegacyGroupEdits)
	t.Run("namespace_rollback", testGroupNamespaceRollbackState)
	t.Run("group_config_pubsub_and_fallback", testGroupConfigPubSubAndFallback)
	t.Run("two_process_group_runtime", testGroupConfigTwoProcessInstances)
	t.Run("warm_caches", func(t *testing.T) {
		setupGroupRenameTest(t)
		seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
		user := User{Username: "cached-rename", AffCode: "cached-rename", Status: common.UserStatusEnabled, AuthVersion: 1, Group: "old"}
		require.NoError(t, DB.Create(&user).Error)
		token := Token{UserId: user.Id, Key: "synthetic-cached-rename", Group: "old", AutoGroups: `["old"]`, Status: common.TokenStatusEnabled}
		require.NoError(t, DB.Create(&token).Error)
		plan := SubscriptionPlan{Title: "cached-plan", UpgradeGroup: "old", DowngradeGroup: "old"}
		require.NoError(t, DB.Create(&plan).Error)
		cachedToken, err := GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		assert.Equal(t, "old", cachedToken.Group)
		_, err = cacheGetTokenByKey(token.Key)
		require.NoError(t, err, "the test must start with an actual Redis hit")
		cachedUser, err := GetUserCache(user.Id)
		require.NoError(t, err)
		assert.Equal(t, "old", cachedUser.Group)
		cachedPlan, err := GetSubscriptionPlanById(plan.Id)
		require.NoError(t, err)
		assert.Equal(t, "old", cachedPlan.UpgradeGroup)
		_, err = RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
		require.NoError(t, err)
		cachedToken, err = GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		assert.Equal(t, "new", cachedToken.Group)
		assert.JSONEq(t, `["new"]`, cachedToken.AutoGroups)
		cachedUser, err = GetUserCache(user.Id)
		require.NoError(t, err)
		assert.Equal(t, "new", cachedUser.Group)
		cachedPlan, err = GetSubscriptionPlanById(plan.Id)
		require.NoError(t, err)
		assert.Equal(t, "new", cachedPlan.UpgradeGroup)
		assert.Equal(t, "new", cachedPlan.DowngradeGroup)
	})
}
