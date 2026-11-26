package model

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWalletLockTimingReportSeparatesTransactionPhases(t *testing.T) {
	for raw, expected := range map[string]uint64{"": 0, "no": 0, "-1": 0, "1001": 0, "0": 0, " 50 ": 50, "1000": 1000} {
		assert.Equal(t, expected, parseWalletLockSamplePermille(raw), raw)
	}
	now := time.Now()
	timing := &walletLockTiming{operation: "settlement", started: now}
	neverEntered := timing.report(now.Add(time.Millisecond), false)
	assert.Nil(t, neverEntered.PoolAndBeginUS)
	assert.Nil(t, neverEntered.WalletLockQueryUS)
	assert.Nil(t, neverEntered.AfterLockQueryToTxReturnUS)
	assert.False(t, neverEntered.CommitAcknowledged)

	timing.entered = now.Add(time.Millisecond)
	timing.lockQueryStarted = now.Add(2 * time.Millisecond)
	timing.lockReturned = now.Add(5 * time.Millisecond)
	failedLock := timing.report(now.Add(8*time.Millisecond), false)
	require.NotNil(t, failedLock.PoolAndBeginUS)
	require.NotNil(t, failedLock.WalletLockQueryUS)
	assert.EqualValues(t, 1_000, *failedLock.PoolAndBeginUS)
	assert.EqualValues(t, 3_000, *failedLock.WalletLockQueryUS)
	assert.Nil(t, failedLock.AfterLockQueryToTxReturnUS)

	timing.lockAcquired = true
	committed := timing.report(now.Add(8*time.Millisecond), true)
	require.NotNil(t, committed.AfterLockQueryToTxReturnUS)
	assert.EqualValues(t, 3_000, *committed.AfterLockQueryToTxReturnUS)
	assert.EqualValues(t, 8_000, committed.TxReturnUS)
	assert.True(t, committed.CommitAcknowledged)
}

type walletTimingLogBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *walletTimingLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *walletTimingLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func TestWalletLockTimingPostgresReservationAndSettlement(t *testing.T) {
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires disposable PostgreSQL fixture")
	}
	require.NoError(t, DB.AutoMigrate(&BillingFinalization{}))
	var output walletTimingLogBuffer
	common.LogWriterMu.Lock()
	writer := gin.DefaultWriter
	gin.DefaultWriter = &output
	common.LogWriterMu.Unlock()
	rate := walletLockSamplePermille
	walletLockSamplePermille = func() uint64 { return 1000 }
	t.Cleanup(func() {
		walletLockSamplePermille = rate
		common.LogWriterMu.Lock()
		gin.DefaultWriter = writer
		common.LogWriterMu.Unlock()
	})

	user := User{Username: "wallet-timing", AffCode: "wtim", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("user_id = ?", user.Id).Delete(&BillingFinalization{}).Error)
		require.NoError(t, DB.Unscoped().Delete(&user).Error)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	blocker := DB.WithContext(ctx).Begin()
	require.NoError(t, blocker.Error)
	defer blocker.Rollback()
	require.NoError(t, LockFinancialUserTx(blocker, user.Id))
	var blockerPID int
	require.NoError(t, blocker.Raw("SELECT pg_backend_pid()").Scan(&blockerPID).Error)

	const privateID = "wallet-lock-timing-private-id"
	auth := BillingFinalization{RequestID: privateID, UserID: user.Id, Source: "wallet"}
	type result struct {
		held    BillingFinalization
		attempt BillingReservationAttempt
		err     error
	}
	finished := make(chan result, 1)
	joined := make(chan struct{})
	defer func() { cancel(); <-joined }()
	go func() {
		defer close(joined)
		held, attempt, err := ReserveBillingFinalization(ctx, auth, nil, 0, func(*gorm.DB, *BillingFinalization) error { return nil })
		finished <- result{held, attempt, err}
	}()
	// Wait for authoritative blocking evidence, not a guessed sleep, before
	// releasing the competing transaction's users row lock.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var blocked bool
		err := DB.WithContext(ctx).Raw("SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND ? = ANY(pg_blocking_pids(pid)))", blockerPID).Scan(&blocked).Error
		assert.NoError(c, err)
		assert.True(c, blocked)
	}, 3*time.Second, 10*time.Millisecond)
	assert.NotContains(t, output.String(), "wallet_lock_timing ", "a blocked transaction must not emit an acknowledgement")
	require.NoError(t, blocker.Commit().Error)
	reserved := <-finished
	require.NoError(t, reserved.err)
	require.True(t, reserved.attempt.Applied)
	require.Equal(t, BillingHeld, reserved.held.State)
	applied, err := SettleBillingFinalization(ctx, reserved.held, 0, func(*gorm.DB) error { return nil })
	require.NoError(t, err)
	require.True(t, applied)

	// Replay does not lock the wallet again, and a rolled back reservation must
	// never be reported as a committed hold or persist an owner.
	applied, err = SettleBillingFinalization(ctx, reserved.held, 0, func(*gorm.DB) error { return errors.New("must not replay settlement") })
	require.NoError(t, err)
	require.False(t, applied)
	auth.RequestID += "-rollback"
	rollback := errors.New("synthetic rollback")
	_, attempt, err := ReserveBillingFinalization(ctx, auth, nil, 0, func(*gorm.DB, *BillingFinalization) error { return rollback })
	require.ErrorIs(t, err, rollback)
	assert.False(t, attempt.Applied)
	var persisted int64
	require.NoError(t, DB.Model(&BillingFinalization{}).Where("request_id = ?", auth.RequestID).Count(&persisted).Error)
	assert.Zero(t, persisted)

	var reports []walletLockTimingReport
	for line := range strings.SplitSeq(output.String(), "\n") {
		if _, raw, found := strings.Cut(line, "wallet_lock_timing "); found {
			assert.NotContains(t, raw, privateID)
			assert.NotContains(t, raw, rollback.Error())
			var report walletLockTimingReport
			require.NoError(t, common.UnmarshalJsonStr(strings.TrimSpace(raw), &report))
			reports = append(reports, report)
		}
	}
	require.Len(t, reports, 4)
	for i, op := range []string{"reservation", "settlement", "settlement", "reservation"} {
		r := reports[i]
		assert.Equal(t, op, r.Operation)
		assert.Equal(t, i != 3, r.CommitAcknowledged)
		require.NotNil(t, r.PoolAndBeginUS)
		if i == 2 {
			assert.Nil(t, r.WalletLockQueryUS)
			assert.Nil(t, r.AfterLockQueryToTxReturnUS)
		} else {
			require.NotNil(t, r.WalletLockQueryUS)
			require.NotNil(t, r.AfterLockQueryToTxReturnUS)
			assert.GreaterOrEqual(t, r.TxReturnUS, *r.PoolAndBeginUS+*r.WalletLockQueryUS+*r.AfterLockQueryToTxReturnUS)
		}
	}
}
