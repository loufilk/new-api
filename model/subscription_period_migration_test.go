package model

import (
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// These complete table shapes come from the pre-period source e402443. They
// exercise real additive upgrades, not current tables containing zero fields.
type subscriptionBeforePeriodMigration struct {
	Id     int `json:"id"`
	UserId int `json:"user_id" gorm:"index;index:idx_user_sub_active,priority:1"`
	PlanId int `json:"plan_id" gorm:"index"`

	AmountTotal int64 `json:"amount_total" gorm:"type:bigint;not null;default:0"`
	AmountUsed  int64 `json:"amount_used" gorm:"type:bigint;not null;default:0"`

	StartTime int64  `json:"start_time" gorm:"bigint"`
	EndTime   int64  `json:"end_time" gorm:"bigint;index;index:idx_user_sub_active,priority:3"`
	Status    string `json:"status" gorm:"type:varchar(32);index;index:idx_user_sub_active,priority:2"` // active/expired/cancelled

	Source string `json:"source" gorm:"type:varchar(32);default:'order'"` // order/admin

	LastResetTime int64 `json:"last_reset_time" gorm:"type:bigint;default:0"`
	NextResetTime int64 `json:"next_reset_time" gorm:"type:bigint;default:0;index"`

	UpgradeGroup  string `json:"upgrade_group" gorm:"type:varchar(64);default:''"`
	PrevUserGroup string `json:"prev_user_group" gorm:"type:varchar(64);default:''"`

	// Downgrade target group on expiry (snapshot from plan; empty = revert to PrevUserGroup)
	DowngradeGroup string `json:"downgrade_group" gorm:"type:varchar(64);default:''"`

	// Whether wallet fallback is allowed after this subscription's quota is exhausted (snapshot from plan)
	AllowWalletOverflow bool `json:"allow_wallet_overflow"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

type subscriptionReceiptBeforePeriodMigration struct {
	Id                 int    `json:"id"`
	RequestId          string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId             int    `json:"user_id" gorm:"index"`
	UserSubscriptionId int    `json:"user_subscription_id" gorm:"index"`
	PreConsumed        int64  `json:"pre_consumed" gorm:"type:bigint;not null;default:0"`
	Status             string `json:"status" gorm:"type:varchar(32);index"` // consumed/refunded
	CreatedAt          int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt          int64  `json:"updated_at" gorm:"bigint;index"`
}

func TestSubscriptionPeriodMigrationDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		kind common.DatabaseType
		env  string
	}{{common.DatabaseTypeSQLite, ""}, {common.DatabaseTypeMySQL, "TEST_MYSQL_DSN"}, {common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN"}} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(dialect.env))
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " is not configured")
			}
			for _, mode := range []string{"fresh", "upgrade"} {
				t.Run(mode, func(t *testing.T) {
					db := subscriptionPeriodDatabase(t, dialect.kind, dsn)
					require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}))
					now := GetDBTimestamp(db)
					plan := SubscriptionPlan{Id: 91, Title: "migration-plan", TotalAmount: 1000, QuotaResetPeriod: SubscriptionResetNever}
					require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&plan).Error)
					InvalidateSubscriptionPlanCache(plan.Id)
					t.Cleanup(func() { InvalidateSubscriptionPlanCache(plan.Id) })
					subTable := db.NamingStrategy.TableName("UserSubscription")
					receiptTable := db.NamingStrategy.TableName("SubscriptionPreConsumeRecord")
					var beforeIndexes []gorm.Index
					if mode == "upgrade" {
						require.NoError(t, db.Table(subTable).AutoMigrate(&subscriptionBeforePeriodMigration{}))
						require.NoError(t, db.Table(receiptTable).AutoMigrate(&subscriptionReceiptBeforePeriodMigration{}))
						require.False(t, db.Migrator().HasColumn(&UserSubscription{}, "quota_period"))
						require.False(t, db.Migrator().HasColumn(&SubscriptionPreConsumeRecord{}, "reserved_amount"))
						oldSub := subscriptionBeforePeriodMigration{Id: 92, UserId: 93, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 350, StartTime: now - 100, EndTime: now + 3600, Status: "active", Source: "order", CreatedAt: now - 100, UpdatedAt: now - 10, LastResetTime: now - 100, AllowWalletOverflow: true}
						require.NoError(t, db.Table(subTable).Create(&oldSub).Error)
						oldReceipt := subscriptionReceiptBeforePeriodMigration{Id: 94, RequestId: "legacy-period", UserId: 93, UserSubscriptionId: 92, PreConsumed: 200, Status: "consumed", CreatedAt: now - 50, UpdatedAt: now - 10}
						require.NoError(t, db.Table(receiptTable).Create(&oldReceipt).Error)
						var err error
						beforeIndexes, err = db.Table(receiptTable).Migrator().GetIndexes(&subscriptionReceiptBeforePeriodMigration{})
						require.NoError(t, err)
					}
					require.NoError(t, db.AutoMigrate(&UserSubscription{}, &SubscriptionPreConsumeRecord{}))
					// An index of the same name on another PostgreSQL table must not
					// make CREATE INDEX IF NOT EXISTS look like a successful migration.
					indexes, err := db.Migrator().GetIndexes(&UserSubscription{})
					require.NoError(t, err)
					var activeIndex gorm.Index
					for _, index := range indexes {
						if index.Name() == "idx_user_sub_active" {
							activeIndex = index
						}
					}
					require.NotNil(t, activeIndex, "active-subscription index must belong to the fixture table")
					columns := activeIndex.Columns()
					if dialect.kind == common.DatabaseTypePostgreSQL {
						// The driver returns PG index columns in table order, not index
						// order. Read the ordered definition from the actual index OID.
						columns = nil
						require.NoError(t, db.Raw(`SELECT pg_get_indexdef(i.indexrelid, n, true)
FROM pg_catalog.pg_index i
JOIN pg_catalog.pg_class idx ON idx.oid = i.indexrelid
CROSS JOIN generate_series(1, i.indnatts) AS n
WHERE i.indrelid = to_regclass(?) AND idx.relname = ?
ORDER BY n`, subTable, "idx_user_sub_active").Scan(&columns).Error)
					}
					assert.Equal(t, []string{"user_id", "status", "end_time"}, columns)
					recorder := &migrationSQLRecorder{}
					require.NoError(t, db.Session(&gorm.Session{Logger: recorder}).AutoMigrate(&UserSubscription{}, &SubscriptionPreConsumeRecord{}))
					assert.Empty(t, recorder.schemaMutations(), "second startup must not alter the schema again")
					require.True(t, db.Migrator().HasColumn(&UserSubscription{}, "quota_period"))
					for _, column := range []string{"quota_period", "reserved_amount", "settled_amount"} {
						require.True(t, db.Migrator().HasColumn(&SubscriptionPreConsumeRecord{}, column))
					}
					for _, index := range beforeIndexes {
						assert.True(t, db.Migrator().HasIndex(&SubscriptionPreConsumeRecord{}, index.Name()), "receipt index must survive")
					}
					if mode == "fresh" {
						_, sub := subscriptionPeriodSeed(t, db)
						reserve, err := PreConsumeUserSubscription("fresh-period", sub.UserId, "fixture", 0, 100)
						require.NoError(t, err)
						assert.Positive(t, reserve.QuotaPeriod, "new receipts capture their original allowance period")
						return
					}
					var sub UserSubscription
					var record SubscriptionPreConsumeRecord
					require.NoError(t, db.First(&sub, 92).Error)
					require.NoError(t, db.First(&record, 94).Error)
					assert.Zero(t, sub.QuotaPeriod)
					assert.EqualValues(t, 350, sub.AmountUsed)
					assert.EqualValues(t, now-100, sub.StartTime)
					assert.EqualValues(t, now+3600, sub.EndTime)
					assert.Equal(t, "order", sub.Source)
					assert.True(t, sub.AllowWalletOverflow)
					assert.Zero(t, record.QuotaPeriod)
					assert.Zero(t, record.ReservedAmount)
					assert.EqualValues(t, 200, record.PreConsumed)
					assert.Equal(t, "consumed", record.Status)
					assert.EqualValues(t, now-50, record.CreatedAt)
					fresh, err := PreConsumeUserSubscription("after-upgrade", sub.UserId, "fixture", 0, 100)
					require.NoError(t, err)
					assert.Positive(t, fresh.QuotaPeriod, "new receipts capture their original allowance period")
					require.NoError(t, db.First(&sub, 92).Error)
					assert.EqualValues(t, 450, sub.AmountUsed)
					// Repeated startup never guesses legacy ownership, nor rewinds a new epoch.
					require.NoError(t, db.AutoMigrate(&UserSubscription{}, &SubscriptionPreConsumeRecord{}))
					require.NoError(t, db.First(&sub, 92).Error)
					require.NoError(t, db.First(&record, 94).Error)
					assert.EqualValues(t, fresh.QuotaPeriod, sub.QuotaPeriod)
					assert.EqualValues(t, 450, sub.AmountUsed)
					assert.Zero(t, record.QuotaPeriod)
					duplicate := SubscriptionPreConsumeRecord{RequestId: "legacy-period", UserId: 93, UserSubscriptionId: 92}
					require.Error(t, db.Create(&duplicate).Error, "request uniqueness must survive the upgrade")
					// No automatic refund/migration is applied to historical receipts.
					require.NoError(t, db.First(&sub, 92).Error)
					assert.EqualValues(t, 450, sub.AmountUsed)
				})
			}
		})
	}
}
