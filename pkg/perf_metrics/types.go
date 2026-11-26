package perfmetrics

import "sync"

type Store interface {
	Record(sample Sample)
	Query(params QueryParams) (QueryResult, error)
}

type Sample struct {
	Model         string
	Group         string
	LatencyMs     int64
	HasLatency    bool
	TtftMs        int64
	HasTtft       bool
	CacheMeasured bool
	CacheHit      bool
	Success       bool
	OutputTokens  int64
	GenerationMs  int64
}

type QueryParams struct {
	Model string
	Group string
	Hours int
	// AllowedGroups restricts both the per-group results and the model summary;
	// nil allows every group.
	AllowedGroups []string
}

// Summary is the request-weighted aggregate over every bucket in the window.
type Summary struct {
	// Availability flags distinguish a measured (possibly rounded) zero from
	// an absent observation without disclosing private sample counts.
	HasTtft      bool    `json:"has_ttft"`
	HasLatency   bool    `json:"has_latency"`
	HasTps       bool    `json:"has_tps"`
	AvgTtftMs    int64   `json:"avg_ttft_ms"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
	SuccessRate  float64 `json:"success_rate"`
	AvgTps       float64 `json:"avg_tps"`
}

type BucketPoint struct {
	Ts int64 `json:"ts"`
	Summary
}

type GroupResult struct {
	Group string `json:"group"`
	Summary
	Series []BucketPoint `json:"series"`
}

type QueryResult struct {
	ModelName    string        `json:"model_name"`
	SeriesSchema string        `json:"series_schema"`
	Summary      *Summary      `json:"summary"`
	Series       []BucketPoint `json:"series"`
	WindowStart  int64         `json:"window_start"`
	WindowEnd    int64         `json:"window_end"`
	Groups       []GroupResult `json:"groups"`
}

type SuccessRatePoint struct {
	Ts          int64   `json:"ts"`
	SuccessRate float64 `json:"success_rate"`
}

type ModelSummary struct {
	ModelName string `json:"model_name"`
	Summary
	RecentSuccessSeries []SuccessRatePoint `json:"recent_success_series,omitempty"`
	RequestCount        int64              `json:"-"`
}

type SummaryAllResult struct {
	Summary     *Summary       `json:"summary"`
	WindowStart int64          `json:"window_start"`
	WindowEnd   int64          `json:"window_end"`
	Models      []ModelSummary `json:"models"`
	// RecentSuccessSeries contains up to 24 nonempty hours from the last 7 days,
	// including requested models with no traffic in the summary window.
	RecentSuccessSeries map[string][]SuccessRatePoint `json:"recent_success_series,omitempty"`
}

type bucketKey struct {
	model    string
	group    string
	bucketTs int64
}

type counters struct {
	requestCount      int64
	successCount      int64
	totalLatencyMs    int64
	latencyCount      int64
	measuredLatencyMs int64
	cacheSampleCount  int64
	cacheHitCount     int64
	ttftSumMs         int64
	ttftCount         int64
	outputTokens      int64
	generationMs      int64
}

// Keep each completed observation and each drain indivisible. In particular,
// a sample finishing on a bucket boundary must not have its numerator and
// denominator sent in different flushes.
type atomicBucket struct {
	mu    sync.Mutex
	value counters
}

func (b *atomicBucket) add(sample Sample) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := &b.value
	c.requestCount++
	if sample.Success {
		c.successCount++
	}
	if sample.LatencyMs > 0 {
		c.totalLatencyMs += sample.LatencyMs
	}
	if sample.HasLatency || sample.LatencyMs > 0 {
		c.latencyCount++
		c.measuredLatencyMs += sample.LatencyMs
	}
	if sample.Success && sample.CacheMeasured {
		c.cacheSampleCount++
		if sample.CacheHit {
			c.cacheHitCount++
		}
	}
	if sample.HasTtft && sample.TtftMs >= 0 {
		c.ttftSumMs += sample.TtftMs
		c.ttftCount++
	}
	if sample.OutputTokens > 0 && sample.GenerationMs > 0 {
		c.outputTokens += sample.OutputTokens
		c.generationMs += sample.GenerationMs
	}
}

func (b *atomicBucket) snapshot() counters {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.value
}

func (b *atomicBucket) drain() counters {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := b.value
	b.value = counters{}
	return result
}

func (b *atomicBucket) addCounters(c counters) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.value.merge(c)
	b.value.latencyCount += c.latencyCount
	b.value.measuredLatencyMs += c.measuredLatencyMs
	b.value.cacheSampleCount += c.cacheSampleCount
	b.value.cacheHitCount += c.cacheHitCount
}
