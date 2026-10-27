package controller

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/paymentcore"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	waffoutils "github.com/waffo-com/waffo-go/utils"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const normalizationPGTradePrefix = "WAFFO-NORM-PG-"

// openNormalizationPostgres opens the explicitly configured isolated database.
// A present DSN that cannot connect, log its version or migrate must fail the
// run: a skipped matrix must never look like PostgreSQL evidence. Without the
// environment variable the test opts out with a skip.
func openNormalizationPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_NORMALIZATION_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TEST_NORMALIZATION_POSTGRES_DSN to run the payment normalization PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err, "TEST_NORMALIZATION_POSTGRES_DSN is set but the connection failed")

	var version, serverVersion string
	require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	require.NoError(t, db.Raw("SHOW server_version").Scan(&serverVersion).Error)
	require.NotEmpty(t, version)
	require.NotEmpty(t, serverVersion)
	t.Logf("PostgreSQL server_version evidence: server_version=%s version=%s", serverVersion, version)

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.SubscriptionOrder{},
		&model.Option{},
		&model.BalanceLedgerOperation{},
		&model.BalanceLedger{},
		&model.ReferralCommission{},
		&model.Log{},
		&paymentcore.PaymentOrderBinding{},
	))

	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	// Idempotent re-runs on the isolated database: remove only this fixture's
	// rows, never other data. The ledger idempotency headers are deleted
	// through the fixture rows' operation_id before those rows, so a re-run
	// cannot replay a stale operation and silently skip the credit.
	for _, stmt := range []string{
		`DELETE FROM referral_commissions WHERE reference_id LIKE 'WAFFO-NORM-%'`,
		`DELETE FROM balance_ledger_operations WHERE id IN (SELECT operation_id FROM balance_ledger WHERE reference_type = 'topup' AND reference_id LIKE 'WAFFO-NORM-%')`,
		`DELETE FROM balance_ledger WHERE reference_id LIKE 'WAFFO-NORM-%'`,
		`DELETE FROM top_ups WHERE trade_no LIKE 'WAFFO-NORM-%'`,
		`DELETE FROM subscription_orders WHERE trade_no LIKE 'WAFFO-NORM-%'`,
		`DELETE FROM chuanapi_payment_order_bindings WHERE trade_no LIKE 'WAFFO-NORM-%'`,
		`DELETE FROM users WHERE username LIKE 'norm_pg_%'`,
	} {
		require.NoError(t, db.Exec(stmt).Error, "fixture cleanup failed: %s", stmt)
	}
	return db
}

func normalizationPGWaffoKeys(t *testing.T) (privateKeyB64, publicKeyB64 string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(pkcs8), base64.StdEncoding.EncodeToString(spki)
}

