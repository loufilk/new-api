package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errPendingStatusStorage = errors.New("synthetic pending status storage failure")

type pendingStatusRejectCommitPool struct{ gorm.ConnPool }
type pendingStatusRejectCommitTx struct{ *sql.Tx }

func (p *pendingStatusRejectCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &pendingStatusRejectCommitTx{Tx: tx}, nil
}
func (tx *pendingStatusRejectCommitTx) Commit() error {
	if err := tx.Tx.Rollback(); err != nil {
		return err
	}
	return errPendingStatusStorage
}

// These fixtures exercise the status-only persistence contract on real engines.
// Full signed callback/ledger and independent-process Stripe regressions live in
// controller/payment_stale_failure_test.go, not in these state-only SQL tests.
func TestPaymentPendingStatusDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		kind common.DatabaseType
		env  string
	}{{common.DatabaseTypeSQLite, ""}, {common.DatabaseTypeMySQL, "TEST_MYSQL_DSN"}, {common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN"}} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			db := subscriptionPeriodDatabase(t, dialect.kind, dsn)
			require.NoError(t, db.AutoMigrate(&TopUp{}, &SubscriptionOrder{}))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&TopUp{}, &SubscriptionOrder{})) })
			for _, kind := range []string{PaymentProviderWaffo, PaymentProviderWaffoPancake, PaymentProviderStripe, "subscription"} {
				t.Run(kind, func(t *testing.T) {
					provider := kind
					if kind == "subscription" {
						provider = PaymentProviderWaffoPancake
					}
					sequence := 0
					seed := func(status string) (string, any) {
						sequence++
						tradeNo := fmt.Sprintf("pending-%s-%d", kind, sequence)
						if kind == "subscription" {
							order := SubscriptionOrder{UserId: 713, PlanId: 42, Money: 10, TradeNo: tradeNo, PaymentProvider: provider, PaymentMethod: "original-method", CreateTime: 100, CompleteTime: 200, Status: status, ProviderPayload: "original-payload"}
							require.NoError(t, db.Create(&order).Error)
							return tradeNo, order
						}
						order := TopUp{UserId: 713, Amount: 10, Money: 10, TradeNo: tradeNo, PaymentProvider: provider, PaymentMethod: "original-method", CreateTime: 100, CompleteTime: 200, Status: status}
						require.NoError(t, db.Create(&order).Error)
						return tradeNo, order
					}
					apply := func(tradeNo, expectedProvider string) error {
						if kind == "subscription" {
							return FailPendingSubscriptionOrder(tradeNo, expectedProvider)
						}
						return UpdatePendingTopUpStatus(tradeNo, expectedProvider, common.TopUpStatusFailed)
					}
					read := func(tradeNo string) any {
						if kind == "subscription" {
							var order SubscriptionOrder
							require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&order).Error)
							return order
						}
						var order TopUp
						require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&order).Error)
						return order
					}
					invalidStatus, notFound := ErrTopUpStatusInvalid, ErrTopUpNotFound
					if kind == "subscription" {
						invalidStatus, notFound = ErrSubscriptionOrderStatusInvalid, ErrSubscriptionOrderNotFound
					}
					t.Run("pending_changes_only_status", func(t *testing.T) {
						tradeNo, before := seed(common.TopUpStatusPending)
						require.NoError(t, apply(tradeNo, provider))
						switch expected := before.(type) {
						case TopUp:
							expected.Status = common.TopUpStatusFailed
							assert.Equal(t, expected, read(tradeNo))
						case SubscriptionOrder:
							expected.Status = common.TopUpStatusFailed
							assert.Equal(t, expected, read(tradeNo))
						}
					})
					for _, status := range []string{common.TopUpStatusSuccess, common.TopUpStatusFailed, common.TopUpStatusExpired} {
						t.Run("preserve_"+status, func(t *testing.T) {
							tradeNo, before := seed(status)
							require.ErrorIs(t, apply(tradeNo, provider), invalidStatus)
							assert.Equal(t, before, read(tradeNo))
						})
					}
					t.Run("identity_guards", func(t *testing.T) {
						tradeNo, before := seed(common.TopUpStatusPending)
						require.ErrorIs(t, apply(tradeNo, PaymentProviderCreem), ErrPaymentMethodMismatch)
						require.ErrorIs(t, apply("does-not-exist", provider), notFound)
						require.Error(t, apply("", provider))
						if kind == "subscription" {
							require.ErrorIs(t, apply(tradeNo, ""), ErrPaymentMethodMismatch)
						}
						assert.Equal(t, before, read(tradeNo))
					})
					for _, fault := range []string{"read", "write", "zero_rows", "commit_rollback", "commit_lost_ack"} {
						t.Run(fault, func(t *testing.T) {
							tradeNo, before := seed(common.TopUpStatusPending)
							hook := "pending-status-fault"
							switch fault {
							case "read":
								require.NoError(t, db.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) { tx.AddError(errPendingStatusStorage) }))
								err := apply(tradeNo, provider)
								require.NoError(t, db.Callback().Query().Remove(hook))
								require.ErrorIs(t, err, errPendingStatusStorage)
							case "write", "zero_rows":
								require.NoError(t, db.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
									if fault == "write" {
										tx.AddError(errPendingStatusStorage)
									} else {
										tx.Statement.AddClause(clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "1 = 0"}}})
									}
								}))
								err := apply(tradeNo, provider)
								require.NoError(t, db.Callback().Update().Remove(hook))
								if fault == "write" {
									require.ErrorIs(t, err, errPendingStatusStorage)
								} else {
									require.ErrorIs(t, err, invalidStatus)
								}
							case "commit_rollback":
								wrapped := db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
								config := *wrapped.Config
								wrapped.Config = &config
								pool := &pendingStatusRejectCommitPool{ConnPool: db.ConnPool}
								wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
								DB = wrapped
								err := apply(tradeNo, provider)
								DB = db
								require.ErrorIs(t, err, errPendingStatusStorage)
							case "commit_lost_ack":
								subscriptionLoseCommitAck(t, db, func() error { return apply(tradeNo, provider) })
								require.ErrorIs(t, apply(tradeNo, provider), invalidStatus)
								switch expected := before.(type) {
								case TopUp:
									expected.Status = common.TopUpStatusFailed
									before = expected
								case SubscriptionOrder:
									expected.Status = common.TopUpStatusFailed
									before = expected
								}
							}
							assert.Equal(t, before, read(tradeNo))
						})
					}
					if kind == PaymentProviderStripe {
						t.Run("existing_expiry_contract", func(t *testing.T) {
							tradeNo, original := seed(common.TopUpStatusPending)
							require.NoError(t, UpdatePendingTopUpStatus(tradeNo, provider, common.TopUpStatusExpired))
							expected := original.(TopUp)
							expected.Status = common.TopUpStatusExpired
							assert.Equal(t, expected, read(tradeNo))
						})
					}
					if dialect.kind != common.DatabaseTypeSQLite {
						t.Run("native_controlled_success_commit_before_failure_result", func(t *testing.T) {
							tradeNo, original := seed(common.TopUpStatusPending)
							var target any = &TopUp{}
							if kind == "subscription" {
								target = &SubscriptionOrder{}
							}
							tx := db.Begin()
							require.NoError(t, tx.Error)
							defer tx.Rollback()
							require.NoError(t, tx.Model(target).Where("trade_no = ?", tradeNo).Updates(map[string]any{"status": common.TopUpStatusSuccess, "complete_time": 345}).Error)
							attempted, result := make(chan struct{}, 1), make(chan error, 1)
							hook := "pending-status-native-read"
							require.NoError(t, db.Callback().Query().Before("gorm:query").Register(hook, func(query *gorm.DB) { attempted <- struct{}{} }))
							go func() { result <- apply(tradeNo, provider) }()
							joined := false
							defer func() {
								_ = tx.Rollback()
								if !joined {
									<-result
								}
								require.NoError(t, db.Callback().Query().Remove(hook))
							}()
							select {
							case <-attempted:
							case err := <-result:
								joined = true
								require.FailNow(t, "failure writer returned before native read barrier", "%v", err)
							}
							require.NoError(t, tx.Commit().Error)
							err := <-result
							joined = true
							require.NoError(t, db.Callback().Query().Remove(hook))
							require.ErrorIs(t, err, invalidStatus)
							switch expected := original.(type) {
							case TopUp:
								expected.Status, expected.CompleteTime = common.TopUpStatusSuccess, 345
								assert.Equal(t, expected, read(tradeNo))
							case SubscriptionOrder:
								expected.Status, expected.CompleteTime = common.TopUpStatusSuccess, 345
								assert.Equal(t, expected, read(tradeNo))
							}
						})
					}
				})
			}
		})
	}
}
