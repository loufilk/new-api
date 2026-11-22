package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Inject at the real PostgreSQL driver boundary, after all statements have
// executed. Returning an error is deliberately not evidence of rollback.
type nativeTaskCommitFaultPool struct {
	*sql.DB
	at       int32
	rollback bool
	commits  atomic.Int32
	fired    atomic.Bool
}

type nativeTaskCommitFaultTx struct {
	*sql.Tx
	pool *nativeTaskCommitFaultPool
}

func (p *nativeTaskCommitFaultPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &nativeTaskCommitFaultTx{Tx: tx, pool: p}, nil
}

func (tx *nativeTaskCommitFaultTx) Commit() error {
	fault := tx.pool.commits.Add(1) == tx.pool.at
	if fault && tx.pool.rollback {
		if err := tx.Tx.Rollback(); err != nil {
			return err
		}
	} else if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if fault {
		tx.pool.fired.Store(true)
		return errors.New("synthetic native task COMMIT acknowledgement lost")
	}
	return nil
}

func installNativeTaskCommitFault(t *testing.T, db *gorm.DB, at int32, rollback bool) *nativeTaskCommitFaultPool {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	pool := &nativeTaskCommitFaultPool{DB: sqlDB, at: at, rollback: rollback}
	wrapped := db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
	model.DB = wrapped
	// LOG_DB must expose the same original SQL pool, not the wrapper identity.
	// GORM unwraps the embedded DB through GetDBConn so shared-DB log writes join.
	return pool
}

func (p *nativeTaskCommitFaultPool) GetDBConn() (*sql.DB, error) { return p.DB, nil }

// A free task still owns exactly one request statistic and (when enabled) one
// consume log; logging cannot import a previous request's funded reservation.
func TestTaskSubmissionAtomicFreeConsumeLog(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" || os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires disposable PostgreSQL TEST_POSTGRES_DSN")
	}
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("logs_%t", enabled), func(t *testing.T) {
			f := newTaskSubmissionFinancialFixture(t, fmt.Sprintf("native-free-original-%t", enabled), 1000, 100)
			f.info.Billing.Refund(f.c)
			waitTaskSubmissionFixtureQuiescence(t)
			f.info.Billing = nil
			f.info.RequestId = fmt.Sprintf("native-free-completed-%t", enabled)
			f.c.Set(common.RequestIdKey, f.info.RequestId)
			f.info.PriceData.Quota = 0
			common.LogConsumeEnabled = enabled
			task := model.InitTask("task_plugin", f.info)
			task.PrivateData.Execution = service.TaskExecutionSnapshotFromContext(f.c)
			task.Status, task.Quota = model.TaskStatusSuccess, 0
			require.NoError(t, service.PersistTaskBillingAndLog(f.c, f.info, task))
			state := captureTaskSubmissionFinancialState(t, f.db, f.info)
			assert.Equal(t, 1000, state.User.Quota)
			assert.Equal(t, 1000, state.Token.RemainQuota)
			assert.Zero(t, state.User.UsedQuota)
			assert.Equal(t, 1, state.User.RequestCount)
			require.Len(t, state.Tasks, 1)
			wantLogs := 0
			if enabled {
				wantLogs = 1
			}
			require.Len(t, state.Logs, wantLogs)
			if enabled {
				assert.Equal(t, f.info.RequestId, state.Logs[0].RequestId)
			}
			require.NoError(t, service.PersistTaskBillingAndLog(f.c, f.info, task))
			assert.Equal(t, state, captureTaskSubmissionFinancialState(t, f.db, f.info))
			task.Quota = -1
			require.Error(t, service.PersistTaskBillingAndLog(f.c, f.info, task))
			assert.Equal(t, state, captureTaskSubmissionFinancialState(t, f.db, f.info))
		})
	}
}

