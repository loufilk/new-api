//go:build enterprise_lane_lab

package model

import (
	"context"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const EnterpriseLaneRefundNeverDispatched = "dispatch_not_claimed"

func enterpriseLaneValidateRequest(db *gorm.DB, identity EnterpriseLaneIdentity, requestID string) error {
	if err := enterpriseLaneValidate(db, identity); err != nil {
		return err
	}
	if !enterpriseLaneIDValid(requestID) {
		return ErrEnterpriseLaneIdentity
	}
	return nil
}

func lockEnterpriseLaneRequest(tx *gorm.DB, identity EnterpriseLaneIdentity, requestID string) (EnterpriseLaneRequest, error) {
	var request EnterpriseLaneRequest
	if err := lockForUpdate(tx).Where("request_id = ?", requestID).Take(&request).Error; err != nil {
		return request, err
	}
	if request.EnterpriseLaneIdentity != identity || request.Held <= 0 || request.Held > common.MaxQuota || request.Actual < 0 || request.Actual > common.MaxQuota {
		return request, ErrEnterpriseLaneIdentity
	}
	if (request.Usage != EnterpriseLaneUsage{} && !request.Usage.valid()) ||
		(request.UsageProjectionPending && (request.State != EnterpriseLaneSettled || !request.Usage.valid())) {
		return request, ErrEnterpriseLaneUsage
	}
	return request, nil
}

func saveEnterpriseLaneRequest(tx *gorm.DB, request EnterpriseLaneRequest) error {
	updates := map[string]any{
		"held": request.Held, "actual": request.Actual, "state": request.State, "refund_proof": request.RefundProof,
	}
	if (request.State == EnterpriseLaneObserved || request.State == EnterpriseLaneReview) && request.Usage != (EnterpriseLaneUsage{}) {
		updates["usage_channel_id"], updates["usage_prompt_tokens"] = request.Usage.ChannelID, request.Usage.PromptTokens
		updates["usage_completion_tokens"], updates["usage_model"] = request.Usage.CompletionTokens, request.Usage.Model
		updates["usage_group"], updates["usage_observed_at"] = request.Usage.Group, request.Usage.ObservedAt
	}
	if request.UsageProjectionPending {
		updates["usage_projection_pending"] = true
	}
	result := tx.Model(&EnterpriseLaneRequest{}).Where("request_id = ?", request.RequestID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrEnterpriseLaneIdentity
	}
	return nil
}

// ReserveEnterpriseLane pins the request to this exact lane/epoch once, then
// holds the same hard exposure cap in both paid and finite-key partitions.
// Strictly positive caps are required. This is not a pricing estimate or a
// grant to dispatch; only the one-way dispatch claim grants that authority.
func ReserveEnterpriseLane(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity, requestID string, target int) (EnterpriseLaneRequest, error) {
	var request EnterpriseLaneRequest
	if err := enterpriseLaneValidateRequest(db, identity, requestID); err != nil {
		return request, err
	}
	if target <= 0 || target > common.MaxQuota {
		return request, ErrEnterpriseLaneCapacity
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		request = EnterpriseLaneRequest{RequestID: requestID, EnterpriseLaneIdentity: identity, Held: target, State: EnterpriseLaneHeld}
		claim := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&request)
		if claim.Error != nil {
			return claim.Error
		}
		fresh := claim.RowsAffected == 1
		old := 0
		if !fresh {
			var err error
			request, err = lockEnterpriseLaneRequest(tx, identity, requestID)
			if err != nil {
				return err
			}
			if request.State != EnterpriseLaneHeld {
				return ErrEnterpriseLaneState
			}
			old = request.Held
			if target <= old {
				return nil // read-only replay, never an additional dispatch grant
			}
		}
		lane, err := lockEnterpriseLane(tx, identity)
		if err != nil {
			return err
		}
		if lane.State != EnterpriseLaneOpen {
			return ErrEnterpriseLaneState
		}
		delta := target - old
		if delta > lane.PaidAvailable || delta > lane.TokenAvailable {
			return ErrEnterpriseLaneCapacity
		}
		lane.PaidAvailable -= delta
		lane.TokenAvailable -= delta
		lane.Held += delta
		if fresh {
			lane.OpenRequests++
		}
		if err := saveEnterpriseLane(tx, lane); err != nil || fresh {
			return err
		}
		request.Held = target
		return saveEnterpriseLaneRequest(tx, request)
	})
	if err != nil {
		return EnterpriseLaneRequest{}, err
	}
	return request, nil
}

