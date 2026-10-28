package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAgentRedemptionPermission  = errors.New("Agent redemption permission denied")
	ErrAgentRedemptionUnavailable = errors.New("Agent account must be active before issuing redemption codes")
	ErrAgentQuotaInvalid          = errors.New("Invalid agent quota request")
	ErrAgentQuotaInsufficient     = errors.New("Insufficient available agent redemption quota")
	ErrAgentQuotaConflict         = errors.New("Agent redemption request conflicts with an earlier operation")
	ErrAgentQuotaInvariant        = errors.New("Agent redemption quota invariant violated")
	ErrAgentLedgerImmutable       = errors.New("Agent redemption ledger entries are immutable")
)

// AgentRedemptionAccount is an issuance budget, not a user's spendable wallet.
// AllocatedQuota is net lifetime allocation, including already-spent issuance.
// It always equals AvailableQuota + ReservedQuota + SpentQuota.
type AgentRedemptionAccount struct {
	AgentUserID    int    `json:"agent_user_id" gorm:"primaryKey;autoIncrement:false"`
	Username       string `json:"username,omitempty" gorm:"->;-:migration"`
	DisplayName    string `json:"display_name,omitempty" gorm:"->;-:migration"`
	AvailableQuota int    `json:"available_quota" gorm:"type:bigint;not null;default:0"`
	ReservedQuota  int    `json:"reserved_quota" gorm:"type:bigint;not null;default:0"`
	SpentQuota     int    `json:"spent_quota" gorm:"type:bigint;not null;default:0"`
	AllocatedQuota int    `json:"allocated_quota" gorm:"type:bigint;not null;default:0"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;autoCreateTime"`
	UpdatedAt      int64  `json:"updated_at" gorm:"bigint;autoUpdateTime"`
}

// AgentRedemptionOperation owns a global request key per operation kind. Its
// signature binds actor, target, value, and intent, so replays cannot be retargeted.
// It never stores redemption plaintext; creation replies are reconstructed from
// the private rows associated with this operation.
type AgentRedemptionOperation struct {
	ID           int    `json:"id" gorm:"primaryKey"`
	OperationKey string `json:"-" gorm:"type:varchar(128);not null;uniqueIndex"`
	Signature    string `json:"-" gorm:"type:char(64);not null"`
	ActorUserID  int    `json:"actor_user_id" gorm:"not null"`
	AgentUserID  int    `json:"agent_user_id" gorm:"not null;index"`
	Kind         string `json:"kind" gorm:"type:varchar(24);not null"`
	Result       string `json:"-" gorm:"type:text"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint;autoCreateTime"`
}

// AgentRedemptionLedger is append-only. Delta is the change in available quota;
// the other deltas explain transfers between available/reserved/spent buckets.
// No redemption key, session token, or other credential belongs in this ledger.
type AgentRedemptionLedger struct {
	ID             int    `json:"id" gorm:"primaryKey"`
	OperationKey   string `json:"-" gorm:"type:varchar(128);not null;uniqueIndex"`
	AgentUserID    int    `json:"agent_user_id" gorm:"not null;index:idx_agent_redemption_ledger_user_id,priority:1"`
	ActorUserID    int    `json:"actor_user_id" gorm:"not null"`
	RedemptionID   int    `json:"redemption_id" gorm:"index"`
	Kind           string `json:"kind" gorm:"type:varchar(24);not null"`
	Delta          int    `json:"delta" gorm:"type:bigint;not null"`
	ReservedDelta  int    `json:"reserved_delta" gorm:"type:bigint;not null"`
	SpentDelta     int    `json:"spent_delta" gorm:"type:bigint;not null"`
	AllocatedDelta int    `json:"allocated_delta" gorm:"type:bigint;not null"`
	AvailableAfter int    `json:"available_quota_after" gorm:"type:bigint;not null"`
	ReservedAfter  int    `json:"reserved_quota_after" gorm:"type:bigint;not null"`
	SpentAfter     int    `json:"spent_quota_after" gorm:"type:bigint;not null"`
	AllocatedAfter int    `json:"allocated_quota_after" gorm:"type:bigint;not null"`
	RequestID      string `json:"request_id" gorm:"type:varchar(96)"`
	Reason         string `json:"reason" gorm:"type:varchar(255);not null"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;autoCreateTime;index:idx_agent_redemption_ledger_user_id,priority:2"`
}

