package model

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/dbpool"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	IM21Migrate = "migrate"
	IM21Serve   = "serve"
)

func IM21MigrationMode() (string, error) {
	mode, set := os.LookupEnv("CORE_IM21_MIGRATION_MODE")
	if _, rollout := os.LookupEnv(common.IM21RolloutModeEnv); rollout && !set {
		return "", errors.New("explicit im21 rollout requires CORE_IM21_MIGRATION_MODE=migrate or serve")
	}
	if _, release := os.LookupEnv("CORE_IM21_RELEASE_ID"); release && !set {
		return "", errors.New("explicit im21 release requires CORE_IM21_MIGRATION_MODE=migrate or serve")
	}
	if !set || mode == IM21Migrate || mode == IM21Serve {
		return mode, nil
	}
	return "", errors.New("CORE_IM21_MIGRATION_MODE must be migrate or serve when set")
}

// The artifact identifies itself from its actual executable, not an operator
// supplied tag, mutable VERSION override or external job record.
func im21ExecutableDigest() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat("/proc/self/exe"); err == nil {
		path = "/proc/self/exe" // pins the running inode even across a rename
	}
	exe, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer exe.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, exe); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// The managed path deliberately accepts only the tested PostgreSQL contract.
// Never fall back to a new SQLite file for a missing or mistyped DSN.
func openIM21Postgres(key, mode string) (*gorm.DB, error) {
	dsn, err := url.Parse(os.Getenv(key))
	if err != nil || (dsn.Scheme != "postgresql" && dsn.Scheme != "postgres") || dsn.Hostname() == "" || strings.Trim(dsn.Path, "/") == "" {
		return nil, fmt.Errorf("managed rollout requires a PostgreSQL URL in %s", key)
	}
	pool, err := dbpool.Load(os.LookupEnv, key == "LOG_SQL_DSN")
	if err != nil {
		return nil, err
	}
	if mode == IM21Migrate && key == "SQL_DSN" && pool.MaxOpen < 2 {
		return nil, errors.New("managed migration requires at least two primary SQL connections for durable receipt invalidation")
	}
	query, err := url.ParseQuery(dsn.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid PostgreSQL query parameters in %s", key)
	}
	query.Set("connect_timeout", "5")
	dsn.RawQuery = query.Encode()
	db, err := gorm.Open(postgresMigrationDialector{postgres.Dialector{Config: &postgres.Config{
		DSN: dsn.String(), PreferSimpleProtocol: true,
	}}}, newGormConfig(false))
	if err != nil {
		return nil, fmt.Errorf("cannot open PostgreSQL database configured by %s", key)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	pool.Apply(sqlDB)
	return db, nil
}

// All migration statements, including extension callbacks/implicit GORM DDL,
// run on the *same transaction* that owns the advisory lock and SET LOCAL
// timeouts. No detached session locks or one-off SETs on a pool.
func im21SchemaTransaction(db *gorm.DB, migrate bool, fn func(*gorm.DB) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL lock_timeout = '5s'").Error; err != nil {
			return err
		}
		if err := tx.Exec("SET LOCAL statement_timeout = '90s'").Error; err != nil {
			return err
		}
		lock := "SELECT pg_try_advisory_xact_lock_shared(832653145, current_schema()::regnamespace::oid::int)"
		if migrate {
			lock = "SELECT pg_try_advisory_xact_lock(832653145, current_schema()::regnamespace::oid::int)"
		}
		var locked bool
		if err := tx.Raw(lock).Row().Scan(&locked); err != nil {
			return err
		}
		if !locked {
			return errors.New("im21 schema lock busy; wait for the other job (leave LOG_SQL_DSN unset for shared schema)")
		}
		return fn(tx)
	}, &sql.TxOptions{ReadOnly: !migrate})
}

const im21ReceiptTable = "core_im21_schema_receipts"

