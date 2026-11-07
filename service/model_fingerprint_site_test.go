package service

import (
	"context"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupFingerprintSchedulerDatabase(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("TEST_SERVICE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL TEST_SERVICE_POSTGRES_DSN")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback())
	schema := "fingerprint_scheduler_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: parsed.String(), PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE SCHEMA "+schema).Error)
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		require.NoError(t, db.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		require.NoError(t, connection.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.ModelTestGate{}, &model.GroupRenameOperation{},
		&model.ModelFingerprintSiteConfig{}, &model.ModelFingerprintSiteAudit{}, &model.ModelFingerprintSiteRun{},
		&model.ModelFingerprintSiteProbe{}, &model.ModelTestRun{}, &model.SystemTask{}, &model.SystemTaskLock{}))
}

func TestModelFingerprintSiteSchedulerStartsFirstRunAndContinuesAfterPartialResult(t *testing.T) {
	setupFingerprintSchedulerDatabase(t)
	handler := modelFingerprintSiteHandler{}
	withSystemTaskRegistry(t, handler)
	config, err := model.UpdateModelFingerprintSiteConfig(t.Context(), 0, 1, model.ModelFingerprintSiteConfig{
		Enabled: true, AutomaticEnabled: true, DefaultPeriodSecs: 3600,
		Targets: []model.ModelFingerprintSiteTarget{
			{Group: "default", Model: "first", Enabled: true},
			{Group: "default", Model: "second", Enabled: true},
			{Group: "default", Model: "disabled", Enabled: false},
		},
	})
	require.NoError(t, err)
	var visited []string
	fingerprintSiteExecutorMu.Lock()
	previousExecutor := fingerprintSiteExecutor
	fingerprintSiteExecutor = func(ctx context.Context, task *model.SystemTask, runnerID string, target model.ModelFingerprintSiteTarget, run *model.ModelFingerprintSiteRun) error {
		visited = append(visited, target.Model)
		result := model.ModelFingerprintSiteResult{Status: "succeeded", Assessment: "partial",
			FingerprintResult: `{"status":"partial","used_outputs":1,"top_candidates":[{"model":"fixture","probability":null}],"probability_status":"unavailable"}`}
		return model.FinishModelFingerprintSiteRun(ctx, run.ID, result, false)
	}
	fingerprintSiteExecutorMu.Unlock()
	t.Cleanup(func() {
		fingerprintSiteExecutorMu.Lock()
		fingerprintSiteExecutor = previousExecutor
		fingerprintSiteExecutorMu.Unlock()
	})
	runSystemTaskScheduler()
	task, err := model.GetLatestSystemTask(handler.Type())
	require.NoError(t, err)
	require.NotNil(t, task, "enabling automatic monitoring schedules its first run")
	claimedTask, claimed, err := model.ClaimSystemTask(task.ID, task.Type, "monitor-test", time.Now().Add(time.Hour).Unix())
	require.NoError(t, err)
	require.True(t, claimed)
	handler.Run(t.Context(), claimedTask, "monitor-test")
	assert.Equal(t, []string{"first", "second"}, visited, "a partial first result must not starve the next enabled target")
	finished, err := model.GetLatestSystemTask(handler.Type())
	require.NoError(t, err)
	assert.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
	after, err := model.GetModelFingerprintSiteConfig(t.Context())
	require.NoError(t, err)
	assert.True(t, after.AutomaticEnabled)
	assert.Equal(t, config.Version, after.Version)
}
