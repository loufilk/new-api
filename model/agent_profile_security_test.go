package model

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgentProfileSecurityTest(t *testing.T) (User, User, AuthSessionIdentity, AuthSessionIdentity) {
	t.Helper()
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6")
	}
	setupLedgerTestState(t)
	require.NoError(t, DB.AutoMigrate(&AgentProfile{}, &AgentProfileAudit{}, &AgentMigrationIssue{}, &AgentProfileOperation{}, &AgentPolicy{}, &AgentPolicyState{}))
	for _, table := range []string{"agent_profiles", "agent_profile_audits", "agent_migration_issues", "agent_profile_operations", "agent_policies", "agent_policy_states"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	oldSecret := common.SessionSecret
	common.SessionSecret = "synthetic-agent-financial-proof-test-secret"
	t.Cleanup(func() { common.SessionSecret = oldSecret })
	root := User{Username: "profile-root", AffCode: "profile-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	user := User{Username: "profile-user", AffCode: "profile-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, Quota: 600, QuotaPaid: 100, QuotaBonus: 200, QuotaReward: 300, AffQuota: 70}
	require.NoError(t, DB.Create(&root).Error)
	require.NoError(t, DB.Create(&user).Error)
	identities := make([]AuthSessionIdentity, 0, 2)
	for _, actor := range []User{root, user} {
		session := UserSession{SID: "profile-session-" + actor.Username, UserID: actor.Id, Version: 1, UserAuthVersion: 1, Status: UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix(), RefreshHash: "synthetic-session", LoginMethod: "password"}
		require.NoError(t, DB.Create(&session).Error)
		require.NoError(t, DB.Create(&TwoFA{UserId: actor.Id, IsEnabled: true, Secret: "synthetic-not-a-real-secret"}).Error)
		identities = append(identities, AuthSessionIdentity{UserID: actor.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1})
	}
	return root, user, identities[0], identities[1]
}

func issueAgentProfileTestProof(t *testing.T, identity AuthSessionIdentity, context AgentTransactionContext) (string, AgentTransactionAuthorization) {
	t.Helper()
	hash, err := AgentTransactionContextHash(context)
	require.NoError(t, err)
	token, _, err := IssueAgentTransactionProof(identity, hash, "2fa")
	require.NoError(t, err)
	authorization, err := PrepareAgentTransactionAuthorization(token, identity, context)
	require.NoError(t, err)
	return token, authorization
}

func TestAgentProfileMigrationPreservesMoneyAndCredentialsPostgres(t *testing.T) {
	_, user, _, _ := setupAgentProfileSecurityTest(t)
	require.NoError(t, DB.Model(&user).Updates(map[string]any{"role": common.RoleAgentUser, "aff_quota": -7}).Error)
	enabled := Token{UserId: user.Id, Key: "synthetic-enabled-migration-key", Status: common.TokenStatusEnabled}
	disabled := Token{UserId: user.Id, Key: "synthetic-disabled-migration-key", Status: common.TokenStatusDisabled}
	require.NoError(t, DB.Create(&enabled).Error)
	require.NoError(t, DB.Create(&disabled).Error)
	pat := UserAccessToken{UserId: user.Id, TokenHash: AccessTokenFingerprint("synthetic-migration-pat"), Name: "synthetic"}
	require.NoError(t, DB.Create(&pat).Error)
	account := AgentRedemptionAccount{AgentUserID: user.Id, AllocatedQuota: 50, AvailableQuota: 30, ReservedQuota: 15, SpentQuota: 5}
	orphan := AgentRedemptionAccount{AgentUserID: user.Id + 10000, AllocatedQuota: -3, AvailableQuota: -3}
	require.NoError(t, DB.Create(&account).Error)
	require.NoError(t, DB.Create(&orphan).Error)
	for range 2 {
		require.NoError(t, MigrateLegacyAgentProfiles(DB))
	}
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, user.AuthVersion, stored.AuthVersion)
	assert.Equal(t, []int{600, 100, 200, 300, -7}, []int{stored.Quota, stored.QuotaPaid, stored.QuotaBonus, stored.QuotaReward, stored.AffQuota})
	require.NoError(t, DB.First(&enabled, enabled.Id).Error)
	require.NoError(t, DB.First(&disabled, disabled.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, enabled.Status)
	assert.Equal(t, common.TokenStatusDisabled, disabled.Status)
	var pats int64
	require.NoError(t, DB.Model(&UserAccessToken{}).Where("user_id = ?", user.Id).Count(&pats).Error)
	assert.EqualValues(t, 1, pats)
	var accounts []AgentRedemptionAccount
	require.NoError(t, DB.Order("agent_user_id").Find(&accounts).Error)
	require.Len(t, accounts, 2)
	assert.Equal(t, []int{30, 15, 5, 50}, []int{accounts[0].AvailableQuota, accounts[0].ReservedQuota, accounts[0].SpentQuota, accounts[0].AllocatedQuota})
	assert.Equal(t, -3, accounts[1].AvailableQuota)
	profile, err := GetAgentProfile(user.Id)
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, AgentProfileActive, profile.Status)
	assert.True(t, profile.SalesFrozen)
	assert.True(t, profile.SettlementFrozen)
	var issues, audits int64
	require.NoError(t, DB.Model(&AgentMigrationIssue{}).Count(&issues).Error)
	require.NoError(t, DB.Model(&AgentProfileAudit{}).Count(&audits).Error)
	assert.EqualValues(t, 3, issues)
	assert.EqualValues(t, 2, audits)
}

