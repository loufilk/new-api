// Package modelfingerprint is a bounded client for the separate, local scoring
// service. The NewAPI binary does not include the SDK, detector or reference bank.
package modelfingerprint

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// SDKCommit identifies the scoring-compatible core snapshot. The independent
// SDK archive and its provenance are documented in the service README.
const SDKCommit = "f920cfe2ff0bab94989ce01ac061e21425b6e321"
const ProbabilityNote = "Probabilities rank candidates only within the bundled reference library; they are not proof of model identity. Models absent from the library can still rank highly."
const MaxResponseBytes = 64 * 1024
const DefaultSocketPath = "/run/new-api/fingerprint.sock"
const referenceDetectorSHA256 = "59dffad8128dca8bcf0cfb024f0fcebe7f0b7d7fbae3583b4b5d1418c18e601f"
const maxServiceRequestBytes = 256 * 1024
const maxServiceResponseBytes = 512 * 1024

var ErrUnavailable = errors.New("fingerprint scoring service unavailable; start the local fingerprint service")
var errInvalidServiceResponse = errors.New("invalid fingerprint scoring service response")

// Sample never serializes its response into application logs or public results.
// Analyze constructs a separate, private request containing only probe samples.
type Sample struct {
	ChallengeID   string `json:"challenge_id"`
	Response      string `json:"-"`
	ExpectedCount int    `json:"expected_count"`
	Error         string `json:"error,omitempty"`
	NonModel      bool   `json:"non_model"`
	Truncated     bool   `json:"truncated"`
}
type Challenge struct {
	ID            string `json:"id"`
	ExpectedCount int    `json:"expected_count"`
	Prompt        string `json:"prompt"`
}
type Candidate struct {
	Model       string   `json:"model"`
	DisplayName string   `json:"display_name"`
	Family      string   `json:"family"`
	FamilyName  string   `json:"family_name"`
	Score       float64  `json:"score"`
	Probability *float64 `json:"probability"`
}
type Quality struct {
	ChallengeID     string `json:"challenge_id"`
	ExpectedCount   int    `json:"expected_count"`
	ParsedNumbers   int    `json:"parsed_numbers"`
	MinimumNumbers  int    `json:"minimum_numbers"`
	Accepted        bool   `json:"accepted"`
	Truncated       bool   `json:"truncated"`
	Short           bool   `json:"short"`
	OutOfRangeCount int    `json:"out_of_range_count"`
	Reason          string `json:"reason"`
}
type Result struct {
	Status            string      `json:"status"`
	Reason            string      `json:"reason"`
	TopCandidates     []Candidate `json:"top_candidates"`
	SampleQuality     []Quality   `json:"sample_quality"`
	DataVersion       string      `json:"data_version"`
	DataSHA256        string      `json:"data_sha256"`
	SDKCommit         string      `json:"sdk_commit"`
	ProbabilityNote   string      `json:"probability_note"`
	ProbabilityStatus string      `json:"probability_status"`
	UsedOutputs       int         `json:"used_outputs"`
	Strict            bool        `json:"strict"`
}

// Ready is checked before billable probes. Failure is not cached: starting or
// repairing the service makes the next request eligible immediately.
func Ready() error {
	var health struct {
		ProtocolVersion int    `json:"protocol_version"`
		SDKCommit       string `json:"sdk_commit"`
		DataVersion     string `json:"data_version"`
		DataSHA256      string `json:"data_sha256"`
	}
	if err := callService(http.MethodGet, "/v1/ready", nil, &health); err != nil {
		return err
	}
	if health.ProtocolVersion != 1 || health.SDKCommit != SDKCommit || health.DataSHA256 != referenceDetectorSHA256 || health.DataVersion == "" || len(health.DataVersion) > 128 {
		return errInvalidServiceResponse
	}
	return nil
}

// GenerateChallenges asks the independent SDK service for exactly three prompts.
func GenerateChallenges() ([]Challenge, error) {
	var response struct {
		Challenges []Challenge `json:"challenges"`
	}
	if err := callService(http.MethodGet, "/v1/challenges", nil, &response); err != nil {
		return nil, err
	}
	if len(response.Challenges) != 3 {
		return nil, errInvalidServiceResponse
	}
	ids, counts := map[string]bool{}, map[int]bool{}
	for _, c := range response.Challenges {
		if !validChallengeID(c.ID) || ids[c.ID] || c.ExpectedCount < 292 || c.ExpectedCount > 332 || counts[c.ExpectedCount] || len(c.Prompt) == 0 || len(c.Prompt) > 8192 {
			return nil, errInvalidServiceResponse
		}
		ids[c.ID], counts[c.ExpectedCount] = true, true
	}
	return response.Challenges, nil
}

