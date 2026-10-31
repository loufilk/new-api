package model

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AgentProfileActive        = "active"
	AgentProfileExited        = "exited"
	AgentCapabilityHistory    = "history"
	AgentCapabilitySales      = "sales"
	AgentCapabilitySettlement = "settlement"
	AgentCapabilityAdmin      = "admin"
)

var (
	ErrAgentProfilePermission = errors.New("Agent profile permission denied")
	ErrAgentProfileConflict   = errors.New("Agent permissions changed; reload and try again")
	ErrAgentProfileInvalid    = errors.New("Invalid agent profile request")
	ErrAgentProfileImmutable  = errors.New("Agent profile audit records are immutable")
)

// AgentProfile is a business entitlement, not a login role or a wallet. Exiting
// or freezing a business never disables the person's account or API credentials.
type AgentProfile struct {
	UserID           int    `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	Status           string `json:"status" gorm:"type:varchar(16);not null"`
	SalesFrozen      bool   `json:"sales_frozen" gorm:"not null"`
	SettlementFrozen bool   `json:"settlement_frozen" gorm:"not null"`
	Version          int64  `json:"version" gorm:"type:bigint;not null"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        int64  `json:"updated_at" gorm:"autoUpdateTime"`
	UpdatedBy        int    `json:"updated_by" gorm:"not null"`
	Reason           string `json:"reason" gorm:"type:varchar(255);not null"`
}

type AgentCapabilities struct {
	History    bool `json:"history"`
	Sales      bool `json:"sales"`
	Settlement bool `json:"settlement"`
	Admin      bool `json:"admin"`
}

func (profile *AgentProfile) Allows(capability string) bool {
	if profile == nil || profile.Version <= 0 || (profile.Status != AgentProfileActive && profile.Status != AgentProfileExited) {
		return false
	}
	switch capability {
	case AgentCapabilityHistory:
		return true
	case AgentCapabilitySales:
		return profile.Status == AgentProfileActive && !profile.SalesFrozen
	case AgentCapabilitySettlement:
		return !profile.SettlementFrozen
	default:
		return false
	}
}

func AgentCapabilitiesFor(user *User, profile *AgentProfile) AgentCapabilities {
	if user == nil || user.Status != common.UserStatusEnabled || user.DeletedAt.Valid {
		return AgentCapabilities{}
	}
	admin := user.Role == common.RoleAdminUser || user.Role == common.RoleRootUser
	return AgentCapabilities{History: admin || profile.Allows(AgentCapabilityHistory), Sales: profile.Allows(AgentCapabilitySales), Settlement: profile.Allows(AgentCapabilitySettlement), Admin: admin}
}

func GetAgentProfile(userID int) (*AgentProfile, error) {
	return readAgentProfile(DB, userID, false)
}

// LockAgentProfileTx follows related user locks. A missing row is represented
// by nil, never by an implicitly active profile or a numeric legacy role.
func LockAgentProfileTx(tx *gorm.DB, userID int) (*AgentProfile, error) {
	return readAgentProfile(tx, userID, true)
}

func readAgentProfile(tx *gorm.DB, userID int, locked bool) (*AgentProfile, error) {
	if tx == nil || userID <= 0 {
		return nil, ErrAgentProfileInvalid
	}
	query := tx.Where("user_id = ?", userID)
	if locked {
		query = lockForUpdate(query)
	}
	var profile AgentProfile
	if err := query.First(&profile).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &profile, nil
}

// RequireAgentCapabilityTx rechecks live users, then the target profile, then
// the browser session. Financial callers must already hold the current policy
// and their operation/account/batch locks before calling this function.
func RequireAgentCapabilityTx(tx *gorm.DB, identity AuthSessionIdentity, targetUserID int, expectedVersion int64, capability string) (*AgentProfile, error) {
	if tx == nil || identity.UserID <= 0 || targetUserID <= 0 || expectedVersion < 0 {
		return nil, ErrAgentProfilePermission
	}
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat || identity.SessionID == "" {
		return nil, ErrAgentProfilePermission
	}
	var users []User
	if err := lockForUpdate(tx).Where("id IN ?", []int{identity.UserID, targetUserID}).Order("id").Find(&users).Error; err != nil {
		return nil, err
	}
	var actor, target *User
	for i := range users {
		if users[i].Id == identity.UserID {
			actor = &users[i]
		}
		if users[i].Id == targetUserID {
			target = &users[i]
		}
	}
	if actor == nil || target == nil || actor.Status != common.UserStatusEnabled {
		return nil, ErrAgentProfilePermission
	}
	admin := actor.Role == common.RoleAdminUser || actor.Role == common.RoleRootUser
	if (identity.UserID != targetUserID || capability == AgentCapabilityAdmin) && !admin {
		return nil, ErrAgentProfilePermission
	}
	profile, err := LockAgentProfileTx(tx, targetUserID)
	if err != nil {
		return nil, err
	}
	version := int64(0)
	if profile != nil {
		version = profile.Version
	}
	if version != expectedVersion {
		return nil, ErrAgentProfileConflict
	}
	if capability != AgentCapabilityAdmin && !profile.Allows(capability) {
		return nil, ErrAgentProfilePermission
	}
	if capability == AgentCapabilitySales && target.Status != common.UserStatusEnabled {
		return nil, ErrAgentProfilePermission
	}
	if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
		return nil, err
	}
	return profile, nil
}

type AgentProfileMutation struct {
	ExpectedVersion  int64  `json:"expected_version"`
	Status           string `json:"status"`
	SalesFrozen      bool   `json:"sales_frozen"`
	SettlementFrozen bool   `json:"settlement_frozen"`
	Reason           string `json:"reason"`
}

