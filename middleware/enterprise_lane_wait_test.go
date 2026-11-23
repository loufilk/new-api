//go:build enterprise_lane_lab

package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEnterpriseLaneAdmissionWaitManifest(t *testing.T) {
	off, err := parseEnterpriseLaneWait(16, 48, "", "", "")
	require.NoError(t, err)
	require.Zero(t, off)
	on, err := parseEnterpriseLaneWait(16, 48, "35", "32", "2")
	require.NoError(t, err)
	require.Equal(t, enterpriseLaneWaitConfig{budget: 35 * time.Millisecond, total: 32, perLane: 2}, on)
	for _, vals := range [][3]string{{"", "32", "2"}, {"51", "32", "2"}, {"20", "65", "4"}, {"20", "20", "5"}, {"20", "33", "2"}, {"20", "1", "2"}} {
		_, err := parseEnterpriseLaneWait(16, 48, vals[0], vals[1], vals[2])
		require.Error(t, err)
	}
	_, err = parseEnterpriseLaneWait(16, 0, "35", "32", "2")
	require.Error(t, err)
}

type enterpriseLaneWaitResult struct {
	permit *enterpriseLaneAdmissionPermit
	denial string
}

// Each acquiring goroutine owns and releases its own permit.
func testLaneWait(t *testing.T, a *enterpriseLaneAdmission, ctx context.Context, lane int) (chan enterpriseLaneWaitResult, func()) {
	t.Helper()
	ch := make(chan enterpriseLaneWaitResult, 1)
	release := make(chan struct{})
	go func() {
		p, denial := a.acquireWithWait(ctx, lane)
		ch <- enterpriseLaneWaitResult{permit: p, denial: denial}
		if p != nil {
			<-release
			p.Release()
		}
	}()
	var released bool
	done := func() {
		if !released {
			released = true
			close(release)
		}
	}
	t.Cleanup(done)
	return ch, done
}

