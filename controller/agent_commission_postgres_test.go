package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81/webhook"
)

type commissionHTTPFixture struct {
	root, owner, buyer     model.User
	rootIdentity, identity model.AuthSessionIdentity
}

func setupCommissionHTTPPostgres(t *testing.T) commissionHTTPFixture {
	t.Helper()
	dsn := os.Getenv("TEST_AGENT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("opt-in PostgreSQL fixture; acceptance runner must configure TEST_AGENT_POSTGRES_DSN and require zero skips")
	}
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldSecret := common.RedisEnabled, common.SessionSecret
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.SessionSecret = "synthetic-commission-http-not-a-secret"
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.RedisEnabled = oldRedis
		common.SessionSecret = oldSecret
		common.SetDatabaseTypes(oldMain, oldLogType)
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.ReferralCommission{}, &model.AuthFlow{}, &model.UserSession{}, &model.UserAccessToken{}, &model.TwoFA{}, &model.PasskeyCredential{}))
	require.NoError(t, db.AutoMigrate(model.AgentPlatformModels()...))
	f := commissionHTTPFixture{}
	f.root = model.User{Username: "http-finance-root", AffCode: "http-finance-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	f.owner = model.User{Username: "http-inviter", AffCode: "http-inviter", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&f.root).Error)
	require.NoError(t, db.Create(&f.owner).Error)
	f.buyer = model.User{Username: "http-buyer", AffCode: "http-buyer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, InviterId: f.owner.Id}
	require.NoError(t, db.Create(&f.buyer).Error)
	for i, user := range []model.User{f.root, f.owner} {
		session := model.UserSession{SID: "synthetic-http-" + user.Username, UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: "synthetic", LoginMethod: "password"}
		require.NoError(t, db.Create(&session).Error)
		require.NoError(t, db.Create(&model.TwoFA{UserId: user.Id, IsEnabled: true, Secret: "synthetic"}).Error)
		identity := model.AuthSessionIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}
		if i == 0 {
			f.rootIdentity = identity
		} else {
			f.identity = identity
		}
	}
	policy := model.AgentPolicy{RequestID: "synthetic-http-policy", CreatedBy: f.root.Id, Reason: "Synthetic acceptance policy", RiskPolicyApproved: true, CommissionEnabled: true, CommissionCutoverAt: common.GetTimestamp() - 10, CommissionCurrency: "USD", CommissionCurrencyExponent: 2, CommissionUserRateBPS: 500, CommissionAgentRateBPS: 500, CommissionReferralCapMinor: 3000, CommissionReserveBPS: 2000, WithdrawalEnabled: true, CommissionConversionEnabled: true, WithdrawalMinimumMinor: 20, WithdrawalCalendarMonthLimit: 10, WithdrawalFeeBPS: 300, ConversionQuotaNumerator: 5000, ConversionQuotaDenominator: 1, QuoteLifetimeSeconds: 300}
	require.NoError(t, db.Create(&policy).Error)
	require.NoError(t, db.Create(&model.AgentPolicyState{ID: 1, CurrentPolicyID: policy.ID, CommissionCutoverAt: policy.CommissionCutoverAt}).Error)
	order := model.TopUp{UserId: f.buyer.Id, TradeNo: "http-topup", Amount: 100, Money: 100, PaymentProvider: model.PaymentProviderStripe, PaymentMethod: model.PaymentMethodStripe, Status: common.TopUpStatusPending}
	require.NoError(t, db.Create(&order).Error)
	facts, ok := model.NewAgentVerifiedPaymentFacts("stripe", "pi_http_fixture", "synthetic-http-merchant", "live", "USD", "10000", true)
	require.True(t, ok)
	require.NoError(t, model.Recharge(order.TradeNo, "synthetic-customer", "", facts))
	_, err := model.ProcessAgentCommissionOutbox(context.Background(), 10)
	require.NoError(t, err)
	_, err = model.MatureAgentCommissionAccounts(context.Background(), 10)
	require.NoError(t, err)
	return f
}

