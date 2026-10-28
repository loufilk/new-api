package imagegen

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageStoreLimitsExpiryAndOwnerIsolation(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store := AsyncImageStore{Client: client}
	ctx := context.Background()
	job, err := store.Create(ctx, 1)
	require.NoError(t, err)
	_, err = store.Create(ctx, 1)
	require.ErrorIs(t, err, ErrAsyncImageBusy)
	_, err = store.Get(ctx, 2, job.ID)
	require.ErrorIs(t, err, redis.Nil)
	job.Status = "succeeded"
	job.Result = []byte(`{"data":[{"url":"https://example.com/image.png"}]}`)
	require.NoError(t, store.Finish(1, job))
	_, err = store.Create(ctx, 1)
	require.NoError(t, err, "finished job releases user admission")
	read, err := store.Get(ctx, 1, job.ID)
	require.NoError(t, err)
	assert.Equal(t, "succeeded", read.Status)
	assert.JSONEq(t, string(job.Result), string(read.Result))
	server.FastForward(AsyncImageTTL + time.Second)
	_, err = store.Get(ctx, 1, job.ID)
	require.ErrorIs(t, err, redis.Nil)
}

func TestAsyncImageStoreBoundsRetainedResultsAndMarksInterruptedWorker(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store := AsyncImageStore{Client: client}
	ctx := context.Background()
	var first *AsyncImageTask
	for user := 1; user <= 32; user++ {
		job, err := store.Create(ctx, user)
		require.NoError(t, err)
		if user == 1 {
			first = job
		}
	}
	_, err := store.Create(ctx, 33)
	require.ErrorIs(t, err, ErrAsyncImageBusy)
	first.Deadline = time.Now().Add(-time.Minute).Unix()
	require.NoError(t, store.Finish(1, first))
	read, err := store.Get(ctx, 1, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", read.Status)
	assert.Equal(t, 504, read.HTTPStatus)
	assert.Contains(t, string(read.Result), "async_image_interrupted")
}
