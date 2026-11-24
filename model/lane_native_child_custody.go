package model

import "gorm.io/gorm"

// Holds and settlements mutate only the exact wallet slot under this same
// lock. A cold grant/reclaim takes the original User lock before wallet slots.
// No assertion here substitutes for a future complete writer inventory.
func verifyLaneNativeChildWalletTx(tx *gorm.DB, b laneBillingChildBinding, cap int) error {
	var wallet SharedWalletShare
	if err := lockForUpdate(tx).Where("user_id = ? AND epoch = ? AND slot = ?", b.Owner.UserID, b.WalletEpoch, b.WalletSlot).
		Take(&wallet).Error; err != nil {
		return err
	}
	var tip SharedWalletTransfer
	if err := tx.Where("user_id = ? AND epoch = ? AND slot = ?", wallet.UserID, wallet.Epoch, wallet.Slot).
		Order("share_version DESC").Take(&tip).Error; err != nil {
		return ErrLaneBillingOwnerEvidence
	}
	if !wallet.valid() || wallet.Available < cap || tip.OperationID != wallet.LastOperationID ||
		tip.ShareVersion > wallet.Version || tip.GrantedAfter != wallet.Granted || tip.ReturnedAfter != wallet.Returned ||
		verifySharedWalletTransferEvidenceTx(tx.Limit(2), tip) != nil {
		return ErrLaneBillingOwnerEvidence
	}
	return nil
}
