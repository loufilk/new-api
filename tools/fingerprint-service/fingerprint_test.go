package main

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func referenceSamples(t *testing.T) []Sample {
	t.Helper()
	data, err := os.ReadFile("testdata/reference_samples.json")
	require.NoError(t, err)
	var samples []Sample
	require.NoError(t, json.Unmarshal(data, &samples))
	return samples
}

func referenceEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := LoadEngine(context.Background(), "data")
	require.NoError(t, err)
	return engine
}

func TestOfficialSDKScoredResult(t *testing.T) {
	engine := referenceEngine(t)
	result, err := engine.Analyze(referenceSamples(t), true)
	require.NoError(t, err)
	require.Equal(t, "scored", result.Status)
	require.Len(t, result.TopCandidates, 3)
	assert.Equal(t, "claude-haiku-4.5", result.TopCandidates[0].Model)
	assert.Equal(t, "reference_calibrated", result.ProbabilityStatus)
	require.NotNil(t, result.TopCandidates[0].Probability)
	assert.GreaterOrEqual(t, *result.TopCandidates[0].Probability, 0.0)
	assert.LessOrEqual(t, *result.TopCandidates[0].Probability, 1.0)
	assert.Equal(t, referenceDetectorSHA256, result.DataSHA256)
	assert.NotEmpty(t, result.DataVersion)
	assert.Equal(t, SDKCommit, result.SDKCommit)
	assert.Equal(t, 3, result.UsedOutputs)
	assert.Contains(t, result.ProbabilityNote, "not proof")
	for _, quality := range result.SampleQuality {
		assert.True(t, quality.Accepted)
		assert.False(t, quality.Truncated)
	}
}

func TestPartialAndStrictThree(t *testing.T) {
	engine := referenceEngine(t)
	for _, accepted := range []int{1, 2} {
		t.Run(strconv.Itoa(accepted)+"_valid_responses", func(t *testing.T) {
			samples := referenceSamples(t)
			for i := accepted; i < len(samples); i++ {
				samples[i].Response = ""
				samples[i].Error = "request_timeout"
			}
			partial, err := engine.Analyze(samples, false)
			require.NoError(t, err)
			assert.Equal(t, "partial", partial.Status)
			assert.Equal(t, accepted, partial.UsedOutputs)
			require.Len(t, partial.TopCandidates, 3)
			for _, candidate := range partial.TopCandidates {
				assert.Nil(t, candidate.Probability)
			}
			assert.Equal(t, "unavailable", partial.ProbabilityStatus)
			strict, err := engine.Analyze(samples, true)
			require.NoError(t, err)
			assert.Equal(t, "unscorable", strict.Status)
			assert.Equal(t, "strict3_requires_three_complete_samples", strict.Reason)
			assert.Empty(t, strict.TopCandidates)
		})
	}
}

func TestThresholdAndTruncation(t *testing.T) {
	engine := referenceEngine(t)
	for _, count := range []int{160, 161, 291, 292} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			samples := referenceSamples(t)
			samples[0].ExpectedCount = 292
			numbers := lmfpd.ParseNumbers(samples[0].Response)
			samples[0].Response = joinNumbers(numbers[:count])
			result, err := engine.Analyze(samples, false)
			require.NoError(t, err)
			assert.Equal(t, 161, result.SampleQuality[0].MinimumNumbers)
			assert.Equal(t, count >= 161, result.SampleQuality[0].Accepted)
			assert.Equal(t, count < 292, result.SampleQuality[0].Short)
			strict, err := engine.Analyze(samples, true)
			require.NoError(t, err)
			if count < 161 {
				assert.Equal(t, "unscorable", strict.Status)
			} else {
				assert.Equal(t, "scored", strict.Status)
			}
		})
	}
	samples := referenceSamples(t)
	samples[0].Truncated = true
	result, err := engine.Analyze(samples, true)
	require.NoError(t, err)
	assert.Equal(t, "unscorable", result.Status)
	assert.True(t, result.SampleQuality[0].Truncated)
	assert.Empty(t, result.TopCandidates)
}

