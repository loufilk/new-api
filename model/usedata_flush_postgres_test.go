package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The package TestMain only accepts disposable loopback PostgreSQL fixtures.
// Save/restore both buffers so failures do not contaminate unrelated tests.
func setupQuotaFlushPG(t *testing.T) {
	t.Helper()
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires TEST_MODEL_POSTGRES_DSN on local PostgreSQL 18.6/18.7")
	}
	truncateTables(t)
	w := &quotaDataWriter
	w.Lock()
	CacheQuotaDataLock.Lock()
	oldID, oldSeq, oldSnapshot, oldRetry, oldLive := w.id, w.seq, w.snapshot, w.retry, CacheQuotaData
	w.id, w.seq, w.snapshot, w.retry, CacheQuotaData = common.GetUUID(), 0, nil, nil, make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
	w.Unlock()
	t.Cleanup(func() {
		w.Lock()
		defer w.Unlock()
		CacheQuotaDataLock.Lock()
		defer CacheQuotaDataLock.Unlock()
		w.id, w.seq, w.snapshot, w.retry, CacheQuotaData = oldID, oldSeq, oldSnapshot, oldRetry, oldLive
	})
}

func TestQuotaFlushPostgresDoesNotBlockEnqueue(t *testing.T) {
	setupQuotaFlushPG(t)
	row := QuotaDataLogParams{UserID: 72, ModelName: "wait-test", CreatedAt: 3601, Quota: 4}
	LogQuotaData(row)
	holder := DB.Begin()
	require.NoError(t, holder.Error)
	defer holder.Rollback()
	require.NoError(t, holder.Exec("LOCK TABLE quota_data IN ACCESS EXCLUSIVE MODE").Error)

	done := make(chan error, 1)
	go func() { done <- saveQuotaDataCache(context.Background()) }()
	// Observe an actual blocked PostgreSQL statement; no assumption that the
	// flush worker has reached SQL just because a goroutine was launched.
	require.Eventually(t, func() bool {
		var n int64
		return DB.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'WITH targets AS (%'").Scan(&n).Error == nil && n > 0
	}, 3*time.Second, 10*time.Millisecond)
	enqueued := make(chan struct{})
	go func() { LogQuotaData(row); close(enqueued) }()
	select {
	case <-enqueued: // The table is still locked.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("request-path enqueue waited behind a blocked report write")
	}
	require.NoError(t, holder.Commit().Error)
	require.NoError(t, <-done)
	require.NoError(t, saveQuotaDataCache(context.Background()))
	var totals QuotaData
	require.NoError(t, DB.Where("model_name = ?", row.ModelName).Take(&totals).Error)
	require.Equal(t, 2, totals.Count)
	require.Equal(t, 8, totals.Quota)
}

// This wrapper runs a real PG transaction, but hides one successful commit
// acknowledgment from the writer. Nothing is mocked inside the transaction.
type loseQuotaCommitAck struct{ *sql.DB }
type quotaCommittedWithoutAck struct{ *sql.Tx }

func (pool loseQuotaCommitAck) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := pool.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &quotaCommittedWithoutAck{tx}, nil
}

func (tx quotaCommittedWithoutAck) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	return errors.New("synthetic lost acknowledgment after PostgreSQL committed")
}

func TestQuotaFlushPostgresLostAcknowledgmentAndBoundedReplay(t *testing.T) {
	setupQuotaFlushPG(t)
	for n := range quotaDataBatchSize + 5 {
		LogQuotaData(QuotaDataLogParams{UserID: 90, ModelName: fmt.Sprintf("ack-%d", n), CreatedAt: 3601, Quota: 7, TokenUsed: 9, CacheReadTokens: 2})
	}
	base := DB
	sqlDB, err := base.DB()
	require.NoError(t, err)
	DB = base.Session(&gorm.Session{NewDB: true, Context: t.Context()})
	DB.Statement.ConnPool = loseQuotaCommitAck{sqlDB}
	defer func() { DB = base }()
	err = saveQuotaDataCache(t.Context())
	DB = base
	require.ErrorContains(t, err, "synthetic lost acknowledgment")
	var total int64
	require.NoError(t, DB.Model(&QuotaData{}).Where("user_id = 90").Count(&total).Error)
	require.EqualValues(t, quotaDataBatchSize, total, "only the first bounded batch committed before connection loss")
	LogQuotaData(QuotaDataLogParams{UserID: 90, ModelName: "ack-0", CreatedAt: 3601, Quota: 7, TokenUsed: 9, CacheReadTokens: 2})
	require.NoError(t, saveQuotaDataCache(t.Context()))
	require.NoError(t, saveQuotaDataCache(t.Context()))
	var sum QuotaData
	require.NoError(t, DB.Model(&QuotaData{}).Select("sum(count) AS count, sum(quota) AS quota, sum(token_used) AS token_used, sum(cache_read_tokens) AS cache_read_tokens").Where("user_id = 90").Scan(&sum).Error)
	require.Equal(t, quotaDataBatchSize+6, sum.Count)
	require.Equal(t, sum.Count*7, sum.Quota)
	require.Equal(t, sum.Count*9, sum.TokenUsed)
	require.EqualValues(t, sum.Count*2, sum.CacheReadTokens)
}

