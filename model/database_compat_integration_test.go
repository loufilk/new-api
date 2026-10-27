package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// installDatabaseCompatRegistry mirrors the root-model-owned schema/data
// descriptors installed by chuanapi.Register at the production composition
// root. This test lives in the model package, so importing concrete ChuanAPI
// modules here would create an import cycle. Provider-owned schema (including
// PaymentOrderBinding) is exercised by its module-level real-database
// compatibility test instead of being duplicated here.
func installDatabaseCompatRegistry(t *testing.T) {
	t.Helper()
	reg := extension.NewRegistry()
	require.NoError(t, reg.RegisterModelSet(extension.ModelSet{
		Extension: "chuanapi",
		Name:      "wallet_models",
		Phase:     extension.PhaseAfterCoreModels,
		Models: []any{
			&BalanceLedgerOperation{},
			&BalanceLedger{},
			&ModelBonusEligibleAudit{},
			&ReferralCommission{},
		},
	}))
	require.NoError(t, reg.RegisterModelSet(extension.ModelSet{
		Extension: "chuanapi",
		Name:      "support_models",
		Phase:     extension.PhaseAfterCoreModels,
		Models:    []any{&SupportTicket{}, &SupportTicketMessage{}},
	}))
	require.NoError(t, reg.RegisterDataStep(extension.DataStep{
		Extension: "chuanapi",
		Name:      "legacy_quota_backfill",
		Phase:     extension.PhaseAfterCoreData,
		Run: func(db *gorm.DB) error {
			_, err := BackfillLegacyQuotaAccountsWithDB(db)
			return err
		},
	}))
	require.NoError(t, extension.SetDefault(reg))
}

