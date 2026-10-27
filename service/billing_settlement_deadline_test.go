package service

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Use a real built-in funding session. SQLite needs a file here: cancellation
// may discard its SQL connection, but must not discard the evidence database.
// Native matrix callers keep their dedicated MySQL/PostgreSQL fixture.
func settlementDeadlineSession(t *testing.T, request string, withToken bool, source string) (*BillingSession, context.CancelFunc) {
	t.Helper()
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "settlement.db")), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.BillingFinalization{}, &model.Model{}, &model.Vendor{}, &model.ModelBonusEligibleAudit{}, &model.Option{}, &model.Ability{}, &model.Channel{}, &model.UserSubscription{}, &model.SubscriptionPlan{}, &model.SubscriptionPreConsumeRecord{}))
		original := model.DB
		model.DB = db
		t.Cleanup(func() { model.DB = original; require.NoError(t, sqlDB.Close()) })
	}
	db := model.DB
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	seedBonusUser(t, 93201, 0, 1000)
	seedToken(t, 93202, 93201, "synthetic-settlement-deadline-token", 1000)
	seedBonusEligibleModel(t, "settlement-deadline-model", boolPtr(false))
	var planID int
	t.Cleanup(func() {
		for _, entity := range []any{&model.BillingFinalization{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}} {
			require.NoError(t, db.Where("user_id = ?", 93201).Delete(entity).Error)
		}
		require.NoError(t, db.Unscoped().Delete(&model.Token{}, 93202).Error)
		require.NoError(t, db.Unscoped().Delete(&model.User{}, 93201).Error)
		require.NoError(t, db.Unscoped().Where("model_name = ?", "settlement-deadline-model").Delete(&model.Model{}).Error)
		if planID > 0 {
			require.NoError(t, db.Where("user_id = ?", 93201).Delete(&model.SubscriptionPreConsumeRecord{}).Error)
			require.NoError(t, db.Delete(&model.UserSubscription{}, 93203).Error)
			require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Delete(&model.SubscriptionPlan{}, planID).Error)
			model.InvalidateSubscriptionPlanCache(planID)
		}
		model.InvalidateModelBonusEligibleCache()
	})
	setting := operation_setting.GetQuotaSetting()
	oldTrust := setting.TrustQuotaUSD
	setting.TrustQuotaUSD = 0
	t.Cleanup(func() { setting.TrustQuotaUSD = oldTrust })
	c := testGinContext(t)
	client, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest("POST", "/settlement-deadline-test", nil).WithContext(client)
	c.Set("token_quota", 1000)
	info := &relaycommon.RelayInfo{UserId: 93201, TokenId: 93202, TokenKey: "synthetic-settlement-deadline-token", RequestId: request, OriginModelName: "settlement-deadline-model", IsPlayground: !withToken, UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}}
	if source == BillingSourceSubscription {
		plan := &model.SubscriptionPlan{Title: "settlement deadline plan", TotalAmount: 1000, QuotaResetPeriod: model.SubscriptionResetNever}
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(plan).Error)
		planID = plan.Id
		model.InvalidateSubscriptionPlanCache(planID)
		now := model.GetDBTimestamp()
		require.NoError(t, db.Create(&model.UserSubscription{Id: 93203, UserId: 93201, PlanId: planID, AmountTotal: 1000, AmountUsed: 500, QuotaPeriod: 1, Status: "active", StartTime: now - 10, EndTime: now + 3600}).Error)
		info.UserSetting.BillingPreference = "subscription_only"
	}
	s, apiErr := NewBillingSession(c, info, 100)
	require.Nil(t, apiErr)
	info.Billing = s
	return s, cancel
}

// The first transaction either waits in its driver, waits in the real SQL
// connection pool, or completes COMMIT/ROLLBACK but withholds its ACK until the
// operation expires. Later probes use the normal authoritative SQL path.
type settlementDeadlinePool struct {
	gorm.ConnPool
	mode     string
	first    atomic.Bool
	skip     atomic.Int32
	entered  chan context.Context
	released chan struct{}
	sqlDB    *sql.DB
	held     chan *sql.Conn
}

