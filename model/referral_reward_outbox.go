package model

import (
	"context"
	"errors"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrReferralRewardInvariant = errors.New("Referral reward accounting requires review")

// ReferralRewardOutbox belongs only to the ordinary invitation reward. The
// original payment and this immutable eligibility snapshot commit together;
// malformed policy or inviter balances cannot undo a customer's paid principal.
type ReferralRewardOutbox struct {
	ID            int64  `gorm:"primaryKey"`
	ReferenceID   string `gorm:"type:varchar(255);not null;uniqueIndex"`
	SourceUserID  int    `gorm:"not null"`
	Payload       string `gorm:"type:text;not null"`
	Status        string `gorm:"type:varchar(16);not null;index:idx_referral_reward_due,priority:1"`
	Attempts      int    `gorm:"not null"`
	NextAttemptAt int64  `gorm:"not null;index:idx_referral_reward_due,priority:2"`
	LastError     string `gorm:"type:varchar(255)"`
	CreatedAt     int64  `gorm:"autoCreateTime"`
	ProcessedAt   int64
}

func (*ReferralRewardOutbox) BeforeUpdate(tx *gorm.DB) error {
	if tx.Statement.Changed("ReferenceID", "SourceUserID", "Payload", "CreatedAt") {
		return ErrLedgerImmutable
	}
	return nil
}
func (*ReferralRewardOutbox) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

type ReferralRewardInbox struct {
	ID        int64  `gorm:"primaryKey"`
	EventID   int64  `gorm:"not null;uniqueIndex"`
	Outcome   string `gorm:"type:varchar(32);not null"`
	CreatedAt int64  `gorm:"autoCreateTime"`
}

func (*ReferralRewardInbox) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (*ReferralRewardInbox) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }
func ReferralRewardModels() []any                        { return []any{&ReferralRewardOutbox{}, &ReferralRewardInbox{}} }

type referralRewardSnapshot struct {
	UserID    int    `json:"user_id"`
	InviterID int    `json:"inviter_id"`
	Eligible  bool   `json:"eligible"`
	Enabled   bool   `json:"enabled"`
	Rate      string `json:"rate"`
	Ordinal   int    `json:"ordinal"`
	PaidQuota int    `json:"paid_quota"`
	PaidAt    int64  `json:"paid_at"`
}

// EnqueueReferralRewardTopUpTx is called after the customer's wallet lock and
// order success transition. That lock serializes the first-three ordinal across
// concurrent payments. It never reads a sales policy or creates cash commission.
func EnqueueReferralRewardTopUpTx(tx *gorm.DB, entry LedgerEntry) error {
	if entry.Reason != LedgerReasonTopup || entry.ReferenceType != ReferralReferenceTopUp {
		return nil
	}
	var topup TopUp
	if err := tx.Where("trade_no = ? AND user_id = ?", entry.ReferenceId, entry.UserId).First(&topup).Error; err != nil {
		return err
	}
	qualifies, err := currentTopUpOrderQualifiesTx(tx, entry.UserId, entry.ReferenceId)
	if err != nil {
		return err
	}
	snapshot := referralRewardSnapshot{UserID: entry.UserId, PaidQuota: entry.Delta, Enabled: common.AffiliateRewardEnabled, Rate: strconv.FormatFloat(common.AffiliateRewardRate, 'g', -1, 64), PaidAt: topup.CompleteTime}
	var source User
	if err := tx.Select("id", "status", "inviter_id").First(&source, entry.UserId).Error; err != nil {
		return err
	}
	snapshot.InviterID = source.InviterId
	if qualifies && source.Status == common.UserStatusEnabled && source.InviterId > 0 && source.InviterId != source.Id {
		var inviter User
		err := tx.Select("id", "status").First(&inviter, source.InviterId).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		snapshot.Eligible = err == nil && inviter.Status == common.UserStatusEnabled
	}
	if qualifies {
		snapshot.Ordinal, err = countQualifyingTopUpEventsTx(tx, entry.UserId)
		if err != nil {
			return err
		}
	}
	payload, err := common.Marshal(snapshot)
	if err != nil {
		return err
	}
	event := ReferralRewardOutbox{ReferenceID: entry.ReferenceId, SourceUserID: entry.UserId, Payload: string(payload), Status: "pending", NextAttemptAt: common.GetTimestamp()}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	// A newly credited principal must not reuse an unrelated event snapshot.
	var existing ReferralRewardOutbox
	if err := tx.Where("reference_id = ?", entry.ReferenceId).First(&existing).Error; err != nil {
		return err
	}
	if existing.SourceUserID != event.SourceUserID || existing.Payload != event.Payload {
		return ErrReferralRewardInvariant
	}
	return nil
}