func writeIM21Receipt(tx *gorm.DB, role, digest, runID string) error {
	if err := tx.Exec("CREATE TABLE IF NOT EXISTS " + im21ReceiptTable +
		" (role text PRIMARY KEY, binary_sha256 text NOT NULL, run_id text NOT NULL)").Error; err != nil {
		return err
	}
	return tx.Exec("INSERT INTO "+im21ReceiptTable+" (role, binary_sha256, run_id) VALUES (?, ?, ?) "+
		"ON CONFLICT (role) DO UPDATE SET binary_sha256 = EXCLUDED.binary_sha256, run_id = EXCLUDED.run_id", role, digest, runID).Error
}

func readIM21Receipt(tx *gorm.DB, role, digest, releaseID string) (string, error) {
	var runID string
	// A schema listed later in search_path may have been migrated, but that
	// is not evidence for this connection's current target.
	if err := tx.Raw("SELECT run_id FROM "+im21ReceiptTable+" WHERE role = ? AND binary_sha256 = ? AND "+
		"tableoid = to_regclass(format('%I.%I', current_schema(), ?))", role, digest, im21ReceiptTable).Row().Scan(&runID); err != nil || !strings.HasPrefix(runID, releaseID+":") || len(runID) <= len(releaseID)+1 {
		return "", fmt.Errorf("missing or invalid im21 %s receipt for this executable/schema; run a successful migrator first", role)
	}
	return runID, nil
}

func invalidateIM21Receipt(db *gorm.DB) error {
	// Committed separately, while the primary migration transaction holds the
	// exclusive advisory lock: an aborted repeat job cannot leave yesterday's
	// success usable. Never create receipt metadata ahead of financial preflight.
	ctx, cancel := context.WithTimeout(db.Statement.Context, 5*time.Second)
	defer cancel()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var exists bool
		if err := tx.Raw("SELECT to_regclass(format('%I.%I', current_schema(), ?)) IS NOT NULL", im21ReceiptTable).Row().Scan(&exists); err != nil {
			return err
		}
		if exists {
			return tx.Exec("DELETE FROM " + im21ReceiptTable + " WHERE role = 'primary'").Error
		}
		return nil
	})
}

