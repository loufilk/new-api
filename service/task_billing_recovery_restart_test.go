package service

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const recoveryRestartFileEnv = "NEW_API_TEST_RECOVERY_RESTART_FILE"
const recoveryRestartPhaseEnv = "NEW_API_TEST_RECOVERY_RESTART_PHASE"

func migrateRecoveryFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	tables, err := db.Migrator().GetTables()
	require.NoError(t, err)
	require.Empty(t, tables, "refusing a nonempty disposable recovery database")
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.User{}, &model.Token{}, &model.Channel{}, &model.Log{}, &model.BillingFinalization{}, &model.SystemTask{}, &model.SystemTaskLock{}, &model.Midjourney{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
}

func bindRecoveryFixture(t *testing.T, db *gorm.DB, dialect common.DatabaseType) {
	t.Helper()
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
	})
}

// Two distinct, joined OS processes run successive real jobs against one file.
// This proves restart persistence, not native SQL row-lock behavior or a crash.
func TestBillingRecoveryFileRestart(t *testing.T) {
	if phase := os.Getenv(recoveryRestartPhaseEnv); phase != "" {
		t.Cleanup(func() {
			if !t.Failed() && !t.Skipped() {
				fmt.Println(refundFixtureChecked + t.Name())
			}
		}) // Runs after the child's binding restoration and SQL-pool close.
		require.Contains(t, []string{"history", "resume"}, phase)
		path := os.Getenv(recoveryRestartFileEnv)
		require.NotEmpty(t, path)
		_, err := os.Stat(path)
		require.NoError(t, err, "the child must open the parent's existing database")
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
		bindRecoveryFixture(t, db, common.DatabaseTypeSQLite)
		handler := billingRefundRecoveryHandler{}
		require.True(t, handler.Enabled())
		job, err := model.CreateSystemTask(handler.Type(), nil, nil)
		require.NoError(t, err)
		runner := fmt.Sprintf("restart-process-%d", os.Getpid())
		claimed, acquired, err := model.ClaimSystemTask(job.ID, handler.Type(), runner, time.Now().Add(time.Minute).Unix())
		require.NoError(t, err)
		require.True(t, acquired)
		handler.Run(t.Context(), claimed, runner)
		finished, err := model.GetSystemTaskByTaskID(job.TaskID)
		require.NoError(t, err)
		require.NotNil(t, finished)
		require.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
		require.Positive(t, recoveryStoredCursor(t, finished).TaskID)
		// No job/actor cleanup: the next process must consume this persisted state.
		t.Logf("recovery-restart-complete:%s pid=%d", phase, os.Getpid())
		return
	}

	path := filepath.Join(t.TempDir(), "recovery.sqlite")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(1)
		return db
	}
	db := open()
	t.Cleanup(func() {
		pool, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, pool.Close())
	})
	migrateRecoveryFixture(t, db)
	bindRecoveryFixture(t, db, common.DatabaseTypeSQLite)
	legacy, oldUser, oldToken, oldChannel := seedRecoveryLegacyPage(t, 101)
	oldMoney := reviewAccounting(t, oldUser, oldToken, oldChannel)
	payable, user, token, channel := seedAtomicFinancialTask(t, true)
	before := reviewAccounting(t, user, token, channel)
	executable, err := os.Executable()
	require.NoError(t, err)
	var firstID int64
	for _, phase := range []string{"history", "resume"} {
		pool, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, pool.Close(), "no parent SQL connection may carry state across the process boundary")
		budget := 30 * time.Second
		if deadline, ok := t.Deadline(); ok {
			budget = min(budget, time.Until(deadline)-3*time.Second)
		}
		require.Greater(t, budget, 3*time.Second)
		ctx, cancel := context.WithTimeout(t.Context(), budget)
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestBillingRecoveryFileRestart$", "-test.count=1", "-test.v=true", "-test.timeout="+(budget-time.Second).String())
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, recoveryRestartFileEnv+"=") && !strings.HasPrefix(entry, recoveryRestartPhaseEnv+"=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, recoveryRestartFileEnv+"="+path, recoveryRestartPhaseEnv+"="+phase)
		cmd.WaitDelay = time.Second
		output, runErr := cmd.CombinedOutput() // joined even on timeout; no detached worker
		ctxErr := ctx.Err()
		cancel()
		// Reopen before assertions so owned actor cleanup also works on failure.
		db = open()
		model.DB, model.LOG_DB = db, db
		t.Logf("restart %s child output:\n%s", phase, output)
		require.NoError(t, validateRefundFixtureChild(t.Name(), []string{t.Name()}, output, runErr, ctxErr))
		require.Contains(t, string(output), "recovery-restart-complete:"+phase)
		latest, err := model.GetLatestSystemTask((billingRefundRecoveryHandler{}).Type())
		require.NoError(t, err)
		require.NotNil(t, latest)
		if phase == "history" {
			firstID = latest.ID
			require.Equal(t, legacy[99].ID, recoveryStoredCursor(t, latest).TaskID)
			require.Equal(t, before, reviewAccounting(t, user, token, channel))
		} else {
			require.Greater(t, latest.ID, firstID)
			require.Equal(t, payable.ID, recoveryStoredCursor(t, latest).TaskID)
			requireRecoveryRefund(t, payable, user, token, channel)
		}
	}
	after := reviewAccounting(t, user, token, channel)
	recoveryFaultPass(t, model.SystemTaskStatusSucceeded)
	require.Equal(t, after, reviewAccounting(t, user, token, channel))
	for _, original := range legacy {
		var stored model.Task
		require.NoError(t, model.DB.First(&stored, original.ID).Error)
		assert.Equal(t, original, stored)
	}
	assert.Equal(t, oldMoney, reviewAccounting(t, oldUser, oldToken, oldChannel))
}

