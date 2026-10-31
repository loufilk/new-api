package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAgreementStatementHTTP(t *testing.T) creditHTTPFixture {
	t.Helper()
	f := setupCreditHTTPPostgres(t)
	p := f.policy
	p.ID = 0
	p.RequestID = "commercial-http-policy"
	p.PurchaseEnabled = true
	p.PurchaseRefundTermsApproved = true
	p.PurchaseCurrency = "USD"
	p.PurchaseCurrencyExponent = 2
	p.PurchaseRateBPS = 9000
	p.PurchaseQuotaNumerator = 10
	p.PurchaseQuotaDenominator = 1
	p.PurchaseMinimumQuota = 1
	p.PurchaseMaximumQuota = 100000
	p.QuoteLifetimeSeconds = 600
	p.AgreementPricingEnabled = true
	p.AgreementMinimumRateBPS = 5000
	p.ManualReconciliationEnabled = true
	require.NoError(t, model.ValidateAgentPolicy(&p))
	require.NoError(t, model.DB.Create(&p).Error)
	require.NoError(t, model.DB.Model(&model.AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	f.policy = p
	return f
}
func agreementStatementHTTP(t *testing.T, id model.AuthSessionIdentity, method, path, body, proof string, pat bool) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", id.UserID)
		c.Set("session_id", id.SessionID)
		c.Set("auth_version", id.UserAuthVersion)
		c.Set("session_version", id.SessionVersion)
		c.Set("use_access_token", pat)
		c.Next()
	})
	r.POST("/agreements/:action", ExecuteAgentAgreement)
	r.GET("/agreements", GetAgentAgreements)
	r.GET("/agreements/:id", GetAgentAgreement)
	r.POST("/statements/:action", ExecuteAgentStatement)
	r.GET("/statements/:kind", GetAgentStatements)
	r.GET("/statements/:kind/:id", GetAgentStatementDetail)
	r.GET("/statements/lines/:id/candidates", GetAgentStatementCandidates)
	r.GET("/statements/lines/:id/evidence", GetAgentStatementEvidence)
	r.POST("/prepare", PrepareAgentPlatformOperation)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Security-Proof", proof)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
