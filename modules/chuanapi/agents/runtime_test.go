package agents

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountingWorkerRetriesAndDrainsWithoutExternalPayout(t *testing.T) {
	workers := lifecycle.NewGroup()
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	startAccountingWorker(workers, "synthetic-accounting", time.Millisecond, func(ctx context.Context, limit int) (int, error) {
		assert.Equal(t, 100, limit)
		_, hasDeadline := ctx.Deadline()
		assert.True(t, hasDeadline)
		if calls.Add(1) == 1 {
			return 0, errors.New("synthetic-retry")
		}
		close(entered)
		<-release
		return 1, nil
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("accounting worker did not retry")
	}
	workers.BeginDrain()
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, workers.Wait(ctx))
	assert.EqualValues(t, 2, calls.Load(), "draining joins the in-flight transaction and schedules no further pass")
}
