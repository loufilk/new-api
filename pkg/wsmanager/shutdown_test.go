package wsmanager

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestShutdownJoinsCallbacksAndRejectsLateConnections(t *testing.T) {
	mu.Lock()
	oldRegistry, oldStopping := registry, shuttingDown
	registry = make(map[int]map[uint64]*entry)
	shuttingDown = false
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); registry, shuttingDown = oldRegistry, oldStopping; mu.Unlock() })
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	allowClose := func() { once.Do(func() { close(release) }) }
	t.Cleanup(allowClose)
	Register(10, KindResponses, func(reason string) {
		if reason != "server shutting down" {
			t.Errorf("unexpected reason %s", reason)
		}
		close(entered)
		<-release
	})
	go func() { defer close(done); Shutdown() }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not close socket")
	}
	select {
	case <-done:
		t.Fatal("shutdown returned before callback finished")
	default:
	}
	var late atomic.Int32
	unregister := Register(20, KindRealtime, func(string) { late.Add(1) })
	unregister()
	if late.Load() != 1 {
		t.Fatal("late connection was not closed")
	}
	allowClose()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not join socket close")
	}
	if n := CloseChannels([]int{10, 20}, "test"); n != 0 {
		t.Fatalf("shutdown retained %d connections", n)
	}
	Shutdown() // Repeated shutdown must not re-close a registered connection.
	if late.Load() != 1 {
		t.Fatal("late connection closed twice")
	}
}

func TestShutdownCallbackPanicDoesNotLeakOtherCloses(t *testing.T) {
	mu.Lock()
	oldRegistry, oldStopping := registry, shuttingDown
	registry = make(map[int]map[uint64]*entry)
	shuttingDown = false
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); registry, shuttingDown = oldRegistry, oldStopping; mu.Unlock() })
	var closed atomic.Bool
	Register(10, KindResponses, func(string) { panic("not-for-client") })
	Register(20, KindResponses, func(string) { closed.Store(true) })
	Shutdown()
	if !closed.Load() {
		t.Fatal("another callback was lost after panic")
	}
}
