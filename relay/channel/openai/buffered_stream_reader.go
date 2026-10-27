package openai

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

var errBufferedStreamIdle = errors.New("upstream buffered stream idle timeout")

// bufferedStreamReader bounds a blocked read without writing SSE headers to a
// non-streaming client. Closing the upstream body interrupts net/http's read;
// stop joins the only watcher before the caller releases its request state.
type bufferedStreamReader struct {
	body     io.ReadCloser
	mu       sync.Mutex
	lastRead time.Time
	err      error
	stopCh   chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newBufferedStreamReader(ctx context.Context, body io.ReadCloser, idle time.Duration) *bufferedStreamReader {
	r := &bufferedStreamReader{body: body, lastRead: time.Now(), stopCh: make(chan struct{}), done: make(chan struct{})}
	go r.watch(ctx, idle)
	return r
}

func (r *bufferedStreamReader) watch(ctx context.Context, idle time.Duration) {
	defer close(r.done)
	var timer *time.Timer
	var deadline <-chan time.Time
	if idle > 0 {
		timer = time.NewTimer(idle)
		deadline = timer.C
		defer timer.Stop()
	}
	for {
		var cause error
		select {
		case <-r.stopCh:
			return
		case <-ctx.Done():
			cause = ctx.Err()
		case <-deadline:
			r.mu.Lock()
			remaining := idle - time.Since(r.lastRead)
			if remaining <= 0 {
				r.err = errBufferedStreamIdle
			}
			r.mu.Unlock()
			if remaining > 0 {
				timer.Reset(remaining)
				continue
			}
			cause = errBufferedStreamIdle
		}
		r.mu.Lock()
		r.err = cause
		r.mu.Unlock()
		_ = r.body.Close()
		return
	}
}

func (r *bufferedStreamReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > 0 {
		r.lastRead = time.Now()
	}
	if r.err != nil {
		return n, r.err
	}
	return n, err
}

func (r *bufferedStreamReader) stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	<-r.done
}
