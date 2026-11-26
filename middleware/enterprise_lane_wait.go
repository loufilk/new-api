//go:build enterprise_lane_lab

package middleware

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	admissionBusy         = "enterprise_lane_admission_busy"
	admissionQueueFull    = "enterprise_lane_admission_queue_full"
	admissionQueueTimeout = "enterprise_lane_admission_wait_timeout"
)

type enterpriseLaneWaitConfig struct {
	budget         time.Duration
	total, perLane int
}

func parseEnterpriseLaneWait(lanes, activeGlobal int, ms, total, perLane string) (enterpriseLaneWaitConfig, error) {
	if ms == "" && total == "" && perLane == "" {
		return enterpriseLaneWaitConfig{}, nil
	}
	m, me := strconv.ParseUint(ms, 10, 8)
	g, ge := strconv.ParseUint(total, 10, 8)
	l, le := strconv.ParseUint(perLane, 10, 8)
	if lanes < 1 || lanes > model.EnterpriseLaneGatewayMaxLanes || activeGlobal <= 0 ||
		me != nil || ge != nil || le != nil || m < 1 || m > 50 || g < 1 || g > 64 || l < 1 || l > 4 ||
		l > g || g > uint64(lanes)*l {
		return enterpriseLaneWaitConfig{}, errors.New("enterprise lane queue requires active admission limits and explicit bounded wait/global/per-lane queue settings")
	}
	return enterpriseLaneWaitConfig{budget: time.Duration(m) * time.Millisecond, total: int(g), perLane: int(l)}, nil
}

type enterpriseLaneWaitState uint8

const (
	laneWaiting enterpriseLaneWaitState = iota
	laneGranted
	laneElapsed
	laneCanceled
)

type enterpriseLaneWaiter struct {
	ctx            context.Context
	started, until time.Time
	slot           int
	ready          chan struct{}
	state          enterpriseLaneWaitState // Protected by the gate mutex.
}

type enterpriseLaneWaitCounters struct {
	enqueued, queuedLane, queuedGlobal, queuedFIFO, queueRejected, fullLane, fullGlobal, granted, timeout, canceled atomic.Uint64
	waitUS, waitMaxUS                                                                                               atomic.Uint64
}

// Remove under the gate mutex, retaining arrival order.
func (a *enterpriseLaneAdmission) removeWaiter(i int) {
	w := a.waiters[i]
	copy(a.waiters[i:], a.waiters[i+1:])
	a.waiters[len(a.waiters)-1] = nil
	a.waiters = a.waiters[:len(a.waiters)-1]
	a.pending[w.slot]--
}

// Bounded integer gauges are maintained while holding the gate mutex;
// formatting and emissions happen only from the detached snapshot.
func (a *enterpriseLaneAdmission) recordActiveHigh(slot int) {
	a.activeHigh = max(a.activeHigh, a.active)
	a.laneActiveHigh[slot] = max(a.laneActiveHigh[slot], a.lanes[slot])
}

// Grant the oldest eligible waiter under the mutex. A saturated lane never
// blocks unrelated lanes. No SQL, logging, timers or waits in this function.
func (a *enterpriseLaneAdmission) serveWaiters(now time.Time) {
	for i := 0; i < len(a.waiters); {
		w := a.waiters[i]
		switch {
		case w.ctx.Err() != nil:
			w.state = laneCanceled
		case !now.Before(w.until):
			w.state = laneElapsed
		case a.active < a.globalLimit && a.lanes[w.slot] < a.laneLimit:
			w.state = laneGranted
			a.active++
			a.lanes[w.slot]++
			a.recordActiveHigh(w.slot)
		default:
			i++
			continue
		}
		a.removeWaiter(i)
		close(w.ready)
	}
}

func (a *enterpriseLaneAdmission) acquireWithWait(ctx context.Context, slot int) (*enterpriseLaneAdmissionPermit, string) {
	if slot < 0 || slot >= len(a.lanes) || ctx.Err() != nil {
		return nil, admissionBusy
	}
	a.mu.Lock()
	if len(a.waiters) > 0 {
		a.serveWaiters(time.Now())
	}
	if a.active < a.globalLimit && a.lanes[slot] < a.laneLimit && a.pending[slot] == 0 {
		a.active++
		a.lanes[slot]++
		a.recordActiveHigh(slot)
		a.mu.Unlock()
		return &enterpriseLaneAdmissionPermit{gate: a, lane: slot}, ""
	}
	stats := &a.waitStats[slot]
	laneFull, globalFull, fifoBlocked := a.lanes[slot] >= a.laneLimit, a.active >= a.globalLimit, a.pending[slot] > 0
	laneQueueFull, globalQueueFull := a.pending[slot] >= a.wait.perLane, len(a.waiters) >= a.wait.total
	if laneQueueFull || globalQueueFull {
		a.mu.Unlock()
		stats.queueRejected.Add(1)
		if laneQueueFull {
			stats.fullLane.Add(1)
		}
		if globalQueueFull {
			stats.fullGlobal.Add(1)
		}
		return nil, admissionQueueFull
	}
	starting := time.Now()
	w := &enterpriseLaneWaiter{ctx: ctx, slot: slot, started: starting, until: starting.Add(a.wait.budget), ready: make(chan struct{})}
	a.waiters = append(a.waiters, w)
	a.pending[slot]++
	a.pendingHigh = max(a.pendingHigh, len(a.waiters))
	a.lanePendingHigh[slot] = max(a.lanePendingHigh[slot], a.pending[slot])
	a.mu.Unlock()
	stats.enqueued.Add(1)
	if laneFull {
		stats.queuedLane.Add(1)
	}
	if globalFull {
		stats.queuedGlobal.Add(1)
	}
	if fifoBlocked {
		stats.queuedFIFO.Add(1)
	}

	timer := time.NewTimer(time.Until(w.until))
	defer timer.Stop()
	select {
	case <-w.ready:
	case <-ctx.Done():
	case <-timer.C:
	}

	state := a.finishWait(w, time.Now)
	us := uint64(time.Since(w.started).Microseconds())
	stats.waitUS.Add(us)
	for before := stats.waitMaxUS.Load(); us > before && !stats.waitMaxUS.CompareAndSwap(before, us); before = stats.waitMaxUS.Load() {
	}
	switch state {
	case laneGranted:
		stats.granted.Add(1)
		return &enterpriseLaneAdmissionPermit{gate: a, lane: slot, waited: true}, ""
	case laneCanceled:
		stats.canceled.Add(1)
		return nil, admissionBusy
	default:
		stats.timeout.Add(1)
		return nil, admissionQueueTimeout
	}
}

