package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSelfLogStatPostgresQueryBudgetAndIsolation(t *testing.T) {
	if !common.UsingLogDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires disposable TEST_SERVICE_POSTGRES_DSN")
	}
	previous := selfLogStats
	selfLogStats = newSelfLogStatCache(2048, 32, 15*time.Second)
	t.Cleanup(func() { selfLogStats = previous })
	var clock atomic.Int64
	const now int64 = 1700000000
	clock.Store(now)
	selfLogStats.now = func() time.Time { return time.Unix(clock.Load(), 0) }
	require.NoError(t, model.LOG_DB.Exec("DELETE FROM logs").Error)
	base := model.Log{UserId: 501, Type: model.LogTypeConsume, ModelName: "literal_model", TokenName: "token", Group: "group", RequestId: "req", UpstreamRequestId: "upstream"}
	for _, row := range []struct {
		at            int64
		quota, tokens int
	}{{now - 600, 10, 0}, {now - 500, 20, 0}, {now - 59, 5, 11}, {now - 5, 7, 13}} {
		log := base
		log.CreatedAt, log.Quota, log.PromptTokens = row.at, row.quota, row.tokens
		require.NoError(t, model.LOG_DB.Create(&log).Error)
	}
	for _, alter := range []func(*model.Log){
		func(l *model.Log) { l.UserId++ }, func(l *model.Log) { l.Type = model.LogTypeError },
		func(l *model.Log) { l.ModelName = "literalXmodel" }, func(l *model.Log) { l.TokenName = "other" },
		func(l *model.Log) { l.Group = "other" }, func(l *model.Log) { l.RequestId = "other" },
		func(l *model.Log) { l.UpstreamRequestId = "other" },
	} {
		for _, at := range []int64{now - 550, now - 5} {
			log := base
			alter(&log)
			log.CreatedAt, log.Quota, log.PromptTokens = at, 100, 200
			require.NoError(t, model.LOG_DB.Create(&log).Error)
		}
	}
	filter := model.LogFilter{UserId: 501, Type: model.LogTypeError, ModelName: "literal_model", TokenName: "token", Group: "group", RequestId: "req", UpstreamRequestId: "upstream", StartTimestamp: now - 600, EndTimestamp: now - 500}
	var statements atomic.Int64
	const callback = "self_log_stat_count"
	require.NoError(t, model.LOG_DB.Callback().Row().Before("gorm:row").Register(callback, func(*gorm.DB) { statements.Add(1) }))
	t.Cleanup(func() { require.NoError(t, model.LOG_DB.Callback().Row().Remove(callback)) })
	want := model.Stat{Quota: 30, Rpm: 2, Tpm: 24}
	for range 32 {
		stat, err := model.SumUsedQuotaAt(model.LOG_DB, filter, time.Unix(now, 0))
		require.NoError(t, err)
		require.Equal(t, want, stat)
	}
	assert.EqualValues(t, 32, statements.Swap(0), "baseline is one SQL per request")
	type result struct {
		stat model.Stat
		err  error
	}
	results := make(chan result, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			stat, err := GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 86400, false)
			results <- result{stat, err}
		})
	}
	wg.Wait()
	close(results)
	for result := range results {
		require.NoError(t, result.err)
		assert.Equal(t, want, result.stat)
	}
	assert.EqualValues(t, 1, statements.Swap(0), "32 simultaneous cold readers share one combined SQL")
	for range 32 {
		stat, err := GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 86400, false)
		require.NoError(t, err)
		assert.Equal(t, want, stat)
	}
	assert.Zero(t, statements.Swap(0), "warm burst performs no SQL")

	// Explicit boundaries are not bucketed, and the rolling rates are independent.
	inner := filter
	inner.StartTimestamp++
	inner.EndTimestamp--
	stat, err := GetSelfLogStat(context.Background(), inner, common.RoleCommonUser, 86400, false)
	require.NoError(t, err)
	assert.Equal(t, model.Stat{Rpm: 2, Tpm: 24}, stat)
	assert.EqualValues(t, 1, statements.Swap(0))
	for _, alter := range []func(*model.LogFilter){
		func(f *model.LogFilter) { f.UserId++ }, func(f *model.LogFilter) { f.ModelName = "literalXmodel" },
		func(f *model.LogFilter) { f.TokenName = "other" }, func(f *model.LogFilter) { f.Group = "other" },
		func(f *model.LogFilter) { f.RequestId = "other" }, func(f *model.LogFilter) { f.UpstreamRequestId = "other" },
	} {
		other := filter
		alter(&other)
		stat, err := GetSelfLogStat(context.Background(), other, common.RoleCommonUser, 86400, false)
		require.NoError(t, err)
		assert.Equal(t, model.Stat{Quota: 100, Rpm: 1, Tpm: 200}, stat)
	}
	assert.EqualValues(t, 6, statements.Swap(0), "every user/filter has its own cold snapshot")
	// A new historical row cannot freeze rates for the historical TTL.
	added := base
	added.CreatedAt, added.Quota = now-550, 15
	require.NoError(t, model.LOG_DB.Create(&added).Error)
	clock.Store(now + 2)
	stat, err = GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 86400, false)
	require.NoError(t, err)
	assert.Equal(t, model.Stat{Quota: 30, Rpm: 1, Tpm: 13}, stat)
	assert.EqualValues(t, 1, statements.Swap(0), "only rolling rate SQL refreshes")
	clock.Store(now + 6)
	stat, err = GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 86400, false)
	require.NoError(t, err)
	assert.Equal(t, model.Stat{Quota: 45, Rpm: 1, Tpm: 13}, stat)
	assert.EqualValues(t, 1, statements.Swap(0), "history expiry still uses one combined SQL")
	// Same data, but a changed visibility policy must not reuse the old entry.
	_, err = GetSelfLogStat(context.Background(), filter, common.RoleAdminUser, 86400, false)
	require.NoError(t, err)
	_, err = GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 172800, false)
	require.NoError(t, err)
	assert.EqualValues(t, 2, statements.Swap(0))
	// Replacing the log database/session handle is also a distinct cache scope.
	oldDB := model.LOG_DB
	model.LOG_DB = oldDB.Session(&gorm.Session{})
	_, err = GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 86400, false)
	model.LOG_DB = oldDB
	require.NoError(t, err)
	assert.EqualValues(t, 1, statements.Swap(0))
	t.Log("SQL evidence: baseline 32; concurrent cold 1; warm 0; rate expiry 1; history expiry 1")
}

