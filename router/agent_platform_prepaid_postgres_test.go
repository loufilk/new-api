package router

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type agentPrepaidRouterFixture struct {
	engine                                    *gin.Engine
	finance, reviewer, owner, other, customer agentFinanceBrowser
}

func newAgentPrepaidRouterFixture(t *testing.T) agentPrepaidRouterFixture {
	t.Helper()
	engine, finance, _, _ := agentFinanceRouterFixture(t)
	// Match host startup's dialect-specific quoted columns. The finance-only
	// fixture does not call initialization because it never queries a code key.
	// Empty log DSN explicitly reuses this disposable schema, never an external DB.
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	var version string
	require.NoError(t, model.DB.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, "180006", version)
	require.NoError(t, model.DB.AutoMigrate(&model.Redemption{}, &model.AgentRedemptionAccount{}, &model.AgentRedemptionLedger{}, &model.AgentRedemptionOperation{}))
	f := agentPrepaidRouterFixture{engine: engine, finance: finance}
	for _, seed := range []struct {
		name    string
		role    int
		profile bool
		target  *agentFinanceBrowser
	}{
		{"prepaid-owner", common.RoleCommonUser, true, &f.owner},
		{"prepaid-other", common.RoleCommonUser, true, &f.other},
		{"prepaid-reviewer", common.RoleAdminUser, false, &f.reviewer},
		{"prepaid-customer", common.RoleCommonUser, false, &f.customer},
	} {
		user := model.User{Username: seed.name, AffCode: seed.name, Role: seed.role, Status: common.UserStatusEnabled, AuthVersion: 1}
		require.NoError(t, model.DB.Create(&user).Error)
		identity := model.AuthSessionIdentity{UserID: user.Id, SessionID: "synthetic-router-" + seed.name, UserAuthVersion: 1, SessionVersion: 1}
		require.NoError(t, model.DB.Create(&model.UserSession{SID: identity.SessionID, UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: "synthetic", LoginMethod: "password"}).Error)
		require.NoError(t, model.DB.Create(&model.TwoFA{UserId: user.Id, IsEnabled: true, Secret: "synthetic-noncredential"}).Error)
		if seed.profile {
			require.NoError(t, model.DB.Create(&model.AgentProfile{UserID: user.Id, Status: model.AgentProfileActive, Version: 1, Reason: "Synthetic route acceptance"}).Error)
		}
		bearer, _, err := service.IssueAccessToken(identity)
		require.NoError(t, err)
		*seed.target = agentFinanceBrowser{identity: identity, bearer: bearer}
	}
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.customer.identity.UserID).Update("inviter_id", f.other.identity.UserID).Error)
	policy := model.AgentPolicy{RequestID: "router-prepaid-policy", CreatedBy: finance.identity.UserID, Reason: "Synthetic prepaid route acceptance", RiskPolicyApproved: true, PurchaseEnabled: true, DeliveryEnabled: true, PurchaseRefundEnabled: true, PurchaseRefundTermsApproved: true, ProcurementSelfUseEnabled: true, PurchaseCurrency: "USD", PurchaseCurrencyExponent: 2, PurchaseRateBPS: 9000, PurchaseQuotaNumerator: 10, PurchaseQuotaDenominator: 1, PurchaseMinimumQuota: 1, PurchaseMaximumQuota: common.MaxWalletQuota, QuoteLifetimeSeconds: 600, RefundFeeBPS: 300, CommissionCurrency: "USD", CommissionCurrencyExponent: 2}
	require.NoError(t, model.ValidateAgentPolicy(&policy))
	require.NoError(t, model.DB.Create(&policy).Error)
	require.NoError(t, model.DB.Model(&model.AgentPolicyState{}).Where("id = ?", 1).Update("current_policy_id", policy.ID).Error)
	return f
}

