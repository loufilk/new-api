package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type creditHTTPFixture struct {
	owner, finance, checker model.AuthSessionIdentity
	policy                  model.AgentPolicy
}

func setupCreditHTTPPostgres(t *testing.T) creditHTTPFixture {
	t.Helper()
	dsn := os.Getenv("TEST_AGENT_POSTGRES_DSN")
	require.NotEmpty(t, dsn, "credit HTTP acceptance requires real PostgreSQL; no silent skip")
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldSecret := common.RedisEnabled, common.SessionSecret
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled = false
	common.SessionSecret = "synthetic-credit-http-secret"
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.SetDatabaseTypes(oldMain, oldLogType)
		common.RedisEnabled = oldRedis
		common.SessionSecret = oldSecret
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.TwoFA{}, &model.PasskeyCredential{}, &model.UserAccessToken{}))
	require.NoError(t, db.AutoMigrate(model.AgentPlatformModels()...))
	ids := make([]model.AuthSessionIdentity, 0, 3)
	for i, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleAdminUser} {
		name := []string{"credit-owner", "credit-maker", "credit-checker"}[i]
		u := model.User{Username: name, AffCode: name, Role: role, Status: common.UserStatusEnabled, AuthVersion: 1}
		require.NoError(t, db.Create(&u).Error)
		sid := "synthetic-credit-http-" + name
		require.NoError(t, db.Create(&model.UserSession{SID: sid, UserID: u.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: "fixture-only", LoginMethod: "password", ExpiresAt: time.Now().Add(time.Hour).Unix()}).Error)
		require.NoError(t, db.Create(&model.TwoFA{UserId: u.Id, Secret: "fixture-only", IsEnabled: true}).Error)
		ids = append(ids, model.AuthSessionIdentity{UserID: u.Id, SessionID: sid, SessionVersion: 1, UserAuthVersion: 1})
	}
	require.NoError(t, db.Create(&model.AgentProfile{UserID: ids[0].UserID, Status: model.AgentProfileActive, Version: 1, Reason: "Synthetic approved eligibility"}).Error)
	p := model.AgentPolicy{RequestID: "credit-http-policy", Reason: "Explicit synthetic no-interest terms", RiskPolicyApproved: true, CreditEnabled: true, CreditTermsApproved: true, CreditRecognitionMode: model.AgentCreditRecognitionSourceSpend, CreditInterestMode: "none", CreditCurrency: "USD", CreditCurrencyExponent: 2, CreditMaximumLimitMinor: 1000, CreditTermSeconds: 86400, CreditMaximumTermSeconds: 86400, CreditQuotaNumerator: 10, CreditQuotaDenominator: 7, CreditRateBPS: 10000, DepositEnabled: true, DepositTermsApproved: true, DepositCurrency: "USD", DepositCurrencyExponent: 2, DepositMaximumMinor: 1000}
	require.NoError(t, model.ValidateAgentPolicy(&p))
	require.NoError(t, db.Create(&p).Error)
	require.NoError(t, db.Create(&model.AgentPolicyState{ID: 1, CurrentPolicyID: p.ID}).Error)
	return creditHTTPFixture{owner: ids[0], finance: ids[1], checker: ids[2], policy: p}
}
func creditHTTPRequest(t *testing.T, id model.AuthSessionIdentity, method, path, action, body, proof string, pat bool, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Security-Proof", proof)
	c.Set("id", id.UserID)
	c.Set("session_id", id.SessionID)
	c.Set("auth_version", id.UserAuthVersion)
	c.Set("session_version", id.SessionVersion)
	c.Set("use_access_token", pat)
	c.Params = gin.Params{{Key: "action", Value: action}}
	handler(c)
	return recorder
}
func creditHTTPProof(t *testing.T, id model.AuthSessionIdentity, action, requestID string, body []byte) string {
	t.Helper()
	var p *model.AgentPreparedOperation
	var err error
	if strings.HasPrefix(action, "credit.") {
		p, err = model.PrepareAgentCreditOperation(id, action, requestID, body)
	} else {
		p, err = model.PrepareAgentDepositOperation(id, action, requestID, body)
	}
	require.NoError(t, err)
	require.NotNil(t, p)
	hash, err := model.AgentTransactionContextHash(p.VerificationContext)
	require.NoError(t, err)
	token, _, err := model.IssueAgentTransactionProof(id, hash, "2fa")
	require.NoError(t, err)
	return token
}
func TestAgentCreditHTTPBoundProofExactLostAckAndNoPATPostgres(t *testing.T) {
	f := setupCreditHTTPPostgres(t)
	command := model.AgentCreditCommand{RequestID: "http-credit-limit-request", AmountMinor: "100", TermSeconds: 86400, ExpiresAt: common.GetTimestamp() + 86400, Reason: "Explicit manual credit application", EvidenceReference: "synthetic-controlled-contract"}
	body, err := common.Marshal(command)
	require.NoError(t, err)
	response := creditHTTPRequest(t, f.owner, http.MethodPost, "/api/agent-platform/credit/actions/limit.request", "limit.request", string(body), "", false, AgentCreditAction)
	assert.NotEqual(t, http.StatusOK, response.Code)
	var rows int64
	require.NoError(t, model.DB.Model(&model.AgentCreditLimitRequest{}).Count(&rows).Error)
	assert.Zero(t, rows)
	token := creditHTTPProof(t, f.owner, "credit.limit.request", command.RequestID, body)
	changed := bytes.Replace(body, []byte(`"100"`), []byte(`"101"`), 1)
	response = creditHTTPRequest(t, f.owner, http.MethodPost, "/api/agent-platform/credit/actions/limit.request", "limit.request", string(changed), token, false, AgentCreditAction)
	assert.NotEqual(t, http.StatusOK, response.Code)
	response = creditHTTPRequest(t, f.owner, http.MethodPost, "/api/agent-platform/credit/actions/limit.request", "limit.request", string(body), token, true, AgentCreditAction)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = creditHTTPRequest(t, f.owner, http.MethodPost, "/api/agent-platform/credit/actions/limit.request", "limit.request", string(body), token, false, AgentCreditAction)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.NotContains(t, response.Body.String(), command.EvidenceReference)
	response = creditHTTPRequest(t, f.owner, http.MethodPost, "/api/agent-platform/credit/actions/limit.request", "limit.request", string(body), "", false, AgentCreditAction)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, model.DB.Model(&model.AgentCreditLimitRequest{}).Count(&rows).Error)
	assert.EqualValues(t, 1, rows)
	response = creditHTTPRequest(t, f.owner, http.MethodPost, "/api/agent-platform/credit/actions/limit.request", "limit.request", string(changed), "", false, AgentCreditAction)
	assert.Equal(t, http.StatusConflict, response.Code)
	malformed := strings.TrimSuffix(string(body), "}") + `,"unexpected_privilege":true}`
	response = creditHTTPRequest(t, f.owner, http.MethodPost, "/api/agent-platform/credit/actions/limit.request", "limit.request", malformed, "", false, AgentCreditAction)
	assert.Equal(t, http.StatusBadRequest, response.Code)
}
func TestAgentDepositHTTPIndependentDossierVerificationAndPrivateRecoveryPostgres(t *testing.T) {
	f := setupCreditHTTPPostgres(t)
	command := model.AgentDepositCommand{RequestID: "http-deposit-record", AgentUserID: f.owner.UserID, AmountMinor: "20", Currency: "USD", CurrencyExponent: 2, Provider: "bank_transfer", MerchantScope: "fixture-bank", Environment: "live", ExternalReference: "sensitive-bank-reference", PaidAt: common.GetTimestamp(), EvidenceReference: "private-proof-document", PayerReference: "controlled-private-payer-dossier"}
	body, err := common.Marshal(command)
	require.NoError(t, err)
	token := creditHTTPProof(t, f.finance, "deposit.receipt.record", command.RequestID, body)
	response := creditHTTPRequest(t, f.finance, http.MethodPost, "/api/agent-platform/deposit/actions/receipt.record", "receipt.record", string(body), token, false, AgentDepositAction)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), command.PayerReference)
	assert.NotContains(t, response.Body.String(), command.ExternalReference)
	var receipt model.AgentDepositReceipt
	require.NoError(t, model.DB.First(&receipt).Error)
	confirm := model.AgentDepositCommand{RequestID: "http-deposit-confirm", ResourceID: receipt.ID, Version: receipt.Version, EvidenceReference: "checker-proof", PayerReference: command.PayerReference}
	body, err = common.Marshal(confirm)
	require.NoError(t, err)
	response = creditHTTPRequest(t, f.finance, http.MethodPost, "/api/agent-platform/deposit/actions/receipt.confirm", "receipt.confirm", string(body), "", false, AgentDepositAction)
	assert.Equal(t, http.StatusForbidden, response.Code)
	token = creditHTTPProof(t, f.checker, "deposit.receipt.confirm", confirm.RequestID, body)
	response = creditHTTPRequest(t, f.checker, http.MethodPost, "/api/agent-platform/deposit/actions/receipt.confirm", "receipt.confirm", string(body), token, false, AgentDepositAction)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = creditHTTPRequest(t, f.owner, http.MethodGet, "/api/agent-platform/deposit", "", "", "", false, GetAgentDeposit)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), command.PayerReference)
	assert.NotContains(t, response.Body.String(), command.EvidenceReference)
	assert.Contains(t, response.Body.String(), `"available_minor":"20"`)
	response = creditHTTPRequest(t, f.owner, http.MethodGet, "/api/agent-platform/deposit?limit=101", "", "", "", false, GetAgentDeposit)
	assert.Equal(t, http.StatusBadRequest, response.Code)
}
