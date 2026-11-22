package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestChannelPublicOwnerValidation(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"empty keeps default", "", true},
		{"spaces keep default", "   ", true},
		{"unicode label", " 万川直营 ", true},
		{"unicode boundary", strings.Repeat("川", 64), true},
		{"overlong", strings.Repeat("川", 65), false},
		{"newline", "public\nprivate", false},
		{"leading newline", "\npublic", false},
		{"bidi override", "public\u202eprivate", false},
		{"zero width", "public\u200bprivate", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := Channel{Type: constant.ChannelTypeOpenAI}
			channel.SetSetting(dto.ChannelSettings{ModelOwnedBy: tc.value})
			if tc.valid {
				require.NoError(t, channel.ValidateSettings())
			} else {
				require.ErrorContains(t, channel.ValidateSettings(), "model_owned_by")
			}
		})
	}
	channel := Channel{Setting: common.GetPointer(`{"model_owned_by":42}`)}
	require.Error(t, channel.ValidateSettings())
}

// This query contract also runs against real MySQL and PostgreSQL. The tests
// use dedicated empty test databases; never point TEST_*_DSN at production.
func TestPublicModelOwnerDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			dbType := common.DatabaseTypeSQLite
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "owners.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
				dbType = common.DatabaseTypeMySQL
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
				dbType = common.DatabaseTypePostgreSQL
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			previous, mainType, logType := DB, common.MainDatabaseType(), common.LogDatabaseType()
			DB = db
			common.SetDatabaseTypes(dbType, logType)
			initCol()
			t.Cleanup(func() {
				DB = previous
				common.SetDatabaseTypes(mainType, logType)
				initCol()
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, DB.AutoMigrate(&Option{}, &Channel{}, &Ability{}))
			var version string
			if dialect == "sqlite" {
				require.NoError(t, DB.Raw("select sqlite_version()").Scan(&version).Error)
			} else {
				require.NoError(t, DB.Raw("select version()").Scan(&version).Error)
			}
			t.Logf("%s version: %s", dialect, version)
			clearPreferredOwnerTables(t)
			t.Cleanup(func() {
				require.NoError(t, db.Exec("DELETE FROM abilities").Error)
				require.NoError(t, db.Exec("DELETE FROM channels").Error)
				require.NoError(t, db.Exec("DELETE FROM options").Error)
			})
			insertPreferredOwnerCandidate(t, 11, "public-model", "default", constant.ChannelTypeOpenAI, 1, 50, common.ChannelStatusEnabled, true)
			insertPreferredOwnerCandidate(t, 12, "public-model", "default", constant.ChannelTypeOpenAI, 2, 0, common.ChannelStatusEnabled, true)
			insertPreferredOwnerCandidate(t, 13, "public-model", "private", constant.ChannelTypeOpenAI, 100, 100, common.ChannelStatusEnabled, true)
			for id, value := range map[int]string{11: `{"model_owned_by":"fallback"}`, 12: `{"model_owned_by":" 万川公开 ","proxy":"https://private.proxy","future_setting":true}`, 13: `{"model_owned_by":"private-owner"}`} {
				require.NoError(t, DB.Model(&Channel{}).Where("id = ?", id).Update("setting", value).Error)
			}
			for range 2 {
				require.NoError(t, DB.AutoMigrate(&Option{}, &Channel{}, &Ability{}))
				owners, err := GetPreferredModelOwners([]string{"public-model", "public-model"}, []string{" default "})
				require.NoError(t, err)
				require.Equal(t, map[string]PreferredModelOwner{"public-model": {ChannelType: constant.ChannelTypeOpenAI, ModelOwnedBy: "万川公开"}}, owners)
			}
			for _, groups := range [][]string{nil, {}, {" "}, {"unknown"}} {
				owners, err := GetPreferredModelOwners([]string{"public-model"}, groups)
				require.NoError(t, err)
				assert.Empty(t, owners)
			}
			owners, err := GetPreferredModelOwners([]string{"not-listed"}, []string{"default"})
			require.NoError(t, err)
			assert.Empty(t, owners)
			owners, err = GetPreferredModelOwners([]string{"public-model"}, []string{"private"})
			require.NoError(t, err)
			assert.Equal(t, "private-owner", owners["public-model"].ModelOwnedBy)
			// Owner reads must not repair, persist or disclose malformed/absent settings.
			for _, raw := range []string{`{broken`, `{"model_owned_by":42}`, `{"model_owned_by":""}`, `{"model_owned_by":"bad\u202e"}`} {
				require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 12).Update("setting", raw).Error)
				owners, err = GetPreferredModelOwners([]string{"public-model"}, []string{"default"})
				require.NoError(t, err)
				assert.Equal(t, PreferredModelOwner{ChannelType: constant.ChannelTypeOpenAI}, owners["public-model"])
				var saved Channel
				require.NoError(t, DB.First(&saved, 12).Error)
				require.NotNil(t, saved.Setting)
				assert.Equal(t, raw, *saved.Setting)
			}
			// No owner cache: updates and channel/ability disablement apply immediately.
			require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 12).Update("setting", `{"model_owned_by":"changed"}`).Error)
			owners, err = GetPreferredModelOwners([]string{"public-model"}, []string{"default"})
			require.NoError(t, err)
			assert.Equal(t, "changed", owners["public-model"].ModelOwnedBy)
			require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 12).Update("status", common.ChannelStatusManuallyDisabled).Error)
			owners, err = GetPreferredModelOwners([]string{"public-model"}, []string{"default"})
			require.NoError(t, err)
			assert.Equal(t, "fallback", owners["public-model"].ModelOwnedBy)
			require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", 11).Update("enabled", false).Error)
			owners, err = GetPreferredModelOwners([]string{"public-model"}, []string{"default"})
			require.NoError(t, err)
			assert.Empty(t, owners)
		})
	}
}