func (p *settlementDeadlinePool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	if p.skip.Load() > 0 && p.skip.Add(-1) >= 0 {
		return p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	}
	if !p.first.CompareAndSwap(false, true) {
		return p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	}
	if p.mode == "connection" {
		// Occupy the real pool only after the read-only intent recheck has
		// committed, so the timed wait still belongs to the money transaction.
		conn, err := p.sqlDB.Conn(ctx)
		if err != nil {
			return nil, err
		}
		p.held <- conn
	}
	p.entered <- ctx
	if p.mode == "driver" {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.released:
			return nil, errors.New("test driver released before its deadline")
		}
	}
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil || p.mode == "connection" {
		return tx, err
	}
	return &settlementDeadlineTx{Tx: tx, ctx: ctx, commit: p.mode == "committed_lost_ack", released: p.released}, nil
}

type settlementDeadlineTx struct {
	*sql.Tx
	ctx      context.Context
	commit   bool
	released <-chan struct{}
}

func (tx *settlementDeadlineTx) Commit() error {
	var err error
	if tx.commit {
		err = tx.Tx.Commit()
	} else {
		err = tx.Tx.Rollback()
	}
	if err != nil {
		return err
	}
	select {
	case <-tx.ctx.Done():
		return tx.ctx.Err()
	case <-tx.released:
		return errors.New("test ACK released before its deadline")
	}
}

func installSettlementDeadlinePool(t *testing.T, mode string, skipTransactions ...int32) *settlementDeadlinePool {
	t.Helper()
	original := model.DB
	wrapped := original.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	sqlDB, err := original.DB()
	require.NoError(t, err)
	pool := &settlementDeadlinePool{ConnPool: original.ConnPool, mode: mode, entered: make(chan context.Context, 1), released: make(chan struct{}), sqlDB: sqlDB, held: make(chan *sql.Conn, 1)}
	if len(skipTransactions) > 0 {
		pool.skip.Store(skipTransactions[0])
	}
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
	model.DB = wrapped
	t.Cleanup(func() { model.DB = original })
	return pool
}

// Watchdogs only fail a broken implementation; normal progress is driven by
// the operation context or an explicit release, never a scheduling sleep.
// Allow the five-second operation, one-second failure-evidence tail and bounded
// cache cleanup to finish; the asserted money-operation deadline stays five seconds.
func awaitSettlementResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("settlement retained the session beyond its cooperative operation/cache budgets")
		return nil
	}
}

func assertSettlementBalances(t *testing.T, wallet, token int) {
	t.Helper()
	assertUserBalances(t, 93201, 0, wallet, 0)
	var stored model.Token
	require.NoError(t, model.DB.First(&stored, 93202).Error)
	require.Equal(t, token, stored.RemainQuota)
	require.Equal(t, 1000-token, stored.UsedQuota)
}

// Current entry has already committed a durable hold before the attempted
// settlement. A failed deadline may preserve that receipt, never invent a
// terminal record or mutate the original identity/source/token amounts.
func settlementDeadlineHeldReceipt(t *testing.T, request string) model.BillingFinalization {
	t.Helper()
	var receipts []model.BillingFinalization
	require.NoError(t, model.DB.Where("request_id = ?", request).Find(&receipts).Error)
	require.Len(t, receipts, 1)
	require.Equal(t, model.BillingHeld, receipts[0].State)
	return receipts[0]
}

func freezeSettlementDeadlineReceipt(t *testing.T, s *BillingSession, actual int) model.BillingFinalization {
	t.Helper()
	receipt := settlementDeadlineHeldReceipt(t, s.relayInfo.RequestId)
	require.NoError(t, model.RequestBillingSettlement(context.Background(), receipt, actual))
	var frozen model.BillingFinalization
	require.NoError(t, model.DB.First(&frozen, "request_id = ?", receipt.RequestID).Error)
	require.Equal(t, model.BillingSettlementReview, frozen.State)
	require.Equal(t, actual, frozen.ActualQuota)
	return frozen
}

