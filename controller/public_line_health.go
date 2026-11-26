package controller

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/modules/chuanapi/console"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/gin-gonic/gin"
)

const (
	publicHealthHours      = 24
	publicHealthLineLimit  = 200
	publicHealthMinSamples = 20
)

type publicHealthMeasurement struct {
	SuccessRate      *float64 `json:"success_rate"`
	AverageLatencyMs *float64 `json:"average_latency_ms"`
	CacheHitRate     *float64 `json:"cache_hit_rate"`
}

type publicHealthHistory struct {
	BucketStart int64 `json:"bucket_start"`
	publicHealthMeasurement
}

type publicHealthLine struct {
	ID                  string                `json:"id"`
	Label               string                `json:"label"`
	RepresentativeModel string                `json:"representative_model"`
	Vendor              string                `json:"vendor,omitempty"`
	Ratio               float64               `json:"ratio"`
	SortOrder           int                   `json:"sort_order"`
	Buckets             []publicHealthHistory `json:"buckets"`
	publicHealthMeasurement
}

type publicHealthResponse struct {
	WindowStart int64              `json:"window_start"`
	WindowEnd   int64              `json:"window_end"`
	Lines       []publicHealthLine `json:"lines"`
}

func GetPublicLineHealth(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	result, err := readPublicLineHealth(ctx, time.Now(), perf_metrics_setting.GetSetting(), console.PublishedLines, model.ReadPublicLineHealthBuckets)
	if err != nil {
		// Do not return a last-good data set after a withdrawal or failed read.
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Unable to load public line metrics."))
		return
	}
	common.ApiSuccess(c, result)
}

func readPublicLineHealth(
	ctx context.Context, now time.Time, settings perf_metrics_setting.PerfMetricsSetting,
	published func(context.Context) ([]console.PublishedLine, error),
	read func(context.Context, []model.PublicLineHealthKey, int64, int64) ([]model.PublicLineHealthBucket, error),
) (publicHealthResponse, error) {
	// Allow the normal flush to settle before reading the preceding hour.
	// Constrain delay so an unusually large flush setting cannot present
	// historical measurements as current indefinitely.
	lag := min(max(1, settings.FlushInterval), 59)
	end := now.UTC().Add(-time.Duration(lag) * time.Minute).Truncate(time.Hour)
	start := end.Add(-publicHealthHours * time.Hour)
	response := publicHealthResponse{
		WindowStart: start.Unix(), WindowEnd: end.Unix(), Lines: []publicHealthLine{},
	}
	lines, err := published(ctx)
	if err != nil {
		return response, err
	}
	lines = healthEligibleLines(lines, now.Unix())
	results := make(map[model.PublicLineHealthKey]map[int64]model.PublicLineHealthBucket)
	if len(lines) != 0 && settings.Enabled {
		keys := make([]model.PublicLineHealthKey, 0, len(lines))
		for _, line := range lines {
			keys = append(keys, lineHealthKey(line))
		}
		rows, err := read(ctx, keys, start.Unix(), end.Unix())
		if err != nil {
			return response, err
		}
		for _, row := range rows {
			if results[row.PublicLineHealthKey] == nil {
				results[row.PublicLineHealthKey] = make(map[int64]model.PublicLineHealthBucket)
			}
			results[row.PublicLineHealthKey][row.BucketStart] = row
		}
		// The measurement query is not an authority. Re-read publication
		// configuration after that query so removed, renamed, restricted, or
		// newly published lines cannot inherit a stale public measurement.
		current, err := published(ctx)
		if err != nil {
			return response, err
		}
		currentByID := make(map[string]console.PublishedLine, len(current))
		for _, line := range healthEligibleLines(current, now.Unix()) {
			currentByID[line.ID] = line
		}
		var verified []console.PublishedLine
		for _, line := range lines {
			match, ok := currentByID[line.ID]
			if ok && match.Group == line.Group && match.RepresentativeModel == line.RepresentativeModel && match.ObservedSince == line.ObservedSince {
				verified = append(verified, match)
			}
		}
		lines = verified
	}
	for _, line := range lines {
		item := publicHealthLine{
			ID: line.ID, Label: line.Label, RepresentativeModel: line.RepresentativeModel,
			Vendor: line.Vendor, Ratio: line.Ratio, SortOrder: line.SortOrder,
			Buckets: make([]publicHealthHistory, publicHealthHours),
		}
		buckets := results[lineHealthKey(line)]
		var total model.PublicLineHealthBucket
		for i := range item.Buckets {
			ts := start.Add(time.Duration(i) * time.Hour).Unix()
			item.Buckets[i].BucketStart = ts
			// Drop the whole transition bucket: nothing from before explicit
			// approval becomes public, including older samples in the same hour.
			if ts < line.ObservedSince {
				continue
			}
			row := buckets[ts]
			total.Requests += row.Requests
			total.Successes += row.Successes
			total.LatencySamples += row.LatencySamples
			total.LatencySumMs += row.LatencySumMs
			total.CacheSamples += row.CacheSamples
			total.CacheHits += row.CacheHits
			item.Buckets[i].publicHealthMeasurement = measurePublicHealth(row)
		}
		item.publicHealthMeasurement = measurePublicHealth(total)
		response.Lines = append(response.Lines, item)
	}
	return response, nil
}

func healthEligibleLines(lines []console.PublishedLine, now int64) []console.PublishedLine {
	var eligible []console.PublishedLine
	for _, line := range lines {
		if line.PublishHealth && line.ObservedSince > 0 && line.ObservedSince <= now && line.Group != "" && line.RepresentativeModel != "" {
			eligible = append(eligible, line)
			if len(eligible) >= publicHealthLineLimit {
				break
			}
		}
	}
	return eligible
}

func lineHealthKey(line console.PublishedLine) model.PublicLineHealthKey {
	return model.PublicLineHealthKey{Group: line.Group, Model: line.RepresentativeModel}
}

func measurePublicHealth(row model.PublicLineHealthBucket) publicHealthMeasurement {
	var out publicHealthMeasurement
	if row.Requests >= publicHealthMinSamples && row.Successes >= 0 && row.Successes <= row.Requests {
		rate := math.Round(float64(row.Successes) * 100 / float64(row.Requests))
		out.SuccessRate = &rate
	}
	if row.LatencySamples >= publicHealthMinSamples && row.LatencySamples <= row.Requests && row.LatencySumMs >= 0 {
		mean := math.Round(float64(row.LatencySumMs)/float64(row.LatencySamples)/10) * 10
		out.AverageLatencyMs = &mean
	}
	if row.CacheSamples >= publicHealthMinSamples && row.CacheSamples <= row.Successes && row.CacheHits >= 0 && row.CacheHits <= row.CacheSamples {
		rate := math.Round(float64(row.CacheHits) * 100 / float64(row.CacheSamples))
		out.CacheHitRate = &rate
	}
	return out
}