// These requests enter the registered router with real browser JWTs, not
// pre-populated Gin identity. Error paths must be unsuccessful JSON as well as
// the expected HTTP status. Positive POSTs reuse the finance route helper.
func agentPrepaidRoute(t *testing.T, f agentPrepaidRouterFixture, browser agentFinanceBrowser, method, path string, input any, proof string, status int, target any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := common.Marshal(input)
	require.NoError(t, err)
	request := httptest.NewRequest(method, "https://console.example/api/agent-platform"+path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	if browser.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+browser.bearer)
	}
	request.Header.Set("Origin", "https://console.example")
	request.Header.Set("X-Security-Proof", proof)
	response := httptest.NewRecorder()
	f.engine.ServeHTTP(response, request)
	require.Equal(t, status, response.Code, response.Body.String())
	var envelope struct {
		Success bool              `json:"success"`
		Data    common.RawMessage `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, status == http.StatusOK, envelope.Success, response.Body.String())
	assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	if target != nil {
		require.NoError(t, common.Unmarshal(envelope.Data, target))
	}
	return response
}

// As in agentFinanceSubmit, actual TOTP/Passkey ceremonies are separately
// tested. This issues the same opaque one-use proof only after routed prepare.
func agentPrepaidProof(t *testing.T, f agentPrepaidRouterFixture, browser agentFinanceBrowser, action, requestID string, input any) string {
	t.Helper()
	var prepared model.AgentPreparedOperation
	agentFinanceRequest(t, f.engine, browser, "/operations/prepare", map[string]any{"action": action, "request_id": requestID, "payload": input}, "", &prepared)
	require.Equal(t, action, prepared.VerificationContext.Action)
	require.Equal(t, requestID, prepared.VerificationContext.RequestID)
	hash, err := model.AgentTransactionContextHash(prepared.VerificationContext)
	require.NoError(t, err)
	proof, _, err := model.IssueAgentTransactionProof(browser.identity, hash, "2fa")
	require.NoError(t, err)
	return proof
}

func agentPrepaidCreditThroughRouter(t *testing.T, f agentPrepaidRouterFixture) model.AgentPurchaseOrder {
	t.Helper()
	var quote model.AgentPurchaseQuote
	agentFinanceRequest(t, f.engine, f.owner, "/purchases/quotes", model.AgentPurchaseQuoteRequest{RequestID: "router-prepaid-quote", Quota: 1000}, "", &quote)
	assert.Equal(t, "90", quote.PaidMinor)
	var order model.AgentPurchaseOrder
	agentFinanceRequest(t, f.engine, f.owner, "/purchases", model.AgentPurchaseCreateRequest{RequestID: "router-prepaid-order", QuoteID: quote.ID}, "", &order)
	evidence := model.AgentPurchaseCommand{RequestID: "router-prepaid-evidence", OrderID: order.ID, Version: order.Version, EvidenceReference: "synthetic-buyer-document"}
	agentFinanceRequest(t, f.engine, f.owner, fmt.Sprintf("/purchases/%d/evidence", order.ID), evidence, "", &order)
	payment := model.AgentPurchaseCommand{RequestID: "router-prepaid-verify", OrderID: order.ID, Version: order.Version, EvidenceReference: "synthetic-bank-statement", Provider: "bank_transfer", MerchantScope: "synthetic-merchant", Environment: "live", ExternalReference: "synthetic-prepaid-transaction", PaidMinor: order.PaidMinor, Currency: order.Currency, PaidAt: common.GetTimestamp(), PayerReference: "dossier:synthetic-routed-payer"}
	path := fmt.Sprintf("/finance/purchases/%d/verify", order.ID)
	agentPrepaidRoute(t, f, f.owner, http.MethodPost, path, payment, "", http.StatusForbidden, nil)
	agentPrepaidRoute(t, f, f.finance, http.MethodPost, path, payment, "", http.StatusForbidden, nil)
	agentFinanceSubmit(t, f.engine, f.finance, "purchase.payment.record", payment.RequestID, path, payment, &order)
	var batches int64
	require.NoError(t, model.DB.Model(&model.AgentFundBatch{}).Where("agent_user_id = ?", f.owner.identity.UserID).Count(&batches).Error)
	assert.Zero(t, batches, "recorded evidence is not yet spendable procurement")
	review := model.AgentPurchaseCommand{RequestID: "router-prepaid-review", OrderID: order.ID, Version: order.Version, PayerReference: payment.PayerReference}
	path = fmt.Sprintf("/finance/purchases/%d/review", order.ID)
	proof := agentPrepaidProof(t, f, f.finance, "purchase.payment.confirm", review.RequestID, review)
	agentPrepaidRoute(t, f, f.finance, http.MethodPost, path, review, proof, http.StatusForbidden, nil)
	agentFinanceSubmit(t, f.engine, f.reviewer, "purchase.payment.confirm", review.RequestID, path, review, &order)
	assert.Equal(t, "credited", order.Status)
	assert.Equal(t, f.finance.identity.UserID, order.VerifiedBy)
	assert.Equal(t, f.reviewer.identity.UserID, order.ReviewedBy)
	require.Positive(t, order.FundBatchID)
	// Exact request replay resolves its durable signature without new MFA or
	// another source credit, even though the order version already advanced.
	agentFinanceRequest(t, f.engine, f.reviewer, path, review, "", &order)
	require.NoError(t, model.DB.Model(&model.AgentFundBatch{}).Where("agent_user_id = ?", f.owner.identity.UserID).Count(&batches).Error)
	assert.EqualValues(t, 1, batches)
	var owner model.User
	require.NoError(t, model.DB.First(&owner, f.owner.identity.UserID).Error)
	assert.Zero(t, owner.Quota, "procurement credit does not silently top up personal consumption")
	return order
}

func assertAgentPrepaidBudget(t *testing.T, owner, available, reserved, spent int) {
	t.Helper()
	var account model.AgentRedemptionAccount
	require.NoError(t, model.DB.Where("agent_user_id = ?", owner).First(&account).Error)
	assert.Equal(t, []int{available, reserved, spent, 1000}, []int{account.AvailableQuota, account.ReservedQuota, account.SpentQuota, account.AllocatedQuota})
}

func TestAgentPlatformRoutedPurchaseDeliveryCustomerBindingPostgres(t *testing.T) {
	f := newAgentPrepaidRouterFixture(t)
	order := agentPrepaidCreditThroughRouter(t, f)
	var batches struct {
		Items []model.AgentFundBatchView `json:"items"`
		Total int                        `json:"total"`
	}
	agentPrepaidRoute(t, f, f.owner, http.MethodGet, "/fund-batches", nil, "", http.StatusOK, &batches)
	require.Len(t, batches.Items, 1)
	assert.Equal(t, order.FundBatchID, batches.Items[0].ID)
	assert.Equal(t, model.AgentFundPurchase, batches.Items[0].SourceKind)
	assert.Equal(t, model.AccountPaid, batches.Items[0].WalletAccount)
	assert.Equal(t, "90", batches.Items[0].OriginalPaidMinor)
	assert.Equal(t, 1000, batches.Items[0].AvailableQuota)
	agentPrepaidRoute(t, f, f.other, http.MethodGet, fmt.Sprintf("/fund-batches?agent_user_id=%d", f.owner.identity.UserID), nil, "", http.StatusOK, &batches)
	assert.Zero(t, batches.Total, "an agent cannot select another agent's source ledger")
	create := model.AgentDeliveryCommand{RequestID: "router-prepaid-delivery", Name: "Synthetic delivery", Quota: 400, ExpiresAt: common.GetTimestamp() + 3600}
	agentPrepaidRoute(t, f, f.owner, http.MethodPost, "/deliveries", create, "", http.StatusForbidden, nil)
	var delivery model.AgentDelivery
	agentFinanceSubmit(t, f.engine, f.owner, "delivery.create", create.RequestID, "/deliveries", create, &delivery)
	require.Len(t, delivery.ClaimCode, 32)
	assertAgentPrepaidBudget(t, f.owner.identity.UserID, 600, 400, 0)
	claim := model.AgentDeliveryCommand{RequestID: "router-prepaid-claim", ClaimCode: delivery.ClaimCode}
	agentPrepaidRoute(t, f, agentFinanceBrowser{}, http.MethodPost, "/deliveries/claim", claim, "", http.StatusUnauthorized, nil)
	agentPrepaidRoute(t, f, f.owner, http.MethodPost, "/deliveries/claim", claim, "", http.StatusBadRequest, nil)
	var claimed model.AgentDelivery
	response := agentFinanceRequest(t, f.engine, f.customer, "/deliveries/claim", claim, "", &claimed)
	assert.NotContains(t, response.Body.String(), delivery.ClaimCode)
	assert.Equal(t, "claimed", claimed.State)
	assert.Equal(t, f.customer.identity.UserID, claimed.CustomerUserID)
	var received struct {
		Items []model.AgentDelivery `json:"items"`
	}
	response = agentPrepaidRoute(t, f, f.customer, http.MethodGet, "/deliveries/received", nil, "", http.StatusOK, &received)
	require.Len(t, received.Items, 1)
	assert.Equal(t, claimed.ID, received.Items[0].ID)
	assert.NotContains(t, response.Body.String(), delivery.ClaimCode)
	var customer model.User
	require.NoError(t, model.DB.First(&customer, f.customer.identity.UserID).Error)
	assert.Zero(t, customer.Quota, "claim is ownership binding, not wallet credit")
	assert.Equal(t, f.other.identity.UserID, customer.InviterId, "delivery must not overwrite invitation attribution")
	confirm := model.AgentDeliveryCommand{RequestID: "router-prepaid-confirm", DeliveryID: claimed.ID, Version: claimed.Version}
	path := fmt.Sprintf("/deliveries/%d/confirm", claimed.ID)
	agentPrepaidRoute(t, f, f.other, http.MethodPost, path, confirm, "", http.StatusNotFound, nil)
	// Existing customer rights survive the seller's later exit/sales freeze.
	require.NoError(t, model.DB.Model(&model.AgentProfile{}).Where("user_id = ?", f.owner.identity.UserID).Updates(map[string]any{"status": model.AgentProfileExited, "sales_frozen": true, "version": 2}).Error)
	for range 2 {
		agentFinanceRequest(t, f.engine, f.customer, path, confirm, "", &claimed)
		assert.Equal(t, "redeemed", claimed.State)
	}
	require.NoError(t, model.DB.First(&customer, f.customer.identity.UserID).Error)
	assert.Equal(t, []int{400, 400, 0, 0}, []int{customer.Quota, customer.QuotaPaid, customer.QuotaBonus, customer.QuotaReward})
	assert.Equal(t, f.other.identity.UserID, customer.InviterId)
	assertAgentPrepaidBudget(t, f.owner.identity.UserID, 600, 0, 400)
	var reconciliation model.AgentFundReconciliationPage
	agentPrepaidRoute(t, f, f.finance, http.MethodGet, fmt.Sprintf("/finance/funds/reconciliation?agent_user_id=%d", f.owner.identity.UserID), nil, "", http.StatusOK, &reconciliation)
	assert.Empty(t, reconciliation.Anomalies)
	assert.Equal(t, []int{f.owner.identity.UserID}, reconciliation.CheckedAgentIDs)
}

func TestAgentPlatformRoutedPurchaseRefundRejectAndUnknownFailurePostgres(t *testing.T) {
	f := newAgentPrepaidRouterFixture(t)
	order := agentPrepaidCreditThroughRouter(t, f)
	request := model.AgentPurchaseRefundCommand{RequestID: "router-prepaid-refund", BatchID: order.FundBatchID, Quota: 500}
	var refund model.AgentPurchaseRefund
	agentFinanceSubmit(t, f.engine, f.owner, "purchase.refund.request", request.RequestID, "/purchase-refunds", request, &refund)
	assert.Equal(t, []string{"45", "1", "44"}, []string{refund.PrincipalMinor, refund.FeeMinor, refund.NetMinor})
	assertAgentPrepaidBudget(t, f.owner.identity.UserID, 500, 500, 0)
	for _, step := range []struct{ suffix, action, outcome, state string }{
		{"approve", "purchase.refund.approve", "", "approved"},
		{"submit", "purchase.refund.submit", "", "payment_pending"},
		{"resolve", "purchase.refund.resolve", "unknown", "payment_unknown"},
	} {
		input := model.AgentPurchaseRefundCommand{RequestID: "router-prepaid-" + step.state, RefundID: refund.ID, Version: refund.Version, BusinessKey: refund.BusinessKey, Outcome: step.outcome, EvidenceReference: "synthetic-reviewed-" + step.state}
		if step.suffix == "approve" {
			input.Decision = "approve"
		}
		agentFinanceSubmit(t, f.engine, f.finance, step.action, input.RequestID, fmt.Sprintf("/finance/purchase-refunds/%d/%s", refund.ID, step.suffix), input, &refund)
		assert.Equal(t, step.state, refund.State)
	}
	assertAgentPrepaidBudget(t, f.owner.identity.UserID, 500, 500, 0)
	var original []model.AgentFundSlice
	require.NoError(t, model.DB.Where("refund_id = ?", refund.ID).Find(&original).Error)
	require.NotEmpty(t, original)
	for _, invalid := range []struct{ action, suffix, outcome string }{
		{"purchase.refund.submit", "submit", ""},
		{"purchase.refund.resolve", "resolve", "explicit_failed"},
	} {
		input := model.AgentPurchaseRefundCommand{RequestID: "router-prepaid-deny-" + invalid.suffix, RefundID: refund.ID, Version: refund.Version, BusinessKey: refund.BusinessKey, Outcome: invalid.outcome, EvidenceReference: "synthetic-inconclusive-observation"}
		actor := f.finance
		if invalid.outcome == "explicit_failed" {
			actor = f.reviewer // Preserve the independent checker's missing-evidence gate.
		}
		proof := agentPrepaidProof(t, f, actor, invalid.action, input.RequestID, input)
		agentPrepaidRoute(t, f, actor, http.MethodPost, fmt.Sprintf("/finance/purchase-refunds/%d/%s", refund.ID, invalid.suffix), input, proof, http.StatusBadRequest, nil)
		assertAgentPrepaidBudget(t, f.owner.identity.UserID, 500, 500, 0)
	}
	failure := model.AgentPurchaseRefundCommand{RequestID: "router-prepaid-explicit-failure", RefundID: refund.ID, Version: refund.Version, BusinessKey: refund.BusinessKey, Outcome: "explicit_failed", EvidenceReference: "synthetic-bank-confirmed-unpaid", NoPaymentConfirmed: true}
	path := fmt.Sprintf("/finance/purchase-refunds/%d/resolve", refund.ID)
	proof := agentPrepaidProof(t, f, f.finance, "purchase.refund.resolve", failure.RequestID, failure)
	agentPrepaidRoute(t, f, f.finance, http.MethodPost, path, failure, proof, http.StatusForbidden, nil)
	var unchanged model.AgentPurchaseRefund
	require.NoError(t, model.DB.First(&unchanged, refund.ID).Error)
	assert.Equal(t, refund.Version, unchanged.Version)
	assert.Equal(t, "payment_unknown", unchanged.State)
	assertAgentPrepaidBudget(t, f.owner.identity.UserID, 500, 500, 0)
	agentFinanceSubmit(t, f.engine, f.reviewer, "purchase.refund.resolve", failure.RequestID, path, failure, &refund)
	assert.Equal(t, "failed", refund.State)
	agentFinanceRequest(t, f.engine, f.reviewer, path, failure, "", &refund)
	assertAgentPrepaidBudget(t, f.owner.identity.UserID, 1000, 0, 0)
	for _, before := range original {
		var after model.AgentFundSlice
		require.NoError(t, model.DB.First(&after, before.ID).Error)
		assert.Equal(t, before.StartQuota, after.StartQuota)
		assert.Equal(t, before.Quota, after.Quota)
		assert.Equal(t, model.AgentSliceAvailable, after.State)
		assert.Zero(t, after.RefundID)
	}
	request.RequestID = "router-prepaid-reject-request"
	agentFinanceSubmit(t, f.engine, f.owner, "purchase.refund.request", request.RequestID, "/purchase-refunds", request, &refund)
	require.NoError(t, model.DB.Model(&model.AgentProfile{}).Where("user_id = ?", f.owner.identity.UserID).Updates(map[string]any{"settlement_frozen": true, "version": 2}).Error)
	reject := model.AgentPurchaseRefundCommand{RequestID: "router-prepaid-reject", RefundID: refund.ID, Version: refund.Version, Decision: "reject", EvidenceReference: "synthetic-independent-rejection"}
	path = fmt.Sprintf("/finance/purchase-refunds/%d/approve", refund.ID)
	agentFinanceSubmit(t, f.engine, f.finance, "purchase.refund.reject", reject.RequestID, path, reject, &refund)
	assert.Equal(t, "rejected", refund.State, "approve route must select the reject proof action for decision=reject")
	assertAgentPrepaidBudget(t, f.owner.identity.UserID, 1000, 0, 0)
	var receipts int64
	require.NoError(t, model.DB.Model(&model.AgentOutboundPaymentReceipt{}).Count(&receipts).Error)
	assert.Zero(t, receipts, "unknown, explicit failure and rejection cannot invent a payment receipt")
	var reconciliation model.AgentFundReconciliationPage
	agentPrepaidRoute(t, f, f.finance, http.MethodGet, fmt.Sprintf("/finance/funds/reconciliation?agent_user_id=%d", f.owner.identity.UserID), nil, "", http.StatusOK, &reconciliation)
	assert.Empty(t, reconciliation.Anomalies)
	assert.Equal(t, []int{f.owner.identity.UserID}, reconciliation.CheckedAgentIDs)
}
