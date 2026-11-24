//go:build lane_billing_integration_dev

package model

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var ErrLaneLogicalWalletPolicyDev = errors.New("logical quota rehearsal only accepts an existing paid-only wallet_first owner")

// LaneLogicalQuotaDevScope is a trusted fixture scope, not a public funding
// choice. Multi-wallet-epoch history is deliberately refused by this slice.
type LaneLogicalQuotaDevScope struct {
	Key         LaneBillingFiniteKeyIdentity
	WalletEpoch int64
}

type LaneLogicalQuotaDevView struct {
	WalletPaidRaw       int
	WalletPaidIdle      int
	WalletPaidAvailable int
	WalletBonusRaw      int
	WalletRewardRaw     int
	KeyRemainRaw        int
	KeyIdle             int
	KeyHeld             int
	KeyUnprojectedSpent int
	KeyRemainAvailable  int
	KeyUsedRaw          int
	KeyUsedCompatible   int
}

func (s LaneLogicalQuotaDevScope) valid() bool { return s.Key.valid() && s.WalletEpoch > 0 }

func laneLogicalQuotaDevDBValid(ctx context.Context, db *gorm.DB) bool {
	if ctx == nil || db == nil || db.Config == nil || db.Statement == nil || db.Dialector == nil ||
		db.Dialector.Name() != "postgres" || db.DryRun || common.RedisEnabled || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return false
	}
	_, nestedStatement := db.Statement.ConnPool.(gorm.TxCommitter)
	_, nestedConfig := db.Config.ConnPool.(gorm.TxCommitter)
	return !nestedStatement && !nestedConfig
}

// ReadLaneLogicalQuotaDev reads one repeatable snapshot without touching
// raw quota/cache/state. The fixture has no usage projector: all slot Spent
// remains unprojected. Production cannot use this formula without durable
// projection offsets. Key holds are counted in Used for legacy display
// semantics but neither they nor wallet holds are available to spend.
func ReadLaneLogicalQuotaDev(ctx context.Context, db *gorm.DB, scope LaneLogicalQuotaDevScope) (LaneLogicalQuotaDevView, error) {
	var view LaneLogicalQuotaDevView
	if !scope.valid() || !laneLogicalQuotaDevDBValid(ctx, db) {
		return view, ErrLaneBillingOwnerUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	err := db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		if err := laneBillingFiniteKeyDevScopeTx(tx); err != nil {
			return err
		}
		snapshot, err := readLaneLogicalQuotaDevTx(tx, scope, false)
		if err != nil {
			return err
		}
		view = snapshot.view
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return LaneLogicalQuotaDevView{}, laneLogicalQuotaDevError(ctx, err)
	}
	return view, nil
}

type laneLogicalQuotaDevSnapshot struct {
	user   User
	token  Token
	wallet []SharedWalletShare
	key    []laneBillingFiniteKeyShareV1
	view   LaneLogicalQuotaDevView
}

// Two index range seeks cover all out-of-scope history. An operation that
// survives its share must not look like an empty slot, even in another epoch.
const laneWalletDevOutsideScopeSQL = `SELECT EXISTS (
	SELECT FROM shared_wallet_transfers WHERE user_id = ? AND (epoch, slot) < (?, 0)
) OR EXISTS (
	SELECT FROM shared_wallet_transfers WHERE user_id = ? AND (epoch, slot) >= (?, ?)
)`

// Enumerate the fixed slot domain, not this user's lifetime transfers.
const laneWalletDevTipsSQL = `SELECT tip.* FROM generate_series(0, ?) AS slots(slot)
	CROSS JOIN LATERAL (
		SELECT * FROM shared_wallet_transfers
		WHERE user_id = ? AND epoch = ? AND slot = slots.slot
		ORDER BY share_version DESC LIMIT 1
	) AS tip ORDER BY tip.slot`

