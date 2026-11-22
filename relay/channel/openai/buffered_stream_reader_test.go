package openai

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countedBufferedBody struct {
	io.Reader
	closes atomic.Int32
}

func (b *countedBufferedBody) Close() error { b.closes.Add(1); return nil }

func TestBufferedStreamReaderStopJoinsWatcher(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	body := &countedBufferedBody{Reader: strings.NewReader("buffered")}
	reader := newBufferedStreamReader(ctx, body, time.Hour)
	bytes, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, "buffered", string(bytes))
	reader.stop()
	reader.stop()
	select {
	case <-reader.done:
	default:
		t.Fatal("watcher was not joined")
	}
	cancel()
	assert.Zero(t, body.closes.Load(), "normal completion leaves body ownership with the handler")
}
