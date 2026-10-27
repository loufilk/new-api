package model

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestConsumeLogExportsCacheUsageWithSeparateLogDatabase(t *testing.T) {
	open := func(name string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name)), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
		return db
	}
	mainDB, logDB := open("main.db"), open("log.db")
	require.NoError(t, mainDB.AutoMigrate(&User{}, &QuotaData{}))
	require.NoError(t, logDB.AutoMigrate(&Log{}))
	require.NoError(t, mainDB.Create(&User{Id: 1, Username: "split-cache-user", Setting: "{}"}).Error)

	previousDB, previousLogDB := DB, LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousExport, previousConsume := common.RedisEnabled, common.DataExportEnabled, common.LogConsumeEnabled
	DB, LOG_DB = mainDB, logDB
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled, common.DataExportEnabled, common.LogConsumeEnabled = false, true, true
	CacheQuotaDataLock.Lock()
	previousCache := CacheQuotaData
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.RedisEnabled, common.DataExportEnabled, common.LogConsumeEnabled = previousRedis, previousExport, previousConsume
		CacheQuotaDataLock.Lock()
		CacheQuotaData = previousCache
		CacheQuotaDataLock.Unlock()
	})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("username", "split-cache-user")
	other := NewLogOther()
	other.SetPublic("cache_tokens", 7)
	other.SetPublic("cache_write_tokens", 5)
	RecordConsumeLog(c, 1, RecordConsumeLogParams{
		ModelName: "split-cache-model", PromptTokens: 20, CompletionTokens: 3,
		Quota: 9, TokenId: 4, ChannelId: 6, Group: "default", Other: other,
	})
	SaveQuotaDataCache()

	var metrics QuotaData
	require.NoError(t, mainDB.Where("model_name = ?", "split-cache-model").First(&metrics).Error)
	assert.EqualValues(t, 7, metrics.CacheReadTokens)
	assert.EqualValues(t, 5, metrics.CacheCreationTokens)
	assert.Equal(t, 23, metrics.TokenUsed, "cache categories must not inflate token totals")
	assert.Equal(t, 9, metrics.Quota)
	assert.Equal(t, 1, metrics.Count)
	var log Log
	require.NoError(t, logDB.Where("model_name = ?", "split-cache-model").First(&log).Error)
	assert.Equal(t, 9, log.Quota)
	assert.False(t, mainDB.Migrator().HasTable(&Log{}), "consumption logs belong to the separate log database")
	assert.False(t, logDB.Migrator().HasTable(&QuotaData{}), "dashboard metrics remain in the main database")
}
