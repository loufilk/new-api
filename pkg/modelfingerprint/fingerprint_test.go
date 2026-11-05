package modelfingerprint

import (
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type handlerTransport struct{ handler http.Handler }

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func testService(t *testing.T, handler http.Handler) {
	t.Helper()
	previous := serviceTransport
	t.Cleanup(func() { serviceTransport = previous })
	serviceTransport = func(socket string) http.RoundTripper { return handlerTransport{handler: handler} }
	t.Setenv("MODEL_FINGERPRINT_SOCKET", "/test-only/fingerprint.sock")
}

func TestProductionTransportHasNoProxyAndUsesBoundedHeaders(t *testing.T) {
	transport := unixServiceTransport("/test-only/fingerprint.sock").(*http.Transport)
	assert.Nil(t, transport.Proxy)
	assert.True(t, transport.DisableKeepAlives)
	assert.EqualValues(t, 8*1024, transport.MaxResponseHeaderBytes)
	require.NotNil(t, transport.DialContext)
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	b, err := common.Marshal(value)
	require.NoError(t, err)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func testHealth() map[string]any {
	return map[string]any{"protocol_version": 1, "sdk_commit": SDKCommit, "data_version": "2026-11-03T12:06:39.864352+00:00", "data_sha256": referenceDetectorSHA256}
}

func testSamples() []Sample {
	return []Sample{{ChallengeID: "probe-1", ExpectedCount: 300, Response: "1, 2, 3"}, {ChallengeID: "probe-2", ExpectedCount: 301, Response: "4, 5, 6"}, {ChallengeID: "probe-3", ExpectedCount: 302, Response: "7, 8, 9"}}
}

func testResult(samples []Sample, strict bool) Result {
	r := Result{Status: "scored", Reason: "test fixture", TopCandidates: []Candidate{{Model: "fixture-model", DisplayName: "fixture-model", Family: "fixture", FamilyName: "Fixture", Score: 0.5, Probability: new(0.8)}}, SampleQuality: []Quality{}, DataVersion: "2026-11-03T12:06:39.864352+00:00", DataSHA256: referenceDetectorSHA256, SDKCommit: SDKCommit, ProbabilityNote: ProbabilityNote, ProbabilityStatus: "reference_calibrated", UsedOutputs: 3, Strict: strict}
	for _, s := range samples {
		r.SampleQuality = append(r.SampleQuality, Quality{ChallengeID: s.ChallengeID, ExpectedCount: s.ExpectedCount, ParsedNumbers: s.ExpectedCount, MinimumNumbers: max(80, int(math.Ceil(float64(s.ExpectedCount)*0.55))), Accepted: true, Reason: "accepted"})
	}
	return r
}

func TestReadyFailureIsRecoverableAndIgnoresProxy(t *testing.T) {
	t.Setenv("MODEL_FINGERPRINT_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	require.ErrorIs(t, Ready(), ErrUnavailable)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/ready", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Empty(t, r.Header.Get("Authorization"))
		writeTestJSON(t, w, testHealth())
	}))
	require.NoError(t, Ready())
}

func TestReadyRejectsWrongIdentityUnknownFieldsAndRedirects(t *testing.T) {
	for _, name := range []string{"version", "sdk", "data", "unknown", "redirect", "status", "content_type", "trailing", "oversized"} {
		t.Run(name, func(t *testing.T) {
			testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				value := testHealth()
				switch name {
				case "version":
					value["protocol_version"] = 2
				case "sdk":
					value["sdk_commit"] = "other"
				case "data":
					value["data_sha256"] = "other"
				case "unknown":
					value["api_key"] = "must-not-be-accepted"
				case "redirect":
					w.Header().Set("Location", "https://lm.ikale.io/api/proxy")
					w.WriteHeader(http.StatusFound)
					return
				case "status":
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte("private upstream error"))
					return
				case "content_type":
					_, _ = w.Write([]byte("not-json"))
					return
				case "trailing":
					writeTestJSON(t, w, value)
					_, _ = w.Write([]byte("{}"))
					return
				case "oversized":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(strings.Repeat(" ", maxServiceResponseBytes+1)))
					return
				}
				writeTestJSON(t, w, value)
			}))
			err := Ready()
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private upstream error")
		})
	}
}

func TestGenerateChallengesValidatesServiceContract(t *testing.T) {
	for _, name := range []string{"valid", "count", "duplicate_id", "duplicate_count", "invalid_id", "range", "empty_prompt"} {
		t.Run(name, func(t *testing.T) {
			values := []Challenge{{ID: "a", ExpectedCount: 292, Prompt: "Write 292 integers."}, {ID: "b", ExpectedCount: 300, Prompt: "Write 300 integers."}, {ID: "c", ExpectedCount: 332, Prompt: "Write 332 integers."}}
			switch name {
			case "count":
				values = values[:2]
			case "duplicate_id":
				values[1].ID = values[0].ID
			case "duplicate_count":
				values[1].ExpectedCount = values[0].ExpectedCount
			case "invalid_id":
				values[1].ID = "bad\nvalue"
			case "range":
				values[1].ExpectedCount = 999
			case "empty_prompt":
				values[1].Prompt = ""
			}
			testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1/challenges", r.URL.Path)
				writeTestJSON(t, w, map[string]any{"challenges": values})
			}))
			result, err := GenerateChallenges()
			if name == "valid" {
				require.NoError(t, err)
				assert.Equal(t, values, result)
			} else {
				require.Error(t, err)
				assert.Nil(t, result)
			}
		})
	}
}

