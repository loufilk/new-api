package service

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Snapshot the financial evidence, not just a caller's flags: a rolled-back
// reservation must leave neither a partial source/token update nor an orphaned
// hold, source receipt or ledger idempotency header.
type lifecycleAccountingSnapshot struct {
	WalletTotal, Paid, Bonus, Reward int
	TokenRemain, TokenUsed           int
	Subscriptions                    []model.UserSubscription
	SubscriptionReceipts             []model.SubscriptionPreConsumeRecord
	Ledger                           []model.BalanceLedger
	Operations                       []model.BalanceLedgerOperation
	Owners                           []model.BillingFinalization
}

func captureLifecycleAccounting(t *testing.T) lifecycleAccountingSnapshot {
	t.Helper()
	var user model.User
	var token model.Token
	require.NoError(t, model.DB.First(&user, 93201).Error)
	require.NoError(t, model.DB.First(&token, 93202).Error)
	snapshot := lifecycleAccountingSnapshot{WalletTotal: user.Quota, Paid: user.QuotaPaid, Bonus: user.QuotaBonus, Reward: user.QuotaReward, TokenRemain: token.RemainQuota, TokenUsed: token.UsedQuota}
	require.NoError(t, model.DB.Where("user_id = ?", 93201).Order("id").Find(&snapshot.Subscriptions).Error)
	require.NoError(t, model.DB.Where("user_id = ?", 93201).Order("id").Find(&snapshot.SubscriptionReceipts).Error)
	require.NoError(t, model.DB.Where("user_id = ?", 93201).Order("id").Find(&snapshot.Ledger).Error)
	require.NoError(t, model.DB.Where("user_id = ?", 93201).Order("id").Find(&snapshot.Operations).Error)
	require.NoError(t, model.DB.Where("user_id = ?", 93201).Order("request_id").Find(&snapshot.Owners).Error)
	return snapshot
}

func lifecycleRequest(t *testing.T, baseline *BillingSession, requestID string) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	c := testGinContext(t)
	c.Request = httptest.NewRequest("POST", "/lifecycle-atomic", nil)
	c.Set("token_quota", getTokenRemainQuota(t, baseline.relayInfo.TokenId))
	info := &relaycommon.RelayInfo{
		UserId: baseline.relayInfo.UserId, TokenId: baseline.relayInfo.TokenId,
		TokenKey: baseline.relayInfo.TokenKey, RequestId: requestID,
		OriginModelName: baseline.relayInfo.OriginModelName,
		UserSetting:     baseline.relayInfo.UserSetting,
	}
	return c, info
}

func lifecycleOwner(t *testing.T, requestID string) model.BillingFinalization {
	t.Helper()
	var row model.BillingFinalization
	require.NoError(t, model.DB.First(&row, "request_id = ?", requestID).Error)
	return row
}

func requireLifecycleAccounting(t *testing.T, source string, totalHeld int) {
	t.Helper()
	wallet := 1000 - totalHeld
	if source == BillingSourceSubscription {
		wallet = 1000
		var sub model.UserSubscription
		require.NoError(t, model.DB.First(&sub, 93203).Error)
		require.EqualValues(t, 500+totalHeld, sub.AmountUsed)
		require.EqualValues(t, 1, sub.QuotaPeriod)
	}
	assertSettlementBalances(t, wallet, 1000-totalHeld)
}

// All these faults happen BEFORE the named SQL statement. The COMMIT/lost-ACK
// cases below use the separately audited real sql.Tx wrapper instead.
func failLifecycleWrite(t *testing.T, entity any, create bool) (error, *atomic.Int32, func()) {
	t.Helper()
	db := model.DB
	statement := &gorm.Statement{DB: db}
	require.NoError(t, statement.Parse(entity))
	table := statement.Schema.Table
	injected := fmt.Errorf("injected lifecycle write failure: %s", table)
	hits := &atomic.Int32{}
	name := "lifecycle_write_" + t.Name()
	callback := func(tx *gorm.DB) {
		if tx.Statement.Table == table {
			hits.Add(1)
			tx.AddError(injected)
		}
	}
	if create {
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, callback))
	} else {
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, callback))
	}
	var once sync.Once
	remove := func() {
		once.Do(func() {
			if create {
				require.NoError(t, db.Callback().Create().Remove(name))
			} else {
				require.NoError(t, db.Callback().Update().Remove(name))
			}
		})
	}
	t.Cleanup(remove)
	return injected, hits, remove
}

