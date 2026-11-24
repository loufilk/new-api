//go:build lane_billing_integration_dev

package model

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const (
	LaneBillingFiniteKeyActive  = "active"
	LaneBillingFiniteKeyPaused  = "paused"
	LaneBillingFiniteKeyRevoked = "revoked"
)

var (
	ErrLaneBillingFiniteKeyState    = errors.New("finite Key generation does not authorize this operation")
	ErrLaneBillingFiniteKeyIdentity = errors.New("original finite Key identity does not match")
	ErrLaneBillingFiniteKeyConflict = errors.New("finite Key transfer operation identity conflicts with persisted evidence")
)

// LaneBillingFiniteKeyIdentity identifies an original existing Key. The
// caller supplies an authenticated original fingerprint, never a bearer key.
type LaneBillingFiniteKeyIdentity struct {
	UserID           int
	TokenID          int
	Epoch            int64
	TokenFingerprint string `json:"-"`
}

func (id LaneBillingFiniteKeyIdentity) valid() bool {
	return id.UserID > 0 && id.TokenID > 0 && id.Epoch > 0 && laneChildSHAValid(id.TokenFingerprint)
}

// Enrollment is separate from the existing Stage 5 bucket. Once a generation
// is paused or revoked it cannot be reopened under the same epoch.
type laneBillingFiniteKeyEnrollmentV1 struct {
	UserID           int    `gorm:"primaryKey;autoIncrement:false"`
	TokenID          int    `gorm:"primaryKey;autoIncrement:false"`
	Epoch            int64  `gorm:"primaryKey;autoIncrement:false"`
	TokenFingerprint string `gorm:"type:varchar(64);not null" json:"-"`
	State            string `gorm:"type:varchar(8);not null;check:ck_lane_key_enrollment_state,state IN ('active','paused','revoked')"`
}

func (laneBillingFiniteKeyEnrollmentV1) TableName() string { return "lane_billing_key_enrollments_v1" }

func (e laneBillingFiniteKeyEnrollmentV1) identity() LaneBillingFiniteKeyIdentity {
	return LaneBillingFiniteKeyIdentity{UserID: e.UserID, TokenID: e.TokenID, Epoch: e.Epoch, TokenFingerprint: e.TokenFingerprint}
}

func (e laneBillingFiniteKeyEnrollmentV1) valid() bool {
	return e.identity().valid() && (e.State == LaneBillingFiniteKeyActive || e.State == LaneBillingFiniteKeyPaused || e.State == LaneBillingFiniteKeyRevoked)
}

// OperationID is unique in the dev store, including across sibling Keys.
type LaneBillingFiniteKeyTransfer struct {
	Identity    LaneBillingFiniteKeyIdentity
	OperationID string
	Slot        int
	Direction   string
	Amount      int
}

func (t LaneBillingFiniteKeyTransfer) valid() bool {
	return t.Identity.valid() && laneBillingOwnerIDValid(t.OperationID) && t.Slot >= 0 && t.Slot < SharedWalletMaxSlots &&
		(t.Direction == "grant" || t.Direction == "reclaim") && t.Amount > 0 && t.Amount <= common.MaxWalletQuota
}

// LaneBillingFiniteKeyTransferReceipt is exact historical evidence of a
// non-consumption move. A replay is not permission to reserve or dispatch.
type LaneBillingFiniteKeyTransferReceipt struct {
	Transfer      LaneBillingFiniteKeyTransfer
	RawBefore     int
	RawAfter      int
	ShareVersion  int64
	GrantedAfter  int
	ReturnedAfter int
}

