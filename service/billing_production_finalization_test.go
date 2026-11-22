package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func productionBillingSession(t *testing.T, request string, trusted bool, hold int) *BillingSession {
	t.Helper()
	truncate(t)
	seedBonusUser(t, 31201, 300, 400)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 31201).Updates(map[string]any{"quota_reward": 300, "quota": 1000}).Error)
	seedToken(t, 31202, 31201, "synthetic-production-finalization-token", 2000)
	seedBonusEligibleModel(t, "production-finalization-model", boolPtr(true))
	setting := operation_setting.GetQuotaSetting()
	oldTrust := setting.TrustQuotaUSD
	setting.TrustQuotaUSD = 0
	if trusted {
		setting.TrustQuotaUSD = 0.0001
	}
	t.Cleanup(func() { setting.TrustQuotaUSD = oldTrust })
	c := testGinContext(t)
	c.Set("token_quota", 2000)
	info := &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "synthetic-production-finalization-token", RequestId: request, OriginModelName: "production-finalization-model", UserSetting: dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningEnabled: boolPtr(false)}}
	s, apiErr := NewBillingSession(c, info, hold)
	require.Nil(t, apiErr)
	info.Billing = s
	require.Equal(t, trusted, s.trusted)
	return s
}

func requireProductionAccounting(t *testing.T, s *BillingSession, actual int) {
	t.Helper()
	user := getUserBalances(t, 31201)
	require.Equal(t, 1000-actual, user.Quota)
	require.Equal(t, user.QuotaPaid+user.QuotaBonus+user.QuotaReward, user.Quota)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 2000-actual, token.RemainQuota)
	require.Equal(t, actual, token.UsedQuota)
	var rows []model.BalanceLedger
	require.NoError(t, model.DB.Where("reference_type = ? AND reference_id = ?", "relay", s.relayInfo.RequestId).Find(&rows).Error)
	sums := map[string]int{}
	for _, r := range rows {
		sums[r.Account] += r.Delta
	}
	require.Equal(t, user.QuotaPaid-400, sums[string(model.AccountPaid)])
	require.Equal(t, user.QuotaBonus-300, sums[string(model.AccountBonus)])
	require.Equal(t, user.QuotaReward-300, sums[string(model.AccountReward)])
}

