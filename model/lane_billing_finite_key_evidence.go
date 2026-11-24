package model

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// The read-only evidence contracts are independent of fixture-only writes.
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

func originalLaneBillingFiniteKey(id LaneBillingFiniteKeyIdentity, token Token) bool {
	return token.Id == id.TokenID && token.UserId == id.UserID && !token.UnlimitedQuota && token.Key != "" &&
		subtle.ConstantTimeCompare([]byte(BillingTokenFingerprint(token.Key)), []byte(id.TokenFingerprint)) == 1
}

func liveLaneBillingFiniteKey(user User, token Token) bool {
	return !user.DeletedAt.Valid && user.Status == common.UserStatusEnabled && !token.DeletedAt.Valid &&
		token.Status == common.TokenStatusEnabled && (token.ExpiredTime == -1 || token.ExpiredTime > common.GetTimestamp())
}

func laneBillingKeySlotTransferMatches(id LaneBillingFiniteKeyIdentity, row laneBillingFiniteKeyShareV1, last laneBillingFiniteKeyTransferV1) bool {
	return row.valid() && row.UserID == id.UserID && row.TokenID == id.TokenID && row.Epoch == id.Epoch && row.TokenFingerprint == id.TokenFingerprint &&
		last.valid() && last.receipt().Transfer.Identity == id && last.Slot == row.Slot && last.ShareVersion <= row.Version &&
		last.GrantedAfter == row.Granted && last.ReturnedAfter == row.Returned
}

type laneFiniteKeyCustody struct {
	slots             []laneBillingFiniteKeyShareV1
	idle, held, spent int
}

// The caller verifies the current enrollment and binds this read to the
// caller's read-only snapshot or existing reservation transaction.
func readLaneFiniteKeyCustodyTx(tx *gorm.DB, id LaneBillingFiniteKeyIdentity, raw int, lock bool) (laneFiniteKeyCustody, error) {
	var s laneFiniteKeyCustody
	query := tx.Session(&gorm.Session{NewDB: true})
	if lock {
		query = lockForUpdate(query)
	}
	if raw < 0 || raw > common.MaxWalletQuota {
		return s, ErrLaneBillingOwnerEvidence
	}
	if err := query.Where("user_id = ? AND token_id = ? AND epoch = ?", id.UserID, id.TokenID, id.Epoch).
		Order("slot").Limit(SharedWalletMaxSlots + 1).Find(&s.slots).Error; err != nil {
		return s, err
	}
	if len(s.slots) > SharedWalletMaxSlots {
		return s, ErrLaneBillingOwnerEvidence
	}
	// Bounded indexed tip probes include empty slots, so an operation-only
	// claim cannot be mistaken for an untouched Key. Never scan old history.
	var tips []laneBillingFiniteKeyTransferV1
	if err := tx.Raw(`SELECT tip.* FROM generate_series(0, ?) AS slots(slot)
		CROSS JOIN LATERAL (
			SELECT * FROM lane_billing_key_transfers_v1
			WHERE user_id = ? AND token_id = ? AND epoch = ? AND slot = slots.slot
			ORDER BY share_version DESC LIMIT 1
		) AS tip`, SharedWalletMaxSlots-1, id.UserID, id.TokenID, id.Epoch).Scan(&tips).Error; err != nil {
		return s, err
	}
	if len(tips) != len(s.slots) {
		return s, ErrLaneBillingOwnerEvidence
	}
	tipBySlot := make(map[int]laneBillingFiniteKeyTransferV1, len(tips))
	for _, tip := range tips {
		tipBySlot[tip.Slot] = tip
	}
	keyUnspentBound := common.MaxWalletQuota - raw
	for _, slot := range s.slots {
		if !laneBillingKeySlotTransferMatches(id, slot, tipBySlot[slot.Slot]) ||
			slot.Available+slot.Held > keyUnspentBound {
			return s, ErrLaneBillingOwnerEvidence
		}
		keyUnspentBound -= slot.Available + slot.Held
		s.idle += slot.Available
		s.held += slot.Held
		s.spent += slot.Spent
	}
	return s, nil
}
