package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

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

// readLaneWalletCustodyTx runs under the caller's consistent financial
// snapshot or existing user lock. Capacity excludes all held/returned/spent.
func readLaneWalletCustodyTx(tx *gorm.DB, userID int, walletEpoch int64, raw int, lock bool) ([]SharedWalletShare, int, error) {
	if userID <= 0 || walletEpoch <= 0 || raw < 0 || raw > common.MaxWalletQuota {
		return nil, 0, ErrSharedWalletEvidence
	}
	query := tx.Session(&gorm.Session{NewDB: true})
	if lock {
		query = lockForUpdate(query)
	}
	var wallet []SharedWalletShare
	idle := 0
	if err := query.Where("user_id = ?", userID).Order("epoch, slot").Limit(SharedWalletMaxSlots + 1).Find(&wallet).Error; err != nil {
		return nil, 0, err
	}
	if len(wallet) > SharedWalletMaxSlots {
		return nil, 0, ErrSharedWalletEvidence
	}
	var outside bool
	if err := tx.Raw(laneWalletDevOutsideScopeSQL, userID, walletEpoch, userID, walletEpoch, SharedWalletMaxSlots).Scan(&outside).Error; err != nil {
		return nil, 0, err
	}
	if outside {
		return nil, 0, ErrSharedWalletEvidence
	}
	var walletTips []SharedWalletTransfer
	if err := tx.Raw(laneWalletDevTipsSQL, SharedWalletMaxSlots-1, userID, walletEpoch).Scan(&walletTips).Error; err != nil {
		return nil, 0, err
	}
	if len(walletTips) != len(wallet) {
		return nil, 0, ErrSharedWalletEvidence
	}
	walletUnspentBound := common.MaxWalletQuota - raw
	for i, slot := range wallet {
		if slot.UserID != userID || slot.Epoch != walletEpoch || !slot.valid() ||
			slot.Available+slot.Held > walletUnspentBound {
			return nil, 0, ErrSharedWalletEvidence
		}
		tip := walletTips[i]
		if tip.UserID != slot.UserID || tip.Epoch != slot.Epoch || tip.Slot != slot.Slot ||
			tip.OperationID != slot.LastOperationID || tip.ShareVersion > slot.Version ||
			tip.GrantedAfter != slot.Granted || tip.ReturnedAfter != slot.Returned ||
			verifySharedWalletTransferEvidenceTx(tx.Limit(2), tip) != nil {
			return nil, 0, ErrSharedWalletEvidence
		}
		walletUnspentBound -= slot.Available + slot.Held
		idle += slot.Available
	}
	return wallet, idle, nil
}