func assertSettlementDeadlineReviewReceipt(t *testing.T, frozen model.BillingFinalization) {
	t.Helper()
	var stored model.BillingFinalization
	require.NoError(t, model.DB.First(&stored, "request_id = ?", frozen.RequestID).Error)
	require.Equal(t, model.BillingSettlementReview, stored.State)
	require.Zero(t, stored.NextRetryAt, "review does not schedule an automatic debit")
	// Failure diagnostics may evolve; identity, wallet shares, original Token,
	// subscription period, held amounts and frozen actual must not change.
	frozen.Attempts = stored.Attempts
	frozen.FirstErrorCode, frozen.FirstErrorClass = stored.FirstErrorCode, stored.FirstErrorClass
	frozen.LastErrorCode, frozen.LastErrorClass = stored.LastErrorCode, stored.LastErrorClass
	frozen.FirstFailedAt, frozen.LastAttemptAt = stored.FirstFailedAt, stored.LastAttemptAt
	frozen.EscalatedAt, frozen.UpdatedAt = stored.EscalatedAt, stored.UpdatedAt
	require.Equal(t, frozen, stored)
}

func TestBillingSettlementDeadlineDriverAndConnectionWait(t *testing.T) {
	for _, mode := range []string{"driver", "connection"} {
		t.Run(mode, func(t *testing.T) {
			s, cancelClient := settlementDeadlineSession(t, "settlement-wait-"+mode, true, BillingSourceWallet)
			initialReceipt := freezeSettlementDeadlineReceipt(t, s, 200)
			cancelClient()
			pool := installSettlementDeadlinePool(t, mode, 1)
			var release sync.Once
			releaseWait := func() { release.Do(func() { close(pool.released) }) }
			var held *sql.Conn
			result := make(chan error, 1)
			joined := false
			go func() { result <- s.Settle(200) }()
			defer func() {
				releaseWait()
				if held == nil {
					select {
					case held = <-pool.held:
					default:
					}
				}
				if held != nil {
					_ = held.Close()
				}
				if !joined {
					<-result
				}
				// A watchdog may fire before the worker acquires its connection.
				// After joining, also close any late acquisition queued by it.
				select {
				case lateHeld := <-pool.held:
					_ = lateHeld.Close()
				default:
				}
			}()
			var attempt context.Context
			select {
			case attempt = <-pool.entered:
			case <-time.After(time.Second):
				t.Fatal("settlement never reached the transaction boundary")
			}
			if mode == "connection" {
				held = <-pool.held
			}
			deadline, bounded := attempt.Deadline()
			require.True(t, bounded)
			require.WithinDuration(t, time.Now().Add(5*time.Second), deadline, time.Second)
			require.NoError(t, attempt.Err(), "cancelled client must not cancel settlement")
			err := awaitSettlementResult(t, result)
			joined = true
			require.ErrorIs(t, err, context.DeadlineExceeded)
			if held != nil {
				require.NoError(t, held.Close())
				held = nil
			}
			require.False(t, s.NeedsRefund(), "a timed-out charge remains frozen, not refundable")
			require.True(t, s.settlementUncertain)
			assertSettlementBalances(t, 900, 900)
			assertSettlementDeadlineReviewReceipt(t, initialReceipt)
			require.ErrorIs(t, s.Reserve(150), model.ErrBillingFinalized)
			s.Refund(testGinContext(t))
			assertSettlementBalances(t, 900, 900)
			require.NoError(t, s.Settle(200))
			require.NoError(t, s.Settle(200))
			require.False(t, s.NeedsRefund())
			assertSettlementBalances(t, 800, 800)
		})
	}
}

