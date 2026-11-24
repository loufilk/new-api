package model

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var laneStartupFixtureSequence atomic.Int64

func laneStartupFixture(t *testing.T, generation int) *gorm.DB {
	t.Helper()
	raw := os.Getenv("TEST_MODEL_POSTGRES_DSN")
	if raw == "" {
		t.Skip("requires isolated loopback PostgreSQL TEST_MODEL_POSTGRES_DSN")
	}
	schema := fmt.Sprintf("lane_startup_%d_%d", os.Getpid(), laneStartupFixtureSequence.Add(1))
	require.NoError(t, DB.Exec(`CREATE SCHEMA "`+schema+`"`).Error)
	t.Cleanup(func() { require.NoError(t, DB.Exec(`DROP SCHEMA "`+schema+`" CASCADE`).Error) })
	u, err := url.Parse(raw)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(3)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	if generation == 0 {
		return db
	}
	require.NoError(t, db.Exec(`CREATE TABLE enterprise_lanes (
		lane_key text PRIMARY KEY, lane_id text, epoch bigint, user_id bigint, token_id bigint, token_fingerprint text,
		paid_budget bigint, token_budget bigint, paid_available bigint, token_available bigint,
		held bigint, spent bigint, open_requests bigint, paid_returned bigint, token_returned bigint, state text)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE enterprise_lane_requests (
		request_id text PRIMARY KEY, lane_id text, epoch bigint, user_id bigint, token_id bigint, token_fingerprint text,
		held bigint, actual bigint, state text, refund_proof text)`).Error)
	if generation == 4 {
		require.NoError(t, db.Exec(`ALTER TABLE enterprise_lane_requests
			ADD usage_projection_pending boolean DEFAULT false, ADD usage_channel_id bigint DEFAULT 0,
			ADD usage_prompt_tokens bigint DEFAULT 0, ADD usage_completion_tokens bigint DEFAULT 0,
			ADD usage_model text DEFAULT '', ADD usage_group text DEFAULT '', ADD usage_observed_at bigint DEFAULT 0`).Error)
		require.NoError(t, db.Exec(`CREATE TABLE enterprise_lane_usage_projections (
			request_id text PRIMARY KEY, lane_id text, epoch bigint, user_id bigint, token_id bigint, actual bigint,
			projected_at bigint, usage_channel_id bigint, usage_prompt_tokens bigint, usage_completion_tokens bigint,
			usage_model text, usage_group text, usage_observed_at bigint)`).Error)
	}
	return db
}