func TestAnalyzeSendsOnlyProbeDataAndNormalizedErrors(t *testing.T) {
	samples := testSamples()
	samples[1].Error = "private error sk-test-secret"
	samples[1].Response = "must-not-send-failed-response"
	samples[2].NonModel = true
	samples[2].Response = "must-not-send-intercepted-response"
	testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/analyze", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("Cookie"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "sk-test-secret")
		assert.NotContains(t, string(body), "must-not-send")
		assert.Contains(t, string(body), "request_failed")
		assert.Contains(t, string(body), "1, 2, 3")
		var payload map[string]any
		require.NoError(t, common.Unmarshal(body, &payload))
		assert.Len(t, payload, 2)
		result := testResult(samples, false)
		result.Status, result.UsedOutputs, result.ProbabilityStatus = "partial", 1, "unavailable"
		result.TopCandidates[0].Probability = nil
		for i := 1; i < 3; i++ {
			result.SampleQuality[i].Accepted = false
			result.SampleQuality[i].ParsedNumbers = 0
		}
		result.ProbabilityNote = "untrusted service prose"
		writeTestJSON(t, w, result)
	}))
	result, err := Analyze(samples, false)
	require.NoError(t, err)
	assert.Equal(t, ProbabilityNote, result.ProbabilityNote)
	bytes, err := common.Marshal(samples)
	require.NoError(t, err)
	assert.NotContains(t, string(bytes), "1, 2, 3")
}

func TestAnalyzeRejectsInvalidInputsBeforeDispatch(t *testing.T) {
	for _, name := range []string{"count", "id", "duplicate", "expected", "large"} {
		t.Run(name, func(t *testing.T) {
			samples := testSamples()
			switch name {
			case "count":
				samples = samples[:2]
			case "id":
				samples[0].ChallengeID = "bad secret"
			case "duplicate":
				samples[1].ChallengeID = samples[0].ChallengeID
			case "expected":
				samples[0].ExpectedCount = 1
			case "large":
				samples[0].Response = strings.Repeat("1", MaxResponseBytes+1)
			}
			t.Setenv("MODEL_FINGERPRINT_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
			_, err := Analyze(samples, true)
			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrUnavailable)
		})
	}
}

func TestAnalyzeRejectsMalformedResults(t *testing.T) {
	for _, name := range []string{"sdk", "hash", "strict", "count", "probability", "quality", "quality_count", "partial_probability", "unknown"} {
		t.Run(name, func(t *testing.T) {
			samples := testSamples()
			result := testResult(samples, true)
			switch name {
			case "sdk":
				result.SDKCommit = "other"
			case "hash":
				result.DataSHA256 = "other"
			case "strict":
				result.Strict = false
			case "count":
				result.UsedOutputs = 4
			case "probability":
				result.TopCandidates[0].Probability = new(1.1)
			case "quality":
				result.SampleQuality[0].ChallengeID = "other"
			case "quality_count":
				result.SampleQuality[0].Accepted = false
			case "partial_probability":
				result.Status = "partial"
				result.UsedOutputs = 2
			case "unknown":
				result.Status = "trusted"
			}
			testService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeTestJSON(t, w, result) }))
			got, err := Analyze(samples, true)
			require.ErrorIs(t, err, errInvalidServiceResponse)
			assert.Empty(t, got.TopCandidates)
		})
	}
}

func TestOfficialSidecarIntegration(t *testing.T) {
	socket := os.Getenv("MODEL_FINGERPRINT_INTEGRATION_SOCKET")
	if socket == "" {
		t.Skip("set MODEL_FINGERPRINT_INTEGRATION_SOCKET to exercise the separate official SDK service")
	}
	t.Setenv("MODEL_FINGERPRINT_SOCKET", socket)
	require.NoError(t, Ready())
	challenges, err := GenerateChallenges()
	require.NoError(t, err)
	require.Len(t, challenges, 3)
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
	result, err := Analyze(samples, true)
	require.NoError(t, err)
	assert.Equal(t, "scored", result.Status)
	require.NotEmpty(t, result.TopCandidates)
	assert.Equal(t, "claude-haiku-4.5", result.TopCandidates[0].Model)
	samples[2].Error = "not_dispatched"
	samples[2].Response = ""
	partial, err := Analyze(samples, false)
	require.NoError(t, err)
	assert.Equal(t, "partial", partial.Status)
	assert.Equal(t, "unavailable", partial.ProbabilityStatus)
	strict, err := Analyze(samples, true)
	require.NoError(t, err)
	assert.Equal(t, "unscorable", strict.Status)
}
