package controller

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/paymentcore"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81/webhook"
	waffo "github.com/waffo-com/waffo-go"
	waffonet "github.com/waffo-com/waffo-go/net"
	"github.com/waffo-com/waffo-go/utils"
	"gorm.io/gorm"
)

type staleWaffoTransport func(context.Context, *waffonet.HttpRequest) (*waffonet.HttpResponse, error)

func (f staleWaffoTransport) Send(ctx context.Context, request *waffonet.HttpRequest) (*waffonet.HttpResponse, error) {
	return f(ctx, request)
}

type stalePancakeTransport func(*http.Request) (*http.Response, error)

func (f stalePancakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// The model's quoted-column caches are private process globals. Keep their
// initialization inside a same-binary child, including when the whole controller
// package is run: the parent retains only the original test-node topology.
func runStalePaymentFixtureChild(t *testing.T, intermediateAncestors ...string) bool {
	t.Helper()
	const childEnv = "PAYMENT_STALE_FIXTURE_CHILD"
	if target := os.Getenv(childEnv); target != "" {
		require.Equal(t, t.Name(), target, "isolated fixture child must match its exact target")
		return false
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	budget := 12 * time.Minute
	if deadline, ok := t.Deadline(); ok {
		budget = time.Until(deadline) - 5*time.Second
	}
	require.Positive(t, budget, "isolated fixture needs time to run and join within the parent deadline")
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()
	parts := strings.Split(t.Name(), "/")
	for i := range parts {
		parts[i] = "^" + regexp.QuoteMeta(parts[i]) + "$"
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run="+strings.Join(parts, "/"), "-test.count=1", "-test.v=true", "-test.timeout="+budget.String())
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, childEnv+"=") && !strings.HasPrefix(entry, "PAYMENT_STALE_STRIPE_DSN=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, childEnv+"="+t.Name())
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	pid, exitCode := 0, -1
	joined := false
	err = cmd.Start()
	if err == nil {
		pid = cmd.Process.Pid
		t.Cleanup(func() {
			if !joined {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		err = cmd.Wait()
		joined = true
		exitCode = cmd.ProcessState.ExitCode()
	}
	// A slash inside one t.Run name is not a separate ancestor. Callers with
	// genuinely nested t.Run scopes explicitly supply their intermediate nodes.
	root := strings.SplitN(t.Name(), "/", 2)[0]
	expected := []string{root}
	for _, ancestor := range intermediateAncestors {
		require.True(t, strings.HasPrefix(ancestor, root+"/"))
		require.True(t, strings.HasPrefix(t.Name(), ancestor+"/"))
		require.NotContains(t, expected, ancestor)
		expected = append(expected, ancestor)
	}
	if t.Name() != root {
		expected = append(expected, t.Name())
	}
	runs, passes, fails, skips := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	packagePasses, packageFails := 0, 0
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			switch fields[0] + " " + fields[1] {
			case "=== RUN":
				runs[fields[2]]++
			case "--- PASS:":
				passes[fields[2]]++
			case "--- FAIL:":
				fails[fields[2]]++
			case "--- SKIP:":
				skips[fields[2]]++
			}
		}
		if line == "PASS" {
			packagePasses++
		}
		if line == "FAIL" {
			packageFails++
		}
	}
	contextError := ""
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		contextError = "deadline_exceeded"
	} else if ctx.Err() != nil {
		contextError = "canceled"
	}
	valid := joined && ctx.Err() == nil && err == nil && scanner.Err() == nil && exitCode == 0 && packagePasses == 1 && packageFails == 0 && len(fails) == 0 && len(skips) == 0 && len(runs) == len(expected) && len(passes) == len(expected)
	for _, name := range expected {
		valid = valid && runs[name] == 1 && passes[name] == 1
	}
	for _, marker := range []string{"WARNING: DATA RACE", "panic:", "fatal error:"} {
		valid = valid && !strings.Contains(stdout.String(), marker) && !strings.Contains(stderr.String(), marker)
	}
	result := map[string]any{"target": t.Name(), "pid": pid, "exit_code": exitCode, "joined": joined, "context_error": contextError, "package_passes": packagePasses, "package_fails": packageFails, "expected_nodes": expected, "runs": runs, "passes": passes, "fails": fails, "skips": skips}
	if valid {
		result["stdout_sha256"] = fmt.Sprintf("%x", sha256.Sum256(stdout.Bytes()))
		result["stderr_sha256"] = fmt.Sprintf("%x", sha256.Sum256(stderr.Bytes()))
	} else {
		result["stdout"], result["stderr"] = stdout.String(), stderr.String()
		if err != nil {
			result["process_error"] = err.Error()
		}
		if scanner.Err() != nil {
			result["parse_error"] = scanner.Err().Error()
		}
	}
	encoded, encodeErr := common.Marshal(result)
	require.NoError(t, encodeErr)
	// JSON quoting keeps child === RUN / --- PASS lines out of the parent's
	// event stream while preserving the original financial assertion on RED.
	t.Logf("PAYMENT_STALE_FIXTURE_CHILD_RESULT %s", encoded)
	require.True(t, valid, "isolated fixture child must exit zero with exact required RUN/PASS and no skipped, failed, or extra nodes")
	return true
}

// Every outbound payment request is handled by an in-memory transport. The
// request/response channels are a barrier, not a timing race: the signed webhook
// must commit before the creation request receives its delayed failure.
func stalePaymentFixture(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "stale-payment.db")), &gorm.Config{})
	require.NoError(t, err)
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		pool, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, pool.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.SubscriptionOrder{}, &model.Option{}, &paymentcore.PaymentOrderBinding{}, &model.User{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.Log{}, &model.SubscriptionPlan{}, &model.UserSubscription{}))
	confirmPaymentComplianceForTest(t)
	oldLog, oldRedis := model.LOG_DB, common.RedisEnabled
	oldKind, oldLogKind, oldMaster := common.MainDatabaseType(), common.LogDatabaseType(), common.IsMasterNode
	oldDisplay, oldDiscounts := operation_setting.GetGeneralSetting().QuotaDisplayType, operation_setting.GetPaymentSetting().AmountDiscount
	oldGroups := common.TopupGroupRatio2JSONString()
	t.Setenv("LOG_SQL_DSN", "")
	t.Cleanup(func() {
		model.LOG_DB, common.RedisEnabled = oldLog, oldRedis
		common.SetDatabaseTypes(oldKind, oldLogKind)
		common.IsMasterNode = oldMaster
		operation_setting.GetGeneralSetting().QuotaDisplayType, operation_setting.GetPaymentSetting().AmountDiscount = oldDisplay, oldDiscounts
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(oldGroups))
		model.InvalidateSubscriptionPlanCache(29601)
	})
	model.LOG_DB, common.RedisEnabled = db, false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	// The shared, non-master log branch initializes model's quoted columns
	// without opening another database or running any migrations.
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = oldMaster
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, db.Create(&model.User{Id: 29600, Username: "stale-payment", Status: common.UserStatusEnabled, Group: "default"}).Error)
	model.InvalidateSubscriptionPlanCache(29601)
	require.NoError(t, db.Create(&model.SubscriptionPlan{Id: 29601, Title: "stale payment", PriceAmount: 10, Currency: "USD", Enabled: true, DurationUnit: model.SubscriptionDurationDay, DurationValue: 1, TotalAmount: 1000, WaffoPancakeProductId: "PROD_AbCdEfGhIjKlMnOpQrStUv"}).Error)
	return db
}

