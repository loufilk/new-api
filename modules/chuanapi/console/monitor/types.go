// Package monitor serves a bounded, read-only view of external benchmark results.
// These results are reference observations, not measurements of this gateway.
package monitor

type Target struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Number int    `json:"number"`
	Name   string `json:"name,omitempty"`
}

type Result struct {
	ID             string `json:"id"`
	TargetID       string `json:"target_id"`
	Benchmark      string `json:"benchmark"`
	Status         string `json:"status"`
	StartedAt      int64  `json:"started_at"`
	CompletedAt    int64  `json:"completed_at"`
	DurationMS     int64  `json:"duration_ms"`
	Revision       int    `json:"revision"`
	ExpectedAnswer *int   `json:"expected_answer,omitempty"`
	QualityStatus  string `json:"quality_status,omitempty"`
	QualityReason  string `json:"quality_reason,omitempty"`
}

type Snapshot struct {
	Enabled     bool     `json:"enabled"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Running     bool     `json:"running"`
	ObservedAt  int64    `json:"observed_at"`
	FetchedAt   int64    `json:"fetched_at"`
	Stale       bool     `json:"stale"`
	Targets     []Target `json:"targets"`
	Results     []Result `json:"results"`
}

type Detail struct {
	Prompt          string `json:"prompt"`
	Answer          string `json:"answer"`
	ReferenceReason string `json:"reference_reason"`
}

type upstreamSnapshot struct {
	Enabled    *bool    `json:"enabled"`
	Running    bool     `json:"running"`
	ServerTime int64    `json:"server_time"`
	Targets    []Target `json:"targets"`
	Results    []Result `json:"results"`
}

type envelope[T any] struct {
	Success bool `json:"success"`
	Data    T    `json:"data"`
}
