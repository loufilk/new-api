package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelfLogStatCacheCoalescesCancellationAndAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newSelfLogStatCache(2, 1, 15*time.Second)
		key := selfLogStatKey{role: 1}
		var calls atomic.Int32
		release := make(chan struct{})
		load := func(ctx context.Context, _ time.Time) (model.Stat, error) {
			calls.Add(1)
			select {
			case <-release:
				return model.Stat{Quota: 42}, nil
			case <-ctx.Done():
				return model.Stat{}, ctx.Err()
			}
		}
		leader, cancelLeader := context.WithCancel(context.Background())
		leaderResult := make(chan error, 1)
		go func() { _, err := cache.get(leader, key, time.Minute, nil, load); leaderResult <- err }()
		synctest.Wait()
		results := make(chan model.Stat, 8)
		errorsCh := make(chan error, 8)
		for range 8 {
			go func() {
				stat, err := cache.get(context.Background(), key, time.Minute, nil, load)
				results <- stat
				errorsCh <- err
			}()
		}
		synctest.Wait()
		// Distinct misses wait for admission rather than bypassing the work cap.
		queued, cancelQueued := context.WithCancel(context.Background())
		queuedResult := make(chan error, 1)
		go func() {
			_, err := cache.get(queued, selfLogStatKey{role: 2}, time.Minute, nil, load)
			queuedResult <- err
		}()
		synctest.Wait()
		cancelLeader()
		cancelQueued()
		synctest.Wait()
		require.ErrorIs(t, <-leaderResult, context.Canceled)
		require.ErrorIs(t, <-queuedResult, context.Canceled)
		assert.EqualValues(t, 1, calls.Load())
		close(release)
		synctest.Wait()
		for range 8 {
			require.NoError(t, <-errorsCh)
			assert.Equal(t, model.Stat{Quota: 42}, <-results)
		}
		value, err := cache.get(context.Background(), key, time.Minute, nil, load)
		require.NoError(t, err)
		assert.Equal(t, model.Stat{Quota: 42}, value)
		assert.EqualValues(t, 1, calls.Load(), "warm requests must not execute SQL")
	})
}

func TestSelfLogStatCacheLastWaiterCancelsBuildAndDoesNotPublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newSelfLogStatCache(2, 1, time.Minute)
		key := selfLogStatKey{}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		stopped := make(chan struct{})
		go func() {
			_, err := cache.get(ctx, key, time.Minute, nil, func(buildCtx context.Context, _ time.Time) (model.Stat, error) {
				<-buildCtx.Done()
				close(stopped)
				return model.Stat{Quota: 999}, nil // even a misbehaving loader cannot cache cancellation
			})
			result <- err
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		require.ErrorIs(t, <-result, context.Canceled)
		<-stopped
		value, err := cache.get(context.Background(), key, time.Minute, nil, func(context.Context, time.Time) (model.Stat, error) { return model.Stat{Quota: 7}, nil })
		require.NoError(t, err)
		assert.Equal(t, 7, value.Quota)
	})
}

func TestSelfLogStatCacheExpiryErrorsAndBounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newSelfLogStatCache(2, 1, 15*time.Second)
		key, rateKey := selfLogStatKey{}, selfLogStatKey{rates: true}
		failure := errors.New("database unavailable")
		_, err := cache.get(context.Background(), key, 5*time.Second, &rateKey, func(context.Context, time.Time) (model.Stat, error) { return model.Stat{}, failure })
		require.ErrorIs(t, err, failure)
		var calls int
		load := func(context.Context, time.Time) (model.Stat, error) {
			calls++
			return model.Stat{Quota: calls, Rpm: calls}, nil
		}
		first, err := cache.get(context.Background(), key, 5*time.Second, &rateKey, load)
		require.NoError(t, err)
		assert.Equal(t, 1, first.Quota)
		rate, err := cache.get(context.Background(), rateKey, time.Second, nil, load)
		require.NoError(t, err)
		assert.Equal(t, model.Stat{Rpm: 1}, rate, "cold history seeds rates without an extra SQL query")
		time.Sleep(time.Second)
		_, err = cache.get(context.Background(), rateKey, time.Second, nil, load)
		require.NoError(t, err)
		assert.Equal(t, 2, calls, "rates expire independently")
		cached, err := cache.get(context.Background(), key, 5*time.Second, nil, load)
		require.NoError(t, err)
		assert.Equal(t, first, cached)
		time.Sleep(4 * time.Second)
		_, err = cache.get(context.Background(), key, 5*time.Second, &rateKey, load)
		require.NoError(t, err)
		assert.Equal(t, 3, calls, "historical TTL is not extended by rate hits")
		for role := range 6 {
			_, err = cache.get(context.Background(), selfLogStatKey{role: role + 1}, time.Minute, nil, load)
			require.NoError(t, err)
		}
		assert.Len(t, cache.entries, 2, "retained memory stays bounded under distinct filters")
		assert.Empty(t, cache.builds)
	})
}

func TestSelfLogStatCacheSlowBuildAndTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newSelfLogStatCache(2, 1, 3*time.Second)
		key := selfLogStatKey{}
		var calls int
		load := func(context.Context, time.Time) (model.Stat, error) {
			calls++
			time.Sleep(2 * time.Second)
			return model.Stat{Quota: calls}, nil
		}
		_, err := cache.get(context.Background(), key, time.Second, nil, load)
		require.NoError(t, err)
		_, err = cache.get(context.Background(), key, time.Second, nil, load)
		require.NoError(t, err)
		assert.Equal(t, 2, calls, "a slow result cannot extend TTL beyond its sampling time")
		_, err = cache.get(context.Background(), key, time.Minute, nil, func(ctx context.Context, _ time.Time) (model.Stat, error) {
			<-ctx.Done()
			return model.Stat{}, ctx.Err()
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		synctest.Wait()
		assert.Empty(t, cache.entries)
		assert.Empty(t, cache.builds)
	})
}

func TestSelfLogStatCacheCanceledAtPublicationDoesNotSeed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newSelfLogStatCache(2, 1, time.Minute)
		ctx, cancel := context.WithCancel(context.Background())
		key, rateKey := selfLogStatKey{}, selfLogStatKey{rates: true}
		_, err := cache.get(ctx, key, time.Minute, &rateKey, func(context.Context, time.Time) (model.Stat, error) {
			cancel() // completion races the request goroutine's cancellation cleanup
			return model.Stat{Quota: 999, Rpm: 999}, nil
		})
		require.ErrorIs(t, err, context.Canceled)
		synctest.Wait()
		assert.Empty(t, cache.entries, "neither history nor rates may publish when all callers are already canceled")
	})
}

func TestSelfLogStatFilterByteIdentity(t *testing.T) {
	valid := model.LogFilter{UserId: 1, TokenName: "\uFFFD"}
	invalid := valid
	invalid.TokenName = string([]byte{0xff})
	assert.NotEqual(t, selfLogFilterHash(valid), selfLogFilterHash(invalid), "PostgreSQL rejects invalid UTF-8; it must not hit a valid filter's cache")
	first := model.LogFilter{UserId: 1, ModelName: "a|b", TokenName: "c"}
	second := model.LogFilter{UserId: 1, ModelName: "a", TokenName: "b|c"}
	assert.NotEqual(t, selfLogFilterHash(first), selfLogFilterHash(second), "field boundaries are part of query identity")
}
