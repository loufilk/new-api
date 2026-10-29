package model

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgentInvitationTest(t *testing.T) (User, User) {
	t.Helper()
	setupLedgerTestState(t)
	previousPayment := *operation_setting.GetPaymentSetting()
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() { *operation_setting.GetPaymentSetting() = previousPayment })
	require.NoError(t, DB.AutoMigrate(&AgentInvitation{}, &AgentInvitationPolicy{}, &AgentInvitationPolicyAudit{}))
	for _, table := range []string{"agent_invitations", "agent_invitation_policies", "agent_invitation_policy_audits", "external_identity_claims"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	root := User{Username: "invitation-root", AffCode: "invitation-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	agent := User{Username: "invitation-agent", AffCode: "invitation-agent", Role: common.RoleAgentUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&root).Error)
	require.NoError(t, DB.Create(&agent).Error)
	return root, agent
}

func TestAgentInvitationComplianceGateDoesNotGrantRetroactively(t *testing.T) {
	root, agent := setupAgentInvitationTest(t)
	_, err := UpdateAgentInvitationPolicy(root.Id, true, 800)
	require.NoError(t, err)
	operation_setting.GetPaymentSetting().ComplianceConfirmed = false
	policy, err := GetAgentInvitationPolicy(root.Id)
	require.NoError(t, err)
	assert.False(t, policy.PaymentComplianceConfirmed)
	assert.True(t, policy.Enabled, "the root policy and effective payment gate are independent")
	user := &User{Username: "compliance-invitee"}
	require.NoError(t, user.Insert(agent.Id))
	var invitation AgentInvitation
	require.NoError(t, DB.Where("user_id = ?", user.Id).First(&invitation).Error)
	assert.Equal(t, agent.Id, invitation.InviterId)
	assert.Zero(t, invitation.BonusQuota)
	assert.Equal(t, "payment_compliance_required", invitation.BonusReason)
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	user.FinishInsert(agent.Id)
	user.FinalizeOAuthUserCreation(agent.Id)
	stored := getUserBalances(t, user.Id)
	assert.Zero(t, stored.QuotaBonus)
	assert.Zero(t, countLedgerRows(t, fmt.Sprintf("agent_invitation_bonus:%d", user.Id)))
	assert.Equal(t, 1, getUserBalances(t, agent.Id).AffCount)
}

func TestAgentInvitationPolicyRootOnlyDefaultAndAudit(t *testing.T) {
	root, agent := setupAgentInvitationTest(t)
	policy, err := GetAgentInvitationPolicy(root.Id)
	require.NoError(t, err)
	assert.False(t, policy.Enabled)
	assert.Zero(t, policy.Quota)
	assert.Zero(t, policy.Revision)
	for _, role := range []int{common.RoleCommonUser, common.RoleAgentUser, common.RoleAdminUser} {
		require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.Id).Update("role", role).Error)
		_, err = UpdateAgentInvitationPolicy(agent.Id, true, 100)
		require.ErrorIs(t, err, ErrAgentInvitationPermission)
		_, err = GetAgentInvitationPolicy(agent.Id)
		require.ErrorIs(t, err, ErrAgentInvitationPermission)
	}
	for _, quota := range []int{-1, common.MaxWalletQuota + 1} {
		_, err = UpdateAgentInvitationPolicy(root.Id, true, quota)
		require.ErrorIs(t, err, ErrAgentInvitationPolicy)
	}
	policy, err = UpdateAgentInvitationPolicy(root.Id, true, 123)
	require.NoError(t, err)
	assert.True(t, policy.Enabled)
	assert.Equal(t, 123, policy.Quota)
	assert.EqualValues(t, 1, policy.Revision)
	assert.Equal(t, root.Id, policy.UpdatedBy)
	_, err = UpdateAgentInvitationPolicy(root.Id, true, 123)
	require.NoError(t, err)
	var audits []AgentInvitationPolicyAudit
	require.NoError(t, DB.Order("id").Find(&audits).Error)
	require.Len(t, audits, 1)
	assert.Zero(t, audits[0].PreviousQuota)
	assert.Equal(t, 123, audits[0].Quota)
	require.ErrorIs(t, DB.Model(&audits[0]).Update("quota", 456).Error, ErrAgentInvitationImmutable)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", root.Id).Update("status", common.UserStatusDisabled).Error)
	_, err = UpdateAgentInvitationPolicy(root.Id, false, 0)
	require.ErrorIs(t, err, ErrAgentInvitationPermission)
}