func TestSelfLogStatPostgresDefaultRangeAndFailure(t *testing.T) {
	if !common.UsingLogDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires disposable TEST_SERVICE_POSTGRES_DSN")
	}
	previous := selfLogStats
	selfLogStats = newSelfLogStatCache(2048, 32, 15*time.Second)
	t.Cleanup(func() { selfLogStats = previous })
	var clock atomic.Int64
	const now int64 = 1700000000
	clock.Store(now)
	selfLogStats.now = func() time.Time { return time.Unix(clock.Load(), 0) }
	require.NoError(t, model.LOG_DB.Exec("DELETE FROM logs").Error)
	require.NoError(t, model.LOG_DB.Create(&model.Log{UserId: 777, Type: model.LogTypeConsume, CreatedAt: now - 86400 + 1, Quota: 9}).Error)
	filter := model.LogFilter{UserId: 777, StartTimestamp: now - 86400}
	stat, err := GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 86400, true)
	require.NoError(t, err)
	assert.Equal(t, 9, stat.Quota)
	clock.Store(now + 6)
	// The controller-computed lower bound is deliberately old. The semantic
	// omitted-start key must rebuild against the new sample, not freeze it.
	stat, err = GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 86400, true)
	require.NoError(t, err)
	assert.Zero(t, stat.Quota)
	stat, err = GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 172800, true)
	require.NoError(t, err)
	assert.Equal(t, 9, stat.Quota)
	filter.UserId = 778
	const failureCallback = "self_log_stat_failure"
	require.NoError(t, model.LOG_DB.Callback().Row().Before("gorm:row").Register(failureCallback, func(db *gorm.DB) { db.AddError(errors.New("synthetic query failure")) }))
	_, err = GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 86400, true)
	require.Error(t, err)
	require.NoError(t, model.LOG_DB.Callback().Row().Remove(failureCallback))
	require.NoError(t, model.LOG_DB.Create(&model.Log{UserId: 778, Type: model.LogTypeConsume, CreatedAt: now, Quota: 17}).Error)
	stat, err = GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 86400, true)
	require.NoError(t, err)
	assert.Equal(t, 17, stat.Quota, "query errors cannot become successful cached zeroes")
}

// Compare identical one-reader queries without concurrent traffic. Cold cache
// entries use exact, distinct historical boundaries; no URL/time bucketing.
func BenchmarkSelfLogStatPostgres(b *testing.B) {
	if !common.UsingLogDatabase(common.DatabaseTypePostgreSQL) {
		b.Skip("requires disposable TEST_SERVICE_POSTGRES_DSN")
	}
	previous := selfLogStats
	b.Cleanup(func() { selfLogStats = previous })
	for _, mode := range []string{"baseline", "cold", "warm"} {
		b.Run(mode, func(b *testing.B) {
			selfLogStats = newSelfLogStatCache(2048, 32, 15*time.Second)
			filter := model.LogFilter{UserId: 88888, StartTimestamp: 1600000000, EndTimestamp: 1600100000}
			if mode == "warm" {
				_, err := GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 86400, false)
				require.NoError(b, err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				if mode == "cold" {
					filter.StartTimestamp = 1600000000 + int64(i)
				}
				var stat model.Stat
				var err error
				if mode == "baseline" {
					stat, err = model.SumUsedQuota(filter)
				} else {
					stat, err = GetSelfLogStat(context.Background(), filter, common.RoleCommonUser, 86400, false)
				}
				if err != nil || stat != (model.Stat{}) {
					b.Fatalf("invalid statistics: %+v %v", stat, err)
				}
			}
		})
	}
}
