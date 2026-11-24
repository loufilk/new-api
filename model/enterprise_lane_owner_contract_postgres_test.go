//go:build enterprise_lane_lab

package model

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This is an executable isolation contract, not an installed bridge. The
// fixture is the sole writer of these marker values. The ordinary startup,
// tagged lab routes, schemas, and workers are deliberately unchanged.
const (
	contractLaneSource = "enterprise_lane"
	contractLaneState  = "lane_owner_v1"
)

var (
	errContractOwnerConflict = errors.New("cross-mode billing owner conflict")
	errContractOwnerEvidence = errors.New("isolated owner and hold evidence do not match")
)

func contractLaneMarker(id EnterpriseLaneIdentity, requestID string) BillingFinalization {
	return BillingFinalization{RequestID: requestID, UserID: id.UserID, TokenID: id.TokenID,
		TokenFingerprint: id.TokenFingerprint, Source: contractLaneSource, State: contractLaneState}
}

// reserveCanonicalLaneFixture is intentionally test-only. Its outer SQL
// transaction commits the existing lane hold and a synthetic canonical marker
// together. Both sides serialize on the ordinary request-ID PK first. A live
// bridge needs an independently audited installation/drain/reporting contract.
func reserveCanonicalLaneFixture(ctx context.Context, db *gorm.DB, id EnterpriseLaneIdentity, requestID string, amount int, beforeHold func() error) (EnterpriseLaneRequest, error) {
	if err := enterpriseLaneValidateRequest(db, id, requestID); err != nil {
		return EnterpriseLaneRequest{}, err
	}
	marker := contractLaneMarker(id, requestID)
	var held EnterpriseLaneRequest
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		insert := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&marker)
		if insert.Error != nil {
			return insert.Error
		}
		var owner BillingFinalization
		if err := lockForUpdate(tx).Where("request_id = ?", requestID).Take(&owner).Error; err != nil {
			return err
		}
		if !sameBillingIdentity(owner, marker) || owner.State != contractLaneState {
			return errContractOwnerConflict
		}
		// A historical receipt without a marker must not be adopted. Likewise,
		// a marker with no exact receipt must not create a second hold.
		var prior EnterpriseLaneRequest
		lookup := lockForUpdate(tx).Where("request_id = ?", requestID).Take(&prior).Error
		if lookup != nil && !errors.Is(lookup, gorm.ErrRecordNotFound) {
			return lookup
		}
		if insert.RowsAffected == 1 && lookup == nil || insert.RowsAffected == 0 && errors.Is(lookup, gorm.ErrRecordNotFound) {
			return errContractOwnerEvidence
		}
		if lookup == nil && (prior.EnterpriseLaneIdentity != id || prior.Held != amount) {
			return errContractOwnerEvidence
		}
		if beforeHold != nil {
			if err := beforeHold(); err != nil {
				return err
			}
		}
		var err error
		held, err = ReserveEnterpriseLane(ctx, tx, id, requestID, amount)
		return err
	})
	if err != nil {
		return EnterpriseLaneRequest{}, err
	}
	return held, nil
}

func ownerContractFixture(t *testing.T) EnterpriseLaneIdentity {
	t.Helper()
	id := enterpriseLaneFixture(t)
	require.NoError(t, DB.AutoMigrate(&BillingFinalization{}))
	t.Cleanup(func() {
		require.NoError(t, DB.Where("user_id = ?", id.UserID).Delete(&BillingFinalization{}).Error)
	})
	_, err := CreateEnterpriseLane(t.Context(), DB, id, 300, 300)
	require.NoError(t, err)
	return id
}

func ownerContractOrdinaryIdentity(id EnterpriseLaneIdentity, requestID string) BillingFinalization {
	return BillingFinalization{RequestID: requestID, UserID: id.UserID, TokenID: id.TokenID,
		TokenFingerprint: id.TokenFingerprint, Source: "wallet"}
}

