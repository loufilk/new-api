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
	gormlogger "gorm.io/gorm/logger"
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
	ActiveAgent    bool   `json:"active_agent" gorm:"->;-:migration"`
	UserRole       int    `json:"user_role,omitempty" gorm:"->;-:migration"`
	UserStatus     int    `json:"user_status,omitempty" gorm:"->;-:migration"`
	UserDeleted    bool   `json:"user_deleted,omitempty" gorm:"->;-:migration"`
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
	ActorRole    int    `json:"-"`
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
	RequestID      string               `json:"-"`
	Identity       *AuthSessionIdentity `json:"-"`
	UserID, Role   int
	HasProfile     bool
	ProfileVersion int64
	Authorization  *AgentTransactionAuthorization `json:"-"`
	system         bool
}

func (s RedemptionScope) IsAdmin() bool {
	return s.Role == common.RoleAdminUser || s.Role == common.RoleRootUser
}
func (s RedemptionScope) valid() bool {
	return s.system || (s.UserID > 0 && (s.HasProfile || s.Role == common.RoleAgentUser || s.IsAdmin()))
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

// ResolveRedemptionScope derives access from current persisted identity rather
// than a client field or a cached role. Mutations repeat this check under lock.
func ResolveRedemptionScope(userID int) (RedemptionScope, error) {
	var user User
	if userID <= 0 {
		return RedemptionScope{}, ErrAgentRedemptionPermission
	}
	if err := DB.Select("id", "role", "status").Where("id = ?", userID).First(&user).Error; err != nil {
		return RedemptionScope{}, ErrAgentRedemptionPermission
	}
	scope := RedemptionScope{UserID: user.Id, Role: user.Role}
	profile, err := GetAgentProfile(user.Id)
	if err != nil {
		return RedemptionScope{}, err
	}
	if profile != nil {
		scope.HasProfile = true
		scope.ProfileVersion = profile.Version
	}
	if !scope.valid() || user.Status != common.UserStatusEnabled {
		return RedemptionScope{}, ErrAgentRedemptionPermission
	}
	return scope, nil
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
	operation := AgentRedemptionOperation{OperationKey: kind + ":" + requestID, Signature: hex.EncodeToString(signature[:]), ActorUserID: scope.UserID, ActorRole: scope.Role, AgentUserID: agentID, Kind: kind}
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
	if scope.system && activeAgentID == 0 {
		return nil
	} // internal legacy model methods, never HTTP
	ids := make([]int, 0, 2)
	if !scope.system {
		ids = append(ids, scope.UserID)
	}
	if activeAgentID > 0 && activeAgentID != scope.UserID {
		ids = append(ids, activeAgentID)
	}
	var users []User
	if err := lockForUpdate(tx).Select("id", "role", "status").Where("id IN ?", ids).Order("id ASC").Find(&users).Error; err != nil {
		return err
	}
	actorValid, agentValid := scope.system, activeAgentID == 0
	for _, user := range users {
		if user.Id == scope.UserID {
			actorValid = user.Role == scope.Role && user.Status == common.UserStatusEnabled
		}
		if user.Id == activeAgentID {
			agentValid = user.Status == common.UserStatusEnabled
		}
	}
	if !actorValid {
		return ErrAgentRedemptionPermission
	}
	if !agentValid {
		return ErrAgentRedemptionUnavailable
	}
	if activeAgentID > 0 {
		profile, err := LockAgentProfileTx(tx, activeAgentID)
		if err != nil {
			return err
		}
		if profile == nil || !profile.Allows(AgentCapabilitySales) {
			return ErrAgentRedemptionUnavailable
		}
	}
	if !scope.system && !scope.IsAdmin() {
		profile, err := LockAgentProfileTx(tx, scope.UserID)
		if err != nil {
			return err
		}
		if profile == nil || !profile.Allows(AgentCapabilityHistory) || (scope.ProfileVersion > 0 && scope.ProfileVersion != profile.Version) {
			return ErrAgentRedemptionPermission
		}
	}
	if scope.Identity != nil {
		if scope.Identity.UserID != scope.UserID {
			return ErrAgentRedemptionPermission
		}
		// Actor is already locked above; validation next locks its session or PAT
		// receipt. Never log SQL containing a session identifier on failure.
		privateTx := tx.Session(&gorm.Session{Logger: tx.Logger.LogMode(gormlogger.Silent)})
		if err := ValidateAuthSessionWithTx(privateTx, *scope.Identity); err != nil {
			return ErrAgentRedemptionPermission
		}
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
	err := DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
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
		var authorizationContext AgentTransactionContext
		if scope.Identity != nil {
			if scope.Authorization == nil || scope.Authorization.Identity() != *scope.Identity {
				return ErrAgentTransactionProof
			}
			authorizationContext, err = buildAgentAllocationContextTx(tx, policy, account, request)
			if err != nil {
				return err
			}
		}
		if err := ensureAgentFundSourcesTx(tx, account); err != nil {
			return err
		}
		if request.Delta > 0 {
			batch := AgentFundBatch{SourceKey: operation.OperationKey, AgentUserID: request.AgentUserID, SourceKind: AgentFundPromotion, OriginalQuota: request.Delta, OriginalPaidMinor: "0", WalletAccount: AccountPaid, PolicySnapshot: "{}"}
			if err := createAgentFundBatchTx(tx, &batch); err != nil {
				return err
			}
		} else {
			// Administrative free-budget reclaim can never confiscate purchased or
			// converted commission principal, even when it shares the aggregate.
			if _, err := takeAgentFundSlicesTx(tx, request.AgentUserID, -request.Delta, 0, []string{AgentFundLegacy, AgentFundPromotion}, AgentSliceReclaimed, operation.OperationKey, 0, 0); err != nil {
				return err
			}
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
		if scope.Identity != nil {
			if err := ConsumeAgentTransactionAuthorizationTx(tx, *scope.Authorization, authorizationContext); err != nil {
				return err
			}
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
	start, count = min(max(start, 0), 100_000_000), min(max(count, 1), 100)
	if !scope.valid() {
		return nil, 0, ErrAgentRedemptionPermission
	}
	// The UNION also retains an orphaned budget after a hard user deletion.
	// FULL OUTER JOIN is not portable; this standard UNION works on existing
	// supported dialects while exposing all zero-budget agent identities.
	query := DB.Table("(SELECT agent_user_id FROM agent_redemption_accounts UNION SELECT user_id AS agent_user_id FROM agent_profiles UNION SELECT id AS agent_user_id FROM users WHERE role = ?) AS agent_ids", common.RoleAgentUser).
		Joins("LEFT JOIN users AS agent_users ON agent_users.id = agent_ids.agent_user_id").
		Joins("LEFT JOIN agent_redemption_accounts AS budgets ON budgets.agent_user_id = agent_ids.agent_user_id").
		Joins("LEFT JOIN agent_profiles AS profiles ON profiles.user_id = agent_ids.agent_user_id")
	if !scope.IsAdmin() {
		agentID = scope.UserID
	}
	if agentID > 0 {
		query = query.Where("agent_ids.agent_user_id = ?", agentID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	accounts := make([]*AgentRedemptionAccount, 0)
	selectColumns := "COALESCE((agent_users.id IS NOT NULL AND agent_users.deleted_at IS NULL AND agent_users.status = 1 AND profiles.status = 'active' AND profiles.sales_frozen = false AND profiles.version > 0), false) AS active_agent, agent_ids.agent_user_id, COALESCE(agent_users.role, 0) AS user_role, COALESCE(agent_users.status, 0) AS user_status, (agent_users.id IS NULL OR agent_users.deleted_at IS NOT NULL) AS user_deleted, COALESCE(agent_users.username, '') AS username, COALESCE(agent_users.display_name, '') AS display_name, COALESCE(budgets.available_quota, 0) AS available_quota, COALESCE(budgets.reserved_quota, 0) AS reserved_quota, COALESCE(budgets.spent_quota, 0) AS spent_quota, COALESCE(budgets.allocated_quota, 0) AS allocated_quota, COALESCE(budgets.created_at, 0) AS created_at, COALESCE(budgets.updated_at, 0) AS updated_at"
	if err := query.Select(selectColumns).Order("agent_ids.agent_user_id ASC").Offset(start).Limit(count).Scan(&accounts).Error; err != nil {
		return nil, 0, err
	}
	return accounts, total, nil
}

func GetAgentRedemptionLedger(scope RedemptionScope, agentID, start, count int) ([]*AgentRedemptionLedger, int64, error) {
	start, count = min(max(start, 0), 100_000_000), min(max(count, 1), 100)
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
