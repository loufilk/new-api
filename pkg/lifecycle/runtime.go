// Package lifecycle coordinates admission, periodic work and bounded shutdown.
// Quiescing is deliberately separate from cancelling in-flight work.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Group struct {
	mu             sync.Mutex
	schedule       context.Context
	cancelSchedule context.CancelFunc
	work           context.Context
	cancelWork     context.CancelFunc
	stopping       bool
	active         int
	done           chan struct{}
	failure        chan error
	err            error
}

func NewGroup() *Group {
	schedule, cancelSchedule := context.WithCancel(context.Background())
	work, cancelWork := context.WithCancel(context.Background())
	return &Group{schedule: schedule, cancelSchedule: cancelSchedule, work: work, cancelWork: cancelWork, done: make(chan struct{}), failure: make(chan error, 1)}
}

// Go must be called before BeginDrain. A worker stops scheduling when schedule
// is cancelled, but finishes any accepted work using work. ForceCancel is the
// only operation that cancels that second context.
func (g *Group) Go(name string, fn func(schedule, work context.Context)) bool {
	return g.Submit(name, func(run func()) { go run() }, fn)
}

// Submit retains an existing executor while counting work at admission time,
// before it can wait in a pool queue. The executor must schedule fn exactly once.
func (g *Group) Submit(name string, executor func(func()), fn func(schedule, work context.Context)) bool {
	g.mu.Lock()
	if g.stopping {
		g.mu.Unlock()
		return false
	}
	g.active++
	g.mu.Unlock()
	executor(func() {
		defer func() {
			panicValue := recover()
			g.mu.Lock()
			defer g.mu.Unlock()
			if panicValue != nil {
				// Do not include arbitrary panic values: they may contain secrets.
				err := fmt.Errorf("background worker %s panicked", name)
				g.err = errors.Join(g.err, err)
				select {
				case g.failure <- err:
				default:
				}
			}
			g.active--
			if g.stopping && g.active == 0 {
				close(g.done)
			}
		}()
		fn(g.schedule, g.work)
	})
	return true
}

func (g *Group) Failures() <-chan error { return g.failure }

func (g *Group) BeginDrain() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping {
		return
	}
	g.stopping = true
	g.cancelSchedule()
	if g.active == 0 {
		close(g.done)
	}
}

func (g *Group) ForceCancel() { g.cancelWork() }

func (g *Group) Wait(ctx context.Context) error {
	select {
	case <-g.done:
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.err
	case <-ctx.Done():
		// Both channels can be ready at the deadline. Prefer proven completion
		// so a finished group does not hide its worker failure behind ctx.Err().
		select {
		case <-g.done:
			g.mu.Lock()
			defer g.mu.Unlock()
			return g.err
		default:
			return ctx.Err()
		}
	}
}

// Periodic never starts a new iteration after quiescing. The callback's work
// context survives quiescing so transactions can commit or refund normally.
func Periodic(schedule, work context.Context, interval func() time.Duration, immediate bool, fn func(context.Context)) {
	if immediate && schedule.Err() == nil {
		fn(work)
	}
	for schedule.Err() == nil {
		delay := interval()
		if delay <= 0 {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-schedule.Done():
			timer.Stop()
			return
		case <-timer.C:
			if schedule.Err() != nil {
				return
			}
			fn(work)
		}
	}
}

// Gate counts handlers, including hijacked WebSocket handlers, without wrapping
// ResponseWriter and hiding optional net/http interfaces such as Hijacker.
// A hijacked connection is drained only when its handler returns.
type Gate struct {
	mu       sync.Mutex
	draining bool
	active   int
	done     chan struct{}
}

func NewGate() *Gate { return &Gate{done: make(chan struct{})} }

func (g *Gate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		if g.draining {
			g.mu.Unlock()
			w.Header().Set("Retry-After", "1")
			http.Error(w, "instance is draining", http.StatusServiceUnavailable)
			return
		}
		g.active++
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.active--
			if g.draining && g.active == 0 {
				close(g.done)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (g *Gate) BeginDrain() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.draining {
		return
	}
	g.draining = true
	if g.active == 0 {
		close(g.done)
	}
}

func (g *Gate) Wait(ctx context.Context) error {
	select {
	case <-g.done:
		return nil
	case <-ctx.Done():
		// A completed drain takes precedence when the deadline is ready too.
		select {
		case <-g.done:
			return nil
		default:
			return ctx.Err()
		}
	}
}
