package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClientGoneNeverCreatesChannelErrorRows(t *testing.T) {
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldRedis, oldLog := common.RedisEnabled, constant.ErrorLogEnabled
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	constant.ErrorLogEnabled = true
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.RedisEnabled, constant.ErrorLogEnabled = oldRedis, oldLog
		common.SetDatabaseTypes(oldMain, oldLogType)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.Create(&model.User{Id: 7, Username: "cancel-log-owner", Group: "default"}).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set("id", 7)
	c.Set("username", "cancel-log-owner")
	c.Set("token_name", "test-only")
	c.Set("original_model", "deepseek/deepseek-v4.1-flash")
	c.Set("group", "default")
	snapshot := types.ChannelError{ChannelId: 44, ChannelType: constant.ChannelTypeCline, ChannelName: "test-pool", AutoBan: false}
	for _, apiErr := range []*types.NewAPIError{
		service.NewRelayClientGoneError(context.Canceled),
		// Guard the persistence boundary even when another caller forgets NoRecord.
		types.NewErrorWithStatusCode(context.Canceled, service.ClientGoneErrorCode, 499, types.ErrOptionWithSkipRetry()),
	} {
		service.RecordPolicyFailure(c, 44, apiErr, service.DecideRelayRetry(c, apiErr, 3))
		processChannelError(c, snapshot, apiErr, nil)
	}
	var count int64
	require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
	assert.Zero(t, count, "local client cancellation must not be recorded as channel 500/499 errors")
	events := service.RequestPolicy(c).Events()
	require.NotEmpty(t, events)
	assert.Equal(t, "client_gone", events[len(events)-1].Decision.Reason)
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		apiErr := types.NewOpenAIError(errors.New("genuine upstream failure"), types.ErrorCodeBadResponseStatusCode, status)
		assert.True(t, service.ShouldRetryRelayError(c, apiErr, 2))
		processChannelError(c, snapshot, apiErr, nil)
	}
	// A real upstream cannot suppress error recording with a same-named code.
	upstream := types.WithOpenAIError(types.OpenAIError{Code: "client_gone", Type: "server_error", Message: "genuine upstream fault"}, 503)
	processChannelError(c, snapshot, upstream, nil)
	require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
	assert.Equal(t, int64(3), count)
}
