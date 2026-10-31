package model

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// MaxRedemptionDeleteBatchSize bounds both selected and invalid-code cleanup.
// Each request commits at most this many code transitions and budget refunds.
const MaxRedemptionDeleteBatchSize = 1000

var (
	ErrRedemptionUsedImmutable  = errors.New("Used redemption codes cannot be changed or re-enabled")
	ErrRedemptionInvalidStatus  = errors.New("Invalid redemption code status")
	ErrRedemptionInvalidRequest = errors.New("Invalid redemption code request")
)

type RedemptionCreateRequest struct {
	Name        string `json:"name"`
	Quota       int    `json:"quota"`
	Count       int    `json:"count"`
	ExpiredTime int64  `json:"expired_time"`
	RequestID   string `json:"request_id"`
}

type RedemptionUpdateRequest struct {
	RequestID   string `json:"request_id"`
	Version     int64  `json:"version"`
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Quota       int    `json:"quota"`
	Status      int    `json:"status"`
	ExpiredTime int64  `json:"expired_time"`
}

func validateRedemptionValue(name string, quota int, expiredTime int64) error {
	if utf8.RuneCountInString(strings.TrimSpace(name)) == 0 || utf8.RuneCountInString(name) > 20 || quota <= 0 || expiredTime < 0 {
		return ErrRedemptionInvalidRequest
	}
	return common.ValidateWalletQuota(quota)
}

func GetRedemptionsScoped(scope RedemptionScope, agentID int, keyword, status string, start, count int) ([]*Redemption, int64, error) {
	start, count = min(max(start, 0), 100_000_000), min(max(count, 1), 100)
	if !scope.valid() {
		return nil, 0, ErrAgentRedemptionPermission
	}
	query := scope.query(DB.Model(&Redemption{}))
	if scope.IsAdmin() && agentID > 0 {
		query = query.Where("budget_agent_id = ?", agentID)
	}
	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("(id = ? OR name LIKE ?)", id, keyword+"%")
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}
	now := common.GetTimestamp()
	switch status {
	case "expired":
		query = query.Where("status = ? AND expired_time != 0 AND expired_time < ?", common.RedemptionCodeStatusEnabled, now)
	case strconv.Itoa(common.RedemptionCodeStatusEnabled):
		query = query.Where("status = ? AND (expired_time = 0 OR expired_time >= ?)", common.RedemptionCodeStatusEnabled, now)
	case strconv.Itoa(common.RedemptionCodeStatusDisabled):
		query = query.Where("status = ?", common.RedemptionCodeStatusDisabled)
	case strconv.Itoa(common.RedemptionCodeStatusUsed):
		query = query.Where("status = ?", common.RedemptionCodeStatusUsed)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]*Redemption, 0)
	if err := query.Order("id DESC").Offset(start).Limit(count).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func GetRedemptionScoped(scope RedemptionScope, id int) (*Redemption, error) {
	if !scope.valid() {
		return nil, ErrAgentRedemptionPermission
	}
	if id <= 0 {
		return nil, ErrRedemptionInvalidRequest
	}
	var redemption Redemption
	err := scope.query(DB).Where("id = ?", id).First(&redemption).Error
	return &redemption, err
}