func commissionHTTPRequest(t *testing.T, identity model.AuthSessionIdentity, method, path string, input any, proof string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	var err error
	if input != nil {
		data, err = common.Marshal(input)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if proof != "" {
		req.Header.Set("X-Security-Proof", proof)
	}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = req
	c.Set("id", identity.UserID)
	c.Set("session_id", identity.SessionID)
	c.Set("auth_version", identity.UserAuthVersion)
	c.Set("session_version", identity.SessionVersion)
	handler(c)
	return response
}
func commissionHTTPProof(t *testing.T, identity model.AuthSessionIdentity, action, request string, input any) string {
	t.Helper()
	raw, err := common.Marshal(input)
	require.NoError(t, err)
	prepared, err := model.PrepareAgentCommissionOperation(identity, action, request, raw)
	require.NoError(t, err)
	hash, err := model.AgentTransactionContextHash(prepared.VerificationContext)
	require.NoError(t, err)
	proof, _, err := model.IssueAgentTransactionProof(identity, hash, "2fa")
	require.NoError(t, err)
	return proof
}
func commissionHTTPData(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}
	envelope.Data = target
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
	require.True(t, envelope.Success, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

func TestAgentCommissionHTTPBrowserProofBindingIsolationAndReplayPostgres(t *testing.T) {
	f := setupCommissionHTTPPostgres(t)
	recipient := model.AgentCommissionRecipientRequest{RequestID: "http-recipient-create", Label: "Synthetic offline beneficiary", RecipientReference: "synthetic-offline-reference"}
	response := commissionHTTPRequest(t, f.identity, "POST", "/", recipient, "", ChangeAgentCommissionRecipient)
	assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	pat := f.identity
	pat.SessionID = model.AccessTokenSessionID(99)
	response = commissionHTTPRequest(t, pat, "POST", "/", recipient, "", ChangeAgentCommissionRecipient)
	assert.Equal(t, http.StatusForbidden, response.Code)
	proof := commissionHTTPProof(t, f.identity, "recipient.change", recipient.RequestID, recipient)
	tampered := recipient
	tampered.RecipientReference = "changed-offline-reference"
	response = commissionHTTPRequest(t, f.identity, "POST", "/", tampered, proof, ChangeAgentCommissionRecipient)
	assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	var row model.AgentCommissionRecipient
	response = commissionHTTPRequest(t, f.identity, "POST", "/", recipient, proof, ChangeAgentCommissionRecipient)
	commissionHTTPData(t, response, &row)
	require.NotEmpty(t, row.ID)
	assert.NotContains(t, response.Body.String(), recipient.RecipientReference, "raw financial destination must not be returned")
	response = commissionHTTPRequest(t, f.identity, "POST", "/", recipient, "", ChangeAgentCommissionRecipient)
	var replay model.AgentCommissionRecipient
	commissionHTTPData(t, response, &replay)
	assert.Equal(t, row.ID, replay.ID)
	response = commissionHTTPRequest(t, f.identity, "POST", "/", tampered, proof, ChangeAgentCommissionRecipient)
	assert.Equal(t, http.StatusConflict, response.Code)
	response = commissionHTTPRequest(t, f.identity, "GET", fmt.Sprintf("/?user_id=%d", f.buyer.Id), nil, "", GetAgentCommissionSummary)
	assert.Equal(t, http.StatusForbidden, response.Code)
	injected := map[string]any{"request_id": "http-owner-injection", "label": "injected", "recipient_reference": "synthetic", "user_id": f.buyer.Id}
	response = commissionHTTPRequest(t, f.identity, "POST", "/", injected, proof, ChangeAgentCommissionRecipient)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	status, err := model.FindAgentCommissionOperation(f.identity, recipient.RequestID)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, "completed", status.Status)
	foreign, err := model.FindAgentCommissionOperation(f.rootIdentity, recipient.RequestID)
	require.NoError(t, err)
	assert.Nil(t, foreign, "request recovery is actor scoped even for administrators")
}

func TestAgentCommissionHTTPMoneyStringsConversionAndRevocationPostgres(t *testing.T) {
	f := setupCommissionHTTPPostgres(t)
	query := model.AgentCommissionQuoteRequest{RequestID: "http-convert-wallet", Target: "wallet", Currency: "USD", AmountMinor: 400}
	response := commissionHTTPRequest(t, f.identity, "POST", "/", query, "", CreateAgentCommissionConversionQuote)
	var quote model.AgentCommissionQuote
	commissionHTTPData(t, response, &quote)
	assert.Contains(t, response.Body.String(), `"amount_minor":"400"`)
	assert.EqualValues(t, 2, quote.CurrencyExponent)
	execute := model.AgentCommissionExecuteRequest{RequestID: query.RequestID, QuoteID: quote.ID}
	proof := commissionHTTPProof(t, f.identity, quote.Action, execute.RequestID, execute)
	response = commissionHTTPRequest(t, f.identity, "POST", "/", execute, "", ExecuteAgentCommissionConversion)
	assert.Equal(t, http.StatusForbidden, response.Code)
	// Numeric JSON (rather than decimal string) is not accepted as cash amount.
	response = commissionHTTPRequest(t, f.identity, "POST", "/", map[string]any{"request_id": "http-numeric-minor", "target": "wallet", "currency": "USD", "amount_minor": 1}, "", CreateAgentCommissionConversionQuote)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	var conversion model.AgentCommissionConversion
	response = commissionHTTPRequest(t, f.identity, "POST", "/", execute, proof, ExecuteAgentCommissionConversion)
	commissionHTTPData(t, response, &conversion)
	assert.EqualValues(t, 2000000, conversion.Quota)
	response = commissionHTTPRequest(t, f.identity, "POST", "/", execute, "", ExecuteAgentCommissionConversion)
	commissionHTTPData(t, response, &conversion)
	var owner model.User
	require.NoError(t, model.DB.First(&owner, f.owner.Id).Error)
	assert.Equal(t, 2000000, owner.QuotaReward)
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", f.identity.SessionID).Update("revoked_at", common.GetTimestamp()).Error)
	response = commissionHTTPRequest(t, f.identity, "POST", "/", execute, proof, ExecuteAgentCommissionConversion)
	assert.Equal(t, http.StatusForbidden, response.Code, "committed replay is still a private authenticated read")
}

func TestAgentCommissionHTTPSignedStripeRefundDurabilityAndNoInlineReversalPostgres(t *testing.T) {
	f := setupCommissionHTTPPostgres(t)
	confirmPaymentComplianceForTest(t)
	oldKey, oldSecret, oldPrice := setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId
	setting.StripeApiSecret = "sk_test_synthetic_commission"
	setting.StripeWebhookSecret = "whsec_synthetic_commission"
	setting.StripePriceId = "price_synthetic_commission"
	t.Cleanup(func() {
		setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = oldKey, oldSecret, oldPrice
	})
	payload := []byte(`{"id":"evt_http_refund","object":"event","type":"charge.refunded","account":"synthetic-http-merchant","livemode":true,"data":{"object":{"id":"ch_synthetic","object":"charge","payment_intent":"pi_http_fixture","currency":"usd","amount":10000,"amount_refunded":5000,"livemode":true}}}`)
	request := func(signed bool) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Request = httptest.NewRequest("POST", "/api/stripe/webhook", bytes.NewReader(payload))
		if signed {
			signature := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: setting.StripeWebhookSecret})
			c.Request.Header.Set("Stripe-Signature", signature.Header)
		}
		StripeWebhook(c)
		return r
	}
	response := request(false)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	var count int64
	require.NoError(t, model.DB.Model(&model.AgentCommissionOutbox{}).Where("kind = ?", "provider_refund").Count(&count).Error)
	assert.Zero(t, count)
	response = request(true)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = request(true)
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, model.DB.Model(&model.AgentCommissionOutbox{}).Where("kind = ?", "provider_refund").Count(&count).Error)
	assert.EqualValues(t, 1, count)
	var account model.AgentCommissionAccount
	require.NoError(t, model.DB.Where("user_id = ?", f.owner.Id).First(&account).Error)
	assert.Zero(t, account.ReversedMinor, "callback acknowledges durable receipt, not commission worker completion")
	for range 2 {
		_, err := model.ProcessAgentCommissionOutbox(context.Background(), 10)
		require.NoError(t, err)
	}
	require.NoError(t, model.DB.First(&account, account.ID).Error)
	assert.EqualValues(t, 250, account.ReversedMinor)
}

