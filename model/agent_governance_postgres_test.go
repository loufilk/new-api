package model

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgentGovernancePostgres(t *testing.T) commissionFixture {
	t.Helper()
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL; acceptance runner supplies it and rejects skips")
	}
	f := setupAgentCommissionPostgres(t)
	require.NoError(t, DB.AutoMigrate(AgentGovernanceModels()...))
	for _, schema := range AgentGovernanceModels() {
		statement := &gorm.Statement{DB: DB}
		require.NoError(t, statement.Parse(schema))
		require.NoError(t, DB.Exec("DELETE FROM "+statement.Schema.Table).Error)
	}
	require.NoError(t, DB.AutoMigrate(&AgentInvitation{}, &Redemption{}, &AgentRedemptionAccount{}, &AgentRedemptionOperation{}, &AgentRedemptionLedger{}))
	require.NoError(t, DB.Exec("DELETE FROM agent_invitations").Error)
	p := f.policy
	p.ID = 0
	p.RequestID = "governance-fixture-policy"
	p.GovernanceEnabled = true
	p.GovernanceRequiredForActivation = true
	p.CreditGovernanceRequired = true
	p.GovernanceInactivitySeconds = 86400
	require.NoError(t, ValidateAgentPolicy(&p))
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	f.policy = p
	return f
}
func governanceCommand(f commissionFixture) AgentGovernanceCommand {
	return AgentGovernanceCommand{RequestID: "governance-submit-001", UserID: f.inviter.Id, IdentityDossierReference: "dossier:case-0001", AgreementReference: "agreement:terms-0001", AgreementDigest: strings.Repeat("a", 64), AcceptTerms: true}
}
func governanceProof(t *testing.T, identity AuthSessionIdentity, action string, c AgentGovernanceCommand) AgentTransactionAuthorization {
	t.Helper()
	payload, err := common.Marshal(c)
	require.NoError(t, err)
	prepared, err := PrepareAgentGovernanceOperation(identity, action, c.RequestID, payload)
	require.NoError(t, err)
	_, auth := issueAgentProfileTestProof(t, identity, prepared.VerificationContext)
	return auth
}
func governanceApprove(t *testing.T, f commissionFixture, limits AgentGovernanceRiskLimits) AgentGovernanceCommand {
	t.Helper()
	submit := governanceCommand(f)
	_, err := ExecuteAgentGovernance(governanceProof(t, f.identity, "governance.submit", submit), "governance.submit", submit)
	require.NoError(t, err)
	profile, err := GetAgentProfile(f.inviter.Id)
	require.NoError(t, err)
	var profileVersion int64
	if profile != nil {
		profileVersion = profile.Version
	}
	review := AgentGovernanceCommand{RequestID: "governance-review-001", UserID: f.inviter.Id, ExpectedVersion: 1, ExpectedProfileVersion: profileVersion, Decision: "approved", IdentityVerified: true, AgreementVerified: true, ReviewDueAt: common.GetTimestamp() + 7*86400, Reason: "Independent synthetic verification", RiskLimits: limits}
	_, err = ExecuteAgentGovernance(governanceProof(t, f.rootIdentity, "governance.review", review), "governance.review", review)
	require.NoError(t, err)
	return review
}
func TestAgentGovernanceConsentIndependentReviewPrivacyAndProtectedIdentityPostgres(t *testing.T) {
	f := setupAgentGovernancePostgres(t)
	submit := governanceCommand(f)
	for _, mutate := range []func(*AgentGovernanceCommand){func(c *AgentGovernanceCommand) { c.AcceptTerms = false }, func(c *AgentGovernanceCommand) { c.IdentityDossierReference = "11010519491231002X" }, func(c *AgentGovernanceCommand) { c.AgreementReference = "https://files.example/secret?token=raw" }, func(c *AgentGovernanceCommand) { c.UserID = f.buyer.Id }} {
		bad := submit
		mutate(&bad)
		payload, err := common.Marshal(bad)
		require.NoError(t, err)
		_, err = PrepareAgentGovernanceOperation(f.identity, "governance.submit", bad.RequestID, payload)
		require.Error(t, err)
	}
	before := getUserBalances(t, f.inviter.Id)
	token := Token{UserId: f.inviter.Id, Key: "synthetic-governance-existing-key", Status: common.TokenStatusEnabled}
	require.NoError(t, token.Insert(f.inviter.AuthVersion))
	pat := UserAccessToken{TokenHash: AccessTokenFingerprint("synthetic-governance-existing-pat")}
	require.NoError(t, CreateUserAccessToken(f.inviter.Id, &pat, 20, f.inviter.AuthVersion))
	submitted, err := ExecuteAgentGovernance(governanceProof(t, f.identity, "governance.submit", submit), "governance.submit", submit)
	require.NoError(t, err)
	assert.Equal(t, "pending", submitted.Status)
	assert.Zero(t, submitted.ProfileVersion)
	self, err := GetAgentGovernance(f.identity, f.inviter.Id, false)
	require.NoError(t, err)
	require.Nil(t, self.Profile)
	require.NotNil(t, self.Submission)
	assert.True(t, self.Submission.AcceptTerms)
	encoded, err := common.Marshal(self)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), submit.IdentityDossierReference)
	assert.NotContains(t, string(encoded), submit.AgreementReference)
	finance, err := GetAgentGovernance(f.rootIdentity, f.inviter.Id, true)
	require.NoError(t, err)
	assert.Equal(t, submit.IdentityDossierReference, finance.Submission.IdentityDossierReference)
	_, err = GetAgentGovernance(f.identity, f.buyer.Id, false)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	review := AgentGovernanceCommand{RequestID: "governance-independent-review", UserID: f.inviter.Id, ExpectedVersion: 1, Decision: "approved", IdentityVerified: true, AgreementVerified: true, ReviewDueAt: common.GetTimestamp() + 86400, Reason: "Independent verified dossier"}
	payload, err := common.Marshal(review)
	require.NoError(t, err)
	_, err = PrepareAgentGovernanceOperation(f.identity, "governance.review", review.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	reviewAuth := governanceProof(t, f.rootIdentity, "governance.review", review)
	tampered := review
	tampered.Reason = "Tampered after transaction verification"
	_, err = ExecuteAgentGovernance(reviewAuth, "governance.review", tampered)
	require.ErrorIs(t, err, ErrAgentTransactionProof)
	approved, err := ExecuteAgentGovernance(reviewAuth, "governance.review", review)
	require.NoError(t, err)
	assert.Equal(t, "approved", approved.Status)
	profile, err := GetAgentProfile(f.inviter.Id)
	require.NoError(t, err)
	assert.Nil(t, profile, "review never activates business or promotes a role")
	payload, err = common.Marshal(submit)
	require.NoError(t, err)
	replay, found, err := ReplayAgentGovernanceOperation(f.identity, "governance.submit", submit.RequestID, payload)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, submitted, replay, "replay is immutable original receipt, not a newer approval")
	after := getUserBalances(t, f.inviter.Id)
	assert.Equal(t, before.Role, after.Role)
	assert.Equal(t, before.AuthVersion, after.AuthVersion)
	assert.Equal(t, before.Quota, after.Quota)
	assert.Equal(t, before.QuotaPaid, after.QuotaPaid)
	assert.Equal(t, before.AffQuota, after.AffQuota)
	assert.Equal(t, before.InviterId, after.InviterId)
	require.NoError(t, DB.First(&token, token.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, token.Status)
	var pats int64
	require.NoError(t, DB.Model(&UserAccessToken{}).Where("user_id = ?", f.inviter.Id).Count(&pats).Error)
	assert.EqualValues(t, 1, pats)
	var submission AgentGovernanceSubmission
	require.NoError(t, DB.First(&submission, submitted.SubmissionID).Error)
	require.ErrorIs(t, DB.Model(&submission).Update("accept_terms", false).Error, ErrAgentLedgerImmutable)
	require.ErrorIs(t, DB.Delete(&submission).Error, ErrAgentLedgerImmutable)
	var audit AgentGovernanceAudit
	require.NoError(t, DB.First(&audit).Error)
	require.ErrorIs(t, DB.Delete(&audit).Error, ErrAgentLedgerImmutable)
}
func TestAgentGovernanceRootExceptionCannotInventConsentOrVerificationPostgres(t *testing.T) {
	f := setupAgentGovernancePostgres(t)
	command := AgentGovernanceCommand{RequestID: "governance-waiver-before-consent", UserID: f.inviter.Id, ExpectedVersion: 1, ExceptionReference: "exception:review-0001", ReviewDueAt: common.GetTimestamp() + 86400, Reason: "Exceptional independently logged approval"}
	payload, err := common.Marshal(command)
	require.NoError(t, err)
	_, err = PrepareAgentGovernanceOperation(f.rootIdentity, "governance.override", command.RequestID, payload)
	require.Error(t, err)
	submit := governanceCommand(f)
	_, err = ExecuteAgentGovernance(governanceProof(t, f.identity, "governance.submit", submit), "governance.submit", submit)
	require.NoError(t, err)
	rejection := AgentGovernanceCommand{RequestID: "governance-independent-rejection", UserID: f.inviter.Id, ExpectedVersion: 1, Decision: "rejected", Reason: "Identity evidence not verified"}
	rejected, err := ExecuteAgentGovernance(governanceProof(t, f.rootIdentity, "governance.review", rejection), "governance.review", rejection)
	require.NoError(t, err)
	assert.Equal(t, "rejected", rejected.Status)
	rejectedView, err := GetAgentGovernance(f.identity, f.inviter.Id, false)
	require.NoError(t, err)
	assert.True(t, rejectedView.ReviewRequired)
	command.ExpectedVersion = 2
	command.RequestID = "governance-waiver-after-consent"
	result, err := ExecuteAgentGovernance(governanceProof(t, f.rootIdentity, "governance.override", command), "governance.override", command)
	require.NoError(t, err)
	assert.Equal(t, "exception_approved", result.Status)
	assert.True(t, result.Exceptional)
	view, err := GetAgentGovernance(f.identity, f.inviter.Id, false)
	require.NoError(t, err)
	assert.False(t, view.Governance.IdentityVerified)
	assert.False(t, view.Governance.AgreementVerified)
	assert.True(t, view.Submission.AcceptTerms)
	assert.False(t, view.ReviewRequired)
	// A root's demotion cannot retain old privileged read/replay authority.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.root.Id).Update("role", common.RoleAdminUser).Error)
	payload, err = common.Marshal(command)
	require.NoError(t, err)
	_, _, err = ReplayAgentGovernanceOperation(f.rootIdentity, "governance.override", command.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	_, err = FindAgentGovernanceOperation(f.rootIdentity, command.RequestID)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
}
func TestAgentGovernanceVersionInvalidatesProofWithoutFreezingWalletPostgres(t *testing.T) {
	f := setupAgentGovernancePostgres(t)
	governanceApprove(t, f, AgentGovernanceRiskLimits{})
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		_, err := UpdateAgentProfileTx(tx, f.rootIdentity, f.inviter.Id, AgentProfileMutation{Status: AgentProfileActive, Reason: "Separate explicit profile activation"})
		return err
	}))
	profile, err := GetAgentProfile(f.inviter.Id)
	require.NoError(t, err)
	require.NotNil(t, profile)
	context, err := NewAgentTransactionContext("commission.withdraw.request", "governance-stale-business-proof", f.inviter.Id, profile.Version, f.policy.ID, "USD", "20", 0, "withdrawal", "pending", 1, "synthetic")
	require.NoError(t, err)
	_, stale := issueAgentProfileTestProof(t, f.identity, context)
	next := governanceCommand(f)
	next.RequestID = "governance-reconsent-new-terms"
	next.ExpectedVersion = 2
	next.AgreementDigest = strings.Repeat("b", 64)
	receipt, err := ExecuteAgentGovernance(governanceProof(t, f.identity, "governance.submit", next), "governance.submit", next)
	require.NoError(t, err)
	assert.Equal(t, profile.Version+1, receipt.ProfileVersion)
	err = DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		return ConsumeAgentTransactionAuthorizationTx(tx, stale, context)
	})
	require.ErrorIs(t, err, ErrAgentProfileConflict)
	changed, err := GetAgentProfile(f.inviter.Id)
	require.NoError(t, err)
	assert.Equal(t, profile.Status, changed.Status)
	assert.Equal(t, profile.SalesFrozen, changed.SalesFrozen)
	assert.Equal(t, profile.SettlementFrozen, changed.SettlementFrozen)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, f.identity) }))
	review := AgentGovernanceCommand{RequestID: "governance-stale-profile-review", UserID: f.inviter.Id, ExpectedVersion: 3, ExpectedProfileVersion: profile.Version, Decision: "approved", IdentityVerified: true, AgreementVerified: true, ReviewDueAt: common.GetTimestamp() + 86400, Reason: "Stale profile review must fail"}
	payload, err := common.Marshal(review)
	require.NoError(t, err)
	_, err = PrepareAgentGovernanceOperation(f.rootIdentity, "governance.review", review.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentProfileConflict)
}
func TestAgentGovernanceProofRollbackExpiryAndImmutableMigrationPostgres(t *testing.T) {
	f := setupAgentGovernancePostgres(t)
	c := governanceCommand(f)
	auth := governanceProof(t, f.identity, "governance.submit", c)
	require.NoError(t, DB.Exec(`CREATE FUNCTION governance_fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic governance audit failure'; END $$`).Error)
	require.NoError(t, DB.Exec(`CREATE TRIGGER governance_fail_audit BEFORE INSERT ON agent_governance_audits FOR EACH ROW EXECUTE FUNCTION governance_fail_audit()`).Error)
	_, err := ExecuteAgentGovernance(auth, "governance.submit", c)
	require.Error(t, err)
	var count int64
	require.NoError(t, DB.Model(&AgentGovernanceSubmission{}).Count(&count).Error)
	assert.Zero(t, count)
	var flow AuthFlow
	require.NoError(t, DB.Where("token_hash = ?", authFlowTokenHash(auth.token)).First(&flow).Error)
	assert.Nil(t, flow.ConsumedAt)
	require.NoError(t, DB.Exec(`DROP TRIGGER governance_fail_audit ON agent_governance_audits`).Error)
	require.NoError(t, DB.Exec(`DROP FUNCTION governance_fail_audit()`).Error)
	_, err = ExecuteAgentGovernance(auth, "governance.submit", c)
	require.NoError(t, err)
	next := c
	next.RequestID = "governance-expired-submit"
	next.ExpectedVersion = 1
	expired := governanceProof(t, f.identity, "governance.submit", next)
	require.NoError(t, DB.Model(&AuthFlow{}).Where("token_hash = ?", authFlowTokenHash(expired.token)).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	_, err = ExecuteAgentGovernance(expired, "governance.submit", next)
	require.ErrorIs(t, err, ErrAuthFlowExpired)
	next.RequestID = "governance-disabled-factor"
	disabled := governanceProof(t, f.identity, "governance.submit", next)
	require.NoError(t, DB.Model(&TwoFA{}).Where("user_id = ?", f.inviter.Id).Update("is_enabled", false).Error)
	_, err = ExecuteAgentGovernance(disabled, "governance.submit", next)
	require.ErrorIs(t, err, ErrAgentTransactionMethod)
	before := getUserBalances(t, f.inviter.Id)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(AgentGovernanceModels()...))
	}
	after := getUserBalances(t, f.inviter.Id)
	assert.Equal(t, before.Quota, after.Quota)
	assert.Equal(t, before.AffQuota, after.AffQuota)
	require.NoError(t, DB.Model(&AgentGovernanceSubmission{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, DB.Model(&AgentGovernanceRiskUsage{}).Count(&count).Error)
	assert.Zero(t, count)
}
func governanceRiskTx(f commissionFixture, requestID, kind string, quota, now int64, rollback bool) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		var user User
		if err := lockForUpdate(tx).First(&user, f.inviter.Id).Error; err != nil {
			return err
		}
		if _, err := LockAgentProfileTx(tx, user.Id); err != nil {
			return err
		}
		if err := consumeAgentGovernanceRiskAtTx(tx, user.Id, requestID, kind, quota, now); err != nil {
			return err
		}
		if rollback {
			return errors.New("synthetic risk rollback")
		}
		return nil
	})
}
func TestAgentGovernanceGrossRiskConcurrentReplayRollbackUTCPostgres(t *testing.T) {
	f := setupAgentGovernancePostgres(t)
	governanceApprove(t, f, AgentGovernanceRiskLimits{Enabled: true, DailyIssuanceQuota: 100, DailyTransferQuota: 50})
	now := common.GetTimestamp()
	day := time.Unix(now, 0).UTC().Truncate(24 * time.Hour).Unix()
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for i := range 2 {
		workers.Go(func() {
			<-start
			results <- governanceRiskTx(f, fmt.Sprintf("risk-concurrent-%d", i), "issuance", 60, now, false)
		})
	}
	close(start)
	workers.Wait()
	close(results)
	successes, limited := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrAgentGovernanceRiskLimit)
			limited++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, limited)
	var usage AgentGovernanceRiskUsage
	require.NoError(t, DB.First(&usage).Error)
	require.NoError(t, governanceRiskTx(f, usage.RequestID, "issuance", 60, day+86400, false))
	require.ErrorIs(t, governanceRiskTx(f, usage.RequestID, "issuance", 61, now, false), ErrAgentGovernanceConflict)
	require.Error(t, governanceRiskTx(f, "risk-rollback-retry", "issuance", 30, now, true))
	require.NoError(t, governanceRiskTx(f, "risk-rollback-retry", "issuance", 30, now, false))
	require.ErrorIs(t, DB.Model(&usage).Update("quota", 0).Error, ErrAgentLedgerImmutable)
	require.ErrorIs(t, DB.Delete(&usage).Error, ErrAgentLedgerImmutable)
	require.ErrorIs(t, governanceRiskTx(f, "risk-canceled-budget-cycle", "issuance", 11, now, false), ErrAgentGovernanceRiskLimit, "cancellation cannot restore consumed gross volume")
	require.NoError(t, governanceRiskTx(f, "risk-independent-transfer", "transfer", 40, now, false))
	require.ErrorIs(t, governanceRiskTx(f, "risk-transfer-over-cap", "transfer", 11, now, false), ErrAgentGovernanceRiskLimit)
	require.NoError(t, governanceRiskTx(f, "risk-next-utc-day", "issuance", 100, day+86400, false))
	var count int64
	require.NoError(t, DB.Model(&AgentGovernanceRiskUsage{}).Count(&count).Error)
	assert.EqualValues(t, 4, count)
	view, err := GetAgentGovernance(f.identity, f.inviter.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "90", view.DailyRisk.GrossIssuanceQuota)
	require.NotNil(t, view.DailyRisk.RemainingIssuanceQuota)
	assert.Equal(t, "10", *view.DailyRisk.RemainingIssuanceQuota)
}
func TestAgentGovernanceInactivityFlagsDoNotRevokeCustomerOrPersonalRightsPostgres(t *testing.T) {
	f := setupAgentGovernancePostgres(t)
	// Feature OFF leaves existing agents and existing wallets unchanged.
	off := f.policy
	off.ID = 0
	off.RequestID = "governance-off-policy"
	off.GovernanceEnabled = false
	off.GovernanceRequiredForActivation = false
	off.CreditGovernanceRequired = false
	off.GovernanceInactivitySeconds = 0
	require.NoError(t, DB.Create(&off).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", off.ID).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return RequireAgentGovernanceActivationTx(tx, f.inviter.Id) }))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return RequireAgentGovernanceCreditTx(tx, f.inviter.Id) }))
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", f.policy.ID).Error)
	require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { return RequireAgentGovernanceActivationTx(tx, f.inviter.Id) }), ErrAgentGovernanceRequired)
	require.NoError(t, governanceRiskTx(f, "legacy-no-governance-compat", "issuance", 10, common.GetTimestamp(), false))
	governanceApprove(t, f, AgentGovernanceRiskLimits{})
	now := common.GetTimestamp()
	require.NoError(t, DB.Model(&AgentGovernance{}).Where("user_id = ?", f.inviter.Id).Updates(map[string]any{"reviewed_at": now - 86401, "review_due_at": now - 1}).Error)
	before := getUserBalances(t, f.inviter.Id)
	code := Redemption{UserId: f.inviter.Id, Key: "synthetic-inactivity-bound-code", Status: common.RedemptionCodeStatusEnabled, Quota: 20, CustomerUserID: f.buyer.Id, SourceProtected: true, BudgetAgentID: f.inviter.Id}
	require.NoError(t, DB.Create(&code).Error)
	view, err := GetAgentGovernance(f.identity, f.inviter.Id, false)
	require.NoError(t, err)
	assert.True(t, view.ReviewRequired)
	assert.Contains(t, view.ReviewReasons, "review_expired")
	assert.Contains(t, view.ReviewReasons, "long_inactivity")
	require.ErrorIs(t, governanceRiskTx(f, "inactive-new-issuance-blocked", "issuance", 10, now, false), ErrAgentGovernanceRequired)
	require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { return RequireAgentGovernanceCreditTx(tx, f.inviter.Id) }), ErrAgentGovernanceRequired)
	require.NoError(t, DB.First(&code, code.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
	assert.Equal(t, f.buyer.Id, code.CustomerUserID)
	after := getUserBalances(t, f.inviter.Id)
	assert.Equal(t, before.Quota, after.Quota)
	assert.Equal(t, before.AuthVersion, after.AuthVersion)
	assert.Equal(t, before.Status, after.Status)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, f.identity) }))
}
