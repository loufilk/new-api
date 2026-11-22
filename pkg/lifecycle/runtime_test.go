package lifecycle

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQuiescePreservesAcceptedWorkUntilCompletion(t *testing.T) {
	group := NewGroup()
	entered, finish := make(chan context.Context, 1), make(chan struct{})
	if !group.Go("transaction", func(schedule, work context.Context) {
		Periodic(schedule, work, func() time.Duration { return time.Hour }, true, func(ctx context.Context) { entered <- ctx; <-finish })
	}) {
		t.Fatal("worker rejected")
	}
	work := <-entered
	group.BeginDrain()
	group.BeginDrain()
	if work.Err() != nil {
		t.Fatal("quiescing cancelled accepted work")
	}
	if group.Go("late", func(context.Context, context.Context) {}) {
		t.Fatal("accepted new worker after drain")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if group.Wait(ctx) == nil {
		t.Fatal("reported drain before accepted work completed")
	}
	close(finish)
	wait, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := group.Wait(wait); err != nil {
		t.Fatal(err)
	}
}

func TestForceCancellationIsSeparateFromGracefulDrain(t *testing.T) {
	group := NewGroup()
	entered := make(chan struct{})
	group.Go("blocked", func(_, work context.Context) { close(entered); <-work.Done() })
	<-entered
	group.BeginDrain()
	group.ForceCancel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := group.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerPanicIsReportedAndDoesNotLeakTheJoin(t *testing.T) {
	group := NewGroup()
	group.Go("test", func(context.Context, context.Context) { panic("must-not-be-disclosed") })
	err := <-group.Failures()
	if err.Error() != "background worker test panicked" {
		t.Fatal(err)
	}
	group.BeginDrain()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if group.Wait(ctx) == nil {
		t.Fatal("worker failure was lost")
	}
}

func TestGateRejectsNewRequestsWithoutCancellingExistingOnes(t *testing.T) {
	gate := NewGate()
	entered, finish, exited := make(chan context.Context, 1), make(chan struct{}), make(chan struct{})
	handler := gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- r.Context(); <-finish; w.WriteHeader(204) }))
	go func() {
		defer close(exited)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	requestCtx := <-entered
	gate.BeginDrain()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatalf("late status %d", w.Code)
	}
	if requestCtx.Err() != nil {
		t.Fatal("existing request cancelled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if gate.Wait(ctx) == nil {
		t.Fatal("early drain")
	}
	close(finish)
	<-exited
	if err := gate.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestGateTracksHijackedHandlerRatherThanServerConnectionState(t *testing.T) {
	gate := NewGate()
	hijacked, finish := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		close(hijacked)
		<-finish
	})))
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	<-hijacked
	gate.BeginDrain()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if gate.Wait(ctx) == nil {
		t.Fatal("hijacked handler disappeared from drain accounting")
	}
	close(finish)
	wait, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := gate.Wait(wait); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitCountsQueuedWorkBeforeTheExecutorStartsIt(t *testing.T) {
	group := NewGroup()
	queued := make(chan func(), 1)
	ran := false
	if !group.Submit("queued-refund", func(run func()) { queued <- run }, func(_, work context.Context) {
		if work.Err() != nil {
			t.Error("queued work lost its completion context")
		}
		ran = true
	}) {
		t.Fatal("work rejected")
	}
	group.BeginDrain()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if group.Wait(cancelled) == nil {
		t.Fatal("queued work was not counted")
	}
	(<-queued)()
	wait, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := group.Wait(wait); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("queued work did not execute")
	}
}

// Completion and cancellation are both ready here. The result must be based
// on completed work, not the pseudo-random branch chosen by select.
func TestCompletedDrainWinsOverCancelledWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	group := NewGroup()
	group.Submit("completed", func(run func()) { run() }, func(context.Context, context.Context) {})
	group.BeginDrain()
	if err := group.Wait(ctx); err != nil {
		t.Fatalf("completed work reported as interrupted: %v", err)
	}

	gate := NewGate()
	gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	gate.BeginDrain()
	if err := gate.Wait(ctx); err != nil {
		t.Fatalf("completed requests reported as interrupted: %v", err)
	}
}

func TestCompletedWorkerFailureWinsOverCancelledWait(t *testing.T) {
	group := NewGroup()
	group.Submit("failed", func(run func()) { run() }, func(context.Context, context.Context) {
		panic("provider credential must not appear in the error")
	})
	group.BeginDrain()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := group.Wait(ctx); err == nil || err.Error() != "background worker failed panicked" {
		t.Fatalf("lost the sanitized worker failure: %v", err)
	}
}
