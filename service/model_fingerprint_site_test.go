package service

import (
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

func TestRetiredFingerprintSchedulerClosesQueuedJobsWithoutDispatch(t *testing.T) {
	setupFingerprintSchedulerDatabase(t)
	handler := retiredFingerprintSiteHandler{}
	withSystemTaskRegistry(t, handler)
	_, err := model.UpdateModelFingerprintSiteConfig(t.Context(), 0, 1, model.ModelFingerprintSiteConfig{Enabled: true, AutomaticEnabled: true, DefaultPeriodSecs: 3600, Targets: []model.ModelFingerprintSiteTarget{{Group: "default", Model: "old-target", Enabled: true}}})
	require.NoError(t, err)
	runSystemTaskScheduler()
	latest, err := model.GetLatestSystemTask(handler.Type())
	require.NoError(t, err)
	assert.Nil(t, latest)
	queued, err := model.CreateSystemTask(handler.Type(), struct{}{}, nil)
	require.NoError(t, err)
	task, claimed, err := model.ClaimSystemTask(queued.ID, queued.Type, "retirement-test", time.Now().Add(time.Hour).Unix())
	require.NoError(t, err)
	require.True(t, claimed)
	handler.Run(t.Context(), task, "retirement-test")
	latest, err = model.GetLatestSystemTask(handler.Type())
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, model.SystemTaskStatusFailed, latest.Status)
	var count int64
	require.NoError(t, model.DB.Model(&model.ModelFingerprintSiteRun{}).Count(&count).Error)
	assert.Zero(t, count)
	runSystemTaskScheduler()
	next, err := model.GetLatestSystemTask(handler.Type())
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.Equal(t, queued.ID, next.ID)
}