func TestBillingLifecycleInitialReservationRollback(t *testing.T) {
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			for _, boundary := range []string{"source_receipt", "token", "held"} {
				t.Run(boundary, func(t *testing.T) {
					baseline, _ := settlementDeadlineSession(t, "lifecycle-initial-baseline", true, source)
					before := captureLifecycleAccounting(t)
					c, info := lifecycleRequest(t, baseline, "lifecycle-initial-failed")
					var entity any = &model.BillingFinalization{}
					create := false
					switch boundary {
					case "source_receipt":
						entity, create = &model.BalanceLedger{}, true
						if source == BillingSourceSubscription {
							entity = &model.SubscriptionPreConsumeRecord{}
						}
					case "token":
						entity = &model.Token{}
					}
					injected, hits, remove := failLifecycleWrite(t, entity, create)
					session, apiErr := NewBillingSession(c, info, 100)
					require.Nil(t, session)
					require.NotNil(t, apiErr)
					require.ErrorIs(t, apiErr.Err, injected)
					require.EqualValues(t, 1, hits.Load(), "the intended write boundary must actually be reached")
					remove()
					require.Equal(t, before, captureLifecycleAccounting(t))
					ready, err := model.GetReadyBillingRefunds(t.Context(), 100)
					require.NoError(t, err)
					require.Empty(t, ready, "a failed hold is not automatic refund authorization")
					// A successful explicit retry reserves both sides only once.
					session, apiErr = NewBillingSession(c, info, 100)
					require.Nil(t, apiErr)
					require.NotNil(t, session)
					requireLifecycleAccounting(t, source, 200)
					owner := lifecycleOwner(t, info.RequestId)
					require.Equal(t, model.BillingHeld, owner.State)
					require.Equal(t, 100, owner.SourceHeld)
					require.Equal(t, 100, owner.TokenHeld)
				})
			}
		})
	}
}

func TestBillingLifecycleReserveRollback(t *testing.T) {
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			for _, boundary := range []string{"source_receipt", "token", "held"} {
				t.Run(boundary, func(t *testing.T) {
					s, _ := settlementDeadlineSession(t, "lifecycle-reserve-rollback", true, source)
					before, receipt := captureLifecycleAccounting(t), s.Receipt()
					var entity any = &model.BillingFinalization{}
					create := false
					switch boundary {
					case "source_receipt":
						entity, create = &model.BalanceLedger{}, true
						if source == BillingSourceSubscription {
							entity, create = &model.SubscriptionPreConsumeRecord{}, false
						}
					case "token":
						entity = &model.Token{}
					}
					injected, hits, remove := failLifecycleWrite(t, entity, create)
					require.ErrorIs(t, s.Reserve(200), injected)
					require.EqualValues(t, 1, hits.Load())
					remove()
					require.Equal(t, before, captureLifecycleAccounting(t))
					require.Equal(t, receipt, s.Receipt(), "a rolled-back source receipt must not be published")
					require.Equal(t, 100, s.GetPreConsumedQuota())
					ready, err := model.GetReadyBillingRefunds(t.Context(), 100)
					require.NoError(t, err)
					require.Empty(t, ready)
					require.NoError(t, s.Reserve(200), "authoritative readback must recover the previous hold before retrying")
					require.NoError(t, s.Reserve(200))
					requireLifecycleAccounting(t, source, 200)
					owner := lifecycleOwner(t, s.relayInfo.RequestId)
					require.Equal(t, model.BillingHeld, owner.State)
					require.Equal(t, 200, owner.SourceHeld)
					require.Equal(t, 200, owner.TokenHeld)
				})
			}
		})
	}
}

func TestBillingLifecycleRefundAdmissionFailurePreservesHeld(t *testing.T) {
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			s, _ := settlementDeadlineSession(t, "lifecycle-refund-admission", true, source)
			held := lifecycleOwner(t, s.relayInfo.RequestId)
			require.Equal(t, model.BillingHeld, held.State)
			before := captureLifecycleAccounting(t)
			injected, hits, remove := failLifecycleWrite(t, &model.BillingFinalization{}, false)
			owner, err := model.RequestBillingRefund(t.Context(), held)
			require.ErrorIs(t, err, injected)
			require.Nil(t, owner)
			require.EqualValues(t, 1, hits.Load())
			remove()
			require.Equal(t, before, captureLifecycleAccounting(t), "the first intent write failure must preserve recoverable held evidence")
			ready, err := model.GetReadyBillingRefunds(t.Context(), 100)
			require.NoError(t, err)
			require.Empty(t, ready)
			executeBillingRefund(t.Context(), held.RequestID)
			require.Equal(t, before, captureLifecycleAccounting(t), "even an explicit executor call cannot turn a held row into refund permission")
		})
	}
}

