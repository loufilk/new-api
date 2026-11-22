package controller

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The controller suite exercises its scoring contract, not the SDK algorithm or
// Unix-socket transport. Those have independent package/service tests. A real
// integration environment can explicitly supply MODEL_FINGERPRINT_SOCKET.
func setupFingerprintScoringFixture(t *testing.T) {
	t.Helper()
	if os.Getenv("MODEL_FINGERPRINT_SOCKET") != "" {
		require.NoError(t, modelFingerprintScorer.ready(), "explicit scoring service must be available")
		return
	}
	old := modelFingerprintScorer
	t.Cleanup(func() { modelFingerprintScorer = old })
	modelFingerprintScorer.ready = func() error { return nil }
	modelFingerprintScorer.challenges = func() ([]modelfingerprint.Challenge, error) {
		return []modelfingerprint.Challenge{
			{ID: "fixture-292", ExpectedCount: 292, Prompt: "Provide 292 random numbers in [0, 1)."},
			{ID: "fixture-312", ExpectedCount: 312, Prompt: "Provide 312 random numbers in [0, 1)."},
			{ID: "fixture-332", ExpectedCount: 332, Prompt: "Provide 332 random numbers in [0, 1)."},
		}, nil
	}
	modelFingerprintScorer.analyze = func(samples []modelfingerprint.Sample, strict bool) (modelfingerprint.Result, error) {
		require.Len(t, samples, 3)
		result := modelfingerprint.Result{Status: "unscorable", Reason: "controller_fixture", TopCandidates: []modelfingerprint.Candidate{}, SampleQuality: []modelfingerprint.Quality{}, DataVersion: "controller-fixture", DataSHA256: "59dffad8128dca8bcf0cfb024f0fcebe7f0b7d7fbae3583b4b5d1418c18e601f", SDKCommit: modelfingerprint.SDKCommit, ProbabilityStatus: "unavailable", Strict: strict}
		for _, sample := range samples {
			result.SampleQuality = append(result.SampleQuality, modelfingerprint.Quality{ChallengeID: sample.ChallengeID, ExpectedCount: sample.ExpectedCount, MinimumNumbers: max(80, int(math.Ceil(float64(sample.ExpectedCount)*0.55))), Reason: "controller_fixture"})
		}
		return result, nil
	}
}

func TestFingerprintPostgresUnavailableScorerStopsBeforeDispatch(t *testing.T) {
	db, userID, task, target, run := setupFingerprintSiteExecution(t)
	body := modelTestRequestBody(t, userID, uuid.NewString())
	ready := modelFingerprintScorer.ready
	t.Cleanup(func() { modelFingerprintScorer.ready = ready })
	modelFingerprintScorer.ready = func() error { return modelfingerprint.ErrUnavailable }
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", userID); c.Next() })
	engine.POST("/runs", PrepareModelTest, func(c *gin.Context) { t.Error("unavailable scorer must not admit a paid request") })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), "fingerprint_unavailable")
	var manualRuns, probes int64
	require.NoError(t, db.Model(&model.ModelTestRun{}).Count(&manualRuns).Error)
	assert.Zero(t, manualRuns)
	require.Error(t, executeSiteModelFingerprint(context.Background(), task, task.LockedBy, target, run))
	require.NoError(t, db.Model(&model.ModelFingerprintSiteProbe{}).Count(&probes).Error)
	assert.Zero(t, probes, "automatic tests must not dispatch before the scoring service is ready")
	var saved model.ModelFingerprintSiteRun
	require.NoError(t, db.First(&saved, "id = ?", run.ID).Error)
	assert.Equal(t, "failed", saved.Status)
	assert.Equal(t, "fingerprint_unavailable", saved.Assessment)
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	assert.Equal(t, 1_000_000, user.QuotaPaid)
	modelFingerprintScorer.ready = ready
	require.NoError(t, modelFingerprintScorer.ready(), "temporary failures must not be cached after the scorer recovers")
}

func TestFingerprintControllerScoringFixture(t *testing.T) {
	setupFingerprintScoringFixture(t)
	challenges, err := modelFingerprintScorer.challenges()
	require.NoError(t, err)
	require.Len(t, challenges, 3)
	samples := make([]modelfingerprint.Sample, len(challenges))
	for i, challenge := range challenges {
		samples[i] = modelfingerprint.Sample{ChallengeID: challenge.ID, ExpectedCount: challenge.ExpectedCount, Response: "0.1, 0.2, 0.3"}
	}
	result, err := modelFingerprintScorer.analyze(samples, true)
	require.NoError(t, err)
	assert.Equal(t, "unscorable", result.Status)
}