func (*AgentRedemptionLedger) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentRedemptionLedger) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentQuotaAllocationRequest struct {
	AgentUserID int    `json:"agent_user_id"`
	Delta       int    `json:"delta"`
	Reason      string `json:"reason"`
	RequestID   string `json:"request_id"`
}

type AgentQuotaAllocationResult struct {
	Account  AgentRedemptionAccount `json:"account"`
	Replayed bool                   `json:"replayed"`
}

// RedemptionScope must be derived from authenticated server-side identity.
// Agent role is an exact role, not a minimum privilege threshold.
type RedemptionScope struct {
	UserID, Role int
	system       bool
}

func (s RedemptionScope) IsAdmin() bool {
	return s.Role == common.RoleAdminUser || s.Role == common.RoleRootUser
}
func (s RedemptionScope) valid() bool {
	return s.system || (s.UserID > 0 && (s.Role == common.RoleAgentUser || s.IsAdmin()))
}
func (s RedemptionScope) query(tx *gorm.DB) *gorm.DB {
	if !s.valid() {
		return tx.Where("1 = 0")
	}
	if !s.IsAdmin() {
		return tx.Where("budget_agent_id = ?", s.UserID)
	}
	return tx
}

func validateAgentRequestID(requestID string) error {
	if len(requestID) < 8 || len(requestID) > 96 {
		return ErrAgentQuotaInvalid
	}
	for _, ch := range requestID {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' && ch != '_' && ch != ':' {
			return ErrAgentQuotaInvalid
		}
	}
	return nil
}

func claimAgentRedemptionOperation(tx *gorm.DB, kind, requestID string, scope RedemptionScope, agentID int, signatureData any) (*AgentRedemptionOperation, bool, error) {
	if err := validateAgentRequestID(requestID); err != nil {
		return nil, false, err
	}
	encoded, err := common.Marshal(signatureData)
	if err != nil {
		return nil, false, err
	}
	signature := sha256.Sum256(encoded)
	operation := AgentRedemptionOperation{OperationKey: kind + ":" + requestID, Signature: hex.EncodeToString(signature[:]), ActorUserID: scope.UserID, AgentUserID: agentID, Kind: kind}
	result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "operation_key"}}, DoNothing: true}).Create(&operation)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected != 0 {
		return &operation, false, nil
	}
	var existing AgentRedemptionOperation
	if err := tx.Where("operation_key = ?", operation.OperationKey).First(&existing).Error; err != nil {
		return nil, false, err
	}
	if existing.Signature != operation.Signature || existing.ActorUserID != scope.UserID || existing.AgentUserID != agentID || existing.Kind != kind {
		return nil, false, ErrAgentQuotaConflict
	}
	return &existing, true, nil
}

func lockAgentRedemptionAccount(tx *gorm.DB, agentID int, create bool) (*AgentRedemptionAccount, error) {
	if create {
		account := AgentRedemptionAccount{AgentUserID: agentID}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "agent_user_id"}}, DoNothing: true}).Create(&account).Error; err != nil {
			return nil, err
		}
	}
	var account AgentRedemptionAccount
	if err := lockForUpdate(tx).Where("agent_user_id = ?", agentID).First(&account).Error; err != nil {
		return nil, err
	}
	if err := validateAgentAccount(&account); err != nil {
		return nil, err
	}
	return &account, nil
}

