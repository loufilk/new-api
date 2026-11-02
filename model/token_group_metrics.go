package model

import "context"

// TokenGroupMetricBucket contains only aggregate, non-identifying request
// evidence. Callers must pass the live authorized concrete group set.
type TokenGroupMetricBucket struct {
	Group        string
	BucketTs     int64
	RequestCount int64
	SuccessCount int64
}

func GetTokenGroupMetricBuckets(ctx context.Context, groups []string, startTs, endTs int64) ([]TokenGroupMetricBucket, error) {
	rows := make([]TokenGroupMetricBucket, 0)
	if len(groups) == 0 {
		return rows, nil
	}
	err := DB.WithContext(ctx).Model(&PerfMetric{}).
		Select(commonGroupCol+", bucket_ts, SUM(request_count) AS request_count, SUM(success_count) AS success_count").
		Where(commonGroupCol+" IN ? AND bucket_ts >= ? AND bucket_ts <= ?", groups, startTs, endTs).
		Group(commonGroupCol + ", bucket_ts").
		Having("SUM(request_count) > 0").
		Order("bucket_ts ASC").Scan(&rows).Error
	return rows, err
}
