package model

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFingerprintRetentionPrunesReportsPreservesReplayAndAccounting(t *testing.T) {
	ctx, config, now := fingerprintSiteFixture(t)
	config.Targets = append(config.Targets, ModelFingerprintSiteTarget{Group: "default", Model: "synthetic", Enabled: true})
	config, err := UpdateModelFingerprintSiteConfig(ctx, config.Version, 7, *config)
	require.NoError(t, err)
	require.NoError(t, DB.AutoMigrate(&ModelTestRun{}, &User{}, &Token{}, &BillingFinalization{}))
	user := User{Id: 101, Username: "retention-owner", Status: common.UserStatusEnabled, Quota: 1000, QuotaPaid: 1000}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{Id: 102, UserId: user.Id, RemainQuota: 1000, Key: "synthetic-retention-token"}
	require.NoError(t, DB.Create(&token).Error)
	for i := range 65 {
		confirmation := fmt.Sprintf("confirmation-%d", i)
		run := ModelTestRun{ID: fmt.Sprintf("manual-%02d", i), Source: "user", UserID: user.Id, TokenID: token.Id, ConfirmationID: &confirmation, Model: "synthetic", Group: "default", BenchmarkID: "local-fingerprint-v1", Status: "completed", Assessment: "scored", CreatedAt: now - int64(100-i), FingerprintResult: JSONValue(`{"status":"scored"}`), SamplesJSON: `[{"request_id":"preserved-audit"}]`}
		require.NoError(t, DB.Create(&run).Error)
	}
	for i := range 7 {
		require.NoError(t, DB.Create(&ModelTestRun{ID: fmt.Sprintf("failure-%02d", i), Source: "user", UserID: user.Id, Model: "synthetic", Group: "default", BenchmarkID: "local-fingerprint-v1", Status: "failed", CreatedAt: now - int64(100-i), FingerprintResult: JSONValue(`{"status":"unscorable"}`)}).Error)
	}
	running := ModelTestRun{ID: "still-running", Source: "user", UserID: 999, Model: "synthetic", Group: "default", BenchmarkID: "local-fingerprint-v1", Status: "running", CreatedAt: now - 500, ExpiresAt: now + 30, FingerprintResult: JSONValue(`{"status":"running"}`)}
	legacy := ModelTestRun{ID: "legacy-artwork", Source: "user", UserID: 999, Model: "synthetic", Group: "default", BenchmarkID: "pelican-static-v1", Status: "completed", CreatedAt: now - 100000, FingerprintResult: JSONValue(`{"legacy":true}`), ArtworkHTML: "legacy-preserved"}
	require.NoError(t, DB.Create(&running).Error)
	require.NoError(t, DB.Create(&legacy).Error)
	for i := range 62 {
		require.NoError(t, DB.Create(&ModelFingerprintSiteRun{ID: fmt.Sprintf("site-%02d", i), TargetID: config.Targets[0].ID, ConfigVersion: config.Version, Window: int64(i), Group: "default", Model: "synthetic", Status: "succeeded", Assessment: "scored", CreatedAt: now - int64(100-i), FingerprintResult: `{"status":"scored"}`}).Error)
	}
	probe := ModelFingerprintSiteProbe{RunID: "site-00", ProbeID: "retained-probe", RequestID: "preserved-site-request", Status: "completed", PromptTokens: 12, CompletionTokens: 10}
	require.NoError(t, DB.Create(&probe).Error)
	receipt := BillingFinalization{RequestID: "preserved-audit", UserID: user.Id, TokenID: token.Id, State: BillingSettlementReview, ActualQuota: 77}
	require.NoError(t, DB.Create(&receipt).Error)

	for range 2 {
		require.NoError(t, PruneModelFingerprintHistory(ctx, now))
	}
	var old ModelTestRun
	require.NoError(t, DB.First(&old, "id = ?", "manual-00").Error)
	assert.Empty(t, old.FingerprintResult, "the old report is actually removed from storage")
	assert.Positive(t, old.HistoryPrunedAt)
	assert.Equal(t, `[{"request_id":"preserved-audit"}]`, old.SamplesJSON)
	require.NotNil(t, old.ConfirmationID)
	var manualCount, failureCount int64
	require.NoError(t, DB.Model(&ModelTestRun{}).Where("model = ? AND benchmark_id = ? AND status = ? AND history_pruned_at = 0", "synthetic", "local-fingerprint-v1", "completed").Count(&manualCount).Error)
	require.NoError(t, DB.Model(&ModelTestRun{}).Where("model = ? AND status = ? AND history_pruned_at = 0", "synthetic", "failed").Count(&failureCount).Error)
	assert.Zero(t, failureCount, "old failures are not retained in a second history window")
	var siteOld ModelFingerprintSiteRun
	require.NoError(t, DB.First(&siteOld, "id = ?", "site-00").Error)
	assert.Empty(t, siteOld.FingerprintResult)
	assert.Positive(t, siteOld.HistoryPrunedAt)
	assert.Equal(t, config.Targets[0].ID, siteOld.TargetID)
	assert.Equal(t, config.Version, siteOld.ConfigVersion)
	var savedProbe ModelFingerprintSiteProbe
	require.NoError(t, DB.First(&savedProbe, probe.ID).Error)
	assert.Equal(t, probe, savedProbe)
	var siteCount int64
	require.NoError(t, DB.Model(&ModelFingerprintSiteRun{}).Where("history_pruned_at = 0").Count(&siteCount).Error)
	assert.EqualValues(t, 60, siteCount+manualCount, "manual and automatic reports share the same window")
	for _, original := range []ModelTestRun{running, legacy} {
		var saved ModelTestRun
		require.NoError(t, DB.First(&saved, "id = ?", original.ID).Error)
		assert.Equal(t, original.FingerprintResult, saved.FingerprintResult)
		assert.Equal(t, original.ArtworkHTML, saved.ArtworkHTML)
		assert.Zero(t, saved.HistoryPrunedAt)
	}
	// A pruned result still consumes the same one-use confirmation and hourly slot.
	replay := old
	replayed, err := CreateModelTestRun(&replay)
	require.NoError(t, err)
	assert.True(t, replayed)
	copy := old
	copy.ID, copy.Status = "reused-confirmation", "running"
	_, err = CreateModelTestRun(&copy)
	assert.ErrorIs(t, err, ErrModelTestConflict)
	copy.ConfirmationID = nil
	_, err = CreateModelTestRun(&copy)
	assert.ErrorIs(t, err, ErrModelTestLimit)
	var afterUser User
	var afterToken Token
	var afterReceipt BillingFinalization
	require.NoError(t, DB.First(&afterUser, user.Id).Error)
	require.NoError(t, DB.First(&afterToken, token.Id).Error)
	require.NoError(t, DB.First(&afterReceipt, "request_id = ?", receipt.RequestID).Error)
	assert.Equal(t, user.QuotaPaid, afterUser.QuotaPaid)
	assert.Equal(t, token.RemainQuota, afterToken.RemainQuota)
	assert.Equal(t, receipt.State, afterReceipt.State)
	assert.Equal(t, receipt.ActualQuota, afterReceipt.ActualQuota)
}

