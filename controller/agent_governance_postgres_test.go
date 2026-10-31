package controller

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func governanceHTTPFixture(t *testing.T) commissionHTTPFixture {
	t.Helper()
	f := setupCommissionHTTPPostgres(t)
	require.NoError(t, model.DB.AutoMigrate(&model.AgentInvitation{}, &model.AgentRedemptionOperation{}, &model.AgentRedemptionAccount{}, &model.AgentRedemptionLedger{}, &model.Redemption{}))
	p, err := model.GetCurrentAgentPolicy()
	require.NoError(t, err)
	p.ID = 0
	p.RequestID = "governance-http-policy"
	p.GovernanceEnabled = true
	p.GovernanceRequiredForActivation = true
	p.CreditGovernanceRequired = true
	require.NoError(t, model.ValidateAgentPolicy(p))
	require.NoError(t, model.DB.Create(p).Error)
	require.NoError(t, model.DB.Model(&model.AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	return f
}
func governanceHTTPProof(t *testing.T, identity model.AuthSessionIdentity, action string, c model.AgentGovernanceCommand) string {
	t.Helper()
	payload, err := common.Marshal(c)
	require.NoError(t, err)
	prepared, err := model.PrepareAgentGovernanceOperation(identity, action, c.RequestID, payload)
	require.NoError(t, err)
	hash, err := model.AgentTransactionContextHash(prepared.VerificationContext)
	require.NoError(t, err)
	token, _, err := model.IssueAgentTransactionProof(identity, hash, "2fa")
	require.NoError(t, err)
	return token
}
func TestAgentGovernanceHTTPConsentStrictPrivacyRecoveryAndFreshRolesPostgres(t *testing.T) {
	f := governanceHTTPFixture(t)
	c := model.AgentGovernanceCommand{RequestID: "governance-http-submit", ExpectedVersion: 0, IdentityDossierReference: "dossier:case-0001", AgreementReference: "agreement:terms-0001", AgreementDigest: strings.Repeat("a", 64), AcceptTerms: true}
	response := commissionHTTPRequest(t, f.identity, http.MethodPost, "/api/agent-platform/governance/submit", c, "", SubmitAgentGovernance)
	assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	bad := c
	bad.AcceptTerms = false
	response = commissionHTTPRequest(t, f.identity, http.MethodPost, "/api/agent-platform/governance/submit", bad, "", SubmitAgentGovernance)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	payload, err := common.Marshal(c)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, common.Unmarshal(payload, &fields))
	fields["identity_document_number"] = "synthetic-raw-identity-field-must-not-be-stored"
	response = commissionHTTPRequest(t, f.identity, http.MethodPost, "/api/agent-platform/governance/submit", fields, "", SubmitAgentGovernance)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	preparation := map[string]any{"action": "governance.submit", "request_id": c.RequestID, "payload": c}
	response = commissionHTTPRequest(t, f.identity, http.MethodPost, "/api/agent-platform/operations/prepare", preparation, "", PrepareAgentPlatformOperation)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"profile_version":0`)
	proof := governanceHTTPProof(t, f.identity, "governance.submit", c)
	response = commissionHTTPRequest(t, f.identity, http.MethodPost, "/api/agent-platform/governance/submit", c, proof, SubmitAgentGovernance)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"status":"pending"`)
	response = commissionHTTPRequest(t, f.identity, http.MethodGet, "/api/agent-platform/governance/self", nil, "", GetAgentGovernanceSelf)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), c.IdentityDossierReference)
	assert.NotContains(t, response.Body.String(), c.AgreementReference)
	assert.Contains(t, response.Body.String(), `"profile":null`)
	financeView := func(ctx *gin.Context) {
		ctx.Params = gin.Params{{Key: "user_id", Value: fmt.Sprint(f.owner.Id)}}
		GetAgentGovernanceFinance(ctx)
	}
	response = commissionHTTPRequest(t, f.rootIdentity, http.MethodGet, "/api/agent-platform/finance/governance/subject", nil, "", financeView)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), c.IdentityDossierReference)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	review := model.AgentGovernanceCommand{RequestID: "governance-http-review", UserID: f.owner.Id, ExpectedVersion: 1, Decision: "approved", IdentityVerified: true, AgreementVerified: true, ReviewDueAt: common.GetTimestamp() + 86400, Reason: "Independent synthetic review"}
	reviewHandler := func(ctx *gin.Context) {
		ctx.Params = gin.Params{{Key: "user_id", Value: fmt.Sprint(f.owner.Id)}}
		ReviewAgentGovernance(ctx)
	}
	response = commissionHTTPRequest(t, f.identity, http.MethodPost, "/api/agent-platform/finance/governance/subject/review", review, "", reviewHandler)
	assert.Equal(t, http.StatusForbidden, response.Code)
	reviewProof := governanceHTTPProof(t, f.rootIdentity, "governance.review", review)
	mismatched := review
	mismatched.UserID = f.buyer.Id
	response = commissionHTTPRequest(t, f.rootIdentity, http.MethodPost, "/api/agent-platform/finance/governance/subject/review", mismatched, reviewProof, reviewHandler)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	response = commissionHTTPRequest(t, f.rootIdentity, http.MethodPost, "/api/agent-platform/finance/governance/subject/review", review, reviewProof, reviewHandler)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"status":"approved"`)
	// Exact response recovery needs a fresh browser session, not another OTP.
	response = commissionHTTPRequest(t, f.identity, http.MethodPost, "/api/agent-platform/governance/submit", c, "", SubmitAgentGovernance)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"status":"pending"`)
	statusHandler := func(ctx *gin.Context) {
		ctx.Params = gin.Params{{Key: "request_id", Value: c.RequestID}}
		GetAgentPlatformOperation(ctx)
	}
	response = commissionHTTPRequest(t, f.identity, http.MethodGet, "/api/agent-platform/operations/request", nil, "", statusHandler)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"resource_state":"pending"`)
	var count int64
	require.NoError(t, model.DB.Model(&model.AgentGovernanceSubmission{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.root.Id).Update("role", common.RoleCommonUser).Error)
	response = commissionHTTPRequest(t, f.rootIdentity, http.MethodGet, "/api/agent-platform/finance/governance/subject", nil, "", financeView)
	assert.Equal(t, http.StatusForbidden, response.Code)
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", f.identity.SessionID).Update("status", model.UserSessionStatusRevoked).Error)
	response = commissionHTTPRequest(t, f.identity, http.MethodPost, "/api/agent-platform/governance/submit", c, "", SubmitAgentGovernance)
	assert.Equal(t, http.StatusForbidden, response.Code)
}
func TestAgentContributionsHTTPOwnershipNoStoreAndPrivateReferencesPostgres(t *testing.T) {
	f := governanceHTTPFixture(t)
	response := commissionHTTPRequest(t, f.identity, http.MethodGet, "/api/agent-platform/contributions?page=1&page_size=20", nil, "", GetAgentContributions)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Contains(t, response.Body.String(), `"confirmed_topup_principal_minor":"10000"`)
	assert.Contains(t, response.Body.String(), `"historical_complete":false`)
	assert.NotContains(t, response.Body.String(), "pi_http_fixture")
	assert.NotContains(t, response.Body.String(), "synthetic-http-merchant")
	response = commissionHTTPRequest(t, f.identity, http.MethodGet, fmt.Sprintf("/api/agent-platform/contributions?agent_user_id=%d", f.root.Id), nil, "", GetAgentContributions)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = commissionHTTPRequest(t, f.identity, http.MethodGet, fmt.Sprintf("/api/agent-platform/finance/contributions?agent_user_id=%d", f.owner.Id), nil, "", GetAgentContributionsFinance)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = commissionHTTPRequest(t, f.rootIdentity, http.MethodGet, fmt.Sprintf("/api/agent-platform/finance/contributions?agent_user_id=%d", f.owner.Id), nil, "", GetAgentContributionsFinance)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	response = commissionHTTPRequest(t, f.rootIdentity, http.MethodGet, "/api/agent-platform/finance/contributions", nil, "", GetAgentContributionsFinance)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	pat := f.identity
	pat.SessionID = model.AccessTokenSessionID(1)
	response = commissionHTTPRequest(t, pat, http.MethodGet, "/api/agent-platform/contributions", nil, "", GetAgentContributions)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}
