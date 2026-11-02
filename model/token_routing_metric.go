package model

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TokenRoutingMetric contains aggregate route health only: no user, key,
// prompt, response, URL or upstream credential. It is not an accounting fact.
type TokenRoutingMetric struct {
	ID             int    `gorm:"primaryKey"`
	ChannelID      int    `gorm:"uniqueIndex:idx_token_route_bucket,priority:1"`
	ModelName      string `gorm:"size:128;uniqueIndex:idx_token_route_bucket,priority:2"`
	Group          string `gorm:"size:64;uniqueIndex:idx_token_route_bucket,priority:3"`
	BucketTs       int64  `gorm:"uniqueIndex:idx_token_route_bucket,priority:4;index"`
	RequestCount   int64
	SuccessCount   int64
	TotalLatencyMs int64
}

func RecordTokenRoutingMetric(ctx context.Context, channelID int, modelName, group string, success bool, latency time.Duration) error {
	if channelID <= 0 || modelName == "" || group == "" || group == "auto" {
		return nil
	}
	now := time.Now().Unix()
	row := TokenRoutingMetric{ChannelID: channelID, ModelName: modelName, Group: group, BucketTs: now - now%1800, RequestCount: 1}
	if success {
		row.SuccessCount = 1
		row.TotalLatencyMs = max(0, latency.Milliseconds())
	}
	return DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "channel_id"}, {Name: "model_name"}, {Name: "group"}, {Name: "bucket_ts"}},
		DoUpdates: clause.Assignments(map[string]any{
			"request_count":    gorm.Expr("token_routing_metrics.request_count + 1"),
			"success_count":    gorm.Expr("token_routing_metrics.success_count + ?", row.SuccessCount),
			"total_latency_ms": gorm.Expr("token_routing_metrics.total_latency_ms + ?", row.TotalLatencyMs),
		}),
	}).Create(&row).Error
}

func GetTokenRoutingMetrics(ctx context.Context, modelName string, groups []string) ([]TokenRoutingMetric, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	var rows []TokenRoutingMetric
	err := DB.WithContext(ctx).Model(&TokenRoutingMetric{}).
		Select("channel_id, "+commonGroupCol+", SUM(request_count) AS request_count, SUM(success_count) AS success_count, SUM(total_latency_ms) AS total_latency_ms").
		Where("model_name = ? AND bucket_ts >= ?", modelName, time.Now().Add(-24*time.Hour).Unix()).
		Where(commonGroupCol+" IN ?", groups).Group("channel_id, " + commonGroupCol).Find(&rows).Error
	return rows, err
}

// RoutingCandidate is an internal channel/group pair, never a public response.
type RoutingCandidate struct {
	Channel *Channel
	Group   string
}

// GetTokenRoutingCandidates reads current enabled abilities from the primary
// database. Exact model bindings take precedence over normalized names, just
// like the legacy selector. Every request filter is applied before ranking.
func GetTokenRoutingCandidates(ctx context.Context, groups []string, modelName string, filters []dto.ChannelFilter) ([]RoutingCandidate, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	names := []string{modelName}
	if normalized := ratio_setting.RoutingMatchModelName(modelName); normalized != "" && normalized != modelName {
		names = append(names, normalized)
	}
	var abilities []Ability
	if err := DB.WithContext(ctx).Where(commonGroupCol+" IN ? AND model IN ? AND enabled = ?", groups, names, true).Find(&abilities).Error; err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(abilities))
	for _, a := range abilities {
		ids = append(ids, a.ChannelId)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var channels []*Channel
	if err := DB.WithContext(ctx).Where("id IN ? AND status = ?", ids, common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return nil, err
	}
	byID := make(map[int]*Channel, len(channels))
	for _, ch := range channels {
		if ok, _ := ChannelSatisfiesFilters(ch, modelName, filters); ok {
			byID[ch.Id] = ch
		}
	}
	exact := map[string]bool{}
	for _, a := range abilities {
		if a.Model == modelName && byID[a.ChannelId] != nil {
			exact[a.Group] = true
		}
	}
	seen := map[string]map[int]bool{}
	candidates := make([]RoutingCandidate, 0, len(abilities))
	for _, a := range abilities {
		ch := byID[a.ChannelId]
		if ch == nil || (exact[a.Group] && a.Model != modelName) {
			continue
		}
		if seen[a.Group] == nil {
			seen[a.Group] = map[int]bool{}
		}
		if seen[a.Group][ch.Id] {
			continue
		}
		seen[a.Group][ch.Id] = true
		candidates = append(candidates, RoutingCandidate{Channel: ch, Group: a.Group})
	}
	return candidates, nil
}

func DeleteTokenRoutingMetricsBefore(cutoffTs int64) error {
	return DB.Where("bucket_ts < ?", cutoffTs).Delete(&TokenRoutingMetric{}).Error
}
