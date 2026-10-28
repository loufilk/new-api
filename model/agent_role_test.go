package model

import (
	"os"
	"testing"

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
	initial := user
	var changed *User
	err := DB.Transaction(func(tx *gorm.DB) error {
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
