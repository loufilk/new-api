//go:build lane_billing_integration_dev

package model

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

// LaneUsageEndpointDev is an externally supplied expected identity, not a DSN.
// Names plus PostgreSQL object IDs detect accidental pool swaps or a recreated
// schema with the same name. This is not a restore/failover authority: cloned
// clusters can carry the same IDs and must not be used concurrently.
type LaneUsageEndpointDev struct {
	Address          string
	Port             int
	Database, Schema string
	DatabaseOID      int64 `gorm:"column:database_oid"`
	SchemaOID        int64 `gorm:"column:schema_oid"`
}

func (e LaneUsageEndpointDev) valid() bool {
	ip, err := netip.ParseAddr(e.Address)
	return err == nil && ip.IsLoopback() && e.Port >= 1 && e.Port <= 65535 &&
		laneBillingOwnerIDValid(e.Database) && laneBillingOwnerIDValid(e.Schema) && strings.HasPrefix(e.Schema, "lane_billing_dev_") &&
		e.DatabaseOID > 0 && e.SchemaOID > 0
}

type laneUsageStoreDev struct {
	db    *gorm.DB
	scope *LaneUsageEndpointDev // nil only for the original explicit fixture entry point
}

func (s laneUsageStoreDev) tx(ctx context.Context, apply func(*gorm.DB) error) error {
	return withLaneBillingFiniteKeyDevTx(ctx, s.db, func(tx *gorm.DB) error {
		if s.scope != nil {
			// Pin the connection doing the actual work, not another connection
			// obtained by probing a shared pool before BEGIN.
			var actual LaneUsageEndpointDev
			result := tx.Raw(`SELECT host(inet_server_addr()) AS address, inet_server_port() AS port,
				current_database() AS database, current_schema() AS schema,
				(SELECT oid::bigint FROM pg_catalog.pg_database WHERE datname = current_database()) AS database_oid,
				(SELECT oid::bigint FROM pg_catalog.pg_namespace WHERE nspname = current_schema()) AS schema_oid`).Scan(&actual)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 || actual != *s.scope {
				return ErrLaneBillingOwnerUnavailable
			}
		}
		return apply(tx)
	})
}

type laneUsagePageDev struct{ Candidates, Acknowledged int }

// Counts are per window. QueueObserved is an instantaneous sample of the main
// outbox, not a promise of global quiescence, ordinary Key export parity, or
// reconciliation of historical settlements with a missing outbox row.
type LaneUsageWindowDev struct {
	Pages, Candidates, Acknowledged         int
	QueueObserved, Due, Deferred, Conflicts bool
	Elapsed                                 time.Duration
}

var ErrLaneUsageSupervisorBusyDev = errors.New("synthetic lane usage supervisor already running")

// There is deliberately no global worker, init/start hook, schema migration,
// DDL, or ordinary admission capability. External stage/dev supervision owns
// the caller context, explicitly configured identity, and both database pools.
type LaneUsageSupervisorDev struct {
	main, logs  laneUsageStoreDev
	destination string
	batch       int
	active      atomic.Bool
}

func NewLaneUsageSupervisorDev(mainDB, logDB *gorm.DB, mainScope, logScope LaneUsageEndpointDev, destination string, batch int) (*LaneUsageSupervisorDev, error) {
	if mainDB == nil || logDB == nil || !mainScope.valid() || !logScope.valid() || !laneChildSHAValid(destination) || batch < 1 || batch > 32 {
		return nil, ErrLaneBillingOwnerUnavailable
	}
	return &LaneUsageSupervisorDev{main: laneUsageStoreDev{mainDB, &mainScope}, logs: laneUsageStoreDev{logDB, &logScope}, destination: destination, batch: batch}, nil
}

func (s *LaneUsageSupervisorDev) valid() bool {
	return s != nil && s.main.scope != nil && s.logs.scope != nil && s.batch >= 1 && s.batch <= 32
}