// For a debit, the canonical ordinary owner already owns the user lock.
// Pin user -> original token -> current Key enrollment -> ordered wallet
// slots -> ordered Key slots. Never acquire a new wallet slot after any
// Key slot. Hot finish can still proceed once the transaction releases.
func readLaneLogicalQuotaDevTx(tx *gorm.DB, scope LaneLogicalQuotaDevScope, lock bool) (laneLogicalQuotaDevSnapshot, error) {
	s := laneLogicalQuotaDevSnapshot{}
	query := func() *gorm.DB {
		d := tx.Session(&gorm.Session{NewDB: true})
		if lock {
			return lockForUpdate(d)
		}
		return d
	}
	id := scope.Key
	if err := query().Where("id = ?", id.UserID).Take(&s.user).Error; err != nil {
		return s, err
	}
	if err := query().Where("id = ?", id.TokenID).Take(&s.token).Error; err != nil {
		return s, err
	}
	if !originalLaneBillingFiniteKey(id, s.token) || lock && !liveLaneBillingFiniteKey(s.user, s.token) {
		return s, ErrLaneBillingFiniteKeyIdentity
	}
	var enrollment laneBillingFiniteKeyEnrollmentV1
	if err := query().Where("user_id = ? AND token_id = ?", id.UserID, id.TokenID).
		Order("epoch DESC").Take(&enrollment).Error; err != nil {
		return s, err
	}
	if !enrollment.valid() || enrollment.identity() != id {
		return s, ErrLaneBillingFiniteKeyIdentity
	}
	u, token := s.user, s.token
	if u.QuotaPaid < 0 || u.QuotaBonus < 0 || u.QuotaReward < 0 || u.QuotaPaid > common.MaxWalletQuota ||
		u.QuotaBonus > common.MaxWalletQuota || u.QuotaReward > common.MaxWalletQuota || u.Quota < 0 || u.Quota > common.MaxWalletQuota ||
		u.QuotaPaid+u.QuotaBonus+u.QuotaReward != u.Quota || token.RemainQuota < 0 || token.RemainQuota > common.MaxWalletQuota {
		return s, ErrLaneBillingOwnerEvidence
	}
	s.view.WalletPaidRaw, s.view.WalletBonusRaw, s.view.WalletRewardRaw = u.QuotaPaid, u.QuotaBonus, u.QuotaReward
	s.view.KeyRemainRaw, s.view.KeyUsedRaw = token.RemainQuota, token.UsedQuota
	if err := query().Where("user_id = ?", id.UserID).Order("epoch, slot").Limit(SharedWalletMaxSlots + 1).Find(&s.wallet).Error; err != nil {
		return s, err
	}
	if len(s.wallet) > SharedWalletMaxSlots {
		return s, ErrSharedWalletEvidence
	}
	var outside bool
	if err := tx.Raw(laneWalletDevOutsideScopeSQL, id.UserID, scope.WalletEpoch, id.UserID, scope.WalletEpoch, SharedWalletMaxSlots).Scan(&outside).Error; err != nil {
		return s, err
	}
	if outside {
		return s, ErrSharedWalletEvidence
	}
	var walletTips []SharedWalletTransfer
	if err := tx.Raw(laneWalletDevTipsSQL, SharedWalletMaxSlots-1, id.UserID, scope.WalletEpoch).Scan(&walletTips).Error; err != nil {
		return s, err
	}
	if len(walletTips) != len(s.wallet) {
		return s, ErrSharedWalletEvidence
	}
	walletUnspentBound := common.MaxWalletQuota - u.Quota
	for i, slot := range s.wallet {
		if slot.UserID != id.UserID || slot.Epoch != scope.WalletEpoch || !slot.valid() ||
			slot.Available+slot.Held > walletUnspentBound {
			return s, ErrSharedWalletEvidence
		}
		tip := walletTips[i]
		if tip.UserID != slot.UserID || tip.Epoch != slot.Epoch || tip.Slot != slot.Slot ||
			tip.OperationID != slot.LastOperationID || tip.ShareVersion > slot.Version ||
			tip.GrantedAfter != slot.Granted || tip.ReturnedAfter != slot.Returned ||
			verifySharedWalletTransferEvidenceTx(tx.Limit(2), tip) != nil {
			return s, ErrSharedWalletEvidence
		}
		walletUnspentBound -= slot.Available + slot.Held
		s.view.WalletPaidIdle += slot.Available
	}
	s.view.WalletPaidAvailable = s.view.WalletPaidRaw + s.view.WalletPaidIdle

	if err := query().Where("user_id = ? AND token_id = ? AND epoch = ?", id.UserID, id.TokenID, id.Epoch).
		Order("slot").Limit(SharedWalletMaxSlots + 1).Find(&s.key).Error; err != nil {
		return s, err
	}
	if len(s.key) > SharedWalletMaxSlots {
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
	if len(tips) != len(s.key) {
		return s, ErrLaneBillingOwnerEvidence
	}
	tipBySlot := make(map[int]laneBillingFiniteKeyTransferV1, len(tips))
	for _, tip := range tips {
		tipBySlot[tip.Slot] = tip
	}
	keyUnspentBound := common.MaxWalletQuota - token.RemainQuota
	for _, slot := range s.key {
		if !laneBillingKeySlotTransferMatches(id, slot, tipBySlot[slot.Slot]) ||
			slot.Available+slot.Held > keyUnspentBound {
			return s, ErrLaneBillingOwnerEvidence
		}
		keyUnspentBound -= slot.Available + slot.Held
		s.view.KeyIdle += slot.Available
		s.view.KeyHeld += slot.Held
		s.view.KeyUnprojectedSpent += slot.Spent
	}
	// At most 32 independently bounded bigint slots contribute here.
	if s.view.KeyUsedRaw > math.MaxInt-s.view.KeyHeld-s.view.KeyUnprojectedSpent {
		return s, ErrLaneBillingOwnerEvidence
	}
	s.view.KeyRemainAvailable = s.view.KeyRemainRaw + s.view.KeyIdle
	s.view.KeyUsedCompatible = s.view.KeyUsedRaw + s.view.KeyHeld + s.view.KeyUnprojectedSpent
	return s, nil
}

func checkLaneLogicalWalletFirstDevUser(u User) error {
	if u.DeletedAt.Valid || u.Status != common.UserStatusEnabled || u.QuotaBonus != 0 || u.QuotaReward != 0 ||
		common.NormalizeBillingPreference(u.GetSetting().BillingPreference) != "wallet_first" {
		return ErrLaneLogicalWalletPolicyDev
	}
	return nil
}

func laneLogicalReclaimOperationDevID(kind, requestID string, user, token int, epoch int64, slot int) string {
	raw := sha256.Sum256([]byte(fmt.Sprintf("ordinary-logical-reclaim-v1|%s|%s|%d|%d|%d|%d", kind, requestID, user, token, epoch, slot)))
	return hex.EncodeToString(raw[:])
}

func laneLogicalQuotaDevError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case errors.Is(err, ErrLaneBillingOwnerUnavailable), errors.Is(err, ErrLaneBillingOwnerEvidence),
		errors.Is(err, ErrSharedWalletEvidence), errors.Is(err, ErrLaneBillingFiniteKeyIdentity),
		errors.Is(err, ErrLaneLogicalWalletPolicyDev), errors.Is(err, ErrLedgerInsufficientBalance),
		errors.Is(err, ErrBillingTokenInsufficient), errors.Is(err, ErrBillingRequestOwnerConflict),
		errors.Is(err, ErrBillingFinalized):
		return err
	case errors.Is(err, gorm.ErrRecordNotFound):
		return ErrLaneBillingOwnerEvidence
	default:
		return ErrLaneBillingOwnerStore
	}
}

