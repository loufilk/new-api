package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Dedicated disposable databases are required: the audit's explicit TableName
// deliberately cannot be isolated by a GORM table prefix.
func bonusAuditDatabase(t *testing.T, kind common.DatabaseType, dsn string) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "audit.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=deferred")
	if kind == common.DatabaseTypeMySQL {
		driver = mysql.Open(dsn)
	} else if kind == common.DatabaseTypePostgreSQL {
		driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
	}
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(4) // Two workers plus a real lock-wait observer.
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	tables, err := db.Migrator().GetTables()
	require.NoError(t, err)
	require.Empty(t, tables, "bonus-audit fixtures refuse to modify a nonempty database")
	require.NoError(t, db.AutoMigrate(&Model{}, &ModelBonusEligibleAudit{}))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&ModelBonusEligibleAudit{}, &Model{})) })
	previousDB, previousKind := DB, common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(kind)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousKind)
		InvalidatePricingCache()
		InvalidateModelBonusEligibleCache()
	})
	query := "SELECT version()"
	if kind == common.DatabaseTypeSQLite {
		query = "SELECT sqlite_version()"
		var journal string
		require.NoError(t, db.Raw("PRAGMA journal_mode").Scan(&journal).Error)
		require.Equal(t, "wal", journal)
	}
	var version string
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("bonus-audit real %s version=%s; max connections=4", kind, version)
	return db
}

type bonusAuditIsolationPool struct {
	gorm.ConnPool
	isolation sql.IsolationLevel
}

func (p *bonusAuditIsolationPool) BeginTx(ctx context.Context, _ *sql.TxOptions) (gorm.ConnPool, error) {
	return p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, &sql.TxOptions{Isolation: p.isolation})
}

func bonusAuditReset(t *testing.T, db *gorm.DB, initial *bool) {
	t.Helper()
	require.NoError(t, db.Where("id > ?", 0).Delete(&ModelBonusEligibleAudit{}).Error)
	require.NoError(t, db.Unscoped().Where("id > ?", 0).Delete(&Model{}).Error)
	for _, id := range []int{1, 2} {
		require.NoError(t, db.Create(&Model{Id: id, ModelName: fmt.Sprintf("bonus-audit-%d", id), BonusEligible: initial}).Error)
	}
	// Warm both clocks explicitly; no pricing/channel fixture is needed merely
	// to assert that the mutation makes both cache layers cold.
	modelBonusEligibleLock.Lock()
	modelBonusEligibleTime = time.Now()
	modelBonusEligibleLock.Unlock()
	updatePricingLock.Lock()
	lastGetPricingTime = time.Now()
	updatePricingLock.Unlock()
}

func bonusAuditAssertCold(t *testing.T) {
	t.Helper()
	modelBonusEligibleLock.RLock()
	grantCold := modelBonusEligibleTime.IsZero()
	modelBonusEligibleLock.RUnlock()
	updatePricingLock.Lock()
	pricingCold := lastGetPricingTime.IsZero()
	updatePricingLock.Unlock()
	assert.True(t, grantCold, "eligibility cache must be invalidated even on a failed/unknown transaction")
	assert.True(t, pricingCold, "pricing must not republish a stale eligibility grant")
}

func bonusAuditAssertValue(t *testing.T, db *gorm.DB, id int, value *bool) {
	t.Helper()
	var row Model
	require.NoError(t, db.First(&row, id).Error)
	assert.Equal(t, value, row.BonusEligible)
}

