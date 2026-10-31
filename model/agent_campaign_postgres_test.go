package model

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type campaignFixture struct {
	root, finance, buyer       User
	rootID, financeID, buyerID AuthSessionIdentity
	policy                     AgentPolicy
}

func setupCampaignPostgres(t *testing.T) campaignFixture {
	t.Helper()
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6; acceptance must have zero skips")
	}
	root, buyer, rootID, buyerID := setupAgentProfileSecurityTest(t)
	models := append(BonusSourceModels(), AgentCampaignModels()...)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(models...))
	}
	for _, schema := range models {
		statement := &gorm.Statement{DB: DB}
		require.NoError(t, statement.Parse(schema))
		require.NoError(t, DB.Exec("DELETE FROM "+statement.Schema.Table).Error)
	}
	finance, financeID := campaignBrowser(t, "campaign-finance", common.RoleAdminUser)
	policy := AgentPolicy{RequestID: "campaign-policy-fixture", CreatedBy: root.Id, Reason: "Synthetic campaign terms", RiskPolicyApproved: true, CampaignEnabled: true, CampaignTermsApproved: true}
	require.NoError(t, DB.Create(&policy).Error)
	require.NoError(t, DB.Create(&AgentPolicyState{ID: 1, CurrentPolicyID: policy.ID}).Error)
	return campaignFixture{root: root, finance: finance, buyer: buyer, rootID: rootID, financeID: financeID, buyerID: buyerID, policy: policy}
}
func campaignBrowser(t *testing.T, name string, role int) (User, AuthSessionIdentity) {
	t.Helper()
	user := User{Username: name, AffCode: name, Role: role, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, DB.Create(&user).Error)
	session := UserSession{SID: "campaign-session-" + name, UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: "synthetic-fixture", LoginMethod: "password"}
	require.NoError(t, DB.Create(&session).Error)
	require.NoError(t, DB.Create(&TwoFA{UserId: user.Id, IsEnabled: true, Secret: "synthetic-fixture-not-credential"}).Error)
	return user, AuthSessionIdentity{UserID: user.Id, SessionID: session.SID, SessionVersion: 1, UserAuthVersion: 1}
}
func campaignTerms(key string) AgentCampaignTerms {
	now := common.GetTimestamp()
	return AgentCampaignTerms{Name: key, FamilyKey: key, GrantQuota: 50, TotalBudgetQuota: 500, DailyBudgetQuota: 300, PerAgentBudgetQuota: 200, ClaimStartAt: now - 60, ClaimEndAt: now + 3600, ExpiresAt: now + 7200, RefundTerms: "restore_original_expiry", Revocable: true, Reason: "Synthetic quota terms; no money"}
}
func campaignProof(t *testing.T, identity AuthSessionIdentity, action, requestID string, input any) AgentTransactionAuthorization {
	t.Helper()
	payload, err := common.Marshal(input)
	require.NoError(t, err)
	prepared, err := PrepareAgentCampaignOperation(identity, action, requestID, payload)
	require.NoError(t, err)
	_, auth := issueAgentProfileTestProof(t, identity, prepared.VerificationContext)
	return auth
}
func campaignExecute(t *testing.T, identity AuthSessionIdentity, action, requestID string, input any) *AgentCampaignOutcome {
	t.Helper()
	auth := campaignProof(t, identity, action, requestID, input)
	payload, err := common.Marshal(input)
	require.NoError(t, err)
	out, err := ExecuteAgentCampaignOperation(auth, action, requestID, payload)
	require.NoError(t, err)
	return out
}
func approvedCampaign(t *testing.T, f campaignFixture, terms AgentCampaignTerms) *AgentCampaign {
	t.Helper()
	proposal := AgentCampaignProposeRequest{RequestID: "propose-" + terms.Name, Terms: terms}
	out := campaignExecute(t, f.rootID, "campaign.propose", proposal.RequestID, proposal)
	approval := AgentCampaignActionRequest{RequestID: "approve-" + terms.Name, CampaignID: out.Campaign.ID, ExpectedVersion: 1, Reason: "Independent synthetic terms approval"}
	out = campaignExecute(t, f.financeID, "campaign.approve", approval.RequestID, approval)
	return out.Campaign
}
func campaignClaim(campaign *AgentCampaign, key string) AgentCampaignClaimRequest {
	return AgentCampaignClaimRequest{RequestID: key, CampaignID: campaign.ID, TermsHash: campaign.TermsHash, ConfirmNoncash: true}
}

