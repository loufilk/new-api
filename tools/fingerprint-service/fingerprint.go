package main

import (
	"errors"
	"math"
	"strconv"
	"strings"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
)

const SDKCommit = "f920cfe2ff0bab94989ce01ac061e21425b6e321"
const ProbabilityNote = "Probabilities rank candidates only within the bundled reference library; they are not proof of model identity. Models absent from the library can still rank highly."
const MaxResponseBytes = 64 * 1024

var errInvalidSamples = errors.New("invalid fingerprint request")

// Sample contains only the numeric response and public execution outcome. Never
// include credentials, request headers, provider error bodies or user messages.
type Sample struct {
	ChallengeID   string `json:"challenge_id"`
	Response      string `json:"response"`
	ExpectedCount int    `json:"expected_count"`
	Error         string `json:"error,omitempty"`
	NonModel      bool   `json:"non_model"`
	Truncated     bool   `json:"truncated"`
}

type Candidate = lmfpd.AnalysisResult

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

// GenerateChallenges always generates exactly three independent upstream prompts.
func GenerateChallenges() (challenges []lmfpd.Challenge, err error) {
	defer func() {
		if recover() != nil {
			challenges = nil
			err = errors.New("fingerprint challenge generation failed")
		}
	}()
	return lmfpd.GenerateChallenges(3)
}

// Analyze is pure offline analysis. Strict mode requires all three sufficiently
// long, nontruncated replies. Partial rankings never carry probabilities.
func (e *Engine) Analyze(samples []Sample, strict bool) (r Result, err error) {
	defer func() {
		if recover() != nil {
			r, err = Result{}, errors.New("fingerprint analysis failed")
		}
	}()
	r = Result{Status: "unscorable", TopCandidates: []Candidate{}, SampleQuality: []Quality{}, SDKCommit: SDKCommit,
		ProbabilityNote: ProbabilityNote, ProbabilityStatus: "unavailable", Strict: strict}
	if len(samples) != 3 {
		return r, errInvalidSamples
	}
	if e == nil || e.detector == nil {
		return r, errors.New("fingerprint service unavailable")
	}
	r.DataVersion, r.DataSHA256 = e.version, referenceDetectorSHA256
	outputs := make([]lmfpd.Output, 3)
	seen := make(map[string]bool, 3)
	complete := 0
	nonModel := false
	for i, s := range samples {
		if !validChallengeID(s.ChallengeID) || seen[s.ChallengeID] || s.ExpectedCount < 292 || s.ExpectedCount > 332 {
			return r, errInvalidSamples
		}
		seen[s.ChallengeID] = true
		q := Quality{ChallengeID: s.ChallengeID, ExpectedCount: s.ExpectedCount, MinimumNumbers: max(80, int(math.Ceil(float64(s.ExpectedCount)*0.55))), Truncated: s.Truncated}
		outputs[i].ExpectedCount = s.ExpectedCount
		switch {
		case s.NonModel:
			q.Reason = "non_model_response"
			nonModel = true
		case s.Error != "":
			q.Reason = "request_failed"
		case len(s.Response) > MaxResponseBytes:
			q.Reason = "response_too_large"
		default:
			nums := lmfpd.ParseNumbers(s.Response)
			q.ParsedNumbers = len(nums)
			q.OutOfRangeCount = rawOutOfRange(s.Response)
			q.Short = len(nums) < s.ExpectedCount
			q.Accepted = len(nums) >= q.MinimumNumbers
			if q.Accepted {
				r.UsedOutputs++
				if !strict && len(nums) > s.ExpectedCount {
					nums = nums[:s.ExpectedCount]
				}
				outputs[i].Text = joinNumbers(nums)
				if !q.Truncated {
					complete++
				}
				q.Reason = "accepted"
			} else {
				q.Reason = "insufficient_numbers"
			}
		}
		r.SampleQuality = append(r.SampleQuality, q)
	}
	if strict && complete != 3 {
		r.Reason = "strict3_requires_three_complete_samples"
		return r, nil
	}
	if r.UsedOutputs == 0 {
		r.Reason = "no_valid_samples"
		if nonModel {
			r.Status = "not_model"
			r.Reason = "non_model_response"
		}
		return r, nil
	}
	a, err := e.detector.Analyze(outputs, strict)
	if err != nil {
		return r, errors.New("fingerprint analysis failed")
	}
	r.Status, r.Reason, r.ProbabilityStatus = a.Decision, a.Evidence.Reason, a.ProbabilityStatus
	r.TopCandidates = a.Results[:min(3, len(a.Results))]
	return r, nil
}

// Challenge metadata is echoed in quality results, so allow only bounded public IDs.
func validChallengeID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.:-", c) {
			continue
		}
		return false
	}
	return true
}

func joinNumbers(numbers []int) string {
	var text strings.Builder
	for i, number := range numbers {
		if i > 0 {
			text.WriteString(", ")
		}
		text.WriteString(strconv.Itoa(number))
	}
	return text.String()
}

// Count signed digit runs outside 1..355 without overflowing on long inputs.
// Parsing/ranking itself always uses the unmodified SDK's ParseNumbers.
func rawOutOfRange(text string) int {
	count := 0
	for i := 0; i < len(text); {
		negative := text[i] == '-' && i+1 < len(text) && text[i+1] >= '0' && text[i+1] <= '9'
		if negative {
			i++
		} else if text[i] < '0' || text[i] > '9' {
			i++
			continue
		}
		start := i
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
		digits := strings.TrimLeft(text[start:i], "0")
		if negative || len(digits) == 0 || len(digits) > 3 {
			count++
			continue
		}
		value, _ := strconv.Atoi(digits)
		if value > 355 {
			count++
		}
	}
	return count
}
