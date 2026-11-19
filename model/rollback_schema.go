package model

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/extension"
	"gorm.io/gorm"
)

// This binary is a rollback artifact, never a master schema migrator. A missing
// or mistyped setting must NOT silently select the stock im.13 behavior.
func requireRollbackConfig() error {
	if os.Getenv("STARTUP_MIGRATIONS") != "verify-only" || os.Getenv("NODE_TYPE") != "master" {
		return errors.New("im.13 rollback requires STARTUP_MIGRATIONS=verify-only and NODE_TYPE=master")
	}
	for _, key := range []string{"SQL_DSN", "LOG_SQL_DSN"} {
		value := os.Getenv(key)
		if key == "LOG_SQL_DSN" && value == "" {
			continue
		}
		if !strings.HasPrefix(value, "postgres://") && !strings.HasPrefix(value, "postgresql://") {
			return fmt.Errorf("im.13 rollback requires a PostgreSQL %s", key)
		}
	}
	return nil
}

func verifyRollbackMain(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := verifyRollbackQuotaColumns(tx); err != nil {
			return err
		}
		models := append(mainAutoMigrateModels(extension.Default()), &SubscriptionPlan{})
		if err := verifyRollbackReadableColumns(tx, models); err != nil {
			return err
		}
		// These are im.18 objects, not im.13 models. Never create missing objects.
		if err := tx.Exec("SELECT writer_id, applied_seq FROM quota_data_flush_cursors LIMIT 0").Error; err != nil {
			return fmt.Errorf("im.18 report flush schema is missing: %w", err)
		}
		var setup Setup
		if err := tx.Where("initialized_at > 0").First(&setup).Error; err != nil {
			return fmt.Errorf("rollback requires an existing initialized site: %w", err)
		}
		return loadLegacyAccessTokenRetireAt(tx)
	}, &sql.TxOptions{ReadOnly: true})
}

func verifyRollbackQuotaColumns(db *gorm.DB) error {
	var columns int
	if err := db.Raw(`SELECT COUNT(*) FROM pg_catalog.pg_attribute
		WHERE attrelid = pg_catalog.to_regclass('quota_data') AND NOT attisdropped
		  AND attname IN ('username', 'model_name', 'use_group', 'node_name')
		  AND atttypid = 'pg_catalog.text'::regtype AND atttypmod = -1`).Scan(&columns).Error; err != nil {
		return err
	}
	if columns != 4 {
		return errors.New("rollback requires quota_data.username, model_name, use_group, node_name to already be unrestricted PostgreSQL text; no migration attempted")
	}
	return nil
}

func verifyRollbackLog(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return verifyRollbackReadableColumns(tx, []any{&Log{}, &AuditLog{}})
	}, &sql.TxOptions{ReadOnly: true})
}

// Ask PostgreSQL to resolve the live relations and all persisted im.13 column
// names using this connection's search_path, without scanning production rows.
func verifyRollbackReadableColumns(db *gorm.DB, models []any) error {
	for _, m := range models {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(m); err != nil {
			return err
		}
		var columns []string
		for _, name := range stmt.Schema.DBNames {
			if !stmt.Schema.FieldsByDBName[name].IgnoreMigration {
				columns = append(columns, name)
			}
		}
		if len(columns) == 0 {
			return fmt.Errorf("no persisted rollback columns for %s", stmt.Schema.Table)
		}
		rows, err := db.Model(m).Select(columns).Limit(0).Rows()
		if err != nil {
			return fmt.Errorf("rollback cannot read required columns of %s: %w", stmt.Schema.Table, err)
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}