func reserveOrdinaryOwnerFixture(ctx context.Context, authorization BillingFinalization, amount int, applied *atomic.Int32, beforeDebit func() error) (BillingFinalization, BillingReservationAttempt, error) {
	return ReserveBillingFinalization(ctx, authorization, nil, amount, func(tx *gorm.DB, receipt *BillingFinalization) error {
		applied.Add(1)
		if beforeDebit != nil {
			if err := beforeDebit(); err != nil {
				return err
			}
		}
		rid := authorization.RequestID
		debit, err := DebitQuotaTx(tx, LedgerEntry{UserId: authorization.UserID, Delta: amount,
			Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: rid,
			RequestId: rid, IdempotencyKey: "contract-ordinary:" + rid}, AccountPaid)
		if err != nil {
			return err
		}
		if debit.Replayed || len(debit.Rows) != 1 || debit.Rows[0].Delta != -amount || debit.Rows[0].Account != string(AccountPaid) {
			return errors.New("unexpected fixture debit evidence")
		}
		if err := ReserveBillingTokenTx(tx, receipt, amount, false); err != nil {
			return err
		}
		receipt.SourceHeld, receipt.Paid, receipt.TokenHeld = amount, amount, amount
		return nil
	})
}

type ownerContractBalances struct {
	paid, remain, used, lanePaid, laneToken, laneHeld, laneOpen int
	ledger, operations                                          int64
}

func readOwnerContractBalances(t *testing.T, id EnterpriseLaneIdentity) ownerContractBalances {
	t.Helper()
	var user User
	var token Token
	require.NoError(t, DB.Select("quota_paid").First(&user, id.UserID).Error)
	require.NoError(t, DB.Select("remain_quota", "used_quota").First(&token, id.TokenID).Error)
	lane := readLabLane(t, id)
	balances := ownerContractBalances{paid: user.QuotaPaid, remain: token.RemainQuota, used: token.UsedQuota, lanePaid: lane.PaidAvailable, laneToken: lane.TokenAvailable, laneHeld: lane.Held, laneOpen: lane.OpenRequests}
	require.NoError(t, DB.Model(&BalanceLedger{}).Where("user_id = ?", id.UserID).Count(&balances.ledger).Error)
	require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Where("user_id = ?", id.UserID).Count(&balances.operations).Error)
	return balances
}

func readOwnerContractClaims(t *testing.T, requestID string) (BillingFinalization, int64) {
	t.Helper()
	var owner BillingFinalization
	var laneRows int64
	require.NoError(t, DB.Where("request_id = ?", requestID).Take(&owner).Error)
	require.NoError(t, DB.Model(&EnterpriseLaneRequest{}).Where("request_id = ?", requestID).Count(&laneRows).Error)
	return owner, laneRows
}