type stalePaymentSnapshot struct {
	TopUp         model.TopUp
	Order         model.SubscriptionOrder
	User          model.User
	Ledgers       []model.BalanceLedger
	Operations    []model.BalanceLedgerOperation
	Subscriptions []model.UserSubscription
}

func captureStalePaymentSnapshot(t *testing.T, db *gorm.DB, tradeNo string, subscription bool) stalePaymentSnapshot {
	t.Helper()
	var state stalePaymentSnapshot
	require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&state.TopUp).Error)
	if subscription {
		require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&state.Order).Error)
	}
	require.NoError(t, db.First(&state.User, 29600).Error)
	require.NoError(t, db.Order("id").Find(&state.Ledgers).Error)
	require.NoError(t, db.Order("id").Find(&state.Operations).Error)
	require.NoError(t, db.Order("id").Find(&state.Subscriptions).Error)
	return state
}

func staleCreationContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/payment", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 29600)
	return c, recorder
}

func configureStaleWaffo(t *testing.T, privateKey, publicKey string) {
	t.Helper()
	oldEnabled, oldSandbox := setting.WaffoEnabled, setting.WaffoSandbox
	oldAPI, oldPrivate, oldPublic, oldMerchant, oldCurrency := setting.WaffoApiKey, setting.WaffoPrivateKey, setting.WaffoPublicCert, setting.WaffoMerchantId, setting.WaffoCurrency
	oldPrice, oldMin := setting.WaffoUnitPrice, setting.WaffoMinTopUp
	setting.WaffoEnabled, setting.WaffoSandbox = true, false
	setting.WaffoApiKey, setting.WaffoPrivateKey, setting.WaffoPublicCert = "synthetic-key", privateKey, publicKey
	setting.WaffoMerchantId, setting.WaffoCurrency, setting.WaffoUnitPrice, setting.WaffoMinTopUp = "MER_SYNTHETIC", "USD", 1, 1
	t.Cleanup(func() {
		setting.WaffoEnabled, setting.WaffoSandbox = oldEnabled, oldSandbox
		setting.WaffoApiKey, setting.WaffoPrivateKey, setting.WaffoPublicCert, setting.WaffoMerchantId, setting.WaffoCurrency = oldAPI, oldPrivate, oldPublic, oldMerchant, oldCurrency
		setting.WaffoUnitPrice, setting.WaffoMinTopUp = oldPrice, oldMin
	})
}

