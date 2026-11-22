package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFingerprintCurrentResultsShareOneSixtyResultWindow(t *testing.T) {
	ctx, config, now := fingerprintSiteFixture(t)
	require.NoError(t, DB.AutoMigrate(&ModelTestRun{}, &BillingFinalization{}))
	target := config.Targets[0]
	for i := range 40 {
		require.NoError(t, DB.Create(&ModelTestRun{
			ID: fmt.Sprintf("manual-%02d", i), Source: "user", Model: target.Model, Group: target.Group,
			BenchmarkID: "local-fingerprint-v1", Status: "completed", Assessment: "scored",
			CreatedAt: now + int64(i*2), FingerprintResult: JSONValue(`{"status":"scored"}`),
		}).Error)
		require.NoError(t, DB.Create(&ModelFingerprintSiteRun{
			ID: fmt.Sprintf("site-%02d", i), TargetID: target.ID, ConfigVersion: config.Version, Window: int64(i),
			Model: target.Model, Group: target.Group, Status: "succeeded", Assessment: "partial",
			CreatedAt: now + int64(i*2+1), FingerprintResult: `{"status":"partial"}`,
		}).Error)
	}
	// Late failures are current observations too; they must displace old greens.
	require.NoError(t, DB.Create(&ModelTestRun{
		ID: "latest-error", Source: "user", Model: target.Model, Group: target.Group,
		BenchmarkID: "local-fingerprint-v1", Status: "timeout", CreatedAt: now + 80,
	}).Error)
	for range 2 {
		require.NoError(t, PruneModelFingerprintHistory(ctx, now+90))
	}
	siteRows, err := ListModelFingerprintSitePublicRuns(ctx)
	require.NoError(t, err)
	manualRows, err := ListPublicModelTestRuns(ctx)
	require.NoError(t, err)
	assert.Len(t, siteRows, 30)
	assert.Len(t, manualRows, 30)
	assert.Equal(t, "latest-error", manualRows[0].ID)
	for _, row := range siteRows {
		assert.GreaterOrEqual(t, row.CreatedAt, now+21)
	}
	for _, row := range manualRows {
		assert.GreaterOrEqual(t, row.CreatedAt, now+21)
	}
	var oldManual ModelTestRun
	var oldSite ModelFingerprintSiteRun
	require.NoError(t, DB.First(&oldManual, "id = ?", "manual-00").Error)
	require.NoError(t, DB.First(&oldSite, "id = ?", "site-00").Error)
	assert.Empty(t, oldManual.FingerprintResult)
	assert.Empty(t, oldSite.FingerprintResult)
	assert.Positive(t, oldManual.HistoryPrunedAt)
	assert.Positive(t, oldSite.HistoryPrunedAt)
}

func TestFingerprintCurrentResultsHideDisabledAndRemovedTargets(t *testing.T) {
	ctx, config, now := fingerprintSiteFixture(t)
	require.NoError(t, DB.AutoMigrate(&ModelTestRun{}))
	active := config.Targets[0]
	for i, group := range []string{active.Group, "disabled", "removed"} {
		require.NoError(t, DB.Create(&ModelTestRun{
			ID: "manual-" + group, Source: "user", Model: active.Model, Group: group,
			BenchmarkID: "local-fingerprint-v1", Status: "completed", Assessment: "scored", CreatedAt: now + int64(i),
		}).Error)
		require.NoError(t, DB.Create(&ModelFingerprintSiteRun{
			ID: "site-" + group, TargetID: active.ID, ConfigVersion: config.Version, Window: int64(i),
			Model: active.Model, Group: group, Status: "succeeded", Assessment: "scored", CreatedAt: now + int64(i),
		}).Error)
	}
	config.Targets = append(config.Targets, ModelFingerprintSiteTarget{Group: "disabled", Model: active.Model})
	config, err := UpdateModelFingerprintSiteConfig(ctx, config.Version, 7, *config)
	require.NoError(t, err)
	siteRows, err := ListModelFingerprintSitePublicRuns(ctx)
	require.NoError(t, err)
	manualRows, err := ListPublicModelTestRuns(ctx)
	require.NoError(t, err)
	require.Len(t, siteRows, 1)
	require.Len(t, manualRows, 1)
	assert.Equal(t, active.Group, siteRows[0].Group)
	assert.Equal(t, active.Group, manualRows[0].Group)
	config.Enabled = false
	_, err = UpdateModelFingerprintSiteConfig(ctx, config.Version, 7, *config)
	require.NoError(t, err)
	siteRows, err = ListModelFingerprintSitePublicRuns(ctx)
	require.NoError(t, err)
	manualRows, err = ListPublicModelTestRuns(ctx)
	require.NoError(t, err)
	assert.Empty(t, siteRows)
	assert.Empty(t, manualRows)
}

func TestFingerprintPartialAssessmentPreservesUnknownUsageAndNextPeriod(t *testing.T) {
	ctx, config, now := fingerprintSiteFixture(t)
	require.NoError(t, DB.AutoMigrate(&ModelTestRun{}))
	run, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", config.Targets[0].ID, config.Version, now)
	require.NoError(t, err)
	for _, id := range []string{"completed", "timeout-1", "timeout-2"} {
		require.NoError(t, AuthorizeModelFingerprintSiteProbe(ctx, run.ID, "fixture-task", "fixture-runner", id))
	}
	require.NoError(t, CompleteModelFingerprintSiteProbe(ctx, run.ID, "completed", 10, 20, "fixture-model"))
	result := ModelFingerprintSiteResult{
		Status: "succeeded", Assessment: "partial",
		FingerprintResult: `{"status":"partial","used_outputs":1,"top_candidates":[{"model":"fixture-model","probability":null}],"probability_status":"unavailable"}`,
	}
	require.NoError(t, FinishModelFingerprintSiteRun(ctx, run.ID, result, false))
	var saved ModelFingerprintSiteRun
	require.NoError(t, DB.First(&saved, "id = ?", run.ID).Error)
	assert.Equal(t, "unknown", saved.UsageState)
	assert.Equal(t, "unknown", saved.Status)
	assert.Equal(t, "partial", saved.Assessment)
	var report map[string]any
	require.NoError(t, common.UnmarshalJsonStr(saved.FingerprintResult, &report))
	assert.Equal(t, float64(1), report["used_outputs"])
	after, err := GetModelFingerprintSiteConfig(ctx)
	require.NoError(t, err)
	assert.True(t, after.AutomaticEnabled)
	assert.Equal(t, config.Version, after.Version)
	_, err = ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", config.Targets[0].ID, config.Version, now)
	assert.ErrorIs(t, err, ErrFingerprintSiteNotDue, "partial outcomes never replay in the same interval")
	_, err = ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", config.Targets[0].ID, config.Version, now+3600)
	require.NoError(t, err, "the next scheduled interval remains eligible")
	var probes []ModelFingerprintSiteProbe
	require.NoError(t, DB.Where("run_id = ? AND status = ?", run.ID, "dispatching").Find(&probes).Error)
	require.Len(t, probes, 2)
	for _, probe := range probes {
		assert.Zero(t, probe.PromptTokens)
		assert.Zero(t, probe.CompletionTokens)
	}
}