func TestNonModelAndFailedSamples(t *testing.T) {
	engine := referenceEngine(t)
	ordered := make([]int, 332)
	for i := range ordered {
		ordered[i] = i + 1
	}
	for _, test := range []struct {
		name   string
		sample Sample
		strict bool
		status string
		reason string
	}{
		{name: "program generated", sample: Sample{Response: joinNumbers(ordered)}, strict: true, status: "not_model"},
		{name: "no numbers", sample: Sample{Response: "I cannot provide that."}, status: "unscorable", reason: "insufficient_numbers"},
		{name: "explicit non model", sample: Sample{NonModel: true}, status: "not_model", reason: "non_model_response"},
		{name: "safe error", sample: Sample{Error: "synthetic-secret-must-not-escape", Response: "synthetic-private-response"}, status: "unscorable", reason: "request_failed"},
		{name: "oversize", sample: Sample{Response: strings.Repeat("1", MaxResponseBytes+1)}, status: "unscorable", reason: "response_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			samples := referenceSamples(t)
			for i := range samples {
				testSample := test.sample
				testSample.ChallengeID, testSample.ExpectedCount = samples[i].ChallengeID, samples[i].ExpectedCount
				samples[i] = testSample
			}
			result, err := engine.Analyze(samples, test.strict)
			require.NoError(t, err)
			assert.Equal(t, test.status, result.Status)
			assert.Empty(t, result.TopCandidates)
			if test.reason != "" {
				assert.Equal(t, test.reason, result.SampleQuality[0].Reason)
			}
			data, err := json.Marshal(result)
			require.NoError(t, err)
			assert.NotContains(t, string(data), "synthetic-")
		})
	}
}

func TestInvalidSampleMetadata(t *testing.T) {
	engine := referenceEngine(t)
	for _, test := range []struct {
		name string
		edit func([]Sample) []Sample
	}{
		{"two samples", func(s []Sample) []Sample { return s[:2] }},
		{"four samples", func(s []Sample) []Sample { return append(s, s[0]) }},
		{"duplicate id", func(s []Sample) []Sample { s[1].ChallengeID = s[0].ChallengeID; return s }},
		{"empty id", func(s []Sample) []Sample { s[0].ChallengeID = ""; return s }},
		{"long id", func(s []Sample) []Sample { s[0].ChallengeID = strings.Repeat("x", 129); return s }},
		{"unsafe id", func(s []Sample) []Sample { s[0].ChallengeID = "private\nmessage"; return s }},
		{"low expected", func(s []Sample) []Sample { s[0].ExpectedCount = 291; return s }},
		{"high expected", func(s []Sample) []Sample { s[0].ExpectedCount = 333; return s }},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := engine.Analyze(test.edit(referenceSamples(t)), true)
			require.ErrorIs(t, err, errInvalidSamples)
		})
	}
}

func TestQualityReportsNumericOutliers(t *testing.T) {
	samples := referenceSamples(t)
	samples[0].Response += ", -1, 0, 356, 999999999999999999999999999, 0000355"
	result, err := referenceEngine(t).Analyze(samples, true)
	require.NoError(t, err)
	assert.Equal(t, 4, result.SampleQuality[0].OutOfRangeCount)
	assert.True(t, result.SampleQuality[0].Accepted)
}

func TestGenerateExactlyThreeChallenges(t *testing.T) {
	challenges, err := GenerateChallenges()
	require.NoError(t, err)
	require.Len(t, challenges, 3)
	ids, counts := map[string]bool{}, map[int]bool{}
	for _, challenge := range challenges {
		assert.True(t, validChallengeID(challenge.ID))
		assert.NotEmpty(t, challenge.Prompt)
		assert.False(t, ids[challenge.ID])
		ids[challenge.ID] = true
		assert.False(t, counts[challenge.ExpectedCount])
		counts[challenge.ExpectedCount] = true
		assert.GreaterOrEqual(t, challenge.ExpectedCount, 292)
		assert.LessOrEqual(t, challenge.ExpectedCount, 332)
		assert.Contains(t, challenge.Prompt, strconv.Itoa(challenge.ExpectedCount))
	}
}