// Window is a bounded pass/catch-up. It does not wait out leases or sleep.
// With all producers stopped, a known queue sample with all flags false means
// no known pending/conflicted rows at that instant. Without producer fencing
// no worker may certify a final drain.
func (s *LaneUsageSupervisorDev) Window(ctx context.Context, maxPages int) (LaneUsageWindowDev, error) {
	if !s.valid() || ctx == nil || maxPages < 1 || maxPages > 8 {
		return LaneUsageWindowDev{}, ErrLaneBillingOwnerUnavailable
	}
	if !s.active.CompareAndSwap(false, true) {
		return LaneUsageWindowDev{}, ErrLaneUsageSupervisorBusyDev
	}
	defer s.active.Store(false)
	return s.window(ctx, maxPages)
}

func (s *LaneUsageSupervisorDev) window(ctx context.Context, maxPages int) (out LaneUsageWindowDev, err error) {
	started := time.Now()
	defer func() { out.Elapsed = time.Since(started) }()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for range maxPages {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.Pages++
		page, projectErr := projectLaneBillingUsagePageDev(ctx, s.main, s.logs, s.destination, s.batch)
		out.Candidates += page.Candidates
		out.Acknowledged += page.Acknowledged
		if projectErr != nil {
			err = projectErr
			break
		}
		// Do not spin on rows locked by other workers.
		if page.Candidates < s.batch || page.Acknowledged == 0 {
			break
		}
	}
	if ctx.Err() != nil {
		return out, errors.Join(err, ctx.Err())
	}
	// One statement/snapshot, three existence probes on the existing
	// (state,due_at,request_id) index. No COUNT or scan of all pending history.
	var flags struct{ Due, Deferred, Conflicts bool }
	probeErr := s.main.tx(ctx, func(tx *gorm.DB) error {
		return tx.Raw(`SELECT
			EXISTS(SELECT 1 FROM lane_billing_usage_outbox_dev_v1 WHERE state='pending' AND due_at <= EXTRACT(EPOCH FROM statement_timestamp())::bigint) AS due,
			EXISTS(SELECT 1 FROM lane_billing_usage_outbox_dev_v1 WHERE state='pending' AND due_at > EXTRACT(EPOCH FROM statement_timestamp())::bigint) AS deferred,
			EXISTS(SELECT 1 FROM lane_billing_usage_outbox_dev_v1 WHERE state='conflict') AS conflicts`).Scan(&flags).Error
	})
	if probeErr == nil {
		out.QueueObserved = true
		out.Due, out.Deferred, out.Conflicts = flags.Due, flags.Deferred, flags.Conflicts
	}
	return out, errors.Join(err, probeErr)
}

// Run calls at most one bounded Window per tick, serially, until cancellation
// or an invalid identity/scope. report runs synchronously and must not block.
// A lost COMMIT ACK is not success or permission to create a second log: both
// databases' durable receipts and retry lease remain authoritative.
func (s *LaneUsageSupervisorDev) Run(ctx context.Context, tick time.Duration, report func(LaneUsageWindowDev, error)) error {
	if !s.valid() || ctx == nil || tick < 100*time.Millisecond || tick > time.Minute || report == nil {
		return ErrLaneBillingOwnerUnavailable
	}
	if !s.active.CompareAndSwap(false, true) {
		return ErrLaneUsageSupervisorBusyDev
	}
	defer s.active.Store(false)
	delay := tick
	for ctx.Err() == nil {
		window, err := s.window(ctx, 1)
		report(window, err)
		if errors.Is(err, ErrLaneBillingOwnerUnavailable) || errors.Is(err, ErrLaneBillingOwnerEvidence) {
			return err
		}
		if err == nil || errors.Is(err, ErrLaneBillingUsageProjectionConflict) {
			delay = tick
		} else {
			delay = min(max(30*time.Second, tick), 2*delay)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}
