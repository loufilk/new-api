package model

import (
	"context"
	"database/sql"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type AgentCommissionSummary struct {
	Accounts       []AgentCommissionAccount `json:"accounts"`
	LegacyAffQuota int                      `json:"legacy_aff_quota"`
	AllowedActions []string                 `json:"allowed_actions"`
	DisabledReason string                   `json:"disabled_reason"`
}

func GetAgentCommissionSummary(identity AuthSessionIdentity, ownerID int) (*AgentCommissionSummary, error) {
	if ownerID <= 0 {
		ownerID = identity.UserID
	}
	result := AgentCommissionSummary{Accounts: []AgentCommissionAccount{}, AllowedActions: []string{}}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommissionReaderTx(tx, identity, ownerID, false); err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", ownerID).Order("currency").Find(&result.Accounts).Error; err != nil {
			return err
		}
		var user User
		if err := tx.Select("id", "aff_quota").First(&user, ownerID).Error; err != nil {
			return err
		}
		result.LegacyAffQuota = user.AffQuota
		policy, err := ReadCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		profile, err := readAgentProfile(tx, ownerID, false)
		if err != nil {
			return err
		}
		if profile != nil && !profile.Allows(AgentCapabilitySettlement) {
			result.DisabledReason = "Commission settlement is frozen"
			return nil
		}
		result.AllowedActions = append(result.AllowedActions, "recipient.change")
		if !policy.RiskPolicyApproved {
			result.DisabledReason = "New commission financial policies are disabled"
			return nil
		}
		for _, account := range result.Accounts {
			if account.Currency != policy.CommissionCurrency || account.AvailableMinor <= 0 || account.DebtMinor > 0 {
				continue
			}
			if policy.WithdrawalEnabled {
				result.AllowedActions = append(result.AllowedActions, "commission.withdraw.request")
			}
			if policy.CommissionConversionEnabled {
				result.AllowedActions = append(result.AllowedActions, "commission.convert.wallet")
				if profile != nil && profile.Allows(AgentCapabilitySales) {
					result.AllowedActions = append(result.AllowedActions, "commission.convert.procurement")
				}
			}
		}
		return nil
	})
	return &result, err
}

// MatureAgentCommissionAccounts is a deterministic DB worker, not a payout.
// Risk-reserved shares remain reserved after maturity until separately reviewed.
func MatureAgentCommissionAccounts(ctx context.Context, limit int) (int, error) {
	limit = min(max(limit, 1), 200)
	var rows []AgentCommissionAward
	if err := DB.WithContext(ctx).Where("matured = ? AND matures_at <= ?", false, common.GetTimestamp()).Order("id").Limit(limit).Find(&rows).Error; err != nil {
		return 0, err
	}
	count := 0
	for _, row := range rows {
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			account, err := lockAgentCommissionAccount(tx, row.UserID, row.Currency, row.CurrencyExponent)
			if err != nil {
				return err
			}
			return matureAgentCommissionAccountTx(tx, account, common.GetTimestamp())
		})
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func ListAgentCommissionRecords(identity AuthSessionIdentity, kind string, ownerID, page, size int, requestID string, admin bool) (any, int64, error) {
	page = min(max(page, 1), 1_000_000)
	size = min(max(size, 1), 100)
	if !admin {
		ownerID = identity.UserID
	}
	var total int64
	var result any
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommissionReaderTx(tx, identity, ownerID, admin); err != nil {
			return err
		}
		query := tx
		if ownerID > 0 {
			query = query.Where("user_id = ?", ownerID)
		}
		if requestID != "" {
			if validateAgentRequestID(requestID) != nil {
				return ErrAgentCommissionInvalid
			}
			query = query.Where("request_id = ?", requestID)
		}
		var target any
		order := "id DESC"
		switch kind {
		case "awards":
			if requestID != "" {
				return ErrAgentCommissionInvalid
			}
			target = &[]AgentCommissionAward{}
		case "ledger":
			target = &[]AgentCommissionLedger{}
		case "withdrawals":
			target = &[]AgentCommissionWithdrawal{}
		case "conversions":
			target = &[]AgentCommissionConversion{}
		case "recipients", "recipients_current":
			if kind == "recipients_current" {
				query = query.Where("NOT EXISTS (SELECT 1 FROM agent_commission_recipients newer WHERE newer.id = agent_commission_recipients.id AND newer.version > agent_commission_recipients.version)")
			}
			target = &[]AgentCommissionRecipient{}
			order = "created_at DESC, version DESC"
		case "debts":
			if requestID != "" {
				return ErrAgentCommissionInvalid
			}
			target = &[]AgentCommissionDebt{}
		case "outbox":
			if !admin {
				return ErrAgentProfilePermission
			}
			query = tx
			target = &[]AgentCommissionOutbox{}
		case "refunds":
			if !admin {
				return ErrAgentProfilePermission
			}
			query = tx
			target = &[]AgentCommissionRefundFact{}
		default:
			return ErrAgentCommissionInvalid
		}
		if err := query.Model(target).Count(&total).Error; err != nil {
			return err
		}
		if err := query.Order(order).Offset((page - 1) * size).Limit(size).Find(target).Error; err != nil {
			return err
		}
		result = target
		return nil
	})
	return result, total, err
}

