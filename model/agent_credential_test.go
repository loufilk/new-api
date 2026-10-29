package model

import (
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgentCredentialTest(t *testing.T) (User, User, Token) {
	t.Helper()
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6")
	}
	setupLedgerTestState(t)
	admin := User{Username: "credential-admin", AffCode: "credential-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	owner := User{Username: "credential-owner", AffCode: "credential-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&admin).Error)
	require.NoError(t, DB.Create(&owner).Error)
	token := Token{UserId: owner.Id, Key: "synthetic-credential-existing", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, Group: "default"}
	require.NoError(t, token.Insert(owner.AuthVersion))
	return admin, owner, token
}

func agentCredentialRoleChange(t *testing.T, admin, owner User, action string) User {
	t.Helper()
	var changed *User
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		changed, err = ApplyUserManagementActionTx(tx, admin.Id, owner, action)
		return err
	}))
	return *changed
}

func agentCredentialWrite(kind string, owner User, existing Token, version int64) error {
	switch kind {
	case "relay_create":
		token := Token{UserId: owner.Id, Key: "synthetic-credential-new", Status: common.TokenStatusEnabled, Group: "default"}
		return token.Insert(version)
	case "relay_update":
		existing.Status = common.TokenStatusEnabled
		existing.Name = "inflight credential update"
		return existing.Update(version)
	case "pat_create":
		token := UserAccessToken{Name: "synthetic pat", TokenHash: AccessTokenFingerprint("synthetic-credential-pat")}
		return CreateUserAccessToken(owner.Id, &token, 20, version)
	default:
		panic("unknown credential test case")
	}
}

func assertAgentCredentialsRevoked(t *testing.T, owner User) {
	t.Helper()
	var enabled, pats int64
	require.NoError(t, DB.Model(&Token{}).Where("user_id = ? AND status != ?", owner.Id, common.TokenStatusDisabled).Count(&enabled).Error)
	require.NoError(t, DB.Model(&UserAccessToken{}).Where("user_id = ?", owner.Id).Count(&pats).Error)
	assert.Zero(t, enabled, "entering agent must leave no usable relay credential")
	assert.Zero(t, pats, "entering agent must remove every management credential")
}

func TestAgentCredentialOwnerAndVersionPostgres(t *testing.T) {
	for _, kind := range []string{"relay_create", "relay_update", "pat_create"} {
		t.Run(kind, func(t *testing.T) {
			admin, owner, token := setupAgentCredentialTest(t)
			require.ErrorIs(t, agentCredentialWrite(kind, owner, token, 0), ErrCredentialWriteUnauthorized)
			require.ErrorIs(t, agentCredentialWrite(kind, owner, token, owner.AuthVersion+1), ErrCredentialWriteUnauthorized)
			agent := agentCredentialRoleChange(t, admin, owner, "set_agent")
			require.ErrorIs(t, agentCredentialWrite(kind, owner, token, agent.AuthVersion), ErrCredentialWriteUnauthorized)
			commonUser := agentCredentialRoleChange(t, admin, agent, "unset_agent")
			// An old request cannot resume after a complete agent -> common cycle.
			require.ErrorIs(t, agentCredentialWrite(kind, owner, token, owner.AuthVersion), ErrCredentialWriteUnauthorized)
			assertAgentCredentialsRevoked(t, owner)
			require.NoError(t, agentCredentialWrite(kind, commonUser, token, commonUser.AuthVersion))
			disabled := agentCredentialRoleChange(t, admin, commonUser, "disable")
			require.ErrorIs(t, agentCredentialWrite(kind, owner, token, disabled.AuthVersion), ErrCredentialWriteUnauthorized)
		})
	}
	t.Run("internal callers still require live owner", func(t *testing.T) {
		admin, owner, existing := setupAgentCredentialTest(t)
		agentCredentialRoleChange(t, admin, owner, "set_agent")
		newToken := Token{UserId: owner.Id, Key: "synthetic-internal-agent-key"}
		require.ErrorIs(t, newToken.Insert(), ErrCredentialWriteUnauthorized)
		require.ErrorIs(t, existing.Update(), ErrCredentialWriteUnauthorized)
		pat := UserAccessToken{TokenHash: AccessTokenFingerprint("synthetic-internal-agent-pat")}
		require.ErrorIs(t, CreateUserAccessToken(owner.Id, &pat, 20), ErrCredentialWriteUnauthorized)
		newToken.UserId = owner.Id + 1000000
		require.ErrorIs(t, newToken.Insert(), ErrCredentialWriteUnauthorized)
	})
	t.Run("token owner cannot be substituted", func(t *testing.T) {
		admin, owner, token := setupAgentCredentialTest(t)
		token.UserId = admin.Id
		token.Status = common.TokenStatusDisabled
		require.Error(t, token.Update(admin.AuthVersion))
		var stored Token
		require.NoError(t, DB.First(&stored, token.Id).Error)
		assert.Equal(t, owner.Id, stored.UserId)
		assert.Equal(t, common.TokenStatusEnabled, stored.Status)
	})
}