type laneBillingFiniteKeyTransferV1 struct {
	UserID           int    `gorm:"not null;uniqueIndex:idx_lane_key_transfer_slot_version,priority:1"`
	TokenID          int    `gorm:"not null;uniqueIndex:idx_lane_key_transfer_slot_version,priority:2"`
	OperationID      string `gorm:"type:varchar(64);primaryKey"`
	Epoch            int64  `gorm:"not null;uniqueIndex:idx_lane_key_transfer_slot_version,priority:3"`
	Slot             int    `gorm:"not null;uniqueIndex:idx_lane_key_transfer_slot_version,priority:4;check:ck_lane_key_transfer_slot,slot >= 0 AND slot < 32"`
	ShareVersion     int64  `gorm:"not null;uniqueIndex:idx_lane_key_transfer_slot_version,priority:5"`
	TokenFingerprint string `gorm:"type:varchar(64);not null" json:"-"`
	Direction        string `gorm:"type:varchar(8);not null"`
	Amount           int    `gorm:"type:bigint;not null"`
	RawBefore        int    `gorm:"type:bigint;not null"`
	RawAfter         int    `gorm:"type:bigint;not null"`
	GrantedAfter     int    `gorm:"type:bigint;not null"`
	ReturnedAfter    int    `gorm:"type:bigint;not null"`
	Proof            string `gorm:"type:varchar(64);not null"`
}

func (laneBillingFiniteKeyTransferV1) TableName() string            { return "lane_billing_key_transfers_v1" }
func (*laneBillingFiniteKeyTransferV1) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (*laneBillingFiniteKeyTransferV1) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

func (r laneBillingFiniteKeyTransferV1) receipt() LaneBillingFiniteKeyTransferReceipt {
	return LaneBillingFiniteKeyTransferReceipt{
		Transfer: LaneBillingFiniteKeyTransfer{
			Identity:    LaneBillingFiniteKeyIdentity{UserID: r.UserID, TokenID: r.TokenID, Epoch: r.Epoch, TokenFingerprint: r.TokenFingerprint},
			OperationID: r.OperationID, Slot: r.Slot, Direction: r.Direction, Amount: r.Amount,
		},
		RawBefore: r.RawBefore, RawAfter: r.RawAfter, ShareVersion: r.ShareVersion, GrantedAfter: r.GrantedAfter, ReturnedAfter: r.ReturnedAfter,
	}
}

// This checksum binds exact arithmetic and identity, not external authority or
// an attestation against a database writer who can rewrite both records.
func (r laneBillingFiniteKeyTransferV1) checksum() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("finite-key-transfer-v1|%d|%d|%d|%s|%s|%d|%s|%d|%d|%d|%d|%d|%d",
		r.UserID, r.TokenID, r.Epoch, r.TokenFingerprint, r.OperationID, r.Slot, r.Direction, r.Amount,
		r.RawBefore, r.RawAfter, r.ShareVersion, r.GrantedAfter, r.ReturnedAfter)))
	return hex.EncodeToString(h[:])
}

func (r laneBillingFiniteKeyTransferV1) valid() bool {
	if !r.receipt().Transfer.valid() || r.ShareVersion < 1 || r.ShareVersion >= common.MaxWalletQuota ||
		r.GrantedAfter <= 0 || r.GrantedAfter > common.MaxWalletQuota || r.ReturnedAfter < 0 || r.ReturnedAfter > r.GrantedAfter ||
		r.RawBefore < 0 || r.RawBefore > common.MaxWalletQuota || r.RawAfter < 0 || r.RawAfter > common.MaxWalletQuota ||
		subtle.ConstantTimeCompare([]byte(r.Proof), []byte(r.checksum())) != 1 {
		return false
	}
	if r.Direction == "grant" {
		return r.RawBefore-r.RawAfter == r.Amount && r.GrantedAfter-r.ReturnedAfter >= r.Amount
	}
	return r.RawAfter-r.RawBefore == r.Amount && r.ReturnedAfter >= r.Amount
}

// Unlike the ordinary serving configuration, all of these paths are tagged
// and restricted to one explicitly named, isolated loopback dev schema. Do
// not let a missing synthetic table fall through to public or pg_temp.
func laneBillingFiniteKeyDevScopeTx(tx *gorm.DB) error {
	if common.RedisEnabled || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return ErrLaneBillingOwnerUnavailable
	}
	var scope struct {
		SchemaName string
		ServerIP   string
		Isolated   bool
	}
	err := tx.Raw(`SELECT current_schema() AS schema_name, host(inet_server_addr()) AS server_ip,
		current_schemas(false) = ARRAY[current_schema()]::name[] AND pg_my_temp_schema() = 0 AS isolated`).Scan(&scope).Error
	ip, parseErr := netip.ParseAddr(scope.ServerIP)
	if err != nil || parseErr != nil || !ip.IsLoopback() || !scope.Isolated ||
		!strings.HasPrefix(scope.SchemaName, "lane_billing_dev_") || !laneBillingOwnerIDValid(scope.SchemaName) {
		return ErrLaneBillingOwnerUnavailable
	}
	return nil
}