// ReserveLaneLogicalWalletFirstDev rehearses an INITIAL ordinary paid wallet
// debit. It cannot change source, claim a lane request, reclaim held/settled
// amounts, or reinterpret absolute quota writes. Any failure rolls back
// reclaims, standard ledger debit, original Key hold, and canonical receipt.
func ReserveLaneLogicalWalletFirstDev(ctx context.Context, db *gorm.DB, scope LaneLogicalQuotaDevScope, requestID string, amount int) (BillingFinalization, BillingReservationAttempt, error) {
	var empty BillingFinalization
	if !scope.valid() || !laneLogicalQuotaDevDBValid(ctx, db) || !laneBillingOwnerIDValid(requestID) || amount <= 0 || amount > common.MaxQuota {
		return empty, BillingReservationAttempt{}, ErrLaneBillingOwnerUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, laneBillingOwnerTimeout)
	defer cancel()
	db = db.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: logger.Discard})
	id := scope.Key
	authorization := BillingFinalization{RequestID: requestID, UserID: id.UserID, Source: "wallet",
		TokenID: id.TokenID, TokenFingerprint: id.TokenFingerprint}
	owner, attempt, err := reserveBillingFinalizationWithDB(ctx, db, authorization, nil, amount, laneBillingFiniteKeyDevScopeTx,
		func(tx *gorm.DB, owner *BillingFinalization) error {
			s, err := readLaneLogicalQuotaDevTx(tx, scope, true)
			if err != nil {
				return err
			}
			if err := checkLaneLogicalWalletFirstDevUser(s.user); err != nil {
				return err
			}
			if s.view.WalletPaidAvailable < amount {
				return ErrLedgerInsufficientBalance
			}
			if s.view.KeyRemainAvailable < amount {
				return ErrBillingTokenInsufficient
			}
			keyNeed := amount - s.view.KeyRemainRaw
			for _, slot := range s.key {
				if keyNeed <= 0 {
					break
				}
				n := min(slot.Available, keyNeed)
				if n > 0 {
					var receipt LaneBillingFiniteKeyTransferReceipt
					var replayed bool
					err := applyLaneBillingFiniteKeyDevTransferTx(tx, LaneBillingFiniteKeyTransfer{
						Identity: id, OperationID: laneLogicalReclaimOperationDevID("finite", requestID, id.UserID, id.TokenID, slot.Epoch, slot.Slot),
						Slot: slot.Slot, Direction: "reclaim", Amount: n}, true, &receipt, &replayed)
					if err != nil {
						return err
					}
					if replayed {
						return ErrLaneBillingOwnerEvidence
					}
					keyNeed -= n
				}
			}
			if keyNeed > 0 {
				return ErrLaneBillingOwnerEvidence
			}
			entry := LedgerEntry{UserId: id.UserID, Delta: amount, Reason: LedgerReasonBillingPreConsume,
				RequestId: requestID, ReferenceType: "relay", ReferenceId: requestID, IdempotencyKey: "billing:preconsume:" + requestID}
			debit, err := debitQuotaTx(tx, entry, &walletPaidIdleDebit{
				tx: tx, userID: id.UserID, epoch: scope.WalletEpoch, request: requestID, slots: s.wallet,
			}, AccountPaid)
			if err != nil {
				return err
			}
			if debit.Replayed {
				return ErrLaneBillingOwnerEvidence
			}
			if err := ReserveBillingTokenTx(tx, owner, amount, false); err != nil {
				return err
			}
			owner.SourceHeld, owner.TokenHeld, owner.Paid = amount, amount, amount
			return nil
		}, func(tx *gorm.DB, owner BillingFinalization) error {
			if owner.SourceHeld != amount || owner.Paid != amount || owner.TokenHeld != amount ||
				owner.Bonus != 0 || owner.Reward != 0 {
				return ErrLaneBillingOwnerEvidence
			}
			return validateBillingSourceTx(tx, &owner)
		})
	if err != nil {
		if attempt.FreshInitialPrepared {
			return empty, attempt, ErrLaneBillingOwnerCommitUncertain
		}
		return empty, attempt, laneLogicalQuotaDevError(ctx, err)
	}
	return owner, attempt, nil
}