func TestAgentInvitationPolicyAuditFailureRollsBackChange(t *testing.T) {
	root, _ := setupAgentInvitationTest(t)
	_, err := UpdateAgentInvitationPolicy(root.Id, true, 100)
	require.NoError(t, err)
	failure := errors.New("injected policy audit failure")
	callback := "agent-invitation-policy-audit-failure"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "agent_invitation_policy_audits" {
			tx.AddError(failure)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Create().Remove(callback) })
	_, err = UpdateAgentInvitationPolicy(root.Id, true, 200)
	require.ErrorIs(t, err, failure)
	policy, err := GetAgentInvitationPolicy(root.Id)
	require.NoError(t, err)
	assert.Equal(t, 100, policy.Quota)
	assert.EqualValues(t, 1, policy.Revision)
}

func TestAgentInvitationPolicyRejectsConcurrentLogoutAndRoleCycle(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires PostgreSQL row locks and concurrent connections")
	}
	for _, scenario := range []string{"logout", "role_cycle"} {
		t.Run(scenario, func(t *testing.T) {
			root, _ := setupAgentInvitationTest(t)
			_, err := UpdateAgentInvitationPolicy(root.Id, false, 10)
			require.NoError(t, err)
			session := newTestUserSession("agent-policy-race-"+scenario, root.Id, time.Now().Unix())
			require.NoError(t, CreateUserSession(session))
			identity := AuthSessionIdentity{UserID: root.Id, SessionID: session.SID, UserAuthVersion: root.AuthVersion, SessionVersion: session.Version}
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, identity) }))
			// The request has passed middleware with this identity. Suspend it
			// before the model's authoritative read, then commit revocation.
			beforeRead, resumeRead := make(chan struct{}), make(chan struct{})
			resume := sync.OnceFunc(func() { close(resumeRead) })
			defer resume()
			var intercepted atomic.Bool
			callback := "agent-invitation-policy-identity-barrier"
			require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "users" && intercepted.CompareAndSwap(false, true) {
					close(beforeRead)
					<-resumeRead
				}
			}))
			t.Cleanup(func() { _ = DB.Callback().Query().Remove(callback) })
			done := make(chan error, 1)
			go func() { _, err := UpdateAgentInvitationPolicy(root.Id, true, 999, identity); done <- err }()
			<-beforeRead
			if scenario == "logout" {
				revoked, err := RevokeUserSession(root.Id, session.SID, "test_logout")
				require.NoError(t, err)
				require.True(t, revoked)
			} else {
				// A role cycle back to root must not restore an old request's
				// authority merely because the current numeric role matches.
				require.NoError(t, DB.Model(&User{}).Where("id = ?", root.Id).Updates(map[string]any{"role": common.RoleCommonUser, "auth_version": root.AuthVersion + 1}).Error)
				require.NoError(t, DB.Model(&User{}).Where("id = ?", root.Id).Updates(map[string]any{"role": common.RoleRootUser, "auth_version": root.AuthVersion + 2}).Error)
			}
			resume()
			require.ErrorIs(t, <-done, ErrAgentInvitationPermission)
			policy, err := GetAgentInvitationPolicy(root.Id)
			require.NoError(t, err)
			assert.False(t, policy.Enabled)
			assert.Equal(t, 10, policy.Quota)
			assert.EqualValues(t, 1, policy.Revision)
			var count int64
			require.NoError(t, DB.Model(&AgentInvitationPolicyAudit{}).Count(&count).Error)
			assert.EqualValues(t, 1, count, "rejected stale requests create no policy audit")
		})
	}
}