// TestDatabaseCompatibilityReal exercises the same InitDB/migrateDB path used by
// production against a real database selected by TEST_DATABASE_DIALECT.
// It is opt-in so the normal unit-test suite stays self-contained.
func TestDatabaseCompatibilityReal(t *testing.T) {
	if os.Getenv("TEST_DATABASE_COMPAT") != "1" {
		t.Skip("set TEST_DATABASE_COMPAT=1 to run the real database compatibility test")
	}

	// Production installs the ChuanAPI extension registry before InitDB. The
	// compatibility test must do the same or wallet tables are intentionally not
	// part of mainAutoMigrateModels and the test becomes a false negative.
	installDatabaseCompatRegistry(t)

	// InitDB only runs migrations on the master node. Unit tests do not run the
	// application's bootstrap that normally sets this flag, so make the intended
	// production role explicit for this integration test.
	oldIsMasterNode := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = oldIsMasterNode })

	dialect := strings.ToLower(strings.TrimSpace(os.Getenv("TEST_DATABASE_DIALECT")))
	switch dialect {
	case "sqlite":
		oldSQLitePath := common.SQLitePath
		common.SQLitePath = filepath.Join(t.TempDir(), "compat.db") + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate"
		t.Cleanup(func() { common.SQLitePath = oldSQLitePath })
		t.Setenv("SQL_DSN", "local")
	case "mysql", "postgres":
		dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
		require.NotEmpty(t, dsn, "TEST_DATABASE_DSN is required for %s", dialect)
		t.Setenv("SQL_DSN", dsn)
	default:
		t.Fatalf("unsupported TEST_DATABASE_DIALECT %q", dialect)
	}
	logDSN := strings.TrimSpace(os.Getenv("TEST_LOG_DATABASE_DSN"))
	t.Setenv("LOG_SQL_DSN", logDSN)

	require.NoError(t, InitDB(), "production InitDB/migrateDB path must succeed on %s", dialect)
	require.NoError(t, InitLogDB(), "production InitLogDB path must succeed on %s", dialect)
	firstMainDB := DB
	firstLogDB := LOG_DB
	firstMainSQLDB, err := firstMainDB.DB()
	require.NoError(t, err)
	require.NoError(t, firstMainSQLDB.Close())
	if firstLogDB != firstMainDB {
		firstLogSQLDB, logErr := firstLogDB.DB()
		require.NoError(t, logErr)
		require.NoError(t, firstLogSQLDB.Close())
	}
	require.NoError(t, InitDB(), "production InitDB/migrateDB path must be idempotent on %s", dialect)
	require.NoError(t, InitLogDB(), "production InitLogDB path must be idempotent on %s", dialect)
	require.NotNil(t, DB)
	require.NotNil(t, LOG_DB)

	sqlDB, err := DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if LOG_DB != DB {
		logSQLDB, logErr := LOG_DB.DB()
		require.NoError(t, logErr)
		t.Cleanup(func() { require.NoError(t, logSQLDB.Close()) })
		require.NoError(t, logSQLDB.Ping())
	}
	if logDSN != "" {
		require.NotSame(t, DB, LOG_DB, "configured log databases must be initialized independently")
		require.True(t, LOG_DB.Migrator().HasTable(&Log{}))
		require.True(t, LOG_DB.Migrator().HasTable(&AuditLog{}))
	}
	require.NoError(t, sqlDB.Ping())

	var version string
	switch dialect {
	case "sqlite":
		require.NoError(t, DB.Raw("SELECT sqlite_version()").Scan(&version).Error)
	case "mysql":
		require.NoError(t, DB.Raw("SELECT VERSION()").Scan(&version).Error)
	case "postgres":
		require.NoError(t, DB.Raw("SHOW server_version").Scan(&version).Error)
	}
	t.Logf("validated %s server version: %s", dialect, version)

	for name, table := range map[string]any{
		"users":              &User{},
		"tokens":             &Token{},
		"options":            &Option{},
		"quota_data":         &QuotaData{},
		"balance_ledger":     &BalanceLedger{},
		"subscription_plans": &SubscriptionPlan{},
		"support_tickets":    &SupportTicket{},
		"support_messages":   &SupportTicketMessage{},
	} {
		require.Truef(t, DB.Migrator().HasTable(table), "migrateDB did not create %s on %s", name, dialect)
	}

	// Values above int32 prove that the wallet mirror and paid account survive
	// actual round-trips on the target database rather than only looking correct
	// in GORM metadata. Keep the fixture identifier within User.Username's
	// max=20 validation contract and users.aff_code's varchar(32) schema limit.
	const largeQuota = 3_000_000_000
	unique := fmt.Sprintf("dc%s%x", dialect[:1], uint64(time.Now().UnixNano()))
	user := User{
		Username:    unique,
		Password:    "compat-test-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		Quota:       largeQuota,
		QuotaPaid:   largeQuota,
		AuthVersion: 1,
		AffCode:     unique,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NotZero(t, user.Id)

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.Equal(t, largeQuota, stored.Quota)
	require.Equal(t, largeQuota, stored.QuotaPaid)
	require.Equal(t, stored.QuotaPaid+stored.QuotaBonus+stored.QuotaReward, stored.Quota)

	// Exercise the quota_data write/update/group-by path, which is sensitive to
	// SQL dialect differences and is used by the dashboard/export flow.
	bucket := time.Now().Unix()
	bucket -= bucket % 3600
	first := QuotaData{
		UserID: user.Id, Username: unique, ModelName: "compat-model",
		CreatedAt: bucket, UseGroup: "default", TokenID: 101, ChannelID: 202,
		NodeName: "compat-node", TokenUsed: 11, Count: 1, Quota: 123,
	}
	require.NoError(t, DB.Create(&first).Error)

	increaseQuotaData(&QuotaData{
		UserID: user.Id, Username: unique, ModelName: "compat-model",
		CreatedAt: bucket, UseGroup: "default", TokenID: 101, ChannelID: 202,
		NodeName: "compat-node", TokenUsed: 7, Count: 2, Quota: 77,
	})

	rows, err := GetQuotaDataByUserId(user.Id, bucket, bucket)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 3, rows[0].Count)
	require.Equal(t, 200, rows[0].Quota)
	require.Equal(t, 18, rows[0].TokenUsed)

	// A real transactional update catches lock/transaction syntax differences
	// between SQLite, MySQL 5.7 and PostgreSQL 9.6.
	require.NoError(t, DB.Transaction(func(txDB *gorm.DB) error {
		return txDB.Model(&User{}).Where("id = ?", user.Id).Update("quota_paid", largeQuota+1).Error
	}))
	var updatedPaid int
	require.NoError(t, DB.Model(&User{}).Select("quota_paid").Where("id = ?", user.Id).Scan(&updatedPaid).Error)
	require.Equal(t, largeQuota+1, updatedPaid)

	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 5
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	paymentUsername := fmt.Sprintf("e%x", uint64(time.Now().UnixNano()))
	paymentUser := User{
		Username:    paymentUsername,
		Password:    "compat-payment-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		AffCode:     paymentUsername,
	}
	require.NoError(t, DB.Create(&paymentUser).Error)

	legacyTradeNo := fmt.Sprintf("DBLEGACY%x", uint64(time.Now().UnixNano()))
	legacyOrder := TopUp{
		UserId:          paymentUser.Id,
		Amount:          2,
		Money:           10,
		TradeNo:         legacyTradeNo,
		PaymentMethod:   "",
		PaymentProvider: PaymentProviderEpay,
		Status:          common.TopUpStatusPending,
		CreateTime:      common.GetTimestamp(),
	}
	require.NoError(t, legacyOrder.Insert())

	alreadyDone, err := RechargeEpay(legacyTradeNo, "wxpay", "10.00", "127.0.0.1")
	require.NoError(t, err)
	require.False(t, alreadyDone)

	var storedLegacy TopUp
	require.NoError(t, DB.Where("trade_no = ?", legacyTradeNo).First(&storedLegacy).Error)
	require.Equal(t, common.TopUpStatusSuccess, storedLegacy.Status)
	require.Equal(t, "wxpay", storedLegacy.PaymentMethod)

	var paymentQuota int
	require.NoError(t, DB.Model(&User{}).Select("quota").Where("id = ?", paymentUser.Id).Scan(&paymentQuota).Error)
	require.Equal(t, 10, paymentQuota)

	alreadyDone, err = RechargeEpay(legacyTradeNo, "wxpay", "10.00", "127.0.0.1")
	require.NoError(t, err)
	require.True(t, alreadyDone)
	require.NoError(t, DB.Model(&User{}).Select("quota").Where("id = ?", paymentUser.Id).Scan(&paymentQuota).Error)
	require.Equal(t, 10, paymentQuota)

	mismatchTradeNo := fmt.Sprintf("DBMISMATCH%x", uint64(time.Now().UnixNano()))
	mismatchOrder := TopUp{
		UserId:          paymentUser.Id,
		Amount:          2,
		Money:           10,
		TradeNo:         mismatchTradeNo,
		PaymentMethod:   "alipay",
		PaymentProvider: PaymentProviderEpay,
		Status:          common.TopUpStatusPending,
		CreateTime:      common.GetTimestamp(),
	}
	require.NoError(t, mismatchOrder.Insert())
	_, err = RechargeEpay(mismatchTradeNo, "wxpay", "10.00", "127.0.0.1")
	require.ErrorIs(t, err, ErrPaymentMethodMismatch)
	require.NoError(t, DB.Where("trade_no = ?", mismatchTradeNo).First(&mismatchOrder).Error)
	require.Equal(t, common.TopUpStatusPending, mismatchOrder.Status)
	require.Equal(t, "alipay", mismatchOrder.PaymentMethod)

	concurrentUsername := fmt.Sprintf("c%x", uint64(time.Now().UnixNano()))
	concurrentUser := User{
		Username:    concurrentUsername,
		Password:    "compat-concurrent-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		AffCode:     concurrentUsername,
	}
	require.NoError(t, DB.Create(&concurrentUser).Error)
	concurrentTradeNo := fmt.Sprintf("DBCONCURRENT%x", uint64(time.Now().UnixNano()))
	concurrentOrder := TopUp{
		UserId:          concurrentUser.Id,
		Amount:          2,
		Money:           10,
		TradeNo:         concurrentTradeNo,
		PaymentMethod:   "wxpay",
		PaymentProvider: PaymentProviderEpay,
		Status:          common.TopUpStatusPending,
		CreateTime:      common.GetTimestamp(),
	}
	require.NoError(t, concurrentOrder.Insert())

	type callbackResult struct {
		alreadyDone bool
		err         error
	}
	start := make(chan struct{})
	results := make(chan callbackResult, 2)
	var waitGroup sync.WaitGroup
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			done, callbackErr := RechargeEpay(concurrentTradeNo, "wxpay", "10.00", "127.0.0.1")
			results <- callbackResult{alreadyDone: done, err: callbackErr}
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	completedCallbacks := 0
	idempotentCallbacks := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.alreadyDone {
			idempotentCallbacks++
		} else {
			completedCallbacks++
		}
	}
	require.Equal(t, 1, completedCallbacks)
	require.Equal(t, 1, idempotentCallbacks)

	var concurrentQuota int
	require.NoError(t, DB.Model(&User{}).Select("quota").Where("id = ?", concurrentUser.Id).Scan(&concurrentQuota).Error)
	require.Equal(t, 10, concurrentQuota)
}