func TestEnterpriseLaneOwnerIsolationContractPostgres(t *testing.T) {
	for _, laneFirst := range []bool{false, true} {
		name := "ordinary_wins"
		if laneFirst {
			name = "lane_wins"
		}
		t.Run(name, func(t *testing.T) {
			id := ownerContractFixture(t)
			auth := ownerContractOrdinaryIdentity(id, id.LaneID+"-race")
			before := readOwnerContractBalances(t, id)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			var workers sync.WaitGroup
			defer func() { cancel(); workers.Wait() }()
			var applied atomic.Int32
			held, release := make(chan struct{}), make(chan struct{})
			pauseWinner := func() error {
				close(held)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			// Observe the actual losing backend blocked by the actual winning
			// backend, not a schedulability assumption based on a sleep. Register
			// the hook before goroutines start and join them before cleanup.
			type backendKey struct{}
			winnerBackend, loserBackend := make(chan int, 1), make(chan int, 1)
			hook := "test:lane-owner-contract:" + auth.RequestID
			require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
				if backend, ok := tx.Statement.Context.Value(backendKey{}).(chan int); ok && tx.Statement.Table == "billing_finalizations" {
					var pid int
					if err := tx.Session(&gorm.Session{NewDB: true}).Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
						tx.AddError(err)
						return
					}
					backend <- pid
				}
			}))
			t.Cleanup(func() { require.NoError(t, DB.Callback().Create().Remove(hook)) })
			winner, loser := make(chan error, 1), make(chan error, 1)
			winnerCtx := context.WithValue(ctx, backendKey{}, winnerBackend)
			if laneFirst {
				workers.Go(func() {
					_, err := reserveCanonicalLaneFixture(winnerCtx, DB, id, auth.RequestID, 40, pauseWinner)
					winner <- err
				})
			} else {
				workers.Go(func() {
					_, _, err := reserveOrdinaryOwnerFixture(winnerCtx, auth, 40, &applied, pauseWinner)
					winner <- err
				})
			}
			select {
			case <-held:
			case err := <-winner:
				t.Fatalf("winner did not reach owner barrier: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var winnerPID int
			select {
			case winnerPID = <-winnerBackend:
			case <-ctx.Done():
				t.Fatal("winner backend identity was not captured:", ctx.Err())
			}
			loserCtx := context.WithValue(ctx, backendKey{}, loserBackend)
			if laneFirst {
				workers.Go(func() { _, _, err := reserveOrdinaryOwnerFixture(loserCtx, auth, 40, &applied, nil); loser <- err })
			} else {
				workers.Go(func() { _, err := reserveCanonicalLaneFixture(loserCtx, DB, id, auth.RequestID, 40, nil); loser <- err })
			}
			var loserPID int
			select {
			case loserPID = <-loserBackend:
			case err := <-loser:
				t.Fatalf("loser did not reach owner barrier: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.Eventually(t, func() bool {
				var blocked bool
				err := DB.WithContext(ctx).Raw("SELECT ? = ANY (pg_blocking_pids(?))", winnerPID, loserPID).Scan(&blocked).Error
				return err == nil && blocked
			}, 2*time.Second, 10*time.Millisecond, "second claim must actually wait on the first transaction")
			close(release)
			require.NoError(t, <-winner)
			loserErr := <-loser
			owner, laneRows := readOwnerContractClaims(t, auth.RequestID)
			after := before
			if laneFirst {
				code, class := BillingRefundErrorCode(loserErr)
				assert.Equal(t, "identity_mismatch", code)
				assert.Equal(t, billingRefundReview, class)
				assert.Zero(t, applied.Load(), "ordinary debit callback must never run")
				assert.Equal(t, contractLaneState, owner.State)
				assert.EqualValues(t, 1, laneRows)
				after.lanePaid -= 40
				after.laneToken -= 40
				after.laneHeld += 40
				after.laneOpen++
			} else {
				assert.ErrorIs(t, loserErr, errContractOwnerConflict)
				assert.EqualValues(t, 1, applied.Load())
				assert.Equal(t, BillingHeld, owner.State)
				assert.Zero(t, laneRows)
				after.paid -= 40
				after.remain -= 40
				after.used += 40
				after.ledger++
				after.operations++
				_, err := MarkEnterpriseLaneDispatched(ctx, DB, id, auth.RequestID)
				assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
				_, err = RefundEnterpriseLane(ctx, DB, id, auth.RequestID)
				assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
			}
			require.Error(t, loserErr)
			assert.NotContains(t, loserErr.Error(), id.TokenFingerprint)
			assert.Equal(t, after, readOwnerContractBalances(t, id), "only the winning financial owner may change accounting")
		})
	}
}

