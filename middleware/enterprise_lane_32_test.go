//go:build enterprise_lane_lab

package middleware

import (
	"context"
	"fmt"
	"hash/fnv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/require"
)

func TestEnterpriseLaneGateway32SlotReachAndGate(t *testing.T) {
	// Exercise the same selector used after canonical authentication. Verify
	// deterministic slot coverage without depending on stochastic collisions.
	var hit [32]int
	for i := range 1024 {
		id := fmt.Sprintf("fixture-request-%04d", i)
		slot := enterpriseLaneSlot(id, len(hit))
		require.GreaterOrEqual(t, slot, 0)
		require.Less(t, slot, len(hit))
		hit[slot]++
		old := fnv.New32a()
		_, _ = old.Write([]byte(id))
		require.Equal(t, int(old.Sum32()%16), enterpriseLaneSlot(id, 16), "the 16-lane mapping is unchanged")
	}
	for slot, seen := range hit {
		require.Positive(t, seen, "slot %d has no candidate", slot)
	}

	g, l, err := parseEnterpriseLaneAdmission(32, "96", "8")
	require.NoError(t, err)
	require.Equal(t, 96, g)
	require.Equal(t, 8, l)
	_, _, err = parseEnterpriseLaneAdmission(33, "96", "8")
	require.Error(t, err)
	wait, err := parseEnterpriseLaneWait(32, g, "35", "32", "2")
	require.NoError(t, err)
	require.Equal(t, 35*time.Millisecond, wait.budget)
	_, err = parseEnterpriseLaneWait(32, g, "35", "65", "2")
	require.Error(t, err, "queue caps are not doubled with active caps")

	gate := &enterpriseLaneAdmission{globalLimit: g, laneLimit: l, wait: wait}
	var held []*enterpriseLaneAdmissionPermit
	defer func() {
		for _, p := range held {
			p.Release()
		}
	}()
	// All 32 distinct gate buckets are addressable and the 31st/last indexes
	// do not alias lower slots in occupancy and wait statistics.
	for i := 31; i >= 20; i-- {
		for range 8 {
			p, ok := gate.tryAcquire(i)
			require.True(t, ok)
			held = append(held, p)
		}
		_, ok := gate.tryAcquire(i)
		require.False(t, ok, "slot %d may not exceed eight active permits", i)
	}
	for _, slot := range []int{0, 20, 31} {
		_, ok := gate.tryAcquire(slot)
		require.False(t, ok)
	}
	held[0].Release()
	p, ok := gate.tryAcquire(0)
	require.True(t, ok)
	p.Release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, denial := gate.acquireWithWait(ctx, 31)
	require.NotEmpty(t, denial)
	original := enterpriseLaneGatewayRuntime.Load()
	enterpriseLaneGatewayRuntime.Store(&enterpriseLaneGatewayConfig{
		lanes: make([]model.EnterpriseLaneIdentity, 32), admission: gate,
	})
	t.Cleanup(func() { enterpriseLaneGatewayRuntime.Store(original) })
	snapshot := EnterpriseLaneAdmissionWaitSnapshot()
	require.NotNil(t, snapshot)
	require.Equal(t, len(held), snapshot.ActiveHigh)
	require.Len(t, snapshot.Slots, 32)
	require.Equal(t, 1, snapshot.Slots[0].ActiveHigh)
	require.Equal(t, 8, snapshot.Slots[31].ActiveHigh)
	require.Equal(t, 0, snapshot.Pending)
}