// Selected only inside the real PostgreSQL finalization fixture. Cancellation
// with unknown delivery is a separate case: zero held does not prove zero use.
func TestProductionBillingFinalization(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) || os.Getenv("TEST_FINALIZATION_SERVICE_POSTGRES_DSN") == "" || os.Getenv("TEST_REDIS_DSN") == "" {
		t.Skip("dedicated PostgreSQL + Redis production finalization fixture required")
	}
	t.Run("responses_prelude", TestResponsesPreludeInterruptedIsNotUsageEvidence)
	t.Run("zero_hold_cancel", func(t *testing.T) {
		s := productionBillingSession(t, "production-zero-hold-cancel", true, 100)
		require.Zero(t, s.GetPreConsumedQuota())
		s.Refund(testGinContext(t)) // Caller has established no dispatched/billable work.
		waitRefundCallback(t, s)
		owner := lifecycleOwner(t, s.relayInfo.RequestId)
		require.Equal(t, model.BillingRefunded, owner.State)
		require.Zero(t, owner.SourceHeld)
		require.Zero(t, owner.TokenHeld)
		s.Refund(testGinContext(t))
		requireProductionAccounting(t, s, 0)
	})
	t.Run("unmeasured_cancel_review", func(t *testing.T) {
		s := productionBillingSession(t, "production-unmeasured-cancel", true, 100)
		PreserveUnknownBillingDelivery(s.relayInfo)
		s.Refund(testGinContext(t))
		require.ErrorIs(t, s.Settle(0), model.ErrBillingFinalized)
		require.Equal(t, model.BillingDeliveryUnknown, lifecycleOwner(t, s.relayInfo.RequestId).State)
		requireProductionAccounting(t, s, 0)
	})
	t.Run("trusted_partial_redis", func(t *testing.T) {
		s := productionBillingSession(t, "production-trusted-partial", true, 100)
		options, err := redis.ParseURL(os.Getenv("TEST_REDIS_DSN"))
		require.NoError(t, err, "real disposable Redis is required")
		client := redis.NewClient(options)
		require.NoError(t, client.Ping(t.Context()).Err())
		version, err := client.Info(t.Context(), "server").Result()
		require.NoError(t, err)
		require.Contains(t, version, "redis_version:8.8.0")
		oldClient, oldEnabled := common.RDB, common.RedisEnabled
		common.RDB, common.RedisEnabled = client, true
		t.Cleanup(func() {
			waitRefundCallback(t, s)
			common.RDB, common.RedisEnabled = oldClient, oldEnabled
			require.NoError(t, client.Close())
		})
		require.NoError(t, client.Set(t.Context(), "user:31201", "stale", time.Minute).Err())
		require.NoError(t, s.Settle(650))
		require.NoError(t, s.Settle(650))
		s.Refund(testGinContext(t))
		requireProductionAccounting(t, s, 650)
		exists, err := client.Exists(t.Context(), "user:31201").Result()
		require.NoError(t, err)
		require.Zero(t, exists, "real Redis cache invalidation must run")
		owner := lifecycleOwner(t, s.relayInfo.RequestId)
		require.Equal(t, model.BillingSettled, owner.State)
		require.Equal(t, 650, owner.ActualQuota)
	})
	t.Run("insufficient_retains_actual_and_hold", func(t *testing.T) {
		s := productionBillingSession(t, "production-settlement-insufficient", false, 100)
		require.Error(t, s.Settle(1200))
		owner := lifecycleOwner(t, s.relayInfo.RequestId)
		require.Equal(t, model.BillingSettlementReview, owner.State)
		require.Equal(t, 1200, owner.ActualQuota)
		require.Equal(t, 100, owner.SourceHeld)
		require.Equal(t, 100, owner.TokenHeld)
		require.Equal(t, "insufficient_balance", owner.LastErrorCode)
		require.Equal(t, 1, owner.Attempts)
		require.Zero(t, owner.NextRetryAt)
		for range 3 {
			s.Refund(testGinContext(t))
		}
		require.Error(t, s.Settle(0))
		require.ErrorIs(t, s.Reserve(200), model.ErrBillingFinalized)
		waitRefundCallback(t, s)
		require.Equal(t, owner, lifecycleOwner(t, s.relayInfo.RequestId))
		requireProductionAccounting(t, s, 100)
		ready, err := model.GetReadyBillingRefunds(t.Context(), 100)
		require.NoError(t, err)
		require.Empty(t, ready)
		// A new OS process has no BillingSession or pricing snapshot. It can inspect
		// the exact persisted debt, not auto-charge/refund based on current settings.
		executable, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestProductionSettlementRestartReviewProbe$", "-test.v")
		cmd.Env = append(os.Environ(), "PRODUCTION_SETTLEMENT_REVIEW_PROBE="+owner.RequestID)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		require.Contains(t, string(output), "production-settlement-restart-readonly-ok")
		require.Equal(t, owner, lifecycleOwner(t, s.relayInfo.RequestId))
		requireProductionAccounting(t, s, 100)
		// Explicit recovery retains this original funding policy and frozen fee;
		// replenishment alone never schedules a debit or changes the review owner.
		_, err = model.CreditQuota(model.LedgerEntry{UserId: 31201, Account: model.AccountPaid, Delta: 500, Reason: model.LedgerReasonAdminAdd, ReferenceType: "test", ReferenceId: "replenishment", IdempotencyKey: "production-test-replenishment"})
		require.NoError(t, err)
		require.Equal(t, owner, lifecycleOwner(t, s.relayInfo.RequestId))
		require.NoError(t, s.Settle(1200))
		require.NoError(t, s.Settle(1200))
		s.Refund(testGinContext(t))
		user := getUserBalances(t, 31201)
		require.Equal(t, 300, user.Quota)
		require.Equal(t, 300, user.QuotaPaid)
		require.Zero(t, user.QuotaBonus+user.QuotaReward)
		var token model.Token
		require.NoError(t, model.DB.First(&token, 31202).Error)
		require.Equal(t, 800, token.RemainQuota)
		require.Equal(t, 1200, token.UsedQuota)
		var net int
		require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Where("reference_type = ? AND reference_id = ?", "relay", owner.RequestID).Select("COALESCE(SUM(delta), 0)").Scan(&net).Error)
		require.Equal(t, -1200, net)
		settled := lifecycleOwner(t, owner.RequestID)
		require.Equal(t, model.BillingSettled, settled.State)
		require.Equal(t, 1200, settled.ActualQuota)
		require.Equal(t, "insufficient_balance", settled.FirstErrorCode)
	})
	t.Run("freeze_write_failure", func(t *testing.T) {
		s := productionBillingSession(t, "production-freeze-failed", false, 100)
		before := lifecycleOwner(t, s.relayInfo.RequestId)
		injected, _, remove := failLifecycleWrite(t, &model.BillingFinalization{}, false)
		require.ErrorIs(t, s.Settle(650), injected)
		remove()
		s.Refund(testGinContext(t))
		require.Equal(t, before, lifecycleOwner(t, s.relayInfo.RequestId), "failed freeze is not durable actual evidence")
		requireProductionAccounting(t, s, 100)
		require.Error(t, s.Settle(0))
		require.NoError(t, s.Settle(650))
		requireProductionAccounting(t, s, 650)
	})
	for _, phase := range []string{"intent", "money"} {
		for _, mode := range []int32{1, 2} {
			t.Run(fmt.Sprintf("%s_commit_%d", phase, mode), func(t *testing.T) {
				s := productionBillingSession(t, "production-commit-"+phase+fmt.Sprint(mode), false, 100)
				flag := terminalCommitFault(t, mode)
				if phase == "money" {
					flag.Store(0)
					name := "production_money_commit_fault"
					require.NoError(t, model.DB.Callback().Update().After("gorm:update").Register(name, func(tx *gorm.DB) {
						if tx.Statement.Table == "tokens" && tx.Error == nil {
							flag.Store(mode)
						}
					}))
					t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(name)) })
				}
				require.Error(t, s.Settle(650))
				require.Zero(t, flag.Load())
				if phase == "money" {
					require.NoError(t, model.DB.Callback().Update().Remove("production_money_commit_fault"))
				}
				s.Refund(testGinContext(t))
				owner := lifecycleOwner(t, s.relayInfo.RequestId)
				expected, charged := model.BillingSettlementReview, 100
				if phase == "intent" && mode == 1 {
					expected = model.BillingHeld
				}
				if phase == "money" && mode == 2 {
					expected, charged = model.BillingSettled, 650
				}
				require.Equal(t, expected, owner.State)
				requireProductionAccounting(t, s, charged)
				require.NoError(t, s.Settle(650))
				require.NoError(t, s.Settle(650))
				requireProductionAccounting(t, s, 650)
			})
		}
	}
	for _, failures := range []int32{2, 5} {
		t.Run(fmt.Sprintf("transient_%d", failures), func(t *testing.T) {
			s := productionBillingSession(t, "production-transient-"+fmt.Sprint(failures), false, 100)
			hits := &atomic.Int32{}
			name := "production_transient"
			require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
				if tx.Statement.Table == "tokens" && hits.Add(1) <= failures {
					tx.AddError(&pgconn.PgError{Code: "40001", Message: "synthetic serializable conflict"})
				}
			}))
			t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(name)) })
			err := s.Settle(650)
			require.EqualValues(t, 3, hits.Load(), "strictly bounded same-ID attempts")
			owner := lifecycleOwner(t, s.relayInfo.RequestId)
			if failures == 2 {
				require.NoError(t, err)
				require.Equal(t, model.BillingSettled, owner.State)
				requireProductionAccounting(t, s, 650)
			} else {
				require.Error(t, err)
				require.Equal(t, model.BillingSettlementReview, owner.State)
				require.Equal(t, "transient_database", owner.LastErrorCode)
				require.Equal(t, 3, owner.Attempts)
				require.Zero(t, owner.NextRetryAt)
				s.Refund(testGinContext(t))
				requireProductionAccounting(t, s, 100)
			}
		})
	}
	t.Run("concurrent_replay_and_refund", func(t *testing.T) {
		s := productionBillingSession(t, "production-concurrent-replay", false, 100)
		receipt := *s.terminalReceipt
		require.NoError(t, model.RequestBillingSettlement(t.Context(), receipt, 650))
		sqlDB, err := model.DB.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(8)
		t.Cleanup(func() { sqlDB.SetMaxOpenConns(1) })
		start := make(chan struct{})
		results := make(chan error, 12)
		var wg sync.WaitGroup
		for i := range 12 {
			wg.Go(func() {
				<-start
				if i%3 == 0 {
					owner, err := model.RequestBillingRefund(t.Context(), receipt)
					if err == nil {
						// Settlement may commit before admission takes the row lock.
						// Returning that terminal owner is an existing no-op contract,
						// not refund authority, and must retain the frozen charge.
						if owner == nil || owner.State != model.BillingSettled || owner.ActualQuota != 650 {
							results <- fmt.Errorf("refund returned a non-settled owner: %+v", owner)
							return
						}
					} else if !errors.Is(err, model.ErrBillingFinalized) {
						results <- err
						return
					}
					applied, err := model.ApplyBillingRefund(t.Context(), receipt.RequestID)
					if applied || !errors.Is(err, model.ErrBillingFinalized) {
						results <- fmt.Errorf("refund crossed settlement owner: applied=%t err=%v", applied, err)
						return
					}
					results <- nil
					return
				}
				_, err := model.SettleBillingFinalization(t.Context(), receipt, 650, func(tx *gorm.DB) error {
					funding := s.funding.(transactionalSettlementFunding)
					if _, err := funding.SettleInTx(tx, 550); err != nil {
						return err
					}
					return model.ApplyBillingTokenDeltaTx(tx, &receipt, -550)
				})
				results <- err
			})
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			require.NoError(t, err)
		}
		requireProductionAccounting(t, s, 650)
		owner := lifecycleOwner(t, s.relayInfo.RequestId)
		require.Equal(t, model.BillingSettled, owner.State)
		bad := receipt
		bad.UserID++
		require.Error(t, model.RequestBillingSettlement(t.Context(), bad, 650))
		require.Error(t, model.RequestBillingSettlement(t.Context(), receipt, 651))
	})
}