func deliverStaleWaffoSuccess(t *testing.T, tradeNo, privateKey string) {
	t.Helper()
	body, err := common.Marshal(map[string]any{"eventType": "PAYMENT_NOTIFICATION", "result": map[string]any{"paymentRequestId": tradeNo, "merchantOrderId": tradeNo, "acquiringOrderId": "ACQ_WINNER", "orderStatus": "PAY_SUCCESS", "orderAmount": "10.00", "orderCurrency": "USD", "merchantInfo": map[string]any{"merchantId": "MER_SYNTHETIC"}}})
	require.NoError(t, err)
	signature, err := utils.Sign(string(body), privateKey)
	require.NoError(t, err)
	c, recorder := staleCreationContext(string(body))
	c.Request.Header.Set("X-SIGNATURE", signature)
	WaffoWebhook(c)
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestWaffoCreationFailurePreservesSuccessfulWebhook(t *testing.T) {
	keys, err := waffo.GenerateKeyPair()
	require.NoError(t, err)
	for _, failure := range []string{"transport", "rejected_response", "response_identity", "provider_order_binding"} {
		t.Run(failure, func(t *testing.T) {
			if runStalePaymentFixtureChild(t) {
				return
			}
			db := stalePaymentFixture(t)
			configureStaleWaffo(t, keys.PrivateKey, keys.PublicKey)
			requestReady := make(chan *waffonet.HttpRequest, 1)
			release := make(chan struct{})
			done := make(chan struct{})
			var response *waffonet.HttpResponse
			responseErr := errors.New("fixture released before a response was prepared")
			factory := func() (*waffo.Waffo, error) {
				sdk, err := getWaffoSDK()
				if err != nil {
					return nil, err
				}
				cfg := *sdk.GetConfig()
				cfg.CustomTransport = staleWaffoTransport(func(_ context.Context, request *waffonet.HttpRequest) (*waffonet.HttpResponse, error) {
					requestReady <- request
					<-release
					return response, responseErr
				})
				return waffo.New(&cfg), nil
			}
			c, recorder := staleCreationContext(`{"amount":10}`)
			go func() { defer close(done); requestWaffoPay(c, factory) }()
			// Cleanup also releases a blocked request if a fatal assertion fails.
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
				<-done
			})
			var request *waffonet.HttpRequest
			select {
			case request = <-requestReady:
			case <-done:
				require.FailNow(t, "creation returned before upstream barrier", recorder.Body.String())
			}
			var payload struct {
				MerchantOrderID string `json:"merchantOrderId"`
			}
			require.NoError(t, common.Unmarshal(request.Body, &payload))
			require.NotEmpty(t, payload.MerchantOrderID)
			binding, err := paymentcore.GetPaymentOrderBinding(payload.MerchantOrderID)
			require.NoError(t, err)
			require.Empty(t, binding.ProviderOrderID)
			deliverStaleWaffoSuccess(t, payload.MerchantOrderID, keys.PrivateKey)
			before := captureStalePaymentSnapshot(t, db, payload.MerchantOrderID, false)
			require.Equal(t, common.TopUpStatusSuccess, before.TopUp.Status)
			require.Positive(t, before.TopUp.CompleteTime)
			require.Len(t, before.Ledgers, 1)
			require.Len(t, before.Operations, 1)
			require.Equal(t, int(10*common.QuotaPerUnit), before.Ledgers[0].Delta)
			require.Equal(t, int(10*common.QuotaPerUnit), before.User.Quota)
			require.Equal(t, int(10*common.QuotaPerUnit), before.User.QuotaPaid)
			if failure == "transport" {
				responseErr = errors.New("synthetic delayed transport failure")
			} else {
				code, merchantID, acquiringID := "0", payload.MerchantOrderID, "ACQ_WINNER"
				if failure == "rejected_response" {
					code = "E9999"
				}
				if failure == "response_identity" {
					merchantID = "OTHER_ORDER"
				}
				if failure == "provider_order_binding" {
					acquiringID = "ACQ_CONFLICT"
				}
				body, err := common.Marshal(map[string]any{"code": code, "data": map[string]any{"paymentRequestId": payload.MerchantOrderID, "merchantOrderId": merchantID, "acquiringOrderId": acquiringID}})
				require.NoError(t, err)
				signature, err := utils.Sign(string(body), keys.PrivateKey)
				require.NoError(t, err)
				responseErr = nil
				response = &waffonet.HttpResponse{StatusCode: http.StatusOK, Body: body, Headers: map[string]string{"X-SIGNATURE": signature}}
			}
			close(release)
			<-done
			expectedError := "Failed to start payment"
			if failure == "response_identity" {
				expectedError = "上游支付订单身份异常"
			}
			if failure == "provider_order_binding" {
				expectedError = "支付订单绑定失败"
			}
			assert.Contains(t, recorder.Body.String(), expectedError)
			assert.Equal(t, before, captureStalePaymentSnapshot(t, db, payload.MerchantOrderID, false), "delayed creation failure must preserve the complete committed financial state")
		})
	}
}