func TestAgentCommissionHTTPLegacyShortDottedRequestRecoveryPostgres(t *testing.T) {
	f := setupCommissionHTTPPostgres(t)
	oldUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldUnit })
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.owner.Id).Update("aff_quota", 500000).Error)
	input := model.AgentLegacyCommissionRequest{RequestID: "x.1", Quota: 500000}
	data, err := common.Marshal(input)
	require.NoError(t, err)
	prepared, err := model.PrepareAgentCommissionOperation(f.identity, "commission.convert.legacy", input.RequestID, data)
	require.NoError(t, err)
	proof := commissionHTTPProof(t, f.identity, "commission.convert.legacy", input.RequestID, input)
	auth, err := model.PrepareAgentTransactionAuthorization(proof, f.identity, prepared.VerificationContext)
	require.NoError(t, err)
	_, err = model.TransferAffQuota(f.owner.Id, input.Quota, input.RequestID, auth)
	require.NoError(t, err)
	handler := func(c *gin.Context) {
		c.Params = gin.Params{{Key: "request_id", Value: input.RequestID}}
		GetAgentPlatformOperation(c)
	}
	response := commissionHTTPRequest(t, f.identity, "GET", "/api/agent-platform/operations/x.1", nil, "", handler)
	var status model.AgentOperationStatus
	commissionHTTPData(t, response, &status)
	assert.Equal(t, "completed", status.Status)
	assert.Equal(t, "commission.convert.legacy", status.Action)
	response = commissionHTTPRequest(t, f.rootIdentity, "GET", "/api/agent-platform/operations/x.1", nil, "", handler)
	commissionHTTPData(t, response, &status)
	assert.Equal(t, "not_observed", status.Status)
}