const recoveryNativeClaimEnv = "NEW_API_TEST_RECOVERY_NATIVE_CLAIM"
const recoveryNativeClaimPrefix = "recovery-native-claim:"

type recoveryNativeClaimConfig struct {
	Dialect         common.DatabaseType
	JobID, CursorID int64
	Runner, Nonce   string
}

type recoveryNativeClaimEvent struct {
	Phase, Runner, Nonce, Code string
	ServerID                   int64
	Acquired                   *bool
}

// Each child owns its global DB and opens only the parent's migrated native
// fixture. Errors use fixed codes: no DSN, SQL, or raw driver error is printed.
func recoveryNativeClaimChild(t *testing.T, configText string) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			fmt.Println(refundFixtureChecked + t.Name())
		}
	})
	var config recoveryNativeClaimConfig
	if common.UnmarshalJsonStr(configText, &config) != nil || config.JobID <= 0 || config.CursorID <= 0 || config.Runner == "" || config.Nonce == "" {
		t.Fatal("invalid native claim child configuration")
	}
	var driver gorm.Dialector
	dsn := ""
	query := "SELECT CONNECTION_ID()"
	switch config.Dialect {
	case common.DatabaseTypeMySQL:
		dsn = os.Getenv("TEST_BILLING_RECOVERY_MYSQL_DSN")
		driver = mysql.Open(dsn)
	case common.DatabaseTypePostgreSQL:
		dsn = os.Getenv("TEST_BILLING_RECOVERY_POSTGRES_DSN")
		driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
		query = "SELECT pg_backend_pid()"
	default:
		t.Fatal("invalid native claim child dialect")
	}
	if dsn == "" {
		t.Fatal("native claim child DSN missing")
	}
	db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("native claim child open failed")
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal("native claim child pool failed")
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if pool.Close() != nil {
			t.Error("native claim child close failed")
		}
	})
	bindRecoveryFixture(t, db, config.Dialect)
	var serverID int64
	if db.Raw(query).Scan(&serverID).Error != nil || serverID <= 0 {
		t.Fatal("native claim child identity failed")
	}
	emit := func(event recoveryNativeClaimEvent) {
		data, err := common.Marshal(event)
		if err != nil {
			t.Fatal("native claim child protocol failed")
		}
		fmt.Println(recoveryNativeClaimPrefix + string(data))
	}
	notAcquired := false
	emit(recoveryNativeClaimEvent{Phase: "ready", Runner: config.Runner, Nonce: config.Nonce, ServerID: serverID, Acquired: &notAcquired, Code: "ok"})
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil || line != config.Nonce+"\n" {
		t.Fatal("native claim child barrier failed")
	}
	claimed, acquired, err := model.ClaimSystemTask(config.JobID, (billingRefundRecoveryHandler{}).Type(), config.Runner, time.Now().Add(time.Minute).Unix())
	code := "ok"
	if err != nil {
		code = "claim_failed"
	}
	if err == nil && acquired {
		if claimed == nil {
			code = "claim_empty"
		} else if model.UpdateSystemTaskState(claimed.TaskID, config.Runner, billingRecoveryCursor{TaskID: config.CursorID}) != nil {
			code = "cursor_failed"
		}
	}
	emit(recoveryNativeClaimEvent{Phase: "result", Runner: config.Runner, Nonce: config.Nonce, ServerID: serverID, Acquired: &acquired, Code: code})
	if code != "ok" {
		t.Fatal(code)
	}
}