func seedLaneStartupDrained(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO enterprise_lanes VALUES
		('test:1', 'test', 1, 10, 20, repeat('a',64), 100, 80, 0, 0, 0, 30, 0, 70, 50, 'drained')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO enterprise_lane_requests
		(request_id,lane_id,epoch,user_id,token_id,token_fingerprint,held,actual,state,refund_proof)
		VALUES ('request','test',1,10,20,repeat('a',64),40,30,'settled','')`).Error)
}

func TestEnterpriseLaneStartupSchemaPostgres(t *testing.T) {
	for _, tc := range []struct {
		name        string
		generation  int
		mutation    string
		unavailable bool
	}{
		{"legacy_no_tables", 0, "", false},
		{"empty_p3", 3, "", false},
		{"empty_p4", 4, "", false},
		{"only_lane_table", 3, "DROP TABLE enterprise_lane_requests", true},
		{"orphan_request_table", 3, "DROP TABLE enterprise_lanes", true},
		{"orphan_projection_table", 4, "DROP TABLE enterprise_lanes; DROP TABLE enterprise_lane_requests", true},
		{"missing_projection_table", 4, "DROP TABLE enterprise_lane_usage_projections", true},
		{"missing_projection_column", 4, "ALTER TABLE enterprise_lane_requests DROP COLUMN usage_projection_pending", true},
		{"malformed_empty_table", 3, "ALTER TABLE enterprise_lanes DROP COLUMN paid_returned", true},
		{"not_a_table", 3, "ALTER TABLE enterprise_lanes RENAME TO lane_source; CREATE VIEW enterprise_lanes AS SELECT * FROM lane_source", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := laneStartupFixture(t, tc.generation)
			if tc.mutation != "" {
				require.NoError(t, db.Exec(tc.mutation).Error)
			}
			for range 2 {
				err := CheckEnterpriseLaneGatewayDisabled(t.Context(), db)
				if tc.unavailable {
					require.ErrorIs(t, err, ErrEnterpriseLaneUnavailable)
				} else {
					require.NoError(t, err)
				}
			}
		})
	}
}

func TestEnterpriseLaneStartupTerminalEvidencePostgres(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		blocked        bool
	}{
		{"drained_p3", "", false},
		{"open", "UPDATE enterprise_lanes SET state='open'", true},
		{"stopped", "UPDATE enterprise_lanes SET state='stopped'", true},
		{"unknown_state", "UPDATE enterprise_lanes SET state=NULL", true},
		{"held_after_drain", "UPDATE enterprise_lanes SET held=1", true},
		{"nonzero_available", "UPDATE enterprise_lanes SET paid_available=1", true},
		{"negative_return", "UPDATE enterprise_lanes SET paid_returned=-1", true},
		{"broken_conservation", "UPDATE enterprise_lanes SET token_returned=49", true},
		{"missing_owner_receipt", "DELETE FROM enterprise_lane_requests", true},
		{"unknown_delivery", "UPDATE enterprise_lane_requests SET state='delivery_unknown'", true},
		{"wrong_original_key", "UPDATE enterprise_lane_requests SET token_id=21", true},
		{"wrong_fingerprint", "UPDATE enterprise_lane_requests SET token_fingerprint=repeat('b',64)", true},
		{"orphan_receipt", "DELETE FROM enterprise_lanes", true},
		{"negative_actual", "UPDATE enterprise_lane_requests SET actual=-1", true},
		{"missing_actual", "UPDATE enterprise_lane_requests SET actual=NULL", true},
		{"false_refund_proof", "UPDATE enterprise_lane_requests SET refund_proof='expired'", true},
		{"valid_zero_spend_refund", "UPDATE enterprise_lane_requests SET actual=0,state='refunded',refund_proof='dispatch_not_claimed'; UPDATE enterprise_lanes SET spent=0,paid_returned=100,token_returned=80", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := laneStartupFixture(t, 3)
			seedLaneStartupDrained(t, db)
			if tc.mutation != "" {
				require.NoError(t, db.Exec(tc.mutation).Error)
			}
			err := CheckEnterpriseLaneGatewayDisabled(t.Context(), db)
			if tc.blocked {
				require.ErrorIs(t, err, ErrEnterpriseLaneState)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestEnterpriseLaneStartupProjectionPostgres(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		blocked        bool
	}{
		{"complete", "", false},
		{"pending_after_financial_drain", "UPDATE enterprise_lane_requests SET usage_projection_pending=true", true},
		{"unknown_projection_state", "UPDATE enterprise_lane_requests SET usage_projection_pending=NULL", true},
		{"missing_projection", "DELETE FROM enterprise_lane_usage_projections", true},
		{"orphan_projection", "DELETE FROM enterprise_lane_requests; DELETE FROM enterprise_lanes", true},
		{"wrong_projection_key", "UPDATE enterprise_lane_usage_projections SET token_id=21", true},
		{"wrong_projection_actual", "UPDATE enterprise_lane_usage_projections SET actual=29", true},
		{"wrong_projection_usage", "UPDATE enterprise_lane_usage_projections SET usage_prompt_tokens=99", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := laneStartupFixture(t, 4)
			seedLaneStartupDrained(t, db)
			require.NoError(t, CheckEnterpriseLaneGatewayDisabled(t.Context(), db), "model-only P4 receipts need no detached projection")
			require.NoError(t, db.Exec(`UPDATE enterprise_lane_requests SET usage_channel_id=7,usage_prompt_tokens=90,
				usage_completion_tokens=10,usage_model='fixture',usage_group='group',usage_observed_at=100`).Error)
			require.NoError(t, db.Exec(`INSERT INTO enterprise_lane_usage_projections VALUES
				('request','test',1,10,20,30,101,7,90,10,'fixture','group',100)`).Error)
			if tc.mutation != "" {
				require.NoError(t, db.Exec(tc.mutation).Error)
			}
			err := CheckEnterpriseLaneGatewayDisabled(t.Context(), db)
			if tc.blocked {
				require.ErrorIs(t, err, ErrEnterpriseLaneState)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestEnterpriseLaneStartupScopeAndCancellationPostgres(t *testing.T) {
	db := laneStartupFixture(t, 3)
	seedLaneStartupDrained(t, db)
	// Fully qualified evidence must not borrow a valid-looking temporary
	// shadow of the actual funded table.
	require.NoError(t, db.Connection(func(conn *gorm.DB) error {
		require.NoError(t, conn.Exec("CREATE TEMP TABLE enterprise_lanes (LIKE enterprise_lanes)").Error)
		defer conn.Exec("DROP TABLE pg_temp.enterprise_lanes")
		require.NoError(t, CheckEnterpriseLaneGatewayDisabled(t.Context(), conn))
		return nil
	}))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := CheckEnterpriseLaneGatewayDisabled(ctx, db)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, ErrEnterpriseLaneUnavailable)
	locked := db.Begin()
	require.NoError(t, locked.Error)
	require.ErrorIs(t, CheckEnterpriseLaneGatewayDisabled(t.Context(), locked), ErrEnterpriseLaneUnavailable, "nested handles cannot weaken snapshot ownership")
	defer locked.Rollback()
	require.NoError(t, locked.Exec("LOCK TABLE enterprise_lanes IN ACCESS EXCLUSIVE MODE").Error)
	ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err = CheckEnterpriseLaneGatewayDisabled(ctx, db)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, err, ErrEnterpriseLaneUnavailable)
	require.NoError(t, locked.Rollback().Error)
	require.NoError(t, CheckEnterpriseLaneGatewayDisabled(t.Context(), db), "a startup check never mutates financial rows")
}

type laneStartupOtherDialect struct {
	gorm.Dialector
	name string
}

func (d laneStartupOtherDialect) Name() string { return d.name }

func TestEnterpriseLaneStartupNonPostgresNoIO(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql"} {
		// No pool exists: either non-PG path performing I/O would fail. This
		// is a dispatch test, not an unrelated database compatibility matrix.
		db := &gorm.DB{Config: &gorm.Config{Dialector: laneStartupOtherDialect{name: dialect}}, Statement: &gorm.Statement{}}
		assert.NoError(t, CheckEnterpriseLaneGatewayDisabled(t.Context(), db))
	}
	assert.ErrorIs(t, CheckEnterpriseLaneGatewayDisabled(t.Context(), nil), ErrEnterpriseLaneUnavailable)
}