func TestBillingSettlementDeadlineNativeRowLock(t *testing.T) {
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		t.Skip("SQLite has no SELECT FOR UPDATE; real connection-wait coverage is separate")
	}
	s, cancelClient := settlementDeadlineSession(t, "settlement-held-native-row", true, BillingSourceWallet)
	initialReceipt := freezeSettlementDeadlineReceipt(t, s, 200)
	cancelClient()
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(2)
	t.Cleanup(func() { sqlDB.SetMaxOpenConns(1) })
	held := model.DB.Begin()
	require.NoError(t, held.Error)
	var id int
	// Deliberately hold the exact authoritative user row from another native
	// transaction. Both supported native dialects use this standard SQL form.
	require.NoError(t, held.Raw("SELECT id FROM users WHERE id = ? FOR UPDATE", 93201).Scan(&id).Error)
	require.Equal(t, 93201, id)
	result := make(chan error, 1)
	joined := false
	go func() { result <- s.Settle(200) }()
	defer func() {
		_ = held.Rollback().Error
		if !joined {
			<-result
		}
	}()
	err = awaitSettlementResult(t, result)
	joined = true
	require.ErrorIs(t, err, context.DeadlineExceeded, "the operation budget must cancel the held native row wait")
	require.NoError(t, held.Rollback().Error)
	require.False(t, s.NeedsRefund())
	assertSettlementBalances(t, 900, 900)
	assertSettlementDeadlineReviewReceipt(t, initialReceipt)
	require.ErrorIs(t, s.Reserve(150), model.ErrBillingFinalized)
	s.Refund(testGinContext(t))
	assertSettlementBalances(t, 900, 900)
	require.NoError(t, s.Settle(200))
	require.NoError(t, s.Settle(200))
	assertSettlementBalances(t, 800, 800)
}

func TestBillingSettlementDeadlineCommitEvidence(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	for _, source := range []string{BillingSourceWallet} {
		t.Run(source, func(t *testing.T) {
			for _, mode := range []string{"rolled_back", "committed_lost_ack"} {
				t.Run(mode, func(t *testing.T) {
					s, cancelClient := settlementDeadlineSession(t, "settlement-ack-"+source+"-"+mode, true, source)
					initialReceipt := freezeSettlementDeadlineReceipt(t, s, 200)
					cancelClient()
					pool := installSettlementDeadlinePool(t, mode, 1)
					result := make(chan error, 1)
					joined := false
					go func() { result <- s.Settle(200) }()
					defer func() {
						close(pool.released)
						if !joined {
							<-result
						}
					}()
					err := awaitSettlementResult(t, result)
					joined = true
					require.ErrorIs(t, err, context.DeadlineExceeded)
					require.True(t, s.settlementUncertain)
					require.False(t, s.NeedsRefund(), "unknown ACK is not success, but observed usage is never refundable")
					expectedWallet := 800
					if mode == "rolled_back" {
						expectedWallet = 900
					}

					if mode == "rolled_back" {
						assertSettlementBalances(t, expectedWallet, 900)
						assertSettlementDeadlineReviewReceipt(t, initialReceipt)
					} else {
						assertSettlementBalances(t, expectedWallet, 800)
					}
					require.ErrorIs(t, s.Reserve(250), model.ErrBillingFinalized)
					s.Refund(testGinContext(t))
					waitRefundCallback(t, s)
					require.False(t, s.settled, "neither Reserve nor Refund may invent a successful ACK")
					require.NoError(t, s.Settle(200))
					require.NoError(t, s.Settle(200))
					require.Equal(t, 200, s.actualSettled)
					require.Error(t, s.Settle(201))
					expectedHeld := 100
					expectedWallet = 800

					assertSettlementBalances(t, expectedWallet, 800)
					var owner model.BillingFinalization
					require.NoError(t, model.DB.First(&owner, "request_id = ?", s.relayInfo.RequestId).Error)
					require.Equal(t, model.BillingSettled, owner.State)
					require.Equal(t, 200, owner.ActualQuota)
					require.Equal(t, 93201, owner.UserID)
					require.Equal(t, 93202, owner.TokenID)
					require.Equal(t, source, owner.Source)
					require.Equal(t, expectedHeld, owner.SourceHeld)
					require.Equal(t, expectedHeld, owner.TokenHeld)
					require.Equal(t, expectedHeld, owner.PreConsumed)
					require.Equal(t, model.BillingTokenFingerprint("synthetic-settlement-deadline-token"), owner.TokenFingerprint)

				})
			}
		})
	}
}

type settlementRedisHook struct {
	failLive bool
	userDELs atomic.Int32
}

func (h *settlementRedisHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *settlementRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *settlementRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "del" && len(cmd.Args()) == 2 && cmd.Args()[1] == "user:93201" {
			h.userDELs.Add(1)
		}
		if h.failLive {
			return errors.New("synthetic live Redis failure")
		}
		return next(ctx, cmd)
	}
}

