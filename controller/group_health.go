package controller

import (
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type groupHealthMeasurement struct {
	RequestCount  int64    `json:"request_count"`
	SuccessCount  int64    `json:"success_count"`
	SuccessRate   *float64 `json:"success_rate"`
	TtftCount     int64    `json:"ttft_sample_count"`
	AverageTtftMs *float64 `json:"average_ttft_ms"`
}

type groupHealthBucket struct {
	BucketStart int64 `json:"bucket_start"`
	groupHealthMeasurement
}

type groupHealthResponse struct {
	Group                string              `json:"group"`
	Available            bool                `json:"available"`
	MetricsAvailable     bool                `json:"metrics_available"`
	WindowStart          int64               `json:"window_start"`
	WindowEnd            int64               `json:"window_end"`
	ObservedAt           int64               `json:"observed_at"`
	LatestBucketStart    *int64              `json:"latest_bucket_start"`
	BucketSeconds        int64               `json:"bucket_seconds"`
	FlushIntervalSeconds int64               `json:"flush_interval_seconds"`
	Buckets              []groupHealthBucket `json:"buckets"`
	groupHealthMeasurement
}

// GetTokenGroupHealth exposes only an account's currently selectable concrete
// group. It is a separate read from the legacy Auto group listing.
func GetTokenGroupHealth(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("Unauthorized"))
		return
	}
	values := c.QueryArray("group")
	if len(values) != 1 || values[0] == "" || values[0] == "auto" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Please select a group"))
		return
	}
	group := values[0]
	scope, ok := authorizeTokenGroupHealth(c, userID, group)
	if !ok {
		return
	}

	now := time.Now().UTC()
	observedAt := now.Unix()
	windowStart := now.Truncate(time.Hour).Add(-(perfmetrics.TokenGroupHistoryBuckets - 1) * time.Hour).Unix()
	metricsSettings := perf_metrics_setting.GetSetting()
	metricsAvailable := metricsSettings.Enabled && scope.historyKnown && scope.firstConfiguredAt <= observedAt
	historyStart, priorIdentityBefore := windowStart, int64(0)
	if metricsAvailable {
		renameAt, known := model.GroupHealthRenameLineage(c.Request.Context(), group)
		metricsAvailable = known && renameAt <= observedAt
		if changedAt := max(scope.firstConfiguredAt, renameAt); metricsAvailable && changedAt > 0 {
			// A populated full hour before this transition proves this same
			// spelling belonged to a different generation. Later buckets
			// cannot prove they do not contain that generation's old streams.
			priorIdentityBefore = time.Unix(changedAt, 0).UTC().Truncate(time.Hour).Unix()
			historyStart = max(historyStart, priorIdentityBefore+perfmetrics.TokenGroupHistoryBucketSeconds)
		}
	}
	available, reused, rows, err := model.QueryGroupHealth(c.Request.Context(), group, historyStart, observedAt, priorIdentityBefore, metricsAvailable)
	if err != nil && metricsAvailable {
		// Historical lookup has no authority over today's availability. If
		// verification overruns its budget, report no history and retry only
		// the lightweight availability probe.
		metricsAvailable = false
		available, _, rows, err = model.QueryGroupHealth(c.Request.Context(), group, windowStart, observedAt, 0, false)
	}
	if reused {
		metricsAvailable = false
	}
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, errors.New(i18n.T(c, i18n.MsgDatabaseError)))
		return
	}
	// Recheck the same admission generation and exact current account grants
	// before publishing. The potentially slower metrics query holds no lock.
	current, ok := authorizeTokenGroupHealth(c, userID, group)
	if !ok {
		return
	}
	if current != scope {
		metricsAvailable, rows = false, nil
	}

	data := groupHealthResponse{
		Group: group, Available: available, MetricsAvailable: metricsAvailable,
		WindowStart: windowStart, WindowEnd: observedAt, ObservedAt: observedAt,
		BucketSeconds:        perfmetrics.TokenGroupHistoryBucketSeconds,
		FlushIntervalSeconds: int64(max(1, metricsSettings.FlushInterval)) * 60,
		Buckets:              make([]groupHealthBucket, perfmetrics.TokenGroupHistoryBuckets),
	}
	for i := range data.Buckets {
		data.Buckets[i].BucketStart = windowStart + int64(i)*data.BucketSeconds
	}
	var totals model.GroupHealthCounters
	for _, row := range rows {
		index := (row.BucketTs - windowStart) / data.BucketSeconds
		if row.BucketTs < windowStart || index >= int64(len(data.Buckets)) {
			continue
		}
		data.Buckets[index].groupHealthMeasurement = groupHealthMeasurementFromCounters(row)
		if data.LatestBucketStart == nil || *data.LatestBucketStart < row.BucketTs {
			bucketStart := row.BucketTs
			data.LatestBucketStart = &bucketStart
		}
		totals.RequestCount += row.RequestCount
		totals.SuccessCount += row.SuccessCount
		totals.TtftCount += row.TtftCount
		totals.TtftSumMs += row.TtftSumMs
	}
	data.groupHealthMeasurement = groupHealthMeasurementFromCounters(totals)
	common.ApiSuccess(c, data)
}

type groupHealthScope struct {
	firstConfiguredAt int64
	historyKnown      bool
}

func authorizeTokenGroupHealth(c *gin.Context, userID int, group string) (groupHealthScope, bool) {
	guard, ok := beginControllerGroupConfigRead(c)
	if !ok {
		return groupHealthScope{}, false
	}
	defer guard.Close()
	ctx := c.Request.Context()
	user, scopeErr := model.ReadGroupHealthUser(ctx, userID)
	if scopeErr == nil {
		if user.Status != common.UserStatusEnabled {
			scopeErr = service.ErrTokenGroupForbidden
		} else {
			scopeErr = service.ValidateTokenGroupForUser(ctx, userID, user.Group, group)
		}
	}
	var scope groupHealthScope
	if scopeErr == nil {
		scope.firstConfiguredAt, scope.historyKnown = model.GroupHealthFirstConfiguredAt(guard.Version, group)
	}
	if !finishControllerGroupConfigRead(c, guard) {
		return groupHealthScope{}, false
	}
	switch {
	case scopeErr == nil:
		return scope, true
	case errors.Is(scopeErr, service.ErrTokenGroupForbidden), errors.Is(scopeErr, service.ErrTokenGroupDeprecated), errors.Is(scopeErr, gorm.ErrRecordNotFound):
		// A nonexistent group and a private group without a grant look identical.
		common.ApiErrorStatus(c, http.StatusForbidden, errors.New(i18n.T(c, i18n.MsgDistributorGroupAccessDenied)))
	default:
		groupConfigReadFailure(c)
	}
	return groupHealthScope{}, false
}

func groupHealthMeasurementFromCounters(c model.GroupHealthCounters) groupHealthMeasurement {
	data := groupHealthMeasurement{
		RequestCount: c.RequestCount, SuccessCount: c.SuccessCount, TtftCount: c.TtftCount,
	}
	if c.RequestCount > 0 {
		rate := math.Round(float64(c.SuccessCount)/float64(c.RequestCount)*10000) / 100
		data.SuccessRate = &rate
	}
	if c.TtftCount > 0 {
		average := math.Round(float64(c.TtftSumMs)/float64(c.TtftCount)*100) / 100
		data.AverageTtftMs = &average
	}
	return data
}