// CreateRedemptionsScoped reserves the entire batch and inserts every code in
// one transaction. Request signatures never depend on newly generated secrets.
func CreateRedemptionsScoped(scope RedemptionScope, request RedemptionCreateRequest) ([]string, error) {
	if !scope.valid() {
		return nil, ErrAgentRedemptionPermission
	}
	if err := validateRedemptionValue(request.Name, request.Quota, request.ExpiredTime); err != nil {
		return nil, err
	}
	if request.Count < 1 || request.Count > 100 || request.Quota > common.MaxWalletQuota/request.Count {
		return nil, ErrRedemptionInvalidRequest
	}
	budgetAgentID := 0
	if !scope.IsAdmin() {
		budgetAgentID = scope.UserID
	}
	if request.RequestID == "" && scope.IsAdmin() {
		request.RequestID = common.GetUUID()
	}
	keys := make([]string, 0, request.Count)
	// Generated codes are credentials. Do not let SQL error/slow-query logging
	// include plaintext INSERT values or replay responses.
	db := DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)})
	err := db.Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		operation, replayed, err := claimAgentRedemptionOperation(tx, "create", request.RequestID, scope, budgetAgentID, request)
		if err != nil {
			return err
		}
		if replayed {
			if err := requireRedemptionActorTx(tx, scope, 0); err != nil {
				return err
			}
			var rows []Redemption
			// Include tombstones only for their original creator's exact replay.
			// A replay cannot create fresh value or leak another owner's codes.
			if err := tx.Unscoped().Where("creation_operation_id = ? AND user_id = ?", operation.ID, scope.UserID).Order("id ASC").Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) != request.Count {
				return ErrAgentQuotaInvariant
			}
			for _, row := range rows {
				keys = append(keys, row.Key)
			}
			return nil
		}
		var authorizationContext AgentTransactionContext
		if scope.Identity != nil {
			if scope.Authorization == nil || scope.Authorization.Identity() != *scope.Identity {
				return ErrAgentTransactionProof
			}
			authorizationContext, err = buildAgentLegacyCreateContextTx(tx, policy, scope, request)
			if err != nil {
				return err
			}
		}

		if request.ExpiredTime != 0 && request.ExpiredTime < common.GetTimestamp() {
			return ErrRedemptionInvalidRequest
		}
		if budgetAgentID != 0 {
			account, err := lockAgentRedemptionAccount(tx, budgetAgentID, true)
			if err != nil {
				return err
			}
			if err := ensureAgentFundSourcesTx(tx, account); err != nil {
				return err
			}
			total := request.Quota * request.Count // bounded before multiplication
			if err := applyAgentQuotaChangeTx(tx, account, AgentRedemptionLedger{OperationKey: operation.OperationKey, ActorUserID: scope.UserID, Kind: "reserve", Delta: -total, ReservedDelta: total, RequestID: request.RequestID, Reason: "Redemption code batch created"}); err != nil {
				return err
			}
		}
		rows := make([]Redemption, 0, request.Count)
		for range request.Count {
			key := common.GetUUID()
			rows = append(rows, Redemption{UserId: scope.UserID, Name: request.Name, Key: key, Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(), Quota: request.Quota, ExpiredTime: request.ExpiredTime, BudgetAgentID: budgetAgentID, BudgetReserved: budgetAgentID != 0, BudgetVersion: 1, CreationOperationID: operation.ID})
			keys = append(keys, key)
		}
		if err := tx.Create(&rows).Error; err != nil {
			return err
		}
		if budgetAgentID > 0 {
			for i := range rows {
				if err := reserveRedemptionFundSourcesTx(tx, &rows[i], fmt.Sprintf("%s:%d", operation.OperationKey, rows[i].Id)); err != nil {
					return err
				}
			}
		}
		if err := requireRedemptionActorTx(tx, scope, budgetAgentID); err != nil {
			return err
		}

		if scope.Identity != nil {
			if err := ConsumeAgentTransactionAuthorizationTx(tx, *scope.Authorization, authorizationContext); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// updateRedemptionBudgetTx transfers a single code's issuance reservation. The
// code lock is held first, followed by its account lock. BudgetVersion provides
// a stable, non-secret event key for each durable state transition.
func updateRedemptionBudgetTx(tx *gorm.DB, redemption *Redemption, actorID, desiredReserve int, spend bool, kind string) error {
	if redemption.BudgetAgentID == 0 {
		return nil
	}
	oldReserve := 0
	if redemption.BudgetReserved {
		oldReserve = redemption.Quota
	}
	if oldReserve == desiredReserve && !spend {
		return nil
	}
	if spend && (oldReserve <= 0 || desiredReserve != 0) {
		return ErrAgentQuotaInvariant
	}
	account, err := lockAgentRedemptionAccount(tx, redemption.BudgetAgentID, false)
	if err != nil {
		return err
	}
	if err := ensureAgentFundSourcesTx(tx, account); err != nil {
		return err
	}
	redemption.BudgetVersion++
	entry := AgentRedemptionLedger{OperationKey: fmt.Sprintf("redemption:%d:%d", redemption.Id, redemption.BudgetVersion), ActorUserID: actorID, RedemptionID: redemption.Id, Kind: kind, Reason: "Redemption code " + kind}
	if spend {
		entry.ReservedDelta, entry.SpentDelta = -oldReserve, oldReserve
	} else {
		entry.Delta, entry.ReservedDelta = oldReserve-desiredReserve, desiredReserve-oldReserve
	}
	if spend {
		actual, _, err := transitionAgentFundSlicesTx(tx, redemption.BudgetAgentID, redemption.Id, 0, AgentSliceReserved, AgentSliceSpent, entry.OperationKey)
		if err != nil {
			return err
		}
		if actual != oldReserve {
			return ErrAgentFundInvariant
		}
	} else {
		if oldReserve > 0 {
			actual, _, err := transitionAgentFundSlicesTx(tx, redemption.BudgetAgentID, redemption.Id, 0, AgentSliceReserved, AgentSliceAvailable, entry.OperationKey+":release")
			if err != nil {
				return err
			}
			if actual != oldReserve {
				return ErrAgentFundInvariant
			}
		}
		if desiredReserve > 0 {
			oldQuota := redemption.Quota
			redemption.Quota = desiredReserve
			err := reserveRedemptionFundSourcesTx(tx, redemption, entry.OperationKey+":reserve")
			redemption.Quota = oldQuota
			if err != nil {
				return err
			}
		}
	}
	if err := applyAgentQuotaChangeTx(tx, account, entry); err != nil {
		return err
	}
	redemption.BudgetReserved = desiredReserve != 0
	return nil
}

func UpdateRedemptionScoped(scope RedemptionScope, request RedemptionUpdateRequest, statusOnly bool) (*Redemption, error) {
	if !scope.valid() {
		return nil, ErrAgentRedemptionPermission
	}
	if request.ID <= 0 {
		return nil, ErrRedemptionInvalidRequest
	}
	if statusOnly {
		if request.Status != common.RedemptionCodeStatusEnabled && request.Status != common.RedemptionCodeStatusDisabled {
			return nil, ErrRedemptionInvalidStatus
		}
	} else if err := validateRedemptionValue(request.Name, request.Quota, request.ExpiredTime); err != nil {
		return nil, err
	}
	var redemption Redemption
	err := DB.Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		var operation *AgentRedemptionOperation
		replay := false
		if scope.Identity != nil {
			operation, replay, err = claimAgentRedemptionOperation(tx, "legacy_update", request.RequestID, scope, scope.UserID, AgentLegacyRedemptionUpdateRequest{RedemptionUpdateRequest: request, StatusOnly: statusOnly})
			if err != nil {
				return err
			}
		}

		if err := lockForUpdate(scope.query(tx)).Where("id = ?", request.ID).First(&redemption).Error; err != nil {
			return err
		}
		if replay {
			return requireRedemptionActorTx(tx, scope, 0)
		}

		var authorizationContext AgentTransactionContext
		if scope.Identity != nil {
			if scope.Authorization == nil || scope.Authorization.Identity() != *scope.Identity {
				return ErrAgentTransactionProof
			}
			authorizationContext, err = buildAgentLegacyUpdateContextTx(tx, policy, scope, &redemption, request, statusOnly)
			if err != nil {
				return err
			}
		}

		if redemption.CustomerUserID > 0 || redemption.DeliveryID > 0 || redemption.SourceProtected {
			return ErrAgentDeliveryProtected
		}
		// Fail closed for unknown legacy states, and check durable use markers
		// to prevent enabled-after-use resurrection of a redeemed code.
		if (redemption.Status != common.RedemptionCodeStatusEnabled && redemption.Status != common.RedemptionCodeStatusDisabled) || redemption.UsedUserId != 0 || redemption.RedeemedTime != 0 {
			return ErrRedemptionUsedImmutable
		}
		status, quota, expired := redemption.Status, redemption.Quota, redemption.ExpiredTime
		if statusOnly {
			status = request.Status
		} else {
			quota, expired = request.Quota, request.ExpiredTime
		}
		if status == common.RedemptionCodeStatusEnabled && expired != 0 && expired < common.GetTimestamp() {
			return ErrRedemptionInvalidRequest
		}
		desiredReserve := 0
		if status == common.RedemptionCodeStatusEnabled {
			desiredReserve = quota
		}
		// Lock account before locking agent user even on a no-value status edit.
		// Account->user ordering is shared by issuance and wallet settlement.
		if redemption.BudgetAgentID != 0 {
			if _, err := lockAgentRedemptionAccount(tx, redemption.BudgetAgentID, false); err != nil {
				return err
			}
		}
		activeAgentID := 0
		if status == common.RedemptionCodeStatusEnabled {
			activeAgentID = redemption.BudgetAgentID
		}
		kind := "edit"
		if statusOnly {
			if status == common.RedemptionCodeStatusEnabled {
				kind = "enable"
			} else {
				kind = "disable"
			}
		}
		if err := updateRedemptionBudgetTx(tx, &redemption, scope.UserID, desiredReserve, false, kind); err != nil {
			return err
		}
		if err := requireRedemptionActorTx(tx, scope, activeAgentID); err != nil {
			return err
		}

		if scope.Identity != nil {
			if err := ConsumeAgentTransactionAuthorizationTx(tx, *scope.Authorization, authorizationContext); err != nil {
				return err
			}
		}

		redemption.Status, redemption.Quota, redemption.ExpiredTime = status, quota, expired
		if !statusOnly {
			redemption.Name = request.Name
		}
		if err := tx.Model(&redemption).Select("name", "status", "quota", "expired_time", "budget_reserved", "budget_version").Updates(&redemption).Error; err != nil {
			return err
		}
		if operation != nil {
			return storeAgentOperationResultTx(tx, operation, "redemption", redemption.Id, "updated")
		}
		return nil
	})
	return &redemption, err
}

