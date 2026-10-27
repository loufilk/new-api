package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// This suite runs in its own process, as the production extension registry is
// intentionally write-once. It tests real schema, ownership and persistence.
func TestScopedAccessTokenDatabaseCompatibilityReal(t *testing.T) {
	if os.Getenv("TEST_DATABASE_COMPAT") != "1" {
		t.Skip("set TEST_DATABASE_COMPAT=1 to run real scoped-token database contracts")
	}
	installDatabaseCompatRegistry(t)
	oldMaster, oldSQLite := common.IsMasterNode, common.SQLitePath
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode, common.SQLitePath = oldMaster, oldSQLite })
	dialect := strings.ToLower(strings.TrimSpace(os.Getenv("TEST_DATABASE_DIALECT")))
	switch dialect {
	case "sqlite":
		common.SQLitePath = filepath.Join(t.TempDir(), "access-token.db") + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate"
		t.Setenv("SQL_DSN", "local")
	case "mysql", "postgres":
		dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
		require.NotEmpty(t, dsn)
		t.Setenv("SQL_DSN", dsn)
	default:
		t.Fatalf("unsupported database dialect %q", dialect)
	}
	require.NoError(t, InitDB())
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.True(t, DB.Migrator().HasTable(&UserAccessToken{}))

	unique := fmt.Sprintf("pat%x", time.Now().UnixNano())
	user := User{Username: unique, Password: "test-only-hash", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
		Quota: 3_000_000_000, QuotaPaid: 3_000_000_000, AffCode: unique}
	require.NoError(t, DB.Create(&user).Error)
	raw := AccessTokenPrefix + "test-only-" + unique
	token := &UserAccessToken{Name: "database-contract", TokenHash: AccessTokenFingerprint(raw),
		TokenHint: AccessTokenHint(raw), ExpiresAt: 3_000_000_000}
	require.NoError(t, token.SetScopes([]string{"wallet:read"}))
	require.NoError(t, CreateUserAccessToken(user.Id, token, 1))
	require.NotZero(t, token.Id)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("user_id = ?", user.Id).Delete(&UserAccessToken{}).Error)
		require.NoError(t, DB.Unscoped().Delete(&User{}, user.Id).Error)
	})
	found, err := FindUserAccessTokenByHash(AccessTokenFingerprint(raw))
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, token.Id, found.Id)
	require.Equal(t, int64(3_000_000_000), found.ExpiresAt)
	require.Equal(t, []string{"wallet:read"}, found.GetScopes())
	require.NotEqual(t, raw, found.TokenHash)

	second := &UserAccessToken{Name: "over-limit", TokenHash: AccessTokenFingerprint(raw + "2")}
	require.ErrorIs(t, CreateUserAccessToken(user.Id, second, 1), ErrAccessTokenLimit)
	_, err = GetUserAccessToken(user.Id+1, token.Id)
	require.ErrorIs(t, err, ErrAccessTokenNotFound)
	_, err = UpdateUserAccessToken(user.Id+1, token.Id, "stolen", []string{"wallet:write"})
	require.ErrorIs(t, err, ErrAccessTokenNotFound)
	_, err = DeleteUserAccessToken(user.Id+1, token.Id)
	require.ErrorIs(t, err, ErrAccessTokenNotFound)
	found, err = GetUserAccessToken(user.Id, token.Id)
	require.NoError(t, err)
	require.Equal(t, "database-contract", found.Name)
	require.Equal(t, []string{"wallet:read"}, found.GetScopes())

	found, err = UpdateUserAccessToken(user.Id, token.Id, "renamed", []string{"wallet:read", "wallet:write"})
	require.NoError(t, err)
	require.Equal(t, "renamed", found.Name)
	// In-place production migrations must preserve populated grants, BIGINTs and
	// the one-time legacy retirement deadline, not restart the grace period.
	deadline := LegacyAccessTokenRetireAt()
	require.Positive(t, deadline)
	for range 2 {
		require.NoError(t, migrateDB())
	}
	require.NoError(t, EnsureLegacyAccessTokenRetireAt(common.GetTimestamp()+7*24*60*60))
	require.Equal(t, deadline, LegacyAccessTokenRetireAt())
	found, err = GetUserAccessToken(user.Id, token.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"wallet:read", "wallet:write"}, found.GetScopes())
	require.Equal(t, int64(3_000_000_000), found.ExpiresAt)
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.Equal(t, 3_000_000_000, stored.Quota)
	require.Equal(t, stored.Quota, stored.QuotaPaid+stored.QuotaBonus+stored.QuotaReward)
	duplicate := *found
	duplicate.Id = 0
	require.Error(t, DB.Create(&duplicate).Error, "migration must preserve unique credential fingerprints")
	deleted, err := DeleteUserAccessToken(user.Id, token.Id)
	require.NoError(t, err)
	require.Equal(t, token.Id, deleted.Id)
	found, err = FindUserAccessTokenByHash(AccessTokenFingerprint(raw))
	require.NoError(t, err)
	require.Nil(t, found, "revoked credential must no longer resolve")
	t.Logf("scoped-token startup, repeated migrations, ownership, BIGINT and revocation validated on %s", dialect)
}