func runBonusAuditContracts(t *testing.T, db *gorm.DB) {
	t.Helper()
	t.Run("no_op_and_missing_model_remain_unclaimed", func(t *testing.T) {
		bonusAuditReset(t, db, boolPtr(false))
		changed, err := BatchUpdateModelBonusEligible([]int{1, 99}, boolPtr(false), 41, "first", "unclaimed")
		require.NoError(t, err)
		require.Zero(t, changed)
		var count int64
		require.NoError(t, db.Model(&ModelBonusEligibleAudit{}).Count(&count).Error)
		require.Zero(t, count)
		changed, err = BatchUpdateModelBonusEligible([]int{1}, boolPtr(true), 42, "second", "unclaimed")
		require.NoError(t, err)
		require.Equal(t, 1, changed)
		require.NoError(t, db.Delete(&Model{}, 1).Error)
		changed, err = BatchUpdateModelBonusEligible([]int{1}, boolPtr(true), 43, "replay", "unclaimed")
		require.NoError(t, err)
		require.Zero(t, changed, "a recorded replay survives model deletion")
		_, err = BatchUpdateModelBonusEligible([]int{1}, nil, 43, "conflict", "unclaimed")
		require.ErrorIs(t, err, ErrModelAuditConflict)
	})
	t.Run("mixed_replay_applies_every_unclaimed_item", func(t *testing.T) {
		bonusAuditReset(t, db, nil)
		_, err := BatchUpdateModelBonusEligible([]int{1}, boolPtr(true), 41, "first", "mixed")
		require.NoError(t, err)
		changed, err := BatchUpdateModelBonusEligible([]int{2, 1, 2, 0, -1}, boolPtr(true), 42, "second", "mixed")
		require.NoError(t, err)
		require.Equal(t, 1, changed, "one replay must not turn the whole batch into a no-op")
		var audits []ModelBonusEligibleAudit
		require.NoError(t, db.Order("model_id").Find(&audits).Error)
		require.Len(t, audits, 2)
		assert.Equal(t, 41, audits[0].OperatorId)
		assert.Equal(t, 42, audits[1].OperatorId)
		bonusAuditAssertValue(t, db, 2, boolPtr(true))
	})
	t.Run("audit_model_identity_must_match", func(t *testing.T) {
		bonusAuditReset(t, db, nil)
		require.NoError(t, db.Create(&ModelBonusEligibleAudit{ModelId: 2, ModelName: "bonus-audit-2", OperatorId: 41, After: boolPtr(true), IdempotencyKey: "identity:1"}).Error)
		changed, err := BatchUpdateModelBonusEligible([]int{1}, boolPtr(true), 42, "wrong-identity", "identity")
		require.ErrorIs(t, err, ErrModelAuditConflict)
		require.Zero(t, changed)
		bonusAuditAssertValue(t, db, 1, nil)
	})
	t.Run("later_conflict_rolls_back_prior_item_even_if_current_matches", func(t *testing.T) {
		bonusAuditReset(t, db, nil)
		_, err := BatchUpdateModelBonusEligible([]int{2}, boolPtr(false), 41, "first", "conflict")
		require.NoError(t, err)
		_, err = BatchUpdateModelBonusEligible([]int{2}, boolPtr(true), 42, "intervening", "other")
		require.NoError(t, err)
		changed, err := BatchUpdateModelBonusEligible([]int{1, 2}, boolPtr(true), 43, "conflict", "conflict")
		require.ErrorIs(t, err, ErrModelAuditConflict)
		require.Zero(t, changed)
		bonusAuditAssertValue(t, db, 1, nil)
		bonusAuditAssertValue(t, db, 2, boolPtr(true))
		var count int64
		require.NoError(t, db.Model(&ModelBonusEligibleAudit{}).Where("model_id = ?", 1).Count(&count).Error)
		require.Zero(t, count)
		bonusAuditAssertCold(t)
	})
	for _, failure := range []string{"audit_insert", "actual_duplicate", "model_update"} {
		t.Run("later_"+failure+"_rolls_back_entire_batch", func(t *testing.T) {
			bonusAuditReset(t, db, nil)
			injected := errors.New("bonus-audit fixture rejects the second item")
			var hits atomic.Int32
			callback := "bonus-audit-failure"
			if failure == "model_update" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == reflect.TypeFor[Model]() && hits.Add(1) == 2 {
						tx.AddError(injected)
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
			} else {
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
					audit, ok := tx.Statement.Dest.(*ModelBonusEligibleAudit)
					if !ok || audit.ModelId != 2 || hits.Add(1) != 1 {
						return
					}
					if failure == "audit_insert" {
						tx.AddError(injected)
						return
					}
					// A real duplicate SQL statement, not a fabricated error. The
					// conflicting insert belongs to this same disposable transaction.
					duplicate := *audit
					tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Create(&duplicate).Error)
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callback)) })
			}
			changed, err := BatchUpdateModelBonusEligible([]int{1, 2}, boolPtr(true), 41, "failure", "failure")
			if failure == "actual_duplicate" {
				require.ErrorIs(t, err, ErrModelAuditConflict)
			} else {
				require.ErrorIs(t, err, injected)
			}
			require.Zero(t, changed)
			bonusAuditAssertValue(t, db, 1, nil)
			bonusAuditAssertValue(t, db, 2, nil)
			var count int64
			require.NoError(t, db.Model(&ModelBonusEligibleAudit{}).Count(&count).Error)
			require.Zero(t, count, "claims and mutations must both roll back")
			bonusAuditAssertCold(t)
		})
	}
	t.Run("real_commit_lost_ack_preserves_audit_and_invalidates", func(t *testing.T) {
		bonusAuditReset(t, db, boolPtr(true))
		subscriptionLoseCommitAck(t, db, func() error {
			changed, err := BatchUpdateModelBonusEligible([]int{1, 2}, nil, 41, "lost-ack", "lost-ack")
			assert.Zero(t, changed)
			return err
		})
		bonusAuditAssertValue(t, db, 1, nil)
		bonusAuditAssertValue(t, db, 2, nil)
		var audits []ModelBonusEligibleAudit
		require.NoError(t, db.Find(&audits).Error)
		require.Len(t, audits, 2)
		bonusAuditAssertCold(t)
		changed, err := BatchUpdateModelBonusEligible([]int{2, 1}, nil, 42, "replay", "lost-ack")
		require.NoError(t, err)
		require.Zero(t, changed)
	})
}

