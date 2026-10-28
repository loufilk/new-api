package model

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	mysqldsn "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// These fixtures create only uniquely prefixed tables in a disposable loopback
// database. Never accept a remote database or reuse/drop a pre-existing table.
func rootAffCodeFixtureLoopback(dialect, dsn string) bool {
	var host string
	switch dialect {
	case "mysql":
		config, err := mysqldsn.ParseDSN(dsn)
		if err != nil || config.Net != "tcp" || config.DBName == "" {
			return false
		}
		host, _, err = net.SplitHostPort(config.Addr)
		if err != nil {
			return false
		}
	case "postgres":
		parsed, err := url.Parse(dsn)
		if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Path == "" || parsed.Path == "/" {
			return false
		}
		// A libpq query parameter must not override the URL's approved host.
		if parsed.Query().Has("host") || parsed.Query().Has("hostaddr") || parsed.Query().Has("service") {
			return false
		}
		host = parsed.Hostname()
	default:
		return false
	}
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func TestRootAffCodeFixtureRejectsRemoteDatabases(t *testing.T) {
	for _, tc := range []struct {
		dialect, dsn string
		allowed      bool
	}{
		{"mysql", "fixture:synthetic@tcp(127.0.0.1:3306)/repair_compat", true},
		{"mysql", "fixture:synthetic@tcp([::1]:3306)/repair_compat", true},
		{"mysql", "fixture:synthetic@tcp(database.example:3306)/repair_compat", false},
		{"mysql", "fixture:synthetic@unix(/tmp/mysql.sock)/repair_compat", false},
		{"postgres", "postgres://fixture:synthetic@localhost:5432/repair_compat?sslmode=disable", true},
		{"postgres", "postgres://fixture:synthetic@203.0.113.1:5432/repair_compat", false},
		{"postgres", "postgres://fixture:synthetic@localhost/repair_compat?host=database.example", false},
		{"postgres", "postgres://fixture:synthetic@localhost/repair_compat?hostaddr=203.0.113.1", false},
		{"postgres", "host=localhost dbname=repair_compat", false},
	} {
		assert.Equal(t, tc.allowed, rootAffCodeFixtureLoopback(tc.dialect, tc.dsn))
	}
}

// Setup concurrency relies on the existing users.aff_code unique index because
// root setup writes the empty string. Inspect the real database catalog and test
// enforcement instead of inferring either from GORM tags. This is NOT a general
// singleton-root constraint: roots with different affiliate codes remain legal.
func TestRootAffCodeUniqueIndexDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "aff-index.db"))
			if dialect != "sqlite" {
				env := "TEST_MYSQL_DSN"
				if dialect == "postgres" {
					env = "TEST_POSTGRES_DSN"
				}
				dsn := strings.TrimSpace(os.Getenv(env))
				if dsn == "" {
					t.Skip(env + " is not configured")
				}
				require.True(t, rootAffCodeFixtureLoopback(dialect, dsn), "only a disposable loopback database is permitted")
				if dialect == "mysql" {
					driver = mysql.Open(dsn)
				} else {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
			}
			naming := schema.NamingStrategy{TablePrefix: fmt.Sprintf("affidx_%x_", time.Now().UnixNano())}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: naming})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			table := naming.TableName("User")
			require.False(t, db.Migrator().HasTable(table), "must never reuse or drop an existing table")
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(table)) })
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("aff_code catalog/enforcement database: %s %s", dialect, version)
			for _, phase := range []string{"fresh", "repeated_startup"} {
				t.Run(phase, func(t *testing.T) {
					require.NoError(t, db.AutoMigrate(&User{}))
					indexes, err := db.Migrator().GetIndexes(&User{})
					require.NoError(t, err)
					found := false
					for _, index := range indexes {
						columns := index.Columns()
						unique, known := index.Unique()
						if len(columns) == 1 && columns[0] == "aff_code" && known && unique {
							found = true
							t.Logf("catalog index: name=%s columns=%v unique=%t", index.Name(), columns, unique)
						}
					}
					require.True(t, found, "real catalog must contain a single-column unique aff_code index")
					if phase == "fresh" {
						require.NoError(t, db.Create(&User{Username: "root-aff-first", Password: "synthetic-hash", Role: common.RoleRootUser, AffCode: ""}).Error)
					}
					err = db.Create(&User{Username: "root-aff-duplicate", Password: "synthetic-hash", Role: common.RoleRootUser, AffCode: ""}).Error
					require.Error(t, err, "duplicate empty affiliate code must be rejected by the database")
					var count int64
					require.NoError(t, db.Model(&User{}).Where("aff_code = ?", "").Count(&count).Error)
					assert.EqualValues(t, 1, count)
				})
			}
			require.NoError(t, db.Create(&User{Username: "root-aff-distinct", Password: "synthetic-hash", Role: common.RoleRootUser, AffCode: "distinct-fixture-code"}).Error)
			t.Log("Distinct affiliate codes permit another root; this index is not a singleton-role constraint.")
		})
	}
}
