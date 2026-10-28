package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayCancellationPrecedesChannelErrorsAndRetryBudget(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx, cancel := context.WithCancel(context.Background())
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Unix(1, 0))
		} else {
			cancel()
		}
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
		for _, code := range []types.ErrorCode{types.ErrorCodeDoRequestFailed, types.ErrorCodeChannelNoAvailableKey} {
			err := types.NewErrorWithStatusCode(errors.New("attempt failed"), code, 500)
			decision := DecideRelayRetry(c, err, 100)
			assert.Equal(t, "stop", decision.Action)
			assert.Equal(t, "client", decision.Source)
			stopped := RelayRequestCancellation(c)
			require.NotNil(t, stopped)
			assert.True(t, types.IsSkipRetryError(stopped))
			assert.ErrorIs(t, stopped, ctx.Err())
			if deadline {
				assert.Equal(t, http.StatusRequestTimeout, stopped.StatusCode)
				assert.True(t, types.IsRecordErrorLog(stopped), "request deadlines are not silently hidden")
			} else {
				assert.Equal(t, 499, stopped.StatusCode)
				assert.False(t, types.IsRecordErrorLog(stopped))
			}
		}
		// A completed result is not converted into failure merely because the
		// parent context is cancelled during cleanup.
		assert.Equal(t, "request_completed", DecideRelayRetry(c, nil, 3).Reason)
		cancel()
	}
}

func TestRelayCancellationDoesNotDisableHealthyPool(t *testing.T) {
	previousAuto, previousLog := common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled
	common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = true, false
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = previousAuto, previousLog })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	c.Set("auto_ban", true)
	err := types.NewErrorWithStatusCode(errors.New("channel error reported after cancellation"), types.ErrorCodeChannelNoAvailableKey, 500)
	RecordPolicyFailure(c, 44, err, DecideRelayRetry(c, err, 3))
	events := RequestPolicy(c).Events()
	require.Len(t, events, 2)
	assert.Equal(t, "unchanged", events[1].Health)
	// No database fixture: reaching DisableChannel would be an unwanted side
	// effect. The aborted request is diagnosed but never bans the account.
	ProcessChannelError(c, types.ChannelError{ChannelId: 44, AutoBan: true}, err, nil)
}

func TestRelayRetriesStillWorkForLiveRequestUpstreamFailures(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	for _, status := range []int{429, 503} {
		err := types.NewErrorWithStatusCode(errors.New("real upstream failure"), types.ErrorCodeBadResponseStatusCode, status)
		assert.True(t, ShouldRetryRelayError(c, err, 2))
	}
	// A per-attempt deadline is distinct from the incoming request lifetime.
	err := types.NewErrorWithStatusCode(context.DeadlineExceeded, types.ErrorCodeDoRequestFailed, 500)
	assert.True(t, ShouldRetryRelayError(c, err, 2))
	blocked := types.NewErrorWithStatusCode(errors.New("do not replay"), types.ErrorCodeChannelNoAvailableKey, 500, types.ErrOptionWithSkipRetry())
	assert.False(t, ShouldRetryRelayError(c, blocked, 2))
	writeFailure := types.NewErrorWithStatusCode(errors.New("downstream socket closed"), ClientGoneErrorCode, 499, types.ErrOptionWithSkipRetry())
	assert.Equal(t, "client_gone", DecideRelayRetry(c, writeFailure, 2).Reason)
}