type bonusAuditRaceCase struct {
	name         string
	initial      *bool
	first        *bool
	second       *bool
	firstIDs     []int
	secondIDs    []int
	differentKey bool
}

// The old implementation pauses after both stale model/audit reads, then holds
// A's real UPDATE while B waits in SQL. The repaired implementation instead
// holds A's SELECT FOR UPDATE while B waits in SQL before any audit snapshot.
// Native engines prove that wait via their server, never by a sleep or a
// one-connection pool. SQLite uses its distinct WAL snapshot-upgrade schedule.
func runBonusAuditRace(t *testing.T, db *gorm.DB, isolation sql.IsolationLevel, tc bonusAuditRaceCase) {
	t.Helper()
	bonusAuditReset(t, db, tc.initial)
	kind := common.MainDatabaseType()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	var workers sync.WaitGroup
	previousDB := DB
	DB = DB.WithContext(ctx)
	defer func() { cancel(); workers.Wait(); DB = previousDB }()
	pool, err := db.DB()
	require.NoError(t, err)
	var mu sync.Mutex
	roles := make(map[gorm.ConnPool]int)
	var connections [2]atomic.Int64
	var locking atomic.Bool
	var contested atomic.Int64
	aRead, bRead, aUpdated := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseA, releaseB, releaseUpdate := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var aOnce, bOnce, updateOnce sync.Once
	wait := func(tx *gorm.DB, ch <-chan struct{}) {
		select {
		case <-ch:
		case <-ctx.Done():
			tx.AddError(fmt.Errorf("bonus-audit barrier: %w", ctx.Err()))
		}
	}
	roleOf := func(tx *gorm.DB) (int, bool) {
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok || tx.Error != nil {
			return 0, false
		}
		mu.Lock()
		defer mu.Unlock()
		role, exists := roles[tx.Statement.ConnPool]
		if !exists {
			role = len(roles)
			if role >= 2 {
				tx.AddError(errors.New("unexpected extra transaction in bonus-audit race"))
				return 0, false
			}
			roles[tx.Statement.ConnPool] = role
			if kind != common.DatabaseTypeSQLite {
				query := "SELECT CONNECTION_ID()"
				if kind == common.DatabaseTypePostgreSQL {
					query = "SELECT pg_backend_pid()"
				}
				var id int64
				if err := tx.Statement.ConnPool.QueryRowContext(ctx, query).Scan(&id); err != nil {
					tx.AddError(err)
					return 0, false
				}
				connections[role].Store(id)
				if kind == common.DatabaseTypePostgreSQL {
					var actualIsolation string
					if err := tx.Statement.ConnPool.QueryRowContext(ctx, "SHOW transaction_isolation").Scan(&actualIsolation); err != nil {
						tx.AddError(err)
						return 0, false
					}
					if strings.ToLower(actualIsolation) != strings.ToLower(isolation.String()) {
						tx.AddError(fmt.Errorf("unexpected transaction isolation %q, wanted %s", actualIsolation, isolation))
						return 0, false
					}
					t.Logf("worker=%d physical connection=%d actual isolation=%s", role, id, actualIsolation)
				}
			}
		}
		return role, true
	}
	before, after, updated := "bonus-audit-race-before", "bonus-audit-race-after", "bonus-audit-race-update"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(before, func(tx *gorm.DB) { _, _ = roleOf(tx) }))
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(after, func(tx *gorm.DB) {
		model, ok := tx.Statement.Dest.(*Model)
		if !ok {
			return
		}
		role, ok := roleOf(tx)
		if !ok {
			return
		}
		if role == 0 {
			aOnce.Do(func() {
				lock, hasLock := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
				locking.Store(hasLock && lock.Strength == "UPDATE")
				contested.Store(int64(model.Id))
				close(aRead)
				wait(tx, releaseA)
			})
		} else if int64(model.Id) == contested.Load() && !locking.Load() {
			bOnce.Do(func() { close(bRead); wait(tx, releaseB) })
		}
	}))
	require.NoError(t, db.Callback().Update().After("gorm:update").Register(updated, func(tx *gorm.DB) {
		role, ok := roleOf(tx)
		if ok && role == 0 && !locking.Load() && kind != common.DatabaseTypeSQLite {
			updateOnce.Do(func() { close(aUpdated); wait(tx, releaseUpdate) })
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove(before))
		require.NoError(t, db.Callback().Query().Remove(after))
		require.NoError(t, db.Callback().Update().Remove(updated))
	})
	await := func(ch <-chan struct{}, name string) {
		t.Helper()
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatal("did not reach "+name+": ", ctx.Err())
		}
	}
	awaitNativeWait := func() {
		t.Helper()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			if connections[1].Load() > 0 {
				query := "SELECT COUNT(*) > 0 FROM information_schema.innodb_trx WHERE trx_mysql_thread_id = ? AND trx_state = 'LOCK WAIT'"
				args := []any{connections[1].Load()}
				if kind == common.DatabaseTypePostgreSQL {
					query = "SELECT $1 = ANY(pg_blocking_pids($2))"
					args = []any{connections[0].Load(), connections[1].Load()}
				}
				require.NoError(t, pool.QueryRowContext(ctx, query, args...).Scan(&waiting))
				if waiting {
					if kind == common.DatabaseTypeMySQL {
						// TxOptions uses SET TRANSACTION, not SET SESSION. Read
						// the active InnoDB transactions, never the session default.
						for role := range connections {
							var actual string
							require.NoError(t, pool.QueryRowContext(ctx, "SELECT trx_isolation_level FROM information_schema.innodb_trx WHERE trx_mysql_thread_id = ?", connections[role].Load()).Scan(&actual))
							require.Equal(t, strings.ToLower(isolation.String()), strings.ReplaceAll(strings.ToLower(actual), "-", " "))
							t.Logf("worker=%d physical connection=%d active InnoDB isolation=%s", role, connections[role].Load(), actual)
						}
					}
					t.Logf("server observed native lock wait: A=%d B=%d SELECT-FOR-UPDATE=%t", connections[0].Load(), connections[1].Load(), locking.Load())
					return
				}
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal("server never observed B's real lock wait: ", ctx.Err())
			}
		}
	}
	var changed [2]int
	var errs [2]error
	aDone := make(chan struct{})
	workers.Go(func() {
		changed[0], errs[0] = BatchUpdateModelBonusEligible(tc.firstIDs, tc.first, 41, "first-admin", "race")
		close(aDone)
	})
	await(aRead, "A's model read")
	secondKey := "race"
	if tc.differentKey {
		secondKey = "independent"
	}
	workers.Go(func() {
		changed[1], errs[1] = BatchUpdateModelBonusEligible(tc.secondIDs, tc.second, 42, "second-admin", secondKey)
	})
	if locking.Load() {
		awaitNativeWait()
		close(releaseA)
	} else {
		await(bRead, "both stale reads")
		require.GreaterOrEqual(t, pool.Stats().InUse, 2, "WAL/native readers must own separate physical connections")
		close(releaseA)
		if kind == common.DatabaseTypeSQLite {
			await(aDone, "A's commit before B's WAL snapshot upgrade")
			close(releaseB)
		} else {
			await(aUpdated, "A's real UPDATE lock")
			close(releaseB)
			awaitNativeWait()
			close(releaseUpdate)
		}
	}
	workers.Wait()
	t.Logf("first=%d/%v second=%d/%v", changed[0], errs[0], changed[1], errs[1])
	require.NoError(t, errs[0])
	require.Equal(t, len(tc.firstIDs), changed[0])
	if kind != common.DatabaseTypeSQLite {
		require.Positive(t, connections[0].Load())
		require.Positive(t, connections[1].Load())
		require.NotEqual(t, connections[0].Load(), connections[1].Load())
	} else {
		require.Error(t, errs[1], "WAL stale-reader upgrade must fail, not silently commit")
		require.True(t, strings.Contains(strings.ToLower(errs[1].Error()), "locked") || strings.Contains(strings.ToLower(errs[1].Error()), "busy"), errs[1])
		require.Zero(t, changed[1])
		// Remove barrier callbacks before a fresh whole-request retry; this is
		// test-client behavior, not a new production automatic retry policy.
		require.NoError(t, db.Callback().Query().Remove(before))
		require.NoError(t, db.Callback().Query().Remove(after))
		require.NoError(t, db.Callback().Update().Remove(updated))
		changed[1], errs[1] = BatchUpdateModelBonusEligible(tc.secondIDs, tc.second, 42, "second-admin", secondKey)
	}
	conflict := !tc.differentKey && !boolPtrEqual(tc.first, tc.second)
	if conflict {
		assert.ErrorIs(t, errs[1], ErrModelAuditConflict)
		assert.Zero(t, changed[1])
	} else {
		require.NoError(t, errs[1])
		expected := len(tc.secondIDs)
		if !tc.differentKey {
			expected -= len(tc.firstIDs)
		}
		assert.Equal(t, expected, changed[1])
	}
	var audits []ModelBonusEligibleAudit
	require.NoError(t, db.Order("id").Find(&audits).Error)
	expectedAudits := len(tc.firstIDs)
	if !conflict {
		expectedAudits += changed[1]
	}
	require.Len(t, audits, expectedAudits)
	for _, audit := range audits {
		if strings.HasPrefix(audit.IdempotencyKey, "race:") && audit.OperatorId == 41 {
			assert.Equal(t, tc.initial, audit.Before)
			assert.Equal(t, tc.first, audit.After)
			assert.Equal(t, "first-admin", audit.OperatorName)
		} else {
			assert.Equal(t, 42, audit.OperatorId)
			assert.Equal(t, "second-admin", audit.OperatorName)
			assert.Equal(t, tc.second, audit.After)
			if tc.differentKey {
				assert.Equal(t, tc.first, audit.Before, "independent keys must audit the locked predecessor")
			} else {
				assert.Equal(t, tc.initial, audit.Before)
			}
		}
		assert.NotZero(t, audit.CreatedAt)
	}
	for _, id := range tc.firstIDs {
		value := tc.first
		if tc.differentKey {
			value = tc.second
		}
		bonusAuditAssertValue(t, db, id, value)
	}
	if len(tc.firstIDs) == 1 && len(tc.secondIDs) == 2 {
		value := tc.initial
		if !conflict {
			value = tc.second
		}
		bonusAuditAssertValue(t, db, 1, value)
	}
	bonusAuditAssertCold(t)
}

func TestModelBonusEligibleAuditDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		kind common.DatabaseType
		env  string
	}{{common.DatabaseTypeSQLite, ""}, {common.DatabaseTypeMySQL, "TEST_BONUS_AUDIT_MYSQL_DSN"}, {common.DatabaseTypePostgreSQL, "TEST_BONUS_AUDIT_POSTGRES_DSN"}} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(dialect.env))
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured (dedicated empty disposable database required)")
			}
			db := bonusAuditDatabase(t, dialect.kind, dsn)
			runBonusAuditContracts(t, db)
			isolations := []sql.IsolationLevel{sql.LevelReadCommitted}
			if dialect.kind == common.DatabaseTypeMySQL {
				isolations = append(isolations, sql.LevelRepeatableRead)
			} else if dialect.kind == common.DatabaseTypeSQLite {
				isolations = []sql.IsolationLevel{sql.LevelDefault}
			}
			for _, isolation := range isolations {
				t.Run(strings.ReplaceAll(isolation.String(), " ", "_"), func(t *testing.T) {
					original := DB
					if dialect.kind != common.DatabaseTypeSQLite {
						wrapped := db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
						config := *wrapped.Config
						wrapped.Config = &config
						pool := &bonusAuditIsolationPool{ConnPool: db.ConnPool, isolation: isolation}
						wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
						DB = wrapped
					}
					t.Cleanup(func() { DB = original })
					for _, tc := range []bonusAuditRaceCase{
						{name: "false_then_true_conflict", first: boolPtr(false), second: boolPtr(true), firstIDs: []int{2}, secondIDs: []int{2}},
						{name: "true_then_false_conflict", first: boolPtr(true), second: boolPtr(false), firstIDs: []int{2}, secondIDs: []int{2}},
						{name: "true_then_null_conflict", initial: boolPtr(false), first: boolPtr(true), second: nil, firstIDs: []int{2}, secondIDs: []int{2}},
						{name: "same_target_replay", first: boolPtr(false), second: boolPtr(false), firstIDs: []int{2}, secondIDs: []int{2}},
						{name: "overlapping_batch_replay_applies_unclaimed_item", first: boolPtr(true), second: boolPtr(true), firstIDs: []int{2}, secondIDs: []int{1, 2}},
						{name: "overlapping_batch_conflict_rolls_back_unclaimed_item", first: boolPtr(false), second: boolPtr(true), firstIDs: []int{2}, secondIDs: []int{1, 2}},
						{name: "different_keys_preserve_actual_before", first: boolPtr(false), second: boolPtr(true), firstIDs: []int{2}, secondIDs: []int{2}, differentKey: true},
						{name: "reverse_input_batches", first: boolPtr(false), second: boolPtr(true), firstIDs: []int{2, 1}, secondIDs: []int{1, 2}},
					} {
						// In WAL deferred mode B must not write an unrelated row
						// before both snapshots: SQLite has only one writer.
						if dialect.kind == common.DatabaseTypeSQLite && len(tc.secondIDs) != 1 {
							continue
						}
						t.Run(tc.name, func(t *testing.T) { runBonusAuditRace(t, db, isolation, tc) })
					}
				})
			}
		})
	}
}
