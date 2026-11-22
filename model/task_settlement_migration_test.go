package model

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// taskBeforeSettlementMigration is the complete Task table shape at PR15
// (1d70852d), before the durable pending-settlement columns were introduced.
// A separate type ensures upgrading a real old table, not a current schema
// whose newly added fields happen to contain zero values.
type taskBeforeSettlementMigration struct {
	ID          int64 `gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt   int64 `gorm:"index"`
	UpdatedAt   int64
	TaskID      string                `gorm:"type:varchar(191);index"`
	Platform    constant.TaskPlatform `gorm:"type:varchar(30);index"`
	UserId      int                   `gorm:"index"`
	Group       string                `gorm:"type:varchar(50)"`
	ChannelId   int                   `gorm:"index"`
	Quota       int
	Action      string     `gorm:"type:varchar(40);index"`
	Status      TaskStatus `gorm:"type:varchar(20);index"`
	FailReason  string
	SubmitTime  int64           `gorm:"index"`
	StartTime   int64           `gorm:"index"`
	FinishTime  int64           `gorm:"index"`
	Progress    string          `gorm:"type:varchar(20);index"`
	Properties  Properties      `gorm:"type:json"`
	PrivateData TaskPrivateData `gorm:"column:private_data;type:json"`
	Data        json.RawMessage `gorm:"type:json"`
}

// Every case uses its own prefixed table and never drops a pre-existing table.
// CI selects one dialect and supplies a disposable TEST_*_DSN; an absent local
// DSN is explicitly skipped, not reported as a database compatibility pass.
func TestTaskSettlementMigrationDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		kind common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeSQLite, ""},
		{common.DatabaseTypeMySQL, "TEST_MYSQL_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(dialect.env))
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " is not configured")
			}
			for _, mode := range []string{"fresh", "upgrade"} {
				t.Run(mode, func(t *testing.T) {
					var driver gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "tasks.db"))
					if dialect.kind == common.DatabaseTypeMySQL {
						driver = mysql.Open(dsn)
					} else if dialect.kind == common.DatabaseTypePostgreSQL {
						driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
					}
					naming := schema.NamingStrategy{TablePrefix: fmt.Sprintf("tsettle_%x_", time.Now().UnixNano())}
					recorder := &migrationSQLRecorder{}
					db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: naming, Logger: recorder})
					require.NoError(t, err)
					sqlDB, err := db.DB()
					require.NoError(t, err)
					sqlDB.SetMaxOpenConns(1)
					t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
					table := naming.TableName("Task")
					require.False(t, db.Migrator().HasTable(table), "fixture must not reuse an existing table")
					t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(table)) })
					oldDB, oldKind := DB, common.MainDatabaseType()
					DB = db
					common.SetMainDatabaseType(dialect.kind)
					t.Cleanup(func() { DB = oldDB; common.SetMainDatabaseType(oldKind) })
					versionQuery := "SELECT version()"
					if dialect.kind == common.DatabaseTypeSQLite {
						versionQuery = "SELECT sqlite_version()"
					}
					var version string
					require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
					t.Logf("validated %s server version: %s", dialect.kind, version)

					legacy := taskBeforeSettlementMigration{
						CreatedAt: 1772000000, UpdatedAt: 1772000001, TaskID: "legacy-task",
						Platform: "migration-plugin", UserId: 42, Group: "default", ChannelId: 23,
						Quota: 345, Action: "text_to_video", Status: TaskStatusInProgress,
						SubmitTime: 1772000000, StartTime: 1772000001, Progress: "50%",
						Properties: Properties{Input: "historical prompt", OriginModelName: "legacy-model", UpstreamModelName: "provider-model"},
						PrivateData: TaskPrivateData{
							UpstreamTaskID: "provider-task", RequestId: "legacy-request", BillingSource: "wallet", TokenId: 17,
							WalletDebit:    []LedgerShare{{Account: AccountBonus, Amount: 100}, {Account: AccountReward, Amount: 45}, {Account: AccountPaid, Amount: 200}},
							BillingContext: &TaskBillingContext{OriginModelName: "legacy-model", ModelPrice: 0.2, GroupRatio: 1},
						},
						Data: json.RawMessage(`{"progress":50,"payload":"retained"}`),
					}
					var originalIndexes []gorm.Index
					if mode == "upgrade" {
						require.NoError(t, db.Table(table).AutoMigrate(&taskBeforeSettlementMigration{}))
						require.False(t, db.Migrator().HasColumn(&Task{}, "settlement_pending"))
						require.False(t, db.Migrator().HasColumn(&Task{}, "settlement_attempted_at"))
						require.NoError(t, db.Table(table).Create(&legacy).Error)
						originalIndexes, err = db.Table(table).Migrator().GetIndexes(&taskBeforeSettlementMigration{})
						require.NoError(t, err)
						require.NotEmpty(t, originalIndexes)
					}

					require.NoError(t, db.AutoMigrate(&Task{}))
					recorder.reset()
					require.NoError(t, db.AutoMigrate(&Task{}))
					assert.Empty(t, recorder.schemaMutations(), "a second startup must not repeat schema mutations")
					require.True(t, db.Migrator().HasColumn(&Task{}, "SettlementPending"))
					require.True(t, db.Migrator().HasColumn(&Task{}, "SettlementAttemptedAt"))
					currentIndexes, err := db.Migrator().GetIndexes(&Task{})
					require.NoError(t, err)
					indexesByName := make(map[string]gorm.Index, len(currentIndexes))
					for _, index := range currentIndexes {
						indexesByName[index.Name()] = index
					}
					for _, index := range originalIndexes {
						current := indexesByName[index.Name()]
						require.NotNil(t, current, "historical index %s must survive", index.Name())
						assert.Equal(t, index.Columns(), current.Columns())
						wasUnique, wasKnown := index.Unique()
						isUnique, isKnown := current.Unique()
						assert.Equal(t, wasKnown, isKnown)
						assert.Equal(t, wasUnique, isUnique)
					}
					for _, column := range []string{"task_id", "status", "settlement_pending", "settlement_attempted_at"} {
						assert.True(t, db.Migrator().HasIndex(&Task{}, naming.IndexName(table, column)), "index for %s must exist", column)
					}
					if mode == "upgrade" {
						var nulls struct {
							SettlementPending     sql.NullBool
							SettlementAttemptedAt sql.NullInt64
						}
						require.NoError(t, db.Model(&Task{}).Select("settlement_pending", "settlement_attempted_at").Where("id = ?", legacy.ID).Scan(&nulls).Error)
						assert.False(t, nulls.SettlementPending.Valid, "legacy rows exercise nullable added columns")
						assert.False(t, nulls.SettlementAttemptedAt.Valid)
						var stored Task
						require.NoError(t, db.First(&stored, legacy.ID).Error)
						assert.Equal(t, legacy.TaskID, stored.TaskID)
						assert.Equal(t, legacy.CreatedAt, stored.CreatedAt)
						assert.Equal(t, legacy.UpdatedAt, stored.UpdatedAt)
						assert.Equal(t, legacy.Platform, stored.Platform)
						assert.Equal(t, legacy.UserId, stored.UserId)
						assert.Equal(t, legacy.Group, stored.Group)
						assert.Equal(t, legacy.ChannelId, stored.ChannelId)
						assert.Equal(t, legacy.Quota, stored.Quota)
						assert.Equal(t, legacy.Action, stored.Action)
						assert.Equal(t, legacy.Properties, stored.Properties)
						assert.Equal(t, legacy.PrivateData, stored.PrivateData)
						assert.JSONEq(t, string(legacy.Data), string(stored.Data))
						assert.False(t, stored.SettlementPending)
						assert.Zero(t, stored.SettlementAttemptedAt)
						assert.True(t, HasUnfinishedSyncTasks(), "NULL settlement flag must not hide an old unfinished task")
						pending, err := GetPendingTaskSettlements(t.Context(), 10)
						require.NoError(t, err)
						assert.Empty(t, pending, "historical tasks do not manufacture a financial intent")
						stored.Status, stored.Progress = TaskStatusSuccess, "100%"
						won, err := stored.UpdateWithStatus(TaskStatusInProgress)
						require.NoError(t, err)
						assert.True(t, won, "CAS must accept a legacy NULL settlement flag")
						assert.False(t, HasUnfinishedSyncTasks())
					}

					pendingTask := Task{
						TaskID: "new-pending-task", Status: TaskStatusSuccess, Progress: "100%", Quota: 200,
						SettlementPending: true,
						PrivateData: TaskPrivateData{
							SubmissionAccounted: true,
							PendingSettlement:   &TaskSettlement{Phase: "terminal", Quota: 123, Reason: common.NewMessage("durable migration adjustment")},
						},
					}
					require.NoError(t, db.Create(&pendingTask).Error)
					// Re-open the real database after the durable intent was saved.
					// No in-memory observer or transaction survives this boundary.
					require.NoError(t, sqlDB.Close())
					db, err = gorm.Open(driver, &gorm.Config{NamingStrategy: naming, Logger: recorder})
					require.NoError(t, err)
					sqlDB, err = db.DB()
					require.NoError(t, err)
					sqlDB.SetMaxOpenConns(1)
					DB = db
					var loaded Task
					require.NoError(t, db.First(&loaded, pendingTask.ID).Error)
					assert.Equal(t, pendingTask.PrivateData, loaded.PrivateData)
					assert.False(t, HasUnfinishedSyncTasks(), "historical terminal pending rows are not runnable official work")
					pending, err := GetPendingTaskSettlements(t.Context(), 10)
					require.NoError(t, err)
					require.Len(t, pending, 1)
					assert.Equal(t, pendingTask.ID, pending[0].ID)
					require.NoError(t, db.First(&loaded, pendingTask.ID).Error)
					assert.Equal(t, pendingTask.Quota, loaded.Quota)
					assert.Equal(t, pendingTask.PrivateData, loaded.PrivateData)
					assert.True(t, loaded.SettlementPending)
					assert.Zero(t, loaded.SettlementAttemptedAt)
					assert.False(t, HasUnfinishedSyncTasks())
					duplicate := Task{ID: pendingTask.ID, TaskID: "duplicate-primary-key"}
					assert.Error(t, db.Create(&duplicate).Error, "primary-key uniqueness survives migration")
				})
			}
		})
	}
}

func TestTaskSettlementClampPersistence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value float64
	}{
		{"positive_infinity", math.Inf(1)},
		{"negative_infinity", math.Inf(-1)},
		{"nan", math.NaN()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quota, clamp := common.QuotaFromFloatChecked(tc.value)
			require.NotNil(t, clamp)
			private := TaskPrivateData{PendingSettlement: &TaskSettlement{Phase: "terminal", Quota: quota, Clamp: clamp}}
			encoded, err := private.Value()
			require.NoError(t, err, "a clamped upstream quantity must not prevent durable settlement intent persistence")
			var decoded TaskPrivateData
			require.NoError(t, decoded.Scan(encoded))
			require.NotNil(t, decoded.PendingSettlement)
			require.NotNil(t, decoded.PendingSettlement.Clamp)
			assert.Equal(t, clamp.Op, decoded.PendingSettlement.Clamp.Op)
			assert.Equal(t, clamp.Kind, decoded.PendingSettlement.Clamp.Kind)
			assert.Equal(t, clamp.Clamped, decoded.PendingSettlement.Clamp.Clamped)
			if math.IsNaN(tc.value) {
				assert.True(t, math.IsNaN(decoded.PendingSettlement.Clamp.Original))
			} else {
				assert.Equal(t, tc.value, decoded.PendingSettlement.Clamp.Original)
			}
			assert.Equal(t, quota, decoded.PendingSettlement.Quota)
		})
	}
}