// ProcessReferralRewardOutbox is multi-instance safe: event lock, immutable
// inbox receipt, reward record, inviter balance and done state commit atomically.
// A process crash has no leased processing state to strand an eligible reward.
func ProcessReferralRewardOutbox(ctx context.Context, limit int) (int, error) {
	limit = min(max(limit, 1), 200)
	now := common.GetTimestamp()
	var ids []int64
	if err := DB.WithContext(ctx).Model(&ReferralRewardOutbox{}).Where("status <> ? AND next_attempt_at <= ?", "done", now).Order("id").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	processed := 0
	var firstError error
	for _, id := range ids {
		err := processReferralRewardEvent(ctx, id)
		if err == nil {
			processed++
			continue
		}
		if firstError == nil {
			firstError = err
		}
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
		var event ReferralRewardOutbox
		if err := DB.WithContext(ctx).First(&event, id).Error; err != nil {
			continue
		}
		delay := int64(1) << min(max(event.Attempts, 0), 10)
		result := DB.WithContext(ctx).Model(&ReferralRewardOutbox{}).Where("id = ? AND status <> ?", id, "done").Updates(map[string]any{"status": "failed", "attempts": gorm.Expr("attempts + 1"), "next_attempt_at": now + delay, "last_error": "Referral reward processing failed; review the original snapshot before retrying"})
		if result.Error != nil {
			return processed, result.Error
		}
	}
	return processed, firstError
}
func processReferralRewardEvent(ctx context.Context, id int64) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var event ReferralRewardOutbox
		if err := lockForUpdate(tx).First(&event, id).Error; err != nil {
			return err
		}
		if event.Status == "done" {
			return nil
		}
		var inbox ReferralRewardInbox
		err := tx.Where("event_id = ?", id).First(&inbox).Error
		if err == nil {
			return tx.Model(&event).Updates(map[string]any{"status": "done", "processed_at": inbox.CreatedAt, "last_error": ""}).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var snapshot referralRewardSnapshot
		if err := common.UnmarshalJsonStr(event.Payload, &snapshot); err != nil {
			return err
		}
		if snapshot.UserID != event.SourceUserID || snapshot.PaidQuota <= 0 || snapshot.PaidQuota > common.MaxWalletQuota {
			return ErrReferralRewardInvariant
		}
		outcome, err := recordReferralRewardSnapshotTx(tx, &event, snapshot)
		if err != nil {
			return err
		}
		if err := tx.Create(&ReferralRewardInbox{EventID: id, Outcome: outcome}).Error; err != nil {
			return err
		}
		return tx.Model(&event).Updates(map[string]any{"status": "done", "processed_at": common.GetTimestamp(), "last_error": ""}).Error
	})
}
func recordReferralRewardSnapshotTx(tx *gorm.DB, event *ReferralRewardOutbox, s referralRewardSnapshot) (string, error) {
	if !s.Eligible || !s.Enabled || s.Ordinal < 1 || s.Ordinal > ReferralCommissionMaxTopUpEvents {
		return "ineligible", nil
	}
	if s.InviterID <= 0 || s.InviterID == s.UserID {
		return "", ErrReferralRewardInvariant
	}
	rate, err := strconv.ParseFloat(s.Rate, 64)
	if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
		return "", ErrReferralRewardInvariant
	}
	reward, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(int64(s.PaidQuota)).Mul(decimal.NewFromFloat(rate)).Truncate(0))
	if err != nil {
		return "", err
	}
	if reward == 0 {
		return "zero", nil
	}
	var inviter User
	if err := lockForUpdate(tx).Select("id", "aff_quota", "aff_history").First(&inviter, s.InviterID).Error; err != nil {
		return "", err
	}
	if inviter.AffQuota < 0 || inviter.AffHistoryQuota < 0 || inviter.AffQuota > common.MaxWalletQuota-reward || inviter.AffHistoryQuota > common.MaxWalletQuota-reward {
		return "", ErrReferralRewardInvariant
	}
	row := ReferralCommission{UserId: s.InviterID, SourceUserId: s.UserID, ReferenceType: ReferralReferenceTopUp, ReferenceId: event.ReferenceID, PaidQuota: s.PaidQuota, RewardQuota: reward, Rate: rate}
	created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if created.Error != nil {
		return "", created.Error
	}
	if created.RowsAffected == 0 {
		var existing ReferralCommission
		if err := tx.Where("reference_type = ? AND reference_id = ?", ReferralReferenceTopUp, event.ReferenceID).First(&existing).Error; err != nil {
			return "", err
		}
		if existing.UserId != row.UserId || existing.SourceUserId != row.SourceUserId || existing.PaidQuota != row.PaidQuota || existing.RewardQuota != row.RewardQuota {
			return "", ErrReferralRewardInvariant
		}
		return "replayed", nil
	}
	result := tx.Model(&User{}).Where("id = ?", s.InviterID).Updates(map[string]any{"aff_quota": inviter.AffQuota + reward, "aff_history": inviter.AffHistoryQuota + reward})
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected != 1 {
		return "", ErrReferralRewardInvariant
	}
	return "awarded", nil
}