func awaitAgentCredentialBarrier(t *testing.T, barrier <-chan struct{}) {
	t.Helper()
	select {
	case <-barrier:
	case <-time.After(10 * time.Second):
		t.Fatal("credential transaction did not reach deterministic barrier")
	}
}

func TestAgentCredentialRoleChangeWinsInFlightPostgres(t *testing.T) {
	for _, kind := range []string{"relay_create", "relay_update", "pat_create"} {
		t.Run(kind, func(t *testing.T) {
			admin, owner, token := setupAgentCredentialTest(t)
			roleTx := DB.Begin()
			require.NoError(t, roleTx.Error)
			t.Cleanup(func() { _ = roleTx.Rollback().Error })
			_, err := ApplyUserManagementActionTx(roleTx, admin.Id, owner, "set_agent")
			require.NoError(t, err)
			attempted := make(chan struct{})
			var once sync.Once
			callback := "agent:credential_waits_for_role_" + kind
			require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "users" && slices.Equal(tx.Statement.Selects, []string{"id", "role", "status", "auth_version"}) {
					once.Do(func() { close(attempted) })
				}
			}))
			t.Cleanup(func() { _ = DB.Callback().Query().Remove(callback) })
			done := make(chan error, 1)
			go func() { done <- agentCredentialWrite(kind, owner, token, owner.AuthVersion) }()
			awaitAgentCredentialBarrier(t, attempted)
			require.NoError(t, roleTx.Commit().Error)
			require.ErrorIs(t, <-done, ErrCredentialWriteUnauthorized)
			assertAgentCredentialsRevoked(t, owner)
		})
	}
}

func TestAgentCredentialWriteWinsBeforeRoleChangePostgres(t *testing.T) {
	for _, kind := range []string{"relay_create", "relay_update", "pat_create"} {
		t.Run(kind, func(t *testing.T) {
			admin, owner, token := setupAgentCredentialTest(t)
			ownerLocked, releaseOwner, roleAttempted := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var ownerOnce, roleOnce, releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseOwner) }) }
			t.Cleanup(release)
			lockCallback, roleCallback := "agent:credential_holds_owner_"+kind, "agent:role_waits_for_credential_"+kind
			require.NoError(t, DB.Callback().Query().After("gorm:query").Register(lockCallback, func(tx *gorm.DB) {
				if tx.Statement.Table == "users" && slices.Equal(tx.Statement.Selects, []string{"id", "role", "status", "auth_version"}) {
					ownerOnce.Do(func() { close(ownerLocked); <-releaseOwner })
				}
			}))
			require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(roleCallback, func(tx *gorm.DB) {
				if tx.Statement.Table == "users" && len(tx.Statement.Selects) == 0 {
					roleOnce.Do(func() { close(roleAttempted) })
				}
			}))
			t.Cleanup(func() {
				_ = DB.Callback().Query().Remove(lockCallback)
				_ = DB.Callback().Query().Remove(roleCallback)
			})
			credentialDone, roleDone := make(chan error, 1), make(chan error, 1)
			go func() { credentialDone <- agentCredentialWrite(kind, owner, token, owner.AuthVersion) }()
			awaitAgentCredentialBarrier(t, ownerLocked)
			go func() {
				roleDone <- DB.Transaction(func(tx *gorm.DB) error {
					_, err := ApplyUserManagementActionTx(tx, admin.Id, owner, "set_agent")
					return err
				})
			}()
			awaitAgentCredentialBarrier(t, roleAttempted)
			release()
			require.NoError(t, <-credentialDone)
			require.NoError(t, <-roleDone)
			assertAgentCredentialsRevoked(t, owner)
		})
	}
}

func TestAgentCredentialDelayedAutomaticStatusCannotRevivePostgres(t *testing.T) {
	for _, status := range []int{common.TokenStatusExpired, common.TokenStatusExhausted, common.TokenStatusEnabled} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			admin, owner, stale := setupAgentCredentialTest(t)
			agent := agentCredentialRoleChange(t, admin, owner, "set_agent")
			agentCredentialRoleChange(t, admin, agent, "unset_agent")
			stale.Status = status
			require.ErrorIs(t, stale.SelectUpdate(), ErrPermissionCacheStale)
			assertAgentCredentialsRevoked(t, owner)
		})
	}
}