type AgentProfileAudit struct {
	ID                       int64  `json:"id" gorm:"primaryKey"`
	UserID                   int    `json:"user_id" gorm:"not null;uniqueIndex:idx_agent_profile_audit_version,priority:1"`
	Version                  int64  `json:"version" gorm:"not null;uniqueIndex:idx_agent_profile_audit_version,priority:2"`
	ActorUserID              int    `json:"actor_user_id" gorm:"not null"`
	PreviousStatus           string `json:"previous_status" gorm:"type:varchar(16)"`
	Status                   string `json:"status" gorm:"type:varchar(16);not null"`
	PreviousSalesFrozen      bool   `json:"previous_sales_frozen" gorm:"not null"`
	SalesFrozen              bool   `json:"sales_frozen" gorm:"not null"`
	PreviousSettlementFrozen bool   `json:"previous_settlement_frozen" gorm:"not null"`
	SettlementFrozen         bool   `json:"settlement_frozen" gorm:"not null"`
	Reason                   string `json:"reason" gorm:"type:varchar(255);not null"`
	CreatedAt                int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentProfileAudit) BeforeUpdate(*gorm.DB) error { return ErrAgentProfileImmutable }
func (*AgentProfileAudit) BeforeDelete(*gorm.DB) error { return ErrAgentProfileImmutable }

// UpdateAgentProfileTx does not acquire financial locks or modify any balance,
// account status, user auth version, model key, PAT, or existing delivery.
func UpdateAgentProfileTx(tx *gorm.DB, identity AuthSessionIdentity, userID int, request AgentProfileMutation) (*AgentProfile, error) {
	if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
		return nil, err
	}
	if _, err := RequireAgentCapabilityTx(tx, identity, userID, request.ExpectedVersion, AgentCapabilityAdmin); err != nil {
		return nil, err
	}
	if identity.UserID == userID {
		return nil, ErrAgentProfilePermission
	}
	var actor, target User
	if err := tx.First(&actor, identity.UserID).Error; err != nil {
		return nil, err
	}
	if err := tx.First(&target, userID).Error; err != nil {
		return nil, err
	}
	if target.Role >= actor.Role {
		return nil, ErrAgentProfilePermission
	}
	return updateAgentProfileTx(tx, identity.UserID, userID, request)
}

// updateAgentProfileTx requires the caller to hold the users rows first. The
// compare-and-swap version also handles concurrent first-time qualification.
func updateAgentProfileTx(tx *gorm.DB, actorID, userID int, request AgentProfileMutation) (*AgentProfile, error) {
	if err := validateAgentProfileMutation(userID, request); err != nil {
		return nil, err
	}
	previous, err := LockAgentProfileTx(tx, userID)
	if err != nil {
		return nil, err
	}
	if previous == nil {
		if request.ExpectedVersion != 0 {
			return nil, ErrAgentProfileConflict
		}
		previous = &AgentProfile{UserID: userID}
	} else if previous.Version != request.ExpectedVersion {
		return nil, ErrAgentProfileConflict
	}
	if previous.Version > 0 && previous.Status == request.Status && previous.SalesFrozen == request.SalesFrozen && previous.SettlementFrozen == request.SettlementFrozen && previous.Reason == request.Reason {
		return previous, nil
	}
	// Migration (actor 0) preserves existing qualification; only explicitly
	// enabling new business is subject to the approved onboarding policy.
	if actorID > 0 && request.Status == AgentProfileActive && previous.Status != AgentProfileActive {
		if err := RequireAgentGovernanceActivationTx(tx, userID); err != nil {
			return nil, err
		}
	}
	profile := AgentProfile{UserID: userID, Status: request.Status, SalesFrozen: request.SalesFrozen, SettlementFrozen: request.SettlementFrozen, Version: previous.Version + 1, CreatedAt: previous.CreatedAt, UpdatedBy: actorID, Reason: request.Reason}
	if previous.Version == 0 {
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoNothing: true}).Create(&profile)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, ErrAgentProfileConflict
		}
	} else {
		profile.UpdatedAt = common.GetTimestamp()
		result := tx.Model(&AgentProfile{}).Where("user_id = ? AND version = ?", userID, previous.Version).Updates(map[string]any{"status": profile.Status, "sales_frozen": profile.SalesFrozen, "settlement_frozen": profile.SettlementFrozen, "version": profile.Version, "updated_at": profile.UpdatedAt, "updated_by": actorID, "reason": profile.Reason})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, ErrAgentProfileConflict
		}
	}
	audit := AgentProfileAudit{UserID: userID, Version: profile.Version, ActorUserID: actorID, PreviousStatus: previous.Status, Status: profile.Status, PreviousSalesFrozen: previous.SalesFrozen, SalesFrozen: profile.SalesFrozen, PreviousSettlementFrozen: previous.SettlementFrozen, SettlementFrozen: profile.SettlementFrozen, Reason: profile.Reason}
	if err := tx.Create(&audit).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

func validateAgentProfileMutation(userID int, request AgentProfileMutation) error {
	if userID <= 0 || request.ExpectedVersion < 0 || request.ExpectedVersion == math.MaxInt64 ||
		(request.Status != AgentProfileActive && request.Status != AgentProfileExited) || strings.TrimSpace(request.Reason) == "" || request.Reason != strings.TrimSpace(request.Reason) || utf8.RuneCountInString(request.Reason) > 255 {
		return ErrAgentProfileInvalid
	}
	return nil
}