// MarkEnterpriseLaneDispatched is a single-use, commit-acknowledged dispatch
// grant. A lost ACK or replay does not grant a second upstream attempt and
// never authorizes a refund. Stop blocks new dispatch claims for the lane.
func MarkEnterpriseLaneDispatched(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity, requestID string) (EnterpriseLaneRequest, error) {
	return updateEnterpriseLaneRequest(ctx, db, identity, requestID, func(tx *gorm.DB, request *EnterpriseLaneRequest) error {
		if request.State != EnterpriseLaneHeld {
			return ErrEnterpriseLaneState
		}
		lane, err := lockEnterpriseLane(tx, identity)
		if err != nil {
			return err
		}
		if lane.State != EnterpriseLaneOpen {
			return ErrEnterpriseLaneState
		}
		request.State = EnterpriseLaneDispatch
		return saveEnterpriseLaneRequest(tx, *request)
	})
}

// MarkEnterpriseLaneDeliveryUnknown permanently blocks automatic refund and
// settlement of a dispatched request whose provider result could not be proven.
func MarkEnterpriseLaneDeliveryUnknown(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity, requestID string) (EnterpriseLaneRequest, error) {
	return updateEnterpriseLaneRequest(ctx, db, identity, requestID, func(tx *gorm.DB, request *EnterpriseLaneRequest) error {
		switch request.State {
		case EnterpriseLaneUnknown:
			return nil
		case EnterpriseLaneDispatch:
			request.State = EnterpriseLaneUnknown
			return saveEnterpriseLaneRequest(tx, *request)
		default:
			return ErrEnterpriseLaneState
		}
	})
}

// SettleEnterpriseLane freezes verified measured actual use before attempting
// money movement. A bounded over-cap result remains recorded in review and
// cannot draw unreserved funds or become a refund. Out-of-domain input is
// rejected without changing the dispatched hold or its nonrefund protection;
// callers must mark delivery unknown if this cannot be resolved to valid
// measured evidence. Same-actual retry after any
// uncertain COMMIT safely returns the settled owner or resumes its debit.
func SettleEnterpriseLane(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity, requestID string, actual int) (EnterpriseLaneRequest, error) {
	return settleEnterpriseLane(ctx, db, identity, requestID, actual, EnterpriseLaneUsage{})
}

func settleEnterpriseLane(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity, requestID string, actual int, usage EnterpriseLaneUsage) (EnterpriseLaneRequest, error) {
	if actual < 0 || actual > common.MaxQuota {
		return EnterpriseLaneRequest{}, ErrEnterpriseLaneExposure
	}
	frozen, err := updateEnterpriseLaneRequest(ctx, db, identity, requestID, func(tx *gorm.DB, request *EnterpriseLaneRequest) error {
		switch request.State {
		case EnterpriseLaneDispatch:
			request.Actual, request.State, request.Usage = actual, EnterpriseLaneObserved, usage
			if actual > request.Held {
				request.State = EnterpriseLaneReview
			}
			return saveEnterpriseLaneRequest(tx, *request)
		case EnterpriseLaneObserved, EnterpriseLaneSettled, EnterpriseLaneReview:
			if request.Actual != actual || request.Usage != usage {
				return ErrEnterpriseLaneIdentity
			}
			return nil
		default:
			return ErrEnterpriseLaneState
		}
	})
	if err != nil {
		return EnterpriseLaneRequest{}, err
	}
	if frozen.State == EnterpriseLaneReview {
		return frozen, ErrEnterpriseLaneExposure
	}
	if frozen.State == EnterpriseLaneSettled {
		return frozen, nil
	}
	return completeEnterpriseLaneObserved(ctx, db, identity, requestID, &frozen)
}