func TestAgentCampaignIndependentApprovalPolicyProofAndImmutabilityPostgres(t *testing.T) {
	f := setupCampaignPostgres(t)
	terms := campaignTerms("approval-controls")
	proposal := AgentCampaignProposeRequest{RequestID: "approval-propose", Terms: terms}
	out := campaignExecute(t, f.rootID, "campaign.propose", proposal.RequestID, proposal)
	approval := AgentCampaignActionRequest{RequestID: "approval-confirm", CampaignID: out.Campaign.ID, ExpectedVersion: 1, Reason: "Independent approval"}
	payload, err := common.Marshal(approval)
	require.NoError(t, err)
	_, err = PrepareAgentCampaignOperation(f.rootID, "campaign.approve", approval.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentCampaignConflict)
	_, err = ClaimAgentCampaign(f.buyerID, campaignClaim(out.Campaign, "claim-unapproved"))
	require.ErrorIs(t, err, ErrAgentCampaignIneligible)
	auth := campaignProof(t, f.financeID, "campaign.approve", approval.RequestID, approval)
	changed := approval
	changed.Reason = "Different bound operation"
	wrong, err := common.Marshal(changed)
	require.NoError(t, err)
	_, err = ExecuteAgentCampaignOperation(auth, "campaign.approve", approval.RequestID, wrong)
	require.ErrorIs(t, err, ErrAgentTransactionProof)
	result, err := ExecuteAgentCampaignOperation(auth, "campaign.approve", approval.RequestID, payload)
	require.NoError(t, err)
	assert.Equal(t, "approved", result.Publication.Status)
	replay, err := ExecuteAgentCampaignOperation(auth, "campaign.approve", approval.RequestID, payload)
	require.NoError(t, err)
	assert.Equal(t, result.Publication.ApprovedAt, replay.Publication.ApprovedAt)
	require.ErrorIs(t, DB.Model(out.Campaign).Update("grant_quota", 900).Error, ErrAgentLedgerImmutable)
	disabled := f.policy
	disabled.ID = 0
	disabled.RequestID = "campaign-policy-disabled"
	disabled.CampaignEnabled = false
	require.NoError(t, DB.Create(&disabled).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", disabled.ID).Error)
	_, err = ClaimAgentCampaign(f.buyerID, campaignClaim(out.Campaign, "claim-disabled"))
	require.ErrorIs(t, err, ErrAgentFeatureDisabled)
	before := getUserBalances(t, f.buyer.Id)
	_, err = DebitQuota(LedgerEntry{UserId: f.buyer.Id, Delta: 10, Reason: "legitimate-old-consumption"}, AccountBonus)
	require.NoError(t, err)
	assert.Equal(t, before.Quota-10, getUserBalances(t, f.buyer.Id).Quota)
	pat := f.buyerID
	pat.SessionID = AccessTokenSessionID(123)
	_, err = ClaimAgentCampaign(pat, campaignClaim(out.Campaign, "claim-pat"))
	require.ErrorIs(t, err, ErrAgentProfilePermission)
}

func TestAgentCampaignReplayOverlapAndConcurrentCapsPostgres(t *testing.T) {
	f := setupCampaignPostgres(t)
	terms := campaignTerms("concurrent-cap")
	terms.TotalBudgetQuota = 100
	terms.DailyBudgetQuota = 100
	terms.PerAgentBudgetQuota = 100
	campaign := approvedCampaign(t, f, terms)
	_, secondID := campaignBrowser(t, "campaign-second", common.RoleCommonUser)
	_, thirdID := campaignBrowser(t, "campaign-third", common.RoleCommonUser)
	identities := []AuthSessionIdentity{f.buyerID, f.buyerID, secondID, thirdID}
	keys := []string{"same-event", "same-event", "second-event", "third-event"}
	start := make(chan struct{})
	errs := make(chan error, 4)
	var wg sync.WaitGroup
	for i := range identities {
		wg.Go(func() {
			<-start
			_, err := ClaimAgentCampaign(identities[i], campaignClaim(campaign, keys[i]))
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			require.ErrorIs(t, err, ErrAgentCampaignBudget)
		}
	}
	var claims []AgentCampaignClaim
	require.NoError(t, DB.Where("campaign_id = ?", campaign.ID).Find(&claims).Error)
	require.Len(t, claims, 2)
	var budgets []AgentCampaignBudget
	require.NoError(t, DB.Where("campaign_id = ?", campaign.ID).Find(&budgets).Error)
	require.Len(t, budgets, 3)
	for _, b := range budgets {
		assert.Equal(t, 100, b.SpentQuota)
		assert.Zero(t, b.ReservedQuota)
	}
	var lots int64
	require.NoError(t, DB.Model(&BonusSourceLot{}).Where("campaign_id = ?", campaign.ID).Count(&lots).Error)
	assert.EqualValues(t, 2, lots)
	// Overlapping versions in a family cannot grant another event to a user.
	terms2 := campaignTerms("overlap-version")
	terms2.FamilyKey = terms.FamilyKey
	overlap := approvedCampaign(t, f, terms2)
	_, err := ClaimAgentCampaign(identities[0], campaignClaim(overlap, "same-family-new-id"))
	if claims[0].UserID == f.buyer.Id || claims[1].UserID == f.buyer.Id {
		require.ErrorIs(t, err, ErrAgentCampaignIneligible)
	} else {
		require.NoError(t, err)
	}
	// Replay evidence remains accessible to its own browser, not another account.
	own := claims[0]
	ownerID := f.buyerID
	for _, id := range identities {
		if id.UserID == own.UserID {
			ownerID = id
		}
	}
	status, err := FindAgentCampaignOperation(ownerID, own.RequestID)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, "campaign.claim", status.Action)
	replay, err := ClaimAgentCampaign(ownerID, campaignClaim(campaign, own.RequestID))
	require.NoError(t, err)
	assert.Equal(t, own.ID, replay.ID)
	conflict := campaignClaim(overlap, own.RequestID)
	_, err = ClaimAgentCampaign(ownerID, conflict)
	require.ErrorIs(t, err, ErrAgentCampaignConflict)
}

func TestAgentCampaignReviewBudgetReleaseRevocationAndOriginalRefundPostgres(t *testing.T) {
	f := setupCampaignPostgres(t)
	terms := campaignTerms("manual-review")
	terms.ManualReview = true
	terms.ReviewWindowSeconds = 300
	campaign := approvedCampaign(t, f, terms)
	before := getUserBalances(t, f.buyer.Id)
	claim, err := ClaimAgentCampaign(f.buyerID, campaignClaim(campaign, "review-claim"))
	require.NoError(t, err)
	assert.Equal(t, "review_pending", claim.Status)
	assert.Equal(t, before.Quota, getUserBalances(t, f.buyer.Id).Quota)
	deny := AgentCampaignActionRequest{RequestID: "review-deny", ClaimID: claim.ID, ExpectedVersion: 1, Decision: "reject", Reason: "Manual review denial fixture"}
	out := campaignExecute(t, f.financeID, "campaign.review.reject", deny.RequestID, deny)
	assert.Equal(t, "rejected", out.Claim.Status)
	var budgets []AgentCampaignBudget
	require.NoError(t, DB.Where("campaign_id = ?", campaign.ID).Find(&budgets).Error)
	for _, b := range budgets {
		assert.Zero(t, b.ReservedQuota)
		assert.Zero(t, b.SpentQuota)
		assert.Equal(t, 50, b.ReleasedQuota)
	}
	assert.Equal(t, before.Quota, getUserBalances(t, f.buyer.Id).Quota)
	_, err = ClaimAgentCampaign(f.buyerID, campaignClaim(campaign, "denial-new-id"))
	require.ErrorIs(t, err, ErrAgentCampaignIneligible)
	buyer, buyerID := campaignBrowser(t, "review-approved", common.RoleCommonUser)
	claim, err = ClaimAgentCampaign(buyerID, campaignClaim(campaign, "approve-review-claim"))
	require.NoError(t, err)
	approve := AgentCampaignActionRequest{RequestID: "review-approve", ClaimID: claim.ID, ExpectedVersion: 1, Decision: "approve", Reason: "Verified supported eligibility"}
	out = campaignExecute(t, f.financeID, "campaign.review.approve", approve.RequestID, approve)
	assert.Equal(t, "granted", out.Claim.Status)
	sourceDebit(t, buyer.Id, 20, "revoked-held")
	revoke := AgentCampaignActionRequest{RequestID: "review-revoke", ClaimID: claim.ID, ExpectedVersion: 2, Reason: "Original revocable terms fixture"}
	out = campaignExecute(t, f.financeID, "campaign.revoke", revoke.RequestID, revoke)
	assert.Equal(t, "revoked", out.Claim.Status)
	var lot BonusSourceLot
	require.NoError(t, DB.Where("claim_id = ?", claim.ID).First(&lot).Error)
	state := sourceState(t, lot.ID)
	assert.Equal(t, 30, state.Revoked)
	assert.Equal(t, 20, state.Outstanding)
	assert.True(t, state.RevocationActive)
	sourceRefund(t, buyer.Id, 20, "revoked-held")
	_, err = CreditQuota(LedgerEntry{UserId: buyer.Id, Account: AccountPaid, Delta: 10, Reason: "test", IdempotencyKey: "post-revocation-paid"})
	require.NoError(t, err)
	state = sourceState(t, lot.ID)
	assert.Equal(t, 50, state.Revoked)
	assert.Zero(t, state.Available)
	assert.Zero(t, state.Outstanding)
	assert.Equal(t, 10, getUserBalances(t, buyer.Id).QuotaPaid)
	// Maintenance releases only a pending ungranted review reservation.
	_, lateID := campaignBrowser(t, "review-expired", common.RoleCommonUser)
	late, err := ClaimAgentCampaign(lateID, campaignClaim(campaign, "late-review-claim"))
	require.NoError(t, err)
	require.NoError(t, DB.Model(late).Update("review_deadline", common.GetTimestamp()-1).Error)
	report, err := RunAgentCampaignMaintenance(t.Context(), 100)
	require.NoError(t, err)
	assert.Equal(t, 1, report.ExpiredClaims)
	require.NoError(t, DB.First(late, late.ID).Error)
	assert.Equal(t, "expired", late.Status)
	require.NoError(t, DB.Where("campaign_id = ?", campaign.ID).Find(&budgets).Error)
	for _, b := range budgets {
		assert.Zero(t, b.ReservedQuota)
		assert.Equal(t, 50, b.SpentQuota)
		assert.Equal(t, 100, b.ReleasedQuota)
	}
}

func TestAgentCampaignSupportedEligibilityAndProspectiveNonstackPostgres(t *testing.T) {
	f := setupCampaignPostgres(t)
	terms := campaignTerms("supported-identity")
	terms.RequireLinuxDO = true
	terms.RequireAgentInvitation = true
	terms.ExclusionGroup = "new-user-trial"
	campaign := approvedCampaign(t, f, terms)
	_, err := ClaimAgentCampaign(f.buyerID, campaignClaim(campaign, "not-eligible"))
	require.ErrorIs(t, err, ErrAgentCampaignIneligible)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return UpdateLinuxDOBindingWithTx(tx, f.buyer.Id, "synthetic-verified-provider-subject")
	}))
	invitation := AgentInvitation{UserId: f.buyer.Id, InviterId: f.root.Id, Source: InvitationSourceLinuxDO, BonusQuota: 200, BonusReason: LedgerReasonAgentInvitationBonus}
	require.NoError(t, DB.Create(&invitation).Error)
	claim, err := ClaimAgentCampaign(f.buyerID, campaignClaim(campaign, "eligible-claim"))
	require.NoError(t, err)
	assert.Equal(t, 50, claim.Quota)
	// Existing legacy gifts remain in addition to the prospective campaign.
	assert.Equal(t, 250, getUserBalances(t, f.buyer.Id).QuotaBonus)
	terms2 := campaignTerms("other-campaign")
	terms2.ExclusionGroup = terms.ExclusionGroup
	other := approvedCampaign(t, f, terms2)
	_, err = ClaimAgentCampaign(f.buyerID, campaignClaim(other, "nonstack-other"))
	require.ErrorIs(t, err, ErrAgentCampaignIneligible)
	second, secondID := campaignBrowser(t, "same-subject", common.RoleCommonUser)
	// Unlink/relink and login-secret rotation cannot reset retained eligibility.
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		if err := UpdateLinuxDOBindingWithTx(tx, f.buyer.Id, ""); err != nil {
			return err
		}
		return UpdateLinuxDOBindingWithTx(tx, second.Id, "synthetic-verified-provider-subject")
	}))
	oldSecret := common.SessionSecret
	common.SessionSecret = "rotated-synthetic-login-secret"
	t.Cleanup(func() { common.SessionSecret = oldSecret })
	require.NoError(t, DB.Create(&AgentInvitation{UserId: second.Id, InviterId: f.root.Id, Source: InvitationSourceLinuxDO}).Error)
	_, err = ClaimAgentCampaign(secondID, campaignClaim(campaign, "related-subject"))
	require.ErrorIs(t, err, ErrAgentCampaignIneligible)
	result, total, err := ListAgentCampaignRecords(f.financeID, "claims", 0, 1, 100, "", true)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	views := result.([]AgentCampaignClaimView)
	assert.Contains(t, views[0].AllowedActions, "campaign.revoke")
	assert.Equal(t, "restore_original_expiry", views[0].RefundTerms)
}

