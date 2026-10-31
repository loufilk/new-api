package controller

import (
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func campaignHTTPProof(t *testing.T, identity model.AuthSessionIdentity, action, requestID string, input any) string {
	t.Helper()
	payload, err := common.Marshal(input)
	require.NoError(t, err)
	prepared, err := model.PrepareAgentCampaignOperation(identity, action, requestID, payload)
	require.NoError(t, err)
	hash, err := model.AgentTransactionContextHash(prepared.VerificationContext)
	require.NoError(t, err)
	proof, _, err := model.IssueAgentTransactionProof(identity, hash, "2fa")
	require.NoError(t, err)
	return proof
}
func TestAgentCampaignHTTPProofClaimsListsAndPrivacyPostgres(t *testing.T) {
	f := setupCommissionHTTPPostgres(t)
	require.NoError(t, model.DB.AutoMigrate(&model.AgentInvitation{}, &model.ExternalIdentityClaim{}, &model.BillingFinalization{}, &model.Task{}))
	require.NoError(t, model.DB.Model(&f.owner).Update("role", common.RoleAdminUser).Error)
	policy, err := model.GetCurrentAgentPolicy()
	require.NoError(t, err)
	policy.ID = 0
	policy.RequestID = "campaign-http-policy"
	policy.CampaignEnabled = true
	policy.CampaignTermsApproved = true
	require.NoError(t, model.DB.Create(policy).Error)
	require.NoError(t, model.DB.Model(&model.AgentPolicyState{}).Where("id = 1").Update("current_policy_id", policy.ID).Error)
	session := model.UserSession{SID: "campaign-http-buyer", UserID: f.buyer.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: "synthetic-session", LoginMethod: "password"}
	require.NoError(t, model.DB.Create(&session).Error)
	buyerID := model.AuthSessionIdentity{UserID: f.buyer.Id, SessionID: session.SID, SessionVersion: 1, UserAuthVersion: 1}
	now := common.GetTimestamp()
	proposal := model.AgentCampaignProposeRequest{RequestID: "http-campaign-propose", Terms: model.AgentCampaignTerms{Name: "HTTP acceptance", FamilyKey: "http-acceptance", GrantQuota: 25, TotalBudgetQuota: 100, DailyBudgetQuota: 100, PerAgentBudgetQuota: 100, ClaimStartAt: now - 60, ClaimEndAt: now + 3600, ExpiresAt: now + 7200, RefundTerms: "restore_original_expiry", Revocable: true, Reason: "Synthetic fixture"}}
	proposeHandler := AgentCampaignOperationHandler("campaign.propose")
	response := commissionHTTPRequest(t, f.rootIdentity, http.MethodPost, "/", proposal, "", proposeHandler)
	assert.Equal(t, http.StatusForbidden, response.Code)
	proof := campaignHTTPProof(t, f.rootIdentity, "campaign.propose", proposal.RequestID, proposal)
	tampered := proposal
	tampered.Terms.GrantQuota = 26
	response = commissionHTTPRequest(t, f.rootIdentity, http.MethodPost, "/", tampered, proof, proposeHandler)
	assert.Equal(t, http.StatusForbidden, response.Code)
	// An omitted boolean cannot be silently defaulted by controller re-encoding.
	raw, err := common.Marshal(proposal)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, common.Unmarshal(raw, &fields))
	delete(fields["terms"].(map[string]any), "manual_review")
	response = commissionHTTPRequest(t, f.rootIdentity, http.MethodPost, "/", fields, proof, proposeHandler)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	response = commissionHTTPRequest(t, f.rootIdentity, http.MethodPost, "/", proposal, proof, proposeHandler)
	var outcome model.AgentCampaignOutcome
	commissionHTTPData(t, response, &outcome)
	require.NotNil(t, outcome.Campaign)
	approval := model.AgentCampaignActionRequest{RequestID: "http-campaign-approve", CampaignID: outcome.Campaign.ID, ExpectedVersion: 1, Reason: "Independent finance check"}
	response = commissionHTTPRequest(t, f.rootIdentity, http.MethodPost, "/", approval, "", AgentCampaignOperationHandler("campaign.approve"))
	assert.Equal(t, http.StatusConflict, response.Code)
	proof = campaignHTTPProof(t, f.identity, "campaign.approve", approval.RequestID, approval)
	response = commissionHTTPRequest(t, f.identity, http.MethodPost, "/", approval, proof, AgentCampaignOperationHandler("campaign.approve"))
	commissionHTTPData(t, response, &outcome)
	claim := model.AgentCampaignClaimRequest{RequestID: "http-campaign-claim", CampaignID: outcome.Campaign.ID, TermsHash: outcome.Campaign.TermsHash, ConfirmNoncash: true}
	pat := buyerID
	pat.SessionID = model.AccessTokenSessionID(888)
	response = commissionHTTPRequest(t, pat, http.MethodPost, "/", claim, "", ClaimAgentCampaign)
	assert.Equal(t, http.StatusForbidden, response.Code)
	unconfirmed := claim
	unconfirmed.ConfirmNoncash = false
	response = commissionHTTPRequest(t, buyerID, http.MethodPost, "/", unconfirmed, "", ClaimAgentCampaign)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	var first model.AgentCampaignClaim
	for range 2 {
		response = commissionHTTPRequest(t, buyerID, http.MethodPost, "/", claim, "", ClaimAgentCampaign)
		var current model.AgentCampaignClaim
		commissionHTTPData(t, response, &current)
		if first.ID == 0 {
			first = current
		}
		assert.Equal(t, first.ID, current.ID)
	}
	var user model.User
	require.NoError(t, model.DB.First(&user, f.buyer.Id).Error)
	assert.Equal(t, 25, user.QuotaBonus)
	response = commissionHTTPRequest(t, buyerID, http.MethodGet, "/?p=1&page_size=20", nil, "", AgentCampaignListHandler("sources", false))
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"available":25`)
	assert.NotContains(t, response.Body.String(), "synthetic-session")
	response = commissionHTTPRequest(t, buyerID, http.MethodGet, "/?user_id=999", nil, "", AgentCampaignListHandler("sources", false))
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = commissionHTTPRequest(t, buyerID, http.MethodGet, "/", nil, "", AgentCampaignListHandler("anomalies", true))
	assert.Equal(t, http.StatusForbidden, response.Code)
	// Sensitive review evidence remains hidden even on an identical POST replay.
	require.NoError(t, model.DB.Model(&model.AgentCampaignClaim{}).Where("id = ?", first.ID).Updates(map[string]any{"review_reason": "synthetic-internal-review-evidence", "reviewed_by": f.owner.Id}).Error)
	response = commissionHTTPRequest(t, buyerID, http.MethodPost, "/", claim, "", ClaimAgentCampaign)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.NotContains(t, response.Body.String(), "synthetic-internal-review-evidence")
	response = commissionHTTPRequest(t, buyerID, http.MethodGet, "/", nil, "", func(c *gin.Context) {
		c.Params = gin.Params{{Key: "request_id", Value: claim.RequestID}}
		GetAgentCampaignClaim(c)
	})
	assert.Equal(t, http.StatusOK, response.Code)
	assert.NotContains(t, response.Body.String(), "synthetic-internal-review-evidence")
	status, err := model.FindAgentCampaignOperation(buyerID, claim.RequestID)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, "campaign.claim", status.Action)
}