func TestAgentProfileLifecycleDoesNotRevokePersonalConsumptionPostgres(t *testing.T) {
	root, user, rootIdentity, userIdentity := setupAgentProfileSecurityTest(t)
	enabled := Token{UserId: user.Id, Key: "synthetic-profile-active-key", Status: common.TokenStatusEnabled}
	disabled := Token{UserId: user.Id, Key: "synthetic-profile-disabled-key", Status: common.TokenStatusDisabled}
	require.NoError(t, enabled.Insert(user.AuthVersion))
	require.NoError(t, disabled.Insert(user.AuthVersion))
	pat := UserAccessToken{TokenHash: AccessTokenFingerprint("synthetic-profile-existing-pat")}
	require.NoError(t, CreateUserAccessToken(user.Id, &pat, 20, user.AuthVersion))
	var profile *AgentProfile
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		profile, err = UpdateAgentProfileTx(tx, rootIdentity, user.Id, AgentProfileMutation{Status: AgentProfileActive, Reason: "Enable business"})
		return err
	}))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := RequireAgentCapabilityTx(tx, userIdentity, user.Id, profile.Version, AgentCapabilitySales)
		return err
	}))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := UpdateAgentProfileTx(tx, rootIdentity, user.Id, AgentProfileMutation{ExpectedVersion: profile.Version, Status: AgentProfileExited, SalesFrozen: true, SettlementFrozen: false, Reason: "Exit business"})
		return err
	}))
	profile, err := GetAgentProfile(user.Id)
	require.NoError(t, err)
	assert.True(t, profile.Allows(AgentCapabilityHistory))
	assert.True(t, profile.Allows(AgentCapabilitySettlement))
	assert.False(t, profile.Allows(AgentCapabilitySales))
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, user.AuthVersion, stored.AuthVersion)
	assert.Equal(t, 600, stored.Quota)
	require.NoError(t, DB.First(&enabled, enabled.Id).Error)
	require.NoError(t, DB.First(&disabled, disabled.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, enabled.Status)
	assert.Equal(t, common.TokenStatusDisabled, disabled.Status)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, userIdentity) }))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := ApplyUserManagementActionTx(tx, root.Id, stored, "disable", rootIdentity)
		return err
	}))
	require.Error(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, userIdentity) }))
}

func TestAgentTransactionAuthorizationAtomicRollbackAndReplayPostgres(t *testing.T) {
	_, user, _, identity := setupAgentProfileSecurityTest(t)
	context, err := NewAgentTransactionContext("commission.convert.wallet", "atomic-proof-request", user.Id, 0, 0, "USD", "20", 100, "conversion", "draft-1", 1, map[string]any{"amount": "20", "quota": 100})
	require.NoError(t, err)
	token, authorization := issueAgentProfileTestProof(t, identity, context)
	injected := errors.New("injected accounting rollback")
	err = DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, context); err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("quota_paid", 999).Error; err != nil {
			return err
		}
		return injected
	})
	require.ErrorIs(t, err, injected)
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, 100, stored.QuotaPaid)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		return ConsumeAgentTransactionAuthorizationTx(tx, authorization, context)
	}))
	// Preparation is not a second authorization. It allows the exact business
	// request to reach its idempotent replay branch even after the proof is used.
	authorization, err = PrepareAgentTransactionAuthorization(token, identity, context)
	require.NoError(t, err)
	require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { return ConsumeAgentTransactionAuthorizationTx(tx, authorization, context) }), ErrAuthFlowConsumed)
}