func TestFingerprintRetentionConcurrentCompletionKeepsNewestSixty(t *testing.T) {
	_, _, now := fingerprintSiteFixture(t)
	require.NoError(t, DB.AutoMigrate(&ModelTestRun{}, &BillingFinalization{}))
	for i := range 65 {
		require.NoError(t, DB.Create(&ModelTestRun{ID: fmt.Sprintf("concurrent-%02d", i), Source: "user", UserID: 1, Model: "synthetic", Group: "default", BenchmarkID: "local-fingerprint-v1", Status: "completed", Assessment: "scored", CreatedAt: now - int64(100-i), FingerprintResult: JSONValue(`{"status":"scored"}`)}).Error)
	}
	latest := &ModelTestRun{ID: "latest", Source: "user", UserID: 1, Model: "synthetic", Group: "default", BenchmarkID: "local-fingerprint-v1", Status: "running", CreatedAt: now}
	require.NoError(t, DB.Create(latest).Error)
	latest.Status, latest.Assessment, latest.FingerprintResult = "completed", "scored", JSONValue(`{"status":"scored"}`)
	var wg sync.WaitGroup
	failures := make(chan error, 3)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); failures <- PruneModelFingerprintHistory(context.Background(), now) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); failures <- FinishModelTestRun(latest) }()
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, DB.Model(&ModelTestRun{}).Where("history_pruned_at = 0").Count(&count).Error)
	assert.EqualValues(t, 60, count)
	var saved ModelTestRun
	require.NoError(t, DB.First(&saved, "id = ?", latest.ID).Error)
	assert.Zero(t, saved.HistoryPrunedAt)
	assert.NotEmpty(t, saved.FingerprintResult)
}

