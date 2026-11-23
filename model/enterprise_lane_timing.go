//go:build enterprise_lane_lab

package model

import (
	"context"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Only the explicitly enabled laboratory route creates samples.
var enterpriseLaneTimingPermille = sync.OnceValue(func() uint64 {
	n, err := strconv.ParseUint(os.Getenv("ENTERPRISE_LANE_LAB_TIMING_SAMPLE_PERMILLE"), 10, 16)
	if err != nil || n > 1000 {
		return 0
	}
	return n
})

func EnterpriseLaneTimingEnabled() bool { return enterpriseLaneTimingPermille() != 0 }

type EnterpriseLaneTimingStage uint8

const (
	EnterpriseLaneTimingPreflight EnterpriseLaneTimingStage = iota
	EnterpriseLaneTimingRecheck
	EnterpriseLaneTimingProviderDo
	EnterpriseLaneTimingFirstOutputAttempt
	EnterpriseLaneTimingUsageConfirmed
	EnterpriseLaneTimingTerminalAttempt
	enterpriseLaneTimingStageCount
)

var enterpriseLaneTimingStageNames = [...]string{
	"preflight_passed", "recheck_passed", "provider_do", "first_output_attempt", "usage_confirmed", "terminal_attempt",
}

type enterpriseLaneTimingOp uint8

const (
	enterpriseLaneTimingReserve enterpriseLaneTimingOp = iota
	enterpriseLaneTimingDispatch
	enterpriseLaneTimingFreeze
	enterpriseLaneTimingSettle
	enterpriseLaneTimingRefund
	enterpriseLaneTimingUnknown
	enterpriseLaneTimingOpCount
)

var enterpriseLaneTimingOpNames = [...]string{"reserve", "dispatch", "freeze_usage", "settle", "refund", "mark_unknown"}

type EnterpriseLaneTiming struct {
	ingress, started, ended time.Time
	status                  int
	marks                   [enterpriseLaneTimingStageCount]time.Time
	tx                      [enterpriseLaneTimingOpCount]enterpriseLaneTxTiming
}

type enterpriseLaneTxTiming struct {
	started, entered, bodyReturned, returned time.Time
	lane, request                            enterpriseLaneLockTiming
	bodyOK, acknowledged                    bool
}

type enterpriseLaneLockTiming struct {
	started, returned time.Time
	acquired          bool
}

type enterpriseLaneTimingContextKey struct{}
type enterpriseLaneTxTimingContextKey struct{}

var enterpriseLaneTimingQueue = sync.OnceValue(func() chan *EnterpriseLaneTiming {
	return make(chan *EnterpriseLaneTiming, 128)
})
var enterpriseLaneTimingDropped atomic.Uint64

func BeginEnterpriseLaneTiming(ingress time.Time) *EnterpriseLaneTiming {
	rate := enterpriseLaneTimingPermille()
	if rate == 0 || (rate != 1000 && rand.Uint64N(1000) >= rate) {
		return nil
	}
	return &EnterpriseLaneTiming{ingress: ingress, started: time.Now()}
}

func (s *EnterpriseLaneTiming) WithContext(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, enterpriseLaneTimingContextKey{}, s)
}

func (s *EnterpriseLaneTiming) Mark(stage EnterpriseLaneTimingStage) {
	if s != nil && stage < enterpriseLaneTimingStageCount && s.marks[stage].IsZero() {
		s.marks[stage] = time.Now()
	}
}

// Finish is called after the handler's SQL transactions have returned. It
// never writes to the logger, waits for the sink, or changes the response.
func (s *EnterpriseLaneTiming) Finish(status int) {
	if s == nil {
		return
	}
	s.status, s.ended = status, time.Now()
	select {
	case enterpriseLaneTimingQueue() <- s:
	default:
		enterpriseLaneTimingDropped.Add(1)
	}
}

func enterpriseLaneTimedTransaction(ctx context.Context, db *gorm.DB, op enterpriseLaneTimingOp, body func(*gorm.DB) error) error {
	if ctx == nil || !EnterpriseLaneTimingEnabled() {
		return db.WithContext(ctx).Transaction(body)
	}
	s, _ := ctx.Value(enterpriseLaneTimingContextKey{}).(*EnterpriseLaneTiming)
	if s == nil {
		return db.WithContext(ctx).Transaction(body)
	}
	// A nested savepoint does not acknowledge a durable COMMIT.
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return db.WithContext(ctx).Transaction(body)
	}
	stamp := &s.tx[op]
	stamp.started = time.Now()
	timed := context.WithValue(ctx, enterpriseLaneTxTimingContextKey{}, stamp)
	err := db.WithContext(timed).Transaction(func(tx *gorm.DB) error {
		stamp.entered = time.Now()
		defer func() { stamp.bodyReturned = time.Now() }()
		err := body(tx)
		stamp.bodyOK = err == nil
		return err
	})
	stamp.returned = time.Now()
	stamp.acknowledged = err == nil
	return err
}