func TestProductionSettlementRestartReviewProbe(t *testing.T) {
	request := os.Getenv("PRODUCTION_SETTLEMENT_REVIEW_PROBE")
	if request == "" {
		return
	} // An explicit child-only entrypoint, never a skipped matrix case.
	dsn := os.Getenv("TEST_FINALIZATION_SERVICE_POSTGRES_DSN")
	require.NotEmpty(t, dsn)
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET TRANSACTION READ ONLY").Error; err != nil {
			return err
		}
		page, err := model.ReadFinancialReviewReport(t.Context(), tx, model.FinancialReviewReportOptions{Entity: model.FinancialReviewBilling, Limit: 100})
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			if item.RequestID == request {
				require.Equal(t, model.BillingSettlementReview, item.State)
				require.Contains(t, item.Issues, "settlement_requires_review")
				require.Equal(t, "insufficient_balance", item.FailureCode)
				require.EqualValues(t, 1200, item.Amounts["actual_quota"])
				require.EqualValues(t, 100, item.Amounts["source_held"])
				require.EqualValues(t, 1100, item.Amounts["settlement_delta"])
				fmt.Println("production-settlement-restart-readonly-ok")
				return nil
			}
		}
		return fmt.Errorf("restart report missed frozen review owner %s", strings.TrimSpace(request))
	}))
}