func GetAgentCommissionWithdrawal(identity AuthSessionIdentity, id int64) (*AgentCommissionWithdrawal, error) {
	var result AgentCommissionWithdrawal
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&result, id).Error; err != nil {
			return err
		}
		if err := requireAgentCommissionReaderTx(tx, identity, result.UserID, false); err != nil {
			return err
		}
		result.AllowedActions = []string{}
		var actor User
		if err := tx.Select("id", "role").First(&actor, identity.UserID).Error; err != nil {
			return err
		}
		if (actor.Role != common.RoleAdminUser && actor.Role != common.RoleRootUser) || actor.Id == result.RequestedBy || actor.Id == result.UserID {
			return nil
		}
		profile, err := readAgentProfile(tx, result.UserID, false)
		if err != nil {
			return err
		}
		var owner User
		if err := tx.Select("id", "status").First(&owner, result.UserID).Error; err != nil {
			return err
		}
		settlementAllowed := owner.Status == common.UserStatusEnabled && (profile == nil || profile.Allows(AgentCapabilitySettlement))
		var account AgentCommissionAccount
		if err := tx.Where("user_id = ? AND currency = ?", result.UserID, result.Currency).First(&account).Error; err != nil {
			return err
		}
		policy, err := ReadCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		switch result.Status {
		case "applied":
			result.AllowedActions = []string{"commission.withdraw.reject"}
			if settlementAllowed && account.DebtMinor == 0 {
				result.AllowedActions = append(result.AllowedActions, "commission.withdraw.approve")
			}
		case "approved":
			if settlementAllowed && account.DebtMinor == 0 && policy.WithdrawalEnabled && policy.RiskPolicyApproved {
				result.AllowedActions = []string{"commission.withdraw.submit"}
			}
		case "paying", "unknown":
			result.AllowedActions = []string{"commission.withdraw.resolve", "commission.withdraw.instructions"}
		}
		return nil
	})
	return &result, err
}

type AgentCommissionReconciliation struct {
	PendingOutbox       int64   `json:"pending_outbox"`
	FailedOutbox        int64   `json:"failed_outbox"`
	UnknownWithdrawals  int64   `json:"unknown_withdrawals"`
	PayingWithdrawals   int64   `json:"paying_withdrawals"`
	InvariantViolations []int64 `json:"invariant_violations"`
	AccountsChecked     int     `json:"accounts_checked"`
	NextAccountID       int64   `json:"next_account_id"`
}

func ReconcileAgentCommissions(identity AuthSessionIdentity, afterID int64) (*AgentCommissionReconciliation, error) {
	result := AgentCommissionReconciliation{InvariantViolations: []int64{}}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommissionReaderTx(tx, identity, 0, true); err != nil {
			return err
		}
		for _, item := range []struct {
			table  any
			status string
			count  *int64
		}{{&AgentCommissionOutbox{}, "pending", &result.PendingOutbox}, {&AgentCommissionOutbox{}, "failed", &result.FailedOutbox}, {&AgentCommissionWithdrawal{}, "unknown", &result.UnknownWithdrawals}, {&AgentCommissionWithdrawal{}, "paying", &result.PayingWithdrawals}} {
			if err := tx.Model(item.table).Where("status = ?", item.status).Count(item.count).Error; err != nil {
				return err
			}
		}
		var accounts []AgentCommissionAccount
		if err := tx.Where("id > ?", max(afterID, 0)).Order("id").Limit(100).Find(&accounts).Error; err != nil {
			return err
		}
		for _, account := range accounts {
			result.AccountsChecked++
			result.NextAccountID = account.ID
			if validateAgentCommissionAccount(&account) != nil {
				result.InvariantViolations = append(result.InvariantViolations, account.ID)
				continue
			}
			var lots struct {
				Pending   int64
				Available int64
				Reserved  int64
				Held      int64
			}
			if err := tx.Model(&AgentCommissionAward{}).Where("user_id = ? AND currency = ?", account.UserID, account.Currency).Select("COALESCE(SUM(pending_minor),0) AS pending, COALESCE(SUM(available_minor),0) AS available, COALESCE(SUM(reserved_minor),0) AS reserved, COALESCE(SUM(held_minor),0) AS held").Scan(&lots).Error; err != nil {
				return err
			}
			var debt int64
			if err := tx.Model(&AgentCommissionDebt{}).Where("user_id = ? AND currency = ?", account.UserID, account.Currency).Select("COALESCE(SUM(amount_minor - recovered_minor),0)").Scan(&debt).Error; err != nil {
				return err
			}
			if lots.Pending != account.PendingMinor || lots.Available != account.AvailableMinor || lots.Reserved != account.ReservedMinor || lots.Held != account.HeldMinor || debt != account.DebtMinor {
				result.InvariantViolations = append(result.InvariantViolations, account.ID)
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return &result, err
}
