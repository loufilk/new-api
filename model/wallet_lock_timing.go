package model

import (
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var walletLockSamplePermille = sync.OnceValue(func() uint64 {
	return parseWalletLockSamplePermille(os.Getenv("WALLET_LOCK_TIMING_SAMPLE_PERMILLE"))
})

func parseWalletLockSamplePermille(raw string) uint64 {
	rate, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 16)
	if err != nil || rate > 1000 {
		return 0
	}
	return rate
}

type walletLockTiming struct {
	operation                      string
	started, entered               time.Time
	lockQueryStarted, lockReturned time.Time
	lockAcquired                   bool
}

type walletLockTimingReport struct {
	Operation                  string `json:"operation"`
	PoolAndBeginUS             *int64 `json:"pool_and_begin_us,omitempty"`
	WalletLockQueryUS          *int64 `json:"wallet_lock_query_us,omitempty"`
	AfterLockQueryToTxReturnUS *int64 `json:"after_lock_query_to_tx_return_us,omitempty"`
	TxReturnUS                 int64  `json:"tx_return_us"`
	CommitAcknowledged         bool   `json:"commit_acknowledged"`
}

func beginWalletLockTiming(db *gorm.DB, source, operation string) *walletLockTiming {
	if source != "wallet" {
		return nil
	}
	rate := walletLockSamplePermille()
	if rate == 0 || db == nil || db.Dialector.Name() != "postgres" || (rate < 1000 && rand.Uint64N(1000) >= rate) {
		return nil
	}
	// A savepoint return does not release the wallet row or acknowledge COMMIT.
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return nil
	}
	return &walletLockTiming{operation: operation, started: time.Now()}
}

func (s *walletLockTiming) enterTransaction() {
	if s != nil {
		s.entered = time.Now()
	}
}

func (s *walletLockTiming) lockFinancialUser(tx *gorm.DB, userID int) error {
	if s == nil {
		return LockFinancialUserTx(tx, userID)
	}
	s.lockQueryStarted = time.Now()
	err := LockFinancialUserTx(tx, userID)
	s.lockReturned = time.Now()
	s.lockAcquired = err == nil
	return err
}

func (s *walletLockTiming) finish(txErr error) {
	if s == nil {
		return
	}
	// Measurements are emitted only after GORM returns from COMMIT/ROLLBACK.
	// No user ID, request ID, SQL text, amounts or error details enter the log.
	report := s.report(time.Now(), txErr == nil)
	if encoded, err := common.Marshal(report); err == nil {
		common.SysLog("wallet_lock_timing " + string(encoded))
	}
}

func (s *walletLockTiming) report(returned time.Time, commitAcknowledged bool) walletLockTimingReport {
	report := walletLockTimingReport{
		Operation:          s.operation,
		TxReturnUS:         returned.Sub(s.started).Microseconds(),
		CommitAcknowledged: commitAcknowledged,
	}
	if !s.entered.IsZero() {
		// Includes pool checkout/connection setup and BEGIN, not pure pool wait.
		us := s.entered.Sub(s.started).Microseconds()
		report.PoolAndBeginUS = &us
	}
	if !s.lockReturned.IsZero() {
		// Wall time of SELECT FOR UPDATE includes server execution, transport
		// and scan in addition to any PostgreSQL lock wait.
		us := s.lockReturned.Sub(s.lockQueryStarted).Microseconds()
		report.WalletLockQueryUS = &us
	}
	if s.lockAcquired {
		// This is a client-side proxy for the remaining lock hold, not the
		// exact server release instant, including after a failed COMMIT ACK.
		us := returned.Sub(s.lockReturned).Microseconds()
		report.AfterLockQueryToTxReturnUS = &us
	}
	return report
}
