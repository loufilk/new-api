package model

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestConsumeLogExportsNormalizedCacheUsage(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "consume-cache.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&User{}, &Log{}, &QuotaData{}))
	require.NoError(t, db.Create(&User{Id: 1, Username: "cache-user", Setting: "{}"}).Error)

	previousDB, previousLogDB := DB, LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousExport, previousConsume := common.RedisEnabled, common.DataExportEnabled, common.LogConsumeEnabled
	DB, LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled, common.DataExportEnabled, common.LogConsumeEnabled = false, true, true
	CacheQuotaDataLock.Lock()
	previousCache := CacheQuotaData
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousMainType)
		common.SetLogDatabaseType(previousLogType)
		common.RedisEnabled, common.DataExportEnabled, common.LogConsumeEnabled = previousRedis, previousExport, previousConsume
		CacheQuotaDataLock.Lock()
		CacheQuotaData = previousCache
		CacheQuotaDataLock.Unlock()
	})

	for _, test := range []struct {
		name                string
		metadata            map[string]any
		cacheRead, cachePut int64
	}{
		{"normalized cache totals", map[string]any{"cache_tokens": 11, "cache_write_tokens": 9, "cache_creation_5m_tokens": 3, "cache_creation_1h_tokens": 6}, 11, 9},
		{"missing cache usage", nil, 0, 0},
		{"negative cache usage", map[string]any{"cache_tokens": -11, "cache_write_tokens": -9}, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("username", "cache-user")
			other := NewLogOther()
			other.MergePublic(test.metadata)
			RecordConsumeLog(c, 1, RecordConsumeLogParams{
				ModelName: test.name, PromptTokens: 20, CompletionTokens: 7,
				Quota: 4, TokenId: 8, ChannelId: 9, Group: "default", Other: other,
			})
			SaveQuotaDataCache()
			var row QuotaData
			require.NoError(t, db.Where("model_name = ?", test.name).First(&row).Error)
			assert.Equal(t, 27, row.TokenUsed, "cached tokens are a category, not additional total tokens")
			assert.Equal(t, test.cacheRead, row.CacheReadTokens)
			assert.Equal(t, test.cachePut, row.CacheCreationTokens, "normalized total must not add TTL subtotals again")
			assert.Equal(t, 8, row.TokenID)
			assert.Equal(t, 9, row.ChannelID)
			assert.Equal(t, "default", row.UseGroup)
			var stored Log
			require.NoError(t, db.Where("model_name = ?", test.name).First(&stored).Error)
			assert.Equal(t, 4, stored.Quota)
		})
	}
}

// This is the schema immediately before cache usage counters were introduced.
// Keep it separate from QuotaData so the upgrade test starts from a real old table.
type quotaDataBeforeCache struct {
	Id        int
	UserID    int    `gorm:"index"`
	Username  string `gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName string `gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	CreatedAt int64  `gorm:"bigint;index:idx_qdt_created_at,priority:2"`
	UseGroup  string `gorm:"index;size:64;default:''"`
	TokenID   int    `gorm:"index;default:0"`
	ChannelID int    `gorm:"index;default:0"`
	NodeName  string `gorm:"index;size:64;default:''"`
	TokenUsed int    `gorm:"default:0"`
	Count     int    `gorm:"default:0"`
	Quota     int    `gorm:"default:0"`
}

func (quotaDataBeforeCache) TableName() string { return "quota_data" }

