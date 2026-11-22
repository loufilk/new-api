package imagegen

import (
	"context"
	"fmt"
	"strings"
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
	job, err := store.Create(ctx, 1, 1)
	require.NoError(t, err)
	_, err = store.Create(ctx, 1, 1)
	require.ErrorIs(t, err, ErrAsyncImageBusy)
	_, err = store.Get(ctx, 2, job.ID)
	require.ErrorIs(t, err, redis.Nil)
	job.Status = "succeeded"
	job.Result = []byte(`{"data":[{"url":"https://example.com/image.png"}]}`)
	require.NoError(t, store.Finish(1, job))
	_, err = store.Create(ctx, 1, 1)
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
	for user := 1; user <= 26; user++ {
		job, err := store.Create(ctx, user, 1)
		require.NoError(t, err)
		job.Status = "succeeded"
		job.Result = []byte(`{"data":[{"url":"https://example.com/image.png"}]}`)
		require.NoError(t, store.Finish(user, job))
	}
	_, err := store.Create(ctx, 33, 1)
	require.ErrorIs(t, err, ErrAsyncImageBusy)
	server.FastForward(AsyncImageTTL + time.Second)
	first, err := store.Create(ctx, 1, 1)
	require.NoError(t, err)
	first.Deadline = time.Now().Add(-time.Minute).Unix()
	require.NoError(t, store.Finish(1, first))
	read, err := store.Get(ctx, 1, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", read.Status)
	assert.Equal(t, 504, read.HTTPStatus)
	assert.Contains(t, string(read.Result), "async_image_interrupted")
}

func TestAsyncImageStoreReservesBatchBytesBeforeAdmission(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store := AsyncImageStore{Client: client}
	ctx := context.Background()
	batch, err := store.Create(ctx, 1, 4)
	require.NoError(t, err)
	assert.Equal(t, 4*AsyncImageResultPerImage, batch.ResultLimit)
	_, err = store.Create(ctx, 2, 4)
	require.ErrorIs(t, err, ErrAsyncImageBusy, "another instance must not overbook retained bytes")
	_, err = store.Create(ctx, 2, 1)
	require.NoError(t, err)
	_, err = store.Create(ctx, 3, 1)
	require.ErrorIs(t, err, ErrAsyncImageBusy)
	batch.Status = "succeeded"
	batch.Result = []byte(`{"data":[{"url":"https://example.com/image.png"}]}`)
	require.NoError(t, store.Finish(1, batch))
	_, err = store.Create(ctx, 3, 3)
	require.NoError(t, err, "completion releases unused reservation, not the retained result")
	read, err := store.Get(ctx, 1, batch.ID)
	require.NoError(t, err)
	assert.JSONEq(t, string(batch.Result), string(read.Result))
	server.FastForward(AsyncImageTTL + time.Second)
	_, err = store.Create(ctx, 4, 4)
	require.NoError(t, err, "expired results must release their byte reservation")
	for _, count := range []int{-1, 0, 5} {
		_, err = store.Create(ctx, 10+count, count)
		require.Error(t, err, "unsupported batch count must fail before admission")
	}
}

func TestAsyncImageStoreLegacyAdmissionCannotOverbookNewBatch(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store := AsyncImageStore{Client: client}
	ctx := context.Background()
	batch, err := store.Create(ctx, 1, 4)
	require.NoError(t, err)
	// This is the pre-upgrade admission script: a rolling old worker only
	// knows the 32-member bound and must still see all reserved capacity.
	legacy := redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if redis.call('ZCARD', KEYS[1]) >= 32 or redis.call('EXISTS', KEYS[2]) == 1 then return 0 end
redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[4])
redis.call('SET', KEYS[3], ARGV[5], 'PX', ARGV[4])
redis.call('ZADD', KEYS[1], ARGV[2], KEYS[3])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return 1`)
	for i := range 8 {
		id := fmt.Sprintf("%048d", i+100)
		accepted, err := legacy.Run(ctx, client, []string{"image-async:{tasks}:index", fmt.Sprintf("image-async:{tasks}:active:%d", i+100), asyncImageKey(i+100, id)}, time.Now().UnixMilli(), time.Now().Add(AsyncImageTTL).UnixMilli(), id, AsyncImageTTL.Milliseconds(), `{"status":"running"}`).Int()
		require.NoError(t, err)
		if i < 7 {
			assert.Equal(t, 1, accepted)
		} else {
			assert.Zero(t, accepted, "old workers must see the weighted slots")
		}
	}
	assert.EqualValues(t, 32, client.ZCard(ctx, "image-async:{tasks}:index").Val())
	batch.Status, batch.Result = "succeeded", []byte(`{"data":[{"url":"https://example.com/image.png"}]}`)
	require.NoError(t, store.Finish(1, batch))
	assert.EqualValues(t, 8, client.ZCard(ctx, "image-async:{tasks}:index").Val())
	_, err = store.Create(ctx, 200, 1)
	require.NoError(t, err, "completed payload keeps one slot and releases unused reservations")
}

func TestAsyncImageStoreSerializedExpansionBecomesTerminalFailure(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store := AsyncImageStore{Client: client}
	ctx := context.Background()
	job, err := store.Create(ctx, 1, 1)
	require.NoError(t, err)
	job.Status = "succeeded"
	job.Result = []byte(`{"revised_prompt":"` + strings.Repeat("<", 9<<20) + `"}`)
	require.Less(t, len(job.Result), job.ResultLimit, "raw JSON fits but HTML escaping expands it beyond the reservation")
	require.NoError(t, store.Finish(1, job))
	read, err := store.Get(ctx, 1, job.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", read.Status)
	assert.Contains(t, string(read.Result), "async_image_result_invalid")
	assert.False(t, server.Exists("image-async:{tasks}:active:1"))
	assert.EqualValues(t, 1, client.ZCard(ctx, "image-async:{tasks}:index").Val())
	job.Result = []byte(`{"data":[{"url":"https://example.com/duplicate.png"}]}`)
	require.NoError(t, store.Finish(1, job))
	unchanged, err := store.Get(ctx, 1, job.ID)
	require.NoError(t, err)
	assert.Equal(t, read, unchanged, "duplicate completion must not replace a terminal result")
	server.FastForward(AsyncImageTTL + time.Second)
	require.NoError(t, store.Finish(1, job))
	_, err = store.Get(ctx, 1, job.ID)
	require.ErrorIs(t, err, redis.Nil, "late completion must not resurrect an expired task")
}
