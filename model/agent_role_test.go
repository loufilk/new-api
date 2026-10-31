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
	root, user, identity, _ := setupAgentProfileSecurityTest(t)
	enabled := Token{UserId: user.Id, Key: "synthetic-legacy-profile-enabled", Status: common.TokenStatusEnabled}
	disabled := Token{UserId: user.Id, Key: "synthetic-legacy-profile-disabled", Status: common.TokenStatusDisabled}
	require.NoError(t, enabled.Insert(user.AuthVersion))
	require.NoError(t, disabled.Insert(user.AuthVersion))
	for _, action := range []string{"set_agent", "unset_agent"} {
		var changed *User
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
			var err error
			changed, err = ApplyUserManagementActionTx(tx, root.Id, user, action, identity)
			return err
		}))
		require.Equal(t, common.RoleCommonUser, changed.Role)
		assert.Equal(t, user.AuthVersion, changed.AuthVersion)
		assert.Equal(t, user.Quota, changed.Quota)
		profile, err := GetAgentProfile(user.Id)
		require.NoError(t, err)
		require.NotNil(t, profile)
		if action == "set_agent" {
			assert.Equal(t, AgentProfileActive, profile.Status)
		} else {
			assert.Equal(t, AgentProfileExited, profile.Status)
		}
	}
	require.NoError(t, DB.First(&enabled, enabled.Id).Error)
	require.NoError(t, DB.First(&disabled, disabled.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, enabled.Status)
	assert.Equal(t, common.TokenStatusDisabled, disabled.Status, "historical disabled keys never reactivate")
	require.Error(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := ApplyUserManagementActionTx(tx, user.Id, root, "set_agent")
		return err
	}))
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