func TestWaffoSDKFailureBeforeRequestMarksPendingFailed(t *testing.T) {
	if runStalePaymentFixtureChild(t) {
		return
	}
	db := stalePaymentFixture(t)
	configureStaleWaffo(t, "not-used", "not-used")
	c, recorder := staleCreationContext(`{"amount":10}`)
	requestWaffoPay(c, func() (*waffo.Waffo, error) {
		return nil, errors.New("synthetic SDK failure before any upstream request")
	})
	var topUp model.TopUp
	require.NoError(t, db.First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusFailed, topUp.Status)
	assert.Zero(t, topUp.CompleteTime)
	assert.Contains(t, recorder.Body.String(), "error")
}

func TestPancakeCreationFailurePreservesSuccessfulWebhook(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	for _, subscription := range []bool{false, true} {
		kind := "topup"
		if subscription {
			kind = "subscription"
		}
		for _, failure := range []string{"transport", "provider_session_binding"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				if runStalePaymentFixtureChild(t) {
					return
				}
				db := stalePaymentFixture(t)
				withPancakeBindingConfig(t, "STORE_SYNTHETIC")
				t.Setenv("WAFFO_WEBHOOK_PROD_PUBLIC_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})))
				oldMerchant, oldPrivate, oldProduct := setting.WaffoPancakeMerchantID, setting.WaffoPancakePrivateKey, setting.WaffoPancakeProductID
				oldPrice, oldMin, oldClient := setting.WaffoPancakeUnitPrice, setting.WaffoPancakeMinTopUp, http.DefaultClient
				setting.WaffoPancakeMerchantID, setting.WaffoPancakePrivateKey, setting.WaffoPancakeProductID = "MER_AbCdEfGhIjKlMnOpQrStUv", base64.StdEncoding.EncodeToString(privateDER), "PROD_AbCdEfGhIjKlMnOpQrStUv"
				setting.WaffoPancakeUnitPrice, setting.WaffoPancakeMinTopUp = 1, 1
				t.Cleanup(func() {
					setting.WaffoPancakeMerchantID, setting.WaffoPancakePrivateKey, setting.WaffoPancakeProductID = oldMerchant, oldPrivate, oldProduct
					setting.WaffoPancakeUnitPrice, setting.WaffoPancakeMinTopUp, http.DefaultClient = oldPrice, oldMin, oldClient
				})
				requestReady := make(chan []byte, 1)
				release, done := make(chan struct{}), make(chan struct{})
				http.DefaultClient = &http.Client{Transport: stalePancakeTransport(func(request *http.Request) (*http.Response, error) {
					body, err := io.ReadAll(request.Body)
					if err != nil {
						return nil, err
					}
					response := `{"data":{"token":"synthetic-token","expiresAt":"2099-01-01T00:00:00Z"}}`
					switch request.URL.Path {
					case "/v1/actions/auth/issue-session-token":
					case "/v1/actions/checkout/create-session":
						requestReady <- body
						<-release
						if failure == "transport" {
							return nil, errors.New("synthetic delayed checkout failure")
						}
						response = `{"data":{"sessionId":"SESSION_DELAYED","checkoutUrl":"https://checkout.invalid/synthetic","expiresAt":"2099-01-01T00:00:00Z"}}`
					default:
						return nil, errors.New("unexpected outbound path: " + request.URL.Path)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
				})}
				body := `{"amount":10}`
				handler := RequestWaffoPancakePay
				if subscription {
					body, handler = `{"plan_id":29601}`, SubscriptionRequestWaffoPancakePay
				}
				c, recorder := staleCreationContext(body)
				go func() { defer close(done); handler(c) }()
				t.Cleanup(func() {
					select {
					case <-release:
					default:
						close(release)
					}
					<-done
				})
				var payload struct {
					TradeNo string `json:"orderMerchantExternalId"`
				}
				var requestBody []byte
				select {
				case requestBody = <-requestReady:
				case <-done:
					require.FailNow(t, "creation returned before checkout barrier", recorder.Body.String())
				}
				require.NoError(t, common.Unmarshal(requestBody, &payload))
				require.NotEmpty(t, payload.TradeNo)
				binding, err := paymentcore.GetPaymentOrderBinding(payload.TradeNo)
				require.NoError(t, err)
				require.Empty(t, binding.ProviderSessionID)
				require.Empty(t, binding.ProviderOrderID)
				callbackBody, err := common.Marshal(map[string]any{"id": "synthetic-success", "eventType": "order.completed", "mode": "prod", "storeId": "STORE_SYNTHETIC", "data": map[string]any{"orderId": "ORD_WINNER", "orderMerchantExternalId": payload.TradeNo, "merchantProvidedBuyerIdentity": service.WaffoPancakeBuyerIdentityFromUserID(29600), "amount": "10.00", "currency": "USD"}})
				require.NoError(t, err)
				callback := runPancakeRetryWebhook(t, signedPancakeRetryRequest(t, callbackBody, key))
				require.Equal(t, http.StatusOK, callback.Code)
				before := captureStalePaymentSnapshot(t, db, payload.TradeNo, subscription)
				require.Equal(t, common.TopUpStatusSuccess, before.TopUp.Status)
				require.Positive(t, before.TopUp.CompleteTime)
				if subscription {
					require.Equal(t, string(callbackBody), before.Order.ProviderPayload)
					require.Equal(t, common.TopUpStatusSuccess, before.Order.Status)
					require.Positive(t, before.Order.CompleteTime)
					require.Len(t, before.Subscriptions, 1)
					require.Empty(t, before.Ledgers)
					require.Empty(t, before.Operations)
					require.Zero(t, before.User.Quota)
					require.Zero(t, before.User.QuotaPaid)
					require.Equal(t, model.PaymentMethodWaffoPancake, before.Order.PaymentMethod)
				} else {
					require.Len(t, before.Ledgers, 1)
					require.Len(t, before.Operations, 1)
					require.Equal(t, int(10*common.QuotaPerUnit), before.Ledgers[0].Delta)
					require.Equal(t, int(10*common.QuotaPerUnit), before.User.Quota)
					require.Equal(t, int(10*common.QuotaPerUnit), before.User.QuotaPaid)
				}
				if failure == "provider_session_binding" {
					require.NoError(t, paymentcore.BindProviderSessionID(payload.TradeNo, "SESSION_OTHER"))
				}
				close(release)
				<-done
				expectedError := "Failed to start payment"
				if failure == "provider_session_binding" {
					expectedError = "支付订单绑定失败"
				}
				assert.Contains(t, recorder.Body.String(), expectedError)
				assert.Equal(t, before, captureStalePaymentSnapshot(t, db, payload.TradeNo, subscription), "delayed checkout failure must not erase successful settlement or subscription payload")
			})
		}
	}
}

