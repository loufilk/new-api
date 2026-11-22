package model

import (
	"context"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	NewUserBenefitRewardPending = "pending"
	NewUserBenefitRewardGranted = "granted"
	NewUserBenefitRewardDenied  = "denied"
)

var newUserBenefitAlipayUID = regexp.MustCompile(`^2088[0-9]{12}$`)

// Payment success and reward approval are separate durable transitions. The
// principal is already in the wallet whenever this record exists.
type NewUserBenefitReward struct {
	UserID    int    `gorm:"primaryKey;autoIncrement:false"`
	TradeNo   string `gorm:"type:varchar(32);not null;uniqueIndex"`
	Status    string `gorm:"type:varchar(16);not null"`
	Reason    string `gorm:"type:varchar(64);not null"`
	CheckedAt int64  `gorm:"not null"`
	GrantedAt int64  `gorm:"not null"`
	Attempts  int64  `gorm:"not null"`
}

// The global Alipay UID namespace survives merchant changes and account
// deletion. Insertion and bonus credit share one transaction; a unique-key
// conflict cannot turn a second paid order into another rewarded identity.
type NewUserBenefitPaymentIdentity struct {
	IdentityHash string `gorm:"type:varchar(64);primaryKey"`
	UserID       int    `gorm:"not null;uniqueIndex"`
	TradeNo      string `gorm:"type:varchar(32);not null;uniqueIndex"`
	CreatedAt    int64  `gorm:"not null"`
}

func (*NewUserBenefitPaymentIdentity) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (*NewUserBenefitPaymentIdentity) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

func hydrateNewUserBenefitRewardTx(tx *gorm.DB, order *NewUserBenefitOrder, topUp *TopUp) error {
	if order.RiskPolicyVersion == 0 {
		order.RequireVerifiedEmail, order.RequireTurnstile, order.NetworkLimitEnabled = true, true, true
	}
	if topUp.Status != common.TopUpStatusSuccess {
		return nil
	}
	if !order.PaymentIdentityCheckEnabled {
		order.RewardStatus = NewUserBenefitRewardGranted
		return nil
	}
	var reward NewUserBenefitReward
	if err := tx.First(&reward, order.UserID).Error; err != nil {
		return ErrNewUserBenefitOrderConflict
	}
	if reward.TradeNo != order.TradeNo {
		return ErrNewUserBenefitOrderConflict
	}
	order.RewardStatus, order.RewardReason, order.GrantedAt = reward.Status, reward.Reason, reward.GrantedAt
	switch reward.Status {
	case NewUserBenefitRewardPending:
		order.Status = NewUserBenefitStatusVerificationPending
	case NewUserBenefitRewardGranted:
		order.Status = NewUserBenefitStatusClaimed
	case NewUserBenefitRewardDenied:
		order.Status = NewUserBenefitStatusRewardDenied
	default:
		return ErrNewUserBenefitOrderConflict
	}
	return nil
}

// GetNewUserBenefitPaymentReview returns only a paid order whose reward still
// requires a provider query. userID=0 is reserved for the signed webhook path.
func GetNewUserBenefitPaymentReview(ctx context.Context, tradeNo string, userID int) (*NewUserBenefitOrder, *TopUp, error) {
	var topUp TopUp
	query := DB.WithContext(ctx).Where("trade_no = ?", tradeNo)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.First(&topUp).Error; err != nil {
		return nil, nil, err
	}
	if !topUp.NewUserBenefit || topUp.Status != common.TopUpStatusSuccess {
		return nil, nil, nil
	}
	order, err := getNewUserBenefitOrderTx(DB.WithContext(ctx), topUp.UserId)
	if err != nil {
		return nil, nil, err
	}
	if !order.PaymentIdentityCheckEnabled || order.RewardStatus != NewUserBenefitRewardPending {
		return nil, nil, nil
	}
	if topUp.PaymentReceiptID <= 0 || topUp.PaidProviderReference == "" || topUp.PaidMerchantScope != order.MerchantScope {
		return nil, nil, ErrNewUserBenefitOrderConflict
	}
	return order, &topUp, nil
}

