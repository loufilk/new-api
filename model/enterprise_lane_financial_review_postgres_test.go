//go:build enterprise_lane_lab

package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnterpriseLaneFinancialReviewPostgres(t *testing.T) {
	id := ownerContractFixture(t)
	ctx := t.Context()
	makeHold := func(suffix string, amount int) string {
		rid := id.LaneID + "-" + suffix
		_, err := ReserveEnterpriseLane(ctx, DB, id, rid, amount)
		require.NoError(t, err)
		return rid
	}
	dispatch := func(rid string) {
		_, err := MarkEnterpriseLaneDispatched(ctx, DB, id, rid)
		require.NoError(t, err)
	}
	canonical := id.LaneID + "-a-held"
	_, err := reserveCanonicalLaneFixture(ctx, DB, id, canonical, 21, nil)
	require.NoError(t, err)
	dispatch(makeHold("b-dispatch", 22))
	unknown := makeHold("c-unknown", 23)
	dispatch(unknown)
	_, err = MarkEnterpriseLaneDeliveryUnknown(ctx, DB, id, unknown)
	require.NoError(t, err)
	observed := makeHold("d-observed", 24)
	dispatch(observed)
	const hook = "test:lane-financial-review-observed"
	injected := errors.New("synthetic failure retaining observed evidence")
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if tx.Statement.Table == "enterprise_lane_requests" && ok && updates["state"] == EnterpriseLaneSettled {
			tx.AddError(injected)
		}
	}))
	_, err = SettleEnterpriseLane(ctx, DB, id, observed, 10)
	require.NoError(t, DB.Callback().Update().Remove(hook))
	require.ErrorIs(t, err, injected)
	review := makeHold("e-over-cap", 25)
	dispatch(review)
	_, err = SettleEnterpriseLane(ctx, DB, id, review, 50)
	require.ErrorIs(t, err, ErrEnterpriseLaneExposure)
	zero := makeHold("f-old-zero-settled", 26)
	dispatch(zero)
	_, err = SettleEnterpriseLane(ctx, DB, id, zero, 0)
	require.NoError(t, err)
	measuredZero := makeHold("g-measured-zero", 27)
	dispatch(measuredZero)
	_, err = SettleEnterpriseLaneWithUsage(ctx, DB, id, measuredZero, 0, EnterpriseLaneUsage{
		ChannelID: 71, PromptTokens: 4, Model: "synthetic-model-hidden", Group: "synthetic-group-hidden", ObservedAt: common.GetTimestamp(),
	})
	require.NoError(t, err)
	old := makeHold("h-old-settled", 28)
	dispatch(old)
	_, err = SettleEnterpriseLane(ctx, DB, id, old, 13)
	require.NoError(t, err)
	refunded := makeHold("i-refunded", 29)
	_, err = RefundEnterpriseLane(ctx, DB, id, refunded)
	require.NoError(t, err)
	// Unrelated ordinary money must not be included in a lane total.
	var calls atomic.Int32
	_, _, err = reserveOrdinaryOwnerFixture(ctx, ownerContractOrdinaryIdentity(id, id.LaneID+"-ordinary"), 31, &calls, nil)
	require.NoError(t, err)
	before := readOwnerContractBalances(t, id)
	var laneReaders atomic.Int32
	const readHook = "test:lane-financial-review-read-only"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(readHook, func(tx *gorm.DB) {
		if tx.Statement.Table != "enterprise_lanes" {
			return
		}
		var mode struct{ ReadOnly, Isolation string }
		probe := tx.Session(&gorm.Session{NewDB: true}).Raw(`SELECT current_setting('transaction_read_only') AS read_only, current_setting('transaction_isolation') AS isolation`).Scan(&mode)
		if probe.Error != nil || mode.ReadOnly != "on" || mode.Isolation != "repeatable read" {
			tx.AddError(errors.New("financial report did not open the required read-only snapshot"))
			return
		}
		laneReaders.Add(1)
	}))
	report, err := ReadEnterpriseLaneFinancialReview(ctx, DB, id, 1)
	require.NoError(t, DB.Callback().Query().Remove(readHook))
	require.NoError(t, err)
	require.EqualValues(t, 1, laneReaders.Load())
	single, err := ReadEnterpriseLaneFinancialReview(ctx, DB, id, 128)
	require.NoError(t, err)
	assert.Equal(t, single, report, "pagination must neither skip nor double count states")
	assert.Equal(t, "lane_epoch_only_bridge_unproven", report.Scope)
	assert.EqualValues(t, 9, report.ReceiptCount)
	assert.EqualValues(t, 5, report.OpenRequests)
	assert.EqualValues(t, 1, report.CanonicalMarkerMatches)
	assert.EqualValues(t, 8, report.UnfencedLegacyReceipts)
	assert.EqualValues(t, 1, report.MeasuredProjectionPending)
	want := EnterpriseLaneFinancialPartition{Original: 300, Available: 172, Outstanding: 115, Spent: 13}
	assert.Equal(t, want, report.Paid)
	assert.Equal(t, want, report.FiniteKey)
	assert.Equal(t, EnterpriseLaneFinancialState{Requests: 1, OriginalCaps: 21, Outstanding: 21}, report.ByState[EnterpriseLaneHeld])
	assert.Equal(t, EnterpriseLaneFinancialState{Requests: 1, OriginalCaps: 22, Outstanding: 22}, report.ByState[EnterpriseLaneDispatch])
	assert.Equal(t, EnterpriseLaneFinancialState{Requests: 1, OriginalCaps: 23, Outstanding: 23}, report.ByState[EnterpriseLaneUnknown])
	assert.Equal(t, EnterpriseLaneFinancialState{Requests: 1, OriginalCaps: 24, Outstanding: 24, KnownActual: common.GetPointer(int64(10))}, report.ByState[EnterpriseLaneObserved])
	assert.Equal(t, EnterpriseLaneFinancialState{Requests: 1, OriginalCaps: 25, Outstanding: 25, KnownActual: common.GetPointer(int64(50))}, report.ByState[EnterpriseLaneReview])
	assert.Equal(t, EnterpriseLaneFinancialState{Requests: 3, OriginalCaps: 81, KnownActual: common.GetPointer(int64(13))}, report.ByState[EnterpriseLaneSettled])
	assert.Equal(t, EnterpriseLaneFinancialState{Requests: 1, OriginalCaps: 29}, report.ByState[EnterpriseLaneRefunded])
	assert.Equal(t, before, readOwnerContractBalances(t, id))
	payload, err := common.Marshal(report)
	require.NoError(t, err)
	for _, private := range []string{id.TokenFingerprint, canonical, id.LaneID, "synthetic-model-hidden", "synthetic-group-hidden"} {
		assert.NotContains(t, string(payload), private)
	}
}

