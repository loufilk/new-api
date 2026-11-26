package imagegen

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Lose exactly one successful Lua reply *after* Redis has applied the script.
type loseAdmissionReply struct{ lost atomic.Bool }

func (h *loseAdmissionReply) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *loseAdmissionReply) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *loseAdmissionReply) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if err == nil && (cmd.Name() == "eval" || cmd.Name() == "evalsha") && h.lost.CompareAndSwap(false, true) {
			err = errors.New("fixture Redis ACK lost")
			cmd.SetErr(err)
		}
		return err
	}
}
func TestImageOperationLostRedisReplyDoesNotAuthorizeDispatch(t *testing.T) {
	m := miniredis.NewMiniRedis()
	t.Cleanup(m.Close)
	now := time.Now()
	m.SetTime(now)
	c := redis.NewClient(&redis.Options{Dialer: func(context.Context, string, string) (net.Conn, error) { return m.Dial() }})
	t.Cleanup(func() { _ = c.Close() })
	h := &loseAdmissionReply{}
	c.AddHook(h)
	s := AsyncImageStore{Client: c}
	ctx := context.Background()
	id, err := s.issueOperation(ctx, 4)
	require.NoError(t, err)
	task, dispatch, err := s.admit(ctx, 4, 1, id, "bound", true)
	require.ErrorContains(t, err, "fixture Redis ACK lost")
	require.Nil(t, task)
	require.False(t, dispatch)
	task, err = s.getOperation(ctx, 4, id)
	require.NoError(t, err)
	require.Equal(t, "running", task.Status)
	task, dispatch, err = s.admit(ctx, 4, 1, id, "bound", true)
	require.NoError(t, err)
	require.Equal(t, id, task.ID)
	require.False(t, dispatch)
	_, _, err = s.admit(ctx, 4, 1, id, "other request", true)
	require.ErrorIs(t, err, errOperationConflict)
	m.FastForward(AsyncImageTTL + time.Second)
	m.SetTime(now.Add(AsyncImageTTL + time.Second))
	_, err = s.getOperation(ctx, 4, id)
	require.ErrorIs(t, err, errOperationExpired)
	m.FastForward(asyncOperationTTL)
	m.SetTime(now.Add(asyncOperationTTL + AsyncImageTTL + time.Second))
	_, err = s.getOperation(ctx, 4, id)
	require.ErrorIs(t, err, errOperationMissing)
	_, dispatch, err = s.admit(ctx, 4, 1, id, "bound", true)
	require.False(t, dispatch)
	require.ErrorIs(t, err, errOperationExpired)
}

// Only a real owner-bound server ticket may race admission. Forging a
// timestamp (including days ahead) never becomes consent as time advances.
func TestImageOperationServerClockAndOwner(t *testing.T) {
	m := miniredis.NewMiniRedis()
	t.Cleanup(m.Close)
	now := time.Now().Truncate(time.Millisecond)
	m.SetTime(now)
	c := redis.NewClient(&redis.Options{Dialer: func(context.Context, string, string) (net.Conn, error) { return m.Dial() }})
	t.Cleanup(func() { _ = c.Close() })
	s := AsyncImageStore{Client: c}
	ctx := context.Background()
	issued, err := s.issueOperation(ctx, 4)
	require.NoError(t, err)
	require.Len(t, issued, 48)
	stamp, err := operationTime(issued)
	require.NoError(t, err)
	require.Equal(t, now.UnixMilli(), stamp)
	_, err = s.getOperation(ctx, 4, issued)
	require.ErrorIs(t, err, errOperationPending)
	_, err = s.getOperation(ctx, 9, issued)
	require.ErrorIs(t, err, errOperationMissing)
	_, created, err := s.admit(ctx, 9, 1, issued, "bound", true)
	require.ErrorIs(t, err, errOperationExpired)
	require.False(t, created)

	// A clock-ahead browser used to create this 48-character ID.
	fakeFuture := now.Add(72 * time.Hour)
	future := fmt.Sprintf("%012x%s", fakeFuture.UnixMilli(), strings.Repeat("9", 36))
	_, err = s.getOperation(ctx, 4, future)
	require.ErrorIs(t, err, errOperationMissing)
	_, created, err = s.admit(ctx, 4, 1, future, "bound", true)
	require.ErrorIs(t, err, errOperationExpired)
	require.False(t, created)
	m.SetTime(fakeFuture.Add(time.Second))
	_, err = s.getOperation(ctx, 4, future)
	require.ErrorIs(t, err, errOperationMissing)
	_, created, err = s.admit(ctx, 4, 1, future, "bound", true)
	require.ErrorIs(t, err, errOperationExpired)
	require.False(t, created)
	_, err = s.getOperation(ctx, 4, issued)
	require.ErrorIs(t, err, errOperationMissing)
	_, created, err = s.admit(ctx, 4, 1, issued, "bound", true)
	require.ErrorIs(t, err, errOperationExpired)
	require.False(t, created)
}

func TestImageOperationBusyCanStillBeAdmittedOnAnotherNode(t *testing.T) {
	m := miniredis.NewMiniRedis()
	t.Cleanup(m.Close)
	c := redis.NewClient(&redis.Options{Dialer: func(context.Context, string, string) (net.Conn, error) { return m.Dial() }})
	t.Cleanup(func() { _ = c.Close() })
	s := AsyncImageStore{Client: c}
	ctx := context.Background()
	id, err := s.issueOperation(ctx, 4)
	require.NoError(t, err)
	_, created, err := s.admit(ctx, 4, 1, id, "bound", false)
	require.ErrorIs(t, err, ErrAsyncImageBusy)
	require.False(t, created)
	_, err = s.getOperation(ctx, 4, id)
	require.ErrorIs(t, err, errOperationPending)
	_, created, err = s.admit(ctx, 4, 1, id, "bound", true)
	require.NoError(t, err)
	require.True(t, created)
	_, created, err = s.admit(ctx, 4, 1, id, "bound", false)
	require.NoError(t, err)
	require.False(t, created)
}

func TestImageOperationPreviouslyBoundReceiptDoesNotDependOnSigningKey(t *testing.T) {
	m := miniredis.NewMiniRedis()
	t.Cleanup(m.Close)
	c := redis.NewClient(&redis.Options{Dialer: func(context.Context, string, string) (net.Conn, error) { return m.Dial() }})
	t.Cleanup(func() { _ = c.Close() })
	s := AsyncImageStore{Client: c}
	ctx := context.Background()
	id := fmt.Sprintf("%012x%s", time.Now().UnixMilli(), strings.Repeat("9", 36))
	require.NoError(t, m.Set(receiptKey(4, id), "priorBinding"))
	require.NoError(t, m.Set(asyncImageKey(4, id), fmt.Sprintf(`{"id":%q,"status":"succeeded"}`, id)))
	task, err := s.getOperation(ctx, 4, id)
	require.NoError(t, err)
	require.Equal(t, "succeeded", task.Status)
	_, created, err := s.admit(ctx, 4, 1, id, "priorBinding", true)
	require.NoError(t, err)
	require.False(t, created)
}
