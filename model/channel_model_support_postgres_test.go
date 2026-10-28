package model

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestGroupModelSupportPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires an isolated TEST_POSTGRES_DSN")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	tx := db.Begin()
	require.NoError(t, tx.Error)
	oldDB, oldMemory := DB, common.MemoryCacheEnabled
	oldRefresh := channelCacheLastRefresh.Load()
	DB, common.MemoryCacheEnabled = tx, false
	t.Cleanup(func() {
		DB, common.MemoryCacheEnabled = oldDB, oldMemory
		channelCacheLastRefresh.Store(oldRefresh)
		require.NoError(t, tx.Rollback().Error)
		require.NoError(t, sqlDB.Close())
	})
	// Transaction-local fixture; never migrate or replace the connected database.
	require.NoError(t, tx.Exec("SET LOCAL search_path = pg_temp").Error)
	require.NoError(t, tx.Exec(`CREATE TEMPORARY TABLE abilities (
		"group" varchar(64) NOT NULL, model varchar(255) NOT NULL, channel_id integer NOT NULL,
		enabled boolean, priority bigint, weight bigint, tag varchar(255),
		PRIMARY KEY ("group", model, channel_id)
	) ON COMMIT DROP`).Error)
	require.NoError(t, tx.Create(&[]Ability{
		{Group: "selected", Model: "enabled-model", ChannelId: 1, Enabled: true},
		{Group: "selected", Model: "disabled-model", ChannelId: 2, Enabled: false},
		{Group: "fallback", Model: "fallback-model", ChannelId: 3, Enabled: false},
		{Group: "other-private-group", Model: "other-model", ChannelId: 4, Enabled: true},
		{Group: "selected", Model: "qwen3-max", ChannelId: 5, Enabled: false},
		{Group: "selected", Model: "claude-3-7-sonnet", ChannelId: 6, Enabled: false},
		{Group: "selected", Model: "gemini-2.5-flash-thinking-*", ChannelId: 7, Enabled: false},
		{Group: "selected", Model: "client-alias", ChannelId: 8, Enabled: true},
	}).Error)
	for _, tc := range []struct {
		name   string
		groups []string
		model  string
		want   bool
	}{
		{"enabled", []string{"selected"}, "enabled-model", true},
		{"disabled still configured", []string{"selected"}, "disabled-model", true},
		{"all fallback groups checked", []string{"selected", "fallback"}, "fallback-model", true},
		{"other group is not in scope", []string{"selected"}, "other-model", false},
		{"missing model", []string{"selected", "fallback"}, "missing-model", false},
		{"modifier routing name", []string{"selected"}, "qwen3-max@thinking:on", true},
		{"legacy alias", []string{"selected"}, "claude-3-7-sonnet-thinking", true},
		{"legacy wildcard", []string{"selected"}, "gemini-2.5-flash-thinking-8192", true},
		{"public mapping source", []string{"selected"}, "client-alias", true},
		{"mapping target is not a public route", []string{"selected"}, "private-upstream-target", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			supported, err := HasConfiguredModelForGroups(ctx, tc.groups, tc.model)
			require.NoError(t, err)
			assert.Equal(t, tc.want, supported)
		})
	}

	for _, groups := range [][]string{nil, {""}, {"auto"}} {
		_, err := HasConfiguredModelForGroups(context.Background(), groups, "missing-model")
		require.Error(t, err, "an unresolved scope is not evidence of absence")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = HasConfiguredModelForGroups(ctx, []string{"selected"}, "missing-model")
	require.ErrorIs(t, err, context.Canceled)

	common.MemoryCacheEnabled = true
	t.Setenv("CHANNEL_CACHE_MAX_STALE_SECONDS", "30")
	channelCacheLastRefresh.Store(time.Now().Add(-time.Minute).UnixNano())
	_, err = HasConfiguredModelForGroups(context.Background(), []string{"selected"}, "missing-model")
	require.ErrorIs(t, err, ErrChannelCacheStale)
	common.MemoryCacheEnabled = false

	require.NoError(t, tx.Exec("DROP TABLE pg_temp.abilities").Error)
	_, err = HasConfiguredModelForGroups(context.Background(), []string{"selected"}, "missing-model")
	require.Error(t, err, "a database failure must not report confirmed absence")
}