func withLaneBillingFiniteKeyDevTx(ctx context.Context, db *gorm.DB, apply func(*gorm.DB) error) error {
	if ctx == nil || db == nil || db.Config == nil || db.Dialector == nil || db.Statement == nil || db.DryRun ||
		db.Dialector.Name() != "postgres" || common.RedisEnabled || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return ErrLaneBillingOwnerUnavailable
	}
	_, nestedStatement := db.Statement.ConnPool.(gorm.TxCommitter)
	_, nestedConfig := db.Config.ConnPool.(gorm.TxCommitter)
	if nestedStatement || nestedConfig {
		return ErrLaneBillingOwnerUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	prepared := false
	err := db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		if err := laneBillingFiniteKeyDevScopeTx(tx); err != nil {
			return err
		}
		if err := apply(tx); err != nil {
			return err
		}
		prepared = true
		return nil
	})
	if err == nil {
		return nil
	}
	if prepared {
		return ErrLaneBillingOwnerCommitUncertain
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case errors.Is(err, ErrLaneBillingOwnerUnavailable), errors.Is(err, ErrLaneBillingOwnerEvidence),
		errors.Is(err, ErrLaneBillingFiniteKeyState), errors.Is(err, ErrLaneBillingFiniteKeyIdentity),
		errors.Is(err, ErrLaneBillingFiniteKeyConflict), errors.Is(err, ErrBillingTokenInsufficient):
		return err
	default:
		return ErrLaneBillingOwnerStore
	}
}

