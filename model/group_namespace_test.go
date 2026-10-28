package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestGroupNamespaceHooksAllowFocusedSQLiteFixtureWithoutOptions(t *testing.T) {
	previousDB, previousLog := DB, LOG_DB
	previousMain, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB, LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&User{}, &SubscriptionPlan{}, &UserSubscription{}, &Ability{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLog
		common.SetDatabaseTypes(previousMain, previousLogType)
		_ = sqlDB.Close()
	})

	plan := SubscriptionPlan{Title: "focused", UpgradeGroup: "pro", DowngradeGroup: "default"}
	require.NoError(t, db.Create(&plan).Error)

	sub := UserSubscription{PlanId: plan.Id, UserId: 1, Status: "active", UpgradeGroup: "pro", PrevUserGroup: "default"}
	require.NoError(t, db.Create(&sub).Error)

	ability := Ability{Group: "pro", Model: "m1", ChannelId: 1, Enabled: true}
	require.NoError(t, db.Create(&ability).Error)
}
