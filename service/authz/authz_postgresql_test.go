package authz

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const legacyScopedPoliciesPostgresSchema = "authz_legacy_scoped_policies_pg"

func dsnWithSearchPath(dsn, schema string) (string, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return "", err
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String(), nil
	}
	return dsn + " search_path=" + schema, nil
}

// openAuthzPostgresSchema isolates the legacy scoped policy fixture in its own
// schema inside the explicitly configured database. A present DSN that cannot
// connect, report its server version or migrate fails the run; the test only
// skips when the operator did not opt in. The actual server_version is logged
// as evidence.
func openAuthzPostgresSchema(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_AUTHZ_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TEST_AUTHZ_POSTGRES_DSN to run the legacy scoped policy PostgreSQL test")
	}
	isolatedDSN, err := dsnWithSearchPath(dsn, legacyScopedPoliciesPostgresSchema)
	require.NoError(t, err)

	db, err := gorm.Open(postgres.Open(isolatedDSN), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err, "TEST_AUTHZ_POSTGRES_DSN is set but the connection failed")

	var version, serverVersion string
	require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	require.NoError(t, db.Raw("SHOW server_version").Scan(&serverVersion).Error)
	require.NotEmpty(t, version)
	require.NotEmpty(t, serverVersion)
	t.Logf("PostgreSQL server_version evidence: server_version=%s version=%s", serverVersion, version)

	require.NoError(t, db.Exec(`DROP SCHEMA IF EXISTS "`+legacyScopedPoliciesPostgresSchema+`" CASCADE`).Error)
	require.NoError(t, db.Exec(`CREATE SCHEMA "`+legacyScopedPoliciesPostgresSchema+`"`).Error)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	return db
}

// TestLegacyScopedPoliciesDoNotExpandPermissionsPostgreSQL is the PostgreSQL
// counterpart of TestLegacyScopedPoliciesDoNotExpandPermissions. The 043ff99a
// authz changes read legacy own/other scoped rows from the real database, so
// SQLite alone cannot represent the deployed behavior. This exercises master
// and slave init/reload cycles against an isolated PostgreSQL schema and
// proves: unsupported scopes stay deny (no admin baseline fallback), supported
// rows keep working, and every legacy row is preserved for administrator
// review.
func TestLegacyScopedPoliciesDoNotExpandPermissionsPostgreSQL(t *testing.T) {
	for _, master := range []bool{true, false} {
		name := "master"
		if !master {
			name = "slave"
		}
		t.Run(name, func(t *testing.T) {
			db := openAuthzPostgresSchema(t)
			wasMaster := common.IsMasterNode
			common.IsMasterNode = master
			t.Cleanup(func() { common.IsMasterNode = wasMaster })

			rules := []model.CasbinRule{
				{Ptype: "p", V0: "role:vendor", V1: "channel", V2: "read", V3: "allow", V4: "own"},
				{Ptype: "p", V0: UserSubject(42), V1: "channel", V2: "read", V3: "allow", V4: "own"},
				{Ptype: "p", V0: UserSubject(43), V1: "channel", V2: "sensitive_write", V3: "allow", V4: "all"},
				{Ptype: "p", V0: UserSubject(44), V1: "channel", V2: "secret_view", V3: "deny", V4: "all"},
				{Ptype: "p", V0: UserSubject(45), V1: "channel", V2: "read"},
				{Ptype: "p", V0: UserSubject(46), V1: "channel", V2: "read", V3: "allow", V4: "unknown-scope"},
				{Ptype: "p", V0: UserSubject(47), V1: "channel", V2: "read", V3: "allow", V5: "own"},
				{Ptype: "p", V0: UserSubject(48), V1: "channel", V2: "read", V3: "allow", V4: "own"},
				{Ptype: "p", V0: UserSubject(48), V1: "channel", V2: "read", V3: "allow", V4: "all"},
				{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "channel", V2: "operate", V3: "allow", V4: "own"},
				{Ptype: "p", V0: UserSubject(50), V1: "channel", V2: "sensitive_write", V3: "allow"},
				{Ptype: "g", V0: UserSubject(99), V1: RoleSubject(BuiltInRoleAdmin)},
			}
			require.NoError(t, db.Create(&rules).Error)
			ids := make([]uint, len(rules))
			for i := range rules {
				ids[i] = rules[i].Id
			}
			for range 2 {
				require.NoError(t, Init(db))
				require.NoError(t, ReloadPolicy())
				assert.False(t, Can(42, common.RoleAdminUser, ChannelRead), "own must not fall back to the admin allow baseline")
				assert.True(t, Can(43, common.RoleAdminUser, ChannelSensitiveWrite))
				assert.False(t, Can(44, common.RoleAdminUser, ChannelSecretView))
				assert.True(t, Can(45, common.RoleAdminUser, ChannelRead))
				for _, userID := range []int{46, 47, 48} {
					assert.False(t, Can(userID, common.RoleAdminUser, ChannelRead))
				}
				assert.False(t, Can(51, common.RoleAdminUser, ChannelOperate), "reseed must not erase a scoped role restriction")
				assert.True(t, Can(50, common.RoleAdminUser, ChannelSensitiveWrite))
				assert.False(t, Can(99, common.RoleCommonUser, ChannelRead))
				var stored []model.CasbinRule
				require.NoError(t, db.Where("id IN ?", ids).Order("id").Find(&stored).Error)
				assert.Equal(t, rules, stored, "legacy rows must remain available for administrator review")
			}
		})
	}
}
