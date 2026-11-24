//go:build lane_billing_integration_dev

package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type laneEpayFixtureAuthority struct {
	check func(tx, primary, log *gorm.DB, walletEpoch int64) error
}

func (a laneEpayFixtureAuthority) CheckTx(tx, primary, log *gorm.DB, walletEpoch int64) error {
	return a.check(tx, primary, log, walletEpoch)
}

func TestLaneEpayBoundOrderRequiresSameDBTxAndObservesWithoutCreditPostgres(t *testing.T) {
	db, scope, order, client, bound := laneEpayCaptureFixtureWithMode(t, false, true)
	capture, _ := verifiedLaneEpayFixture(t, client, order, "1.00", "SYNTHETIC_SCOPED_CAPTURE")
	input := LaneEpayVerifiedNotification{capture.tradeNo, capture.method, capture.money, capture.facts}
	var zero BoundLaneEpayOrder
	_, err := zero.ApplyVerified(t.Context(), input)
	require.ErrorIs(t, err, ErrLaneStoreAuthorityUnavailable)
	_, err = zero.Status(t.Context())
	require.ErrorIs(t, err, ErrLaneStoreAuthorityUnavailable)
	_, err = zero.Reconcile(t.Context())
	require.ErrorIs(t, err, ErrLaneStoreAuthorityUnavailable)
	checks := 0
	allow := false
	bound.authority = laneEpayFixtureAuthority{
		check: func(tx, primary, log *gorm.DB, epoch int64) error {
			_, txOK := tx.Statement.ConnPool.(gorm.TxCommitter)
			require.True(t, txOK)
			require.Same(t, db, primary)
			require.Same(t, db, log)
			require.Equal(t, scope.walletEpoch, epoch)
			checks++
			if !allow {
				return ErrLaneStoreAuthorityState
			}
			return nil
		},
	}
	_, err = bound.ApplyVerified(t.Context(), input)
	require.ErrorIs(t, err, ErrLaneStoreAuthorityState)
	_, err = bound.Status(t.Context())
	require.ErrorIs(t, err, ErrLaneStoreAuthorityState)
	_, err = bound.Reconcile(t.Context())
	require.ErrorIs(t, err, ErrLaneStoreAuthorityState)
	require.Equal(t, 3, checks)
	assertLaneEpayEffects(t, db, order, 0, 0)
	allow = true
	_, err = bound.Status(t.Context())
	require.ErrorIs(t, err, ErrTopUpStatusInvalid)

	parked, err := bound.ApplyVerified(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, LaneEpayCapturedWaiting, parked.State)
	releaseLaneEpayHeadroom(t, db, scope.userID)
	observed, err := bound.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, LaneEpayCapturedWaiting, observed.State)
	require.Equal(t, parked.ReceiptID, observed.ReceiptID)
	require.Positive(t, observed.ReceiptRecordedAt)
	require.Zero(t, observed.CreditCompletedAt, "observation must not credit")
	assertLaneEpayEffects(t, db, order, 1, 0)

	credited, err := bound.Reconcile(t.Context())
	require.NoError(t, err)
	require.Equal(t, LaneEpayCredited, credited.State)
	require.Equal(t, parked.ReceiptID, credited.ReceiptID)
	after, err := bound.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, LaneEpayCredited, after.State)
	require.Equal(t, observed.ReceiptRecordedAt, after.ReceiptRecordedAt)
	require.Positive(t, after.CreditCompletedAt)
	assertLaneEpayEffects(t, db, order, 1, 1)
	require.Equal(t, 8, checks)
}