func TestBillingSettlementDeadlineRedisBoundaries(t *testing.T) {
	for _, mode := range []string{"precommit_fence", "postcommit_cache", "zero_delta_cache", "terminal_replay_cache", "live_redis_error"} {
		t.Run(mode, func(t *testing.T) {
			withToken := mode != "postcommit_cache"
			s, cancelClient := settlementDeadlineSession(t, "settlement-redis-"+mode, withToken, BillingSourceWallet)
			cancelClient()
			actual := 200
			if mode == "zero_delta_cache" {
				actual = 100
			}
			initialReceipt := freezeSettlementDeadlineReceipt(t, s, actual)
			if mode == "terminal_replay_cache" {
				flag := terminalCommitFault(t, 2, 1)
				require.Error(t, s.Settle(actual), "seed a real committed owner with an unknown acknowledgement")
				require.Zero(t, flag.Load(), "money COMMIT fault must be consumed")
			}
			oldRedis, oldEnabled := common.RDB, common.RedisEnabled
			var sockets sync.WaitGroup
			var socketMu sync.Mutex
			var servers []net.Conn
			closed := false
			client := redis.NewClient(&redis.Options{ReadTimeout: -1, WriteTimeout: -1, MaxRetries: -1, ContextTimeoutEnabled: true, DisableIdentity: true, Protocol: 2,
				Dialer: func(context.Context, string, string) (net.Conn, error) {
					clientConn, serverConn := net.Pipe()
					socketMu.Lock()
					defer socketMu.Unlock()
					if closed {
						_ = clientConn.Close()
						_ = serverConn.Close()
						return nil, net.ErrClosed
					}
					servers = append(servers, serverConn)
					sockets.Go(func() { _, _ = io.Copy(io.Discard, serverConn); _ = serverConn.Close() })
					return clientConn, nil
				},
			})
			hook := &settlementRedisHook{failLive: mode == "live_redis_error"}
			client.AddHook(hook)
			common.RDB, common.RedisEnabled = client, true
			t.Cleanup(func() {
				closeErr := client.Close()
				socketMu.Lock()
				closed = true
				for _, server := range servers {
					_ = server.Close()
				}
				socketMu.Unlock()
				sockets.Wait()
				common.RDB, common.RedisEnabled = oldRedis, oldEnabled
				require.NoError(t, closeErr)
			})
			result := make(chan error, 1)
			joined := false
			go func() { result <- s.Settle(actual) }()
			defer func() {
				if !joined {
					_ = client.Close()
					<-result
				}
			}()
			err := awaitSettlementResult(t, result)
			joined = true
			require.EqualValues(t, 2, hook.userDELs.Load(), "one logical settle has only one two-attempt cache tail")
			common.RedisEnabled = false
			if mode == "precommit_fence" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.False(t, s.NeedsRefund())
				assertSettlementBalances(t, 900, 900)
				assertSettlementDeadlineReviewReceipt(t, initialReceipt)
				require.ErrorIs(t, s.Reserve(150), model.ErrBillingFinalized)
				s.Refund(testGinContext(t))
				assertSettlementBalances(t, 900, 900)
			} else {
				require.NoError(t, err, "cache failure, while work context is live or after commit, preserves the financial policy")
				require.False(t, s.NeedsRefund())
			}
			require.NoError(t, s.Settle(actual))
			expectedToken := 1000 - actual
			if !withToken {
				expectedToken = 1000
			}
			assertSettlementBalances(t, 1000-actual, expectedToken)
		})
	}
}

func TestBillingSettlementSurvivesCancelledClient(t *testing.T) {
	s, cancelClient := settlementDeadlineSession(t, "settlement-after-client-cancel", true, BillingSourceWallet)
	cancelClient()
	require.NoError(t, s.Settle(200))
	require.NoError(t, s.Settle(200))
	require.False(t, s.NeedsRefund())
	assertSettlementBalances(t, 800, 800)
}

