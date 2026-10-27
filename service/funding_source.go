package service

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"gorm.io/gorm"
)

// FundingSource is retained as a service compatibility alias while ownership
// of the runtime contract lives in the stdlib-only moneyport package.
type FundingSource = moneyport.FundingSource

var (
	_ FundingSource                           = (*SubscriptionFunding)(nil)
	_ moneyport.ReservableFunding             = (*SubscriptionFunding)(nil)
	_ transactionalSettlementFunding          = (*SubscriptionFunding)(nil)
	_ moneyport.HostBillingFinalizationSource = (*SubscriptionFunding)(nil)
)

// ---------------------------------------------------------------------------
// SubscriptionFunding — 订阅资金来源实现
// ---------------------------------------------------------------------------

type SubscriptionFunding struct {
	requestId      string
	userId         int
	modelName      string
	amount         int64 // 预扣的订阅额度（subConsume）
	subscriptionId int
	quotaPeriod    int64
	preConsumed    int64
	// reserveDelta mirrors the durable receipt's extra reservation. Refund owns
	// the entire receipt; BillingSession must not return this amount separately.
	reserveDelta int
	// 以下字段在 PreConsume 成功后填充，供 RelayInfo 同步使用
	AmountTotal     int64
	AmountUsedAfter int64
	SnapshotUnknown bool
	PlanId          int
	PlanTitle       string
}

func (s *SubscriptionFunding) Source() string { return BillingSourceSubscription }

func (s *SubscriptionFunding) UsesHostBillingFinalization() bool { return true }

func (s *SubscriptionFunding) BillingIdentity() model.SubscriptionBillingIdentity {
	return model.SubscriptionBillingIdentity{
		RequestID: s.requestId, UserID: s.userId, SubscriptionID: s.subscriptionId,
		QuotaPeriod: s.quotaPeriod, PreConsumed: s.preConsumed,
	}
}

func (s *SubscriptionFunding) PreConsume(amount int) error {
	var result moneyport.SettlementResult
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = s.PreConsumeInTx(tx, amount)
		return err
	})
	if err != nil {
		return err
	}
	s.ApplyReservationResult(amount, result)
	return nil
}

// PreConsumeInTx does not publish mutable funding state before the host-owned
// source/token/held receipt transaction has actually committed.
func (s *SubscriptionFunding) PreConsumeInTx(tx *gorm.DB, _ int) (moneyport.SettlementResult, error) {
	res, err := model.PreConsumeUserSubscriptionTx(tx, s.requestId, s.userId, s.modelName, 0, s.amount)
	if err != nil {
		return moneyport.SettlementResult{}, err
	}
	return moneyport.SettlementResult{
		Replayed: res.Replayed, SourceID: res.UserSubscriptionId, SourcePeriod: res.QuotaPeriod,
		SourceHeld: int(res.ReservedAmount), OriginalAmount: int(res.PreConsumed),
		AmountTotal: res.AmountTotal, AmountUsedAfter: res.AmountUsedAfter,
	}, nil
}

func (s *SubscriptionFunding) ApplyReservationResult(_ int, result moneyport.SettlementResult) {
	s.subscriptionId, s.quotaPeriod = result.SourceID, result.SourcePeriod
	s.preConsumed = int64(result.OriginalAmount)
	s.reserveDelta = result.SourceHeld - result.OriginalAmount
	s.AmountTotal = result.AmountTotal
	// RelayInfo currently adds extraReserved when displaying the preconsume
	// snapshot. Store the base here so replay/Reserve cannot double that extra.
	s.AmountUsedAfter = result.AmountUsedAfter - int64(s.reserveDelta)
	s.SnapshotUnknown = result.SnapshotUnknown
	s.PlanId, s.PlanTitle = result.SourcePlanID, result.SourcePlanTitle
}

func (s *SubscriptionFunding) Settle(delta int) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := s.SettleInTx(tx, delta)
		return err
	})
}

func (s *SubscriptionFunding) SettleInTx(tx *gorm.DB, delta int) (moneyport.SettlementResult, error) {
	if s.amount <= 0 || s.amount > common.MaxQuota || s.preConsumed != s.amount || s.reserveDelta < 0 || int64(s.reserveDelta) > common.MaxQuota-s.amount || delta < -common.MaxQuota || delta > common.MaxQuota {
		return moneyport.SettlementResult{}, errors.New("invalid subscription settlement delta")
	}
	actual := s.amount + int64(s.reserveDelta) + int64(delta)
	applied, err := model.SettleSubscriptionPreConsumeTx(tx, s.BillingIdentity(), actual)
	return moneyport.SettlementResult{Replayed: !applied}, err
}

// The receipt is the authoritative state; no separate cache mirrors allowances.
func (s *SubscriptionFunding) ApplySettlementResult(_ int, _ moneyport.SettlementResult) {}

// Reserve persists an absolute target, including on replay from a new object.
func (s *SubscriptionFunding) Reserve(target int) error {
	var result moneyport.SettlementResult
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = s.ReserveInTx(tx, target)
		return err
	})
	if err != nil {
		return err
	}
	s.ApplyReservationResult(target, result)
	return nil
}

func (s *SubscriptionFunding) ReserveInTx(tx *gorm.DB, target int) (moneyport.SettlementResult, error) {
	identity := s.BillingIdentity()
	before, err := model.ReadSubscriptionPreConsumeTx(tx, identity)
	if err != nil {
		return moneyport.SettlementResult{}, err
	}
	if err := model.ReserveSubscriptionPreConsumeTx(tx, identity, int64(target)); err != nil {
		return moneyport.SettlementResult{}, err
	}
	after, err := model.ReadSubscriptionPreConsumeTx(tx, identity)
	if err != nil {
		return moneyport.SettlementResult{}, err
	}
	return moneyport.SettlementResult{
		Replayed: after.ReservedAmount == before.ReservedAmount,
		SourceID: after.UserSubscriptionId, SourcePeriod: after.QuotaPeriod,
		SourceHeld: int(after.ReservedAmount), OriginalAmount: int(after.PreConsumed),
		AmountTotal: after.AmountTotal, AmountUsedAfter: after.AmountUsedAfter,
	}, nil
}

func (s *SubscriptionFunding) Refund() error {
	if s.preConsumed <= 0 {
		return nil
	}
	return refundWithRetry(func() error {
		return model.DB.Transaction(func(tx *gorm.DB) error {
			_, err := model.RefundSubscriptionPreConsumeTx(tx, s.BillingIdentity())
			return err
		})
	})
}

// refundWithRetry 尝试多次执行退款操作以提高成功率，只能用于基于事务的退款函数！！！！！！
// try to refund with retries, only for refund functions based on transactions!!!
func refundWithRetry(fn func() error) error {
	if fn == nil {
		return nil
	}
	const maxAttempts = 3
	var lastErr error
	for i := range maxAttempts {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i < maxAttempts-1 {
			time.Sleep(time.Duration(200*(i+1)) * time.Millisecond)
		}
	}
	return lastErr
}