// TEST_*_DSN must refer to dedicated test databases: each case recreates quota_data.
func TestQuotaDataCacheUsageDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "quota-data.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
			var version string
			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("%s version: %s", dialect, version)

			for _, migration := range []string{"fresh", "upgrade"} {
				t.Run(migration, func(t *testing.T) {
					require.NoError(t, db.Migrator().DropTable(&QuotaData{}))
					t.Cleanup(func() { assert.NoError(t, db.Migrator().DropTable(&QuotaData{})) })
					previousDB, previousType := DB, common.MainDatabaseType()
					DB = db
					common.SetMainDatabaseType(common.DatabaseType(dialect))
					CacheQuotaDataLock.Lock()
					previousCache := CacheQuotaData
					CacheQuotaData = make(map[string]*QuotaData)
					CacheQuotaDataLock.Unlock()
					t.Cleanup(func() {
						DB = previousDB
						common.SetMainDatabaseType(previousType)
						CacheQuotaDataLock.Lock()
						CacheQuotaData = previousCache
						CacheQuotaDataLock.Unlock()
					})

					historical := quotaDataBeforeCache{UserID: 9, Username: "historical", ModelName: "legacy-model", CreatedAt: 7200, UseGroup: "legacy", TokenID: 5, ChannelID: 6, NodeName: "old-node", Count: 2, Quota: 37, TokenUsed: 59}
					var oldIndexes []gorm.Index
					if migration == "upgrade" {
						require.NoError(t, db.AutoMigrate(&quotaDataBeforeCache{}))
						require.False(t, db.Migrator().HasColumn(&QuotaData{}, "cache_read_tokens"))
						require.NoError(t, db.Create(&historical).Error)
						oldIndexes, err = db.Migrator().GetIndexes(&quotaDataBeforeCache{})
						require.NoError(t, err)
					}
					for range 2 {
						require.NoError(t, db.AutoMigrate(&QuotaData{}))
					}
					require.True(t, db.Migrator().HasColumn(&QuotaData{}, "cache_read_tokens"))
					require.True(t, db.Migrator().HasColumn(&QuotaData{}, "cache_creation_tokens"))
					for _, index := range oldIndexes {
						assert.True(t, db.Migrator().HasIndex(&QuotaData{}, index.Name()), "upgrade preserves existing index %s", index.Name())
					}
					if migration == "upgrade" {
						var oldRow quotaDataBeforeCache
						require.NoError(t, db.First(&oldRow, historical.Id).Error)
						assert.Equal(t, historical, oldRow, "upgrade preserves every original column")
						var upgraded QuotaData
						require.NoError(t, db.First(&upgraded, historical.Id).Error)
						assert.Zero(t, upgraded.CacheReadTokens)
						assert.Zero(t, upgraded.CacheCreationTokens, "historical usage is not fabricated")
					}

					params := QuotaDataLogParams{UserID: 1, Username: "alice", ModelName: "cache-model", CreatedAt: 3607, UseGroup: "default", TokenID: 1, ChannelID: 1, NodeName: "node-a", Quota: 10, TokenUsed: 15, CacheReadTokens: 3, CacheCreationTokens: 5}
					LogQuotaData(params)
					params.CacheReadTokens, params.CacheCreationTokens = 7, 11
					LogQuotaData(params)
					SaveQuotaDataCache()
					params.CacheReadTokens, params.CacheCreationTokens = -4, -9
					LogQuotaData(params)
					SaveQuotaDataCache()
					params.TokenID, params.CacheReadTokens, params.CacheCreationTokens = 2, 4, 9
					LogQuotaData(params)
					params.UserID, params.Username, params.CacheReadTokens, params.CacheCreationTokens = 2, "bob", 13, 17
					LogQuotaData(params)
					SaveQuotaDataCache()

					queries := []struct {
						name                           string
						read                           func() ([]*QuotaData, error)
						count, readTokens, writeTokens int
					}{
						{"username", func() ([]*QuotaData, error) { return GetQuotaDataByUsername("alice", 3600, 7199) }, 4, 14, 25},
						{"user ID", func() ([]*QuotaData, error) { return GetQuotaDataByUserId(1, 3600, 7199) }, 4, 14, 25},
						{"all models", func() ([]*QuotaData, error) { return GetAllQuotaDates(3600, 7199, "") }, 5, 27, 42},
						{"all models filtered by user", func() ([]*QuotaData, error) { return GetAllQuotaDates(3600, 7199, "alice") }, 4, 14, 25},
					}
					for _, query := range queries {
						rows, err := query.read()
						require.NoError(t, err, query.name)
						require.Len(t, rows, 1, query.name)
						assert.Equal(t, query.count, rows[0].Count, query.name)
						assert.Equal(t, query.count*10, rows[0].Quota, query.name)
						assert.Equal(t, query.count*15, rows[0].TokenUsed, "cache counters do not inflate existing token totals")
						assert.EqualValues(t, query.readTokens, rows[0].CacheReadTokens, query.name)
						assert.EqualValues(t, query.writeTokens, rows[0].CacheCreationTokens, query.name)
					}
					users, err := GetQuotaDataGroupByUser(3600, 7199)
					require.NoError(t, err)
					require.Len(t, users, 2)
					byUser := make(map[string]*QuotaData, len(users))
					for _, user := range users {
						byUser[user.Username] = user
					}
					require.Contains(t, byUser, "alice")
					require.Contains(t, byUser, "bob")
					assert.EqualValues(t, 14, byUser["alice"].CacheReadTokens)
					assert.EqualValues(t, 25, byUser["alice"].CacheCreationTokens)
					assert.EqualValues(t, 13, byUser["bob"].CacheReadTokens)
					assert.EqualValues(t, 17, byUser["bob"].CacheCreationTokens)
				})
			}
		})
	}
}
