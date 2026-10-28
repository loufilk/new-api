package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Lose the acknowledgement of the first N actual SQL COMMITs. N=2 covers
// initial reservation plus refund admission, with no immediate worker scheduled.
type initialReservationCommitLossPool struct {
	*sql.DB
	lostCommits int32
	commits     atomic.Int32
}

type initialReservationCommitLossTx struct {
	*sql.Tx
	pool *initialReservationCommitLossPool
}

func (p *initialReservationCommitLossPool) BeginTx(ctx context.Context, options *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.DB.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &initialReservationCommitLossTx{Tx: tx, pool: p}, nil
}

func (tx *initialReservationCommitLossTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if tx.pool.commits.Add(1) <= tx.pool.lostCommits {
		return errors.New("synthetic initial COMMIT acknowledgement lost")
	}
	return nil
}

func initialReservationCommitLoss(t *testing.T, count int32) *initialReservationCommitLossPool {
	t.Helper()
	original := model.DB
	sqlDB, err := original.DB()
	require.NoError(t, err)
	pool := &initialReservationCommitLossPool{DB: sqlDB, lostCommits: count}
	wrapped := original.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
	model.DB = wrapped
	t.Cleanup(func() { model.DB = original })
	return pool
}

func TestInitialReservationRecoveryPostgres(t *testing.T) {
	// A fresh schema on the explicitly supplied disposable PostgreSQL instance.
	// Do not widen this regression into an unrelated compatibility matrix.
	openTaskPollingSQLFixture(t, "postgres")
	require.NoError(t, model.DB.AutoMigrate(&model.BillingFinalization{}, &model.Model{}, &model.Vendor{}, &model.ModelBonusEligibleAudit{}, &model.SubscriptionPlan{}, &model.SystemTask{}, &model.SystemTaskLock{}, &model.Midjourney{}))
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			t.Run("existing_owner_replay_is_not_abort_authority", func(t *testing.T) {
				s, _ := settlementDeadlineSession(t, "initial-replay-live", true, source)
				before := captureLifecycleAccounting(t)
				c, info := lifecycleRequest(t, s, s.relayInfo.RequestId)
				initialReservationCommitLoss(t, 1)
				failed, apiErr := NewBillingSession(c, info, 100)
				require.Nil(t, failed)
				require.NotNil(t, apiErr)
				require.ErrorContains(t, apiErr.Err, "synthetic initial COMMIT")
				require.Equal(t, before, captureLifecycleAccounting(t), "a replay's lost read-only COMMIT cannot refund another live session")
				ready, err := model.GetReadyBillingRefunds(t.Context(), 10)
				require.NoError(t, err)
				require.Empty(t, ready)
			})
			for _, fault := range []string{"admission_read", "admission_write", "admission_and_review_unavailable"} {
				t.Run(fault, func(t *testing.T) {
					s, _ := settlementDeadlineSession(t, "initial-review-baseline", true, source)
					c, info := lifecycleRequest(t, s, "initial-review-failed")
					logs := captureUnknownDeliveryLog(t, s)
					pool := initialReservationCommitLoss(t, 1)
					var hits atomic.Int32
					const hook = "initial-recovery-admission-failure"
					injected := errors.New("synthetic-secret admission database details")
					if fault == "admission_write" {
						require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
							values, ok := tx.Statement.Dest.(map[string]any)
							if tx.Statement.Table == "billing_finalizations" && ok && values["state"] == model.BillingRefundReady {
								hits.Add(1)
								tx.AddError(injected)
							}
						}))
						t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(hook)) })
					} else {
						require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
							if pool.commits.Load() > 0 && tx.Statement.Table == "billing_finalizations" && (fault == "admission_and_review_unavailable" || hits.Load() == 0) {
								hits.Add(1)
								tx.AddError(injected)
							}
						}))
						t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(hook)) })
					}
					failed, apiErr := NewBillingSession(c, info, 100)
					require.Nil(t, failed)
					require.NotNil(t, apiErr)
					require.ErrorContains(t, apiErr.Err, "synthetic initial COMMIT", "recovery failure must not replace the original request error")
					require.Positive(t, hits.Load())
					if fault == "admission_write" {
						require.NoError(t, model.DB.Callback().Update().Remove(hook))
					} else {
						require.NoError(t, model.DB.Callback().Query().Remove(hook))
					}
					owner := lifecycleOwner(t, info.RequestId)
					require.Equal(t, model.BillingHeld, owner.State)
					requireLifecycleAccounting(t, source, 200)
					ready, err := model.GetReadyBillingRefunds(t.Context(), 10)
					require.NoError(t, err)
					require.Empty(t, ready)
					if fault != "admission_and_review_unavailable" {
						require.Equal(t, "initial_refund_unconfirmed", owner.LastErrorCode)
						require.Positive(t, owner.EscalatedAt)
						page, err := model.ReadFinancialReviewReport(t.Context(), model.DB, model.FinancialReviewReportOptions{Entity: model.FinancialReviewBilling, Limit: 10})
						require.NoError(t, err)
						found := false
						for _, item := range page.Items {
							if item.RequestID == info.RequestId {
								found = true
								require.Contains(t, item.Issues, "initial_reservation_refund_requires_review")
							}
						}
						require.True(t, found)
					} else {
						require.Empty(t, owner.LastErrorCode, "DB unavailability prevents durable metadata; do not claim otherwise")
					}
					logs.mu.Lock()
					line := logs.buffer.String()
					logs.mu.Unlock()
					for _, field := range []string{"event=billing_initial_reservation_refund_unresolved", "priority=high", fmt.Sprintf("request_id=%q", info.RequestId), "stage=refund_admission", "code=recovery_error", "action=manual_reconciliation"} {
						require.Contains(t, line, field)
					}
					for _, secret := range []string{injected.Error(), info.TokenKey, model.BillingTokenFingerprint(info.TokenKey)} {
						require.NotContains(t, line, secret)
					}
					before := captureLifecycleAccounting(t)
					recoveryFaultPass(t, model.SystemTaskStatusSucceeded)
					require.Equal(t, before, captureLifecycleAccounting(t), "held rows remain review evidence, never blanket refund authority")
				})
			}
			for _, changed := range []string{"none", "replacement_token", "new_subscription_period"} {
				if changed == "new_subscription_period" && source != BillingSourceSubscription {
					continue
				}
				t.Run("admission_ack_lost/"+changed, func(t *testing.T) {
					s, _ := settlementDeadlineSession(t, "initial-queued-baseline", true, source)
					c, info := lifecycleRequest(t, s, "initial-queued-failed")
					pool := initialReservationCommitLoss(t, 2)
					failed, apiErr := NewBillingSession(c, info, 100)
					require.Nil(t, failed)
					require.NotNil(t, apiErr)
					require.ErrorContains(t, apiErr.Err, "synthetic initial COMMIT")
					require.GreaterOrEqual(t, pool.commits.Load(), int32(2))
					owner := lifecycleOwner(t, info.RequestId)
					require.Equal(t, model.BillingRefundReady, owner.State)
					requireLifecycleAccounting(t, source, 200)
					if changed == "replacement_token" {
						require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", info.TokenId).Update("key", "synthetic-replacement-token").Error)
					} else if changed == "new_subscription_period" {
						require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", owner.SubscriptionID).Updates(map[string]any{"quota_period": 2, "amount_used": 23}).Error)
					}
					before := captureLifecycleAccounting(t)
					recoveryFaultPass(t, model.SystemTaskStatusSucceeded)
					after := lifecycleOwner(t, info.RequestId)
					if changed == "replacement_token" {
						require.Equal(t, model.BillingRefundReview, after.State)
						require.Equal(t, before.WalletTotal, captureLifecycleAccounting(t).WalletTotal)
						require.Equal(t, before.TokenRemain, captureLifecycleAccounting(t).TokenRemain)
						require.Equal(t, before.Subscriptions, captureLifecycleAccounting(t).Subscriptions)
					} else {
						require.Equal(t, model.BillingRefunded, after.State)
						if changed == "new_subscription_period" {
							var sub model.UserSubscription
							require.NoError(t, model.DB.First(&sub, owner.SubscriptionID).Error)
							require.EqualValues(t, 2, sub.QuotaPeriod)
							require.EqualValues(t, 23, sub.AmountUsed, "old-period refund cannot credit the new allowance")
							require.Equal(t, 900, getTokenRemainQuota(t, info.TokenId))
						} else {
							requireLifecycleAccounting(t, source, 100)
						}
					}
					stable := captureLifecycleAccounting(t)
					recoveryFaultPass(t, model.SystemTaskStatusSucceeded)
					require.Equal(t, stable, captureLifecycleAccounting(t), "a subsequent worker cannot repeat any credit")
				})
			}
		})
	}
	t.Run("trusted_zero", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "initial-zero-baseline", true, BillingSourceWallet)
		c, info := lifecycleRequest(t, s, "initial-zero-failed")
		setting := operation_setting.GetQuotaSetting()
		old := setting.TrustQuotaUSD
		setting.TrustQuotaUSD = 1 / common.QuotaPerUnit
		t.Cleanup(func() { setting.TrustQuotaUSD = old })
		initialReservationCommitLoss(t, 1)
		failed, apiErr := NewBillingSession(c, info, 100)
		waitRefundCallback(t, nil)
		require.Nil(t, failed)
		require.NotNil(t, apiErr)
		owner := lifecycleOwner(t, info.RequestId)
		require.Equal(t, model.BillingRefunded, owner.State)
		require.Zero(t, owner.SourceHeld)
		require.Zero(t, owner.TokenHeld)
		requireLifecycleAccounting(t, BillingSourceWallet, 100)
	})
	t.Run("malformed_log_identity", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "initial-log-baseline", true, BillingSourceWallet)
		logs := captureUnknownDeliveryLog(t, s)
		logInitialReservationRefundFailure("bad\nidentity", "refund_admission", errors.New("synthetic-secret"))
		logs.mu.Lock()
		line := logs.buffer.String()
		logs.mu.Unlock()
		require.Contains(t, line, `request_id="" request_id_unavailable=true`)
		require.False(t, strings.Contains(line, "bad\nidentity"))
		require.NotContains(t, line, "synthetic-secret")
	})
}