func configureStaleStripe(t *testing.T) {
	t.Helper()
	oldAPI, oldSecret, oldPrice := setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId
	setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = "sk_test_synthetic_stale", "whsec_synthetic_stale", "price_synthetic_stale"
	t.Cleanup(func() {
		setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = oldAPI, oldSecret, oldPrice
	})
	confirmPaymentComplianceForTest(t)
}

func deliverStaleStripeWebhook(t *testing.T, eventType, tradeNo string) {
	t.Helper()
	paymentStatus := "paid"
	if eventType == "checkout.session.async_payment_failed" {
		paymentStatus = "unpaid"
	}
	body, err := common.Marshal(map[string]any{"id": "evt_synthetic_stale", "object": "event", "type": eventType, "data": map[string]any{"object": map[string]any{"id": "cs_synthetic_stale", "object": "checkout.session", "client_reference_id": tradeNo, "status": "complete", "payment_status": paymentStatus, "customer": "cus_synthetic_stale", "amount_total": 1000, "currency": "usd"}}})
	require.NoError(t, err)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: body, Secret: setting.StripeWebhookSecret})
	c, recorder := staleCreationContext(string(body))
	c.Request.Header.Set("Stripe-Signature", signed.Header)
	StripeWebhook(c)
	require.Equal(t, http.StatusOK, recorder.Code)
}