func TestEnterpriseLaneOwnerIsolationRecoveryPostgres(t *testing.T) {
	id := ownerContractFixture(t)
	auth := ownerContractOrdinaryIdentity(id, id.LaneID+"-lost-ack")
	ctx := t.Context()
	before := readOwnerContractBalances(t, id)
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	lost := DB.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = loseQuotaCommitAck{sqlDB}
	_, err = reserveCanonicalLaneFixture(ctx, lost, id, auth.RequestID, 50, nil)
	require.ErrorContains(t, err, "synthetic lost acknowledgment after PostgreSQL committed")

	// A new session uses durable rows only, simulating restart without a
	// previous process's local result. Same-owner replay exposes the exact hold.
	reopened, err := gorm.Open(postgres.New(postgres.Config{DSN: os.Getenv("TEST_MODEL_POSTGRES_DSN"), PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	reopenedSQL, err := reopened.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopenedSQL.Close()) })
	request, err := reserveCanonicalLaneFixture(ctx, reopened, id, auth.RequestID, 50, nil)
	require.NoError(t, err)
	assert.Equal(t, 50, request.Held)
	_, err = reserveCanonicalLaneFixture(ctx, reopened, id, auth.RequestID, 51, nil)
	assert.ErrorIs(t, err, errContractOwnerEvidence)
	other := id
	other.Epoch++
	_, err = reserveCanonicalLaneFixture(ctx, reopened, other, auth.RequestID, 50, nil)
	assert.ErrorIs(t, err, errContractOwnerEvidence)
	owner, laneRows := readOwnerContractClaims(t, auth.RequestID)
	assert.Equal(t, contractLaneState, owner.State)
	assert.EqualValues(t, 1, laneRows)
	assert.Zero(t, owner.SourceHeld)
	assert.Zero(t, owner.TokenHeld)

	ordinaryApply := func(*gorm.DB) error { return errors.New("wrong worker callback reached") }
	_, err = ReadBillingReservation(ctx, auth)
	assert.Error(t, err)
	_, err = ProbeBillingFinalization(ctx, auth)
	assert.Error(t, err)
	_, err = RequestBillingRefund(ctx, auth)
	assert.Error(t, err)
	applied, err := ApplyBillingRefund(ctx, auth.RequestID)
	assert.Error(t, err)
	assert.False(t, applied)
	applied, err = SettleBillingFinalization(ctx, auth, 1, ordinaryApply)
	assert.Error(t, err)
	assert.False(t, applied)
	assert.NotContains(t, err.Error(), "wrong worker callback")
	assert.Error(t, MarkBillingDeliveryUnknown(ctx, auth))
	assert.Error(t, RecordInitialReservationRefundFailure(ctx, auth))
	require.NoError(t, RecordBillingRefundFailure(ctx, auth.RequestID, errors.New("synthetic wrong worker failure")))
	claim, err := ClaimBillingSettlementRecovery(ctx, auth.RequestID)
	require.NoError(t, err)
	assert.Nil(t, claim)
	for _, scan := range []func(context.Context, int) ([]BillingFinalization, error){GetReadyBillingRefunds, GetReadyBillingSettlements, GetReadyBillingSettlementLogs} {
		rows, err := scan(ctx, 100)
		require.NoError(t, err)
		for _, row := range rows {
			assert.NotEqual(t, auth.RequestID, row.RequestID)
		}
	}
	ownerAfter, _ := readOwnerContractClaims(t, auth.RequestID)
	assert.Equal(t, owner, ownerAfter, "foreign workers may not change marker or retry metadata")
	before.lanePaid -= 50
	before.laneToken -= 50
	before.laneHeld += 50
	before.laneOpen++
	assert.Equal(t, before, readOwnerContractBalances(t, id))

	// Critical blocker: even with a real held amount in the lane table, the
	// existing ordinary report does not surface the marker as a liability.
	// This assertion documents a reason NOT to install the bridge yet.
	var markerOffset int64
	require.NoError(t, reopened.WithContext(ctx).Model(&BillingFinalization{}).Where("request_id < ?", auth.RequestID).Count(&markerOffset).Error)
	page, err := ReadFinancialReviewReport(ctx, reopened, FinancialReviewReportOptions{
		Entity: FinancialReviewBilling, Offset: int(markerOffset), Limit: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, "available", page.Schema.Status)
	assert.Equal(t, 1, page.Scanned, "the report must actually scan the synthetic marker")
	assert.Empty(t, page.Items, "the marker's outstanding liability is not surfaced")
	payload, err := common.Marshal(page)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), id.TokenFingerprint)

	_, err = MarkEnterpriseLaneDispatched(ctx, lost, id, auth.RequestID)
	require.ErrorContains(t, err, "synthetic lost acknowledgment")
	_, err = MarkEnterpriseLaneDispatched(ctx, reopened, id, auth.RequestID)
	assert.ErrorIs(t, err, ErrEnterpriseLaneState)
	_, err = RefundEnterpriseLane(ctx, reopened, id, auth.RequestID)
	assert.ErrorIs(t, err, ErrEnterpriseLaneState)
	assert.Equal(t, before, readOwnerContractBalances(t, id), "unknown dispatch grants no replay or refund authority")
}