func validateAgentAccount(account *AgentRedemptionAccount) error {
	if account.AgentUserID <= 0 || account.AllocatedQuota < 0 || account.AllocatedQuota > common.MaxWalletQuota || account.AvailableQuota < 0 || account.ReservedQuota < 0 || account.SpentQuota < 0 {
		return ErrAgentQuotaInvariant
	}
	// Subtractions avoid an unchecked sum even when encountering corrupt rows.
	if account.AvailableQuota > account.AllocatedQuota || account.ReservedQuota > account.AllocatedQuota-account.AvailableQuota || account.SpentQuota != account.AllocatedQuota-account.AvailableQuota-account.ReservedQuota {
		return ErrAgentQuotaInvariant
	}
	return nil
}

// Budget and code locks are acquired before user locks. The actor and optional
// issuance owner are locked in numeric order, making live authorization atomic
// with role changes without relying on session/cache role snapshots.
func requireRedemptionActorTx(tx *gorm.DB, scope RedemptionScope, activeAgentID int) error {
	if scope.system {
		return nil
	} // internal legacy model methods, never HTTP
	ids := []int{scope.UserID}
	if activeAgentID > 0 && activeAgentID != scope.UserID {
		ids = append(ids, activeAgentID)
	}
	var users []User
	if err := lockForUpdate(tx).Select("id", "role", "status").Where("id IN ?", ids).Order("id ASC").Find(&users).Error; err != nil {
		return err
	}
	actorValid, agentValid := false, activeAgentID == 0
	for _, user := range users {
		if user.Id == scope.UserID {
			actorValid = user.Role == scope.Role && user.Status == common.UserStatusEnabled
		}
		if user.Id == activeAgentID {
			agentValid = user.Role == common.RoleAgentUser && user.Status == common.UserStatusEnabled
		}
	}
	if !actorValid {
		return ErrAgentRedemptionPermission
	}
	if !agentValid {
		return ErrAgentRedemptionUnavailable
	}
	return nil
}

// applyAgentQuotaChangeTx is the sole budget mutation path. The caller holds the
// account lock. Changes and their immutable audit row commit or roll back together.
func applyAgentQuotaChangeTx(tx *gorm.DB, account *AgentRedemptionAccount, entry AgentRedemptionLedger) error {
	if err := validateAgentAccount(account); err != nil {
		return err
	}
	before := *account
	for _, change := range []struct {
		balance *int
		delta   int
	}{
		{&account.AvailableQuota, entry.Delta}, {&account.ReservedQuota, entry.ReservedDelta}, {&account.SpentQuota, entry.SpentDelta}, {&account.AllocatedQuota, entry.AllocatedDelta},
	} {
		if change.delta > common.MaxWalletQuota || change.delta < -common.MaxWalletQuota {
			return ErrAgentQuotaInvalid
		}
		if change.delta < 0 && *change.balance < -change.delta {
			return ErrAgentQuotaInsufficient
		}
		if change.delta > 0 && *change.balance > common.MaxWalletQuota-change.delta {
			return ErrWalletQuotaLimitExceeded
		}
		*change.balance += change.delta
	}
	if err := validateAgentAccount(account); err != nil {
		return err
	}
	account.UpdatedAt = common.GetTimestamp()
	result := tx.Model(&AgentRedemptionAccount{}).Where("agent_user_id = ? AND available_quota = ? AND reserved_quota = ? AND spent_quota = ? AND allocated_quota = ?", account.AgentUserID, before.AvailableQuota, before.ReservedQuota, before.SpentQuota, before.AllocatedQuota).Updates(map[string]any{"available_quota": account.AvailableQuota, "reserved_quota": account.ReservedQuota, "spent_quota": account.SpentQuota, "allocated_quota": account.AllocatedQuota, "updated_at": account.UpdatedAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAgentQuotaConflict
	}
	entry.AgentUserID = account.AgentUserID
	entry.AvailableAfter, entry.ReservedAfter, entry.SpentAfter, entry.AllocatedAfter = account.AvailableQuota, account.ReservedQuota, account.SpentQuota, account.AllocatedQuota
	return tx.Create(&entry).Error
}

func AllocateAgentRedemptionQuota(scope RedemptionScope, request AgentQuotaAllocationRequest) (*AgentQuotaAllocationResult, error) {
	if !scope.valid() || !scope.IsAdmin() {
		return nil, ErrAgentRedemptionPermission
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.AgentUserID <= 0 || request.Delta == 0 || request.Delta > common.MaxWalletQuota || request.Delta < -common.MaxWalletQuota || request.Reason == "" || utf8.RuneCountInString(request.Reason) > 255 {
		return nil, ErrAgentQuotaInvalid
	}
	var result AgentQuotaAllocationResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		operation, replayed, err := claimAgentRedemptionOperation(tx, "allocation", request.RequestID, scope, request.AgentUserID, request)
		if err != nil {
			return err
		}
		if replayed {
			if err := requireRedemptionActorTx(tx, scope, 0); err != nil {
				return err
			}
			if err := common.UnmarshalJsonStr(operation.Result, &result.Account); err != nil {
				return ErrAgentQuotaInvariant
			}
			result.Replayed = true
			return nil
		}
		account, err := lockAgentRedemptionAccount(tx, request.AgentUserID, true)
		if err != nil {
			return err
		}
		// Reclaiming an existing balance remains possible after a role downgrade
		// or disable. Positive new allocations require an active agent.
		activeAgentID := 0
		if request.Delta > 0 {
			activeAgentID = request.AgentUserID
		}
		if err := requireRedemptionActorTx(tx, scope, activeAgentID); err != nil {
			return err
		}
		if err := applyAgentQuotaChangeTx(tx, account, AgentRedemptionLedger{OperationKey: operation.OperationKey, ActorUserID: scope.UserID, Kind: "allocation", Delta: request.Delta, AllocatedDelta: request.Delta, RequestID: request.RequestID, Reason: request.Reason}); err != nil {
			return err
		}
		result.Account = *account
		encoded, err := common.Marshal(account)
		if err != nil {
			return err
		}
		return tx.Model(operation).Update("result", string(encoded)).Error
	})
	return &result, err
}