func normalizationPGUser(t *testing.T, db *gorm.DB, id int, username string, inviterId int) *model.User {
	t.Helper()
	user := &model.User{
		Id:          id,
		Username:    username,
		Password:    "normalization-test-password",
		DisplayName: username,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		AffCode:     username + "-aff",
		InviterId:   inviterId,
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

func normalizationPGWaffoPayload(tradeNo, acquiringOrderID, merchantID, amount, currency, status string) []byte {
	body, err := common.Marshal(map[string]any{
		"eventType": "PAYMENT_NOTIFICATION",
		"result": map[string]any{
			"paymentRequestId": tradeNo,
			"merchantOrderId":  tradeNo,
			"acquiringOrderId": acquiringOrderID,
			"orderStatus":      status,
			"orderAmount":      amount,
			"orderCurrency":    currency,
			"merchantInfo":     map[string]any{"merchantId": merchantID},
		},
	})
	if err != nil {
		panic(err)
	}
	return body
}

// signedNormalizationPGWaffoRequest signs the exact payload locally (no
// network) and returns an independent request so concurrent callers can each
// read their own body.
func signedNormalizationPGWaffoRequest(t *testing.T, body []byte, privateKeyB64 string) *http.Request {
	t.Helper()
	signature, err := waffoutils.Sign(string(body), privateKeyB64)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/waffo/webhook", bytes.NewReader(body))
	request.Header.Set("X-SIGNATURE", signature)
	return request
}

func runNormalizationPGWaffoWebhook(t *testing.T, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = request
	WaffoWebhook(c)
	return recorder
}

func normalizationPGWaffoOrder(t *testing.T, db *gorm.DB, user *model.User, tradeNo, amount string) *model.TopUp {
	t.Helper()
	topUp := &model.TopUp{
		UserId:          user.Id,
		Amount:          100,
		Money:           100,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWaffo,
		PaymentProvider: model.PaymentProviderWaffo,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	binding, err := paymentcore.NewWaffoBinding(tradeNo, amount, "USD")
	require.NoError(t, err)
	require.NoError(t, paymentcore.InsertTopUpWithBinding(topUp, binding))
	return topUp
}

func normalizationPGPancakePayload(tradeNo, orderID, storeID, mode, amount, currency string, userID int) []byte {
	body, err := common.Marshal(map[string]any{
		"id":        "normalization-pg",
		"eventType": "order.completed",
		"mode":      mode,
		"storeId":   storeID,
		"data": map[string]any{
			"orderId":                       orderID,
			"orderMerchantExternalId":       tradeNo,
			"merchantProvidedBuyerIdentity": service.WaffoPancakeBuyerIdentityFromUserID(userID),
			"amount":                        amount,
			"currency":                      currency,
		},
	})
	if err != nil {
		panic(err)
	}
	return body
}

func normalizationPGPancakeOrder(t *testing.T, db *gorm.DB, user *model.User, tradeNo, amount string) *model.TopUp {
	t.Helper()
	topUp := &model.TopUp{
		UserId:          user.Id,
		Amount:          50,
		Money:           50,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	binding, err := paymentcore.NewWaffoPancakeBinding(tradeNo, paymentcore.OrderBindingKindTopUp, amount)
	require.NoError(t, err)
	require.NoError(t, paymentcore.InsertTopUpWithBinding(topUp, binding))
	return topUp
}

// TestWaffoPancakeNormalizationPostgreSQL drives the real signed provider
// success chain into binding validation and settlement on PostgreSQL:
// credited paid ledger, native referral commission, duplicate/concurrent
// replay, late out-of-order callbacks, and fail-closed mismatched callbacks.
// Every network interaction is local crypto against httptest gin contexts;
// the settlement functions under test are never mocked.
func TestWaffoPancakeNormalizationPostgreSQL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openNormalizationPostgres(t)

	privateKeyB64, publicKeyB64 := normalizationPGWaffoKeys(t)
	confirmPaymentComplianceForTest(t)

	previousWaffo := struct {
		enabled      bool
		sandbox      bool
		apiKey       string
		privateKey   string
		publicCert   string
		merchantID   string
		currency     string
		sandboxKey   string
		sandboxPriv  string
		sandboxCert  string
		affEnabled   bool
		affRate      float64
		waffoUnit    float64
		quotaPerUnit float64
		redisEnabled bool
	}{
		setting.WaffoEnabled, setting.WaffoSandbox, setting.WaffoApiKey, setting.WaffoPrivateKey,
		setting.WaffoPublicCert, setting.WaffoMerchantId, setting.WaffoCurrency,
		setting.WaffoSandboxApiKey, setting.WaffoSandboxPrivateKey, setting.WaffoSandboxPublicCert,
		common.AffiliateRewardEnabled, common.AffiliateRewardRate, setting.WaffoUnitPrice, common.QuotaPerUnit,
		common.RedisEnabled,
	}
	previousPancake := struct {
		merchantID string
		privateKey string
		storeID    string
		productID  string
		env        string
	}{
		setting.WaffoPancakeMerchantID, setting.WaffoPancakePrivateKey,
		setting.WaffoPancakeStoreID, setting.WaffoPancakeProductID, setting.WaffoPancakeEnvironment,
	}
	t.Cleanup(func() {
		setting.WaffoEnabled = previousWaffo.enabled
		setting.WaffoSandbox = previousWaffo.sandbox
		setting.WaffoApiKey = previousWaffo.apiKey
		setting.WaffoPrivateKey = previousWaffo.privateKey
		setting.WaffoPublicCert = previousWaffo.publicCert
		setting.WaffoMerchantId = previousWaffo.merchantID
		setting.WaffoCurrency = previousWaffo.currency
		setting.WaffoSandboxApiKey = previousWaffo.sandboxKey
		setting.WaffoSandboxPrivateKey = previousWaffo.sandboxPriv
		setting.WaffoSandboxPublicCert = previousWaffo.sandboxCert
		common.AffiliateRewardEnabled = previousWaffo.affEnabled
		common.AffiliateRewardRate = previousWaffo.affRate
		setting.WaffoUnitPrice = previousWaffo.waffoUnit
		common.QuotaPerUnit = previousWaffo.quotaPerUnit
		common.RedisEnabled = previousWaffo.redisEnabled
		setting.WaffoPancakeMerchantID = previousPancake.merchantID
		setting.WaffoPancakePrivateKey = previousPancake.privateKey
		setting.WaffoPancakeStoreID = previousPancake.storeID
		setting.WaffoPancakeProductID = previousPancake.productID
		setting.WaffoPancakeEnvironment = previousPancake.env
	})

	setting.WaffoEnabled = true
	setting.WaffoSandbox = false
	setting.WaffoApiKey = "normalization-pg-api-key"
	setting.WaffoPrivateKey = privateKeyB64
	setting.WaffoPublicCert = publicKeyB64
	setting.WaffoSandboxApiKey = "normalization-pg-sandbox-api-key"
	setting.WaffoSandboxPrivateKey = privateKeyB64
	setting.WaffoSandboxPublicCert = publicKeyB64
	setting.WaffoMerchantId = "MER_NORM"
	setting.WaffoCurrency = "USD"
	setting.WaffoUnitPrice = 1
	common.AffiliateRewardEnabled = true
	common.AffiliateRewardRate = 0.05
	common.RedisEnabled = false

	setting.WaffoPancakeMerchantID = "MER_PANCAKE_NORM"
	setting.WaffoPancakePrivateKey = "normalization-pg-pancake-private"
	setting.WaffoPancakeStoreID = "STORE_NORM"
	setting.WaffoPancakeProductID = "PROD_NORM"
	setting.WaffoPancakeEnvironment = setting.WaffoPancakeEnvironmentProd

	inviter := normalizationPGUser(t, db, 91001, "norm_pg_inviter", 0)
	quotaPerUnit := common.QuotaPerUnit
	require.Positive(t, quotaPerUnit)

	// One consistent buyer fixture per scenario keeps quota/commission
	// assertions exact; inviter receives every scenario's commission.
	buyerSeq := 91010
	newBuyer := func() *model.User {
		buyerSeq++
		return normalizationPGUser(t, db, buyerSeq, fmt.Sprintf("norm_pg_buyer_%d", buyerSeq), inviter.Id)
	}

	assertUserCreditedOnce := func(t *testing.T, user *model.User, ledgerRef string, expectedPaid int) {
		t.Helper()
		var refreshed model.User
		require.NoError(t, db.Where("id = ?", user.Id).First(&refreshed).Error)
		assert.Equal(t, expectedPaid, refreshed.QuotaPaid, "paid account ledger must be credited once")
		assert.Equal(t, expectedPaid, refreshed.Quota, "compatibility quota mirror must match the paid account")

		var ledgerRows []model.BalanceLedger
		require.NoError(t, db.Where("reference_type = ? AND reference_id = ?", "topup", ledgerRef).Find(&ledgerRows).Error)
		require.Len(t, ledgerRows, 1, "exactly one paid ledger row per top-up")
		assert.Equal(t, string(model.AccountPaid), ledgerRows[0].Account)
		assert.Equal(t, expectedPaid, ledgerRows[0].Delta)
		assert.Equal(t, model.LedgerReasonTopup, ledgerRows[0].Reason)
	}

	assertReferralCommissionOnce := func(t *testing.T, buyer *model.User, ledgerRef string, paid int) {
		t.Helper()
		expectedReward := int(float64(paid) * 0.05)
		require.Positive(t, expectedReward)

		var commissions []model.ReferralCommission
		require.NoError(t, db.Where("reference_type = ? AND reference_id = ?", "topup", ledgerRef).Find(&commissions).Error)
		require.Len(t, commissions, 1, "exactly one native referral commission per paid top-up")
		assert.Equal(t, inviter.Id, commissions[0].UserId)
		assert.Equal(t, buyer.Id, commissions[0].SourceUserId)
		assert.Equal(t, paid, commissions[0].PaidQuota)
		assert.Equal(t, expectedReward, commissions[0].RewardQuota)
		assert.InDelta(t, 0.05, commissions[0].Rate, 0.000001)

		var refreshedInviter model.User
		require.NoError(t, db.Where("id = ?", inviter.Id).First(&refreshedInviter).Error)
		var commissionTotal int
		for _, commission := range commissions {
			commissionTotal += commission.RewardQuota
		}
		assert.GreaterOrEqual(t, refreshedInviter.AffQuota, commissionTotal)
		assert.GreaterOrEqual(t, refreshedInviter.AffHistoryQuota, commissionTotal)
	}

	t.Run("waffo_signed_callback_credits_paid_ledger_and_referral", func(t *testing.T) {
		buyer := newBuyer()
		tradeNo := normalizationPGTradePrefix + "waffo-success"
		normalizationPGWaffoOrder(t, db, buyer, tradeNo, "100.00")
		expectedPaid := int(100 * quotaPerUnit)

		body := normalizationPGWaffoPayload(tradeNo, "ACQ_NORM_1", "MER_NORM", "100.00", "USD", "PAY_SUCCESS")
		recorder := runNormalizationPGWaffoWebhook(t, signedNormalizationPGWaffoRequest(t, body, privateKeyB64))
		require.Equal(t, http.StatusOK, recorder.Code)
		assert.Contains(t, recorder.Body.String(), `"message":"success"`)

		var topUp model.TopUp
		require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&topUp).Error)
		assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
		assert.Positive(t, topUp.CompleteTime)

		assertUserCreditedOnce(t, buyer, tradeNo, expectedPaid)
		assertReferralCommissionOnce(t, buyer, tradeNo, expectedPaid)
	})

	t.Run("waffo_duplicate_callback_settles_once", func(t *testing.T) {
		buyer := newBuyer()
		tradeNo := normalizationPGTradePrefix + "waffo-duplicate"
		normalizationPGWaffoOrder(t, db, buyer, tradeNo, "100.00")
		expectedPaid := int(100 * quotaPerUnit)
		body := normalizationPGWaffoPayload(tradeNo, "ACQ_NORM_DUP", "MER_NORM", "100.00", "USD", "PAY_SUCCESS")

		for i := range 3 {
			recorder := runNormalizationPGWaffoWebhook(t, signedNormalizationPGWaffoRequest(t, body, privateKeyB64))
			require.Equal(t, http.StatusOK, recorder.Code, "replay %d must be acknowledged", i)
			assert.Contains(t, recorder.Body.String(), `"message":"success"`)
		}
		assertUserCreditedOnce(t, buyer, tradeNo, expectedPaid)
		assertReferralCommissionOnce(t, buyer, tradeNo, expectedPaid)
	})

	t.Run("waffo_concurrent_callbacks_settle_once", func(t *testing.T) {
		buyer := newBuyer()
		tradeNo := normalizationPGTradePrefix + "waffo-concurrent"
		normalizationPGWaffoOrder(t, db, buyer, tradeNo, "100.00")
		expectedPaid := int(100 * quotaPerUnit)
		body := normalizationPGWaffoPayload(tradeNo, "ACQ_NORM_RACE", "MER_NORM", "100.00", "USD", "PAY_SUCCESS")

		// Each concurrent callback needs its own request because the handler
		// reads the request body; signing is local and deterministic.
		requests := make([]*http.Request, 4)
		for i := range requests {
			requests[i] = signedNormalizationPGWaffoRequest(t, body, privateKeyB64)
		}
		var waitGroup sync.WaitGroup
		for _, request := range requests {
			waitGroup.Add(1)
			go func(req *http.Request) {
				defer waitGroup.Done()
				_ = runNormalizationPGWaffoWebhook(t, req)
			}(request)
		}
		waitGroup.Wait()

		var topUp model.TopUp
		require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&topUp).Error)
		assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
		assertUserCreditedOnce(t, buyer, tradeNo, expectedPaid)
		assertReferralCommissionOnce(t, buyer, tradeNo, expectedPaid)
	})

	t.Run("waffo_late_close_callback_does_not_downgrade", func(t *testing.T) {
		buyer := newBuyer()
		tradeNo := normalizationPGTradePrefix + "waffo-out-of-order"
		normalizationPGWaffoOrder(t, db, buyer, tradeNo, "100.00")
		expectedPaid := int(100 * quotaPerUnit)

		success := normalizationPGWaffoPayload(tradeNo, "ACQ_NORM_OOO", "MER_NORM", "100.00", "USD", "PAY_SUCCESS")
		require.Equal(t, http.StatusOK, runNormalizationPGWaffoWebhook(t, signedNormalizationPGWaffoRequest(t, success, privateKeyB64)).Code)

		// An out-of-order terminal close callback arriving after settlement
		// must be acknowledged without downgrading or double-crediting.
		lateClose := normalizationPGWaffoPayload(tradeNo, "ACQ_NORM_OOO", "MER_NORM", "100.00", "USD", "ORDER_CLOSE")
		require.Equal(t, http.StatusOK, runNormalizationPGWaffoWebhook(t, signedNormalizationPGWaffoRequest(t, lateClose, privateKeyB64)).Code)

		var topUp model.TopUp
		require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&topUp).Error)
		assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
		assertUserCreditedOnce(t, buyer, tradeNo, expectedPaid)
		assertReferralCommissionOnce(t, buyer, tradeNo, expectedPaid)
	})

	t.Run("waffo_mismatched_callbacks_fail_closed_without_credit", func(t *testing.T) {
		type mismatchCase struct {
			name      string
			payload   func(tradeNo string) []byte
			prePinned string
		}
		cases := []mismatchCase{
			{
				name: "amount",
				payload: func(tradeNo string) []byte {
					return normalizationPGWaffoPayload(tradeNo, "ACQ_NORM_BAD", "MER_NORM", "99.00", "USD", "PAY_SUCCESS")
				},
			},
			{
				name: "currency",
				payload: func(tradeNo string) []byte {
					return normalizationPGWaffoPayload(tradeNo, "ACQ_NORM_BAD", "MER_NORM", "100.00", "EUR", "PAY_SUCCESS")
				},
			},
			{
				name: "merchant",
				payload: func(tradeNo string) []byte {
					return normalizationPGWaffoPayload(tradeNo, "ACQ_NORM_BAD", "MER_OTHER", "100.00", "USD", "PAY_SUCCESS")
				},
			},
			{
				name: "order-id",
				payload: func(tradeNo string) []byte {
					return normalizationPGWaffoPayload(tradeNo, "ACQ_NORM_LOSER", "MER_NORM", "100.00", "USD", "PAY_SUCCESS")
				},
				prePinned: "ACQ_NORM_WINNER",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				buyer := newBuyer()
				tradeNo := normalizationPGTradePrefix + "waffo-mismatch-" + tc.name
				normalizationPGWaffoOrder(t, db, buyer, tradeNo, "100.00")
				if tc.prePinned != "" {
					require.NoError(t, paymentcore.BindProviderOrderID(tradeNo, tc.prePinned))
				}

				recorder := runNormalizationPGWaffoWebhook(t, signedNormalizationPGWaffoRequest(t, tc.payload(tradeNo), privateKeyB64))
				require.Equal(t, http.StatusOK, recorder.Code)
				assert.Contains(t, recorder.Body.String(), `"message":"failed"`,
					"a rejected callback must be signed as failed without settling")

				var refreshed model.TopUp
				require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&refreshed).Error)
				assert.Equal(t, common.TopUpStatusPending, refreshed.Status, "rejected callback must not settle the order")

				var ledgerCount, commissionCount int64
				require.NoError(t, db.Model(&model.BalanceLedger{}).
					Where("reference_type = ? AND reference_id = ?", "topup", tradeNo).Count(&ledgerCount).Error)
				require.NoError(t, db.Model(&model.ReferralCommission{}).
					Where("reference_type = ? AND reference_id = ?", "topup", tradeNo).Count(&commissionCount).Error)
				assert.Zero(t, ledgerCount)
				assert.Zero(t, commissionCount)

				var refreshedBuyer model.User
				require.NoError(t, db.Where("id = ?", buyer.Id).First(&refreshedBuyer).Error)
				assert.Zero(t, refreshedBuyer.QuotaPaid)
				assert.Zero(t, refreshedBuyer.Quota)
			})
		}
	})

	t.Run("pancake_signed_callback_credits_and_rejects_store_env_amount", func(t *testing.T) {
		pancakeKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		publicDER, err := x509.MarshalPKIXPublicKey(&pancakeKey.PublicKey)
		require.NoError(t, err)
		publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
		t.Setenv("WAFFO_WEBHOOK_PROD_PUBLIC_KEY", publicPEM)
		t.Setenv("WAFFO_WEBHOOK_TEST_PUBLIC_KEY", publicPEM)

		buyer := newBuyer()
		tradeNo := normalizationPGTradePrefix + "pancake-success"
		normalizationPGPancakeOrder(t, db, buyer, tradeNo, "50.00")
		expectedPaid := int(50 * quotaPerUnit)

		success := normalizationPGPancakePayload(tradeNo, "ORD_NORM_1", "STORE_NORM", "prod", "50.00", "USD", buyer.Id)
		recorder := runPancakeRetryWebhook(t, signedPancakeRetryRequest(t, success, pancakeKey))
		require.Equal(t, http.StatusOK, recorder.Code)

		var topUp model.TopUp
		require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&topUp).Error)
		assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
		assertUserCreditedOnce(t, buyer, tradeNo, expectedPaid)
		assertReferralCommissionOnce(t, buyer, tradeNo, expectedPaid)

		rejections := []struct {
			name    string
			storeID string
			mode    string
			amount  string
		}{
			{name: "amount", storeID: "STORE_NORM", mode: "prod", amount: "0.01"},
			{name: "store", storeID: "STORE_OTHER", mode: "prod", amount: "50.00"},
			{name: "environment", storeID: "STORE_NORM", mode: "test", amount: "50.00"},
		}
		for _, rejection := range rejections {
			t.Run(rejection.name, func(t *testing.T) {
				rejectedBuyer := newBuyer()
				rejectedTradeNo := normalizationPGTradePrefix + "pancake-reject-" + rejection.name
				normalizationPGPancakeOrder(t, db, rejectedBuyer, rejectedTradeNo, "50.00")

				body := normalizationPGPancakePayload(rejectedTradeNo, "ORD_NORM_BAD", rejection.storeID, rejection.mode, rejection.amount, "USD", rejectedBuyer.Id)
				recorder := runPancakeRetryWebhook(t, signedPancakeRetryRequest(t, body, pancakeKey))
				require.Equal(t, http.StatusOK, recorder.Code)

				var refreshed model.TopUp
				require.NoError(t, db.Where("trade_no = ?", rejectedTradeNo).First(&refreshed).Error)
				assert.Equal(t, common.TopUpStatusPending, refreshed.Status)

				var ledgerCount, commissionCount int64
				require.NoError(t, db.Model(&model.BalanceLedger{}).
					Where("reference_type = ? AND reference_id = ?", "topup", rejectedTradeNo).Count(&ledgerCount).Error)
				require.NoError(t, db.Model(&model.ReferralCommission{}).
					Where("reference_type = ? AND reference_id = ?", "topup", rejectedTradeNo).Count(&commissionCount).Error)
				assert.Zero(t, ledgerCount)
				assert.Zero(t, commissionCount)
			})
		}
	})
}
