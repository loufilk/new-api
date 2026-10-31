package router

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type agentFinanceBrowser struct {
	identity model.AuthSessionIdentity
	bearer   string
}

func agentFinanceRouterFixture(t *testing.T) (*gin.Engine, agentFinanceBrowser, agentFinanceBrowser) {
	t.Helper()
	dsn := os.Getenv("TEST_AGENT_MIGRATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL routed financial acceptance")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.NotNil(t, net.ParseIP(parsed.Hostname()))
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback())
	admin, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	schema := fmt.Sprintf("agent_router_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)
	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	config.RuntimeParams["search_path"] = schema
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool := stdlib.OpenDB(*config)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{})
	require.NoError(t, err)
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldSecret, oldCritical := common.RedisEnabled, common.SessionSecret, common.CriticalRateLimitEnable
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.SessionSecret = "synthetic-routed-finance-secret"
	common.CriticalRateLimitEnable = false
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.RedisEnabled, common.SessionSecret, common.CriticalRateLimitEnable = oldRedis, oldSecret, oldCritical
		common.SetDatabaseTypes(oldMain, oldLogType)
		require.NoError(t, pool.Close())
		require.NoError(t, admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)).Error)
		adminPool, e := admin.DB()
		require.NoError(t, e)
		require.NoError(t, adminPool.Close())
	})
	require.NoError(t, db.AutoMigrate(append([]any{&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.TwoFA{}, &model.PasskeyCredential{}, &model.UserAccessToken{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.ReferralCommission{}, &model.Log{}, &model.AuditLog{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}}, model.AgentPlatformModels()...)...))
	browsers := make([]agentFinanceBrowser, 2)
	users := []model.User{{Username: "router-finance-root", AffCode: "router-finance-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}, {Username: "router-inviter", AffCode: "router-inviter", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}}
	for i := range users {
		require.NoError(t, db.Create(&users[i]).Error)
		identity := model.AuthSessionIdentity{UserID: users[i].Id, SessionID: "synthetic-routed-" + users[i].Username, UserAuthVersion: 1, SessionVersion: 1}
		require.NoError(t, db.Create(&model.UserSession{SID: identity.SessionID, UserID: identity.UserID, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: "synthetic", LoginMethod: "password"}).Error)
		require.NoError(t, db.Create(&model.TwoFA{UserId: identity.UserID, IsEnabled: true, Secret: "synthetic"}).Error)
		bearer, _, err := service.IssueAccessToken(identity)
		require.NoError(t, err)
		browsers[i] = agentFinanceBrowser{identity: identity, bearer: bearer}
	}
	policy := model.AgentPolicy{RequestID: "router-fixture-policy", CreatedBy: users[0].Id, Reason: "Synthetic routed acceptance", RiskPolicyApproved: true, CommissionEnabled: true, CommissionCutoverAt: common.GetTimestamp() - 10, CommissionCurrency: "USD", CommissionCurrencyExponent: 2, CommissionUserRateBPS: 500, CommissionAgentRateBPS: 500, CommissionReferralCapMinor: 3000, WithdrawalEnabled: true, WithdrawalMinimumMinor: 20, WithdrawalCalendarMonthLimit: 10, QuoteLifetimeSeconds: 300}
	require.NoError(t, model.ValidateAgentPolicy(&policy))
	require.NoError(t, db.Create(&policy).Error)
	require.NoError(t, db.Create(&model.AgentPolicyState{ID: 1, CurrentPolicyID: policy.ID, CommissionCutoverAt: policy.CommissionCutoverAt}).Error)
	buyer := model.User{Username: "router-buyer", AffCode: "router-buyer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, InviterId: users[1].Id}
	require.NoError(t, db.Create(&buyer).Error)
	order := model.TopUp{UserId: buyer.Id, TradeNo: "router-cash-topup", Amount: 100, Money: 100, PaymentProvider: model.PaymentProviderStripe, PaymentMethod: model.PaymentMethodStripe, Status: common.TopUpStatusPending}
	require.NoError(t, db.Create(&order).Error)
	facts, ok := model.NewAgentVerifiedPaymentFacts("stripe", "pi_router_synthetic", "synthetic-merchant", "live", "USD", "10000", true)
	require.True(t, ok)
	require.NoError(t, model.Recharge(order.TradeNo, "synthetic-customer", "", facts))
	_, err = model.ProcessAgentCommissionOutbox(context.Background(), 100)
	require.NoError(t, err)
	_, err = model.MatureAgentCommissionAccounts(context.Background(), 100)
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	require.NoError(t, RegisterAgentPlatformRoutes(extension.RouteScopes{Engine: engine, API: engine.Group("/api")}))
	return engine, browsers[0], browsers[1]
}

func agentFinanceRequest(t *testing.T, engine *gin.Engine, browser agentFinanceBrowser, path string, input any, proof string, target any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := common.Marshal(input)
	require.NoError(t, err)
	request := httptest.NewRequest("POST", "https://console.example/api/agent-platform"+path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+browser.bearer)
	request.Header.Set("Origin", "https://console.example")
	request.Header.Set("X-Security-Proof", proof)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Success bool              `json:"success"`
		Data    common.RawMessage `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
	require.True(t, envelope.Success, response.Body.String())
	assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	if target != nil {
		require.NoError(t, common.Unmarshal(envelope.Data, target))
	}
	return response
}

// TOTP/Passkey ceremony is separately exercised through the verification HTTP
// API. This helper issues the identical one-use model proof after the real
// browser-authenticated prepare endpoint, to isolate router/action wiring.
func agentFinanceSubmit(t *testing.T, engine *gin.Engine, browser agentFinanceBrowser, action, requestID, path string, input any, target any) {
	t.Helper()
	var prepared model.AgentPreparedOperation
	agentFinanceRequest(t, engine, browser, "/operations/prepare", map[string]any{"action": action, "request_id": requestID, "payload": input}, "", &prepared)
	assert.Equal(t, action, prepared.VerificationContext.Action)
	assert.Equal(t, requestID, prepared.VerificationContext.RequestID)
	hash, err := model.AgentTransactionContextHash(prepared.VerificationContext)
	require.NoError(t, err)
	proof, _, err := model.IssueAgentTransactionProof(browser.identity, hash, "2fa")
	require.NoError(t, err)
	agentFinanceRequest(t, engine, browser, path, input, proof, target)
}

func TestAgentPlatformRoutedWithdrawalApprovalUnknownFailureAndRejectPostgres(t *testing.T) {
	engine, finance, owner := agentFinanceRouterFixture(t)
	recipientInput := model.AgentCommissionRecipientRequest{RequestID: "router-recipient-create", Label: "Synthetic beneficiary", RecipientReference: "synthetic-offline-bank-reference"}
	var recipient model.AgentCommissionRecipient
	agentFinanceSubmit(t, engine, owner, "recipient.change", recipientInput.RequestID, "/commission-recipients", recipientInput, &recipient)
	requestWithdrawal := func(requestID string) model.AgentCommissionWithdrawal {
		quoteInput := model.AgentCommissionQuoteRequest{RequestID: requestID, Target: "withdrawal", Currency: "USD", AmountMinor: 100, RecipientID: recipient.ID, RecipientVersion: recipient.Version}
		var quote model.AgentCommissionQuote
		agentFinanceRequest(t, engine, owner, "/withdrawals/quote", quoteInput, "", &quote)
		input := model.AgentCommissionExecuteRequest{RequestID: requestID, QuoteID: quote.ID}
		var withdrawal model.AgentCommissionWithdrawal
		agentFinanceSubmit(t, engine, owner, "commission.withdraw.request", requestID, "/withdrawals", input, &withdrawal)
		return withdrawal
	}
	withdrawal := requestWithdrawal("router-withdraw-first")
	transition := func(suffix, action, decision, status string, failed bool) {
		input := model.AgentCommissionWithdrawalAction{RequestID: "router-" + suffix + "-" + status, WithdrawalID: withdrawal.ID, Version: withdrawal.Version, Decision: decision, Status: status, Reason: "Synthetic independent review", FailureConfirmedUnpaid: failed}
		if suffix == "resolve" {
			input.EvidenceReference = "synthetic-observation-" + status
		}
		agentFinanceSubmit(t, engine, finance, action, input.RequestID, fmt.Sprintf("/finance/withdrawals/%d/%s", withdrawal.ID, suffix), input, &withdrawal)
	}
	transition("approve", "commission.withdraw.approve", "approve", "", false)
	assert.Equal(t, "approved", withdrawal.Status)
	transition("start", "commission.withdraw.submit", "", "", false)
	assert.Equal(t, "paying", withdrawal.Status)
	transition("resolve", "commission.withdraw.resolve", "", "unknown", false)
	assert.Equal(t, "unknown", withdrawal.Status)
	var account model.AgentCommissionAccount
	require.NoError(t, model.DB.Where("user_id = ?", owner.identity.UserID).First(&account).Error)
	assert.EqualValues(t, 100, account.HeldMinor)
	assert.EqualValues(t, 400, account.AvailableMinor)
	transition("resolve", "commission.withdraw.resolve", "", "failed", true)
	assert.Equal(t, "failed", withdrawal.Status)
	require.NoError(t, model.DB.First(&account, account.ID).Error)
	assert.Zero(t, account.HeldMinor)
	assert.EqualValues(t, 500, account.AvailableMinor)
	withdrawal = requestWithdrawal("router-withdraw-second")
	transition("reject", "commission.withdraw.reject", "reject", "", false)
	assert.Equal(t, "rejected", withdrawal.Status)
	require.NoError(t, model.DB.First(&account, account.ID).Error)
	assert.Zero(t, account.HeldMinor)
	assert.EqualValues(t, 500, account.AvailableMinor)
	var receipts int64
	require.NoError(t, model.DB.Model(&model.AgentOutboundPaymentReceipt{}).Count(&receipts).Error)
	assert.Zero(t, receipts, "UNKNOWN/failed/rejected never invent a paid receipt")
}