func TestAgentCampaignIndependentAgentDailyAndLifetimeBudgetsPostgres(t *testing.T) {
	f := setupCampaignPostgres(t)
	now := common.GetTimestamp()
	oldClock := agentCampaignNow
	agentCampaignNow = func() int64 { return now }
	t.Cleanup(func() { agentCampaignNow = oldClock })
	terms := campaignTerms("budget-dimensions")
	terms.TotalBudgetQuota = 200
	terms.DailyBudgetQuota = 150
	terms.PerAgentBudgetQuota = 50
	terms.ClaimEndAt = now + 3*86400
	terms.ExpiresAt = now + 4*86400
	campaign := approvedCampaign(t, f, terms)
	_, err := ClaimAgentCampaign(f.buyerID, campaignClaim(campaign, "budget-first"))
	require.NoError(t, err)
	second, secondID := campaignBrowser(t, "budget-second", common.RoleCommonUser)
	_, err = ClaimAgentCampaign(secondID, campaignClaim(campaign, "budget-second-claim"))
	require.ErrorIs(t, err, ErrAgentCampaignBudget, "unattributed cohort has an explicit per-agent cap")
	require.NoError(t, DB.Create(&AgentInvitation{UserId: second.Id, InviterId: f.root.Id, Source: InvitationSourcePassword}).Error)
	_, err = ClaimAgentCampaign(secondID, campaignClaim(campaign, "budget-second-claim"))
	require.NoError(t, err)
	third, thirdID := campaignBrowser(t, "budget-third", common.RoleCommonUser)
	require.NoError(t, DB.Create(&AgentInvitation{UserId: third.Id, InviterId: f.finance.Id, Source: InvitationSourcePassword}).Error)
	_, err = ClaimAgentCampaign(thirdID, campaignClaim(campaign, "budget-third-claim"))
	require.NoError(t, err)
	fourth, fourthID := campaignBrowser(t, "budget-fourth", common.RoleCommonUser)
	require.NoError(t, DB.Create(&AgentInvitation{UserId: fourth.Id, InviterId: second.Id, Source: InvitationSourcePassword}).Error)
	_, err = ClaimAgentCampaign(fourthID, campaignClaim(campaign, "budget-fourth-claim"))
	require.ErrorIs(t, err, ErrAgentCampaignBudget, "UTC day cap reached while global capacity remains")
	agentCampaignNow = func() int64 { return now + 86400 }
	_, err = ClaimAgentCampaign(fourthID, campaignClaim(campaign, "budget-fourth-claim"))
	require.NoError(t, err)
	fifth, fifthID := campaignBrowser(t, "budget-fifth", common.RoleCommonUser)
	require.NoError(t, DB.Create(&AgentInvitation{UserId: fifth.Id, InviterId: third.Id, Source: InvitationSourcePassword}).Error)
	_, err = ClaimAgentCampaign(fifthID, campaignClaim(campaign, "budget-fifth-claim"))
	require.ErrorIs(t, err, ErrAgentCampaignBudget, "lifetime global cap persists across UTC days")
	var budget AgentCampaignBudget
	require.NoError(t, DB.Where("campaign_id = ? AND scope = ?", campaign.ID, "global").First(&budget).Error)
	assert.Equal(t, 200, budget.SpentQuota)
	assert.Zero(t, budget.ReservedQuota)
	var daily []AgentCampaignBudget
	require.NoError(t, DB.Where("campaign_id = ? AND scope = ?", campaign.ID, "utc_day").Order("scope_key").Find(&daily).Error)
	require.Len(t, daily, 2)
	assert.Equal(t, 150, daily[0].SpentQuota)
	assert.Equal(t, 50, daily[1].SpentQuota)
}

