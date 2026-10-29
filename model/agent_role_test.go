package model

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentRoleLifecyclePostgres(t *testing.T) {
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6")
	}
	setupLedgerTestState(t)
	admin := User{Username: "agent-role-admin", AffCode: "agent-role-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	user := User{Username: "agent-role-user", AffCode: "agent-role-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 123, QuotaPaid: 100, QuotaBonus: 23}
	root := User{Username: "agent-role-root", AffCode: "agent-role-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&admin).Error)
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&root).Error)
	token := Token{UserId: user.Id, Key: "synthetic-agent-role-token", Status: common.TokenStatusEnabled, RemainQuota: 100}
	require.NoError(t, token.Insert())
	oldTokenRevision := token.CacheRevision
	oldRetireAt := legacyAccessTokenRetireAt.Load()
	legacyAccessTokenRetireAt.Store(time.Now().Add(time.Hour).Unix())
	t.Cleanup(func() { legacyAccessTokenRetireAt.Store(oldRetireAt) })
	legacyToken := "synthetic-agent-role-legacy-pat"
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("access_token", legacyToken).Error)
	validated, err := ValidateAccessToken(legacyToken)
	require.NoError(t, err)
	require.NotNil(t, validated)
	initial := user
	var changed *User
	err = DB.Transaction(func(tx *gorm.DB) error {
		var err error
		changed, err = ApplyUserManagementActionTx(tx, admin.Id, user, "set_agent")
		return err
	})
	require.NoError(t, err)
	require.Equal(t, common.RoleAgentUser, changed.Role)
	assert.Greater(t, changed.AuthVersion, initial.AuthVersion)
	require.NoError(t, DB.First(&token, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
	assert.Greater(t, token.CacheRevision, oldTokenRevision, "old enabled cache snapshots cannot republish")
	assert.Equal(t, 123, changed.Quota)
	assert.Equal(t, 100, changed.QuotaPaid)
	assert.Equal(t, 23, changed.QuotaBonus)
	// A stale admin form cannot overwrite a newer role or status.
	err = DB.Transaction(func(tx *gorm.DB) error {
		_, err := ApplyUserManagementActionTx(tx, admin.Id, initial, "disable")
		return err
	})
	require.Error(t, err)
	user = *changed
	err = DB.Transaction(func(tx *gorm.DB) error {
		var err error
		changed, err = ApplyUserManagementActionTx(tx, admin.Id, user, "unset_agent")
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, common.RoleCommonUser, changed.Role)
	require.NoError(t, DB.First(&token, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, token.Status, "downgrade must not reactivate old keys")
	validated, err = ValidateAccessToken(legacyToken)
	require.NoError(t, err)
	assert.Nil(t, validated, "downgrade must not revive legacy management credentials")
	var persisted User
	require.NoError(t, DB.First(&persisted, user.Id).Error)
	assert.Nil(t, persisted.AccessToken)
	assert.Nil(t, persisted.AccessTokenCreatedAt)
	for _, input := range []struct {
		actor  int
		target User
		action string
	}{
		{changed.Id, admin, "set_agent"},
		{admin.Id, root, "unset_agent"},
		{admin.Id, *changed, "promote"},
		{admin.Id, admin, "disable"},
	} {
		err := DB.Transaction(func(tx *gorm.DB) error {
			_, err := ApplyUserManagementActionTx(tx, input.actor, input.target, input.action)
			return err
		})
		require.Error(t, err)
	}
	// A revoked operator is rejected from DB authority, not accepted from an
	// earlier middleware role snapshot.
	require.NoError(t, DB.Model(&admin).Update("status", common.UserStatusDisabled).Error)
	err = DB.Transaction(func(tx *gorm.DB) error {
		_, err := ApplyUserManagementActionTx(tx, admin.Id, *changed, "set_agent")
		return err
	})
	require.Error(t, err)
}

// A valid middleware snapshot or consumed proof is not sufficient after logout
// or a role cycle. The mutation must recheck the receipt under the user lock.
func TestAgentRoleAdministrativeReceiptsPostgres(t *testing.T) {
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6")
	}
	setupLedgerTestState(t)
	admin := User{Username: "receipt-admin", AffCode: "receipt-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&admin).Error)
	session := UserSession{SID: "synthetic-agent-role-session", UserID: admin.Id, Version: 1, UserAuthVersion: admin.AuthVersion, Status: UserSessionStatusActive, ExpiresAt: time.Now().Unix() + 3600}
	require.NoError(t, DB.Create(&session).Error)
	identity := AuthSessionIdentity{UserID: admin.Id, SessionID: session.SID, UserAuthVersion: admin.AuthVersion, SessionVersion: 1}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAdminUserCreationTx(tx, identity, common.RoleAgentUser) }))
	for _, mode := range []string{"logout", "role-cycle"} {
		t.Run(mode, func(t *testing.T) {
			require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", session.SID).Update("status", UserSessionStatusActive).Error)
			require.NoError(t, DB.Model(&User{}).Where("id = ?", admin.Id).Update("auth_version", admin.AuthVersion).Error)
			if mode == "logout" {
				require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", session.SID).Update("status", UserSessionStatusRevoked).Error)
			} else {
				require.NoError(t, DB.Model(&User{}).Where("id = ?", admin.Id).Updates(map[string]any{"role": common.RoleAdminUser, "auth_version": admin.AuthVersion + 2}).Error)
			}
			target := User{Username: "receipt-target-" + mode, AffCode: "receipt-target-" + mode, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
			require.NoError(t, DB.Create(&target).Error)
			err := DB.Transaction(func(tx *gorm.DB) error {
				if err := ValidateAdminUserCreationTx(tx, identity, common.RoleAgentUser); err != nil {
					return err
				}
				return tx.Create(&User{Username: "must-not-create-" + mode, AffCode: "must-not-create-" + mode, Role: common.RoleAgentUser}).Error
			})
			require.ErrorIs(t, err, ErrUserSessionInactive)
			err = DB.Transaction(func(tx *gorm.DB) error {
				_, err := ApplyUserManagementActionTx(tx, admin.Id, target, "set_agent", identity)
				return err
			})
			require.ErrorIs(t, err, ErrUserSessionInactive)
			for _, hard := range []bool{false, true} {
				_, err = DeleteUserByAdministrator(identity, target, hard)
				require.ErrorIs(t, err, ErrUserSessionInactive, fmt.Sprint(hard))
			}
			var unchanged User
			require.NoError(t, DB.First(&unchanged, target.Id).Error)
			require.Equal(t, common.RoleCommonUser, unchanged.Role)
			var count int64
			require.NoError(t, DB.Model(&User{}).Where("username = ?", "must-not-create-"+mode).Count(&count).Error)
			require.Zero(t, count)
		})
	}
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", session.SID).Update("status", UserSessionStatusActive).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", admin.Id).Update("auth_version", admin.AuthVersion).Error)
	for _, hard := range []bool{false, true} {
		target := User{Username: fmt.Sprintf("receipt-delete-%v", hard), AffCode: fmt.Sprintf("receipt-delete-%v", hard), Role: common.RoleAgentUser, Status: common.UserStatusEnabled}
		require.NoError(t, DB.Create(&target).Error)
		_, err := DeleteUserByAdministrator(identity, target, hard)
		require.NoError(t, err)
		var count int64
		require.NoError(t, DB.Model(&User{}).Where("id = ?", target.Id).Count(&count).Error)
		require.Zero(t, count)
	}
}
