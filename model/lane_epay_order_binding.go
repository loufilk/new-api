package model

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// The stage reserves a new, not-yet-payable order from every legacy pending
// writer. No ordinary order is ever reclassified on the way to checkout.
const laneEpayPendingCapture = "lane_epay_pending_capture"

var ErrLaneEpayExclusiveOrder = fmt.Errorf("%w: order belongs to exclusive Epay capture", ErrTopUpStatusInvalid)

func laneEpayReservedFromOrdinary(status string) bool {
	return status == laneEpayPendingCapture || status == laneEpayCaptureWaiting
}

// An opaque trusted issuer must supply a dedicated stable key and identity.
// There is deliberately no load, environment default, or ordinary constructor.
// A different key or epoch cannot adopt an already-issued order.
type laneEpayOrderSigner struct {
	keyID string
	key   [sha256.Size]byte
}

func (s *laneEpayOrderSigner) valid() bool {
	return s != nil && laneBillingOwnerIDValid(s.keyID) && s.key != ([sha256.Size]byte{})
}

// No ordinary migration registers this relation. Only an explicit isolated
// fixture creates it until the real custody schema/roles are versioned.
type laneEpayOrderBindingV1 struct {
	TradeNo     string `gorm:"primaryKey;type:varchar(128)"`
	OrderID     int    `gorm:"not null;uniqueIndex"`
	UserID      int    `gorm:"not null"`
	WalletEpoch int64  `gorm:"not null"`
	KeyID       string `gorm:"type:varchar(64);not null"`
	MAC         string `gorm:"type:varchar(64);not null"`
}

func (laneEpayOrderBindingV1) TableName() string            { return "lane_epay_order_bindings_v1" }
func (*laneEpayOrderBindingV1) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (*laneEpayOrderBindingV1) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

func (s *laneEpayOrderSigner) mac(scope laneEpaySettlementScope, order TopUp) (string, error) {
	raw, err := common.Marshal([]any{"lane-epay-order-binding-v1", s.keyID, scope.schema, scope.walletEpoch, order.Id,
		order.TradeNo, order.UserId, order.PaymentProvider, order.PaymentMethod, order.CreateTime, order.Amount,
		order.Money, order.QuotedWalletQuota, order.QuotedPaymentMinor, order.QuotedPaymentCurrency,
		order.QuotedPaymentEnvironment, order.QuotedPaymentScope})
	if err != nil {
		return "", err
	}
	hash := hmac.New(sha256.New, s.key[:])
	_, _ = hash.Write(raw)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyLaneEpayOrderBindingTx(tx *gorm.DB, scope laneEpaySettlementScope, order TopUp, signer *laneEpayOrderSigner) error {
	if !signer.valid() || (order.Status != laneEpayPendingCapture && order.Status != laneEpayCaptureWaiting && order.Status != common.TopUpStatusSuccess) {
		return ErrLaneEpayExclusiveOrder
	}
	var binding laneEpayOrderBindingV1
	if err := tx.Where("trade_no = ?", order.TradeNo).Take(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrLaneEpayExclusiveOrder
		}
		return err
	}
	want, err := signer.mac(scope, order)
	if err != nil {
		return err
	}
	if binding.TradeNo != order.TradeNo || binding.OrderID != order.Id || binding.UserID != order.UserId ||
		binding.WalletEpoch != scope.walletEpoch || binding.KeyID != signer.keyID || !hmac.Equal([]byte(binding.MAC), []byte(want)) {
		return ErrLaneEpayExclusiveOrder
	}
	return nil
}

// createBoundLaneEpayOrder may be used only before constructing or emitting
// a provider form. It does not sign a provider request or contact anybody.
// The unique local order, its status fence, and MAC commit together. A prior
// order of any kind is never adopted, even while its raw status is pending.
// The clean copy never leaves this function after an uncertain COMMIT.
func createBoundLaneEpayOrder(ctx context.Context, b *BoundLaneEpayOrder, draft TopUp) (TopUp, error) {
	if b == nil || b.authority == nil || !b.orderSigner.valid() {
		return TopUp{}, ErrLaneStoreAuthorityUnavailable
	}
	scope := b.scope
	if !validLaneEpaySettlementScope(ctx, scope) || !laneEpayQuoteMatchesScope(scope, draft) ||
		draft.Id != 0 || draft.Status != "" || draft.CreateTime != 0 || draft.CompleteTime != 0 || !laneEpayHasNoPaidFacts(draft) ||
		draft.QuotedStripePromotionAllowed || draft.AgentSimpleKind != "" || draft.AgentSimpleQuota != 0 ||
		draft.AgentSimpleDeferredQuota != 0 || draft.NewUserBenefitCreditQuota != 0 || draft.NewUserBenefitCreditAmountCNY != "" ||
		math.IsNaN(draft.Money) || math.IsInf(draft.Money, 0) ||
		!decimal.NewFromFloat(draft.Money).Shift(2).Equal(decimal.NewFromInt(draft.QuotedPaymentMinor)) {
		return TopUp{}, ErrLaneEpayExclusiveOrder
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	prepared := false
	err := scope.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := b.authority.CheckTx(tx, scope.db, LOG_DB, scope.walletEpoch); err != nil {
			return err
		}
		wallet := laneWalletCustodyScope{tx, scope.schema, scope.userID, scope.walletEpoch}
		t, err := wallet.lockableTx(tx, scope.userID)
		if err != nil {
			return err
		}
		// The same order lock used by ordinary manual/callback writers. The
		// unique insert also fences a concurrent writer when no old row
		// was visible. No UPDATE-to-enroll or duplicate-key recovery.
		var prior TopUp
		err = lockForUpdate(t).Select("id").Where("trade_no = ?", draft.TradeNo).Take(&prior).Error
		if err == nil {
			return ErrLaneEpayExclusiveOrder
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// This does not allocate a global cross-provider trade namespace;
		// just reject an already-known subscription collision before offer.
		var subscriptions []SubscriptionOrder
		if err := t.Select("id").Where("trade_no = ?", draft.TradeNo).Limit(1).Find(&subscriptions).Error; err != nil {
			return err
		}
		if len(subscriptions) != 0 {
			return ErrLaneEpayExclusiveOrder
		}
		draft.Status, draft.CreateTime = laneEpayPendingCapture, common.GetTimestamp()
		if err := t.Create(&draft).Error; err != nil {
			if isDuplicateKeyError(err) {
				return ErrLaneEpayExclusiveOrder
			}
			return err
		}
		scoped := laneWalletCustodyScope{t, scope.schema, scope.userID, scope.walletEpoch}
		if err := validateTopUpQuotaCapacity(t, draft.UserId, draft.QuotedWalletQuota, &scoped); err != nil {
			return err
		}
		mac, err := b.orderSigner.mac(scope, draft)
		if err != nil {
			return err
		}
		row := laneEpayOrderBindingV1{draft.TradeNo, draft.Id, draft.UserId, scope.walletEpoch, b.orderSigner.keyID, mac}
		if err := t.Create(&row).Error; err != nil {
			return err
		}
		prepared = true
		return nil
	})
	if err != nil {
		if prepared {
			return TopUp{}, ErrLaneStoreCommitUncertain
		}
		return TopUp{}, err
	}
	return draft, nil
}

func laneEpayHasNoPaidFacts(o TopUp) bool {
	return o.PaymentReceiptID == 0 && o.PaidProviderReference == "" && o.PaidMerchantScope == "" &&
		o.PaidEnvironment == "" && o.PaidCurrency == "" && o.PaidCurrencyExponent == 0 && o.PaidAmountMinor == 0
}