func agreementStatementHTTPProof(t *testing.T, id model.AuthSessionIdentity, action, requestID string, body []byte) string {
	t.Helper()
	var prepared *model.AgentPreparedOperation
	var err error
	if strings.HasPrefix(action, "agreement.") {
		prepared, err = model.PrepareAgentAgreementOperation(id, action, requestID, body)
	} else {
		prepared, err = model.PrepareAgentStatementOperation(id, action, requestID, body)
	}
	require.NoError(t, err)
	hash, err := model.AgentTransactionContextHash(prepared.VerificationContext)
	require.NoError(t, err)
	proof, _, err := model.IssueAgentTransactionProof(id, hash, "2fa")
	require.NoError(t, err)
	return proof
}
func TestAgentAgreementHTTPBoundProofMakerCheckerCurrentAccessPostgres(t *testing.T) {
	f := setupAgreementStatementHTTP(t)
	now := common.GetTimestamp()
	c := model.AgentAgreementCommand{RequestID: "http-agreement-propose", Reason: "Synthetic individually reviewed contract", Terms: model.AgentAgreementTerms{AgreementKey: "http-agreement", TermsVersion: 1, Scope: "agreement", AgentUserID: f.owner.UserID, Currency: "USD", CurrencyExponent: 2, QuotaNumerator: 10, QuotaDenominator: 1, BaseRateBPS: 8800, EffectiveFrom: now - 60, EffectiveUntil: now + 3600, DossierReference: "contract:synthetic-http", Tiers: []model.AgentAgreementTier{}}}
	body, err := common.Marshal(c)
	require.NoError(t, err)
	response := agreementStatementHTTP(t, f.finance, http.MethodPost, "/agreements/propose", string(body), "", false)
	assert.NotEqual(t, http.StatusOK, response.Code)
	proof := agreementStatementHTTPProof(t, f.finance, "agreement.propose", c.RequestID, body)
	response = agreementStatementHTTP(t, f.finance, http.MethodPost, "/agreements/propose", string(body), proof, true)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = agreementStatementHTTP(t, f.finance, http.MethodPost, "/agreements/propose", strings.Replace(string(body), "8800", "8700", 1), proof, false)
	assert.NotEqual(t, http.StatusOK, response.Code)
	response = agreementStatementHTTP(t, f.finance, http.MethodPost, "/agreements/propose", string(body), proof, false)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	// Lost acknowledgement can recover exact result without reusing an MFA proof.
	response = agreementStatementHTTP(t, f.finance, http.MethodPost, "/agreements/propose", string(body), "", false)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var row model.AgentAgreement
	require.NoError(t, model.DB.First(&row).Error)
	review := model.AgentAgreementCommand{RequestID: "http-agreement-approve", AgreementID: row.ID, ExpectedVersion: 1, Reason: "Synthetic independent contractual approval"}
	body, err = common.Marshal(review)
	require.NoError(t, err)
	proof = agreementStatementHTTPProof(t, f.finance, "agreement.approve", review.RequestID, body)
	response = agreementStatementHTTP(t, f.finance, http.MethodPost, "/agreements/approve", string(body), proof, false)
	assert.Equal(t, http.StatusForbidden, response.Code)
	proof = agreementStatementHTTPProof(t, f.checker, "agreement.approve", review.RequestID, body)
	response = agreementStatementHTTP(t, f.checker, http.MethodPost, "/agreements/approve", string(body), proof, false)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = agreementStatementHTTP(t, f.owner, http.MethodGet, "/agreements/"+strconv.FormatInt(row.ID, 10), "", "", false)
	assert.Equal(t, http.StatusForbidden, response.Code)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.checker.UserID).Update("role", common.RoleCommonUser).Error)
	response = agreementStatementHTTP(t, f.checker, http.MethodPost, "/agreements/approve", string(body), "", false)
	assert.Equal(t, http.StatusForbidden, response.Code)
}
func TestAgentStatementHTTPRoutePaginationCapsNoStoreAndSessionRevocationPostgres(t *testing.T) {
	f := setupAgreementStatementHTTP(t)
	now := common.GetTimestamp()
	c := model.AgentStatementCommand{RequestID: "http-statement-period", Reason: "Synthetic bank statement review window", AccountDossierReference: "account:synthetic", Currency: "USD", StartsAt: now - 60, EndsAt: now + 3600}
	body, err := common.Marshal(c)
	require.NoError(t, err)
	preparedBody, err := common.Marshal(map[string]any{"action": "statement.period.create", "request_id": c.RequestID, "payload": c})
	require.NoError(t, err)
	response := agreementStatementHTTP(t, f.finance, http.MethodPost, "/prepare", string(preparedBody), "", false)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), "statement.period.create")
	proof := agreementStatementHTTPProof(t, f.finance, "statement.period.create", c.RequestID, body)
	response = agreementStatementHTTP(t, f.finance, http.MethodPost, "/statements/period-create", string(body), proof, false)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	for _, path := range []string{"/statements/periods", "/statements/lines", "/statements/matches", "/statements/exceptions", "/statements/evidence", "/statements/audit"} {
		response = agreementStatementHTTP(t, f.finance, http.MethodGet, path, "", "", false)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
	response = agreementStatementHTTP(t, f.finance, http.MethodPost, "/statements/import", `{"request_id":"http-oversize","reason":"`+strings.Repeat("a", model.AgentStatementMaximumBodyBytes)+`"}`, "", false)
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	assert.Contains(t, response.Body.String(), "The encoded statement is too large")
	response = agreementStatementHTTP(t, f.finance, http.MethodPost, "/statements/period-create", strings.TrimSuffix(string(body), "}")+`,"unknown_bank_password":"not-accepted"}`, "", false)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", f.finance.SessionID).Update("status", model.UserSessionStatusRevoked).Error)
	response = agreementStatementHTTP(t, f.finance, http.MethodGet, "/statements/periods", "", "", false)
	assert.NotEqual(t, http.StatusOK, response.Code)
	response = agreementStatementHTTP(t, f.finance, http.MethodPost, "/statements/period-create", string(body), "", false)
	assert.NotEqual(t, http.StatusOK, response.Code)
}