func TestFingerprintRetentionAnonymousTombstoneRejectsReuse(t *testing.T) {
	_, _, now := fingerprintSiteFixture(t)
	require.NoError(t, DB.AutoMigrate(&ModelTestRun{}, &User{}, &BillingFinalization{}))
	owner := User{Id: 101, Username: "tombstone-owner", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&owner).Error)
	confirmation := "permanent-one-use-confirmation"
	run := ModelTestRun{ID: "expired-result", Source: "user", UserID: owner.Id, TokenID: 102, ConfirmationID: &confirmation, Model: "synthetic", Group: "default", BenchmarkID: "local-fingerprint-v1", Status: "completed", Assessment: "scored", CreatedAt: now - 86401, FingerprintResult: JSONValue(`{"status":"scored"}`), SamplesJSON: `[{"request_id":"settled-request"}]`}
	require.NoError(t, DB.Create(&run).Error)
	require.NoError(t, DB.Create(&BillingFinalization{RequestID: "settled-request", UserID: owner.Id, TokenID: run.TokenID, State: BillingSettled}).Error)
	require.NoError(t, PruneModelFingerprintHistory(context.Background(), now))
	var stored ModelTestRun
	require.NoError(t, DB.First(&stored, "id = ?", run.ID).Error)
	assert.Zero(t, stored.UserID)
	assert.NotEmpty(t, stored.FingerprintResult, "one unified anonymous report remains")
	assert.Equal(t, run.ConfirmationID, stored.ConfirmationID)
	_, err := GetModelTestRun(owner.Id, run.ID)
	assert.Error(t, err)
	_, err = CreateModelTestRun(&run)
	assert.ErrorIs(t, err, ErrModelTestConflict, "original request id remains consumed")
	run.ID = "another-id"
	_, err = CreateModelTestRun(&run)
	assert.ErrorIs(t, err, ErrModelTestConflict, "one-use confirmation remains consumed forever")
}

func TestFingerprintRetentionPersonalEndpointOnlyReturnsTransientState(t *testing.T) {
	_, _, now := fingerprintSiteFixture(t)
	require.NoError(t, DB.AutoMigrate(&ModelTestRun{}))
	for _, row := range []ModelTestRun{
		{ID: "recent-terminal", Source: "user", UserID: 101, Status: "completed", BenchmarkID: "local-fingerprint-v1", CreatedAt: now - 10, FingerprintResult: JSONValue(`{"status":"scored"}`)},
		{ID: "expired-terminal", Source: "user", UserID: 101, Status: "completed", BenchmarkID: "local-fingerprint-v1", CreatedAt: now - 86401, FingerprintResult: JSONValue(`{"status":"scored"}`)},
	} {
		require.NoError(t, DB.Create(&row).Error)
	}
	rows, err := ListModelTestRuns(101)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "recent-terminal", rows[0].ID)
	assert.Empty(t, rows[0].FingerprintResult, "status endpoint must not be a personal result archive")
	effective, err := ListEffectiveModelTestRuns(101)
	require.NoError(t, err)
	assert.Empty(t, effective)
}