// InstallLaneBillingFiniteKeyDevSchema prepares enrollment, immutable
// operation evidence, and an explicit custody flag on existing child rows.
// Existing fixture rows remain unverified; buckets belong to the child store.
func InstallLaneBillingFiniteKeyDevSchema(ctx context.Context, db *gorm.DB) error {
	return withLaneBillingFiniteKeyDevTx(ctx, db, func(tx *gorm.DB) error {
		for _, m := range []any{&User{}, &Token{}, &laneBillingFiniteKeyShareV1{}, &laneBillingChildReceiptV1{}} {
			if !tx.Migrator().HasTable(m) {
				return ErrLaneBillingOwnerUnavailable
			}
		}
		if err := tx.AutoMigrate(&laneBillingFiniteKeyEnrollmentV1{}, &laneBillingFiniteKeyTransferV1{}); err != nil {
			return err
		}
		if err := tx.Exec(`ALTER TABLE lane_billing_child_receipts_v1
			ADD COLUMN IF NOT EXISTS key_enrollment_verified boolean NOT NULL DEFAULT false`).Error; err != nil {
			return err
		}
		return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_lane_billing_child_key_epoch_v1
			ON lane_billing_child_receipts_v1 (user_id, token_id, key_epoch)`).Error
	})
}

func lockLaneBillingFiniteKeyUserTx(tx *gorm.DB, id LaneBillingFiniteKeyIdentity, strength string) (User, error) {
	var user User
	err := tx.Session(&gorm.Session{NewDB: true}).Unscoped().Clauses(clause.Locking{Strength: strength}).
		Select("id", "status", "deleted_at").Where("id = ?", id.UserID).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return user, ErrLaneBillingFiniteKeyIdentity
	}
	return user, err
}

func lockLaneBillingFiniteKeyTokenTx(tx *gorm.DB, id LaneBillingFiniteKeyIdentity, strength string) (Token, error) {
	var token Token
	err := tx.Session(&gorm.Session{NewDB: true}).Unscoped().Clauses(clause.Locking{Strength: strength}).
		Select("id", "user_id", "key", "status", "deleted_at", "expired_time", "unlimited_quota", "remain_quota", "cache_revision").
		Where("id = ?", id.TokenID).Take(&token).Error
	return token, err
}

func originalLaneBillingFiniteKey(id LaneBillingFiniteKeyIdentity, token Token) bool {
	return token.Id == id.TokenID && token.UserId == id.UserID && !token.UnlimitedQuota && token.Key != "" &&
		subtle.ConstantTimeCompare([]byte(BillingTokenFingerprint(token.Key)), []byte(id.TokenFingerprint)) == 1
}

func liveLaneBillingFiniteKey(user User, token Token) bool {
	return !user.DeletedAt.Valid && user.Status == common.UserStatusEnabled && !token.DeletedAt.Valid &&
		token.Status == common.TokenStatusEnabled && (token.ExpiredTime == -1 || token.ExpiredTime > common.GetTimestamp())
}

func lockLaneBillingKeyEnrollmentTx(tx *gorm.DB, id LaneBillingFiniteKeyIdentity, strength string) (laneBillingFiniteKeyEnrollmentV1, error) {
	var e laneBillingFiniteKeyEnrollmentV1
	err := tx.Clauses(clause.Locking{Strength: strength}).Where("user_id = ? AND token_id = ? AND epoch = ?", id.UserID, id.TokenID, id.Epoch).Take(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return e, ErrLaneBillingFiniteKeyState
	}
	if err != nil {
		return e, err
	}
	if !e.valid() || e.identity() != id {
		return e, ErrLaneBillingFiniteKeyIdentity
	}
	return e, nil
}

func latestLaneBillingKeyTransferTx(tx *gorm.DB, id LaneBillingFiniteKeyIdentity, slot int) (laneBillingFiniteKeyTransferV1, error) {
	var last laneBillingFiniteKeyTransferV1
	err := tx.Where("user_id = ? AND token_id = ? AND epoch = ? AND slot = ?", id.UserID, id.TokenID, id.Epoch, slot).
		Order("share_version DESC").Take(&last).Error
	return last, err
}

func laneBillingKeySlotTransferMatches(id LaneBillingFiniteKeyIdentity, row laneBillingFiniteKeyShareV1, last laneBillingFiniteKeyTransferV1) bool {
	return row.valid() && row.UserID == id.UserID && row.TokenID == id.TokenID && row.Epoch == id.Epoch && row.TokenFingerprint == id.TokenFingerprint &&
		last.valid() && last.receipt().Transfer.Identity == id && last.Slot == row.Slot && last.ShareVersion <= row.Version &&
		last.GrantedAfter == row.Granted && last.ReturnedAfter == row.Returned
}

// A finished first-generation without spending can be rolled safely in this
// slice. Real spent-parent projection is separate: no inferred zero or credit.
// Historical request and transfer rows remain untouched.
func checkLaneBillingKeyOldEpochTx(tx *gorm.DB, e laneBillingFiniteKeyEnrollmentV1) error {
	if !e.valid() || e.State != LaneBillingFiniteKeyRevoked {
		return ErrLaneBillingFiniteKeyState
	}
	id := e.identity()
	var rows []laneBillingFiniteKeyShareV1
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND token_id = ? AND epoch = ?", id.UserID, id.TokenID, id.Epoch).
		Order("slot").Limit(SharedWalletMaxSlots + 1).Find(&rows).Error; err != nil {
		return err
	}
	var slots [SharedWalletMaxSlots]*laneBillingFiniteKeyShareV1
	for i := range rows {
		row := &rows[i]
		if !row.valid() || slots[row.Slot] != nil || row.Available != 0 || row.Held != 0 || row.Spent != 0 || row.Granted != row.Returned {
			return ErrLaneBillingFiniteKeyState
		}
		slots[row.Slot] = row
	}
	// Exact indexed tip probes, never a scan of the old request/transfer history.
	for slot, row := range slots {
		last, err := latestLaneBillingKeyTransferTx(tx, id, slot)
		if row == nil && errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if row == nil || err != nil || !laneBillingKeySlotTransferMatches(id, *row, last) {
			return ErrLaneBillingOwnerEvidence
		}
	}
	return nil
}

// EnrollLaneBillingFiniteKeyDev records one live original finite Key, without
// allocating funds. A higher epoch requires the prior one to be irrevocably
// closed and fully returned. This doesn't authorize a wallet owner or provider.
func EnrollLaneBillingFiniteKeyDev(ctx context.Context, db *gorm.DB, id LaneBillingFiniteKeyIdentity) error {
	if !id.valid() {
		return ErrLaneBillingFiniteKeyIdentity
	}
	return withLaneBillingFiniteKeyDevTx(ctx, db, func(tx *gorm.DB) error {
		user, err := lockLaneBillingFiniteKeyUserTx(tx, id, "UPDATE")
		if err != nil {
			return err
		}
		token, err := lockLaneBillingFiniteKeyTokenTx(tx, id, "UPDATE")
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrLaneBillingFiniteKeyIdentity
		}
		if err != nil {
			return err
		}
		if !originalLaneBillingFiniteKey(id, token) || !liveLaneBillingFiniteKey(user, token) {
			return ErrLaneBillingFiniteKeyIdentity
		}
		var prior laneBillingFiniteKeyEnrollmentV1
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND token_id = ?", id.UserID, id.TokenID).
			Order("epoch DESC").Take(&prior).Error
		initial := errors.Is(err, gorm.ErrRecordNotFound)
		if err == nil {
			if !prior.valid() || prior.Epoch > id.Epoch {
				return ErrLaneBillingFiniteKeyState
			}
			if prior.Epoch == id.Epoch {
				if prior.identity() != id {
					return ErrLaneBillingFiniteKeyIdentity
				}
				if prior.State != LaneBillingFiniteKeyActive {
					return ErrLaneBillingFiniteKeyState
				}
				return nil
			}
			if err := checkLaneBillingKeyOldEpochTx(tx, prior); err != nil {
				return err
			}
		} else if !initial {
			return err
		}
		// Never adopt preseeded Stage 5 slots or operation-only history.
		// Without an enrollment, even another epoch's evidence is foreign.
		for _, row := range []any{&laneBillingFiniteKeyShareV1{}, &laneBillingFiniteKeyTransferV1{}} {
			query := tx.Where("user_id = ? AND token_id = ?", id.UserID, id.TokenID)
			if !initial {
				query = query.Where("epoch = ?", id.Epoch)
			}
			if err := query.Take(row).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
				if err != nil {
					return err
				}
				return ErrLaneBillingOwnerEvidence
			}
		}
		var child laneBillingChildReceiptV1
		childQuery := tx.Where("user_id = ? AND token_id = ?", id.UserID, id.TokenID)
		if !initial {
			childQuery = childQuery.Where("key_epoch = ?", id.Epoch)
		}
		if err := childQuery.Select("request_id").Take(&child).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			if err != nil {
				return err
			}
			return ErrLaneBillingOwnerEvidence
		}
		return tx.Create(&laneBillingFiniteKeyEnrollmentV1{
			UserID: id.UserID, TokenID: id.TokenID, Epoch: id.Epoch, TokenFingerprint: id.TokenFingerprint, State: LaneBillingFiniteKeyActive,
		}).Error
	})
}

// ChangeLaneBillingFiniteKeyDevState is one-way. It closes new reserve and
// dispatch claims, but never clears a hold or alters an already-claimed owner.
// This does not promise no outbound I/O after a previously committed claim.
// It can close an old fingerprint even after the Key has been revoked/replaced.
func ChangeLaneBillingFiniteKeyDevState(ctx context.Context, db *gorm.DB, id LaneBillingFiniteKeyIdentity, state string) error {
	if !id.valid() {
		return ErrLaneBillingFiniteKeyIdentity
	}
	if state != LaneBillingFiniteKeyPaused && state != LaneBillingFiniteKeyRevoked {
		return ErrLaneBillingFiniteKeyState
	}
	return withLaneBillingFiniteKeyDevTx(ctx, db, func(tx *gorm.DB) error {
		if _, err := lockLaneBillingFiniteKeyUserTx(tx, id, "UPDATE"); err != nil {
			return err
		}
		e, err := lockLaneBillingKeyEnrollmentTx(tx, id, "UPDATE")
		if err != nil {
			return err
		}
		if e.State == state {
			return nil
		}
		if e.State == LaneBillingFiniteKeyRevoked {
			return ErrLaneBillingFiniteKeyState
		}
		result := tx.Model(&laneBillingFiniteKeyEnrollmentV1{}).
			Where("user_id = ? AND token_id = ? AND epoch = ? AND state = ?", id.UserID, id.TokenID, id.Epoch, e.State).
			UpdateColumn("state", state)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLaneBillingOwnerEvidence
		}
		return nil
	})
}

func laneBillingKeyUnspentBoundsTx(tx *gorm.DB, id LaneBillingFiniteKeyIdentity, raw int) error {
	if raw < 0 || raw > common.MaxWalletQuota {
		return ErrLaneBillingOwnerEvidence
	}
	var slots []laneBillingFiniteKeyShareV1
	if err := tx.Where("user_id = ? AND token_id = ? AND epoch = ?", id.UserID, id.TokenID, id.Epoch).
		Limit(SharedWalletMaxSlots + 1).Find(&slots).Error; err != nil {
		return err
	}
	if len(slots) > SharedWalletMaxSlots {
		return ErrLaneBillingOwnerEvidence
	}
	left := common.MaxWalletQuota - raw
	for _, s := range slots {
		if !s.valid() || s.TokenFingerprint != id.TokenFingerprint || s.Available+s.Held > left {
			return ErrLaneBillingOwnerEvidence
		}
		left -= s.Available + s.Held
	}
	return nil
}

// ApplyLaneBillingFiniteKeyDevTransfer moves raw remaining quota to/from the
// original bucket, never UsedQuota or the wallet. Owns a top-level transaction:
// original user -> exact Key (including soft-deleted) -> enrollment -> slot.
// Nothing is payable from the returned receipt after an uncertain COMMIT.
func ApplyLaneBillingFiniteKeyDevTransfer(ctx context.Context, db *gorm.DB, op LaneBillingFiniteKeyTransfer) (LaneBillingFiniteKeyTransferReceipt, bool, error) {
	var out LaneBillingFiniteKeyTransferReceipt
	if !op.valid() {
		return out, false, ErrLaneBillingFiniteKeyIdentity
	}
	replay := false
	err := withLaneBillingFiniteKeyDevTx(ctx, db, func(tx *gorm.DB) error {
		id := op.Identity
		user, err := lockLaneBillingFiniteKeyUserTx(tx, id, "UPDATE")
		if err != nil {
			return err
		}
		token, tokenErr := lockLaneBillingFiniteKeyTokenTx(tx, id, "UPDATE")
		if tokenErr != nil && !errors.Is(tokenErr, gorm.ErrRecordNotFound) {
			return tokenErr
		}
		var recorded laneBillingFiniteKeyTransferV1
		err = tx.Where("operation_id = ?", op.OperationID).Take(&recorded).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		existing := err == nil
		if existing && (!recorded.valid() || recorded.receipt().Transfer != op) {
			return ErrLaneBillingFiniteKeyConflict
		}
		e, err := lockLaneBillingKeyEnrollmentTx(tx, id, "UPDATE")
		if err != nil {
			return err
		}
		if !existing {
			if tokenErr != nil || !originalLaneBillingFiniteKey(id, token) {
				return ErrLaneBillingFiniteKeyIdentity
			}
			if op.Direction == "grant" && (e.State != LaneBillingFiniteKeyActive || !liveLaneBillingFiniteKey(user, token)) ||
				op.Direction == "reclaim" && e.State == LaneBillingFiniteKeyActive {
				return ErrLaneBillingFiniteKeyState
			}
			if token.CacheRevision < 1 || token.CacheRevision >= maxPermissionRevision {
				return ErrLaneBillingOwnerEvidence
			}
			if err := laneBillingKeyUnspentBoundsTx(tx, id, token.RemainQuota); err != nil {
				return err
			}
		}
		var row laneBillingFiniteKeyShareV1
		const shareWhere = "user_id = ? AND token_id = ? AND epoch = ? AND slot = ?"
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(shareWhere, id.UserID, id.TokenID, id.Epoch, op.Slot).Take(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		hasShare := err == nil
		last, err := latestLaneBillingKeyTransferTx(tx, id, op.Slot)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if hasShare != (err == nil) || hasShare && !laneBillingKeySlotTransferMatches(id, row, last) {
			return ErrLaneBillingOwnerEvidence
		}
		if existing {
			if !hasShare || recorded.ShareVersion > last.ShareVersion ||
				recorded.GrantedAfter > last.GrantedAfter || recorded.ReturnedAfter > last.ReturnedAfter {
				return ErrLaneBillingOwnerEvidence
			}
			out, replay = recorded.receipt(), true
			return nil
		}
		next := row
		if !hasShare {
			next = laneBillingFiniteKeyShareV1{
				UserID: id.UserID, TokenID: id.TokenID, Epoch: id.Epoch, Slot: op.Slot, TokenFingerprint: id.TokenFingerprint,
			}
		}
		raw := token.RemainQuota
		if op.Direction == "grant" {
			if raw < op.Amount {
				return ErrBillingTokenInsufficient
			}
			if next.Granted > common.MaxWalletQuota-op.Amount {
				return ErrLaneBillingOwnerEvidence
			}
			raw -= op.Amount
			next.Granted += op.Amount
			next.Available += op.Amount
		} else {
			if !hasShare || op.Amount > next.Available {
				return ErrBillingTokenInsufficient
			}
			if raw > common.MaxWalletQuota-op.Amount {
				return ErrLaneBillingOwnerEvidence
			}
			raw += op.Amount
			next.Available -= op.Amount
			next.Returned += op.Amount
		}
		next.Version++
		if !next.valid() {
			return ErrLaneBillingOwnerEvidence
		}
		result := tx.Unscoped().Model(&Token{}).
			Where("id = ? AND user_id = ? AND cache_revision = ?", id.TokenID, id.UserID, token.CacheRevision).
			UpdateColumns(map[string]any{"remain_quota": raw, "cache_revision": token.CacheRevision + 1})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLaneBillingOwnerEvidence
		}
		if !hasShare {
			if err := tx.Create(&next).Error; err != nil {
				return err
			}
		} else {
			result = tx.Model(&laneBillingFiniteKeyShareV1{}).Where(shareWhere+" AND version = ?", id.UserID, id.TokenID, id.Epoch, op.Slot, row.Version).
				UpdateColumns(map[string]any{"granted": next.Granted, "available": next.Available, "returned": next.Returned, "version": next.Version})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrLaneBillingOwnerEvidence
			}
		}
		recorded = laneBillingFiniteKeyTransferV1{
			UserID: id.UserID, TokenID: id.TokenID, Epoch: id.Epoch, TokenFingerprint: id.TokenFingerprint,
			OperationID: op.OperationID, Slot: op.Slot, Direction: op.Direction, Amount: op.Amount,
			RawBefore: token.RemainQuota, RawAfter: raw, ShareVersion: next.Version, GrantedAfter: next.Granted, ReturnedAfter: next.Returned,
		}
		recorded.Proof = recorded.checksum()
		if !recorded.valid() {
			return ErrLaneBillingOwnerEvidence
		}
		if err := tx.Create(&recorded).Error; err != nil {
			return err
		}
		out = recorded.receipt()
		return nil
	})
	if err != nil {
		return LaneBillingFiniteKeyTransferReceipt{}, false, err
	}
	return out, replay, nil
}

func authorizeLaneBillingEnrolledKeyTx(tx *gorm.DB, b laneBillingChildBinding) error {
	if err := laneBillingFiniteKeyDevScopeTx(tx); err != nil {
		return err
	}
	id := LaneBillingFiniteKeyIdentity{UserID: b.Owner.UserID, TokenID: b.Owner.TokenID, Epoch: b.KeyEpoch, TokenFingerprint: b.Owner.TokenFingerprint}
	user, err := lockLaneBillingFiniteKeyUserTx(tx, id, "SHARE")
	if err != nil {
		return err
	}
	token, err := lockLaneBillingFiniteKeyTokenTx(tx, id, "SHARE")
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrLaneBillingFiniteKeyIdentity
	}
	if err != nil {
		return err
	}
	if !originalLaneBillingFiniteKey(id, token) || !liveLaneBillingFiniteKey(user, token) {
		return ErrLaneBillingFiniteKeyIdentity
	}
	e, err := lockLaneBillingKeyEnrollmentTx(tx, id, "SHARE")
	if err != nil {
		return err
	}
	if e.State != LaneBillingFiniteKeyActive {
		return ErrLaneBillingFiniteKeyState
	}
	// A preseeded slot cannot become an enrolled slot. All cold mutations
	// acquire the original user exclusively before touching the slot/tip.
	var share laneBillingFiniteKeyShareV1
	if err := tx.Where("user_id = ? AND token_id = ? AND epoch = ? AND slot = ?", id.UserID, id.TokenID, id.Epoch, b.KeySlot).Take(&share).Error; err != nil {
		return err
	}
	last, err := latestLaneBillingKeyTransferTx(tx, id, b.KeySlot)
	if err != nil {
		return err
	}
	if !laneBillingKeySlotTransferMatches(id, share, last) {
		return ErrLaneBillingOwnerEvidence
	}
	return nil
}

// Unverified children belong to the original seed-only fixture contract.
// A matching active slot does not turn one into an enrolled owner.
func requireLaneBillingEnrolledChildProofTx(tx *gorm.DB, requestID string) error {
	var p struct{ Verified bool }
	result := tx.Raw(`SELECT key_enrollment_verified AS verified
		FROM lane_billing_child_receipts_v1 WHERE request_id = ?`, requestID).Scan(&p)
	if result.Error != nil || result.RowsAffected != 1 || !p.Verified {
		return ErrLaneBillingOwnerEvidence
	}
	return nil
}

type laneBillingEnrolledPGChildStore struct{ laneBillingPGChildStore }

func (s *laneBillingEnrolledPGChildStore) LockLaneBillingReceiptTx(tx *gorm.DB, requestID string) (LaneBillingOwnerReceipt, error) {
	receipt, err := s.laneBillingPGChildStore.LockLaneBillingReceiptTx(tx, requestID)
	if err != nil {
		return LaneBillingOwnerReceipt{}, err
	}
	if err := requireLaneBillingEnrolledChildProofTx(tx, requestID); err != nil {
		return LaneBillingOwnerReceipt{}, err
	}
	return receipt, nil
}

func (s *laneBillingEnrolledPGChildStore) ReserveLaneBillingReceiptTx(tx *gorm.DB, id LaneBillingOwnerIdentity, target int) (bool, error) {
	if tx == nil || tx.Statement == nil || tx.Statement.Context == nil || !s.binding.valid() || s.binding.Owner != id {
		return false, ErrLaneBillingOwnerEvidence
	}
	claim, fresh := tx.Statement.Context.Value(laneBillingFreshClaimContextKey{}).(*laneBillingFreshClaim)
	if !fresh || claim == nil || claim.tx != tx || claim.identity != id || claim.target != target {
		return false, ErrLaneBillingOwnerEvidence
	}
	if err := authorizeLaneBillingEnrolledKeyTx(tx, s.binding); err != nil {
		return false, ErrLaneBillingOwnerEvidence
	}
	fresh, err := s.laneBillingPGChildStore.ReserveLaneBillingReceiptTx(tx, id, target)
	if err != nil || !fresh {
		return fresh, err
	}
	result := tx.Exec(`UPDATE lane_billing_child_receipts_v1 SET key_enrollment_verified = true
		WHERE request_id = ? AND NOT key_enrollment_verified`, id.RequestID)
	if result.Error != nil || result.RowsAffected != 1 {
		return false, ErrLaneBillingOwnerEvidence
	}
	return true, nil
}

func claimEnrolledLaneBillingChildDispatch(ctx context.Context, db *gorm.DB, b laneBillingChildBinding, verifiedWireSHA256 string) error {
	if subtle.ConstantTimeCompare([]byte(b.FinalWireSHA256), []byte(verifiedWireSHA256)) != 1 {
		return ErrLaneBillingOwnerEvidence
	}
	return transitionLaneBillingChild(ctx, db, b, func(tx *gorm.DB, r *laneBillingChildReceiptV1) error {
		if r.State != LaneBillingReceiptHeld {
			return errLaneBillingChildState
		}
		if err := requireLaneBillingEnrolledChildProofTx(tx, b.Owner.RequestID); err != nil {
			return err
		}
		if err := authorizeLaneBillingEnrolledKeyTx(tx, b); err != nil {
			return ErrLaneBillingOwnerEvidence
		}
		return saveLaneBillingChildState(tx, r, laneBillingChildDispatch, 0, "")
	})
}

var _ LaneBillingOwnerStore = (*laneBillingEnrolledPGChildStore)(nil)
