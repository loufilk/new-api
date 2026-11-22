package model

import (
	"context"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func fingerprintSiteFixture(t *testing.T) (context.Context, *ModelFingerprintSiteConfig, int64) {
	t.Helper()
	dsn := os.Getenv("TEST_MODEL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_MODEL_POSTGRES_DSN disposable production-aligned PostgreSQL fixture is required")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback(), "fingerprint tests require a disposable loopback PostgreSQL fixture")
	fixtureSchema := "fingerprint_site_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	query := parsed.Query()
	query.Set("search_path", fixtureSchema)
	parsed.RawQuery = query.Encode()
	dsn = parsed.String()
	old := DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, oldLog)
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	sql, err := db.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(8)
	require.NoError(t, db.Exec("CREATE SCHEMA "+fixtureSchema).Error)
	DB = db
	t.Cleanup(func() {
		DB = old
		common.SetDatabaseTypes(oldMain, oldLog)
		require.NoError(t, db.Exec("DROP SCHEMA "+fixtureSchema+" CASCADE").Error)
		require.NoError(t, sql.Close())
	})
	models := []any{&ModelFingerprintSiteConfig{}, &ModelFingerprintSiteAudit{}, &ModelFingerprintSiteRun{}, &ModelFingerprintSiteProbe{}, &ModelTestRun{}, &SystemTask{}, &SystemTaskLock{}, &Option{}, &ModelTestGate{}}
	require.NoError(t, db.AutoMigrate(models...))
	require.NoError(t, db.AutoMigrate(models...))
	var version string
	require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	t.Logf("PostgreSQL %s fresh migration and second startup", version)
	require.NoError(t, db.Create(&Option{Key: ModelTestEnabledKey, Value: "true"}).Error)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&SystemTask{TaskID: "fixture-task", Type: SystemTaskTypeModelFingerprintSite, Status: SystemTaskStatusRunning, LockedBy: "fixture-runner"}).Error)
	require.NoError(t, db.Create(&SystemTaskLock{Type: SystemTaskTypeModelFingerprintSite, TaskID: "fixture-task", LockedBy: "fixture-runner", LockedUntil: now + 86400}).Error)
	ctx := context.Background()
	c, err := UpdateModelFingerprintSiteConfig(ctx, 0, 7, ModelFingerprintSiteConfig{Enabled: true, AutomaticEnabled: true, DefaultPeriodSecs: 3600, DailyBudgetQuota: 250, Targets: []ModelFingerprintSiteTarget{{ID: uuid.NewString(), Group: "default", Model: "gpt-fixture", ChannelID: 7, Enabled: true, MaxOutputTokens: 2048, MaxRunQuota: 100}}})
	require.NoError(t, err)
	return ctx, c, now
}
func TestFingerprintSiteDefaultsAndConfigurationCAS(t *testing.T) {
	ctx, c, _ := fingerprintSiteFixture(t)
	c.DefaultPeriodSecs = 0
	_, err := UpdateModelFingerprintSiteConfig(ctx, c.Version, 7, *c)
	require.Error(t, err)
	c.DefaultPeriodSecs = 3600
	_, err = UpdateModelFingerprintSiteConfig(ctx, 0, 7, *c)
	require.ErrorIs(t, err, ErrFingerprintSiteConflict)
	var auditCount int64
	require.NoError(t, DB.Model(&ModelFingerprintSiteAudit{}).Count(&auditCount).Error)
	require.EqualValues(t, 1, auditCount)
	c.AutomaticEnabled = false
	next, err := UpdateModelFingerprintSiteConfig(ctx, c.Version, 7, *c)
	require.NoError(t, err)
	require.EqualValues(t, 2, next.Version)
	require.False(t, next.AutomaticEnabled)
	var stored ModelFingerprintSiteConfig
	require.NoError(t, DB.First(&stored, 1).Error)
	require.False(t, stored.AutomaticEnabled)
	require.NoError(t, DB.Delete(&ModelFingerprintSiteConfig{}, 1).Error)
	defaults, err := GetModelFingerprintSiteConfig(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 3600, defaults.DefaultPeriodSecs)
	require.Zero(t, defaults.DailyBudgetQuota)
	require.False(t, defaults.AutomaticEnabled)
	var count int64
	require.NoError(t, DB.Model(&ModelFingerprintSiteConfig{}).Count(&count).Error)
	require.Zero(t, count, "read must not create enabled defaults")
}
func TestFingerprintSiteAdmissionWithoutBudgetAndNoReplay(t *testing.T) {
	ctx, c, now := fingerprintSiteFixture(t)
	run, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, now)
	require.NoError(t, err)
	after, err := GetModelFingerprintSiteConfig(ctx)
	require.NoError(t, err)
	require.Zero(t, after.ReservedQuota)
	_, err = ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, now)
	require.ErrorIs(t, err, ErrFingerprintSiteNotDue)
	require.NoError(t, AuthorizeModelFingerprintSiteProbe(ctx, run.ID, "fixture-task", "fixture-runner", "probe-1"))
	require.Error(t, AuthorizeModelFingerprintSiteProbe(ctx, run.ID, "fixture-task", "fixture-runner", "probe-1"))
	require.Error(t, CompleteModelFingerprintSiteProbe(ctx, run.ID, "probe-1", -1, 20, "fixture"))
	require.NoError(t, CompleteModelFingerprintSiteProbe(ctx, run.ID, "probe-1", 10, 20, "fixture"))
	require.ErrorIs(t, CompleteModelFingerprintSiteProbe(ctx, run.ID, "probe-1", 10, 20, "fixture"), ErrFingerprintSiteConflict)
	var probe ModelFingerprintSiteProbe
	require.NoError(t, DB.Where("run_id = ? AND probe_id = ?", run.ID, "probe-1").Take(&probe).Error)
	require.Equal(t, "completed", probe.Status)
	require.Equal(t, 10, probe.PromptTokens)
	require.Equal(t, 20, probe.CompletionTokens)
	_, requestIDErr := uuid.Parse(probe.RequestID)
	require.NoError(t, requestIDErr)

	require.NoError(t, FinishModelFingerprintSiteRun(ctx, run.ID, ModelFingerprintSiteResult{Status: "succeeded", FingerprintResult: `{"assessment":"fixture"}`}, true))
	after, err = GetModelFingerprintSiteConfig(ctx)
	require.NoError(t, err)
	require.Zero(t, after.ReservedQuota)
	require.ErrorIs(t, FinishModelFingerprintSiteRun(ctx, run.ID, ModelFingerprintSiteResult{Status: "succeeded"}, true), ErrFingerprintSiteConflict)
	require.ErrorIs(t, AuthorizeModelFingerprintSiteProbe(ctx, run.ID, "fixture-task", "fixture-runner", "probe-2"), ErrFingerprintSiteConflict)
	_, err = ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, now)
	require.ErrorIs(t, err, ErrFingerprintSiteNotDue)
}
func TestFingerprintSitePostgresUnsentProbeLifecycle(t *testing.T) {
	t.Run("unsent_intent_has_no_observed_usage_or_safety_pause", func(t *testing.T) {
		ctx, config, now := fingerprintSiteFixture(t)
		run, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", config.Targets[0].ID, config.Version, now)
		require.NoError(t, err)
		requestID := uuid.NewString()
		require.NoError(t, AuthorizeModelFingerprintSiteProbe(ctx, run.ID, "fixture-task", "fixture-runner", "unsent", requestID))
		require.NoError(t, AuthorizeModelFingerprintSiteProbe(ctx, run.ID, "fixture-task", "fixture-runner", "completed"))
		require.NoError(t, CompleteModelFingerprintSiteProbe(ctx, run.ID, "completed", 10, 20, "fixture-model"))
		var before ModelFingerprintSiteProbe
		require.NoError(t, DB.Where("run_id = ? AND probe_id = ?", run.ID, "unsent").Take(&before).Error)

		// Final dispatch can be denied after the durable intent was admitted.
		// Recording that fact remains possible after the switch was closed.
		require.NoError(t, DB.Model(&Option{}).Where("key = ?", ModelTestEnabledKey).Update("value", "false").Error)
		require.NoError(t, MarkModelFingerprintSiteProbeUnsent(ctx, run.ID, "unsent"))
		var unsent ModelFingerprintSiteProbe
		require.NoError(t, DB.Where("run_id = ? AND probe_id = ?", run.ID, "unsent").Take(&unsent).Error)
		assert.Equal(t, "not_sent", unsent.Status)
		assert.GreaterOrEqual(t, unsent.CompletedAt, before.DispatchedAt)
		assert.NotZero(t, unsent.CompletedAt)
		assert.Equal(t, requestID, unsent.RequestID)
		assert.Zero(t, unsent.PromptTokens)
		assert.Zero(t, unsent.CompletionTokens)
		assert.Empty(t, unsent.ResponseModel)
		expected := before
		expected.Status, expected.CompletedAt = "not_sent", unsent.CompletedAt
		assert.Equal(t, expected, unsent, "closing an unused intent preserves its identity and all usage fields")

		require.ErrorIs(t, MarkModelFingerprintSiteProbeUnsent(ctx, run.ID, "unsent"), ErrFingerprintSiteConflict)
		require.ErrorIs(t, CompleteModelFingerprintSiteProbe(ctx, run.ID, "unsent", 50, 60, "contradictory-model"), ErrFingerprintSiteConflict)
		require.ErrorIs(t, MarkModelFingerprintSiteProbeUnsent(ctx, run.ID, "completed"), ErrFingerprintSiteConflict)
		require.ErrorIs(t, MarkModelFingerprintSiteProbeUnsent(ctx, run.ID, "missing"), ErrFingerprintSiteConflict)
		var completed ModelFingerprintSiteProbe
		require.NoError(t, DB.Where("run_id = ? AND probe_id = ?", run.ID, "completed").Take(&completed).Error)
		assert.Equal(t, "completed", completed.Status)
		assert.Equal(t, 10, completed.PromptTokens)
		assert.Equal(t, 20, completed.CompletionTokens)
		assert.Equal(t, "fixture-model", completed.ResponseModel)

		require.NoError(t, FinishModelFingerprintSiteRun(ctx, run.ID, ModelFingerprintSiteResult{Status: "failed", Assessment: "dispatch_not_authorized"}, true))
		var saved ModelFingerprintSiteRun
		require.NoError(t, DB.First(&saved, "id = ?", run.ID).Error)
		assert.Equal(t, "failed", saved.Status)
		assert.Equal(t, "known", saved.UsageState)
		after, err := GetModelFingerprintSiteConfig(ctx)
		require.NoError(t, err)
		assert.True(t, after.AutomaticEnabled, "definite non-delivery does not trigger an unknown-usage safety pause")
		assert.Equal(t, config.Version, after.Version)
	})

	t.Run("concurrent_terminal_writes_have_one_winner", func(t *testing.T) {
		ctx, config, now := fingerprintSiteFixture(t)
		run, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", config.Targets[0].ID, config.Version, now)
		require.NoError(t, err)
		require.NoError(t, AuthorizeModelFingerprintSiteProbe(ctx, run.ID, "fixture-task", "fixture-runner", "probe"))
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Go(func() {
				<-start
				results <- MarkModelFingerprintSiteProbeUnsent(ctx, run.ID, "probe")
			})
		}
		close(start)
		workers.Wait()
		close(results)
		applied := 0
		for err := range results {
			if err == nil {
				applied++
			} else {
				require.ErrorIs(t, err, ErrFingerprintSiteConflict)
			}
		}
		assert.Equal(t, 1, applied)
		var probe ModelFingerprintSiteProbe
		require.NoError(t, DB.Where("run_id = ? AND probe_id = ?", run.ID, "probe").Take(&probe).Error)
		assert.Equal(t, "not_sent", probe.Status)
		assert.NotZero(t, probe.CompletedAt)
		assert.Zero(t, probe.PromptTokens)
		assert.Zero(t, probe.CompletionTokens)
	})

	t.Run("terminal_run_preserves_its_unknown_intent", func(t *testing.T) {
		ctx, config, now := fingerprintSiteFixture(t)
		run, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", config.Targets[0].ID, config.Version, now)
		require.NoError(t, err)
		require.NoError(t, AuthorizeModelFingerprintSiteProbe(ctx, run.ID, "fixture-task", "fixture-runner", "probe"))
		var before ModelFingerprintSiteProbe
		require.NoError(t, DB.Where("run_id = ? AND probe_id = ?", run.ID, "probe").Take(&before).Error)
		require.NoError(t, FinishModelFingerprintSiteRun(ctx, run.ID, ModelFingerprintSiteResult{Status: "failed"}, false))
		require.ErrorIs(t, MarkModelFingerprintSiteProbeUnsent(ctx, run.ID, "probe"), ErrFingerprintSiteConflict)
		var after ModelFingerprintSiteProbe
		require.NoError(t, DB.Where("run_id = ? AND probe_id = ?", run.ID, "probe").Take(&after).Error)
		assert.Equal(t, before, after, "late non-delivery claims cannot rewrite a finalized unknown run")
	})
}