func TestAgentInvitationValidatesInviterAndPolicyAtCreation(t *testing.T) {
	for _, tc := range []struct {
		name             string
		role, status     int
		deleted, enabled bool
		wantInvitation   bool
		wantBonus        int
	}{
		{"enabled_agent", common.RoleAgentUser, common.UserStatusEnabled, false, true, true, 321},
		{"policy_disabled", common.RoleAgentUser, common.UserStatusEnabled, false, false, true, 0},
		{"common_inviter", common.RoleCommonUser, common.UserStatusEnabled, false, true, true, 0},
		{"admin_inviter", common.RoleAdminUser, common.UserStatusEnabled, false, true, true, 0},
		{"root_inviter", common.RoleRootUser, common.UserStatusEnabled, false, true, true, 0},
		{"disabled_agent", common.RoleAgentUser, common.UserStatusDisabled, false, true, false, 0},
		{"deleted_agent", common.RoleAgentUser, common.UserStatusEnabled, true, true, false, 0},
		{"unknown_role", 6, common.UserStatusEnabled, false, true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, agent := setupAgentInvitationTest(t)
			_, err := UpdateAgentInvitationPolicy(root.Id, tc.enabled, 321)
			require.NoError(t, err)
			require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.Id).Updates(map[string]any{"role": tc.role, "status": tc.status}).Error)
			if tc.deleted {
				require.NoError(t, DB.Delete(&agent).Error)
			}
			user := User{Username: "new-invitee", Quota: 999, QuotaPaid: 999, QuotaBonus: 999, QuotaReward: 999, InviterId: root.Id}
			require.NoError(t, user.Insert(agent.Id))
			stored := getUserBalances(t, user.Id)
			assert.Equal(t, tc.wantBonus, stored.QuotaBonus)
			assert.Zero(t, stored.QuotaPaid)
			assert.Zero(t, stored.QuotaReward)
			assertQuotaInvariant(t, stored)
			var invitations []AgentInvitation
			require.NoError(t, DB.Where("user_id = ?", user.Id).Find(&invitations).Error)
			if tc.wantInvitation {
				require.Len(t, invitations, 1)
				assert.Equal(t, agent.Id, stored.InviterId)
				assert.Equal(t, agent.Id, invitations[0].InviterId)
				assert.Equal(t, agent.Username, invitations[0].InviterUsername)
				assert.Equal(t, tc.role, invitations[0].InviterRole)
				assert.Equal(t, InvitationSourcePassword, invitations[0].Source)
				assert.Equal(t, tc.wantBonus, invitations[0].BonusQuota)
			} else {
				assert.Empty(t, invitations)
				assert.Zero(t, stored.InviterId)
			}
			var inviter User
			require.NoError(t, DB.Unscoped().First(&inviter, agent.Id).Error)
			wantCount := 0
			if tc.wantInvitation {
				wantCount = 1
			}
			assert.Equal(t, wantCount, inviter.AffCount)
			assert.Zero(t, inviter.Quota)
			assert.Zero(t, inviter.AffQuota)
			assert.Zero(t, inviter.AffHistoryQuota)
		})
	}
}

func TestAgentInvitationSnapshotAndFinalizerReplayDoNotRegrant(t *testing.T) {
	root, agent := setupAgentInvitationTest(t)
	_, err := UpdateAgentInvitationPolicy(root.Id, true, 500)
	require.NoError(t, err)
	user := &User{Username: "attributed-user"}
	require.NoError(t, user.Insert(agent.Id))
	user.FinishInsert(agent.Id)
	user.FinalizeOAuthUserCreation(agent.Id)
	user.InviterId = root.Id
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return user.UpdateWithTx(tx, false) }))
	assert.Equal(t, agent.Id, user.InviterId, "profile updates cannot rewrite invitation ownership")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.Id).Updates(map[string]any{"role": common.RoleCommonUser, "username": "renamed-inviter"}).Error)
	_, err = UpdateAgentInvitationPolicy(root.Id, true, 900)
	require.NoError(t, err)
	require.NoError(t, PopulateAgentInvitationSummaries([]*User{user}))
	require.NotNil(t, user.Invitation)
	assert.Equal(t, agent.Username, user.Invitation.InviterUsername)
	assert.Equal(t, common.RoleAgentUser, user.Invitation.InviterRole)
	assert.Equal(t, 500, user.Invitation.BonusQuota)
	var invitation AgentInvitation
	require.NoError(t, DB.Where("user_id = ?", user.Id).First(&invitation).Error)
	require.ErrorIs(t, DB.Model(&invitation).Update("inviter_id", root.Id).Error, ErrAgentInvitationImmutable)
	duplicate := invitation
	duplicate.Id = 0
	require.Error(t, DB.Create(&duplicate).Error, "one permanent attribution per new user")
	stored := getUserBalances(t, user.Id)
	assert.Equal(t, 500, stored.QuotaBonus)
	assert.EqualValues(t, 1, countLedgerRows(t, fmt.Sprintf("agent_invitation_bonus:%d", user.Id)))
	inviter := getUserBalances(t, agent.Id)
	assert.Equal(t, 1, inviter.AffCount)
}