func TestAgentTransactionAuthorizationRejectsChangedBindingPostgres(t *testing.T) {
	_, user, _, identity := setupAgentProfileSecurityTest(t)
	context, err := NewAgentTransactionContext("commission.convert.wallet", "binding-proof-request", user.Id, 0, 0, "USD", "20", 100, "conversion", "draft-1", 1, map[string]any{"amount": "20", "quota": 100})
	require.NoError(t, err)
	token, _ := issueAgentProfileTestProof(t, identity, context)
	for _, test := range []struct {
		name   string
		change func(*AgentTransactionContext)
	}{
		{"action", func(c *AgentTransactionContext) { c.Action = "recipient.change" }},
		{"request", func(c *AgentTransactionContext) { c.RequestID = "other-binding-request" }},
		{"amount", func(c *AgentTransactionContext) { c.AmountMinor = "21" }},
		{"currency", func(c *AgentTransactionContext) { c.Currency = "EUR" }},
		{"quota", func(c *AgentTransactionContext) { c.Quota = 101 }},
		{"target", func(c *AgentTransactionContext) { c.TargetID = "draft-2" }},
		{"target version", func(c *AgentTransactionContext) { c.TargetVersion = 2 }},
		{"recipient", func(c *AgentTransactionContext) { c.RecipientVersion = 1 }},
		{"parameters", func(c *AgentTransactionContext) { c.ParametersHash, _ = AgentTransactionParametersHash("different") }},
		{"policy", func(c *AgentTransactionContext) { c.PolicyVersion = 1 }},
		{"qualification", func(c *AgentTransactionContext) { c.ProfileVersion = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := context
			test.change(&changed)
			authorization, err := PrepareAgentTransactionAuthorization(token, identity, changed)
			require.NoError(t, err)
			require.Error(t, DB.Transaction(func(tx *gorm.DB) error { return ConsumeAgentTransactionAuthorizationTx(tx, authorization, changed) }))
		})
	}
	authorization, err := PrepareAgentTransactionAuthorization(token, identity, context)
	require.NoError(t, err)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ConsumeAgentTransactionAuthorizationTx(tx, authorization, context) }))
}

func TestAgentTransactionProofSingleUseAcrossConcurrentConnectionsPostgres(t *testing.T) {
	_, user, _, identity := setupAgentProfileSecurityTest(t)
	context, err := NewAgentTransactionContext("commission.withdraw.request", "concurrent-proof-request", user.Id, 0, 0, "USD", "20", 0, "withdrawal", "draft-1", 1, "fixed")
	require.NoError(t, err)
	_, authorization := issueAgentProfileTestProof(t, identity, context)
	results := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			results <- DB.Transaction(func(tx *gorm.DB) error { return ConsumeAgentTransactionAuthorizationTx(tx, authorization, context) })
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var successes, consumed int
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrAuthFlowConsumed)
			consumed++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, consumed)
}

func TestAgentTransactionProofRevocationExpiryAndOptionalProfilePostgres(t *testing.T) {
	for _, scenario := range []string{"profile_created", "session_revoked", "session_version", "auth_version", "factor_removed", "expired", "pat"} {
		t.Run(scenario, func(t *testing.T) {
			_, user, _, identity := setupAgentProfileSecurityTest(t)
			context, err := NewAgentTransactionContext("commission.convert.wallet", "revocation-proof-request", user.Id, 0, 0, "USD", "20", 10, "conversion", "draft-1", 1, "fixed")
			require.NoError(t, err)
			_, authorization := issueAgentProfileTestProof(t, identity, context)
			switch scenario {
			case "profile_created":
				require.NoError(t, DB.Create(&AgentProfile{UserID: user.Id, Status: AgentProfileActive, Version: 1}).Error)
			case "session_revoked":
				require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", identity.SessionID).Update("status", UserSessionStatusRevoked).Error)
			case "session_version":
				require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", identity.SessionID).Update("version", 2).Error)
			case "auth_version":
				require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("auth_version", 2).Error)
			case "factor_removed":
				require.NoError(t, DB.Model(&TwoFA{}).Where("user_id = ?", user.Id).Update("is_enabled", false).Error)
			case "expired":
				require.NoError(t, DB.Model(&AuthFlow{}).Where("purpose = ?", agentTransactionFlowPurpose).Update("expires_at", time.Now().Add(-time.Second)).Error)
			case "pat":
				authorization.identity.SessionID = AccessTokenSessionID(1)
			}
			require.Error(t, DB.Transaction(func(tx *gorm.DB) error { return ConsumeAgentTransactionAuthorizationTx(tx, authorization, context) }))
		})
	}
}

