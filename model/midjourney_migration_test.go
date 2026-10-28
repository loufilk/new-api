package model

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// The released schema before wallet receipts. Keep this frozen so adding or
// changing a field in Midjourney cannot silently change the upgrade fixture.
type midjourneyBeforeWalletReceipt struct {
	Id               int
	Code             int
	UserId           int    `gorm:"index"`
	Action           string `gorm:"type:varchar(40);index"`
	MjId             string `gorm:"index"`
	Prompt           string
	PromptEn         string
	Description      string
	State            string
	SubmitTime       int64 `gorm:"index"`
	StartTime        int64 `gorm:"index"`
	FinishTime       int64 `gorm:"index"`
	ImageUrl         string
	VideoUrl         string
	VideoUrls        string
	Status           string `gorm:"type:varchar(20);index"`
	Progress         string `gorm:"type:varchar(30);index"`
	FailReason       string
	ChannelId        int
	Quota            int
	Buttons          string
	Properties       string
	TokenId          int `gorm:"default:0"`
	BillingChannelId int `gorm:"default:0"`
}

func TestMidjourneyWalletReceiptMigrationDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		name string
		env  string
	}{
		{"sqlite", ""},
		{"mysql", "TEST_MYSQL_DSN"},
		{"postgres", "TEST_POSTGRES_DSN"},
	} {
		t.Run(dialect.name, func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect.env != "" {
				dsn := strings.TrimSpace(os.Getenv(dialect.env))
				if dsn == "" {
					t.Skip(dialect.env + " is not configured")
				}
				if dialect.name == "mysql" {
					driver = mysql.Open(dsn)
				} else {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			query := "SELECT version()"
			if dialect.name == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("validated %s: %s", dialect.name, version)

			for _, upgrade := range []bool{false, true} {
				t.Run(fmt.Sprintf("upgrade=%t", upgrade), func(t *testing.T) {
					table := fmt.Sprintf("mj_receipt_migrate_%d", time.Now().UnixNano())
					tableDB := db.Table(table).Session(&gorm.Session{})
					t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(table)) })
					if upgrade {
						require.NoError(t, tableDB.AutoMigrate(&midjourneyBeforeWalletReceipt{}))
						require.False(t, db.Migrator().HasColumn(table, "wallet_debit"))
						legacy := midjourneyBeforeWalletReceipt{Id: 41, UserId: 9, MjId: "legacy-receipt-task", Action: "IMAGINE", Status: "IN_PROGRESS", Progress: "50%", Quota: 300, TokenId: 17, ChannelId: 19, BillingChannelId: 23, Prompt: "preserved prompt", Properties: `{"legacy":true}`, SubmitTime: 1788600000}
						require.NoError(t, tableDB.Create(&legacy).Error)
					}
					for range 2 {
						require.NoError(t, tableDB.AutoMigrate(&Midjourney{}))
					}
					require.True(t, db.Migrator().HasColumn(table, "wallet_debit"))
					for _, column := range []string{"token_fingerprint", "billing_accounted", "billing_request_id", "billing_original_quota"} {
						require.True(t, db.Migrator().HasColumn(table, column), "financial provenance column %s must survive two migrations", column)
					}
					for _, column := range []string{"user_id", "action", "mj_id", "submit_time", "start_time", "finish_time", "status", "progress"} {
						assert.True(t, db.Migrator().HasIndex(table, db.NamingStrategy.IndexName(table, column)), "index %s must survive migration", column)
					}
					if upgrade {
						var preserved Midjourney
						require.NoError(t, tableDB.First(&preserved, 41).Error)
						assert.Equal(t, 9, preserved.UserId)
						assert.Equal(t, "legacy-receipt-task", preserved.MjId)
						assert.Equal(t, "IN_PROGRESS", preserved.Status)
						assert.Equal(t, "50%", preserved.Progress)
						assert.Equal(t, 300, preserved.Quota)
						assert.Equal(t, 17, preserved.TokenId)
						assert.Equal(t, 19, preserved.ChannelId)
						assert.Equal(t, 23, preserved.BillingChannelId)
						assert.Equal(t, "preserved prompt", preserved.Prompt)
						assert.JSONEq(t, `{"legacy":true}`, preserved.Properties)
						assert.EqualValues(t, 1788600000, preserved.SubmitTime)
						assert.Nil(t, preserved.WalletDebit, "NULL is unknown historical provenance, never a paid-only refund receipt")
						assert.Empty(t, preserved.TokenFingerprint)
						assert.False(t, preserved.BillingAccounted)
						assert.Empty(t, preserved.BillingRequestID)
						assert.Zero(t, preserved.BillingOriginalQuota, "the old displayed quota is not evidence of an accounted debit")
						assert.False(t, preserved.HasCurrentBillingIdentity())
						var unknown struct {
							TokenFingerprint     sql.NullString
							BillingAccounted     sql.NullBool
							BillingRequestID     sql.NullString
							BillingOriginalQuota sql.NullInt64
						}
						require.NoError(t, tableDB.Select("token_fingerprint, billing_accounted, billing_request_id, billing_original_quota").Where("id = ?", 41).Scan(&unknown).Error)
						assert.False(t, unknown.TokenFingerprint.Valid, "migration must not guess a current token key")
						assert.False(t, unknown.BillingAccounted.Valid, "migration must not fabricate historical accounting")
						assert.False(t, unknown.BillingRequestID.Valid)
						assert.False(t, unknown.BillingOriginalQuota.Valid)
						require.Error(t, tableDB.Create(&Midjourney{Id: 41, MjId: "duplicate-primary-key"}).Error, "primary key uniqueness must survive migration")
					}
					for _, receipt := range []struct {
						name   string
						shares []LedgerShare
					}{
						{"legacy-null", nil},
						{"pending-empty", []LedgerShare{}},
						{"charged-mixed", []LedgerShare{{Account: AccountBonus, Amount: 100}, {Account: AccountReward, Amount: 50}, {Account: AccountPaid, Amount: 150}}},
					} {
						task := Midjourney{UserId: 9, MjId: receipt.name, Quota: 300, WalletDebit: receipt.shares}
						if receipt.name == "charged-mixed" {
							task.TokenId = 17
							task.TokenFingerprint = BillingTokenFingerprint("synthetic-migration-original-token")
							task.BillingAccounted = true
							task.BillingRequestID = "synthetic-migration-request"
							task.BillingOriginalQuota = 300
						}
						require.NoError(t, tableDB.Create(&task).Error)
						for range 2 {
							require.NoError(t, tableDB.AutoMigrate(&Midjourney{}))
						}
						var reloaded Midjourney
						require.NoError(t, tableDB.First(&reloaded, task.Id).Error)
						assert.Equal(t, receipt.shares, reloaded.WalletDebit, receipt.name)
						assert.Equal(t, task.TokenFingerprint, reloaded.TokenFingerprint)
						assert.Equal(t, task.BillingAccounted, reloaded.BillingAccounted)
						assert.Equal(t, task.BillingRequestID, reloaded.BillingRequestID)
						assert.Equal(t, task.BillingOriginalQuota, reloaded.BillingOriginalQuota)
						if task.BillingAccounted {
							require.Len(t, reloaded.TokenFingerprint, 64)
							assert.True(t, reloaded.HasCurrentBillingIdentity())
						}
						encoded, err := common.Marshal(reloaded)
						require.NoError(t, err)
						assert.NotContains(t, string(encoded), "wallet_debit")
						for _, private := range []string{"token_fingerprint", "billing_accounted", "billing_request_id", "billing_original_quota", "synthetic-migration-original-token", "synthetic-migration-request"} {
							assert.NotContains(t, string(encoded), private)
						}
						if task.TokenFingerprint != "" {
							assert.NotContains(t, string(encoded), task.TokenFingerprint)
						}
					}
				})
			}
		})
	}
}
