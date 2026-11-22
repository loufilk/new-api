package model

import (
	"fmt"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// retiredUnusedPromotions recognizes only a complete, never-used, non-cash
// promotional allocation reclaimed by the original version's accounting rules.
// It does not migrate, forgive, or modify any obligation. Purchased funds,
// partial use, extra transitions, missing evidence and unknown states still block.
func retiredUnusedPromotions(tx *gorm.DB, tables []string) ([]int64, error) {
	for _, table := range []string{"agent_fund_batches", "agent_fund_slices", "agent_fund_ledgers", "agent_redemption_accounts", "agent_redemption_ledgers", "agent_redemption_operations", "agent_profiles", "users"} {
		if !slices.Contains(tables, table) {
			return nil, nil
		}
	}
	var batches []struct {
		ID            int64
		AgentUserID   int
		OriginalQuota int64
		SourceKey     string
	}
	err := tx.Table("agent_fund_batches").Select("id, agent_user_id, original_quota, source_key").
		Where("id > 0 AND agent_user_id > 0 AND source_kind = ? AND original_paid_minor = ? AND original_quota > 0 AND original_quota <= ? AND payment_receipt_id = 0 AND purchase_order_id = 0 AND cash_refundable = ? AND currency = ? AND currency_exponent = 0 AND wallet_account = ? AND source_key <> ?", "promotion", "0", common.MaxWalletQuota, false, "", "paid", "").Find(&batches).Error
	if err != nil {
		return nil, err
	}
	var retired []int64
	for _, batch := range batches {
		// Exactly one complete source, with precisely its creation and reclaim
		// entries, makes the conservation proof independent of retired workers.
		checks := []*gorm.DB{
			tx.Table("agent_fund_batches").Where("agent_user_id = ?", batch.AgentUserID),
			tx.Table("agent_fund_slices").Where("batch_id = ?", batch.ID),
			tx.Table("agent_fund_slices").Where("batch_id = ? AND agent_user_id = ? AND start_quota = 0 AND quota = ? AND state = ? AND redemption_id = 0 AND refund_id = 0 AND reclassification_id = 0", batch.ID, batch.AgentUserID, batch.OriginalQuota, "reclaimed"),
			tx.Table("agent_redemption_accounts").Where("agent_user_id = ? AND available_quota = 0 AND reserved_quota = 0 AND spent_quota = 0 AND allocated_quota = 0", batch.AgentUserID),
			tx.Table("agent_profiles").Where("user_id = ? AND status = ? AND sales_frozen = ? AND settlement_frozen = ? AND version > 0", batch.AgentUserID, "exited", true, true),
			tx.Table("users").Where("id = ? AND role IN ?", batch.AgentUserID, []int{common.RoleCommonUser, 5}),
		}
		valid := true
		for _, query := range checks {
			var count int64
			if err := query.Count(&count).Error; err != nil {
				return nil, err
			}
			if count != 1 {
				valid = false
			}
		}
		if !valid {
			continue
		}
		var entries []struct {
			OperationKey string
			Delta        int64
		}
		if err := tx.Table("agent_redemption_ledgers").Select("operation_key, delta").Where("agent_user_id = ?", batch.AgentUserID).Find(&entries).Error; err != nil {
			return nil, err
		}
		if len(entries) != 2 {
			continue
		}
		var fundCount int64
		if err := tx.Table("agent_fund_ledgers").Where("batch_id = ?", batch.ID).Count(&fundCount).Error; err != nil {
			return nil, err
		}
		if fundCount != 2 {
			continue
		}
		created, reclaimed := false, false
		for _, entry := range entries {
			if entry.OperationKey == "" || (entry.Delta != batch.OriginalQuota && entry.Delta != -batch.OriginalQuota) {
				valid = false
				break
			}
			after, from, to := int64(0), "available", "reclaimed"
			eventKey := fmt.Sprintf("%s:%d:0", entry.OperationKey, batch.ID)
			if entry.Delta > 0 {
				after, from, to = batch.OriginalQuota, "", "available"
				created = entry.OperationKey == batch.SourceKey
				eventKey = fmt.Sprintf("%s:create:%d:0", entry.OperationKey, batch.ID)
			} else {
				reclaimed = true
			}
			checks := []*gorm.DB{
				tx.Table("agent_redemption_ledgers").Where("operation_key = ? AND agent_user_id = ? AND kind = ? AND delta = ? AND allocated_delta = ? AND reserved_delta = 0 AND spent_delta = 0 AND available_after = ? AND allocated_after = ? AND reserved_after = 0 AND spent_after = 0 AND redemption_id = 0 AND reason <> ? AND request_id <> ?", entry.OperationKey, batch.AgentUserID, "allocation", entry.Delta, entry.Delta, after, after, "", ""),
				tx.Table("agent_redemption_operations").Where("operation_key = ? AND agent_user_id = ? AND kind = ? AND signature <> ? AND result <> ?", entry.OperationKey, batch.AgentUserID, "allocation", "", ""),
				tx.Table("agent_fund_ledgers").Where("event_key = ? AND batch_id = ? AND agent_user_id = ? AND start_quota = 0 AND quota = ? AND cost_minor = ? AND from_state = ? AND to_state = ? AND redemption_id = 0 AND refund_id = 0 AND reclassification_id = 0 AND currency = ? AND currency_exponent = 0", eventKey, batch.ID, batch.AgentUserID, batch.OriginalQuota, "0", from, to, ""),
			}
			for _, query := range checks {
				var count int64
				if err := query.Count(&count).Error; err != nil {
					return nil, err
				}
				valid = valid && count == 1
			}
		}
		if valid && created && reclaimed {
			retired = append(retired, batch.ID)
		}
	}
	return retired, nil
}
