package model

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var errFinancialReviewWriteAttempt = errors.New("financial report attempted a non-SELECT statement")

type financialReviewSelectOnlyPool struct {
	gorm.ConnPool
	mu         sync.Mutex
	statements []string
	begins     int
	violations int
}

func (p *financialReviewSelectOnlyPool) permit(query string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statements = append(p.statements, query)
	upper := strings.ToUpper(strings.TrimSpace(query))
	ok := strings.HasPrefix(upper, "SELECT ") && !strings.Contains(upper, "FOR UPDATE") && !strings.Contains(upper, "LOCK IN SHARE MODE")
	if !ok {
		p.violations++
	}
	return ok
}

func (p *financialReviewSelectOnlyPool) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	if !p.permit(query) {
		return nil, errFinancialReviewWriteAttempt
	}
	return p.ConnPool.PrepareContext(ctx, query)
}

func (p *financialReviewSelectOnlyPool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if !p.permit(query) {
		return nil, errFinancialReviewWriteAttempt
	}
	return p.ConnPool.ExecContext(ctx, query, args...)
}

func (p *financialReviewSelectOnlyPool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if !p.permit(query) {
		return nil, errFinancialReviewWriteAttempt
	}
	return p.ConnPool.QueryContext(ctx, query, args...)
}

func (p *financialReviewSelectOnlyPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if !p.permit(query) {
		// Return a failing SELECT instead of executing the forbidden statement.
		return p.ConnPool.QueryRowContext(ctx, "SELECT FROM")
	}
	return p.ConnPool.QueryRowContext(ctx, query, args...)
}

func (p *financialReviewSelectOnlyPool) BeginTx(context.Context, *sql.TxOptions) (gorm.ConnPool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.begins++
	return nil, errFinancialReviewWriteAttempt
}

func financialReviewReadOnlyHandle(t *testing.T, db *gorm.DB) *gorm.DB {
	t.Helper()
	reader := db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *reader.Config
	reader.Config = &config
	pool := &financialReviewSelectOnlyPool{ConnPool: db.ConnPool}
	reader.Config.ConnPool, reader.Statement.ConnPool = pool, pool
	t.Cleanup(func() {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		assert.Zero(t, pool.violations, "the report must not issue writes, migration/SHOW/PRAGMA statements, or row locks")
		assert.Zero(t, pool.begins, "the report must not start even an implicit transaction")
		assert.NotEmpty(t, pool.statements, "the report must exercise the real explicit database")
	})
	reader = lockForUpdate(reader.Where("1 = 0"))
	previousDB, previousKind := DB, common.MainDatabaseType()
	DB = nil
	if previousKind == common.DatabaseTypeSQLite {
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	} else {
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	}
	t.Cleanup(func() { DB = previousDB; common.SetMainDatabaseType(previousKind) })
	// Existing clauses on a supplied handle must not leak a lock or a filter
	// into the schema probe or the independent page scan.
	return reader
}

func financialReviewFixtureTable(t *testing.T, db *gorm.DB, model any) string {
	t.Helper()
	stmt := &gorm.Statement{DB: db}
	require.NoError(t, stmt.Parse(model))
	return stmt.Table
}

func TestFinancialReviewReportDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		kind common.DatabaseType
		env  string
	}{{common.DatabaseTypeSQLite, ""}, {common.DatabaseTypeMySQL, "TEST_MYSQL_DSN"}, {common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN"}} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			t.Run("pending_identity_redaction_and_no_writes", func(t *testing.T) {
				db := subscriptionPeriodDatabase(t, dialect.kind, dsn)
				require.NoError(t, db.AutoMigrate(&BillingFinalization{}, &SubscriptionPreConsumeRecord{}, &Task{}, &Midjourney{}))
				t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&BillingFinalization{}, &Task{}, &Midjourney{})) })
				for i, state := range []string{BillingSettled, BillingRefundPending, BillingHeld, BillingDeliveryUnknown, BillingRefundReview, BillingRefundReady} {
					receipt := BillingFinalization{RequestID: []string{"a-settled", "b-legacy-pending", "c-held", "d-delivery-unknown", "e-review", "f-ready"}[i], UserID: 73, Source: "wallet", State: state, SourceHeld: 100, PreConsumed: 100, Paid: 100}
					require.NoError(t, db.Create(&receipt).Error)
				}
				require.NoError(t, db.Create(&BillingFinalization{RequestID: "g-sub-period-unknown", UserID: 73, Source: "subscription", State: BillingHeld, SubscriptionID: 1, SourceHeld: 100, PreConsumed: 100}).Error)
				require.NoError(t, db.Create(&SubscriptionPreConsumeRecord{RequestId: "sub-period-unknown", UserId: 73, UserSubscriptionId: 1, PreConsumed: 100, Status: "consumed"}).Error)
				require.NoError(t, db.Create(&SubscriptionPreConsumeRecord{RequestId: "sub-current", UserId: 73, UserSubscriptionId: 1, PreConsumed: 100, QuotaPeriod: 2, ReservedAmount: 100, Status: "consumed"}).Error)

				const secret = "SYNTHETIC-PRIVATE-MATERIAL-NOT-FOR-REPORT"
				fingerprint := strings.Repeat("a", 64)
				legacyTask := Task{UserId: 73, Quota: 100, Status: TaskStatusFailure, SettlementPending: true, PrivateData: TaskPrivateData{RequestId: "task-legacy", BillingSource: "wallet", Key: secret, TokenId: 9, TokenFingerprint: fingerprint, WalletDebit: []LedgerShare{{Account: AccountPaid, Amount: 100}}, PendingSettlement: &TaskSettlement{Phase: "refund", Quota: 0}, PluginState: common.RawMessage(`{"provider_payload":"` + secret + `"}`)}}
				require.NoError(t, db.Create(&legacyTask).Error)
				currentIdentity := TaskPrivateData{RequestId: "task-current", BillingSource: "wallet", FinancialVersion: 2, SubmissionAccounted: true, WalletDebit: []LedgerShare{{Account: AccountPaid, Amount: 100}}}
				require.NoError(t, db.Create(&Task{UserId: 73, Quota: 100, Status: TaskStatusSuccess, PrivateData: currentIdentity}).Error)
				currentIdentity.RequestId = "task-v2-pending"
				currentIdentity.FinancialIntent = &TaskFinancialIntent{Kind: "settle", PreviousQuota: 100, ActualQuota: 80, OperationID: "task-v2-pending-operation"}
				currentIdentity.FinancialReviewReason = secret
				require.NoError(t, db.Create(&Task{UserId: 73, Quota: 100, Status: TaskStatusSuccess, SettlementPending: true, PrivateData: currentIdentity}).Error)
				require.NoError(t, db.Create(&Task{UserId: 73, Quota: 100, Status: TaskStatusFailure, PrivateData: TaskPrivateData{RequestId: "task-old-period", BillingSource: "subscription", FinancialVersion: 2, SubscriptionId: 1, SubscriptionReservedAmount: 100}}).Error)
				require.NoError(t, db.Create(&Midjourney{UserId: 73, Quota: 100, Status: "FAILURE", Prompt: secret, Properties: secret}).Error)
				require.NoError(t, db.Create(&Midjourney{UserId: 73, Quota: 100, Status: "SUCCESS", BillingRequestID: "mj-current", BillingOriginalQuota: 100, TokenFingerprint: BillingTokenFingerprint("no-token-v2"), BillingAccounted: true, WalletDebit: []LedgerShare{{Account: AccountPaid, Amount: 100}}}).Error)

				reader := financialReviewReadOnlyHandle(t, db)
				found := map[FinancialReviewEntity][]FinancialReviewItem{}
				for _, entity := range []FinancialReviewEntity{FinancialReviewBilling, FinancialReviewSubscription, FinancialReviewTask, FinancialReviewMidjourney} {
					offset := 0
					for {
						page, err := ReadFinancialReviewReport(context.Background(), reader, FinancialReviewReportOptions{Entity: entity, Offset: offset, Limit: 1})
						require.NoError(t, err)
						require.Equal(t, "available", page.Schema.Status)
						require.LessOrEqual(t, page.Scanned, 1)
						require.Equal(t, offset+page.Scanned, page.NextOffset)
						if entity == FinancialReviewBilling && offset == 0 {
							assert.Empty(t, page.Items, "a healthy scanned row is not a finding")
							assert.True(t, page.HasMore, "pagination must advance even across empty finding pages")
						}
						encoded, err := common.Marshal(page)
						require.NoError(t, err)
						for _, forbidden := range []string{secret, fingerprint, "token_fingerprint", "private_data", "provider_payload", "prompt", "properties", "dsn"} {
							assert.NotContains(t, string(encoded), forbidden)
						}
						found[entity] = append(found[entity], page.Items...)
						if !page.HasMore {
							break
						}
						require.Greater(t, page.NextOffset, offset)
						offset = page.NextOffset
					}
				}
				require.Len(t, found[FinancialReviewBilling], 6)
				assert.Contains(t, found[FinancialReviewBilling][0].Issues, "legacy_refund_pending")
				assert.Contains(t, found[FinancialReviewBilling][1].Issues, "held_not_refund_authorization")
				assert.Contains(t, found[FinancialReviewBilling][2].Issues, "delivery_evidence_unknown")
				assert.Contains(t, found[FinancialReviewBilling][3].Issues, "refund_requires_review")
				assert.Contains(t, found[FinancialReviewBilling][4].Issues, "versioned_refund_pending")
				assert.Contains(t, found[FinancialReviewBilling][5].Issues, "subscription_period_unknown")
				require.Len(t, found[FinancialReviewSubscription], 1)
				assert.Contains(t, found[FinancialReviewSubscription][0].Issues, "subscription_period_unknown")
				assert.Contains(t, found[FinancialReviewSubscription][0].Issues, "subscription_reservation_evidence_incomplete")
				require.Len(t, found[FinancialReviewTask], 3)
				assert.Contains(t, found[FinancialReviewTask][0].Issues, "legacy_task_settlement_pending")
				assert.Contains(t, found[FinancialReviewTask][1].Issues, "versioned_task_settlement_pending")
				assert.NotContains(t, found[FinancialReviewTask][1].Issues, "legacy_task_settlement_pending")
				assert.Contains(t, found[FinancialReviewTask][1].Issues, "task_financial_review_required")
				assert.Contains(t, found[FinancialReviewTask][2].Issues, "subscription_period_unknown")
				require.Len(t, found[FinancialReviewMidjourney], 1)
				assert.Contains(t, found[FinancialReviewMidjourney][0].Issues, "midjourney_billing_identity_incomplete")

				var preservedTask Task
				require.NoError(t, db.First(&preservedTask, legacyTask.ID).Error)
				assert.True(t, preservedTask.SettlementPending)
				require.NotNil(t, preservedTask.PrivateData.PendingSettlement)
				assert.Equal(t, 100, preservedTask.Quota)
				var preservedReceipt SubscriptionPreConsumeRecord
				require.NoError(t, db.Where("request_id = ?", "sub-period-unknown").First(&preservedReceipt).Error)
				assert.Zero(t, preservedReceipt.QuotaPeriod)
				assert.Zero(t, preservedReceipt.ReservedAmount)
				var pending BillingFinalization
				require.NoError(t, db.Where("request_id = ?", "b-legacy-pending").First(&pending).Error)
				assert.Equal(t, BillingRefundPending, pending.State)

				for _, opts := range []FinancialReviewReportOptions{{Entity: FinancialReviewTask}, {Entity: FinancialReviewTask, Limit: FinancialReviewMaxPageSize + 1}, {Entity: FinancialReviewTask, Limit: 1, Offset: -1}, {Entity: "not-a-report-entity", Limit: 1}} {
					_, err := ReadFinancialReviewReport(context.Background(), reader, opts)
					assert.ErrorIs(t, err, ErrFinancialReviewOptions)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := ReadFinancialReviewReport(ctx, reader, FinancialReviewReportOptions{Entity: FinancialReviewTask, Limit: 1})
				assert.ErrorIs(t, err, context.Canceled)
				_, err = ReadFinancialReviewReport(context.Background(), nil, FinancialReviewReportOptions{Entity: FinancialReviewTask, Limit: 1})
				assert.ErrorIs(t, err, ErrFinancialReviewOptions)
				broken := reader.Session(&gorm.Session{NewDB: true})
				broken.Error = errors.New("synthetic DSN password=" + secret)
				_, err = ReadFinancialReviewReport(context.Background(), broken, FinancialReviewReportOptions{Entity: FinancialReviewTask, Limit: 1})
				require.ErrorIs(t, err, ErrFinancialReviewQuery)
				assert.NotContains(t, err.Error(), secret)
			})

			t.Run("undeployed_and_partial_schemas_are_not_clean_history", func(t *testing.T) {
				db := subscriptionPeriodDatabase(t, dialect.kind, dsn)
				reader := financialReviewReadOnlyHandle(t, db)
				for _, entity := range []FinancialReviewEntity{FinancialReviewBilling, FinancialReviewSubscription, FinancialReviewTask, FinancialReviewMidjourney} {
					page, err := ReadFinancialReviewReport(context.Background(), reader, FinancialReviewReportOptions{Entity: entity, Limit: 10})
					require.NoError(t, err)
					assert.Equal(t, "missing_or_not_visible", page.Schema.Status)
					assert.NotEmpty(t, page.Schema.MissingColumns)
					assert.Zero(t, page.Scanned)
					assert.Empty(t, page.Items)
				}
				taskTable := financialReviewFixtureTable(t, db, &Task{})
				billingTable := financialReviewFixtureTable(t, db, &BillingFinalization{})
				t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(taskTable, billingTable)) })
				type oldTask struct {
					ID          int64
					UserID      int
					Quota       int
					Status      string
					PrivateData string `gorm:"type:text"`
				}
				require.NoError(t, db.Table(taskTable).AutoMigrate(&oldTask{}))
				require.NoError(t, db.Table(taskTable).Create(&oldTask{UserID: 73, Quota: 100, Status: "FAILURE", PrivateData: `{"request_id":"old-task","pending_settlement":{"phase":"refund"}}`}).Error)
				require.NoError(t, db.Table(taskTable).Create(&oldTask{UserID: 73, Quota: 100, Status: "FAILURE", PrivateData: `malformed-SYNTHETIC-PROVIDER-PAYLOAD`}).Error)
				page, err := ReadFinancialReviewReport(context.Background(), reader, FinancialReviewReportOptions{Entity: FinancialReviewTask, Limit: 10})
				require.NoError(t, err)
				assert.Equal(t, "partial", page.Schema.Status)
				assert.Contains(t, page.Schema.MissingColumns, "settlement_pending")
				require.Len(t, page.Items, 2)
				assert.Contains(t, page.Items[0].Issues, "legacy_task_settlement_pending")
				assert.Contains(t, page.Items[1].Issues, "task_private_identity_invalid")
				encoded, err := common.Marshal(page)
				require.NoError(t, err)
				assert.NotContains(t, string(encoded), "SYNTHETIC-PROVIDER-PAYLOAD")
				assert.False(t, db.Migrator().HasColumn(&Task{}, "settlement_pending"), "inspection must never migrate a legacy table")
				type noReceiptKey struct{ State string }
				require.NoError(t, db.Table(billingTable).AutoMigrate(&noReceiptKey{}))
				page, err = ReadFinancialReviewReport(context.Background(), reader, FinancialReviewReportOptions{Entity: FinancialReviewBilling, Limit: 10})
				require.NoError(t, err)
				assert.Equal(t, "partial_unscannable", page.Schema.Status)
				assert.Contains(t, page.Schema.MissingColumns, "request_id")
				assert.Zero(t, page.Scanned)
				assert.False(t, db.Migrator().HasTable(&Midjourney{}), "inspection must never create an undeployed table")
			})
		})
	}
}
