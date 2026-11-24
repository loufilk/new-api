package model

import (
	"net"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

func TestIM21ManagedModeIsExplicit(t *testing.T) {
	for _, name := range []string{"CORE_IM21_MIGRATION_MODE", common.IM21RolloutModeEnv, "CORE_IM21_RELEASE_ID"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
	mode, err := IM21MigrationMode()
	require.NoError(t, err)
	require.Empty(t, mode)
	t.Setenv(common.IM21RolloutModeEnv, "active")
	_, err = IM21MigrationMode()
	require.Error(t, err)
	for _, value := range []string{"", " migrate", "MIGRATE", "migrator", "off"} {
		t.Setenv("CORE_IM21_MIGRATION_MODE", value)
		_, err := IM21MigrationMode()
		require.Error(t, err)
	}
	for _, value := range []string{IM21Migrate, IM21Serve} {
		t.Setenv("CORE_IM21_MIGRATION_MODE", value)
		mode, err := IM21MigrationMode()
		require.NoError(t, err)
		require.Equal(t, value, mode)
	}
	for _, value := range []string{"", strings.Repeat("A", 32), strings.Repeat("z", 32), "012"} {
		t.Setenv("CORE_IM21_RELEASE_ID", value)
		require.ErrorContains(t, InitIM21Databases(IM21Serve), "CORE_IM21_RELEASE_ID")
	}
	t.Setenv("CORE_IM21_RELEASE_ID", strings.Repeat("a", 32))
	for _, value := range []string{"", "local", "mysql://example.invalid", "clickhouse://example.invalid"} {
		t.Setenv("SQL_DSN", value)
		require.ErrorContains(t, InitIM21Databases(IM21Serve), "PostgreSQL URL")
	}
}

// The caller supplies a disposable loopback PG 18.6/18.7 instance. Every
// fixture lives in a private schema and a transaction that is rolled back.
func TestIM21ReceiptAndLockedStatePostgres(t *testing.T) {
	dsn := os.Getenv("TEST_IM21_GATE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable TEST_IM21_GATE_POSTGRES_DSN")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(u.Hostname()).IsLoopback())
	t.Setenv("SQL_DSN", dsn)
	db, err := openIM21Postgres("SQL_DSN", IM21Serve)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, closeDB(db)) })
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	var version string
	require.NoError(t, tx.Raw("SHOW server_version_num").Row().Scan(&version))
	require.Contains(t, []string{"180006", "180007"}, version)
	schema := "im21_gate_" + strings.ToLower(common.GetRandomString(16))
	require.NoError(t, tx.Exec("CREATE SCHEMA ?", clause.Table{Name: schema}).Error)
	require.NoError(t, tx.Exec("SET LOCAL search_path TO ?", clause.Table{Name: schema}).Error)
	digest := strings.Repeat("0", 64)
	window := strings.Repeat("a", 32)
	id := window + ":abc"
	require.NoError(t, writeIM21Receipt(tx, "primary", digest, id))
	got, err := readIM21Receipt(tx, "primary", digest, window)
	require.NoError(t, err)
	require.Equal(t, id, got)
	for _, input := range [][3]string{{"log", digest, window}, {"primary", "other", window}, {"primary", digest, strings.Repeat("b", 32)}} {
		_, err = readIM21Receipt(tx, input[0], input[1], input[2])
		require.Error(t, err)
	}

	require.NoError(t, tx.AutoMigrate(&Option{}, &UserGroupEntitlement{}, &UserGroupPricing{}))
	require.NoError(t, CheckIM21LockedState(tx))
	catalog := Option{Key: groupCatalogOptionKey, Value: `{"group-one":{"exclusive":true}}`}
	require.NoError(t, tx.Create(&catalog).Error)
	require.ErrorContains(t, CheckIM21LockedState(tx), "exclusive groups")
	require.NoError(t, tx.Delete(&catalog).Error)
	grant := UserGroupEntitlement{UserID: 1, GroupName: "group-one"}
	require.NoError(t, tx.Create(&grant).Error)
	require.ErrorContains(t, CheckIM21LockedState(tx), "personal group grants")
	require.NoError(t, tx.Delete(&grant).Error)
	policy := UserGroupPricing{UserID: 1, RatiosJSON: `{}`}
	require.NoError(t, tx.Create(&policy).Error)
	require.NoError(t, CheckIM21LockedState(tx))
	for _, raw := range []string{`{"group-one":0}`, `null`, `invalid`, ` `} {
		require.NoError(t, tx.Model(&policy).Update("ratios_json", raw).Error)
		require.ErrorContains(t, CheckIM21LockedState(tx), "personal pricing")
	}
}