func startEnterpriseLaneLockTiming(tx *gorm.DB, lane bool) *enterpriseLaneLockTiming {
	if !EnterpriseLaneTimingEnabled() || tx.Statement.Context == nil {
		return nil
	}
	stamp, _ := tx.Statement.Context.Value(enterpriseLaneTxTimingContextKey{}).(*enterpriseLaneTxTiming)
	if stamp == nil {
		return nil
	}
	mark := &stamp.request
	if lane {
		mark = &stamp.lane
	}
	mark.started = time.Now()
	return mark
}

func (s *enterpriseLaneLockTiming) finish(acquired bool) {
	if s != nil {
		s.returned, s.acquired = time.Now(), acquired
	}
}

// Report in the sink; durations are Go observations, never claimed as exact
// server wait, row release or durable commit timestamps.
func (s *EnterpriseLaneTiming) report() map[string]any {
	out := map[string]any{
		"status": s.status, "handler_us": s.ended.Sub(s.started).Microseconds(),
		"handler_start_unix_us": s.started.UnixMicro(),
	}
	if !s.ingress.IsZero() {
		out["ingress_to_handler_us"] = s.started.Sub(s.ingress).Microseconds()
	}
	marks := make(map[string]int64)
	for i, at := range s.marks {
		if !at.IsZero() {
			marks[enterpriseLaneTimingStageNames[i]] = at.Sub(s.started).Microseconds()
		}
	}
	out["handler_marks_us"] = marks
	txs := make(map[string]map[string]any)
	for i, t := range s.tx {
		if t.started.IsZero() {
			continue
		}
		tx := map[string]any{"handler_to_start_us": t.started.Sub(s.started).Microseconds()}
		if !t.returned.IsZero() {
			tx["tx_return_us"] = t.returned.Sub(t.started).Microseconds()
			tx["commit_acknowledged"] = t.acknowledged
		}
		if !t.entered.IsZero() {
			tx["pool_and_begin_us"] = t.entered.Sub(t.started).Microseconds()
		}
		if !t.bodyReturned.IsZero() {
			tx["body_us"] = t.bodyReturned.Sub(t.entered).Microseconds()
			tx["body_ok"] = t.bodyOK
			if !t.returned.IsZero() {
				tx["body_return_to_tx_return_us"] = t.returned.Sub(t.bodyReturned).Microseconds()
			}
		}
		if !t.request.returned.IsZero() {
			tx["owner_lock_query_us"] = t.request.returned.Sub(t.request.started).Microseconds()
		}
		if !t.lane.returned.IsZero() {
			tx["lane_lock_query_us"] = t.lane.returned.Sub(t.lane.started).Microseconds()
			tx["lane_lock_acquired"] = t.lane.acquired
			if t.lane.acquired && !t.bodyReturned.IsZero() {
				tx["after_lane_lock_to_body_return_us"] = t.bodyReturned.Sub(t.lane.returned).Microseconds()
			}
			if t.lane.acquired && !t.returned.IsZero() {
				tx["after_lane_lock_to_tx_return_us"] = t.returned.Sub(t.lane.returned).Microseconds()
			}
		}
		txs[enterpriseLaneTimingOpNames[i]] = tx
	}
	out["transactions"] = txs
	return out
}

// RunEnterpriseLaneTiming is a non-financial diagnostic sink, not part of
// the durable accounting drain. Sampling disabled registers no active sink.
func RunEnterpriseLaneTiming(schedule, _ context.Context) {
	if enterpriseLaneTimingPermille() == 0 {
		return
	}
	queue := enterpriseLaneTimingQueue()
	for {
		if schedule.Err() != nil {
			break
		}
		select {
		case <-schedule.Done():
		case sample := <-queue:
			if encoded, err := common.Marshal(sample.report()); err == nil {
				common.SysLog("enterprise_lane_step_timing " + string(encoded))
			}
		}
	}
	if dropped := enterpriseLaneTimingDropped.Load(); dropped != 0 || len(queue) != 0 {
		if encoded, err := common.Marshal(map[string]any{"dropped": dropped, "unflushed": len(queue)}); err == nil {
			common.SysLog("enterprise_lane_step_timing_loss " + string(encoded))
		}
	}
}