// Both processes report a real server session before either receives its nonce.
// The winner exits without releasing its lease; the parent verifies DB ownership
// before exercising live-lease rejection and expired takeover below.
func competeRecoveryNativeClaim(t *testing.T, dialect common.DatabaseType, job *model.SystemTask, cursorID int64) string {
	t.Helper()
	type child struct {
		input           io.WriteCloser
		pid             int
		ready           chan recoveryNativeClaimEvent
		done            chan struct{}
		output          bytes.Buffer
		stderr          bytes.Buffer
		events          []recoveryNativeClaimEvent
		runErr, scanErr error
	}
	budget := 30 * time.Second
	if deadline, ok := t.Deadline(); ok {
		budget = min(budget, time.Until(deadline)-3*time.Second)
	}
	require.Greater(t, budget, 3*time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	children := make([]*child, 0, 2)
	var releases sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		for _, c := range children {
			_ = c.input.Close()
		}
		releases.Wait()
		for _, c := range children {
			<-c.done
		}
	})
	executable, err := os.Executable()
	require.NoError(t, err)
	nonce := common.GetRandomString(32)
	for i := range 2 {
		config := recoveryNativeClaimConfig{Dialect: dialect, JobID: job.ID, CursorID: cursorID, Runner: fmt.Sprintf("native-recovery-contender-%d", i), Nonce: nonce}
		configText, err := common.Marshal(config)
		require.NoError(t, err)
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestBillingRecoveryNativeSessionLeaseHandoff$", "-test.count=1", "-test.v=true", "-test.timeout="+(budget-time.Second).String())
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, recoveryNativeClaimEnv+"=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, recoveryNativeClaimEnv+"="+string(configText))
		cmd.WaitDelay = time.Second
		input, err := cmd.StdinPipe()
		require.NoError(t, err)
		c := &child{input: input, ready: make(chan recoveryNativeClaimEvent, 1), done: make(chan struct{})}
		reader, writer := io.Pipe()
		cmd.Stdout, cmd.Stderr = writer, &c.stderr
		if err := cmd.Start(); err != nil {
			_ = input.Close()
			_ = reader.Close()
			_ = writer.Close()
			t.Fatal("native claim child start failed")
		}
		c.pid = cmd.Process.Pid
		children = append(children, c)
		scanned := make(chan struct{})
		go func() {
			defer close(scanned)
			defer reader.Close()
			scanner := bufio.NewScanner(io.TeeReader(reader, &c.output))
			for scanner.Scan() {
				if data, ok := strings.CutPrefix(scanner.Text(), recoveryNativeClaimPrefix); ok {
					var event recoveryNativeClaimEvent
					if common.UnmarshalJsonStr(data, &event) != nil {
						c.scanErr = fmt.Errorf("invalid native claim protocol")
						continue
					}
					c.events = append(c.events, event)
					if event.Phase == "ready" {
						select {
						case c.ready <- event:
						default:
							c.scanErr = fmt.Errorf("duplicate native claim readiness")
						}
					}
				}
			}
			if scanner.Err() != nil {
				c.scanErr = fmt.Errorf("native claim output read failed")
			}
		}()
		go func() {
			c.runErr = cmd.Wait() // exactly one Wait, also on every failed assertion
			_ = writer.Close()
			<-scanned
			close(c.done)
		}()
	}
	ready := make([]recoveryNativeClaimEvent, 2)
	for i, c := range children {
		select {
		case ready[i] = <-c.ready:
			require.Equal(t, fmt.Sprintf("native-recovery-contender-%d", i), ready[i].Runner)
			require.Equal(t, nonce, ready[i].Nonce)
			require.Positive(t, ready[i].ServerID)
			require.Equal(t, "ok", ready[i].Code)
			require.NotNil(t, ready[i].Acquired)
			require.False(t, *ready[i].Acquired)
			t.Logf("native claim ready: nonce=%s runner=%s pid=%d server_id=%d", nonce, ready[i].Runner, c.pid, ready[i].ServerID)
		case <-c.done:
			t.Fatal("native claim child exited before readiness")
		case <-ctx.Done():
			t.Fatal("native claim readiness deadline")
		}
	}
	require.NotEqual(t, children[0].pid, children[1].pid, "the contenders must be distinct OS processes")
	require.NotEqual(t, ready[0].ServerID, ready[1].ServerID, "the contenders must use distinct native server sessions")
	gate := make(chan struct{})
	releaseErrors := make([]error, 2)
	for i, c := range children {
		releases.Go(func() { <-gate; _, releaseErrors[i] = fmt.Fprintln(c.input, nonce); _ = c.input.Close() })
	}
	close(gate)
	releases.Wait()
	for _, err := range releaseErrors {
		require.NoError(t, err)
	}
	t.Logf("native claim barrier released: nonce=%s contenders=2", nonce)
	winner := ""
	for i, c := range children {
		<-c.done
		require.NoError(t, c.scanErr)
		require.Zero(t, c.stderr.Len(), "native claim child emitted unexpected diagnostics")
		require.NoError(t, validateRefundFixtureChild("TestBillingRecoveryNativeSessionLeaseHandoff", []string{"TestBillingRecoveryNativeSessionLeaseHandoff"}, c.output.Bytes(), c.runErr, ctx.Err()))
		require.Len(t, c.events, 2)
		require.Equal(t, ready[i], c.events[0])
		result := c.events[1]
		require.Equal(t, "result", result.Phase)
		require.Equal(t, ready[i].Runner, result.Runner)
		require.Equal(t, nonce, result.Nonce)
		require.Equal(t, ready[i].ServerID, result.ServerID)
		require.Equal(t, "ok", result.Code)
		require.NotNil(t, result.Acquired)
		if *result.Acquired {
			require.Empty(t, winner, "exactly one native process may win")
			winner = result.Runner
		}
		t.Logf("native claim result: nonce=%s runner=%s pid=%d server_id=%d acquired=%t code=%s joined_exit=0", nonce, result.Runner, c.pid, result.ServerID, *result.Acquired, result.Code)
	}
	require.NotEmpty(t, winner, "one native process must win")
	var owner model.SystemTaskLock
	require.NoError(t, model.DB.Where("type = ?", job.Type).First(&owner).Error)
	require.Equal(t, job.TaskID, owner.TaskID)
	require.Equal(t, winner, owner.LockedBy)
	require.GreaterOrEqual(t, owner.LockedUntil, common.GetTimestamp(), "the winning process must leave a live durable lease")
	var stored model.SystemTask
	require.NoError(t, model.DB.First(&stored, job.ID).Error)
	require.Equal(t, model.SystemTaskStatusRunning, stored.Status)
	require.Equal(t, winner, stored.LockedBy)
	require.Equal(t, cursorID, recoveryStoredCursor(t, &stored).TaskID)
	return winner
}