func TestDeliveredTaskSettlementThreeWalletConservation(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" || os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires disposable PostgreSQL TEST_POSTGRES_DSN")
	}
	if err := walletmodule.RegisterRuntime(); err != nil {
		require.EqualError(t, err, "walletport: policy already configured")
	}
	for _, failLog := range []bool{false, true} {
		t.Run(fmt.Sprintf("log_rollback_%t", failLog), func(t *testing.T) {
			f := newTaskSubmissionFinancialFixture(t, "native-three-wallet-setup", 1000, 100)
			require.NoError(t, f.db.Model(&model.Model{}).Where("model_name = ?", f.info.OriginModelName).Update("bonus_eligible", true).Error)
			model.InvalidateModelBonusEligibleCache()
			user := model.User{Username: "three-wallet-native", AffCode: "three-wallet-native", Group: "default", Quota: 9000, QuotaPaid: 4000, QuotaBonus: 3000, QuotaReward: 2000}
			require.NoError(t, f.db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: "three-wallet-original-token", Name: "three-wallet-token", Status: common.TokenStatusEnabled, RemainQuota: 9000}
			require.NoError(t, f.db.Create(&token).Error)
			info := *f.info
			info.UserId, info.UserQuota = user.Id, user.Quota
			info.TokenId, info.TokenKey = token.Id, token.Key
			info.RequestId = "native-three-wallet-completed"
			info.Billing = nil
			info.PriceData.Quota = 4500
			c := taskSubmissionTestContext()
			c.Set(common.RequestIdKey, info.RequestId)
			c.Set("token_name", token.Name)
			session, apiErr := service.NewBillingSession(c, &info, 6000)
			require.Nil(t, apiErr)
			info.Billing = session
			held := captureTaskSubmissionFinancialState(t, f.db, &info)
			require.Len(t, held.Owners, 1)
			require.Equal(t, 3000, held.Owners[0].Bonus)
			require.Equal(t, 2000, held.Owners[0].Reward)
			require.Equal(t, 1000, held.Owners[0].Paid)
			task := model.InitTask("task_plugin", &info)
			task.PrivateData.Execution = service.TaskExecutionSnapshotFromContext(c)
			task.PrivateData.BillingSource = service.BillingSourceWallet
			task.PrivateData.TokenId = token.Id
			task.Status, task.Quota = model.TaskStatusSuccess, 4500
			hits := installTaskPostInsertFailure(t, f.db, user.Id, task.TaskID)
			require.Error(t, service.PersistTaskBillingAndLog(c, &info, task))
			require.EqualValues(t, 1, hits.Load())
			if failLog {
				name := "test:three_wallet_consume_rollback"
				require.NoError(t, f.db.Callback().Create().After("gorm:create").Before("gorm:after_create").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Log" && tx.Error == nil {
						tx.AddError(errors.New("synthetic three-wallet main-log failure"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, f.db.Callback().Create().Remove(name)) })
			}
			_, err := c.Writer.WriteString("data: completed\n\n")
			require.NoError(t, err)
			err = service.SettleDeliveredTaskBilling(c, &info, task)
			state := captureTaskSubmissionFinancialState(t, f.db, &info)
			assert.Empty(t, state.Tasks)
			require.Len(t, state.Owners, 1)
			assert.Equal(t, 4500, state.Owners[0].ActualQuota)
			if failLog {
				require.Error(t, err)
				assert.Equal(t, model.BillingSettlementReview, state.Owners[0].State)
				assert.Equal(t, held.User, state.User)
				assert.Equal(t, held.Token, state.Token)
				assert.Equal(t, held.Ledger, state.Ledger)
				assert.Equal(t, held.Operations, state.Operations)
				assert.Empty(t, state.Logs)
			} else {
				require.NoError(t, err)
				assert.Equal(t, model.BillingSettled, state.Owners[0].State)
				// Existing downward settlement refunds the original shares proportionally.
				assert.Equal(t, 4500, state.User.Quota)
				assert.Equal(t, 3250, state.User.QuotaPaid)
				assert.Equal(t, 500, state.User.QuotaReward)
				assert.Equal(t, 750, state.User.QuotaBonus)
				assert.Equal(t, 4500, state.Token.RemainQuota)
				assert.Equal(t, 4500, state.Token.UsedQuota)
				assert.Equal(t, 4500, state.User.UsedQuota)
				assert.Equal(t, 1, state.User.RequestCount)
				assert.EqualValues(t, 4500, state.Channel.UsedQuota)
				require.Len(t, state.Logs, 1)
				assert.Contains(t, state.Logs[0].Other, `"wallet_cost":{"bonus":2250,"paid":750,"reward":1500}`)
				require.NoError(t, service.SettleDeliveredTaskBilling(c, &info, task))
			}
			assert.False(t, info.Billing.NeedsRefund())
			info.Billing.Refund(c)
			waitTaskSubmissionFixtureQuiescence(t)
			assert.Equal(t, state, captureTaskSubmissionFinancialState(t, f.db, &info))
		})
	}
}
