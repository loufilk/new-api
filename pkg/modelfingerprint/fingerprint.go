// Package modelfingerprint provides offline numeric fingerprint analysis only.
// It has no transport, credentials, proxy, download, cache or refresh mechanism.
package modelfingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
)

const SDKCommit = "f920cfe2ff0bab94989ce01ac061e21425b6e321"
const ProbabilityNote = "Probabilities rank candidates only within the bundled reference library; they are not proof of model identity. Models absent from the library can still rank highly."
const MaxResponseBytes = 64 * 1024

// Sample contains only the numeric response and public execution outcome. Never
// include credentials, request headers, provider error bodies or user messages.
type Sample struct {
	ChallengeID   string `json:"challenge_id"`
	Response      string `json:"-"`
	ExpectedCount int    `json:"expected_count"`
	Error         string `json:"error,omitempty"`
	NonModel      bool   `json:"non_model"`
	Truncated     bool   `json:"truncated"`
}

type Candidate = AnalysisResult

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
func GenerateChallenges() (challenges []Challenge, err error) {
	defer func() {
		if recover() != nil {
			challenges = nil
			err = errors.New("fingerprint challenge generation failed")
		}
	}()
	return generateChallenges(3), nil
}

// VerifySHA256 compares actual bytes to a separately pinned digest. Metadata
// reported by a reference file is never accepted as verification of those bytes.
func VerifySHA256(data []byte, expected string) error {
	want, err := hex.DecodeString(expected)
	if err != nil || len(want) != sha256.Size {
		return errors.New("invalid pinned fingerprint data digest")
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != expected {
		return errors.New("fingerprint data checksum mismatch")
	}
	return nil
}

// loadReference validates immutable bundled bytes before decoding them. No path,
// URL, or caller-supplied digest is accepted by Analyze.
func loadReference(bankBytes, detectorBytes []byte, bankHash, detectorHash string) (*Detector, error) {
	if err := VerifySHA256(bankBytes, bankHash); err != nil {
		return nil, err
	}
	if err := VerifySHA256(detectorBytes, detectorHash); err != nil {
		return nil, err
	}
	var b bank
	if err := common.Unmarshal(bankBytes, &b); err != nil {
		return nil, errors.New("invalid fingerprint reference metadata")
	}
	d, err := decodeDetector(detectorBytes)
	if err != nil {
		return nil, errors.New("invalid fingerprint detector data")
	}
	return newDetector(&b, d)
}

// Analyze is pure offline analysis. Strict mode requires all three sufficiently
// long, nontruncated replies. Partial rankings never carry probabilities.
func Analyze(samples []Sample, strict bool) (Result, error) {
	r := Result{Status: "unscorable", TopCandidates: []Candidate{}, SampleQuality: []Quality{}, SDKCommit: SDKCommit,
		ProbabilityNote: ProbabilityNote, ProbabilityStatus: "unavailable", Strict: strict}
	if len(samples) != 3 {
		return r, errors.New("fingerprint analysis requires exactly three samples")
	}
	d, dataHash, referenceErr := bundledReference()
	if referenceErr == nil {
		r.DataVersion, r.DataSHA256 = d.BuiltAt(), dataHash
	}
	outputs := make([]Output, 3)
	seen := make(map[string]bool, 3)
	complete := 0
	nonModel := false
	for i, s := range samples {
		if s.ChallengeID == "" || seen[s.ChallengeID] || s.ExpectedCount < 292 || s.ExpectedCount > 332 {
			return r, errors.New("invalid fingerprint challenge metadata")
		}
		seen[s.ChallengeID] = true
		q := Quality{ChallengeID: s.ChallengeID, ExpectedCount: s.ExpectedCount, MinimumNumbers: max(80, int(math.Ceil(float64(s.ExpectedCount)*0.55)))}
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
			nums := ParseNumbers(s.Response)
			q.ParsedNumbers = len(nums)
			q.OutOfRangeCount = rawOutOfRange(s.Response)
			q.Truncated = s.Truncated
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
	if referenceErr != nil {
		r.Reason = "reference_data_unavailable"
		return r, nil
	}
	a, err := d.analyze(outputs, strict)
	if err != nil {
		return r, fmt.Errorf("offline fingerprint analysis: %w", err)
	}
	r.Status, r.Reason, r.ProbabilityStatus = a.Decision, a.Evidence.Reason, a.ProbabilityStatus
	r.TopCandidates = a.Results[:min(3, len(a.Results))]
	return r, nil
}
