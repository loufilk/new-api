package model

import (
	"context"
	"database/sql"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	LaneEpayCapturedWaiting = laneEpayCaptureWaiting
	LaneEpayCredited        = common.TopUpStatusSuccess
)

// LaneEpayVerifiedNotification is for an internal, verified terminal callback
// only. The HTTP parser does not bind it from a request. The scoped order
// checks its original merchant, amount and identities again transactionally.
type LaneEpayVerifiedNotification struct {
	TradeNo, Method, Money string
	Facts                  VerifiedPaymentFacts
}

type LaneEpayOutcome struct {
	State     string
	ReceiptID int64
	Replayed  bool
}

// ReceiptRecordedAt is when this service recorded verified capture evidence,
// not a provider timestamp. CreditCompletedAt is zero before wallet credit.
type LaneEpayOrderStatus struct {
	LaneEpayOutcome
	ReceiptRecordedAt int64
	CreditCompletedAt int64
}

type laneEpayTxAuthority interface {
	CheckTx(tx, primary, log *gorm.DB, walletEpoch int64) error
}

// BoundLaneEpayOrder is opaque, scoped to an exclusive ordinary quoted order
// and a canonical co-located store. A zero value is not an order or authority.
// No untagged runtime constructor or HTTP entry point enrolls it in this stage.
type BoundLaneEpayOrder struct {
	scope     laneEpaySettlementScope
	authority laneEpayTxAuthority
}

func (b *BoundLaneEpayOrder) ApplyVerified(ctx context.Context, notification LaneEpayVerifiedNotification) (LaneEpayOutcome, error) {
	return b.settle(ctx, &laneVerifiedEpayCapture{notification.TradeNo, notification.Method, notification.Money, notification.Facts})
}

// Reconcile reopens only the original verified receipt. It cannot receive
// provider facts or assert a capture for an ordinary unpaid order.
func (b *BoundLaneEpayOrder) Reconcile(ctx context.Context) (LaneEpayOutcome, error) {
	return b.settle(ctx, nil)
}

func (b *BoundLaneEpayOrder) settle(ctx context.Context, received *laneVerifiedEpayCapture) (LaneEpayOutcome, error) {
	if b == nil || b.authority == nil {
		return LaneEpayOutcome{}, ErrLaneStoreAuthorityUnavailable
	}
	out, err := settleLaneEpayWithAuthority(ctx, b.scope, b.authority, received)
	if err != nil {
		return LaneEpayOutcome{}, err
	}
	return LaneEpayOutcome{State: out.state, ReceiptID: out.receiptID, Replayed: out.replayed}, nil
}

// Status observes one exact captured order. It neither parks a capture nor
// commits credit even if headroom became available. The authority may lock
// the release epoch, so the consistent snapshot is not a SQL READ ONLY
// transaction; this application path performs no writes.
func (b *BoundLaneEpayOrder) Status(ctx context.Context) (LaneEpayOrderStatus, error) {
	var out LaneEpayOrderStatus
	if b == nil || b.authority == nil {
		return out, ErrLaneStoreAuthorityUnavailable
	}
	scope := b.scope
	if !validLaneEpaySettlementScope(ctx, scope) {
		return out, ErrSharedWalletScope
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	err := scope.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := b.authority.CheckTx(tx, scope.db, LOG_DB, scope.walletEpoch); err != nil {
			return err
		}
		s := laneWalletCustodyScope{tx: tx, schema: scope.schema, userID: scope.userID, walletEpoch: scope.walletEpoch}
		t, err := s.lockableTx(tx, scope.userID)
		if err != nil {
			return err
		}
		order, err := loadLaneEpayOrderTx(t, scope, false)
		if err != nil {
			return err
		}
		switch order.Status {
		case LaneEpayCredited:
			if order.CompleteTime <= 0 {
				return errLaneEpayCaptureEvidence
			}
		case LaneEpayCapturedWaiting:
			if order.CompleteTime != 0 {
				return errLaneEpayCaptureEvidence
			}
		default:
			return ErrTopUpStatusInvalid
		}
		_, recordedAt, err := inspectLaneEpayReceiptTx(t, scope, &order)
		if err != nil {
			return err
		}
		entry := LedgerEntry{UserId: order.UserId, Account: AccountPaid, Delta: order.QuotedWalletQuota, Reason: LedgerReasonTopup,
			ReferenceType: ReferralReferenceTopUp, ReferenceId: order.TradeNo}
		if err := verifyLaneEpayOutputsTx(t, &order, entry, order.Status == LaneEpayCredited); err != nil {
			return err
		}
		out = LaneEpayOrderStatus{LaneEpayOutcome: LaneEpayOutcome{State: order.Status, ReceiptID: order.PaymentReceiptID, Replayed: true},
			ReceiptRecordedAt: recordedAt, CreditCompletedAt: order.CompleteTime}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return LaneEpayOrderStatus{}, err
	}
	return out, nil
}
