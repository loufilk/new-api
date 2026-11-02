package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelTestAccessOrdinaryRequestNeverDependsOnSwitchStorage(t *testing.T) {
	originalDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = originalDB })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/playground/chat", nil)
	require.Nil(t, CheckModelTestAccess(c))
	assert.Empty(t, c.GetString("model_test_error_code"))
}

func TestModelTestAccessStorageFailureIsFailClosedAndNotRetryable(t *testing.T) {
	originalDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = originalDB })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/model-tests/runs", nil)
	common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, true)
	err := CheckModelTestAccess(c)
	require.NotNil(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
	assert.True(t, types.IsSkipRetryError(err))
	assert.Equal(t, "model_test_settings_unavailable", c.GetString("model_test_error_code"))
}
