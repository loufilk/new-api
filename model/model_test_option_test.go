package model

import (
	"context"
	"maps"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestModelTestOptionPostgresPersistenceAndAuthoritativeReads(t *testing.T) {
	dsn := os.Getenv("TEST_MODEL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires owned PostgreSQL 18.6 fixture")
	}
	require.Equal(t, "1", os.Getenv("NO_AGENT_FIXTURE_OWNED"))
	originalMap := maps.Clone(common.OptionMap)
	oldDB := DB
	require.NoError(t, DB.Where(Option{Key: ModelTestEnabledKey}).Delete(&Option{}).Error)
	t.Cleanup(func() {
		DB = oldDB
		require.NoError(t, DB.Where(Option{Key: ModelTestEnabledKey}).Delete(&Option{}).Error)
		common.OptionMap = originalMap
	})
	ctx := context.Background()
	enabled, err := ModelTestEnabled(ctx)
	require.NoError(t, err)
	assert.False(t, enabled, "fresh and existing installations are opt-in")
	var count int64
	require.NoError(t, DB.Model(&Option{}).Where(Option{Key: ModelTestEnabledKey}).Count(&count).Error)
	assert.Zero(t, count, "read does not persist a default or migrate existing options")
	require.NoError(t, UpdateOption(ModelTestEnabledKey, "true"))
	// Simulate another process/restart using a fresh independent connection and
	// deliberately stale in-memory options. Only the database decides access.
	other, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := other.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	DB = other
	common.OptionMap[ModelTestEnabledKey] = "false"
	enabled, err = ModelTestEnabled(ctx)
	require.NoError(t, err)
	assert.True(t, enabled)
	require.NoError(t, oldDB.Model(&Option{}).Where(Option{Key: ModelTestEnabledKey}).Update("value", "false").Error)
	common.OptionMap[ModelTestEnabledKey] = "true"
	enabled, err = ModelTestEnabled(ctx)
	require.NoError(t, err)
	assert.False(t, enabled, "another node disables immediately without SyncOptions")
	for _, value := range []string{"", "TRUE", "1", "null", "{}"} {
		require.Error(t, UpdateOption(ModelTestEnabledKey, value))
	}
	var stored Option
	require.NoError(t, DB.Where(Option{Key: ModelTestEnabledKey}).Take(&stored).Error)
	assert.Equal(t, "false", stored.Value)
	require.NoError(t, oldDB.Model(&Option{}).Where(Option{Key: ModelTestEnabledKey}).Update("value", "invalid-legacy-value").Error)
	enabled, err = ModelTestEnabled(ctx)
	require.NoError(t, err)
	assert.False(t, enabled, "corrupt legacy values fail closed")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	enabled, err = ModelTestEnabled(cancelled)
	require.Error(t, err)
	assert.False(t, enabled)
	// A failed persistence operation must not claim success or flip runtime state.
	DB = other.Begin()
	require.NoError(t, DB.Error)
	require.NoError(t, DB.Rollback().Error)
	require.Error(t, UpdateOption(ModelTestEnabledKey, "false"))
	assert.Equal(t, "true", common.OptionMap[ModelTestEnabledKey])
}