// FinalizeNewUserBenefitReward accepts only the result of the server's bound
// provider query. An unavailable/mismatched query records a retryable state;
// a verified reused payer is denied. Neither path can debit or repeat principal.
func FinalizeNewUserBenefitReward(ctx context.Context, tradeNo, buyer, reason string, evidence VerifiedPaymentFacts) error {
	var userID, totalAfter int
	credited := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var topUp TopUp
		if err := lockForUpdate(tx).Where("trade_no = ?", tradeNo).First(&topUp).Error; err != nil {
			return err
		}
		if !topUp.NewUserBenefit || topUp.Status != common.TopUpStatusSuccess {
			return ErrNewUserBenefitPaymentRequired
		}
		order, err := getNewUserBenefitOrderTx(tx, topUp.UserId)
		if err != nil {
			return err
		}
		if !order.PaymentIdentityCheckEnabled || order.RewardStatus != NewUserBenefitRewardPending {
			return nil
		}
		var reward NewUserBenefitReward
		if err := tx.First(&reward, order.UserID).Error; err != nil {
			return err
		}
		reward.CheckedAt, reward.Attempts = common.GetTimestamp(), reward.Attempts+1
		switch reason {
		case "query_unavailable", "order_mismatch", "identity_unavailable":
			reward.Reason = reason
			return tx.Save(&reward).Error
		case "":
		default:
			return ErrNewUserBenefitOrderConflict
		}
		if order.PaymentMethod != "alipay" || !newUserBenefitAlipayUID.MatchString(buyer) {
			return ErrNewUserBenefitPaymentRequired
		}
		if evidence.Provider != PaymentProviderEpay || evidence.MerchantScope != order.MerchantScope || evidence.Environment != "live" ||
			evidence.Currency != "CNY" || evidence.CurrencyExponent != 2 || evidence.PaidMinor != order.PayAmountCents ||
			evidence.ExternalReference != topUp.PaidProviderReference || topUp.PaymentReceiptID <= 0 {
			return ErrNewUserBenefitPaymentRequired
		}
		identity := NewUserBenefitPaymentIdentity{IdentityHash: newUserBenefitDigest("alipay_uid:v1", buyer), UserID: order.UserID, TradeNo: tradeNo, CreatedAt: reward.CheckedAt}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&identity)
		if result.Error != nil {
			return result.Error
		}
		// Read back ownership rather than relying on dialect-specific affected
		// row counts for an INSERT ... ON CONFLICT/ON DUPLICATE KEY no-op.
		var owner NewUserBenefitPaymentIdentity
		if err := lockForUpdate(tx).Where("identity_hash = ?", identity.IdentityHash).First(&owner).Error; err != nil {
			return ErrNewUserBenefitOrderConflict
		}
		if owner.UserID != order.UserID || owner.TradeNo != tradeNo {
			reward.Status, reward.Reason = NewUserBenefitRewardDenied, "payment_identity_used"
			return tx.Save(&reward).Error
		}
		if order.BonusQuota > 0 {
			entry := LedgerEntry{UserId: order.UserID, Account: AccountBonus, Delta: order.BonusQuota, Reason: LedgerReasonNewUserTopUp,
				ReferenceType: "new_user_topup", ReferenceId: tradeNo, IdempotencyKey: "credit:new_user_topup_bonus:" + tradeNo}
			entry.SetMetadata(map[string]any{"config_version": order.ConfigVersion, "payment_identity_verified": true, "credit_amount_cents": order.CreditAmountCents})
			credit, err := CreditQuotaTx(tx, entry)
			if err != nil {
				return err
			}
			if credit.Replayed {
				return ErrNewUserBenefitOrderConflict
			}
			userID, totalAfter, credited = order.UserID, credit.TotalAfter, true
		}
		reward.Status, reward.Reason, reward.GrantedAt = NewUserBenefitRewardGranted, "", reward.CheckedAt
		return tx.Save(&reward).Error
	})
	if err == nil && credited {
		RefreshUserQuotaCache(userID, totalAfter)
	}
	return err
}