func TestQuotaFlushPostgresRollsBackAndPreservesLegacy(t *testing.T) {
	setupQuotaFlushPG(t)
	// Two pre-existing identical dimensions, one with NULL counters, and a
	// legacy row with NULL dimensions. Do not rewrite or consolidate history.
	for range 2 {
		require.NoError(t, DB.Exec("INSERT INTO quota_data(user_id,username,model_name,created_at,use_group,token_id,channel_id,node_name,count,quota,token_used,cache_read_tokens,cache_creation_tokens) VALUES (81,'legacy','old',3600,'',0,0,'',2,10,11,NULL,NULL)").Error)
	}
	require.NoError(t, DB.Exec("INSERT INTO quota_data(user_id,username,model_name,created_at,use_group,count,quota) VALUES (81,'legacy','old',3600,NULL,3,15)").Error)
	require.NoError(t, DB.Exec("ALTER TABLE quota_data ADD CONSTRAINT quota_flush_test_failure CHECK (model_name <> 'fail-write')").Error)
	defer DB.Exec("ALTER TABLE quota_data DROP CONSTRAINT IF EXISTS quota_flush_test_failure")
	row := QuotaDataLogParams{UserID: 81, Username: "legacy", ModelName: "old", CreatedAt: 3601, Quota: 3, TokenUsed: 1, CacheReadTokens: 2, CacheCreationTokens: 4}
	LogQuotaData(row)
	row.ModelName = "fail-write"
	LogQuotaData(row)
	require.Error(t, saveQuotaDataCache(t.Context()))
	var receipts int64
	require.NoError(t, DB.Model(&QuotaDataFlushCursor{}).Where("writer_id = ?", quotaDataWriter.id).Count(&receipts).Error)
	require.Zero(t, receipts, "failed data write rolled back its cursor too")
	require.NoError(t, DB.Exec("ALTER TABLE quota_data DROP CONSTRAINT quota_flush_test_failure").Error)
	LogQuotaData(row)
	require.NoError(t, saveQuotaDataCache(t.Context()))
	var old []QuotaData
	require.NoError(t, DB.Where("user_id = 81 AND model_name = 'old'").Order("id").Find(&old).Error)
	require.Len(t, old, 3)
	require.Equal(t, 3, old[0].Count)
	require.Equal(t, 13, old[0].Quota)
	require.EqualValues(t, 2, old[0].CacheReadTokens)
	require.EqualValues(t, 4, old[0].CacheCreationTokens)
	require.Equal(t, 2, old[1].Count)
	require.Equal(t, 10, old[1].Quota)
	require.Equal(t, 3, old[2].Count)
	require.Equal(t, 15, old[2].Quota)
	var untouched int64
	require.NoError(t, DB.Model(&QuotaData{}).Where("id = ? AND use_group IS NULL", old[2].Id).Count(&untouched).Error)
	require.EqualValues(t, 1, untouched)
	var recovered QuotaData
	require.NoError(t, DB.Where("model_name = 'fail-write'").Take(&recovered).Error)
	require.Equal(t, 2, recovered.Count)
	require.Equal(t, 6, recovered.Quota)
}

func TestQuotaFlushPostgresConcurrentWritersSameNewBucket(t *testing.T) {
	setupQuotaFlushPG(t)
	entry := &QuotaData{UserID: 91, ModelName: "same", CreatedAt: 3600, Count: 1, Quota: 7}
	start := make(chan struct{})
	var group sync.WaitGroup
	ch := make(chan error, 8)
	for range 8 {
		group.Go(func() {
			<-start
			writer := common.GetUUID()
			for range 2 {
				if err := persistQuotaDataBatch(DB.WithContext(t.Context()), writer, 1, []*QuotaData{entry}); err != nil {
					ch <- err
					return
				}
			}
		})
	}
	close(start)
	group.Wait()
	close(ch)
	for err := range ch {
		require.NoError(t, err)
	}
	var buckets []QuotaData
	require.NoError(t, DB.Where("user_id = 91").Find(&buckets).Error)
	require.Len(t, buckets, 1)
	require.Equal(t, 8, buckets[0].Count)
	require.Equal(t, 56, buckets[0].Quota)
}