func DeleteRedemptionsScoped(scope RedemptionScope, ids []int, invalidOnly bool, agentFilter ...int) (int64, error) {
	if !scope.valid() {
		return 0, ErrAgentRedemptionPermission
	}
	if !invalidOnly && (len(ids) == 0 || len(ids) > MaxRedemptionDeleteBatchSize) {
		return 0, ErrRedemptionInvalidRequest
	}
	for _, id := range ids {
		if id <= 0 {
			return 0, ErrRedemptionInvalidRequest
		}
	}
	var deleted int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		var operation *AgentRedemptionOperation
		filter := 0
		if len(agentFilter) > 0 {
			filter = agentFilter[0]
		}
		if scope.Identity != nil {
			var replay bool
			operation, replay, err = claimAgentRedemptionOperation(tx, "legacy_delete", scope.RequestID, scope, scope.UserID, AgentLegacyRedemptionDeleteRequest{RequestID: scope.RequestID, IDs: ids, InvalidOnly: invalidOnly, AgentUserID: filter})
			if err != nil {
				return err
			}
			if replay {
				if err := requireRedemptionActorTx(tx, scope, 0); err != nil {
					return err
				}
				deleted, err = strconv.ParseInt(operation.Result, 10, 64)
				return err
			}
		}

		query := scope.query(tx.Model(&Redemption{}))
		if scope.IsAdmin() && len(agentFilter) > 0 && agentFilter[0] > 0 {
			query = query.Where("budget_agent_id = ?", agentFilter[0])
		}
		if invalidOnly {
			query = query.Where("customer_user_id = 0 AND delivery_id = 0 AND source_protected = false").Where("(status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?))", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, common.GetTimestamp()).Limit(MaxRedemptionDeleteBatchSize)
		} else {
			query = query.Where("id IN ?", ids)
		}
		var rows []Redemption
		if err := lockForUpdate(query).Order("id ASC").Find(&rows).Error; err != nil {
			return err
		}
		var authorizationContext AgentTransactionContext
		if scope.Identity != nil {
			if scope.Authorization == nil || scope.Authorization.Identity() != *scope.Identity {
				return ErrAgentTransactionProof
			}
			filter := 0
			if len(agentFilter) > 0 {
				filter = agentFilter[0]
			}
			authorizationContext, err = buildAgentLegacyDeleteContextTx(tx, policy, scope, rows, AgentLegacyRedemptionDeleteRequest{RequestID: scope.RequestID, IDs: ids, InvalidOnly: invalidOnly, AgentUserID: filter})
			if err != nil {
				return err
			}
		}

		accountIDs := make([]int, 0)
		for _, row := range rows {
			if row.BudgetAgentID > 0 {
				accountIDs = append(accountIDs, row.BudgetAgentID)
			}
		}
		slices.Sort(accountIDs)
		for _, accountID := range slices.Compact(accountIDs) {
			if _, err := lockAgentRedemptionAccount(tx, accountID, false); err != nil {
				return err
			}
		}
		// One bounded transaction makes this batch all-or-none. Additional invalid
		// rows require another request, never an unbounded loop in this transaction.
		// Foreign IDs are never returned; only matching own rows are affected.
		for i := range rows {
			row := &rows[i]
			if row.CustomerUserID > 0 || row.DeliveryID > 0 || row.SourceProtected {
				if invalidOnly {
					continue
				}
				return ErrAgentDeliveryProtected
			}
			if row.Status == common.RedemptionCodeStatusUsed || row.UsedUserId != 0 || row.RedeemedTime != 0 {
				if row.BudgetReserved {
					return ErrAgentQuotaInvariant
				}
			} else if err := updateRedemptionBudgetTx(tx, row, scope.UserID, 0, false, "delete"); err != nil {
				return err
			}
			if err := tx.Model(row).Select("budget_reserved", "budget_version").Updates(row).Error; err != nil {
				return err
			}
			result := tx.Delete(row)
			if result.Error != nil {
				return result.Error
			}
			deleted += result.RowsAffected
		}
		if err := requireRedemptionActorTx(tx, scope, 0); err != nil {
			return err
		}

		if scope.Identity != nil {
			if err := ConsumeAgentTransactionAuthorizationTx(tx, *scope.Authorization, authorizationContext); err != nil {
				return err
			}
		}

		if operation != nil {
			return tx.Model(operation).Update("result", strconv.FormatInt(deleted, 10)).Error
		}

		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// ReleaseExpiredAgentRedemptions reclaims outstanding reservations in bounded
// batches. It leaves code status/expiry unchanged for an accurate expired view.
// Per-code locking makes overlap with redeem, delete, disable and another worker
// idempotent. An expired code must reserve quota again before it can be extended.
func ReleaseExpiredAgentRedemptions(limit int) (int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var ids []int
	if err := DB.Model(&Redemption{}).Where("budget_agent_id > 0 AND budget_reserved = ? AND customer_user_id = 0 AND delivery_id = 0 AND source_protected = false AND expired_time != 0 AND expired_time < ?", true, common.GetTimestamp()).Order("id ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	var released int64
	for _, id := range ids {
		changed := false
		err := DB.Transaction(func(tx *gorm.DB) error {
			if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
				return err
			}
			var row Redemption
			if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			if row.CustomerUserID > 0 || row.DeliveryID > 0 || row.SourceProtected {
				return nil
			}
			if !row.BudgetReserved || row.ExpiredTime == 0 || row.ExpiredTime >= common.GetTimestamp() {
				return nil
			}
			if row.Status == common.RedemptionCodeStatusUsed || row.UsedUserId != 0 || row.RedeemedTime != 0 {
				return ErrAgentQuotaInvariant
			}
			if err := updateRedemptionBudgetTx(tx, &row, 0, 0, false, "expire"); err != nil {
				return err
			}
			changed = true
			return tx.Model(&row).Select("budget_reserved", "budget_version").Updates(&row).Error
		})
		if err != nil {
			return released, err
		}
		if changed {
			released++
		}
	}
	return released, nil
}
