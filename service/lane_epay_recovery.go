package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/model"
)

const (
	maxLaneEpayRecoveryOrders = 8
	laneEpayRecoveryBudget    = 8 * time.Second
)

var errLaneEpayRecoveryScope = errors.New("Epay recovery requires a bounded list of already-bound orders")

// No table discovery, schedule or attachment to startup exists here. The
// future caller may provide only already-authorized original orders.
type laneEpayRecoveryOrder interface {
	Status(context.Context) (model.LaneEpayOrderStatus, error)
	Reconcile(context.Context) (model.LaneEpayOutcome, error)
}

type laneEpayRecoveryResult struct {
	Status  model.LaneEpayOrderStatus
	Outcome model.LaneEpayOutcome
	Err     error
}

// Process only an explicit small list. Counting includes every attempted
// inspection/retry, not just credits. Different orders remain independent;
// partial results on a canceled batch are returned without a hidden retry.
func runLaneEpayRecoveryBatch(ctx context.Context, orders []laneEpayRecoveryOrder, reconcile bool) ([]laneEpayRecoveryResult, error) {
	if ctx == nil || len(orders) == 0 || len(orders) > maxLaneEpayRecoveryOrders {
		return nil, errLaneEpayRecoveryScope
	}
	for _, order := range orders {
		if order == nil {
			return nil, errLaneEpayRecoveryScope
		}
	}
	ctx, cancel := context.WithTimeout(ctx, laneEpayRecoveryBudget)
	defer cancel()
	result := make([]laneEpayRecoveryResult, 0, len(orders))
	for _, order := range orders {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var item laneEpayRecoveryResult
		if reconcile {
			item.Outcome, item.Err = order.Reconcile(ctx)
		} else {
			item.Status, item.Err = order.Status(ctx)
		}
		result = append(result, item)
	}
	return result, ctx.Err()
}
