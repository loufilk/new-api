package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrInvalidUserQuotaAdjustment = errors.New("invalid user quota adjustment")
	ErrUserQuotaPermission        = errors.New("cannot adjust quota for this user role")
)

// UserQuotaAdjustment is the immutable database snapshot of a committed manual
// adjustment. Pending relay deductions in the quota cache are not part of it.
type UserQuotaAdjustment struct {
	UserID   int
	Username string
	Before   int
	After    int
	Replayed bool
}

func AdjustUserQuota(userID, operatorRole int, mode string, value int, entry LedgerEntry) (*UserQuotaAdjustment, error) {
	if userID <= 0 || (mode != "add" && mode != "subtract" && mode != "override") {
		return nil, ErrInvalidUserQuotaAdjustment
	}
	if mode != "override" && value <= 0 {
		return nil, ErrInvalidUserQuotaAdjustment
	}
	if value > common.MaxWalletQuota || value < -common.MaxWalletQuota {
		return nil, ErrWalletQuotaLimitExceeded
	}
	if value < 0 {
		return nil, ErrInvalidUserQuotaAdjustment
	}
	entry.UserId, entry.Account, entry.Delta = userID, AccountPaid, value
	if strings.TrimSpace(entry.IdempotencyKey) == "" {
		entry.IdempotencyKey = NewAutoLedgerKey("admin_quota_request")
	}

	var adjustment UserQuotaAdjustment
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if operatorRole != common.RoleRootUser && operatorRole <= user.Role {
			return ErrUserQuotaPermission
		}
		if user.Quota > common.MaxWalletQuota || user.Quota < -common.MaxWalletQuota {
			return ErrWalletQuotaLimitExceeded
		}
		var result *LedgerResult
		var err error
		switch mode {
		case "add":
			entry.Reason = LedgerReasonAdminAdd
			result, err = CreditQuotaTx(tx, entry)
		case "subtract":
			entry.Reason = LedgerReasonAdminSubtract
			result, err = DebitQuotaTx(tx, entry, AccountPaid)
		case "override":
			entry.Reason = LedgerReasonAdminOverride
			result, err = OverrideQuotaTx(tx, entry)
		}
		if err != nil {
			return err
		}
		adjustment = UserQuotaAdjustment{UserID: user.Id, Username: user.Username, Before: result.TotalBefore, After: result.TotalAfter, Replayed: result.Replayed}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Wallet reservations and adjustments are already committed in the ledger.
	// A concurrent cache miss may have loaded the new balance, so applying the
	// same difference to Redis would credit or debit it twice.
	if !adjustment.Replayed {
		RefreshUserQuotaCache(userID, adjustment.After)
	}
	return &adjustment, nil
}
