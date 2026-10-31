package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Outbox insertion is part of principal settlement. Award calculation is not.
// A worker failure leaves this durable event retryable and cannot un-credit a
// customer whose payment has already committed.
type AgentCommissionOutbox struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	EventKey      string `json:"event_key" gorm:"type:char(64);not null;uniqueIndex"`
	Kind          string `json:"kind" gorm:"type:varchar(32);not null"`
	ReferenceID   string `json:"reference_id" gorm:"type:varchar(255);not null;index"`
	SourceUserID  int    `json:"source_user_id" gorm:"not null"`
	Route         string `json:"route" gorm:"type:varchar(32);not null"`
	Payload       string `json:"-" gorm:"type:text;not null"`
	Status        string `json:"status" gorm:"type:varchar(16);not null;index:idx_commission_outbox_due,priority:1"`
	Attempts      int    `json:"attempts" gorm:"not null"`
	NextAttemptAt int64  `json:"next_attempt_at" gorm:"not null;index:idx_commission_outbox_due,priority:2"`
	LastError     string `json:"last_error" gorm:"type:varchar(255)"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime"`
	ProcessedAt   int64  `json:"processed_at"`
}

type AgentCommissionInbox struct {
	ID        int64  `json:"id" gorm:"primaryKey"`
	EventID   int64  `json:"event_id" gorm:"not null;uniqueIndex"`
	Outcome   string `json:"outcome" gorm:"type:varchar(64);not null"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}

type agentCommissionPaymentSnapshot struct {
	UserID         int                       `json:"user_id"`
	InviterID      int                       `json:"inviter_id"`
	Eligible       bool                      `json:"eligible"`
	Qualification  string                    `json:"qualification"`
	ProfileVersion int64                     `json:"profile_version"`
	PaidQuota      int                       `json:"paid_quota"`
	LegacyEnabled  bool                      `json:"legacy_enabled"`
	LegacyRate     string                    `json:"legacy_rate"`
	LegacyOrdinal  int                       `json:"legacy_ordinal"`
	PaidAt         int64                     `json:"paid_at"`
	Payment        AgentVerifiedPaymentFacts `json:"payment"`
	Policy         AgentPolicy               `json:"policy"`
}

// EnqueueAgentCommissionTopUpTx only snapshots durable payment and entitlement
// facts. Invalid reward rates/negative inviter balances are worker errors, not
// reasons to roll back principal. A missing outbox table/failed insert IS a
// settlement failure: the payment provider must retry the original order.
func EnqueueAgentCommissionTopUpTx(tx *gorm.DB, entry LedgerEntry) error {
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
	snapshot := agentCommissionPaymentSnapshot{UserID: entry.UserId, PaidQuota: entry.Delta, LegacyEnabled: common.AffiliateRewardEnabled, LegacyRate: strconv.FormatFloat(common.AffiliateRewardRate, 'g', -1, 64), PaidAt: topup.CompleteTime, Qualification: "ordinary"}
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
		var profile AgentProfile
		err = tx.Where("user_id = ?", source.InviterId).First(&profile).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			snapshot.ProfileVersion = profile.Version
			if profile.Status == "active" && !profile.SalesFrozen {
				snapshot.Qualification = "agent"
			}
		}
	}
	if qualifies {
		snapshot.LegacyOrdinal, err = countQualifyingTopUpEventsTx(tx, entry.UserId)
		if err != nil {
			return err
		}
	}
	// Read without acquiring a later policy lock while the principal wallet row
	// is locked. Policy versions are immutable, so this is a coherent snapshot.
	policy, err := ReadCurrentAgentPolicyTx(tx)
	if err != nil {
		return err
	}
	snapshot.Policy = *policy
	snapshot.Payment = topup.verifiedAgentPaymentFacts()
	route := "legacy"
	if policy.CommissionCutoverAt > 0 && topup.CompleteTime >= policy.CommissionCutoverAt && topup.PaymentReceiptID > 0 {
		route = "cash"
	}
	payload, err := common.Marshal(snapshot)
	if err != nil {
		return err
	}
	key, err := agentCommissionHash([]string{"topup", entry.ReferenceId})
	if err != nil {
		return err
	}
	row := AgentCommissionOutbox{EventKey: key, Kind: "payment", ReferenceID: entry.ReferenceId, SourceUserID: entry.UserId, Route: route, Payload: string(payload), Status: "pending", NextAttemptAt: common.GetTimestamp()}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// ProcessAgentCommissionOutbox is multi-instance safe. A locked outbox row,
// inbox receipt, award, cap, balance and ledger all commit together. There is
// no leased "processing" state to strand work after a process crash.
func ProcessAgentCommissionOutbox(ctx context.Context, limit int) (int, error) {
	limit = min(max(limit, 1), 200)
	var ids []int64
	now := common.GetTimestamp()
	if err := DB.WithContext(ctx).Model(&AgentCommissionOutbox{}).Where("status <> ? AND next_attempt_at <= ?", "done", now).Order("id").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	processed := 0
	var firstError error
	for _, id := range ids {
		err := processAgentCommissionEvent(ctx, id)
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
		// The failed calculation transaction was rolled back. Record operational
		// retry state separately, without modifying its immutable event payload.
		var event AgentCommissionOutbox
		if loadErr := DB.WithContext(ctx).First(&event, id).Error; loadErr != nil {
			continue
		}
		delay := int64(1) << min(event.Attempts, 10)
		DB.WithContext(ctx).Model(&AgentCommissionOutbox{}).Where("id = ? AND status <> ?", id, "done").Updates(map[string]any{"status": "failed", "attempts": gorm.Expr("attempts + 1"), "next_attempt_at": now + delay, "last_error": "Commission processing failed; review the event before retrying"})
	}
	return processed, firstError
}

func processAgentCommissionEvent(ctx context.Context, id int64) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var event AgentCommissionOutbox
		if err := lockForUpdate(tx).First(&event, id).Error; err != nil {
			return err
		}
		if event.Status == "done" {
			return nil
		}
		var inbox AgentCommissionInbox
		err := tx.Where("event_id = ?", id).First(&inbox).Error
		if err == nil {
			return tx.Model(&event).Updates(map[string]any{"status": "done", "processed_at": inbox.CreatedAt, "last_error": ""}).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		outcome := "ignored"
		switch event.Kind {
		case "payment":
			var snapshot agentCommissionPaymentSnapshot
			if err := common.UnmarshalJsonStr(event.Payload, &snapshot); err != nil {
				return err
			}
			if event.Route == "legacy" {
				outcome, err = recordLegacyReferralSnapshotTx(tx, &event, snapshot)
			} else if event.Route == "cash" {
				outcome, err = recordAgentCashCommissionTx(tx, &event, snapshot)
			} else {
				return ErrAgentCommissionInvariant
			}
		case "provider_refund":
			outcome, err = resolveAgentCommissionProviderRefundTx(tx, &event)
		case "refund":
			outcome, err = reverseAgentCommissionEventTx(tx, &event)
		default:
			return ErrAgentCommissionInvariant
		}
		if err != nil {
			return err
		}
		if err := tx.Create(&AgentCommissionInbox{EventID: id, Outcome: outcome}).Error; err != nil {
			return err
		}
		return tx.Model(&event).Updates(map[string]any{"status": "done", "processed_at": common.GetTimestamp(), "last_error": ""}).Error
	})
}

func recordLegacyReferralSnapshotTx(tx *gorm.DB, event *AgentCommissionOutbox, s agentCommissionPaymentSnapshot) (string, error) {
	if !s.Eligible || !s.LegacyEnabled || s.LegacyOrdinal < 1 || s.LegacyOrdinal > ReferralCommissionMaxTopUpEvents {
		return "legacy_ineligible", nil
	}
	rate, err := strconv.ParseFloat(s.LegacyRate, 64)
	if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
		return "", ErrAgentCommissionInvalid
	}
	reward, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(int64(s.PaidQuota)).Mul(decimal.NewFromFloat(rate)).Truncate(0))
	if err != nil {
		return "", err
	}
	if reward == 0 {
		return "legacy_zero", nil
	}
	var inviter User
	if err := lockForUpdate(tx).Select("id", "aff_quota", "aff_history").First(&inviter, s.InviterID).Error; err != nil {
		return "", err
	}
	if inviter.AffQuota < 0 || inviter.AffHistoryQuota < 0 || inviter.AffQuota > common.MaxWalletQuota-reward || inviter.AffHistoryQuota > common.MaxWalletQuota-reward {
		return "", ErrAgentCommissionInvariant
	}
	row := ReferralCommission{UserId: s.InviterID, SourceUserId: s.UserID, ReferenceType: ReferralReferenceTopUp, ReferenceId: event.ReferenceID, PaidQuota: s.PaidQuota, RewardQuota: reward, Rate: rate}
	created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if created.Error != nil {
		return "", created.Error
	}
	if created.RowsAffected == 0 {
		return "legacy_replayed", nil
	}
	return "legacy_awarded", tx.Model(&User{}).Where("id = ?", s.InviterID).Updates(map[string]any{"aff_quota": inviter.AffQuota + reward, "aff_history": inviter.AffHistoryQuota + reward}).Error
}

func recordAgentCashCommissionTx(tx *gorm.DB, event *AgentCommissionOutbox, s agentCommissionPaymentSnapshot) (string, error) {
	p := s.Policy
	if !s.Eligible || !p.CommissionEnabled || s.Payment.Environment != "live" {
		return "cash_ineligible", nil
	}
	if s.Payment.Currency != p.CommissionCurrency || s.Payment.CurrencyExponent != p.CommissionCurrencyExponent {
		return "cash_currency_not_eligible", nil
	}
	if s.Payment.PaidMinor <= 0 || s.Payment.PaidMinor > MaxAgentMoneyMinor || p.CommissionReferralCapMinor <= 0 || p.CommissionReferralCapMinor > MaxAgentMoneyMinor || p.CommissionMaturitySeconds < 0 || p.CommissionMaturitySeconds > int64(10*365*24*time.Hour/time.Second) || p.CommissionReserveBPS < 0 || p.CommissionReserveBPS > 10000 {
		return "", ErrAgentCommissionInvalid
	}
	rate := p.CommissionUserRateBPS
	if s.Qualification == "agent" {
		rate = p.CommissionAgentRateBPS
	}
	if rate < 0 || rate > 10000 {
		return "", ErrAgentCommissionInvalid
	}
	account, err := lockAgentCommissionAccount(tx, s.InviterID, s.Payment.Currency, s.Payment.CurrencyExponent)
	if err != nil {
		return "", err
	}
	var existing int64
	if err := tx.Model(&ReferralCommission{}).Where("reference_type = ? AND reference_id = ?", ReferralReferenceTopUp, event.ReferenceID).Count(&existing).Error; err != nil {
		return "", err
	}
	if existing != 0 {
		return "legacy_already_awarded", nil
	}
	cap := AgentCommissionCap{UserID: s.InviterID, SourceUserID: s.UserID, Currency: s.Payment.Currency}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&cap).Error; err != nil {
		return "", err
	}
	if err := lockForUpdate(tx).Where("user_id = ? AND source_user_id = ? AND currency = ?", s.InviterID, s.UserID, s.Payment.Currency).First(&cap).Error; err != nil {
		return "", err
	}
	reward, err := agentMinorFraction(s.Payment.PaidMinor, rate, 10000)
	if err != nil {
		return "", err
	}
	reward = min(reward, max(int64(0), p.CommissionReferralCapMinor-cap.EarnedMinor))
	if reward == 0 {
		return "cash_zero_or_capped", nil
	}
	if account.EarnedMinor > MaxAgentMoneyMinor-reward {
		return "", ErrAgentCommissionInvariant
	}
	policyJSON, err := common.Marshal(p)
	if err != nil {
		return "", err
	}
	award := AgentCommissionAward{EventID: event.ID, UserID: s.InviterID, SourceUserID: s.UserID, ReferenceID: event.ReferenceID, Provider: s.Payment.Provider, PaymentReference: s.Payment.ExternalReference, Currency: s.Payment.Currency, CurrencyExponent: s.Payment.CurrencyExponent, PaidMinor: s.Payment.PaidMinor, RewardMinor: reward, PendingMinor: reward, RateBPS: rate, CapMinor: p.CommissionReferralCapMinor, ReserveBPS: p.CommissionReserveBPS, PolicyVersion: p.ID, ProfileVersion: s.ProfileVersion, Qualification: s.Qualification, PolicySnapshot: string(policyJSON), MaturesAt: s.PaidAt + p.CommissionMaturitySeconds, Version: 1}
	if err := tx.Create(&award).Error; err != nil {
		return "", err
	}
	before := *account
	account.PendingMinor += reward
	account.EarnedMinor += reward
	cap.EarnedMinor += reward
	if err := tx.Save(&cap).Error; err != nil {
		return "", err
	}
	if err := saveAgentCommissionAccount(tx, before, account, "award", "event", fmt.Sprint(event.ID), "", 0); err != nil {
		return "", err
	}
	return "cash_awarded", nil
}
