package perfmetrics

import (
	"context"
	"math"
	"time"

	"github.com/QuantumNous/new-api/model"
)

const TokenGroupHistoryBucketSeconds = int64(3600)
const TokenGroupHistoryBuckets = 24

type TokenGroupHistory struct {
	SuccessRate *float64   `json:"success_rate"`
	History     []*float64 `json:"history"`
}

// QueryTokenGroupHistory reports request-weighted real outcomes. It combines
// persisted and current local buckets like the existing model-square reader;
// missing evidence remains nil rather than becoming healthy or failed traffic.
func QueryTokenGroupHistory(ctx context.Context, groups []string, now time.Time) (map[string]TokenGroupHistory, int64, int64, error) {
	startTs, endTs := queryWindow(now, TokenGroupHistoryBuckets)
	rows, err := model.GetTokenGroupMetricBuckets(ctx, groups, startTs, endTs)
	if err != nil {
		return nil, startTs, endTs, err
	}
	allowed := allowedGroupSet(groups)
	buckets := make(map[string][]counters, len(groups))
	for _, group := range groups {
		buckets[group] = make([]counters, TokenGroupHistoryBuckets)
	}
	for _, row := range rows {
		if _, ok := allowed[row.Group]; !ok {
			continue
		}
		index := (row.BucketTs - startTs) / TokenGroupHistoryBucketSeconds
		if index < 0 || index >= TokenGroupHistoryBuckets || row.RequestCount <= 0 {
			continue
		}
		buckets[row.Group][index].requestCount += row.RequestCount
		buckets[row.Group][index].successCount += row.SuccessCount
	}
	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		if _, ok := allowed[k.group]; !ok || k.bucketTs < startTs || k.bucketTs > endTs {
			return true
		}
		index := (k.bucketTs - startTs) / TokenGroupHistoryBucketSeconds
		if index >= TokenGroupHistoryBuckets {
			return true
		}
		snapshot := value.(*atomicBucket).snapshot()
		buckets[k.group][index].requestCount += snapshot.requestCount
		buckets[k.group][index].successCount += snapshot.successCount
		return true
	})
	result := make(map[string]TokenGroupHistory, len(groups))
	for group, values := range buckets {
		history := TokenGroupHistory{History: make([]*float64, TokenGroupHistoryBuckets)}
		var total counters
		for i, value := range values {
			if value.requestCount <= 0 {
				continue
			}
			rate := math.Round(successRate(value)*100) / 100
			history.History[i] = &rate
			total.requestCount += value.requestCount
			total.successCount += value.successCount
		}
		if total.requestCount > 0 {
			rate := math.Round(successRate(total)*100) / 100
			history.SuccessRate = &rate
		}
		result[group] = history
	}
	return result, startTs, endTs, nil
}
