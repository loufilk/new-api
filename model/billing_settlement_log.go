package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
)

func GetReadyBillingSettlementLogs(ctx context.Context, limit int) ([]BillingFinalization, error) {
	if limit <= 0 || limit > 128 {
		return nil, errors.New("invalid settlement log page size")
	}
	var rows []BillingFinalization
	err := DB.WithContext(ctx).Where("state = ? AND settlement_log_retry_at > 0 AND settlement_log_retry_at <= ? AND settlement_recovery <> ''", BillingSettled, common.GetTimestamp()).Order("settlement_log_retry_at, request_id").Limit(limit).Find(&rows).Error
	return rows, err
}

// ReconcileBillingSettlementLog corrects only an existing, exact request log
// after authoritative settlement. It never inserts a log, guesses a wallet
// account split, reruns financial accounting or increments quota-data exports.
// Missing/conflicting logs stay visible in the independent projection backlog.
func ReconcileBillingSettlementLog(ctx context.Context, receipt BillingFinalization) error {
	var owner BillingFinalization
	if err := DB.WithContext(ctx).Where("request_id = ?", receipt.RequestID).First(&owner).Error; err != nil {
		return err
	}
	if !sameBillingIdentity(owner, receipt) || owner.State != BillingSettled || owner.ActualQuota != receipt.ActualQuota || owner.SettlementRecovery == "" || owner.SettlementRecovery != receipt.SettlementRecovery {
		return billingRefundReviewError("identity_mismatch", "settlement log owner mismatch")
	}
	recovery, err := DecodeBillingSettlementRecovery(owner)
	if err != nil {
		return err
	}
	if !recovery.LogExpected || owner.SettlementLogRetryAt == 0 {
		return nil
	}
	// Advance only the projection schedule. The financial state stays settled
	// even if the separately configured log database is unavailable.
	claim := DB.WithContext(ctx).Model(&BillingFinalization{}).Where("request_id = ? AND state = ? AND settlement_log_retry_at = ?", owner.RequestID, BillingSettled, owner.SettlementLogRetryAt).Update("settlement_log_retry_at", common.GetTimestamp()+30)
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected != 1 {
		return nil
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return errors.New("settled log correction requires manual reconciliation on ClickHouse")
	}
	var logs []Log
	if err := LOG_DB.WithContext(ctx).Where("request_id = ? AND user_id = ?", owner.RequestID, owner.UserID).Limit(2).Find(&logs).Error; err != nil {
		return err
	}
	if len(logs) != 1 {
		return errors.New("missing or ambiguous settled usage log")
	}
	log := logs[0]
	if log.TokenId != owner.TokenID || log.ChannelId != recovery.ChannelID {
		return errors.New("settled usage log identity mismatch")
	}
	if log.Type != LogTypeConsume || log.Quota != owner.ActualQuota {
		var other map[string]any
		if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
			return err
		}
		admin, _ := other[logOtherAdminInfoKey].(map[string]any)
		calculated, _ := admin["billing_calculated_quota"].(float64)
		if log.Type != LogTypeError || log.Quota != 0 || other["billing_status"] != "settlement_unconfirmed" || calculated != float64(owner.ActualQuota) {
			return errors.New("settled usage log evidence mismatch")
		}
		other["billing_status"] = "settled"
		delete(other, logOtherContentPartsKey)
		// Exports were not emitted for the original unconfirmed log. Retain an
		// explicit reconciliation marker instead of risking a duplicate aggregate.
		admin["billing_quota_data_reconciliation"] = "required"
		admin["billing_recovered"] = true
		encoded, err := common.Marshal(other)
		if err != nil {
			return err
		}
		result := LOG_DB.WithContext(ctx).Model(&Log{}).Where("id = ? AND type = ? AND quota = 0 AND other = ?", log.Id, LogTypeError, log.Other).Updates(map[string]any{"type": LogTypeConsume, "quota": owner.ActualQuota, "other": string(encoded), "content": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("settled usage log changed concurrently")
		}
	}
	return DB.WithContext(ctx).Model(&BillingFinalization{}).Where("request_id = ? AND state = ? AND actual_quota = ? AND settlement_recovery = ?", owner.RequestID, BillingSettled, owner.ActualQuota, owner.SettlementRecovery).Update("settlement_log_retry_at", 0).Error
}