func GetAgentRedemptionAccounts(scope RedemptionScope, agentID, start, count int) ([]*AgentRedemptionAccount, int64, error) {
	if !scope.valid() {
		return nil, 0, ErrAgentRedemptionPermission
	}
	// Users are soft-deleted. Reading the unscoped identity table retains budgets
	// after disable, downgrade or deletion, and shows unallocated agents as zero.
	query := DB.Table("users AS agent_users").Joins("LEFT JOIN agent_redemption_accounts AS budgets ON budgets.agent_user_id = agent_users.id").Where("(agent_users.role = ? OR budgets.agent_user_id IS NOT NULL)", common.RoleAgentUser)
	if !scope.IsAdmin() {
		agentID = scope.UserID
	}
	if agentID > 0 {
		query = query.Where("agent_users.id = ?", agentID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	accounts := make([]*AgentRedemptionAccount, 0)
	selectColumns := "agent_users.id AS agent_user_id, agent_users.username, agent_users.display_name, COALESCE(budgets.available_quota, 0) AS available_quota, COALESCE(budgets.reserved_quota, 0) AS reserved_quota, COALESCE(budgets.spent_quota, 0) AS spent_quota, COALESCE(budgets.allocated_quota, 0) AS allocated_quota, COALESCE(budgets.created_at, 0) AS created_at, COALESCE(budgets.updated_at, 0) AS updated_at"
	if err := query.Select(selectColumns).Order("agent_users.id ASC").Offset(start).Limit(count).Scan(&accounts).Error; err != nil {
		return nil, 0, err
	}
	return accounts, total, nil
}

func GetAgentRedemptionLedger(scope RedemptionScope, agentID, start, count int) ([]*AgentRedemptionLedger, int64, error) {
	if !scope.valid() || agentID <= 0 || (!scope.IsAdmin() && agentID != scope.UserID) {
		return nil, 0, ErrAgentRedemptionPermission
	}
	query := DB.Model(&AgentRedemptionLedger{}).Where("agent_user_id = ?", agentID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]*AgentRedemptionLedger, 0)
	if err := query.Order("id DESC").Offset(start).Limit(count).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
