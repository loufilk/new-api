package model

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestQuotaReminderThresholdOptionsRejectInvalidWithoutMutation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quota-reminder.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	oldDB, oldMap, oldThreshold := DB, common.OptionMap, common.QuotaRemindThreshold
	oldMain, oldLog, oldRedis := common.MainDatabaseType(), common.LogDatabaseType(), common.RedisEnabled
	oldPolicy := requestPolicySnapshot.Load()
	oldVersion, oldInitialized := groupConfigAppliedVersion.Load(), groupConfigVersionInitialized.Load()
	DB, common.OptionMap, common.QuotaRemindThreshold = db, map[string]string{}, 1234
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	t.Cleanup(func() {
		DB, common.OptionMap, common.QuotaRemindThreshold = oldDB, oldMap, oldThreshold
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled = oldRedis
		requestPolicySnapshot.Store(oldPolicy)
		groupConfigAppliedVersion.Store(oldVersion)
		groupConfigVersionInitialized.Store(oldInitialized)
		_ = sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&Option{}))
	const key, companion = "QuotaRemindThreshold", "ThresholdValidationCompanion"
	reset := func() {
		require.NoError(t, UpdateOptionsBulk(map[string]string{key: "1234", companion: "original"}))
	}
	assertSaved := func(want string) {
		t.Helper()
		var row Option
		require.NoError(t, db.Where("key = ?", key).First(&row).Error)
		assert.Equal(t, want, row.Value)
	}
	for _, value := range []string{"", "garbage", "Infinity", "NaN", "-1", "1.5", "1e3", "9223372036854775808", "9007199254740992", "<nil>"} {
		t.Run("invalid_"+value, func(t *testing.T) {
			reset()
			assert.Error(t, UpdateOption(key, value))
			assertSaved("1234")
			assert.Equal(t, 1234, common.QuotaRemindThreshold)
			assert.Equal(t, "1234", common.OptionMap[key])
			reset()
			assert.Error(t, UpdateOptionsBulk(map[string]string{key: value, companion: "changed"}))
			assertSaved("1234")
			var other Option
			require.NoError(t, db.Where("key = ?", companion).First(&other).Error)
			assert.Equal(t, "original", other.Value)
			assert.Equal(t, "original", common.OptionMap[companion])
			assert.Equal(t, 1234, common.QuotaRemindThreshold)
			assert.Equal(t, "1234", common.OptionMap[key])
			reset()
			require.NoError(t, db.Model(&Option{}).Where("key = ?", key).Update("value", value).Error)
			assert.Error(t, updateOptionMap(key, value), "malformed legacy values must return an explicit error")
			loadOptionsFromDatabase()
			loadOptionsFromDatabase()
			assertSaved(value)
			assert.Equal(t, 1234, common.QuotaRemindThreshold, "reload retains valid runtime/default rather than substituting zero")
			assert.Equal(t, "1234", common.OptionMap[key])
		})
	}
	maximum := "9007199254740991"
	if strconv.IntSize == 32 {
		maximum = "2147483647"
	}
	for _, value := range []string{"0", "1", "500000", maximum} {
		t.Run("valid_"+value, func(t *testing.T) {
			reset()
			require.NoError(t, UpdateOption(key, value))
			assertSaved(value)
			want, err := strconv.Atoi(value)
			require.NoError(t, err)
			assert.Equal(t, want, common.QuotaRemindThreshold)
			require.NoError(t, UpdateOptionsBulk(map[string]string{key: value, companion: "changed"}))
			assertSaved(value)
		})
	}
}