func TestAgentCampaignGrantFailureRollsBackProofSourceAndBudgetPostgres(t *testing.T) {
	f := setupCampaignPostgres(t)
	terms := campaignTerms("grant-rollback")
	terms.ManualReview = true
	terms.ReviewWindowSeconds = 300
	campaign := approvedCampaign(t, f, terms)
	claim, err := ClaimAgentCampaign(f.buyerID, campaignClaim(campaign, "rollback-claim"))
	require.NoError(t, err)
	request := AgentCampaignActionRequest{RequestID: "rollback-review", ClaimID: claim.ID, ExpectedVersion: 1, Decision: "approve", Reason: "Synthetic review"}
	auth := campaignProof(t, f.financeID, "campaign.review.approve", request.RequestID, request)
	payload, err := common.Marshal(request)
	require.NoError(t, err)
	before := getUserBalances(t, f.buyer.Id)
	name := "campaign-source-injected-failure"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "bonus_source_states" {
			tx.AddError(ErrBonusSourceInvariant)
		}
	}))
	_, err = ExecuteAgentCampaignOperation(auth, "campaign.review.approve", request.RequestID, payload)
	require.ErrorIs(t, err, ErrBonusSourceInvariant)
	require.NoError(t, DB.Callback().Create().Remove(name))
	var stored AgentCampaignClaim
	require.NoError(t, DB.First(&stored, claim.ID).Error)
	assert.Equal(t, "review_pending", stored.Status)
	assert.EqualValues(t, 1, stored.Version)
	assert.Equal(t, before.Quota, getUserBalances(t, f.buyer.Id).Quota)
	var budgets []AgentCampaignBudget
	require.NoError(t, DB.Where("campaign_id = ?", campaign.ID).Find(&budgets).Error)
	for _, b := range budgets {
		assert.Equal(t, 50, b.ReservedQuota)
		assert.Zero(t, b.SpentQuota)
	}
	out, err := ExecuteAgentCampaignOperation(auth, "campaign.review.approve", request.RequestID, payload)
	require.NoError(t, err, "transaction rollback restores the same single-use proof")
	assert.Equal(t, "granted", out.Claim.Status)
	assert.Equal(t, before.Quota+50, getUserBalances(t, f.buyer.Id).Quota)
}
