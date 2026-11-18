package authz

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func transactionAuthzPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	db := openAuthzPostgresSchema(t)
	var version string
	require.NoError(t, db.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, "180006", version, "transaction authorization tests require PostgreSQL 18.6")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestCanInTxPostgresSeesRevocationWithoutEnforcerReload(t *testing.T) {
	db := transactionAuthzPostgres(t)
	wasMaster := common.IsMasterNode
	enforcerMu.Lock()
	previous := enforcer
	enforcerMu.Unlock()
	common.IsMasterNode = false
	t.Cleanup(func() {
		common.IsMasterNode = wasMaster
		enforcerMu.Lock()
		enforcer = previous
		enforcerMu.Unlock()
	})
	require.NoError(t, db.Create(&model.CasbinRule{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: ReferralWithdrawalPay.Resource, V2: ReferralWithdrawalPay.Action, V3: EffectAllow}).Error)
	require.NoError(t, Init(db))
	require.True(t, Can(72, common.RoleAdminUser, ReferralWithdrawalPay))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		// The real permissions update writes in a transaction, then reloads the
		// local cache after commit. Financial guards must see these policy rows.
		if err := SetUserPermissionsInTx(tx, 72, PermissionsMap{ReferralWithdrawalPay.Resource: {ReferralWithdrawalPay.Action: false}}); err != nil {
			return err
		}
		allowed, err := CanInTx(tx, 72, common.RoleAdminUser, ReferralWithdrawalPay)
		require.NoError(t, err)
		assert.False(t, allowed)
		assert.True(t, Can(72, common.RoleAdminUser, ReferralWithdrawalPay), "cached enforcer intentionally remains stale")
		return nil
	}))
	assert.True(t, Can(72, common.RoleAdminUser, ReferralWithdrawalPay))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		allowed, err := CanInTx(tx, 72, common.RoleAdminUser, ReferralWithdrawalPay)
		require.NoError(t, err)
		assert.False(t, allowed, "a later transaction sees the committed revocation")
		return nil
	}))
}

func TestCanInTxPostgresPreservesOverrideAndLegacyScopeSemantics(t *testing.T) {
	db := transactionAuthzPostgres(t)
	rules := []model.CasbinRule{
		{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "channel", V2: "read", V3: EffectAllow},
		{Ptype: "p", V0: UserSubject(2), V1: "channel", V2: "read", V3: EffectDeny},
		{Ptype: "p", V0: UserSubject(3), V1: "channel", V2: "read", V3: EffectAllow},
		{Ptype: "p", V0: UserSubject(3), V1: "channel", V2: "read", V3: EffectDeny},
		{Ptype: "p", V0: UserSubject(4), V1: "channel", V2: "read", V4: "own"},
		{Ptype: "p", V0: UserSubject(4), V1: "channel", V2: "read", V3: EffectAllow, V4: "all"},
		{Ptype: "p", V0: UserSubject(5), V1: "channel", V2: "read", V5: "own"},
		{Ptype: "p", V0: UserSubject(6), V1: "channel", V2: "read", V4: "all"},
		{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "channel", V2: "operate", V3: EffectAllow, V4: "own"},
		{Ptype: "p", V0: UserSubject(8), V1: "channel", V2: "operate", V3: EffectAllow},
		{Ptype: "g", V0: UserSubject(9), V1: RoleSubject(BuiltInRoleAdmin)},
	}
	require.NoError(t, db.Create(&rules).Error)
	for _, test := range []struct {
		name       string
		userID     int
		role       int
		permission Permission
		allowed    bool
	}{
		{"role baseline", 1, common.RoleAdminUser, ChannelRead, true},
		{"user deny overrides baseline", 2, common.RoleAdminUser, ChannelRead, false},
		{"deny wins over same-subject allow", 3, common.RoleAdminUser, ChannelRead, false},
		{"unsupported own scope defeats global allow", 4, common.RoleAdminUser, ChannelRead, false},
		{"unknown v5 scope fails closed", 5, common.RoleAdminUser, ChannelRead, false},
		{"empty legacy effect means allow", 6, common.RoleAdminUser, ChannelRead, true},
		{"scoped role baseline fails closed", 7, common.RoleAdminUser, ChannelOperate, false},
		{"explicit user allow wins over baseline", 8, common.RoleAdminUser, ChannelOperate, true},
		{"legacy membership cannot promote ordinary user", 9, common.RoleCommonUser, ChannelRead, false},
		{"missing policy denies", 1, common.RoleAdminUser, ChannelSecretView, false},
		{"root known permission", 1, common.RoleRootUser, ChannelSecretView, true},
		{"unknown permission denies", 1, common.RoleRootUser, Permission{Resource: "unknown", Action: "pay"}, false},
		{"invalid role cannot impersonate root", 1, common.RoleRootUser + 1, ChannelSecretView, false},
		{"invalid user cannot impersonate root", 0, common.RoleRootUser, ChannelSecretView, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				allowed, err := CanInTx(tx, test.userID, test.role, test.permission)
				require.NoError(t, err)
				assert.Equal(t, test.allowed, allowed)
				return nil
			}))
		})
	}
	var stored []model.CasbinRule
	require.NoError(t, db.Order("id").Find(&stored).Error)
	assert.Equal(t, rules, stored, "authorization must preserve legacy evidence")
}

func TestCanInTxPostgresInvalidPolicyAndUnavailableStoreFailClosed(t *testing.T) {
	db := transactionAuthzPostgres(t)
	require.NoError(t, db.Create(&model.CasbinRule{Ptype: "p", V0: UserSubject(72), V1: "channel", V2: "read", V3: "unsupported-effect"}).Error)
	allowed, err := CanInTx(db, 72, common.RoleAdminUser, ChannelRead)
	require.Error(t, err)
	assert.False(t, allowed)
	allowed, err = CanInTx(nil, 72, common.RoleAdminUser, ChannelRead)
	require.Error(t, err)
	assert.False(t, allowed)
	require.NoError(t, db.Migrator().DropTable(&model.CasbinRule{}))
	allowed, err = CanInTx(db, 72, common.RoleAdminUser, ChannelRead)
	require.Error(t, err)
	assert.False(t, allowed)
}
