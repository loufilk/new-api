package model

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// One cursor per process that actually flushed (not per request or batch).
// Never expire a live writer's cursor: even a long DB outage can mask a commit.
type QuotaDataFlushCursor struct {
	WriterID   string `gorm:"primaryKey;size:32"`
	AppliedSeq int64  `gorm:"not null"`
}

const quotaDataBatchSize = 64

// Only the flush worker and the final shutdown drain take this lock. Request
// producers only take CacheQuotaDataLock to enqueue. Unflushed data is still
// process-local reporting data, never the source of funds or settlement.
var quotaDataWriter = struct {
	sync.Mutex
	id       string
	seq      int64
	snapshot map[string]*QuotaData
	retry    []*QuotaData
}{id: common.GetUUID()}

func saveQuotaDataCache(ctx context.Context) error {
	w := &quotaDataWriter
	w.Lock()
	defer w.Unlock()
	saved := 0
	tookSnapshot := false
	for {
		if len(w.retry) == 0 {
			if len(w.snapshot) == 0 {
				w.snapshot = nil
				if tookSnapshot {
					break
				}
				// At most one new snapshot per pass, including a pass recovering
				// from failure. O(1), independent of snapshot size or DB time.
				CacheQuotaDataLock.Lock()
				w.snapshot, CacheQuotaData = CacheQuotaData, make(map[string]*QuotaData)
				CacheQuotaDataLock.Unlock()
				tookSnapshot = true
				if len(w.snapshot) == 0 {
					break
				}
			}
			w.seq++
			for key, bucket := range w.snapshot {
				w.retry = append(w.retry, bucket)
				delete(w.snapshot, key)
				if len(w.retry) == quotaDataBatchSize {
					break
				}
			}
		}
		batchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := persistQuotaDataBatch(DB.WithContext(batchCtx), w.id, w.seq, w.retry)
		cancel()
		if err != nil {
			CacheQuotaDataLock.Lock()
			live := len(CacheQuotaData)
			CacheQuotaDataLock.Unlock()
			wrapped := fmt.Errorf("dashboard flush failed after %d saved buckets (%d pending incl. new arrivals): %w", saved, len(w.retry)+len(w.snapshot)+live, err)
			common.SysError(common.MaskSensitiveLogInfo(wrapped.Error()))
			return wrapped
		}
		saved += len(w.retry)
		w.retry = nil
	}
	if saved != 0 {
		common.SysLog(common.LogText("saved dashboard data, %d records", saved))
	}
	return nil
}

// Cursor and deltas commit together. Even if Commit returns a transport error
// after the server committed, the same immutable (writer, sequence, rows) is
// retried: an already applied sequence does not touch any counter.
func persistQuotaDataBatch(db *gorm.DB, writer string, seq int64, rows []*QuotaData) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// Serialize the missing-bucket decision among reporting writers. There
		// is deliberately no unique index on legacy (possibly duplicated/NULL)
		// dimensions, or lock on the financial ledger / customer user rows.
		const gate = "quota-data-writer-gate"
		seeds := []QuotaDataFlushCursor{{WriterID: gate}, {WriterID: writer}}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seeds).Error; err != nil {
			return err
		}
		var guard QuotaDataFlushCursor
		if err := lockForUpdate(tx).Where("writer_id = ?", gate).Take(&guard).Error; err != nil {
			return err
		}
		claim := tx.Model(&QuotaDataFlushCursor{}).Where("writer_id = ? AND applied_seq < ?", writer, seq).UpdateColumn("applied_seq", seq)
		if claim.Error != nil || claim.RowsAffected == 0 {
			return claim.Error
		}
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			// One set-based write regardless of the number of buckets. Choose
			// only the oldest exact match; historical duplicate/NULL dimensions
			// remain untouched. NULL measures in that one row denote no prior use.
			payload, err := common.Marshal(rows)
			if err != nil {
				return err
			}
			return tx.Exec(`WITH targets AS (
			    SELECT d.*, (SELECT min(q.id) FROM quota_data q
			      WHERE (q.user_id,q.username,q.model_name,q.created_at,q.use_group,q.token_id,q.channel_id,q.node_name)
			          = (d.user_id,d.username,d.model_name,d.created_at,d.use_group,d.token_id,d.channel_id,d.node_name)) AS target_id
			    FROM json_populate_recordset(NULL::quota_data, CAST(? AS json)) d
			), updated AS (
			    UPDATE quota_data q SET count=COALESCE(q.count,0)+d.count, quota=COALESCE(q.quota,0)+d.quota,
			      token_used=COALESCE(q.token_used,0)+d.token_used, cache_read_tokens=COALESCE(q.cache_read_tokens,0)+d.cache_read_tokens,
			      cache_creation_tokens=COALESCE(q.cache_creation_tokens,0)+d.cache_creation_tokens
			    FROM targets d WHERE q.id=d.target_id
			)
			INSERT INTO quota_data (user_id,username,model_name,created_at,use_group,token_id,channel_id,node_name,count,quota,token_used,cache_read_tokens,cache_creation_tokens)
			SELECT user_id,username,model_name,created_at,use_group,token_id,channel_id,node_name,count,quota,token_used,cache_read_tokens,cache_creation_tokens
			FROM targets WHERE target_id IS NULL`, string(payload)).Error
		}

		// Non-production dialects do not support PostgreSQL's composite JSON
		// conversion / data-modifying CTE; keep the same bounded transaction.
		for _, row := range rows {
			var id int
			if err := tx.Model(&QuotaData{}).Where("user_id = ? AND username = ? AND model_name = ? AND created_at = ? AND use_group = ? AND token_id = ? AND channel_id = ? AND node_name = ?",
				row.UserID, row.Username, row.ModelName, row.CreatedAt, row.UseGroup, row.TokenID, row.ChannelID, row.NodeName).Order("id").Limit(1).Pluck("id", &id).Error; err != nil {
				return err
			}
			if id == 0 {
				entry := *row // Create fills the ID; never mutate our replay buffer.
				if err := tx.Create(&entry).Error; err != nil {
					return err
				}
				continue
			}
			if err := tx.Model(&QuotaData{}).Where("id = ?", id).Updates(map[string]any{
				"count":                 gorm.Expr("COALESCE(count,0) + ?", row.Count),
				"quota":                 gorm.Expr("COALESCE(quota,0) + ?", row.Quota),
				"token_used":            gorm.Expr("COALESCE(token_used,0) + ?", row.TokenUsed),
				"cache_read_tokens":     gorm.Expr("COALESCE(cache_read_tokens,0) + ?", row.CacheReadTokens),
				"cache_creation_tokens": gorm.Expr("COALESCE(cache_creation_tokens,0) + ?", row.CacheCreationTokens),
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
