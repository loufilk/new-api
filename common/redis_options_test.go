package common

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisClientUpgradePreservesConnectionPolicy(t *testing.T) {
	options, err := parseRedisOptions("redis://localhost:6379/2")
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	actual := client.Options()
	assert.Equal(t, 2, actual.Protocol)
	assert.Equal(t, 2, actual.DB)
	assert.True(t, actual.ContextTimeoutEnabled)
	assert.Equal(t, 3*time.Second, actual.ReadTimeout)
	assert.Equal(t, 3*time.Second, actual.WriteTimeout)
	assert.Equal(t, 4*time.Second, actual.PoolTimeout)
	assert.Equal(t, 5*time.Minute, actual.ConnMaxIdleTime)
	assert.Equal(t, 3, actual.MaxRetries)
	assert.Equal(t, 1, actual.DialerRetries)
	assert.Equal(t, 8*time.Millisecond, actual.MinRetryBackoff)
	assert.Equal(t, 512*time.Millisecond, actual.MaxRetryBackoff)
	assert.True(t, actual.DisableIdentity)
	assert.Equal(t, maintnotifications.ModeDisabled, actual.MaintNotificationsConfig.Mode)
	assert.Nil(t, actual.ClientSideCache)
	assert.Nil(t, actual.ClientSideCacheConfig)
}

func TestRedisClientUpgradePreservesExplicitURLSettings(t *testing.T) {
	options, err := parseRedisOptions("rediss://account:fixture@localhost:6380/4?read_timeout=7s&write_timeout=9s&pool_timeout=2s&max_retries=-1&min_retry_backoff=20ms&max_retry_backoff=2s&idle_timeout=45s&max_conn_age=10m&protocol=3")
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	actual := client.Options()
	assert.Equal(t, "account", actual.Username)
	assert.Equal(t, "fixture", actual.Password)
	assert.Equal(t, 4, actual.DB)
	assert.Equal(t, 3, actual.Protocol)
	require.NotNil(t, actual.TLSConfig)
	assert.False(t, actual.TLSConfig.InsecureSkipVerify)
	assert.Equal(t, 7*time.Second, actual.ReadTimeout)
	assert.Equal(t, 9*time.Second, actual.WriteTimeout)
	assert.Equal(t, 2*time.Second, actual.PoolTimeout)
	assert.Zero(t, actual.MaxRetries)
	assert.Equal(t, 20*time.Millisecond, actual.MinRetryBackoff)
	assert.Equal(t, 2*time.Second, actual.MaxRetryBackoff)
	assert.Equal(t, 45*time.Second, actual.ConnMaxIdleTime)
	assert.Equal(t, 10*time.Minute, actual.ConnMaxLifetime)
}

func TestRedisClientUpgradeDisabledReadTimeoutPreservesPoolBudget(t *testing.T) {
	options, err := parseRedisOptions("redis://localhost:6379?read_timeout=-1")
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	assert.Zero(t, client.Options().ReadTimeout)
	assert.Equal(t, time.Second, client.Options().PoolTimeout)
}

func TestRedisClientUpgradeRejectsInvalidConfigurationWithoutSecrets(t *testing.T) {
	for _, raw := range []string{
		"redis://user:private-secret@localhost:not-a-port",
		"redis://user:private-secret@localhost?unknown=private-secret",
		"redis://user:private-secret@localhost?protocol=4",
		"redis://user:private-secret@localhost?idle_check_frequency=1m",
	} {
		_, err := parseRedisOptions(raw)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-secret")
	}
}

func TestRedisClientUpgradeAuthenticationTTLTransactionAndCancellation(t *testing.T) {
	server := miniredis.RunT(t)
	server.RequireAuth("fixture-secret")
	u := &url.URL{Scheme: "redis", Host: server.Addr(), Path: "/3", User: url.UserPassword("", "fixture-secret")}
	options, err := parseRedisOptions(u.String())
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err())
	_, err = client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, "upgrade-key", "value", time.Minute)
		pipe.IncrBy(ctx, "upgrade-count", 2)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "value", client.Get(ctx, "upgrade-key").Val())
	assert.Equal(t, "2", client.Get(ctx, "upgrade-count").Val())
	assert.Positive(t, client.TTL(ctx, "upgrade-key").Val())
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	require.ErrorIs(t, client.Set(cancelled, "cancelled-write", "must-not-exist", 0).Err(), context.Canceled)
	assert.Zero(t, client.Exists(ctx, "cancelled-write").Val())
	server.FastForward(time.Minute)
	require.ErrorIs(t, client.Get(ctx, "upgrade-key").Err(), redis.Nil)
}
