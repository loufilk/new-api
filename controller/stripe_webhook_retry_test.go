package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81/webhook"
)

func TestStripePaidWebhookRetriesUntilCommittedAndAcknowledgesReplay(t *testing.T) {
	for _, kind := range []string{"topup", "subscription"} {
		for _, eventType := range []string{"checkout.session.completed", "checkout.session.async_payment_succeeded"} {
			t.Run(kind+"/"+eventType, func(t *testing.T) {
				db := withPaymentBindingDB(t)
				require.NoError(t, db.AutoMigrate(&model.User{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.Log{}, &model.SubscriptionPlan{}, &model.UserSubscription{}))
				confirmPaymentComplianceForTest(t)
				oldAPI, oldSecret, oldPrice := setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId
				oldRedis, oldLog := common.RedisEnabled, model.LOG_DB
				t.Cleanup(func() {
					setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = oldAPI, oldSecret, oldPrice
					common.RedisEnabled = oldRedis
					model.LOG_DB = oldLog
					model.InvalidateSubscriptionPlanCache(29403)
				})
				setting.StripeApiSecret = "sk_test_synthetic_retry"
				setting.StripeWebhookSecret = "whsec_synthetic_retry"
				setting.StripePriceId = "price_synthetic_retry"
				common.RedisEnabled = false
				model.LOG_DB = db
				require.NoError(t, db.Create(&model.User{Id: 29401, Username: "stripe-retry", Status: common.UserStatusEnabled}).Error)
				const tradeNo = "stripe-retry-paid"
				failTable := "balance_ledger_operations"
				if kind == "topup" {
					require.NoError(t, db.Create(&model.TopUp{UserId: 29401, Amount: 10, Money: 10, TradeNo: tradeNo, PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, Status: common.TopUpStatusPending}).Error)
				} else {
					model.InvalidateSubscriptionPlanCache(29403)
					require.NoError(t, db.Create(&model.SubscriptionPlan{Id: 29403, Title: "Stripe retry", PriceAmount: 10, DurationUnit: model.SubscriptionDurationDay, DurationValue: 1, TotalAmount: 1000, Enabled: true}).Error)
					require.NoError(t, db.Create(&model.SubscriptionOrder{UserId: 29401, PlanId: 29403, Money: 10, TradeNo: tradeNo, PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, Status: common.TopUpStatusPending}).Error)
					failTable = "user_subscriptions"
				}
				require.NoError(t, db.Exec(`CREATE TRIGGER stripe_retry_failure BEFORE INSERT ON `+failTable+` BEGIN SELECT RAISE(ABORT, 'transient fulfillment failure'); END`).Error)
				payload, err := common.Marshal(map[string]any{"id": "evt_retry", "object": "event", "type": eventType, "data": map[string]any{"object": map[string]any{"id": "cs_retry", "object": "checkout.session", "client_reference_id": tradeNo, "status": "complete", "payment_status": "paid", "customer": "cus_retry", "amount_total": 1000, "currency": "usd"}}})
				require.NoError(t, err)
				deliver := func() int {
					signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: setting.StripeWebhookSecret})
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/api/stripe/webhook", strings.NewReader(string(payload)))
					c.Request.Header.Set("Stripe-Signature", signed.Header)
					StripeWebhook(c)
					return recorder.Code
				}
				assert.Equal(t, http.StatusInternalServerError, deliver())
				var status string
				table := "top_ups"
				if kind == "subscription" {
					table = "subscription_orders"
				}
				require.NoError(t, db.Table(table).Select("status").Where("trade_no = ?", tradeNo).Scan(&status).Error)
				assert.Equal(t, common.TopUpStatusPending, status)
				require.NoError(t, db.Exec("DROP TRIGGER stripe_retry_failure").Error)
				assert.Equal(t, http.StatusOK, deliver())
				assert.Equal(t, http.StatusOK, deliver())
				require.NoError(t, db.Table(table).Select("status").Where("trade_no = ?", tradeNo).Scan(&status).Error)
				assert.Equal(t, common.TopUpStatusSuccess, status)
				var user model.User
				require.NoError(t, db.First(&user, 29401).Error)
				var ledgerRows, subscriptions int64
				require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&ledgerRows).Error)
				require.NoError(t, db.Model(&model.UserSubscription{}).Count(&subscriptions).Error)
				if kind == "topup" {
					assert.Equal(t, int(10*common.QuotaPerUnit), user.QuotaPaid)
					assert.EqualValues(t, 1, ledgerRows)
					assert.Zero(t, subscriptions)
				} else {
					assert.Zero(t, user.QuotaPaid)
					assert.Zero(t, ledgerRows)
					assert.EqualValues(t, 1, subscriptions)
				}
			})
		}
	}
}
