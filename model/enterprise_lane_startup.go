package model

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	ErrEnterpriseLaneUnavailable = errors.New("enterprise lane schema or PostgreSQL state is unavailable")
	ErrEnterpriseLaneState       = errors.New("enterprise lane transition not permitted")
)

// CheckEnterpriseLaneGatewayDisabled fences ordinary startup in both builds.
// It recognizes trusted P3/P4 lab schemas without migrating or enabling them.
// Non-PostgreSQL installs and PostgreSQL schemas with no lab tables retain the
// ordinary path. Partial schema, unresolved counters, or projection work fail closed.
//
// This read-only, bounded snapshot is NOT installation/activation authority or
// a financial audit. It does not verify return-ledger evidence or historical
// completeness (missing zero-actual receipts cannot be inferred from counters).
// Serving initialization precedes this check; migrator mode does not call it.
// It does not stop an already-running or older binary, nor
// prevent a later writer activating escrow. Deployment topology must exclude
// those writers; a future default-on owner epoch needs an ongoing authority fence.
func CheckEnterpriseLaneGatewayDisabled(ctx context.Context, db *gorm.DB) error {
	if ctx == nil || db == nil || db.Config == nil || db.Dialector == nil || db.Statement == nil || db.DryRun {
		return ErrEnterpriseLaneUnavailable
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	if _, nested := db.Config.ConnPool.(gorm.TxCommitter); nested {
		return ErrEnterpriseLaneUnavailable
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return ErrEnterpriseLaneUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	reader := db.Session(&gorm.Session{NewDB: true, Context: ctx, SkipHooks: true, Logger: logger.Discard})
	err := reader.Transaction(func(tx *gorm.DB) error {
		var schema string
		if err := tx.Raw("SELECT current_schema()").Scan(&schema).Error; err != nil || schema == "" {
			return ErrEnterpriseLaneUnavailable
		}
		var tables []struct{ Name, Kind string }
		if err := tx.Raw(`SELECT c.relname AS name, c.relkind::text AS kind
			FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = ? AND c.relname IN ('enterprise_lanes', 'enterprise_lane_requests', 'enterprise_lane_usage_projections')`, schema).Scan(&tables).Error; err != nil {
			return err
		}
		if len(tables) == 0 {
			return nil
		}
		present := make(map[string]bool, len(tables))
		for _, table := range tables {
			if table.Kind != "r" && table.Kind != "p" {
				return ErrEnterpriseLaneUnavailable
			}
			present[table.Name] = true
		}
		if !present["enterprise_lanes"] || !present["enterprise_lane_requests"] {
			return ErrEnterpriseLaneUnavailable
		}
		var projectionColumn bool
		if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = ? AND table_name = 'enterprise_lane_requests' AND column_name = 'usage_projection_pending')`, schema).Scan(&projectionColumn).Error; err != nil {
			return err
		}
		if projectionColumn != present["enterprise_lane_usage_projections"] {
			return ErrEnterpriseLaneUnavailable
		}
		// Bind all evidence to the same schema, never to a later search-path
		// entry or temporary table. Schema is an identifier, not SQL input.
		prefix := `"` + strings.ReplaceAll(schema, `"`, `""`) + `".`
		lanes, requests := prefix+"enterprise_lanes", prefix+"enterprise_lane_requests"
		var blocked bool
		if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM `+lanes+` l WHERE (
			l.state = 'drained' AND l.lane_id ~ '^[A-Za-z0-9_-]{1,64}$' AND l.epoch > 0
			AND l.lane_key = l.lane_id || ':' || l.epoch::text AND l.user_id > 0 AND l.token_id > 0
			AND l.token_fingerprint ~ '^[a-fA-F0-9]{64}$'
			AND l.paid_budget BETWEEN 1 AND ? AND l.token_budget BETWEEN 1 AND ?
			AND l.paid_available = 0 AND l.token_available = 0 AND l.held = 0 AND l.open_requests = 0
			AND l.spent >= 0 AND l.paid_returned >= 0 AND l.token_returned >= 0
			AND l.spent::numeric + l.paid_returned = l.paid_budget
			AND l.spent::numeric + l.token_returned = l.token_budget) IS NOT TRUE)`, common.MaxWalletQuota, common.MaxWalletQuota).Scan(&blocked).Error; err != nil {
			return err
		}
		if blocked {
			return ErrEnterpriseLaneState
		}
		if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM `+requests+` r LEFT JOIN `+lanes+` l
			ON l.lane_id = r.lane_id AND l.epoch = r.epoch WHERE (
			l.lane_key IS NOT NULL AND r.request_id ~ '^[A-Za-z0-9_-]{1,64}$'
			AND r.user_id = l.user_id AND r.token_id = l.token_id AND r.token_fingerprint = l.token_fingerprint
			AND r.held BETWEEN 1 AND ? AND r.actual BETWEEN 0 AND r.held
			AND ((r.state = 'settled' AND r.refund_proof = '')
			OR (r.state = 'refunded' AND r.actual = 0 AND r.refund_proof = 'dispatch_not_claimed'))) IS NOT TRUE)
			OR EXISTS (SELECT 1 FROM `+lanes+` l WHERE l.spent::numeric <> COALESCE((
			SELECT SUM(r.actual::numeric) FROM `+requests+` r WHERE r.lane_id = l.lane_id
			AND r.epoch = l.epoch AND r.state = 'settled'), 0))`, common.MaxQuota).Scan(&blocked).Error; err != nil {
			return err
		}
		if blocked {
			return ErrEnterpriseLaneState
		}
		if !projectionColumn {
			return nil // P3 has no detached usage schema.
		}
		// P4 zero-usage model-only receipts intentionally have no projection.
		// All measured terminal receipts need an exact detached receipt, and
		// detached receipts without their original owner are never ignored.
		projections := prefix + "enterprise_lane_usage_projections"
		if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM `+requests+` WHERE usage_projection_pending IS DISTINCT FROM FALSE)
			OR EXISTS (SELECT 1 FROM `+requests+` r FULL JOIN `+projections+` p ON p.request_id = r.request_id WHERE (
			(r.request_id IS NOT NULL AND p.request_id IS NULL AND r.usage_channel_id = 0
			AND r.usage_prompt_tokens = 0 AND r.usage_completion_tokens = 0 AND r.usage_model = ''
			AND r.usage_group = '' AND r.usage_observed_at = 0)
			OR (r.request_id IS NOT NULL AND p.request_id IS NOT NULL AND r.state = 'settled'
			AND p.lane_id = r.lane_id AND p.epoch = r.epoch AND p.user_id = r.user_id AND p.token_id = r.token_id
			AND p.actual = r.actual AND p.projected_at > 0 AND r.usage_channel_id > 0 AND r.usage_observed_at > 0
			AND r.usage_prompt_tokens >= 0 AND r.usage_completion_tokens >= 0
			AND r.usage_prompt_tokens::numeric + r.usage_completion_tokens BETWEEN 1 AND ?
			AND octet_length(r.usage_model) BETWEEN 1 AND 128 AND octet_length(r.usage_group) BETWEEN 1 AND 128
			AND p.usage_channel_id = r.usage_channel_id AND p.usage_prompt_tokens = r.usage_prompt_tokens
			AND p.usage_completion_tokens = r.usage_completion_tokens AND p.usage_model = r.usage_model
			AND p.usage_group = r.usage_group AND p.usage_observed_at = r.usage_observed_at)) IS NOT TRUE)`, common.MaxQuota).Scan(&blocked).Error; err != nil {
			return err
		}
		if blocked {
			return ErrEnterpriseLaneState
		}
		return nil
	}, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err == nil || errors.Is(err, ErrEnterpriseLaneState) {
		return err
	}
	if ctx.Err() != nil {
		return errors.Join(ErrEnterpriseLaneUnavailable, ctx.Err())
	}
	return ErrEnterpriseLaneUnavailable // Never expose SQL, DSN or receipt data.
}