func TestAgentInvitationPolicyAuthorizationAtomicAndReplayPostgres(t *testing.T) {
	root, _, identity, _ := setupAgentProfileSecurityTest(t)
	require.NoError(t, DB.AutoMigrate(&AgentInvitationPolicy{}, &AgentInvitationPolicyAudit{}, &AgentInvitationPolicyOperation{}))
	for _, table := range []string{"agent_invitation_policies", "agent_invitation_policy_audits", "agent_invitation_policy_operations"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	original, err := UpdateAgentInvitationPolicy(root.Id, true, 100)
	require.NoError(t, err)
	mutation := AgentInvitationPolicyMutation{ExpectedRevision: original.Revision, Enabled: true, Quota: 500}
	encoded, err := common.Marshal(mutation)
	require.NoError(t, err)
	prepared, err := PrepareAgentProfileOperation(identity, "invitation.policy.update", "strong-invitation-policy", encoded)
	require.NoError(t, err)
	_, authorization := issueAgentProfileTestProof(t, identity, prepared.VerificationContext)
	failure := errors.New("injected immutable policy audit failure")
	callback := "agent-financial-invitation-policy-rollback"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "agent_invitation_policy_audits" {
			tx.AddError(failure)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Create().Remove(callback) })
	_, err = UpdateAgentInvitationPolicyAuthorized(authorization, "strong-invitation-policy", mutation)
	require.ErrorIs(t, err, failure)
	unchanged, err := GetAgentInvitationPolicy(root.Id)
	require.NoError(t, err)
	assert.Equal(t, original.Revision, unchanged.Revision)
	assert.Equal(t, 100, unchanged.Quota)
	require.NoError(t, DB.Callback().Create().Remove(callback))
	for range 2 {
		changed, err := UpdateAgentInvitationPolicyAuthorized(authorization, "strong-invitation-policy", mutation)
		require.NoError(t, err, "rollback must restore proof; exact committed retries are safe")
		assert.EqualValues(t, 2, changed.Revision)
		assert.Equal(t, 500, changed.Quota)
	}
	changed := mutation
	changed.Quota = 501
	_, err = UpdateAgentInvitationPolicyAuthorized(authorization, "strong-invitation-policy", changed)
	require.ErrorIs(t, err, ErrAgentProfileConflict)
	status, err := FindAgentProfileOperation(identity, "strong-invitation-policy")
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, "invitation.policy.update", status.Action)
	assert.Equal(t, "completed", status.Status)
	var audits int64
	require.NoError(t, DB.Model(&AgentInvitationPolicyAudit{}).Count(&audits).Error)
	assert.EqualValues(t, 2, audits)
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", identity.SessionID).Update("status", UserSessionStatusRevoked).Error)
	_, err = UpdateAgentInvitationPolicyAuthorized(authorization, "strong-invitation-policy", mutation)
	require.Error(t, err, "a consumed request cannot be replayed by a revoked session")
}

func TestAgentProfileMigrationInvalidOwnerIsRecordedNotErasedPostgres(t *testing.T) {
	setupAgentProfileSecurityTest(t)
	invalid := AgentRedemptionAccount{AgentUserID: -41, AllocatedQuota: -12, AvailableQuota: -12}
	require.NoError(t, DB.Create(&invalid).Error)
	for range 2 {
		require.NoError(t, MigrateLegacyAgentProfiles(DB))
	}
	var stored AgentRedemptionAccount
	require.NoError(t, DB.Where("agent_user_id = ?", -41).First(&stored).Error)
	assert.Equal(t, -12, stored.AvailableQuota)
	var profiles int64
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", -41).Count(&profiles).Error)
	assert.Zero(t, profiles, "an invalid historical owner is never granted an entitlement")
	var issues []AgentMigrationIssue
	require.NoError(t, DB.Where("user_id = ?", -41).Find(&issues).Error)
	assert.Len(t, issues, 2, "orphan and invalid-budget evidence survive an idempotent migration")
}