// Separate OS processes have genuinely independent LockOrder maps. SQLite is
// used only for this controlled read/commit/write sequence, not to prove native
// row-lock concurrency. Neither conflicting provider events nor their production
// frequency are inferred from this synthetic, correctly signed regression.
func TestStripeDelayedFailurePreservesSuccessAcrossProcesses(t *testing.T) {
	if runStalePaymentFixtureChild(t) {
		return
	}
	db := stalePaymentFixture(t)
	configureStaleStripe(t)
	const tradeNo = "stripe-cross-process-stale"
	require.NoError(t, db.Create(&model.TopUp{UserId: 29600, Amount: 10, Money: 10, TradeNo: tradeNo, PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, Status: common.TopUpStatusPending}).Error)
	var databaseList []struct{ File string }
	require.NoError(t, db.Raw("PRAGMA database_list").Scan(&databaseList).Error)
	require.NotEmpty(t, databaseList)
	executable, err := os.Executable()
	require.NoError(t, err)
	workerBudget := 12 * time.Minute
	if deadline, ok := t.Deadline(); ok {
		workerBudget = time.Until(deadline) - 5*time.Second
	}
	require.Positive(t, workerBudget, "failure worker needs time to run and join within the fixture deadline")
	workerContext, cancelWorker := context.WithTimeout(t.Context(), workerBudget)
	defer cancelWorker()
	cmd := exec.CommandContext(workerContext, executable, "-test.run=^TestStripeFailureCrossProcessWorker$", "-test.count=1", "-test.timeout="+workerBudget.String())
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "PAYMENT_STALE_STRIPE_DSN="+databaseList[0].File)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	waited := false
	t.Cleanup(func() {
		_ = stdin.Close()
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	scanner := bufio.NewScanner(stdout)
	var output strings.Builder
	ready := false
	for scanner.Scan() {
		line := scanner.Text()
		output.WriteString(line + "\n")
		if line == "STALE_STRIPE_PENDING_READ" {
			ready = true
			break
		}
	}
	require.NoError(t, scanner.Err())
	require.True(t, ready, "child must hold the stale pending snapshot: %s", output.String())
	deliverStaleStripeWebhook(t, "checkout.session.async_payment_succeeded", tradeNo)
	before := captureStalePaymentSnapshot(t, db, tradeNo, false)
	require.Equal(t, common.TopUpStatusSuccess, before.TopUp.Status)
	require.Positive(t, before.TopUp.CompleteTime)
	require.Len(t, before.Ledgers, 1)
	require.Len(t, before.Operations, 1)
	require.Equal(t, int(10*common.QuotaPerUnit), before.Ledgers[0].Delta)
	require.Equal(t, int(10*common.QuotaPerUnit), before.User.Quota)
	require.Equal(t, int(10*common.QuotaPerUnit), before.User.QuotaPaid)
	_, err = io.WriteString(stdin, "release\n")
	require.NoError(t, err)
	require.NoError(t, stdin.Close())
	for scanner.Scan() {
		output.WriteString(scanner.Text() + "\n")
	}
	require.NoError(t, scanner.Err())
	err = cmd.Wait()
	waited = true
	require.NoError(t, err, "failure worker: %s %s", output.String(), stderr.String())
	require.NoError(t, workerContext.Err(), "failure worker must finish within its inherited deadline")
	assert.Equal(t, before, captureStalePaymentSnapshot(t, db, tradeNo, false))
}

func TestStripeFailureCrossProcessWorker(t *testing.T) {
	dsn := os.Getenv("PAYMENT_STALE_STRIPE_DSN")
	if dsn == "" {
		t.Skip("only invoked by the controlled cross-process fixture")
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	configureStaleStripe(t)
	blocked := false
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("stripe-stale-pending-read", func(tx *gorm.DB) {
		if blocked || tx.Error != nil || tx.Statement.Schema == nil || tx.Statement.Schema.Name != "TopUp" {
			return
		}
		blocked = true
		_, err := fmt.Fprintln(os.Stdout, "STALE_STRIPE_PENDING_READ")
		if err != nil {
			tx.AddError(err)
			return
		}
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			tx.AddError(err)
			return
		}
		if line != "release\n" {
			tx.AddError(errors.New("invalid cross-process barrier release"))
		}
	}))
	deliverStaleStripeWebhook(t, "checkout.session.async_payment_failed", "stripe-cross-process-stale")
	require.True(t, blocked)
}