// Dedicated empty native databases are required. Barrier-released child
// processes compete for the first claim; parent sessions then test serial lease
// handoff and financial replay. SQLite is not an alternative for this test.
func TestBillingRecoveryNativeSessionLeaseHandoff(t *testing.T) {
	if config := os.Getenv(recoveryNativeClaimEnv); config != "" {
		recoveryNativeClaimChild(t, config)
		return
	}
	for _, dialect := range []struct {
		name common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeMySQL, "TEST_BILLING_RECOVERY_MYSQL_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_BILLING_RECOVERY_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.name), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			open := func() *gorm.DB {
				var driver gorm.Dialector = mysql.Open(dsn)
				if dialect.name == common.DatabaseTypePostgreSQL {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
				db, err := gorm.Open(driver, &gorm.Config{})
				require.NoError(t, err)
				pool, err := db.DB()
				require.NoError(t, err)
				pool.SetMaxOpenConns(1)
				pool.SetMaxIdleConns(1)
				t.Cleanup(func() { require.NoError(t, pool.Close()) })
				return db
			}
			a, b := open(), open()
			migrateRecoveryFixture(t, a)
			bindRecoveryFixture(t, a, dialect.name)
			query := "SELECT CONNECTION_ID()"
			if dialect.name == common.DatabaseTypePostgreSQL {
				query = "SELECT pg_backend_pid()"
			}
			var aID, bID int64
			require.NoError(t, a.Raw(query).Scan(&aID).Error)
			require.NoError(t, b.Raw(query).Scan(&bID).Error)
			require.NotEqual(t, aID, bID, "the handoff must cross actual server sessions, not GORM structs")
			var version string
			require.NoError(t, a.Raw("SELECT version()").Scan(&version).Error)
			t.Logf("native recovery handoff: %s %s; distinct server sessions %d/%d", dialect.name, version, aID, bID)

			legacy, oldUser, oldToken, oldChannel := seedRecoveryLegacyPage(t, 101)
			oldMoney := reviewAccounting(t, oldUser, oldToken, oldChannel)
			payable, user, token, channel := seedAtomicFinancialTask(t, true)
			before := reviewAccounting(t, user, token, channel)
			handler := billingRefundRecoveryHandler{}
			first, err := model.CreateSystemTask(handler.Type(), nil, nil)
			require.NoError(t, err)
			ownedJobIDs := []string{first.TaskID}
			t.Cleanup(func() {
				require.NoError(t, a.Where("task_id IN ?", ownedJobIDs).Delete(&model.SystemTaskLock{}).Error)
				require.NoError(t, a.Where("task_id IN ?", ownedJobIDs).Delete(&model.SystemTask{}).Error)
			})
			winner := competeRecoveryNativeClaim(t, dialect.name, first, legacy[99].ID)
			// An old pending row may predate the active-key invariant. It must
			// still be blocked by the live type lease on the other connection.
			second := &model.SystemTask{TaskID: "native-recovery-pending", Type: handler.Type(), Status: model.SystemTaskStatusPending}
			require.NoError(t, a.Create(second).Error)
			ownedJobIDs = append(ownedJobIDs, second.TaskID)
			model.DB, model.LOG_DB = b, b
			_, acquired, err := model.ClaimSystemTask(second.ID, handler.Type(), "native-recovery-b", time.Now().Add(time.Minute).Unix())
			require.NoError(t, err)
			require.False(t, acquired)
			require.Equal(t, before, reviewAccounting(t, user, token, channel))
			require.NoError(t, a.Model(&model.SystemTaskLock{}).Where("task_id = ? AND locked_by = ?", first.TaskID, winner).Update("locked_until", common.GetTimestamp()-1).Error)
			claimed, acquired, err := model.ClaimSystemTask(second.ID, handler.Type(), "native-recovery-b", time.Now().Add(time.Minute).Unix())
			require.NoError(t, err)
			require.True(t, acquired)
			model.DB, model.LOG_DB = a, a
			require.ErrorIs(t, model.UpdateSystemTaskState(first.TaskID, winner, billingRecoveryCursor{TaskID: payable.ID}), model.ErrSystemTaskLockLost)
			require.ErrorIs(t, model.FinishSystemTask(first.TaskID, winner, model.SystemTaskStatusSucceeded, nil, ""), model.ErrSystemTaskLockLost)
			var expired model.SystemTask
			require.NoError(t, a.First(&expired, first.ID).Error)
			require.Equal(t, model.SystemTaskStatusFailed, expired.Status)
			require.Equal(t, "task lease expired", expired.Error)
			require.Equal(t, legacy[99].ID, recoveryStoredCursor(t, &expired).TaskID)
			model.DB, model.LOG_DB = b, b
			handler.Run(t.Context(), claimed, "native-recovery-b")
			finished, err := model.GetSystemTaskByTaskID(second.TaskID)
			require.NoError(t, err)
			require.NotNil(t, finished)
			require.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
			require.Equal(t, payable.ID, recoveryStoredCursor(t, finished).TaskID)
			after := requireRecoveryRefund(t, payable, user, token, channel)
			// The independent immediate financial executor has no system-task
			// lease; its idempotency comes from the durable task receipt instead.
			model.DB, model.LOG_DB = a, a
			applied, err := ApplyTaskQuota(t.Context(), payable, 0, true)
			require.NoError(t, err)
			require.False(t, applied)
			require.Equal(t, after, reviewAccounting(t, user, token, channel))
			for _, original := range legacy {
				var stored model.Task
				require.NoError(t, a.First(&stored, original.ID).Error)
				assert.Equal(t, original, stored)
			}
			assert.Equal(t, oldMoney, reviewAccounting(t, oldUser, oldToken, oldChannel))
		})
	}
}
