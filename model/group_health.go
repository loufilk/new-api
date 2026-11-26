package model

import (
	"context"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm/clause"
)

// GroupHealthCounters are persisted relay totals, not per-model rates or
// observations from the local gateway's still-unflushed counters.
type GroupHealthCounters struct {
	BucketTs     int64
	RequestCount int64
	SuccessCount int64
	TtftSumMs    int64
	TtftCount    int64
}

// ReadGroupHealthUser retrieves current routing ownership and account status
// directly from the primary without populating a cache or reading credentials.
func ReadGroupHealthUser(ctx context.Context, userID int) (User, error) {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var user User
	err := DB.WithContext(bounded).Select("group", "status").Where("id = ?", userID).Take(&user).Error
	return user, err
}

// GroupHealthFirstConfiguredAt must run under a fenced GroupConfigRead.
// Market lineage resets on a remove/recreate. An invalid or out-of-generation
// snapshot cannot establish which identity owns name-keyed observations.
func GroupHealthFirstConfiguredAt(revision int64, group string) (int64, bool) {
	snapshot := marketRuntime.Load()
	if snapshot == nil || snapshot.records != nil && snapshot.revision != revision {
		return 0, false
	}
	return snapshot.records[group].FirstConfiguredAt, true
}

// GroupHealthRenameLineage recognizes retired names without depending on a
// metrics retention interval. A configured former alias source has been reused.
// A rename target needs its exact transition, because the market date follows
// the group identity whereas stored metric rows do not.
func GroupHealthRenameLineage(ctx context.Context, group string) (int64, bool) {
	const maxReceipts = 128
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	db := DB.WithContext(bounded)
	aliases, err := loadGroupRenameAliasesTx(db)
	if err != nil {
		return 0, false
	}
	if _, isRetiredName := aliases[group]; isRetiredName {
		return 0, false
	}
	target := false
	for _, name := range aliases {
		if name == group {
			target = true
			break
		}
	}
	if !target {
		return 0, true
	}

	var receipts []struct {
		CreatedAt int64
		Result    *string
	}
	// Look from the newest indexed receipt backwards: once the current
	// destination is found, older receipts cannot alter its last transition.
	err = db.Model(&GroupRenameOperation{}).
		Select("created_at, CASE WHEN LENGTH(result) <= 65536 THEN result ELSE NULL END AS result").
		Order("created_at DESC").Limit(maxReceipts).Find(&receipts).Error
	if err != nil {
		return 0, false
	}
	for _, entry := range receipts {
		var result struct {
			Renames []GroupRenameMapping `json:"renames"`
		}
		if entry.CreatedAt <= 0 || entry.Result == nil ||
			common.UnmarshalJsonStr(*entry.Result, &result) != nil || len(result.Renames) == 0 {
			return 0, false
		}
		if slices.ContainsFunc(result.Renames, func(rename GroupRenameMapping) bool { return rename.To == group }) {
			return entry.CreatedAt, true
		}
	}
	return 0, false
}

// QueryGroupHealth reads availability and hourly observations for one concrete,
// already-authorized group. No global group fallback is possible; the
// availability row is returned even when the group has no observations.
func QueryGroupHealth(ctx context.Context, group string, startTs, endTs, priorIdentityBefore int64, includeMetrics bool) (bool, bool, []GroupHealthCounters, error) {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	db := DB.WithContext(bounded)
	enabled := db.Table("abilities").Select("1").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where(clause.Eq{Column: clause.Column{Table: "abilities", Name: "group"}, Value: group}).
		Where("abilities.enabled = ? AND channels.status = ?", true, common.ChannelStatusEnabled)

	type resultRow struct {
		Available     bool
		PriorIdentity bool
		BucketTs      *int64
		RequestCount  int64
		SuccessCount  int64
		TtftSumMs     int64
		TtftCount     int64
	}
	var rows []resultRow
	configured := db.Raw("SELECT EXISTS (?) AS available, FALSE AS prior_identity", enabled)
	if includeMetrics && priorIdentityBefore > 0 {
		previous := db.Model(&PerfMetric{}).Select("1").
			Where(clause.Eq{Column: clause.Column{Name: "group"}, Value: group}).
			Where("bucket_ts < ? AND request_count > 0", priorIdentityBefore)
		configured = db.Raw("SELECT EXISTS (?) AS available, EXISTS (?) AS prior_identity", enabled, previous)
	}
	query := db.Table("(?) AS configured", configured)
	if includeMetrics {
		const hour = "(bucket_ts - (bucket_ts % 3600))"
		stored := db.Model(&PerfMetric{}).
			Select(hour+" AS bucket_ts, SUM(request_count) AS request_count, SUM(success_count) AS success_count, SUM(ttft_sum_ms) AS ttft_sum_ms, SUM(ttft_count) AS ttft_count").
			Where(clause.Eq{Column: clause.Column{Name: "group"}, Value: group}).
			Where("bucket_ts >= ? AND bucket_ts <= ? AND request_count > 0", startTs, endTs).
			Group(hour).Order(hour + " ASC").Limit(24)
		query = query.Select("configured.available, configured.prior_identity, measured.bucket_ts, COALESCE(measured.request_count, 0) AS request_count, COALESCE(measured.success_count, 0) AS success_count, COALESCE(measured.ttft_sum_ms, 0) AS ttft_sum_ms, COALESCE(measured.ttft_count, 0) AS ttft_count").
			Joins("LEFT JOIN (?) AS measured ON NOT configured.prior_identity", stored).
			Order("measured.bucket_ts ASC")
	}
	if err := query.Scan(&rows).Error; err != nil {
		return false, false, nil, err
	}
	var available bool
	var counters []GroupHealthCounters
	for _, row := range rows {
		available = row.Available
		if row.PriorIdentity {
			return available, true, nil, nil
		}
		if row.BucketTs != nil {
			counters = append(counters, GroupHealthCounters{
				BucketTs: *row.BucketTs, RequestCount: row.RequestCount, SuccessCount: row.SuccessCount,
				TtftSumMs: row.TtftSumMs, TtftCount: row.TtftCount,
			})
		}
	}
	return available, false, counters, nil
}