type ownerContractNonPostgresDialect struct{ gorm.Dialector }

func (ownerContractNonPostgresDialect) Name() string { return "mysql" }

func TestEnterpriseLaneOwnerIsolationRollbackAndForeignHistoryPostgres(t *testing.T) {
	id := ownerContractFixture(t)
	ctx := t.Context()
	before := readOwnerContractBalances(t, id)
	rid := id.LaneID + "-rollback"
	_, err := reserveCanonicalLaneFixture(ctx, DB, id, rid, 301, nil)
	assert.ErrorIs(t, err, ErrEnterpriseLaneCapacity)
	var owners, requests int64
	require.NoError(t, DB.Model(&BillingFinalization{}).Where("request_id = ?", rid).Count(&owners).Error)
	require.NoError(t, DB.Model(&EnterpriseLaneRequest{}).Where("request_id = ?", rid).Count(&requests).Error)
	assert.Zero(t, owners)
	assert.Zero(t, requests)
	assert.Equal(t, before, readOwnerContractBalances(t, id))
	var applied atomic.Int32
	_, attempt, err := reserveOrdinaryOwnerFixture(ctx, ownerContractOrdinaryIdentity(id, rid), 40, &applied, nil)
	require.NoError(t, err)
	assert.True(t, attempt.Applied)

	legacy := id.LaneID + "-legacy"
	prior, err := ReserveEnterpriseLane(ctx, DB, id, legacy, 10)
	require.NoError(t, err)
	unchanged := readOwnerContractBalances(t, id)
	_, err = reserveCanonicalLaneFixture(ctx, DB, id, legacy, 10, nil)
	assert.ErrorIs(t, err, errContractOwnerEvidence)
	require.NoError(t, DB.Model(&BillingFinalization{}).Where("request_id = ?", legacy).Count(&owners).Error)
	assert.Zero(t, owners, "no ownership is backfilled over a foreign historical receipt")
	var retained EnterpriseLaneRequest
	require.NoError(t, DB.Where("request_id = ?", legacy).Take(&retained).Error)
	assert.Equal(t, prior, retained)
	assert.Equal(t, unchanged, readOwnerContractBalances(t, id))

	orphan := id.LaneID + "-orphan"
	marker := contractLaneMarker(id, orphan)
	require.NoError(t, DB.Create(&marker).Error)
	_, err = reserveCanonicalLaneFixture(ctx, DB, id, orphan, 10, nil)
	assert.ErrorIs(t, err, errContractOwnerEvidence)
	owner, laneRows := readOwnerContractClaims(t, orphan)
	assert.Equal(t, marker, owner)
	assert.Zero(t, laneRows)
	assert.Equal(t, unchanged, readOwnerContractBalances(t, id))

	// A non-PG runtime selection is rejected before any SQL, even when the
	// explicit handle still points to our only database, a disposable PG.
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeMySQL, oldLog)
	_, err = reserveCanonicalLaneFixture(ctx, DB, id, id.LaneID+"-disabled", 10, nil)
	common.SetDatabaseTypes(oldMain, oldLog)
	assert.ErrorIs(t, err, ErrEnterpriseLaneUnavailable)
	mismatchedHandle := DB.Session(&gorm.Session{NewDB: true, Context: ctx})
	config := *mismatchedHandle.Config
	config.Dialector = ownerContractNonPostgresDialect{DB.Dialector}
	mismatchedHandle.Config = &config
	_, err = reserveCanonicalLaneFixture(ctx, mismatchedHandle, id, id.LaneID+"-other-dialect", 10, nil)
	assert.ErrorIs(t, err, ErrEnterpriseLaneUnavailable)
	assert.Equal(t, unchanged, readOwnerContractBalances(t, id))
}
