package modelfingerprint

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func referenceSamples(t *testing.T) []Sample {
	t.Helper()
	data, err := os.ReadFile("testdata/reference_samples.json")
	require.NoError(t, err)
	var fixture []struct {
		ChallengeID   string `json:"challenge_id"`
		ExpectedCount int    `json:"expected_count"`
		Response      string `json:"response"`
	}
	require.NoError(t, common.Unmarshal(data, &fixture))
	samples := make([]Sample, len(fixture))
	for i, s := range fixture {
		samples[i] = Sample{ChallengeID: s.ChallengeID, ExpectedCount: s.ExpectedCount, Response: s.Response}
	}
	return samples
}

func TestBundledReferenceAndScoredResult(t *testing.T) {
	require.NoError(t, Ready())
	r, err := Analyze(referenceSamples(t), true)
	require.NoError(t, err)
	require.Equal(t, "scored", r.Status)
	require.Len(t, r.TopCandidates, 3)
	assert.Equal(t, "claude-haiku-4.5", r.TopCandidates[0].Model)
	assert.Equal(t, "reference_calibrated", r.ProbabilityStatus)
	require.NotNil(t, r.TopCandidates[0].Probability)
	assert.GreaterOrEqual(t, *r.TopCandidates[0].Probability, 0.0)
	assert.LessOrEqual(t, *r.TopCandidates[0].Probability, 1.0)
	assert.Equal(t, referenceDetectorSHA256, r.DataSHA256)
	assert.NotEmpty(t, r.DataVersion)
	assert.Equal(t, 3, r.UsedOutputs)
	assert.Contains(t, r.ProbabilityNote, "not proof")
}

func TestPartialAndStrictThree(t *testing.T) {
	samples := referenceSamples(t)
	samples[2].Response = ""
	samples[2].Error = "upstream failed"
	r, err := Analyze(samples, false)
	require.NoError(t, err)
	assert.Equal(t, "partial", r.Status)
	require.Len(t, r.TopCandidates, 3)
	for _, c := range r.TopCandidates {
		assert.Nil(t, c.Probability)
	}
	assert.Equal(t, "unavailable", r.ProbabilityStatus)
	strict, err := Analyze(samples, true)
	require.NoError(t, err)
	assert.Equal(t, "unscorable", strict.Status)
	assert.Empty(t, strict.TopCandidates)
}

func TestThresholdAndTruncation(t *testing.T) {
	for _, count := range []int{160, 161, 291, 292} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			samples := referenceSamples(t)
			samples[0].ExpectedCount = 292
			numbers := ParseNumbers(samples[0].Response)
			samples[0].Response = joinNumbers(numbers[:count])
			r, err := Analyze(samples, false)
			require.NoError(t, err)
			assert.Equal(t, 161, r.SampleQuality[0].MinimumNumbers)
			assert.Equal(t, count >= 161, r.SampleQuality[0].Accepted)
			assert.Equal(t, count < 292, r.SampleQuality[0].Short)
			assert.False(t, r.SampleQuality[0].Truncated)
			strict, err := Analyze(samples, true)
			require.NoError(t, err)
			if count < 161 {
				assert.Equal(t, "unscorable", strict.Status)
			} else {
				assert.Equal(t, "scored", strict.Status)
			}
		})
	}
}

func TestStrictRejectsTransportTruncation(t *testing.T) {
	samples := referenceSamples(t)
	samples[0].Truncated = true
	r, err := Analyze(samples, true)
	require.NoError(t, err)
	assert.Equal(t, "unscorable", r.Status)
	assert.True(t, r.SampleQuality[0].Truncated)
	assert.Empty(t, r.TopCandidates)
}

func TestNonModelAndInvalidSamples(t *testing.T) {
	samples := referenceSamples(t)
	ordered := make([]int, 332)
	for i := range ordered {
		ordered[i] = i + 1
	}
	for i := range samples {
		samples[i].Response = joinNumbers(ordered)
	}
	r, err := Analyze(samples, true)
	require.NoError(t, err)
	assert.Equal(t, "not_model", r.Status)
	assert.Empty(t, r.TopCandidates)
	for i := range samples {
		samples[i].Response = "Hello, I cannot provide that."
	}
	r, err = Analyze(samples, false)
	require.NoError(t, err)
	assert.Equal(t, "unscorable", r.Status)
	assert.Empty(t, r.TopCandidates)
	samples[0].Response = strings.Repeat("1", MaxResponseBytes+1)
	r, err = Analyze(samples, false)
	require.NoError(t, err)
	assert.Equal(t, "response_too_large", r.SampleQuality[0].Reason)
	_, err = Analyze(samples[:2], false)
	require.Error(t, err)
	samples[1].ChallengeID = samples[0].ChallengeID
	_, err = Analyze(samples, false)
	require.Error(t, err)
}

func TestDataTamperFailsClosed(t *testing.T) {
	corrupted := []byte(referenceDetector)
	corrupted[len(corrupted)-1] ^= 1
	_, err := loadReference([]byte(referenceBank), corrupted, referenceBankSHA256, referenceDetectorSHA256)
	require.ErrorContains(t, err, "checksum mismatch")
	_, err = loadReference([]byte(referenceBank), []byte(referenceDetector), "invalid", referenceDetectorSHA256)
	require.Error(t, err)
}

func TestGenerateExactlyThreeChallenges(t *testing.T) {
	challenges, err := GenerateChallenges()
	require.NoError(t, err)
	require.Len(t, challenges, 3)
	ids, counts := map[string]bool{}, map[int]bool{}
	for _, c := range challenges {
		assert.NotEmpty(t, c.Prompt)
		assert.NotEmpty(t, c.ID)
		assert.False(t, ids[c.ID])
		ids[c.ID] = true
		assert.False(t, counts[c.ExpectedCount])
		counts[c.ExpectedCount] = true
		assert.GreaterOrEqual(t, c.ExpectedCount, 292)
		assert.LessOrEqual(t, c.ExpectedCount, 332)
		assert.Contains(t, c.Prompt, strconv.Itoa(c.ExpectedCount))
	}
}
