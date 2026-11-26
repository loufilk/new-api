package model

import (
	"context"

	"gorm.io/gorm/clause"
)

// PublicLineHealthKey is selected by the server from the current publication
// configuration, never supplied by a visitor.
type PublicLineHealthKey struct {
	Group string
	Model string
}

// PublicLineHealthBucket is an internal aggregation row. Its counters and
// routing identifiers must never be serialized to a public response.
type PublicLineHealthBucket struct {
	PublicLineHealthKey
	BucketStart    int64
	Requests       int64
	Successes      int64
	LatencySamples int64
	LatencySumMs   int64
	CacheSamples   int64
	CacheHits      int64
}

func ReadPublicLineHealthBuckets(ctx context.Context, keys []PublicLineHealthKey, start, end int64) ([]PublicLineHealthBucket, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	pairs := make([][]any, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, []any{key.Model, key.Group})
	}
	query := DB.WithContext(ctx).Model(&PerfMetric{})
	quotedGroup := query.Statement.Quote(clause.Column{Name: "group"})
	const hour = "(bucket_ts - (bucket_ts % 3600))"
	var rows []PublicLineHealthBucket
	err := query.
		Select("model_name AS model, "+quotedGroup+" AS "+quotedGroup+", "+hour+" AS bucket_start, "+
			"SUM(request_count) AS requests, SUM(success_count) AS successes, "+
			"SUM(latency_count) AS latency_samples, SUM(measured_latency_ms) AS latency_sum_ms, "+
			"SUM(cache_sample_count) AS cache_samples, SUM(cache_hit_count) AS cache_hits").
		Where("(model_name, "+quotedGroup+") IN ?", pairs).
		Where("bucket_ts >= ? AND bucket_ts < ? AND request_count > 0", start, end).
		Group("model_name, " + quotedGroup + ", " + hour).
		Scan(&rows).Error
	return rows, err
}