func TestFingerprintSiteUnknownPausesWithoutChangingLegacyLedger(t *testing.T) {
	ctx, c, now := fingerprintSiteFixture(t)
	require.NoError(t, DB.Model(&ModelFingerprintSiteConfig{}).Where("id = ?", 1).Update("reserved_quota", 100).Error)
	run, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, now)
	require.NoError(t, err)
	require.NoError(t, FinishModelFingerprintSiteRun(ctx, run.ID, ModelFingerprintSiteResult{Status: "failed"}, false))
	after, err := GetModelFingerprintSiteConfig(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 100, after.ReservedQuota)
	require.False(t, after.AutomaticEnabled)
	require.Greater(t, after.Version, c.Version)
	_, staleErr := UpdateModelFingerprintSiteConfig(ctx, c.Version, 7, *c)
	require.ErrorIs(t, staleErr, ErrFingerprintSiteConflict)
	var stored ModelFingerprintSiteRun
	require.NoError(t, DB.First(&stored, "id = ?", run.ID).Error)
	require.Equal(t, "unknown", stored.Status)
	require.Equal(t, "unknown", stored.UsageState)
}
func TestFingerprintSiteUnlimitedBudgetAndConcurrentAdmission(t *testing.T) {
	ctx, c, now := fingerprintSiteFixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, now)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrFingerprintSiteNotDue)
		}
	}
	require.Equal(t, 1, success)
	var run ModelFingerprintSiteRun
	require.NoError(t, DB.First(&run).Error)

	require.NoError(t, FinishModelFingerprintSiteRun(ctx, run.ID, ModelFingerprintSiteResult{Status: "succeeded"}, true))
	next, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, now+3600)
	require.NoError(t, err)
	require.NoError(t, FinishModelFingerprintSiteRun(ctx, next.ID, ModelFingerprintSiteResult{Status: "succeeded"}, true))
	_, err = ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, now+7200)
	require.NoError(t, err)
	after, err := GetModelFingerprintSiteConfig(ctx)
	require.NoError(t, err)
	require.Zero(t, after.ReservedQuota)
}
func TestFingerprintSiteProbeRechecksLeaseGenerationAndGlobalSwitch(t *testing.T) {
	for _, cause := range []string{"lease", "generation", "global", "automatic"} {
		t.Run(cause, func(t *testing.T) {
			ctx, c, now := fingerprintSiteFixture(t)
			run, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, now)
			require.NoError(t, err)
			switch cause {
			case "lease":
				require.NoError(t, DB.Model(&SystemTaskLock{}).Where("type = ?", SystemTaskTypeModelFingerprintSite).Update("locked_until", now-1).Error)
			case "generation":
				_, err = UpdateModelFingerprintSiteConfig(ctx, c.Version, 7, *c)
				require.NoError(t, err)
			case "global":
				require.NoError(t, DB.Model(&Option{}).Where(Option{Key: ModelTestEnabledKey}).Update("value", "false").Error)
			case "automatic":
				c.AutomaticEnabled = false
				_, err = UpdateModelFingerprintSiteConfig(ctx, c.Version, 7, *c)
				require.NoError(t, err)
			}
			require.Error(t, AuthorizeModelFingerprintSiteProbe(ctx, run.ID, "fixture-task", "fixture-runner", "probe-1"))
			// Authorization was denied, so no dispatch exists to complete.
			require.ErrorIs(t, CompleteModelFingerprintSiteProbe(ctx, run.ID, "probe-1", 10, 20, "fixture"), ErrFingerprintSiteConflict)

			var count int64
			require.NoError(t, DB.Model(&ModelFingerprintSiteProbe{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
func TestFingerprintSiteCurrentResultsArePerTargetAndRedacted(t *testing.T) {
	ctx, c, now := fingerprintSiteFixture(t)
	second := c.Targets[0]
	second.ID = uuid.NewString()
	second.Group = "premium"
	c.Targets = append(c.Targets, second)
	c, err := UpdateModelFingerprintSiteConfig(ctx, c.Version, 7, *c)
	require.NoError(t, err)
	for targetIndex, target := range c.Targets {
		for n := range 65 {
			require.NoError(t, DB.Create(&ModelFingerprintSiteRun{ID: uuid.NewString(), TargetID: target.ID, ConfigVersion: c.Version, Window: int64(n), Group: target.Group, Model: target.Model, ChannelID: 777, TaskID: "private-task", Status: "succeeded", Assessment: "scored", UsageState: "known", FingerprintResult: `{"status":"ok","api_key":"secret-fixture-key","channel_id":777,"top_candidates":[{"model":"fixture","provider_headers":"secret-fixture-header"}]}`, CreatedAt: now + int64(n+targetIndex*100)}).Error)
		}
		for n := range 10 {
			require.NoError(t, DB.Create(&ModelFingerprintSiteRun{ID: uuid.NewString(), TargetID: target.ID, ConfigVersion: c.Version, Window: int64(200 + n), Group: target.Group, Model: target.Model, Status: "succeeded", Assessment: "unscorable", CreatedAt: now + int64(200+n)}).Error)
		}
		require.NoError(t, DB.Create(&ModelFingerprintSiteRun{ID: uuid.NewString(), TargetID: target.ID, ConfigVersion: c.Version, Window: 999, Group: target.Group, Model: target.Model, Status: "failed", CreatedAt: now + 999}).Error)
		require.NoError(t, DB.Create(&ModelFingerprintSiteRun{ID: uuid.NewString(), TargetID: target.ID, ConfigVersion: c.Version, Window: 1000, Group: target.Group, Model: target.Model, Status: "running", CreatedAt: now + 1000}).Error)
	}
	rows, err := ListModelFingerprintSitePublicRuns(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 120)
	data, err := common.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(data), "channel_id")
	require.NotContains(t, string(data), "private-task")
	require.NotContains(t, string(data), "reserved_quota")
	require.NotContains(t, string(data), "secret-fixture")
	require.NotContains(t, string(data), "api_key")
	require.NotContains(t, string(data), "provider_headers")
	counts := map[string]int{}
	for _, row := range rows {
		assert.NotEqual(t, "running", row.Status)
		counts[row.TargetID]++
	}
	for _, n := range counts {
		require.Equal(t, 60, n)
	}
}
func TestFingerprintSiteConfigRemovesLegacyLimits(t *testing.T) {
	for _, maxTokens := range []int{-1, 1, 2048, 4096, 100000} {
		c := ModelFingerprintSiteConfig{DefaultPeriodSecs: 3600, DailyBudgetQuota: 1, AutomaticEnabled: true, Targets: []ModelFingerprintSiteTarget{{Group: "default", Model: "gpt-test", ChannelID: 1, Enabled: true, MaxOutputTokens: maxTokens, MaxRunQuota: 1}}}
		require.NoError(t, ValidateModelFingerprintSiteConfig(&c))
		require.Zero(t, c.DailyBudgetQuota)
		require.Zero(t, c.Targets[0].ChannelID)
		require.Zero(t, c.Targets[0].MaxOutputTokens)
		require.Zero(t, c.Targets[0].MaxRunQuota)
	}
}
func TestFingerprintSiteFinishDoesNotModifyLegacyReservations(t *testing.T) {
	ctx, c, now := fingerprintSiteFixture(t)
	run, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, now)
	require.NoError(t, err)
	// Simulate an admin-day rollover while an already dispatched response is pending.
	require.NoError(t, DB.Model(&ModelFingerprintSiteConfig{}).Where("id = ?", 1).Updates(map[string]any{"budget_day": "2099-01-01", "reserved_quota": 70}).Error)

	require.NoError(t, FinishModelFingerprintSiteRun(ctx, run.ID, ModelFingerprintSiteResult{Status: "succeeded"}, true))
	after, err := GetModelFingerprintSiteConfig(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 70, after.ReservedQuota)
}
func TestFingerprintSiteGlobalSwitchSavedAtomically(t *testing.T) {
	ctx, c, _ := fingerprintSiteFixture(t)
	c.Enabled = false
	next, err := UpdateModelFingerprintSiteConfig(ctx, c.Version, 7, *c)
	require.NoError(t, err)
	enabled, err := ModelTestEnabled(ctx)
	require.NoError(t, err)
	require.False(t, enabled)
	fetched, err := GetModelFingerprintSiteConfig(ctx)
	require.NoError(t, err)
	require.False(t, fetched.Enabled)
	next.Enabled = true
	_, err = UpdateModelFingerprintSiteConfig(ctx, c.Version, 7, *next)
	require.ErrorIs(t, err, ErrFingerprintSiteConflict)
	enabled, err = ModelTestEnabled(ctx)
	require.NoError(t, err)
	require.False(t, enabled, "stale CAS must not change global switch")
}

func TestFingerprintSiteElapsedPeriodSurvivesBoundaryAndConfigEdit(t *testing.T) {
	ctx, c, _ := fingerprintSiteFixture(t)
	start := time.Date(2026, time.November, 5, 11, 59, 0, 0, time.UTC).Unix()
	require.NoError(t, DB.Model(&SystemTaskLock{}).Where("type = ?", SystemTaskTypeModelFingerprintSite).Update("locked_until", start+86400).Error)
	run, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, start)
	require.NoError(t, err)

	require.NoError(t, FinishModelFingerprintSiteRun(ctx, run.ID, ModelFingerprintSiteResult{Status: "succeeded"}, true))
	// The next calendar bucket starts in one minute, but the configured hour has not elapsed.
	_, err = ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, start+60)
	require.ErrorIs(t, err, ErrFingerprintSiteNotDue)
	c, err = UpdateModelFingerprintSiteConfig(ctx, c.Version, 7, *c)
	require.NoError(t, err)
	_, err = ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, start+120)
	require.ErrorIs(t, err, ErrFingerprintSiteNotDue, "a configuration save must not reset elapsed-time admission")
	next, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, start+3600)
	require.NoError(t, err)
	require.NoError(t, FinishModelFingerprintSiteRun(ctx, next.ID, ModelFingerprintSiteResult{Status: "succeeded"}, true))
	delayed, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, start+8*3600)
	require.NoError(t, err)
	require.Equal(t, (start+8*3600)/3600, delayed.Window)
	var count int64
	require.NoError(t, DB.Model(&ModelFingerprintSiteRun{}).Count(&count).Error)
	require.EqualValues(t, 3, count, "missed periods must not create catch-up runs")
	after, err := GetModelFingerprintSiteConfig(ctx)
	require.NoError(t, err)
	require.Zero(t, after.ReservedQuota)
}
func TestFingerprintSiteAdmissionRejectsInvalidStoredPeriod(t *testing.T) {
	ctx, c, now := fingerprintSiteFixture(t)
	for _, period := range []int64{-1, 0, 59, 604801} {
		require.NoError(t, DB.Model(&ModelFingerprintSiteConfig{}).Where("id = ?", 1).Update("default_period_secs", period).Error)
		_, err := ReserveModelFingerprintSiteRun(ctx, "fixture-task", "fixture-runner", c.Targets[0].ID, c.Version, now)
		require.ErrorIs(t, err, ErrFingerprintSiteDisabled)
	}
	var count int64
	require.NoError(t, DB.Model(&ModelFingerprintSiteRun{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestFingerprintSiteAutomaticConfigurationRequiresRunnableTargets(t *testing.T) {
	empty := ModelFingerprintSiteConfig{DefaultPeriodSecs: 3600, DailyBudgetQuota: 100, AutomaticEnabled: true}
	require.Error(t, ValidateModelFingerprintSiteConfig(&empty))
	disabled := ModelFingerprintSiteConfig{DefaultPeriodSecs: 3600, DailyBudgetQuota: 100, AutomaticEnabled: true, Targets: []ModelFingerprintSiteTarget{{Group: "default", Model: "gpt-fixture", ChannelID: 1, MaxRunQuota: 100}}}
	require.Error(t, ValidateModelFingerprintSiteConfig(&disabled))
	disabled.Targets[0].Enabled = true
	disabled.Targets[0].MaxRunQuota = 101
	require.NoError(t, ValidateModelFingerprintSiteConfig(&disabled))
	disabled.Targets[0].MaxRunQuota = 100
	require.NoError(t, ValidateModelFingerprintSiteConfig(&disabled))
}
