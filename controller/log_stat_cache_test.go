package controller

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSelfLogStatHTTPPostgres(t *testing.T) {
	dsn := os.Getenv("AUDIT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable loopback AUDIT_POSTGRES_DSN")
	}
	gin.SetMode(gin.TestMode)
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	useLogExportDatabases(t, db, db, common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	now := time.Now().Unix()
	for _, row := range []model.Log{
		{UserId: 701, Type: model.LogTypeConsume, CreatedAt: now - 500, Quota: 11},
		{UserId: 702, Type: model.LogTypeConsume, CreatedAt: now - 500, Quota: 29},
		{UserId: 701, Type: model.LogTypeConsume, CreatedAt: now - 5, PromptTokens: 13},
		{UserId: 702, Type: model.LogTypeConsume, CreatedAt: now - 5, PromptTokens: 17},
	} {
		require.NoError(t, db.Create(&row).Error)
	}
	var statements atomic.Int64
	const callback = "self_log_stat_http_count"
	require.NoError(t, db.Callback().Row().Before("gorm:row").Register(callback, func(*gorm.DB) { statements.Add(1) }))
	t.Cleanup(func() { require.NoError(t, db.Callback().Row().Remove(callback)) })
	query := fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", now-600, now-400)
	// Caller-visible entry point, two authenticated identities, concurrent cold
	// requests, and forged admin-only user/channel filters and raw-URL noise.
	type result struct {
		id       int
		response *httptest.ResponseRecorder
	}
	results := make(chan result, 16)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			id := 701 + i%2
			response := usageLogRequest(GetLogsSelfStat, id, "ignored-username", query+fmt.Sprintf("&user_id=%d&channel=999&noise=%d", 1403-id, i))
			results <- result{id, response}
		})
	}
	wg.Wait()
	close(results)
	for result := range results {
		require.Equal(t, http.StatusOK, result.response.Code)
		var body struct {
			Success bool       `json:"success"`
			Data    model.Stat `json:"data"`
		}
		require.NoError(t, common.Unmarshal(result.response.Body.Bytes(), &body))
		require.True(t, body.Success)
		want := model.Stat{Quota: 11, Rpm: 1, Tpm: 13}
		if result.id == 702 {
			want = model.Stat{Quota: 29, Rpm: 1, Tpm: 17}
		}
		assert.Equal(t, want, body.Data)
	}
	assert.EqualValues(t, 2, statements.Swap(0), "one cold SQL per authenticated user, not per URL")
	assert.Equal(t, http.StatusUnauthorized, usageLogRequest(GetLogsSelfStat, 0, "", query).Code)
	assert.Zero(t, statements.Swap(0), "unauthorized requests cannot reach cached data or SQL")
	// A valid replacement-character filter must not mask PostgreSQL's rejection
	// of a raw invalid UTF-8 filter through a JSON-normalized cache key.
	validUTF8 := usageLogRequest(GetLogsSelfStat, 701, "", query+"&token_name=%EF%BF%BD")
	assert.Contains(t, validUTF8.Body.String(), `"success":true`)
	invalidUTF8 := usageLogRequest(GetLogsSelfStat, 701, "", query+"&token_name=%FF")
	assert.Contains(t, invalidUTF8.Body.String(), `"success":false`)
	settings := operation_setting.GetLogSetting()
	before := *settings
	t.Cleanup(func() { *settings = before })
	settings.MaxRangeDays = 365
	wide := fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", now-2*86400, now)
	ok := callLogHandler(t, GetLogsSelfStat, 701, common.RoleCommonUser, wide)[0]
	require.Equal(t, true, ok["success"])
	statements.Store(0)
	settings.MaxRangeDays = 1
	denied := callLogHandler(t, GetLogsSelfStat, 701, common.RoleCommonUser, wide)[0]
	assert.Equal(t, false, denied["success"])
	assert.NotContains(t, denied, "data")
	assert.Zero(t, statements.Load(), "range-policy validation runs before a warm cache hit")
}

func TestSelfLogStatRangeOverflow(t *testing.T) {
	_, err := limitLogTimeRangeWithMax(math.MinInt64, 1700000000, 86400)
	require.Error(t, err)
	start, err := limitLogTimeRangeWithMax(1700000000-86400, 1700000000, 86400)
	require.NoError(t, err)
	assert.EqualValues(t, 1700000000-86400, start)
	start, err = limitLogTimeRangeWithMax(math.MinInt64, math.MinInt64, 86400)
	require.NoError(t, err)
	assert.EqualValues(t, math.MinInt64, start)
}

func TestSelfLogStatDefaultRangeSaturates(t *testing.T) {
	start, err := limitLogTimeRangeWithMax(0, math.MinInt64, 86400)
	require.NoError(t, err)
	assert.EqualValues(t, math.MinInt64, start)
	_, err = limitLogTimeRangeWithMax(0, 1700000000, 0)
	require.Error(t, err)
}