func TestEnterpriseLaneFinancialReviewReturnsAndMissingEvidencePostgres(t *testing.T) {
	id := ownerContractFixture(t)
	ctx := t.Context()
	rid := id.LaneID + "-settle"
	_, err := ReserveEnterpriseLane(ctx, DB, id, rid, 40)
	require.NoError(t, err)
	_, err = MarkEnterpriseLaneDispatched(ctx, DB, id, rid)
	require.NoError(t, err)
	_, err = SettleEnterpriseLane(ctx, DB, id, rid, 35)
	require.NoError(t, err)
	_, err = StopEnterpriseLane(ctx, DB, id)
	require.NoError(t, err)
	_, err = DrainEnterpriseLane(ctx, DB, id)
	require.NoError(t, err)
	report, err := ReadEnterpriseLaneFinancialReview(ctx, DB, id, 10)
	require.NoError(t, err)
	assert.Equal(t, EnterpriseLaneDrained, report.State)
	want := EnterpriseLaneFinancialPartition{Original: 300, Returned: 265, Spent: 35}
	assert.Equal(t, want, report.Paid)
	assert.Equal(t, want, report.FiniteKey)
	assert.EqualValues(t, 1, report.UnfencedLegacyReceipts)

	wrong := id
	wrong.TokenFingerprint = strings.Repeat("0", 64)
	value, err := ReadEnterpriseLaneFinancialReview(ctx, DB, wrong, 10)
	assert.Nil(t, value)
	assert.ErrorIs(t, err, ErrEnterpriseLaneReviewScope)
	assert.NotContains(t, err.Error(), wrong.TokenFingerprint)
	wrong = id
	wrong.Epoch++
	value, err = ReadEnterpriseLaneFinancialReview(ctx, DB, wrong, 10)
	assert.Nil(t, value)
	assert.ErrorIs(t, err, ErrEnterpriseLaneReviewMissing)
	value, err = ReadEnterpriseLaneFinancialReview(ctx, DB, id, 129)
	assert.Nil(t, value)
	assert.ErrorIs(t, err, ErrEnterpriseLaneReviewScope)
	tx := DB.Begin()
	require.NoError(t, tx.Error)
	value, err = ReadEnterpriseLaneFinancialReview(ctx, tx, id, 10)
	require.NoError(t, tx.Rollback().Error)
	assert.Nil(t, value)
	assert.ErrorIs(t, err, ErrEnterpriseLaneReviewScope)

	// Sold/lane counters remain, but their actual-value receipt is no longer
	// present. The report must not return successful zero or partial totals.
	require.NoError(t, DB.Where("request_id = ?", rid).Delete(&EnterpriseLaneRequest{}).Error)
	value, err = ReadEnterpriseLaneFinancialReview(ctx, DB, id, 10)
	assert.Nil(t, value)
	assert.ErrorIs(t, err, ErrEnterpriseLaneReviewMissing)
}