func TestEnterpriseLaneAdmissionWaitFIFOAndNoCrossLaneHeadOfLine(t *testing.T) {
	a := &enterpriseLaneAdmission{globalLimit: 2, laneLimit: 1, wait: enterpriseLaneWaitConfig{budget: time.Second, total: 2, perLane: 2}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	active, _ := a.tryAcquire(0)
	defer active.Release()
	first, releaseFirst := testLaneWait(t, a, ctx, 0)
	require.Eventually(t, func() bool { return a.waitStats[0].queuedLane.Load() == 1 }, time.Second, time.Millisecond)
	second, _ := testLaneWait(t, a, ctx, 0)
	require.Eventually(t, func() bool { return a.waitStats[0].queuedLane.Load() == 2 }, time.Second, time.Millisecond)
	p, denial := a.acquireWithWait(ctx, 1)
	require.Empty(t, denial)
	require.False(t, p.Waited(), "unrelated lane must take spare global capacity")
	defer p.Release()
	blocked, why := a.acquireWithWait(ctx, 0)
	require.Nil(t, blocked)
	require.Equal(t, admissionQueueFull, why)
	require.EqualValues(t, 1, a.waitStats[0].fullLane.Load())
	require.EqualValues(t, 1, a.waitStats[0].fullGlobal.Load(), "simultaneous capacity flags must both count")
	require.EqualValues(t, 1, a.waitStats[0].queueRejected.Load(), "outcome still counted once")
	require.EqualValues(t, 1, a.waitStats[0].queuedFIFO.Load())
	a.mu.Lock()
	highActive, highPending, laneHigh, lanePending := a.activeHigh, a.pendingHigh, a.laneActiveHigh[0], a.lanePendingHigh[0]
	a.mu.Unlock()
	require.Equal(t, 2, highActive)
	require.Equal(t, 2, highPending)
	require.Equal(t, 1, laneHigh)
	require.Equal(t, 2, lanePending)
	blocked, why = a.acquireWithWait(ctx, 2)
	require.Nil(t, blocked)
	require.Equal(t, admissionQueueFull, why)
	require.EqualValues(t, 1, a.waitStats[2].fullGlobal.Load())
	active.Release()
	select {
	case result := <-first:
		require.Empty(t, result.denial)
		require.True(t, result.permit.Waited())
	case <-time.After(time.Second):
		t.Fatal("first same-lane request not granted")
	}
	select {
	case <-second:
		t.Fatal("second bypassed FIFO")
	default:
	}
	releaseFirst()
	select {
	case result := <-second:
		require.Empty(t, result.denial)
		require.True(t, result.permit.Waited())
	case <-time.After(time.Second):
		t.Fatal("second same-lane request not granted")
	}
}

func TestEnterpriseLaneAdmissionWaitTimeoutAndCancellation(t *testing.T) {
	t.Run("hard timer", func(t *testing.T) {
		a := &enterpriseLaneAdmission{globalLimit: 1, laneLimit: 1, wait: enterpriseLaneWaitConfig{budget: 3 * time.Millisecond, total: 1, perLane: 1}}
		p, _ := a.tryAcquire(0)
		defer p.Release()
		wait, why := a.acquireWithWait(t.Context(), 1)
		require.Nil(t, wait)
		require.Equal(t, admissionQueueTimeout, why)
		require.EqualValues(t, 1, a.waitStats[1].queuedGlobal.Load())
		require.EqualValues(t, 1, a.waitStats[1].timeout.Load())
		require.Positive(t, a.waitStats[1].waitUS.Load())
		require.Zero(t, a.pending[1])
		require.Equal(t, 1, a.active)
	})
	t.Run("request cancellation", func(t *testing.T) {
		a := &enterpriseLaneAdmission{globalLimit: 1, laneLimit: 1, wait: enterpriseLaneWaitConfig{budget: time.Second, total: 1, perLane: 1}}
		p, _ := a.tryAcquire(0)
		defer p.Release()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ch, _ := testLaneWait(t, a, ctx, 1)
		require.Eventually(t, func() bool { return a.waitStats[1].queuedGlobal.Load() == 1 }, time.Second, time.Millisecond)
		cancel()
		select {
		case result := <-ch:
			require.Nil(t, result.permit)
			require.Equal(t, admissionBusy, result.denial)
		case <-time.After(time.Second):
			t.Fatal("cancellation did not release")
		}
		require.EqualValues(t, 1, a.waitStats[1].canceled.Load())
		require.Zero(t, a.pending[1])
		require.Equal(t, 1, a.active)
	})
}

// Deterministic both-ready race: physically grant first, then make the
// opposing signal ready before final grant decision, without sleeps.
func TestEnterpriseLaneAdmissionWaitGrantLosesToExpiryOrCancellation(t *testing.T) {
	for _, tc := range []struct {
		label  string
		cancel bool
		reason enterpriseLaneWaitState
	}{{"deadline", false, laneElapsed}, {"canceled", true, laneCanceled}} {
		t.Run(tc.label, func(t *testing.T) {
			now := time.Now()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			a := &enterpriseLaneAdmission{globalLimit: 2, laneLimit: 1}
			w := &enterpriseLaneWaiter{ctx: ctx, slot: 0, until: now.Add(time.Millisecond), ready: make(chan struct{})}
			next := &enterpriseLaneWaiter{ctx: t.Context(), slot: 0, until: now.Add(time.Second), ready: make(chan struct{})}
			a.waiters = []*enterpriseLaneWaiter{w, next}
			a.pending[0] = 2
			a.mu.Lock()
			a.serveWaiters(now)
			a.mu.Unlock()
			require.Equal(t, laneGranted, w.state)
			decision := w.until
			if tc.cancel {
				cancel()
				decision = now
			}
			require.Equal(t, tc.reason, a.finishWait(w, func() time.Time { return decision }))
			require.Equal(t, laneGranted, a.finishWait(next, func() time.Time { return decision }))
			require.Equal(t, 1, a.active)
			require.Zero(t, a.pending[0])
			other, _ := a.tryAcquire(1)
			require.NotNil(t, other)
			other.Release()
			held := &enterpriseLaneAdmissionPermit{gate: a, lane: 0}
			held.Release()
			require.Zero(t, a.active)
		})
	}
}