func TestBillingLifecycleReadyRefundRecoversWithoutRequestWorker(t *testing.T) {
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			s, _ := settlementDeadlineSession(t, "lifecycle-ready-recovery", true, source)
			held := lifecycleOwner(t, s.relayInfo.RequestId)
			legacy := held
			legacy.RequestID, legacy.State = "lifecycle-historical-pending", model.BillingRefundPending
			legacy.CreatedAt, legacy.UpdatedAt = 0, 0
			require.NoError(t, model.DB.Create(&legacy).Error)
			legacyBefore := lifecycleOwner(t, legacy.RequestID)
			// Admit directly and deliberately do not call BillingSession.Refund:
			// no request-scoped goroutine is scheduled to execute this intent.
			owner, err := model.RequestBillingRefund(t.Context(), held)
			require.NoError(t, err)
			require.Equal(t, model.BillingRefundReady, owner.State)
			require.Equal(t, model.BillingRefundReady, lifecycleOwner(t, held.RequestID).State)
			requireLifecycleAccounting(t, source, 100)
			ready, err := model.GetReadyBillingRefunds(t.Context(), 100)
			require.NoError(t, err)
			require.Len(t, ready, 1)
			require.Equal(t, held.RequestID, ready[0].RequestID, "historical refund_pending is never in the v2 execution set")
			executeBillingRefund(t.Context(), ready[0].RequestID)
			require.Equal(t, model.BillingRefunded, lifecycleOwner(t, held.RequestID).State)
			requireLifecycleAccounting(t, source, 0)
			after := captureLifecycleAccounting(t)
			executeBillingRefund(t.Context(), held.RequestID)
			executeBillingRefund(t.Context(), legacy.RequestID)
			require.Equal(t, after, captureLifecycleAccounting(t), "replaying recovery must not repeat source or token credit")
			ready, err = model.GetReadyBillingRefunds(t.Context(), 100)
			require.NoError(t, err)
			require.Empty(t, ready)
			require.Equal(t, legacyBefore, lifecycleOwner(t, legacy.RequestID))
			if source == BillingSourceWallet {
				var operations int64
				require.NoError(t, model.DB.Model(&model.BalanceLedgerOperation{}).Where("idempotency_key = ?", "billing:refund:"+held.RequestID).Count(&operations).Error)
				require.EqualValues(t, 1, operations)
			} else {
				var record model.SubscriptionPreConsumeRecord
				require.NoError(t, model.DB.Where("request_id = ?", held.RequestID).First(&record).Error)
				require.Equal(t, "refunded", record.Status)
			}
		})
	}
}

func TestBillingLifecycleUnknownDeliveryNeverAdmitsRefund(t *testing.T) {
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			s, _ := settlementDeadlineSession(t, "lifecycle-unknown-delivery", true, source)
			held := lifecycleOwner(t, s.relayInfo.RequestId)
			require.NoError(t, model.MarkBillingDeliveryUnknown(t.Context(), held))
			unknown := lifecycleOwner(t, held.RequestID)
			require.Equal(t, model.BillingDeliveryUnknown, unknown.State)
			before := captureLifecycleAccounting(t)
			_, err := model.RequestBillingRefund(t.Context(), held)
			require.ErrorIs(t, err, model.ErrBillingFinalized)
			ready, err := model.GetReadyBillingRefunds(t.Context(), 100)
			require.NoError(t, err)
			require.Empty(t, ready)
			executeBillingRefund(t.Context(), held.RequestID)
			require.Equal(t, before, captureLifecycleAccounting(t), "ambiguous upstream delivery is evidence for review, not an automatic refund decision")
		})
	}
}

