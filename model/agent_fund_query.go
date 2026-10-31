package model

import (
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"strconv"

	"gorm.io/gorm"
)

type AgentFundBatchView struct {
	AgentFundBatch
	AvailableQuota     int `json:"available_quota"`
	ReservedQuota      int `json:"reserved_quota"`
	SpentQuota         int `json:"spent_quota"`
	RefundFrozenQuota  int `json:"refund_frozen_quota"`
	RefundedQuota      int `json:"refunded_quota"`
	ReclaimedQuota     int `json:"reclaimed_quota"`
	ReclassifiedQuota  int `json:"reclassified_quota"`
	ReclassFrozenQuota int `json:"reclass_frozen_quota"`
}

func GetAgentFundBatches(scope RedemptionScope, agentID, start, count int) ([]AgentFundBatchView, int64, error) {
	if !scope.valid() {
		return nil, 0, ErrAgentRedemptionPermission
	}
	if !scope.IsAdmin() {
		agentID = scope.UserID
	}
	query := DB.Model(&AgentFundBatch{})
	if agentID > 0 {
		query = query.Where("agent_user_id = ?", agentID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var batches []AgentFundBatch
	if err := query.Order("id DESC").Offset(max(0, start)).Limit(min(max(count, 1), 100)).Find(&batches).Error; err != nil {
		return nil, 0, err
	}
	views := make([]AgentFundBatchView, 0, len(batches))
	for _, batch := range batches {
		view := AgentFundBatchView{AgentFundBatch: batch}
		var slices []AgentFundSlice
		if err := DB.Where("batch_id = ?", batch.ID).Find(&slices).Error; err != nil {
			return nil, 0, err
		}
		for _, slice := range slices {
			switch slice.State {
			case AgentSliceAvailable:
				view.AvailableQuota += slice.Quota
			case AgentSliceReserved:
				view.ReservedQuota += slice.Quota
			case AgentSliceSpent:
				view.SpentQuota += slice.Quota
			case AgentSliceRefundFrozen:
				view.RefundFrozenQuota += slice.Quota
			case AgentSliceRefunded:
				view.RefundedQuota += slice.Quota
			case AgentSliceReclaimed:
				view.ReclaimedQuota += slice.Quota
			case AgentSliceReclassified:
				view.ReclassifiedQuota += slice.Quota
			case AgentSliceReclassFrozen:
				view.ReclassFrozenQuota += slice.Quota
			}
		}
		views = append(views, view)
	}
	return views, total, nil
}

func GetAgentFundLedger(scope RedemptionScope, agentID, start, count int) ([]AgentFundLedger, int64, error) {
	if !scope.valid() {
		return nil, 0, ErrAgentRedemptionPermission
	}
	if !scope.IsAdmin() {
		agentID = scope.UserID
	}
	query := DB.Model(&AgentFundLedger{})
	if agentID > 0 {
		query = query.Where("agent_user_id = ?", agentID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]AgentFundLedger, 0)
	err := query.Order("id DESC").Offset(max(0, start)).Limit(min(max(count, 1), 100)).Find(&rows).Error
	return rows, total, err
}

// ReconcileAgentFunds is observational: malformed balances, orphan liabilities,
// interval overlap and missing provenance are reported, never cleared or fixed.
type AgentFundReconciliationPage struct {
	Anomalies       []AgentFundAnomaly `json:"anomalies"`
	CheckedAgentIDs []int              `json:"checked_agent_ids"`
	NextCursor      int                `json:"next_cursor"`
	CheckedAt       int64              `json:"checked_at"`
}

var ErrAgentReconciliationIncomplete = errors.New("Reconciliation has more pages; continue with the next cursor")

func ReconcileAgentFunds(scope RedemptionScope, agentID int) ([]AgentFundAnomaly, error) {
	page, err := ReconcileAgentFundsPage(scope, agentID, 0, 1000)
	if err != nil {
		return nil, err
	}
	if page.NextCursor != 0 {
		return page.Anomalies, ErrAgentReconciliationIncomplete
	}
	return page.Anomalies, nil
}

func ReconcileAgentFundsPage(scope RedemptionScope, agentID, afterAgentID, limit int) (*AgentFundReconciliationPage, error) {
	if !scope.valid() || !scope.IsAdmin() {
		return nil, ErrAgentRedemptionPermission
	}
	anomalies := make([]AgentFundAnomaly, 0)
	page := &AgentFundReconciliationPage{Anomalies: anomalies, CheckedAgentIDs: []int{}, CheckedAt: common.GetTimestamp()}
	limit = min(max(limit, 1), 1000)
	afterAgentID = max(afterAgentID, 0)
	err := DB.Transaction(func(tx *gorm.DB) error {
		// One stable view shares the writers' policy fence, preventing false mismatch
		// reports between an aggregate update and its transaction's source journal.
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		idsQuery := tx.Table("(SELECT agent_user_id FROM agent_redemption_accounts UNION SELECT agent_user_id FROM agent_fund_batches UNION SELECT agent_user_id FROM agent_fund_slices) AS fund_owners").Where("agent_user_id > ?", afterAgentID)
		if agentID > 0 {
			idsQuery = idsQuery.Where("agent_user_id = ?", agentID)
		}
		var ownerIDs []int
		if err := idsQuery.Order("agent_user_id ASC").Limit(limit+1).Pluck("agent_user_id", &ownerIDs).Error; err != nil {
			return err
		}
		if len(ownerIDs) > limit {
			ownerIDs = ownerIDs[:limit]
			page.NextCursor = ownerIDs[len(ownerIDs)-1]
		}
		page.CheckedAgentIDs = ownerIDs
		if len(ownerIDs) == 0 {
			return nil
		}
		var accounts []AgentRedemptionAccount
		if err := tx.Where("agent_user_id IN ?", ownerIDs).Order("agent_user_id ASC").Find(&accounts).Error; err != nil {
			return err
		}
		for _, account := range accounts {
			add := func(code, detail string) {
				anomalies = append(anomalies, AgentFundAnomaly{AgentUserID: account.AgentUserID, Code: code, Detail: detail})
			}
			if err := validateAgentAccount(&account); err != nil {
				add("invalid_aggregate", "Aggregate counters are negative, out of range, or inconsistent")
			}
			var users int64
			if err := tx.Model(&User{}).Where("id = ?", account.AgentUserID).Count(&users).Error; err != nil {
				return err
			}
			if users != 1 {
				add("orphan_account", "Funding account has no active user record")
			}
			var batches []AgentFundBatch
			if err := tx.Where("agent_user_id = ?", account.AgentUserID).Order("id").Find(&batches).Error; err != nil {
				return err
			}
			available, reserved, spent := 0, 0, 0
			for _, batch := range batches {
				var slices []AgentFundSlice
				if err := tx.Where("batch_id = ?", batch.ID).Order("start_quota").Find(&slices).Error; err != nil {
					return err
				}
				start := 0
				cost := int64(0)
				valid := true
				for _, slice := range slices {
					if slice.AgentUserID != batch.AgentUserID || slice.StartQuota != start || slice.Quota <= 0 || slice.Quota > batch.OriginalQuota-start {
						add("invalid_intervals", "Batch "+strconv.Itoa(batch.ID)+" contains missing, overlapping, or out-of-range intervals")
						valid = false
						break
					}
					value, err := AgentFundSliceCost(&batch, slice.StartQuota, slice.Quota)
					if err != nil {
						add("invalid_cost", "Batch "+strconv.Itoa(batch.ID)+" has invalid original cost")
						valid = false
						break
					}
					cost += value
					start += slice.Quota
					switch slice.State {
					case AgentSliceAvailable:
						available += slice.Quota
						if slice.RedemptionID != 0 || slice.RefundID != 0 {
							add("available_responsibility", "Available slice retains a customer or refund responsibility")
						}
					case AgentSliceReserved:
						reserved += slice.Quota
						var code Redemption
						if err := tx.Unscoped().First(&code, slice.RedemptionID).Error; err != nil {
							if err == gorm.ErrRecordNotFound {
								add("orphan_redemption", "Reserved slice has no redemption record")
							} else {
								return err
							}
						} else if !code.BudgetReserved || code.BudgetAgentID != account.AgentUserID || code.DeletedAt.Valid || code.UsedUserId != 0 || code.RedeemedTime != 0 {
							add("redemption_mismatch", "Reserved slice does not match an outstanding redemption responsibility")
						}
					case AgentSliceRefundFrozen:
						reserved += slice.Quota
						var refund AgentPurchaseRefund
						if err := tx.First(&refund, slice.RefundID).Error; err != nil {
							if err == gorm.ErrRecordNotFound {
								add("orphan_refund", "Frozen slice has no refund record")
							} else {
								return err
							}
						} else if refund.AgentUserID != account.AgentUserID || (refund.State != "requested" && refund.State != "approved" && refund.State != "payment_pending" && refund.State != "payment_unknown") {
							add("refund_mismatch", "Frozen slice does not match an in-flight refund")
						}
					case AgentSliceReclassFrozen:
						reserved += slice.Quota
						var record AgentFundReclassification
						if err := tx.First(&record, slice.ReclassificationID).Error; err != nil {
							if err == gorm.ErrRecordNotFound {
								add("orphan_reclassification", "Frozen historical source has no reclassification record")
							} else {
								return err
							}
						} else if record.State != "verified" || record.AgentUserID != account.AgentUserID {
							add("reclassification_mismatch", "Historical source does not match an in-flight independent review")
						}

					case AgentSliceSpent:
						spent += slice.Quota
					case AgentSliceRefunded, AgentSliceReclaimed, AgentSliceReclassified:
					default:
						add("invalid_slice_state", "Unknown slice state: "+slice.State)
					}
				}
				if valid && (start != batch.OriginalQuota || strconv.FormatInt(cost, 10) != batch.OriginalPaidMinor) {
					add("batch_conservation", "Batch "+strconv.Itoa(batch.ID)+" does not conserve original quota and cost")
				}
			}
			if available != account.AvailableQuota || reserved != account.ReservedQuota || spent != account.SpentQuota {
				add("source_aggregate_mismatch", fmt.Sprintf("Source counters available=%d reserved=%d spent=%d do not match the authoritative account", available, reserved, spent))
			}
			var codes []Redemption
			if err := tx.Unscoped().Where("budget_agent_id = ? AND budget_reserved = ?", account.AgentUserID, true).Find(&codes).Error; err != nil {
				return err
			}
			for _, code := range codes {
				var slices []AgentFundSlice
				if err := tx.Where("redemption_id = ? AND state = ?", code.Id, AgentSliceReserved).Find(&slices).Error; err != nil {
					return err
				}
				total := 0
				for _, slice := range slices {
					total += slice.Quota
				}
				if total != code.Quota {
					add("code_source_mismatch", "Outstanding code "+strconv.Itoa(code.Id)+" does not have exact source reservations")
				}
			}
		}
		// Funding batches without an aggregate must be visible too.
		var orphanBatches []AgentFundBatch
		queryBatches := tx.Where("agent_user_id IN ? AND agent_user_id NOT IN (?)", ownerIDs, tx.Model(&AgentRedemptionAccount{}).Select("agent_user_id"))
		if agentID > 0 {
			queryBatches = queryBatches.Where("agent_user_id = ?", agentID)
		}
		if err := queryBatches.Find(&orphanBatches).Error; err != nil {
			return err
		}
		for _, batch := range orphanBatches {
			anomalies = append(anomalies, AgentFundAnomaly{AgentUserID: batch.AgentUserID, Code: "orphan_batch", Detail: "Funding batch " + strconv.Itoa(batch.ID) + " has no authoritative account"})
		}
		var orphanSlices []AgentFundSlice
		if err := tx.Where("agent_user_id IN ? AND batch_id NOT IN (?)", ownerIDs, tx.Model(&AgentFundBatch{}).Select("id")).Find(&orphanSlices).Error; err != nil {
			return err
		}
		for _, slice := range orphanSlices {
			anomalies = append(anomalies, AgentFundAnomaly{AgentUserID: slice.AgentUserID, Code: "orphan_slice", Detail: "Funding slice " + strconv.Itoa(slice.ID) + " has no provenance batch"})
		}

		return nil
	})
	page.Anomalies = anomalies
	return page, err
}
