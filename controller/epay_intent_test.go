package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
)

const testEpayUUID = "e6429007-ded5-4c35-a483-2b97c260b6f4"

var testEpayIntent = testEpayUUID + "." + epayQuoteGatewayRef("https://pay.example.test", "MD5")

func TestEpayIntentIdentifier(t *testing.T) {
	a, err := epayIntentTradeNo(1, testEpayIntent)
	require.NoError(t, err)
	b, err := epayIntentTradeNo(2, testEpayIntent)
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	require.Regexp(t, "^EI[A-Z2-7]{26}$", a)
	for _, id := range []string{"", "1", "00000000-0000-0000-0000-000000000000", "E6429007-DED5-4C35-A483-2B97C260B6F4"} {
		_, err := epayIntentTradeNo(1, id)
		require.Error(t, err)
	}
	_, err = epayIntentTradeNo(0, testEpayIntent)
	require.Error(t, err)
}
func TestEpayIntentGatewayIdentity(t *testing.T) {
	a := epayQuoteGatewayRef("https://PAY.EXAMPLE.TEST:443/v2/", "MD5")
	require.NotEmpty(t, a)
	require.Equal(t, epayEndpointRef("https://pay.example.test/v2/submit.php#return"), a)
	for _, other := range []string{"http://pay.example.test/v2/submit.php", "https://pay.example.test:444/v2/submit.php", "https://pay.example.test/v3/submit.php", "https://pay.example.test/v2/submit.php?site=2", "https://another.test/v2/submit.php"} {
		require.NotEqual(t, a, epayEndpointRef(other))
	}
	require.NotEqual(t, a, epayQuoteGatewayRef("https://pay.example.test/v2/", "RSA"))
	require.Empty(t, epayEndpointRef("https://user:password@example.invalid/"))
	require.Empty(t, epayEndpointRef("/not-absolute"))
	for _, invalid := range []string{testEpayUUID, testEpayUUID + ".not-a-hash", "bad." + a, "00000000-0000-0000-0000-000000000000." + a} {
		_, err := epayIntentTradeNo(7, invalid)
		require.Error(t, err)
	}
}
func TestEpayIntentConcurrentReceiptsPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated TEST_POSTGRES_DSN")
	}
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	old := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = old })
	var version string
	require.NoError(t, db.Raw("SHOW server_version").Scan(&version).Error)
	t.Logf("isolated PostgreSQL %s", version)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}))
	no, err := epayIntentTradeNo(7, testEpayIntent)
	require.NoError(t, err)
	order := model.TopUp{UserId: 7, TradeNo: no, Amount: 10, Money: 73, PaymentProvider: model.PaymentProviderEpay, PaymentMethod: "alipay", QuotedWalletQuota: 5000000, QuotedPaymentMinor: 7300, QuotedPaymentCurrency: "CNY", QuotedPaymentScope: "fixture", QuotedPaymentEnvironment: "live", Status: common.TopUpStatusPending}
	receipt := func(owner int) (int, map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/fixture", nil)
		c.Set("id", owner)
		c.Params = gin.Params{{Key: "intent", Value: testEpayIntent}}
		GetEpayIntent(c)
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return w.Code, body
	}
	status, body := receipt(7)
	require.Equal(t, 404, status)
	require.Equal(t, "epay_intent_not_found", body["code"])
	require.Equal(t, no, body["reference"])
	const concurrent = 12
	results := make(chan error, concurrent)
	var wg sync.WaitGroup
	for range concurrent {
		wg.Add(1)
		go func() { defer wg.Done(); o := order; _, e := persistEpayIntent(t.Context(), &o); results <- e }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		require.NoError(t, e)
	}
	var count int64
	require.NoError(t, db.Model(&model.TopUp{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	status, body = receipt(7)
	require.Equal(t, 200, status)
	require.Equal(t, map[string]any{"success": true, "trade_no": no, "order_status": "pending", "payment_method": "alipay", "payment_minor": float64(7300)}, body)
	status, _ = receipt(8)
	require.Equal(t, 404, status)
	for _, mutate := range []func(*model.TopUp){func(o *model.TopUp) { o.Amount++ }, func(o *model.TopUp) { o.QuotedPaymentMinor++ }, func(o *model.TopUp) { o.PaymentMethod = "wxpay" }, func(o *model.TopUp) { o.QuotedWalletQuota++ }, func(o *model.TopUp) { o.QuotedPaymentScope = "other" }, func(o *model.TopUp) { o.QuotedPaymentEnvironment = "sandbox" }} {
		o := order
		mutate(&o)
		_, err := persistEpayIntent(t.Context(), &o)
		require.ErrorIs(t, err, errEpayIntentConflict)
	}
	require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no=?", no).Update("status", common.TopUpStatusSuccess).Error)
	o := order
	replay, e := persistEpayIntent(t.Context(), &o)
	require.NoError(t, e)
	require.Equal(t, common.TopUpStatusSuccess, replay.Status)
	_, body = receipt(7)
	require.Equal(t, common.TopUpStatusSuccess, body["order_status"])
}

func TestEpayIntentCheckoutHandlerPostgres(t *testing.T) {
	for _, signType := range []string{epay.SignTypeMD5, epay.SignTypeRSA} {
		t.Run(signType, func(t *testing.T) { exerciseEpayIntentCheckout(t, signType) })
	}
}

func exerciseEpayIntentCheckout(t *testing.T, signType string) {
	t.Helper()
	db := setupNewUserBenefitControllerPostgres(t)
	settings := operation_setting.GetPaymentSetting()
	oldSign, oldMerchantKey, oldPlatformKey := settings.EpaySignType, settings.EpayMerchantPrivateKey, settings.EpayPlatformPublicKey
	t.Cleanup(func() {
		settings.EpaySignType, settings.EpayMerchantPrivateKey, settings.EpayPlatformPublicKey = oldSign, oldMerchantKey, oldPlatformKey
	})
	settings.EpaySignType = signType
	var platform *epay.Client
	if signType == epay.SignTypeRSA {
		merchantKey, merchantPublic := newEpayRSAKeyPairForTest(t)
		platformKey, platformPublic := newEpayRSAKeyPairForTest(t)
		settings.EpayMerchantPrivateKey, settings.EpayPlatformPublicKey = merchantKey, platformPublic
		var err error
		platform, err = epay.NewClient(&epay.Config{PartnerID: operation_setting.EpayId, SignType: epay.SignTypeRSA, MerchantPrivateKey: platformKey, PlatformPublicKey: merchantPublic}, operation_setting.PayAddress)
		require.NoError(t, err)
	}
	intent := testEpayUUID + "." + epayQuoteGatewayRef(operation_setting.PayAddress, signType)
	// The benefit fixture assigns model.DB directly; an ordinary wallet checkout
	// also needs the dialect-quoted user group column initialized by application startup.
	t.Setenv("SQL_DSN", db.Dialector.(*postgres.Dialector).DSN)
	t.Setenv("LOG_SQL_DSN", "")
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.AgentCommissionOutbox{}))
	require.NoError(t, model.InitDB())
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	user := createNewUserBenefitControllerUser(t, db, "epay-intent", true)
	quoteResponse := httptest.NewRecorder()
	quoteContext, _ := gin.CreateTestContext(quoteResponse)
	quoteContext.Request = httptest.NewRequest(http.MethodPost, "/api/user/amount", strings.NewReader(`{"amount":100}`))
	quoteContext.Request.Header.Set("Content-Type", "application/json")
	quoteContext.Set("id", user.Id)
	RequestAmount(quoteContext)
	var quote map[string]any
	require.NoError(t, json.Unmarshal(quoteResponse.Body.Bytes(), &quote))
	require.Equal(t, "7700.00", quote["data"])
	require.Equal(t, intent, testEpayUUID+"."+quote["epay_gateway"].(string))
	post := func(intent, body string) (int, map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/fixture", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", user.Id)
		if intent != "" {
			c.Params = gin.Params{{Key: "intent", Value: intent}}
		}
		RequestEpay(c)
		var response map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		return w.Code, response
	}
	const payload = `{"amount":100,"payment_method":"alipay","expected_minor":770000}`
	// The displayed quote was issued on A. A first submit hitting B (same PID)
	// is authoritatively refused before any local order insertion or handoff.
	quotedOn := operation_setting.PayAddress
	operation_setting.PayAddress = "https://new-gateway.example.invalid"
	firstCode, firstResponse := post(intent, payload)
	require.Equal(t, 409, firstCode)
	require.Equal(t, intent, firstResponse["intent_id"])
	require.Equal(t, false, firstResponse["insert_attempted"])
	require.NotContains(t, firstResponse, "data")
	var before int64
	require.NoError(t, db.Model(&model.TopUp{}).Count(&before).Error)
	require.Zero(t, before)
	operation_setting.PayAddress = quotedOn
	code, body := post(intent, `{"amount":100,"payment_method":"alipay","expected_minor":11}`)
	require.Equal(t, 409, code, "%v", body)
	require.Equal(t, "quote_changed", body["code"])
	code, body = post(intent, payload)
	require.Equal(t, 200, code)
	require.Equal(t, "success", body["message"], "%v", body)
	trade := body["trade_no"]
	require.NotEmpty(t, trade)
	require.Equal(t, "pending", body["order_status"])
	require.Equal(t, trade, body["data"].(map[string]any)["out_trade_no"])
	_, next := post(intent, payload)
	require.Equal(t, trade, next["trade_no"])
	code, body = post(intent, `{"amount":100,"payment_method":"wxpay","expected_minor":770000}`)
	require.Equal(t, 409, code, "%v", body)
	require.Equal(t, "intent_conflict", body["code"])
	orig := operation_setting.PayAddress
	operation_setting.PayAddress = "https://other.example.invalid"
	code, body = post(intent, payload)
	require.Equal(t, 409, code, "%v", body)
	require.Equal(t, "gateway_changed", body["code"])
	require.Equal(t, false, body["insert_attempted"])
	require.Equal(t, intent, body["intent_id"])
	require.NotContains(t, body, "url")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/fixture", nil)
	c.Set("id", user.Id)
	c.Params = gin.Params{{Key: "intent", Value: intent}}
	GetEpayIntent(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), trade)
	operation_setting.PayAddress = orig
	var n int64
	require.NoError(t, db.Model(&model.TopUp{}).Count(&n).Error)
	require.EqualValues(t, 1, n)
	// Receipt creation/signing alone must never credit the user.
	var after model.User
	require.NoError(t, db.First(&after, user.Id).Error)
	require.Equal(t, user.Quota, after.Quota)
	// Only the already-existing, verified callback can settle this ordinary order.
	for range 2 {
		var callback *httptest.ResponseRecorder
		if platform == nil {
			callback = newUserBenefitSignedCallbackForTest(t, trade.(string), "new-user-test-merchant", "alipay", "7700.00", "synthetic-ref-1")
		} else {
			signed, err := platform.SignParams(map[string]string{
				"out_trade_no": trade.(string), "trade_no": "synthetic-ref-1", "type": "alipay",
				"money": "7700.00", "trade_status": epay.StatusTradeSuccess, "name": "fixture-top-up",
			})
			require.NoError(t, err)
			form := url.Values{}
			for key, value := range signed {
				form.Set(key, value)
			}
			callback = httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(callback)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user/epay/notify", strings.NewReader(form.Encode()))
			ctx.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			EpayNotify(ctx)
		}
		require.Equal(t, "success", strings.TrimSpace(callback.Body.String()))
	}
	_, body = post(intent, payload)
	require.Equal(t, "success", body["order_status"])
	require.NotContains(t, body, "data")
	require.NotContains(t, body, "url")
	require.NoError(t, db.First(&after, user.Id).Error)
	require.Equal(t, user.Quota+50000000, after.Quota)
	require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id=? AND reference_id=?", user.Id, trade).Count(&n).Error)
	require.EqualValues(t, 1, n)
	// Backwards compatibility: no key or expected_minor required on the legacy route.
	_, body = post("", `{"amount":100,"payment_method":"alipay"}`)
	require.Equal(t, "success", body["message"])
	require.NotEqual(t, trade, body["data"].(map[string]any)["out_trade_no"])
}