// This opt-in target must be a dedicated disposable Redis instance: CLIENT
// PAUSE affects that server's writes. The ordinary suite never pauses a shared
// cache, and this fixture always unpauses before releasing its owned clients.
func TestBillingSettlementDeadlineNativeRedisFence(t *testing.T) {
	address := os.Getenv("TEST_SETTLEMENT_DEADLINE_REDIS_ADDR")
	if address == "" || os.Getenv("TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE") != "1" {
		t.Skip("dedicated TEST_SETTLEMENT_DEADLINE_REDIS_ADDR and TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE=1 required")
	}
	s, cancelClient := settlementDeadlineSession(t, "settlement-native-redis-fence", true, BillingSourceWallet)
	initialReceipt := freezeSettlementDeadlineReceipt(t, s, 200)
	cancelClient()
	client := redis.NewClient(&redis.Options{Addr: address, ReadTimeout: -1, WriteTimeout: -1, MaxRetries: -1, ContextTimeoutEnabled: true, DisableIdentity: true, Protocol: 2})
	admin := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1, ContextTimeoutEnabled: true, DisableIdentity: true, Protocol: 2})
	oldRedis, oldEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = client, true
	unpause := func() error {
		ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		return admin.Do(ctx, "CLIENT", "UNPAUSE").Err()
	}
	t.Cleanup(func() {
		unpauseErr := unpause()
		closeErr := client.Close()
		adminCloseErr := admin.Close()
		common.RDB, common.RedisEnabled = oldRedis, oldEnabled
		require.NoError(t, unpauseErr)
		require.NoError(t, closeErr)
		require.NoError(t, adminCloseErr)
	})
	adminCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(adminCtx).Err())
	info, err := admin.Info(adminCtx, "server").Result()
	require.NoError(t, err)
	for line := range strings.SplitSeq(info, "\n") {
		if strings.HasPrefix(line, "redis_version:") {
			t.Log(strings.TrimSpace(line))
		}
	}
	require.NoError(t, admin.Do(adminCtx, "CLIENT", "PAUSE", 12000, "WRITE").Err())
	result := make(chan error, 1)
	joined := false
	go func() { result <- s.Settle(200) }()
	defer func() {
		if !joined {
			_ = unpause()
			_ = client.Close()
			<-result
		}
	}()
	err = awaitSettlementResult(t, result)
	joined = true
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, unpause())
	common.RedisEnabled = false
	require.False(t, s.NeedsRefund())
	assertSettlementBalances(t, 900, 900)
	assertSettlementDeadlineReviewReceipt(t, initialReceipt)
	require.ErrorIs(t, s.Reserve(150), model.ErrBillingFinalized)
	s.Refund(testGinContext(t))
	assertSettlementBalances(t, 900, 900)
	require.NoError(t, s.Settle(200))
	require.NoError(t, s.Settle(200))
	assertSettlementBalances(t, 800, 800)
}

// A lost money COMMIT ACK cannot turn observed usage into a refund. Only an
// explicit same-actual settlement retry may confirm the durable terminal owner.
func TestOfficialRefundBeforeLostACKReadbackKeepsOfficialSequence(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	s, cancelClient := settlementDeadlineSession(t, "official-refund-before-readback", true, BillingSourceWallet)
	freezeSettlementDeadlineReceipt(t, s, 200)
	cancelClient()
	pool := installSettlementDeadlinePool(t, "committed_lost_ack", 1)
	result := make(chan error, 1)
	joined := false
	go func() { result <- s.Settle(200) }()
	defer func() {
		close(pool.released)
		if !joined {
			<-result
		}
		waitRefundCallback(t, s)
	}()
	err := awaitSettlementResult(t, result)
	joined = true
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.True(t, s.settlementUncertain)
	require.False(t, s.NeedsRefund())
	assertSettlementBalances(t, 800, 800)
	var before model.BillingFinalization
	require.NoError(t, model.DB.Where("request_id = ?", s.relayInfo.RequestId).First(&before).Error)
	require.Equal(t, model.BillingSettled, before.State)
	require.Equal(t, 200, before.ActualQuota)
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	require.False(t, s.NeedsRefund(), "frozen actual blocks refund even before ACK reconciliation")
	require.False(t, s.settled, "Refund cannot manufacture a successful settlement readback")
	assertSettlementBalances(t, 800, 800)
	var after model.BillingFinalization
	require.NoError(t, model.DB.Where("request_id = ?", before.RequestID).First(&after).Error)
	require.Equal(t, before, after, "Refund does not rewrite the normal settlement evidence")
	require.NoError(t, s.Settle(200))
	assertSettlementBalances(t, 800, 800)
}
