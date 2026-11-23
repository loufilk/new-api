package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPostgresLogStatOneStatement(t *testing.T) {
	if !common.UsingLogDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires TEST_MODEL_POSTGRES_DSN on disposable PostgreSQL")
	}
	truncateTables(t)
	now := time.Now().Unix()
	first, last := now-600, now-500
	base := Log{
		UserId: 500, Username: "report-alice", Type: LogTypeConsume,
		ModelName: "report_literal-model", TokenName: "report-key", ChannelId: 33, Group: "report-group",
		RequestId: "report-request", UpstreamRequestId: "report-upstream",
	}
	lower, upper, old, recentA, recentB := base, base, base, base, base
	lower.CreatedAt, lower.Quota = first, 11
	upper.CreatedAt, upper.Quota = last, 29
	old.CreatedAt, old.Quota = first-1, 30
	recentA.CreatedAt, recentA.Quota, recentA.PromptTokens, recentA.CompletionTokens = now-5, 55, 90, 9
	recentB.CreatedAt, recentB.Quota, recentB.PromptTokens, recentB.CompletionTokens = now-4, 56, 7, 91
	for _, row := range []*Log{&lower, &upper, &old, &recentA, &recentB} {
		require.NoError(t, LOG_DB.Create(row).Error)
	}
	legacy := base
	legacy.CreatedAt, legacy.Quota = first+1, 57
	require.NoError(t, LOG_DB.Create(&legacy).Error)
	require.NoError(t, LOG_DB.Model(&legacy).UpdateColumn("quota", nil).Error)
	require.NoError(t, LOG_DB.Model(&recentA).UpdateColumn("prompt_tokens", nil).Error)
	require.NoError(t, LOG_DB.Model(&recentB).UpdateColumn("completion_tokens", nil).Error)

	// Every dimension must constrain both aggregates, not just one.
	for _, alter := range []func(*Log){
		func(l *Log) { l.Type = LogTypeError },
		func(l *Log) { l.UserId++ },
		func(l *Log) { l.Username = "someone-else" },
		func(l *Log) { l.ModelName = "reportXliteral-model" }, // underscore is literal
		func(l *Log) { l.TokenName = "wrong-key" },
		func(l *Log) { l.ChannelId++ },
		func(l *Log) { l.Group = "wrong-group" },
		func(l *Log) { l.RequestId = "other-request" },
		func(l *Log) { l.UpstreamRequestId = "other-upstream" },
	} {
		sample := base
		alter(&sample)
		sample.Quota, sample.PromptTokens, sample.CompletionTokens = 900, 900, 900
		for _, at := range []int64{first, now - 3} {
			row := sample
			row.CreatedAt = at
			require.NoError(t, LOG_DB.Create(&row).Error)
		}
	}

	var statements int
	callback := "count_postgres_log_stat_statements"
	require.NoError(t, LOG_DB.Callback().Row().Before("gorm:row").Register(callback, func(_ *gorm.DB) { statements++ }))
	t.Cleanup(func() { require.NoError(t, LOG_DB.Callback().Row().Remove(callback)) })

	filter := LogFilter{Type: LogTypeError, UserId: 500, Username: "report-alice", ModelName: "report_%model",
		TokenName: "report-key", Channel: 33, Group: "report-group", RequestId: "report-request",
		UpstreamRequestId: "report-upstream", StartTimestamp: first, EndTimestamp: last}
	stat, err := SumUsedQuota(filter)
	require.NoError(t, err)
	require.Equal(t, Stat{Quota: 40, Rpm: 2, Tpm: 16}, stat)
	require.Equal(t, 1, statements)

	// An empty historical range must not erase the independent rate rows.
	statements = 0
	filter.StartTimestamp, filter.EndTimestamp = first-200, first-100
	stat, err = SumUsedQuota(filter)
	require.NoError(t, err)
	require.Equal(t, Stat{Rpm: 2, Tpm: 16}, stat)
	require.Equal(t, 1, statements)

	statements = 0
	filter.UserId = 999
	stat, err = SumUsedQuota(filter)
	require.NoError(t, err)
	require.Equal(t, Stat{}, stat)
	require.Equal(t, 1, statements)
}