func TestEnterpriseLaneFinancialReviewConflictAndSchemaPostgres(t *testing.T) {
	id := ownerContractFixture(t)
	ctx := t.Context()
	rid := id.LaneID + "-held"
	_, err := ReserveEnterpriseLane(ctx, DB, id, rid, 30)
	require.NoError(t, err)
	before := readOwnerContractBalances(t, id)
	check := func(want error) {
		t.Helper()
		report, err := ReadEnterpriseLaneFinancialReview(ctx, DB, id, 1)
		assert.Nil(t, report)
		require.ErrorIs(t, err, want)
		assert.NotContains(t, err.Error(), id.TokenFingerprint)
		assert.NotContains(t, err.Error(), rid)
		assert.Equal(t, before, readOwnerContractBalances(t, id))
	}
	var calls atomic.Int32
	owner, _, err := reserveOrdinaryOwnerFixture(ctx, ownerContractOrdinaryIdentity(id, rid), 20, &calls, nil)
	require.NoError(t, err)
	before = readOwnerContractBalances(t, id)
	check(ErrEnterpriseLaneReviewOwnerConflict)
	require.NoError(t, DB.Delete(&owner).Error)

	require.NoError(t, DB.Model(&EnterpriseLaneRequest{}).Where("request_id = ?", rid).Update("state", "synthetic-private-state").Error)
	check(ErrEnterpriseLaneReviewInconsistent)
	require.NoError(t, DB.Model(&EnterpriseLaneRequest{}).Where("request_id = ?", rid).Update("state", EnterpriseLaneHeld).Error)
	require.NoError(t, DB.Exec(`ALTER TABLE enterprise_lane_requests RENAME COLUMN usage_projection_pending TO temporarily_hidden_projection`).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec(`ALTER TABLE enterprise_lane_requests RENAME COLUMN temporarily_hidden_projection TO usage_projection_pending`).Error)
	})
	check(ErrEnterpriseLaneReviewUnavailable)
}

func TestEnterpriseLaneFinancialReviewSnapshotAndBoundsPostgres(t *testing.T) {
	id := ownerContractFixture(t)
	ctx := t.Context()
	rid := id.LaneID + "-a"
	for _, key := range []string{rid, id.LaneID + "-b"} {
		_, err := ReserveEnterpriseLane(ctx, DB, id, key, 30)
		require.NoError(t, err)
	}
	var revised atomic.Bool
	const hook = "test:lane-financial-review-snapshot"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "enterprise_lane_requests" && revised.CompareAndSwap(false, true) {
			_, err := RefundEnterpriseLane(ctx, DB, id, rid)
			if err != nil {
				tx.AddError(err)
			}
		}
	}))
	report, err := ReadEnterpriseLaneFinancialReview(ctx, DB, id, 1)
	require.NoError(t, DB.Callback().Query().Remove(hook))
	require.NoError(t, err)
	require.True(t, revised.Load())
	assert.EqualValues(t, 60, report.Paid.Outstanding)
	assert.EqualValues(t, 2, report.ByState[EnterpriseLaneHeld].Requests)
	fresh, err := ReadEnterpriseLaneFinancialReview(ctx, DB, id, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 30, fresh.Paid.Outstanding)
	assert.EqualValues(t, 1, fresh.ByState[EnterpriseLaneRefunded].Requests)

	// Historical refunds consume no current budget and can greatly outnumber
	// open requests. Overage must be surfaced, not mistaken for a short report.
	rows := make([]EnterpriseLaneRequest, EnterpriseLaneFinancialReviewMaxReceipts-2)
	for i := range rows {
		rows[i] = EnterpriseLaneRequest{RequestID: fmt.Sprintf("%s-historical-%04d", id.LaneID, i), EnterpriseLaneIdentity: id,
			State: EnterpriseLaneRefunded, Held: 1, RefundProof: EnterpriseLaneRefundNeverDispatched}
	}
	require.NoError(t, DB.CreateInBatches(&rows, 200).Error)
	atLimit, err := ReadEnterpriseLaneFinancialReview(ctx, DB, id, EnterpriseLaneFinancialReviewMaxPageSize)
	require.NoError(t, err)
	assert.EqualValues(t, EnterpriseLaneFinancialReviewMaxReceipts, atLimit.ReceiptCount)
	extra := EnterpriseLaneRequest{RequestID: id.LaneID + "-too-many", EnterpriseLaneIdentity: id,
		State: EnterpriseLaneRefunded, Held: 1, RefundProof: EnterpriseLaneRefundNeverDispatched}
	require.NoError(t, DB.Create(&extra).Error)
	overLimit, err := ReadEnterpriseLaneFinancialReview(ctx, DB, id, EnterpriseLaneFinancialReviewMaxPageSize)
	assert.Nil(t, overLimit)
	assert.ErrorIs(t, err, ErrEnterpriseLaneReviewLimit)

	deferred, cancel := context.WithCancel(ctx)
	cancel()
	broken, err := ReadEnterpriseLaneFinancialReview(deferred, DB, id, 1)
	assert.Nil(t, broken)
	assert.ErrorIs(t, err, context.Canceled)
}