// InitIM21Databases is called before any runtime services, request handlers or
// background jobs. The globals are bound to transactions only in this isolated,
// single-threaded startup phase and restored to their pools before returning.
func InitIM21Databases(mode string) error {
	if mode != IM21Migrate && mode != IM21Serve {
		return errors.New("invalid managed rollout database mode")
	}
	if common.GetEnvOrDefaultBool("SKIP_64BIT_QUOTA_SCHEMA_CHECK", false) || (mode == IM21Migrate && !common.IsMasterNode) {
		return errors.New("managed migrator requires a master role and all quota schema preflight checks")
	}
	releaseID := os.Getenv("CORE_IM21_RELEASE_ID")
	if _, err := hex.DecodeString(releaseID); err != nil || len(releaseID) != 32 || strings.ToLower(releaseID) != releaseID {
		return errors.New("CORE_IM21_RELEASE_ID must be 32 lowercase hex characters unique to this release window")
	}
	digest, err := im21ExecutableDigest()
	if err != nil {
		return fmt.Errorf("fingerprint im21 executable: %w", err)
	}
	primary, err := openIM21Postgres("SQL_DSN", mode)
	if err != nil {
		return err
	}
	DB = primary
	LOG_DB = primary
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	common.SetLogDatabaseType(common.DatabaseTypePostgreSQL)
	initCol()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	migrate := mode == IM21Migrate
	runID := releaseID + ":" + rand.Text()
	return im21SchemaTransaction(primary.WithContext(ctx), migrate, func(mainTx *gorm.DB) error {
		if migrate {
			if err := invalidateIM21Receipt(primary.WithContext(ctx)); err != nil {
				return err
			}
		}
		logDB := primary
		if os.Getenv("LOG_SQL_DSN") != "" {
			logDB, err = openIM21Postgres("LOG_SQL_DSN", mode)
			if err != nil {
				return err
			}
		}
		LOG_DB = logDB
		logStep := func(logTx *gorm.DB) error {
			if !migrate {
				mainID, err := readIM21Receipt(mainTx, "primary", digest, releaseID)
				if err != nil {
					return err
				}
				logID, err := readIM21Receipt(logTx, "log", digest, releaseID)
				if err != nil {
					return err
				}
				if mainID != logID {
					return errors.New("im21 primary and log migration receipts do not match")
				}
			}
			if _, err := AgentSimpleCutoverPreflight(mainTx); err != nil {
				return err
			}
			if err := ensureUserQuotaColumns(mainTx, common.DatabaseTypePostgreSQL); err != nil {
				return err
			}
			if !migrate {
				return loadLegacyAccessTokenRetireAt(mainTx)
			}
			DB, LOG_DB = mainTx, logTx
			defer func() { DB, LOG_DB = primary, logDB }()
			if err := migrateMainSchema(true); err != nil {
				return fmt.Errorf("migrate main schema: %w", err)
			}
			if err := MigrateRetiredFrontendOptions(); err != nil {
				return fmt.Errorf("migrate retired options: %w", err)
			}
			// Malformed legacy options were deliberately retained by the
			// best-effort conversion. Do not certify an incomplete conversion.
			var pending int64
			if err := mainTx.Model(&Option{}).Where(commonKeyCol+" IN ?", []string{"ApiInfo", "Announcements", "FAQ", "UptimeKumaUrl", "UptimeKumaSlug"}).Count(&pending).Error; err != nil {
				return err
			}
			if pending != 0 {
				return errors.New("retired frontend options need manual reconciliation")
			}
			if logDB == primary {
				if err := MigrateAuditLogs(); err != nil {
					return err
				}
			} else if err := migrateLOGDB(); err != nil {
				return fmt.Errorf("migrate log schema: %w", err)
			}
			// With separate DBs, log commits before primary. A partial commit
			// can never produce a matching pair for a serving node.
			if err := writeIM21Receipt(logTx, "log", digest, runID); err != nil {
				return err
			}
			return writeIM21Receipt(mainTx, "primary", digest, runID)
		}
		if logDB == primary {
			return logStep(mainTx)
		}
		return im21SchemaTransaction(logDB.WithContext(ctx), migrate, logStep)
	})
}

// Reject rollback into locked mode rather than silently dropping an existing
// user's exclusive access or changing the rate used on their next request.
// Presentation metadata/cleared prices and retained pricing audits are safe.
func CheckIM21LockedState(db *gorm.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	groups, err := readGroupCatalogTx(db)
	if err != nil {
		return fmt.Errorf("cannot verify locked group catalog: %w", err)
	}
	for _, group := range groups {
		if group.Exclusive {
			return errors.New("locked rollout cannot serve existing exclusive groups; restore active configuration")
		}
	}
	var grants []int
	if err := db.Model(&UserGroupEntitlement{}).Select("1").Limit(1).Scan(&grants).Error; err != nil {
		return err
	}
	if len(grants) != 0 {
		return errors.New("locked rollout cannot serve existing personal group grants")
	}
	// The writer canonically stores a cleared policy as "{}". Parse all other
	// representations rather than treating malformed/NULL JSON as no pricing.
	rows, err := db.Model(&UserGroupPricing{}).Select("ratios_json").Where("ratios_json IS NULL OR ratios_json <> ?", "{}").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		policy := UserGroupPricing{RatiosJSON: raw}
		if strings.TrimSpace(raw) == "" || policy.decodeRatios() != nil || len(policy.Ratios) != 0 {
			return errors.New("locked rollout cannot serve existing or unreadable personal pricing")
		}
	}
	return rows.Err()
}
