package model

import (
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentCredentialsRemainPersonalAcrossBusinessLifecyclePostgres(t *testing.T) {
	root, user, rootIdentity, _ := setupAgentProfileSecurityTest(t)
	token := Token{UserId: user.Id, Key: "synthetic-personal-agent-key", Status: common.TokenStatusEnabled}
	disabled := Token{UserId: user.Id, Key: "synthetic-disabled-agent-key", Status: common.TokenStatusDisabled}
	require.NoError(t, token.Insert(user.AuthVersion))
	require.NoError(t, disabled.Insert(user.AuthVersion))
	for index, action := range []string{"set_agent", "unset_agent", "set_agent"} {
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
			_, err := ApplyUserManagementActionTx(tx, root.Id, user, action, rootIdentity)
			return err
		}))
		fresh := Token{UserId: user.Id, Key: fmt.Sprintf("synthetic-new-agent-key-%d", index), Status: common.TokenStatusEnabled}
		require.NoError(t, fresh.Insert(user.AuthVersion))
		token.Name = fmt.Sprintf("personal key %d", index)
		require.NoError(t, token.Update(user.AuthVersion))
		pat := UserAccessToken{Name: fmt.Sprintf("personal pat %d", index), TokenHash: AccessTokenFingerprint(fmt.Sprintf("synthetic-personal-pat-%d", index))}
		require.NoError(t, CreateUserAccessToken(user.Id, &pat, 20, user.AuthVersion))
	}
	require.NoError(t, DB.First(&disabled, disabled.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, disabled.Status)
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, user.AuthVersion, stored.AuthVersion)
	require.ErrorIs(t, token.Update(0), ErrCredentialWriteUnauthorized)
	require.ErrorIs(t, token.Update(user.AuthVersion+1), ErrCredentialWriteUnauthorized)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := ApplyUserManagementActionTx(tx, root.Id, stored, "disable", rootIdentity)
		return err
	}))
	require.ErrorIs(t, token.Update(user.AuthVersion), ErrCredentialWriteUnauthorized)
}

func TestAgentQualificationAndCredentialCreationCanCommitConcurrentlyPostgres(t *testing.T) {
	root, user, identity, _ := setupAgentProfileSecurityTest(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		results <- DB.Transaction(func(tx *gorm.DB) error {
			_, err := ApplyUserManagementActionTx(tx, root.Id, user, "set_agent", identity)
			return err
		})
	})
	wg.Go(func() {
		<-start
		token := Token{UserId: user.Id, Key: "synthetic-concurrent-personal-key", Status: common.TokenStatusEnabled}
		results <- token.Insert(user.AuthVersion)
	})
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, DB.Model(&Token{}).Where("user_id = ? AND status = ?", user.Id, common.TokenStatusEnabled).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	profile, err := GetAgentProfile(user.Id)
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.True(t, profile.Allows(AgentCapabilitySales))
}

func TestAgentProfileDoesNotResurrectExplicitlyDisabledCredentialPostgres(t *testing.T) {
	root, user, identity, _ := setupAgentProfileSecurityTest(t)
	token := Token{UserId: user.Id, Key: "synthetic-explicitly-disabled-key", Status: common.TokenStatusEnabled}
	require.NoError(t, token.Insert(user.AuthVersion))
	stale := token
	token.Status = common.TokenStatusDisabled
	require.NoError(t, token.Update(user.AuthVersion))
	for _, action := range []string{"set_agent", "unset_agent"} {
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
			_, err := ApplyUserManagementActionTx(tx, root.Id, user, action, identity)
			return err
		}))
	}
	stale.Status = common.TokenStatusEnabled
	require.ErrorIs(t, stale.SelectUpdate(), ErrPermissionCacheStale)
	require.NoError(t, DB.First(&token, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
}