func TestAgentInvitationRollbackAndRetryAreAtomic(t *testing.T) {
	for _, table := range []string{"agent_invitations", "balance_ledger"} {
		t.Run(table, func(t *testing.T) {
			root, agent := setupAgentInvitationTest(t)
			_, err := UpdateAgentInvitationPolicy(root.Id, true, 600)
			require.NoError(t, err)
			failure := errors.New("injected registration failure")
			callback := "agent-invitation-register-failure"
			require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(failure)
				}
			}))
			t.Cleanup(func() { _ = DB.Callback().Create().Remove(callback) })
			user := &User{Username: "retry-invitation"}
			require.ErrorIs(t, user.Insert(agent.Id), failure)
			assert.Zero(t, user.Id)
			for _, entity := range []any{&AgentInvitation{}, &BalanceLedger{}, &BalanceLedgerOperation{}} {
				var count int64
				require.NoError(t, DB.Model(entity).Count(&count).Error)
				assert.Zero(t, count)
			}
			var count int64
			require.NoError(t, DB.Model(&User{}).Where("username = ?", user.Username).Count(&count).Error)
			assert.Zero(t, count)
			inviter := getUserBalances(t, agent.Id)
			assert.Zero(t, inviter.AffCount)
			require.NoError(t, DB.Callback().Create().Remove(callback))
			require.NoError(t, user.Insert(agent.Id))
			assert.Equal(t, 600, getUserBalances(t, user.Id).QuotaBonus)
			assert.EqualValues(t, 1, countLedgerRows(t, fmt.Sprintf("agent_invitation_bonus:%d", user.Id)))
		})
	}
}

func TestAgentInvitationOAuthBindingRollbackAndLinuxDOStacking(t *testing.T) {
	root, agent := setupAgentInvitationTest(t)
	_, err := UpdateAgentInvitationPolicy(root.Id, true, 700)
	require.NoError(t, err)
	failure := errors.New("oauth binding failed")
	require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error {
		user := &User{Username: "oauth-rollback"}
		if err := user.InsertWithTx(tx, agent.Id); err != nil {
			return err
		}
		return failure
	}), failure)
	var count int64
	require.NoError(t, DB.Model(&AgentInvitation{}).Count(&count).Error)
	assert.Zero(t, count)
	assert.Zero(t, getUserBalances(t, agent.Id).AffCount)
	oauthUser := &User{Username: "oauth-invitee"}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return oauthUser.InsertWithTx(tx, agent.Id) }))
	var invitation AgentInvitation
	require.NoError(t, DB.Where("user_id = ?", oauthUser.Id).First(&invitation).Error)
	assert.Equal(t, InvitationSourceOAuth, invitation.Source)
	linuxUser := &User{Username: "linuxdo-invitee"}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return linuxUser.InsertLinuxDOWithTx(tx, agent.Id, "991001") }))
	linuxBonus := common.QuotaRound(common.LinuxDOSignupBonusUSD * common.QuotaPerUnit)
	stored := getUserBalances(t, linuxUser.Id)
	assert.Equal(t, linuxBonus+700, stored.QuotaBonus)
	assertQuotaInvariant(t, stored)
	var rows []BalanceLedger
	require.NoError(t, DB.Where("user_id = ?", linuxUser.Id).Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, LedgerReasonAgentInvitationBonus, rows[0].Reason)
	assert.Equal(t, LedgerReasonSignupBonus, rows[1].Reason)
	invitation = AgentInvitation{}
	require.NoError(t, DB.Where("user_id = ?", linuxUser.Id).First(&invitation).Error)
	assert.Equal(t, InvitationSourceLinuxDO, invitation.Source)
	assert.Equal(t, 700, invitation.BonusQuota)
	_, err = UpdateAgentInvitationPolicy(root.Id, true, common.MaxWalletQuota)
	require.NoError(t, err)
	err = DB.Transaction(func(tx *gorm.DB) error {
		return (&User{Username: "linuxdo-overflow"}).InsertLinuxDOWithTx(tx, agent.Id, "991002")
	})
	require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	require.NoError(t, DB.Model(&User{}).Where("username = ?", "linuxdo-overflow").Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&AgentInvitation{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	assert.Equal(t, 2, getUserBalances(t, agent.Id).AffCount)
}

func TestAgentInvitationConcurrentDuplicateRegistrationHasOneGrant(t *testing.T) {
	root, agent := setupAgentInvitationTest(t)
	_, err := UpdateAgentInvitationPolicy(root.Id, true, 250)
	require.NoError(t, err)
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			<-start
			errs[i] = (&User{Username: "same-new-user"}).Insert(agent.Id)
		})
	}
	close(start)
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	assert.Equal(t, 1, successes)
	var users []User
	require.NoError(t, DB.Where("username = ?", "same-new-user").Find(&users).Error)
	require.Len(t, users, 1)
	assert.Equal(t, 250, users[0].QuotaBonus)
	assertQuotaInvariant(t, users[0])
	var invitations []AgentInvitation
	require.NoError(t, DB.Find(&invitations).Error)
	require.Len(t, invitations, 1)
	assert.Equal(t, 250, invitations[0].BonusQuota)
	assert.EqualValues(t, 1, countLedgerRows(t, fmt.Sprintf("agent_invitation_bonus:%d", users[0].Id)))
	assert.Equal(t, 1, getUserBalances(t, agent.Id).AffCount)
}