func completeEnterpriseLaneObserved(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity, requestID string, expected *EnterpriseLaneRequest) (EnterpriseLaneRequest, error) {
	return updateEnterpriseLaneRequest(ctx, db, identity, requestID, func(tx *gorm.DB, request *EnterpriseLaneRequest) error {
		if request.State == EnterpriseLaneReview {
			return ErrEnterpriseLaneExposure
		}
		if request.State != EnterpriseLaneSettled && request.State != EnterpriseLaneObserved {
			return ErrEnterpriseLaneState
		}
		if (expected != nil && (request.Actual != expected.Actual || request.Usage != expected.Usage)) || request.Actual > request.Held {
			return ErrEnterpriseLaneIdentity
		}
		if expected == nil && !request.Usage.valid() {
			return ErrEnterpriseLaneUsage
		}
		if request.State == EnterpriseLaneSettled {
			return nil
		}
		if err := releaseEnterpriseLaneRequestHold(tx, *request, request.Actual); err != nil {
			return err
		}
		request.State = EnterpriseLaneSettled
		request.UsageProjectionPending = request.Usage.valid()
		return saveEnterpriseLaneRequest(tx, *request)
	})
}

// RefundEnterpriseLane only releases never-dispatched holds: the locked state
// proves no dispatch claim could have succeeded, and the refund tombstone
// prevents a later claim. A dispatched zero-actual response must be settled
// with explicit measured evidence instead of reusing this refund transition.
func RefundEnterpriseLane(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity, requestID string) (EnterpriseLaneRequest, error) {
	return updateEnterpriseLaneRequest(ctx, db, identity, requestID, func(tx *gorm.DB, request *EnterpriseLaneRequest) error {
		if request.State == EnterpriseLaneRefunded && request.RefundProof == EnterpriseLaneRefundNeverDispatched {
			return nil
		}
		if request.State != EnterpriseLaneHeld || request.Actual != 0 {
			return ErrEnterpriseLaneState
		}
		if err := releaseEnterpriseLaneRequestHold(tx, *request, 0); err != nil {
			return err
		}
		request.State, request.RefundProof = EnterpriseLaneRefunded, EnterpriseLaneRefundNeverDispatched
		return saveEnterpriseLaneRequest(tx, *request)
	})
}

func releaseEnterpriseLaneRequestHold(tx *gorm.DB, request EnterpriseLaneRequest, spent int) error {
	lane, err := lockEnterpriseLane(tx, request.EnterpriseLaneIdentity)
	if err != nil {
		return err
	}
	if lane.State == EnterpriseLaneDrained || lane.OpenRequests < 1 || lane.Held < request.Held || spent < 0 || spent > request.Held {
		return ErrEnterpriseLaneState
	}
	lane.Held -= request.Held
	lane.OpenRequests--
	lane.Spent += spent
	lane.PaidAvailable += request.Held - spent
	lane.TokenAvailable += request.Held - spent
	return saveEnterpriseLane(tx, lane)
}

func updateEnterpriseLaneRequest(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity, requestID string, update func(*gorm.DB, *EnterpriseLaneRequest) error) (EnterpriseLaneRequest, error) {
	var request EnterpriseLaneRequest
	if err := enterpriseLaneValidateRequest(db, identity, requestID); err != nil {
		return request, err
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		request, err = lockEnterpriseLaneRequest(tx, identity, requestID)
		if err != nil {
			return err
		}
		return update(tx, &request)
	})
	if err != nil {
		return EnterpriseLaneRequest{}, err
	}
	return request, nil
}