// Claim after wake-up. A select can choose grant when cancel/expiry are also
// ready; the final check under the same lock arbitrates and returns capacity.
func (a *enterpriseLaneAdmission) finishWait(w *enterpriseLaneWaiter, clock func() time.Time) enterpriseLaneWaitState {
	a.mu.Lock()
	now := clock()
	state := w.state
	if state == laneWaiting || state == laneGranted {
		switch {
		case w.ctx.Err() != nil:
			state = laneCanceled
		case !now.Before(w.until):
			state = laneElapsed
		}
	}
	if w.state == laneWaiting {
		for i, waiting := range a.waiters {
			if waiting == w {
				a.removeWaiter(i)
				break
			}
		}
	} else if w.state == laneGranted && state != laneGranted {
		a.active--
		a.lanes[w.slot]--
	}
	a.serveWaiters(now)
	a.mu.Unlock()
	return state
}

// Numeric slot totals only; reasons are independent and can overlap.
// No request identity, payload or credential are exposed.
type EnterpriseLaneAdmissionWaitSlot struct {
	Slot          int    `json:"slot"`
	ActiveHigh    int    `json:"active_high"`
	PendingHigh   int    `json:"pending_high"`
	Enqueued      uint64 `json:"enqueued"`
	QueuedLane    uint64 `json:"queued_lane"`
	QueuedGlobal  uint64 `json:"queued_global"`
	QueuedFIFO    uint64 `json:"queued_fifo"`
	QueueRejected uint64 `json:"queue_rejected"`
	FullLane      uint64 `json:"full_lane"`
	FullGlobal    uint64 `json:"full_global"`
	Granted       uint64 `json:"granted"`
	Timeout       uint64 `json:"timeout"`
	Canceled      uint64 `json:"canceled"`
	WaitUS        uint64 `json:"wait_us"`
	WaitMaxUS     uint64 `json:"wait_max_us"`
}

type EnterpriseLaneAdmissionWaitTotals struct {
	Active      int                               `json:"active"`
	Pending     int                               `json:"pending"`
	ActiveHigh  int                               `json:"active_high"`
	PendingHigh int                               `json:"pending_high"`
	Slots       []EnterpriseLaneAdmissionWaitSlot `json:"slots"`
}

func EnterpriseLaneAdmissionWaitSnapshot() *EnterpriseLaneAdmissionWaitTotals {
	c := enterpriseLaneGatewayRuntime.Load()
	if c == nil || c.admission == nil || c.admission.wait.budget == 0 {
		return nil
	}
	a := c.admission
	out := &EnterpriseLaneAdmissionWaitTotals{Slots: make([]EnterpriseLaneAdmissionWaitSlot, len(c.lanes))}
	a.mu.Lock()
	out.Active, out.Pending, out.ActiveHigh, out.PendingHigh = a.active, len(a.waiters), a.activeHigh, a.pendingHigh
	for i := range out.Slots {
		out.Slots[i].ActiveHigh, out.Slots[i].PendingHigh = a.laneActiveHigh[i], a.lanePendingHigh[i]
	}
	a.mu.Unlock()
	for i := range out.Slots {
		s := &a.waitStats[i]
		slot := &out.Slots[i]
		slot.Slot = i
		slot.Enqueued, slot.QueuedLane, slot.QueuedGlobal, slot.QueuedFIFO = s.enqueued.Load(), s.queuedLane.Load(), s.queuedGlobal.Load(), s.queuedFIFO.Load()
		slot.QueueRejected, slot.FullLane, slot.FullGlobal = s.queueRejected.Load(), s.fullLane.Load(), s.fullGlobal.Load()
		slot.Granted, slot.Timeout, slot.Canceled = s.granted.Load(), s.timeout.Load(), s.canceled.Load()
		slot.WaitUS, slot.WaitMaxUS = s.waitUS.Load(), s.waitMaxUS.Load()
	}
	return out
}

func ReportEnterpriseLaneAdmissionWait() {
	if result := EnterpriseLaneAdmissionWaitSnapshot(); result != nil {
		if b, err := common.Marshal(result); err == nil {
			common.SysLog("enterprise_lane_admission_wait " + string(b))
		}
	}
}
