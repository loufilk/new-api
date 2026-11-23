//go:build enterprise_lane_lab

package middleware

import (
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func parseEnterpriseLaneAdmission(lanes int, rawGlobal, rawPerLane string) (int, int, error) {
	if rawGlobal == "" && rawPerLane == "" {
		return 0, 0, nil
	}
	g, gErr := strconv.ParseUint(rawGlobal, 10, 16)
	l, lErr := strconv.ParseUint(rawPerLane, 10, 16)
	if lanes < 1 || lanes > model.EnterpriseLaneGatewayMaxLanes || gErr != nil || lErr != nil ||
		g == 0 || l == 0 || l > g || g > uint64(lanes)*l {
		return 0, 0, errors.New("enterprise lane admission requires both positive global and per-lane limits within the lane manifest")
	}
	return int(g), int(l), nil
}

// This process-local gate is not quota or a database pool. It bounds only
// new controller admission reaching the shared primary pool. Earlier auth
// queries, worker traffic and already dispatched settlements bypass it.
type enterpriseLaneAdmission struct {
	mu                              sync.Mutex
	globalLimit                     int
	laneLimit                       int
	active                          int
	lanes                           [model.EnterpriseLaneGatewayMaxLanes]int
	wait                            enterpriseLaneWaitConfig
	waiters                         []*enterpriseLaneWaiter
	pending                         [model.EnterpriseLaneGatewayMaxLanes]int
	waitStats                       [model.EnterpriseLaneGatewayMaxLanes]enterpriseLaneWaitCounters
	activeHigh, pendingHigh         int
	laneActiveHigh, lanePendingHigh [model.EnterpriseLaneGatewayMaxLanes]int
}

// Only the acquiring request goroutine may Release. The nil and already
// released forms are no-ops, so panic cleanup can coexist with early release.
type enterpriseLaneAdmissionPermit struct {
	gate   *enterpriseLaneAdmission
	lane   int
	waited bool
}

func (p *enterpriseLaneAdmissionPermit) Waited() bool { return p != nil && p.waited }

func (a *enterpriseLaneAdmission) tryAcquire(slot int) (*enterpriseLaneAdmissionPermit, bool) {
	if slot < 0 || slot >= len(a.lanes) {
		return nil, false
	}
	a.mu.Lock()
	if a.active >= a.globalLimit || a.lanes[slot] >= a.laneLimit {
		a.mu.Unlock()
		return nil, false
	}
	a.active++
	a.lanes[slot]++
	if a.wait.budget > 0 {
		a.recordActiveHigh(slot)
	}
	a.mu.Unlock()
	return &enterpriseLaneAdmissionPermit{gate: a, lane: slot}, true
}

func (p *enterpriseLaneAdmissionPermit) Release() {
	if p == nil || p.gate == nil {
		return
	}
	a, slot := p.gate, p.lane
	p.gate = nil
	a.mu.Lock()
	a.active--
	a.lanes[slot]--
	if len(a.waiters) > 0 {
		a.serveWaiters(time.Now())
	}
	a.mu.Unlock()
}

// AcquireEnterpriseLaneAdmission may wait only with explicit lab opt-in. It
// never connects to a database or changes lane. An error code is terminal.
func AcquireEnterpriseLaneAdmission(c *gin.Context) (*enterpriseLaneAdmissionPermit, string) {
	auth, ok := common.GetContextKeyType[enterpriseLaneGatewayAuthorization](c, enterpriseLaneGatewayContextKey)
	if !ok {
		return nil, admissionBusy
	}
	if auth.admission == nil {
		return nil, "" // original behavior unless explicitly opted in at launch
	}
	if c.Request.Context().Err() != nil {
		return nil, admissionBusy
	}
	if auth.admission.wait.budget > 0 {
		return auth.admission.acquireWithWait(c.Request.Context(), auth.laneSlot)
	}
	if p, ok := auth.admission.tryAcquire(auth.laneSlot); ok {
		return p, ""
	}
	return nil, admissionBusy
}