func TestBillingLifecycleTrustedZeroHasDurableHeldReceipt(t *testing.T) {
	baseline, _ := settlementDeadlineSession(t, "lifecycle-trust-baseline", true, BillingSourceWallet)
	setting := operation_setting.GetQuotaSetting()
	oldTrust := setting.TrustQuotaUSD
	setting.TrustQuotaUSD = 0.0001
	t.Cleanup(func() { setting.TrustQuotaUSD = oldTrust })
	c, info := lifecycleRequest(t, baseline, "lifecycle-trusted-zero")
	s, apiErr := NewBillingSession(c, info, 100)
	require.Nil(t, apiErr)
	require.NotNil(t, s)
	require.True(t, s.trusted)
	require.Zero(t, s.GetPreConsumedQuota())
	requireLifecycleAccounting(t, BillingSourceWallet, 100)
	held := lifecycleOwner(t, info.RequestId)
	require.Equal(t, model.BillingHeld, held.State)
	require.Zero(t, held.SourceHeld)
	require.Zero(t, held.TokenHeld)
	require.Zero(t, held.PreConsumed)
	require.Equal(t, info.TokenId, held.TokenID)
	require.Equal(t, model.BillingTokenFingerprint(info.TokenKey), held.TokenFingerprint)
	before := captureLifecycleAccounting(t)
	require.NoError(t, s.Reserve(250), "ordinary Reserve must preserve the existing trusted bypass")
	require.Equal(t, before, captureLifecycleAccounting(t))
	ready, err := model.GetReadyBillingRefunds(t.Context(), 100)
	require.NoError(t, err)
	require.Empty(t, ready)
}

func TestBillingLifecycleReservationCommitAckRecovery(t *testing.T) {
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			for _, phase := range []string{"initial", "reserve"} {
				t.Run(phase, func(t *testing.T) {
					baseline, _ := settlementDeadlineSession(t, "lifecycle-ack-baseline", true, source)
					c, info := lifecycleRequest(t, baseline, "lifecycle-initial-ack")
					flag := terminalCommitFault(t, 2) // real SQL COMMIT, then an error instead of its ACK.
					s := baseline
					if phase == "initial" {
						failed, apiErr := NewBillingSession(c, info, 100)
						require.Nil(t, failed)
						require.NotNil(t, apiErr)
						require.ErrorContains(t, apiErr.Err, "synthetic committed ACK lost")
						waitRefundCallback(t, nil)
						require.Zero(t, flag.Load())
						requireLifecycleAccounting(t, source, 100)
						owner := lifecycleOwner(t, info.RequestId)
						require.Equal(t, model.BillingRefunded, owner.State, "failed fresh initial request schedules recovery without same-ID retry")
						require.Equal(t, 100, owner.SourceHeld)
						require.Equal(t, 100, owner.TokenHeld)
						stable := captureLifecycleAccounting(t)
						executeBillingRefund(t.Context(), info.RequestId)
						require.Equal(t, stable, captureLifecycleAccounting(t))
						return
					} else {
						require.ErrorContains(t, s.Reserve(200), "synthetic committed ACK lost")
						require.True(t, s.reservationUncertain)
						require.Equal(t, 100, s.GetPreConsumedQuota(), "unacknowledged commit is not yet published in local state")
						info = s.relayInfo
					}
					require.Zero(t, flag.Load(), "the real transaction COMMIT wrapper must have executed")
					requireLifecycleAccounting(t, source, 200)
					stored := lifecycleOwner(t, info.RequestId)
					require.Equal(t, model.BillingHeld, stored.State)
					expectedHeld := 200
					require.Equal(t, expectedHeld, stored.SourceHeld)
					require.Equal(t, expectedHeld, stored.TokenHeld)
					ready, err := model.GetReadyBillingRefunds(t.Context(), 100)
					require.NoError(t, err)
					require.Empty(t, ready, "an unknown reservation ACK never implies a failed upstream request")
					committed := captureLifecycleAccounting(t)
					executeBillingRefund(t.Context(), info.RequestId)
					require.Equal(t, committed, captureLifecycleAccounting(t))
					require.NoError(t, s.Reserve(200), "the frozen hold, not an inferred rollback, determines the recovered amount")
					require.False(t, s.reservationUncertain)
					if source == BillingSourceSubscription {
						var allowance model.UserSubscription
						require.NoError(t, model.DB.First(&allowance, stored.SubscriptionID).Error)
						require.Equal(t, allowance.AmountTotal, s.relayInfo.SubscriptionAmountTotal)
						require.Equal(t, allowance.AmountUsed, s.relayInfo.SubscriptionAmountUsedAfterPreConsume)
						require.Equal(t, allowance.PlanId, s.relayInfo.SubscriptionPlanId)
					}
					require.Equal(t, expectedHeld, s.GetPreConsumedQuota())
					require.Equal(t, committed, captureLifecycleAccounting(t), "authority readback/replay must not repeat either debit")
				})
			}
		})
	}
}