// Analyze sends only synthetic probe text and bounded outcome metadata over the
// local socket. There are no fields for keys, target URLs, headers or history.
func Analyze(samples []Sample, strict bool) (Result, error) {
	empty := Result{Status: "unscorable", TopCandidates: []Candidate{}, SampleQuality: []Quality{}, SDKCommit: SDKCommit, ProbabilityNote: ProbabilityNote, ProbabilityStatus: "unavailable", Strict: strict}
	if len(samples) != 3 {
		return empty, errors.New("fingerprint analysis requires exactly three samples")
	}
	type wireSample struct {
		ChallengeID   string `json:"challenge_id"`
		Response      string `json:"response"`
		ExpectedCount int    `json:"expected_count"`
		Error         string `json:"error"`
		NonModel      bool   `json:"non_model"`
		Truncated     bool   `json:"truncated"`
	}
	request := struct {
		Samples []wireSample `json:"samples"`
		Strict  bool         `json:"strict"`
	}{Samples: make([]wireSample, 3), Strict: strict}
	seen := map[string]bool{}
	for i, sample := range samples {
		if !validChallengeID(sample.ChallengeID) || seen[sample.ChallengeID] || sample.ExpectedCount < 292 || sample.ExpectedCount > 332 || len(sample.Response) > MaxResponseBytes {
			return empty, errors.New("invalid fingerprint sample")
		}
		seen[sample.ChallengeID] = true
		request.Samples[i] = wireSample{ChallengeID: sample.ChallengeID, ExpectedCount: sample.ExpectedCount, NonModel: sample.NonModel, Truncated: sample.Truncated}
		if sample.Error != "" {
			request.Samples[i].Error = "request_failed"
		} else if !sample.NonModel {
			request.Samples[i].Response = sample.Response
		}
	}
	var result Result
	if err := callService(http.MethodPost, "/v1/analyze", request, &result); err != nil {
		return empty, err
	}
	if err := validateResult(result, samples, strict); err != nil {
		return empty, err
	}
	result.ProbabilityNote = ProbabilityNote
	return result, nil
}

func validChallengeID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.:-", r)) {
			return false
		}
	}
	return true
}

func validateResult(result Result, samples []Sample, strict bool) error {
	if result.SDKCommit != SDKCommit || result.DataSHA256 != referenceDetectorSHA256 || result.DataVersion == "" || len(result.DataVersion) > 128 || result.Strict != strict || result.UsedOutputs < 0 || result.UsedOutputs > 3 || len(result.TopCandidates) > 3 || len(result.SampleQuality) != 3 || len(result.Reason) > 1024 {
		return errInvalidServiceResponse
	}
	switch result.Status {
	case "scored":
		if result.UsedOutputs != 3 || len(result.TopCandidates) == 0 {
			return errInvalidServiceResponse
		}
	case "partial":
		if strict || result.UsedOutputs < 1 || result.UsedOutputs > 2 || len(result.TopCandidates) == 0 || result.ProbabilityStatus != "unavailable" {
			return errInvalidServiceResponse
		}
	case "unscorable", "not_model":
		if len(result.TopCandidates) != 0 || result.ProbabilityStatus != "unavailable" {
			return errInvalidServiceResponse
		}
	default:
		return errInvalidServiceResponse
	}
	if result.ProbabilityStatus != "reference_calibrated" && result.ProbabilityStatus != "unavailable" {
		return errInvalidServiceResponse
	}
	for _, candidate := range result.TopCandidates {
		if candidate.Model == "" || len(candidate.Model) > 255 || len(candidate.DisplayName) > 255 || len(candidate.Family) > 255 || len(candidate.FamilyName) > 255 || math.IsNaN(candidate.Score) || math.IsInf(candidate.Score, 0) {
			return errInvalidServiceResponse
		}
		if p := candidate.Probability; p != nil && (result.ProbabilityStatus != "reference_calibrated" || result.Status != "scored" || math.IsNaN(*p) || math.IsInf(*p, 0) || *p < 0 || *p > 1) {
			return errInvalidServiceResponse
		}
	}
	accepted := 0
	for i, q := range result.SampleQuality {
		if q.ChallengeID != samples[i].ChallengeID || q.ExpectedCount != samples[i].ExpectedCount || q.ParsedNumbers < 0 || q.ParsedNumbers > MaxResponseBytes || q.MinimumNumbers != max(80, int(math.Ceil(float64(samples[i].ExpectedCount)*0.55))) || q.OutOfRangeCount < 0 || q.OutOfRangeCount > MaxResponseBytes || len(q.Reason) > 128 {
			return errInvalidServiceResponse
		}
		if q.Accepted {
			accepted++
			if q.ParsedNumbers < q.MinimumNumbers || samples[i].Error != "" || samples[i].NonModel || strict && result.Status == "scored" && q.Truncated {
				return errInvalidServiceResponse
			}
		}
	}
	if accepted != result.UsedOutputs {
		return errInvalidServiceResponse
	}
	return nil
}

func callService(method, path string, input, output any) error {
	socket := os.Getenv("MODEL_FINGERPRINT_SOCKET")
	if socket == "" {
		socket = DefaultSocketPath
	}
	if !filepath.IsAbs(socket) || strings.ContainsRune(socket, '\x00') {
		return ErrUnavailable
	}
	var body []byte
	if input != nil {
		var err error
		body, err = common.Marshal(input)
		if err != nil || len(body) > maxServiceRequestBytes {
			return errors.New("fingerprint scoring request is too large or invalid")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, "http://fingerprint"+path, bytes.NewReader(body))
	if err != nil {
		return ErrUnavailable
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	transport := serviceTransport(socket)
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		defer closer.CloseIdleConnections()
	}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxServiceResponseBytes+1))
	if err != nil || len(data) > maxServiceResponseBytes || common.DecodeJsonStrict(bytes.NewReader(data), output) != nil {
		return errInvalidServiceResponse
	}
	return nil
}

// This internal factory keeps protocol tests in memory. Production always uses
// Unix sockets; there is no exported network or endpoint override.
var serviceTransport = unixServiceTransport

func unixServiceTransport(socket string) http.RoundTripper {
	dialer := &net.Dialer{Timeout: time.Second}
	return &http.Transport{DisableKeepAlives: true, MaxResponseHeaderBytes: 8 * 1024,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		}}
}