func TestAgentInvitationListScopesAndLegacySummary(t *testing.T) {
	root, agent := setupAgentInvitationTest(t)
	other := User{Username: "other-agent", AffCode: "other-agent", Role: common.RoleAgentUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&other).Error)
	for i, inviter := range []User{agent, other} {
		require.NoError(t, (&User{Username: fmt.Sprintf("invited-%d", i)}).Insert(inviter.Id))
	}
	items, total, err := ListAgentInvitations(agent.Id, 1, 20, other.Id)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.EqualValues(t, 1, total)
	assert.Equal(t, agent.Id, items[0].InviterId)
	items, total, err = ListAgentInvitations(root.Id, 1, 1)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.EqualValues(t, 2, total)
	items, total, err = ListAgentInvitations(root.Id, 1, 20, other.Id)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.EqualValues(t, 1, total)
	assert.Equal(t, other.Id, items[0].InviterId)
	legacy := &User{Username: "legacy-attribution", AffCode: "legacy-attribution", InviterId: agent.Id}
	require.NoError(t, DB.Create(legacy).Error)
	require.NoError(t, PopulateAgentInvitationSummaries([]*User{legacy}))
	require.NotNil(t, legacy.Invitation)
	assert.True(t, legacy.Invitation.Legacy)
	assert.Equal(t, agent.Username, legacy.Invitation.InviterUsername)
	assert.Zero(t, legacy.Invitation.InviterRole, "historical role is unknown, not today's role")
	assert.Zero(t, legacy.Invitation.BonusQuota)
	for _, change := range []map[string]any{{"role": common.RoleCommonUser}, {"role": common.RoleAgentUser, "status": common.UserStatusDisabled}} {
		require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.Id).Updates(change).Error)
		_, _, err = ListAgentInvitations(agent.Id, 1, 20)
		require.ErrorIs(t, err, ErrAgentInvitationPermission)
	}
}

func TestAgentInvitationMigrationPreservesLegacyAndUniqueAttribution(t *testing.T) {
	root, agent := setupAgentInvitationTest(t)
	legacy := &User{Username: "before-invitation", AffCode: "before-invitation", InviterId: agent.Id, QuotaPaid: 51, Quota: 51}
	require.NoError(t, DB.Create(legacy).Error)
	// Existing user/referral/balance rows predate the three new tables. Upgrade
	// adds only empty policy/audit tables; it must never mint a backfill grant.
	require.NoError(t, DB.Migrator().DropTable(&AgentInvitation{}, &AgentInvitationPolicyAudit{}, &AgentInvitationPolicy{}))
	require.NoError(t, DB.AutoMigrate(&AgentInvitation{}, &AgentInvitationPolicy{}, &AgentInvitationPolicyAudit{}))
	policy, err := GetAgentInvitationPolicy(root.Id)
	require.NoError(t, err)
	assert.False(t, policy.Enabled)
	assert.Zero(t, policy.Quota)
	_, err = UpdateAgentInvitationPolicy(root.Id, true, 73)
	require.NoError(t, err)
	user := &User{Username: "after-invitation"}
	require.NoError(t, user.Insert(agent.Id))
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&User{}, &AgentInvitation{}, &AgentInvitationPolicy{}, &AgentInvitationPolicyAudit{}))
	}
	storedLegacy := getUserBalances(t, legacy.Id)
	assert.Equal(t, agent.Id, storedLegacy.InviterId)
	assert.Equal(t, 51, storedLegacy.QuotaPaid)
	assert.Zero(t, storedLegacy.QuotaBonus)
	policy, err = GetAgentInvitationPolicy(root.Id)
	require.NoError(t, err)
	assert.True(t, policy.Enabled)
	assert.Equal(t, 73, policy.Quota)
	var invitation AgentInvitation
	require.NoError(t, DB.Where("user_id = ?", user.Id).First(&invitation).Error)
	invitation.Id = 0
	require.Error(t, DB.Create(&invitation).Error, "migration preserves unique new-user attribution")
	var count int64
	require.NoError(t, DB.Model(&AgentInvitation{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	assert.Equal(t, 73, getUserBalances(t, user.Id).QuotaBonus)
}
