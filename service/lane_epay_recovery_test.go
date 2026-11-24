package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

type laneEpayRecoveryFixture struct {
	status, retry func(context.Context) error
}

func (f laneEpayRecoveryFixture) Status(ctx context.Context) (model.LaneEpayOrderStatus, error) {
	return model.LaneEpayOrderStatus{LaneEpayOutcome: model.LaneEpayOutcome{State: model.LaneEpayCapturedWaiting}}, f.status(ctx)
}
func (f laneEpayRecoveryFixture) Reconcile(ctx context.Context) (model.LaneEpayOutcome, error) {
	return model.LaneEpayOutcome{State: model.LaneEpayCredited}, f.retry(ctx)
}

func TestLaneEpayRecoveryBatchBoundAndUncoupledFailure(t *testing.T) {
	ctx := t.Context()
	statusCalls, retryCalls := 0, 0
	failure := errors.New("synthetic storage failure")
	fixture := laneEpayRecoveryFixture{
		status: func(ctx context.Context) error {
			statusCalls++
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.LessOrEqual(t, time.Until(deadline), laneEpayRecoveryBudget)
			return nil
		},
		retry: func(ctx context.Context) error {
			retryCalls++
			if retryCalls == 1 {
				return failure
			}
			return nil
		},
	}
	overlong := make([]laneEpayRecoveryOrder, maxLaneEpayRecoveryOrders+1)
	for i := range overlong {
		overlong[i] = fixture
	}
	for _, invalid := range [][]laneEpayRecoveryOrder{nil, overlong, {fixture, nil}} {
		out, err := runLaneEpayRecoveryBatch(ctx, invalid, true)
		require.ErrorIs(t, err, errLaneEpayRecoveryScope)
		require.Nil(t, out)
	}
	require.Zero(t, retryCalls)
	require.Zero(t, statusCalls)
	out, err := runLaneEpayRecoveryBatch(ctx, overlong[:maxLaneEpayRecoveryOrders], false)
	require.NoError(t, err)
	require.Len(t, out, maxLaneEpayRecoveryOrders)
	require.Equal(t, maxLaneEpayRecoveryOrders, statusCalls)
	require.Zero(t, retryCalls)
	out, err = runLaneEpayRecoveryBatch(ctx, []laneEpayRecoveryOrder{fixture, fixture}, true)
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.ErrorIs(t, out[0].Err, failure)
	require.NoError(t, out[1].Err)
	require.Equal(t, 2, retryCalls)
}

func TestLaneEpayRecoveryBatchStopsBeforeNextOrderOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	fixture := laneEpayRecoveryFixture{retry: func(context.Context) error {
		calls++
		cancel()
		return nil
	}}
	out, err := runLaneEpayRecoveryBatch(ctx, []laneEpayRecoveryOrder{fixture, fixture}, true)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, out, 1)
	require.Equal(t, 1, calls)
}
