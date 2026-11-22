package model

import (
	"fmt"

	"gorm.io/gorm"
)

// migrateChannelGroupToText widens the channel's comma-separated group list,
// not the individual group names in abilities and other tables.
func migrateChannelGroupToText(db *gorm.DB) error {
	if db.Dialector.Name() != "postgres" {
		return nil
	}

	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(&Channel{}); err != nil {
		return fmt.Errorf("parse channel schema for group migration: %w", err)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SET LOCAL lock_timeout = '5s'`).Error; err != nil {
			return fmt.Errorf("set lock timeout for channel group migration: %w", err)
		}
		var column struct {
			QualifiedTable string
			TypeName       string
			IsVarchar      bool
			IsText         bool
		}
		result := tx.Raw(`
SELECT pg_catalog.format('%I.%I', n.nspname, c.relname) AS qualified_table,
       pg_catalog.format_type(a.atttypid, a.atttypmod) AS type_name,
       a.atttypid = 'pg_catalog.varchar'::pg_catalog.regtype AS is_varchar,
       a.atttypid = 'pg_catalog.text'::pg_catalog.regtype AS is_text
FROM pg_catalog.pg_attribute AS a
JOIN pg_catalog.pg_class AS c ON c.oid = a.attrelid
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
WHERE a.attrelid = pg_catalog.to_regclass(?)
  AND a.attname = 'group' AND a.attnum > 0 AND NOT a.attisdropped`, stmt.Quote(stmt.Schema.Table)).Scan(&column)
		if result.Error != nil {
			return fmt.Errorf("inspect channel group column: %w", result.Error)
		}
		if result.RowsAffected == 0 || column.IsText {
			return nil // AutoMigrate creates a missing column with the new type.
		}
		if !column.IsVarchar {
			return fmt.Errorf("migrate channel group to text: unexpected column type %s", column.TypeName)
		}
		// PostgreSQL quotes both identifier parts above. Widen directly to
		// preserve the existing values and default without truncating either.
		if err := tx.Exec(`ALTER TABLE ` + column.QualifiedTable + ` ALTER COLUMN "group" TYPE pg_catalog.text`).Error; err != nil {
			return fmt.Errorf("migrate channel group to text: %w", err)
		}
		return nil
	})
}
