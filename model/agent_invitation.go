package model

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const (
	InvitationSourcePassword         = "password"
	InvitationSourceOAuth            = "oauth"
	InvitationSourceLinuxDO          = "linuxdo"
	LedgerReasonAgentInvitationBonus = "agent_invitation_bonus"
)

var (
	ErrAgentInvitationPermission = errors.New("agent invitation permission denied")
	ErrAgentInvitationPolicy     = errors.New("invalid agent invitation policy")
	ErrAgentInvitationImmutable  = errors.New("agent invitation records are immutable")
)

// AgentInvitation is an immutable registration-time attribution. There is no
// update or late-claim API. It deliberately contains neither email nor OAuth
// subject, session material, referral codes, or the invitee's wallet balance.
// UserId is unique even after a user is deleted; a login/finalize retry cannot
// create another attribution or grant another test balance.
type AgentInvitation struct {
	Id              int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId          int    `json:"user_id" gorm:"not null;uniqueIndex"`
	InviterId       int    `json:"inviter_id" gorm:"not null;index:idx_agent_invitation_inviter_time,priority:1"`
	InviterUsername string `json:"inviter_username" gorm:"type:varchar(64);not null"`
	InviterRole     int    `json:"inviter_role" gorm:"not null"`
	Source          string `json:"source" gorm:"type:varchar(16);not null"`
	BonusQuota      int    `json:"bonus_quota" gorm:"type:bigint;not null"`
	BonusReason     string `json:"bonus_reason" gorm:"type:varchar(32);not null"`
	PolicyRevision  int64  `json:"policy_revision" gorm:"type:bigint;not null"`
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime;index:idx_agent_invitation_inviter_time,priority:2"`
}

func (*AgentInvitation) BeforeUpdate(*gorm.DB) error { return ErrAgentInvitationImmutable }
func (*AgentInvitation) BeforeDelete(*gorm.DB) error { return ErrAgentInvitationImmutable }

// AgentInvitationPolicy is separate from legacy options so ordinary system
// administrators cannot change the root-only test-balance policy indirectly.
// An absent singleton means disabled / zero, without a registration-time write.
type AgentInvitationPolicy struct {
	Id                         int   `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Enabled                    bool  `json:"enabled" gorm:"not null"`
	Quota                      int   `json:"quota" gorm:"type:bigint;not null"`
	Revision                   int64 `json:"revision" gorm:"type:bigint;not null"`
	UpdatedBy                  int   `json:"updated_by" gorm:"not null"`
	UpdatedAt                  int64 `json:"updated_at" gorm:"type:bigint;not null"`
	PaymentComplianceConfirmed bool  `json:"payment_compliance_confirmed" gorm:"-:all"`
}

// AgentInvitationPolicyAudit commits with its policy change in the main DB.
type AgentInvitationPolicyAudit struct {
	Id              int   `json:"id" gorm:"primaryKey;autoIncrement"`
	Revision        int64 `json:"revision" gorm:"type:bigint;not null;uniqueIndex"`
	OperatorId      int   `json:"operator_id" gorm:"not null;index"`
	PreviousEnabled bool  `json:"previous_enabled" gorm:"not null"`
	PreviousQuota   int   `json:"previous_quota" gorm:"type:bigint;not null"`
	Enabled         bool  `json:"enabled" gorm:"not null"`
	Quota           int   `json:"quota" gorm:"type:bigint;not null"`
	CreatedAt       int64 `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentInvitationPolicyAudit) BeforeUpdate(*gorm.DB) error { return ErrAgentInvitationImmutable }
func (*AgentInvitationPolicyAudit) BeforeDelete(*gorm.DB) error { return ErrAgentInvitationImmutable }

type AgentInvitationSummary struct {
	InviterId       int    `json:"inviter_id"`
	InviterUsername string `json:"inviter_username"`
	InviterRole     int    `json:"inviter_role"`
	Source          string `json:"source"`
	BonusQuota      int    `json:"bonus_quota"`
	BonusReason     string `json:"bonus_reason"`
	CreatedAt       int64  `json:"created_at"`
	Legacy          bool   `json:"legacy"`
}

func readAgentInvitationPolicy(tx *gorm.DB) (*AgentInvitationPolicy, error) {
	policy := &AgentInvitationPolicy{Id: 1, PaymentComplianceConfirmed: operation_setting.IsPaymentComplianceConfirmed()}
	err := tx.First(policy, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return policy, nil
	}
	return policy, err
}

// GetAgentInvitationPolicy rechecks the caller's current role; a stale request
// context or a client-supplied role can never disclose or modify this policy.
func GetAgentInvitationPolicy(actorID int) (*AgentInvitationPolicy, error) {
	var policy *AgentInvitationPolicy
	err := DB.Transaction(func(tx *gorm.DB) error {
		var actor User
		if err := lockForUpdate(tx).Select("id", "role", "status").First(&actor, actorID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAgentInvitationPermission
			}
			return err
		}
		if actorID <= 0 || actor.Status != common.UserStatusEnabled || actor.Role != common.RoleRootUser {
			return ErrAgentInvitationPermission
		}
		var err error
		policy, err = readAgentInvitationPolicy(tx)
		return err
	})
	return policy, err
}

// UpdateAgentInvitationPolicy is retained for trusted internal callers. HTTP
// changes must use UpdateAgentInvitationPolicyAuthorized with transaction MFA.
func UpdateAgentInvitationPolicy(actorID int, enabled bool, quota int, identities ...AuthSessionIdentity) (*AgentInvitationPolicy, error) {
	if actorID <= 0 || len(identities) > 1 {
		return nil, ErrAgentInvitationPermission
	}
	if quota < 0 || quota > common.MaxWalletQuota {
		return nil, ErrAgentInvitationPolicy
	}
	var policy *AgentInvitationPolicy
	err := DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		var err error
		policy, err = lockAgentInvitationPolicyTx(tx)
		if err != nil {
			return err
		}
		var actor User
		if err := lockForUpdate(tx).First(&actor, actorID).Error; err != nil {
			return ErrAgentInvitationPermission
		}
		if actor.Status != common.UserStatusEnabled || actor.Role != common.RoleRootUser {
			return ErrAgentInvitationPermission
		}
		if len(identities) == 1 {
			identity := identities[0]
			if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat || identity.UserID != actorID {
				return ErrAgentInvitationPermission
			}
			if err := ValidateAuthSessionWithTx(tx.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}), identity); err != nil {
				return ErrAgentInvitationPermission
			}
		}
		return applyAgentInvitationPolicyValuesTx(tx, policy, actorID, enabled, quota)
	})
	if policy != nil {
		policy.PaymentComplianceConfirmed = operation_setting.IsPaymentComplianceConfirmed()
	}
	return policy, err
}

func lockAgentInvitationPolicyTx(tx *gorm.DB) (*AgentInvitationPolicy, error) {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&AgentInvitationPolicy{Id: 1}).Error; err != nil {
		return nil, err
	}
	var policy AgentInvitationPolicy
	if err := lockForUpdate(tx).First(&policy, 1).Error; err != nil {
		return nil, err
	}
	return &policy, nil
}

func applyAgentInvitationPolicyValuesTx(tx *gorm.DB, policy *AgentInvitationPolicy, actorID int, enabled bool, quota int) error {
	if quota < 0 || quota > common.MaxWalletQuota {
		return ErrAgentInvitationPolicy
	}
	if policy.Enabled == enabled && policy.Quota == quota {
		return nil
	}
	if policy.Revision == math.MaxInt64 {
		return ErrAgentInvitationPolicy
	}
	audit := AgentInvitationPolicyAudit{Revision: policy.Revision + 1, OperatorId: actorID, PreviousEnabled: policy.Enabled, PreviousQuota: policy.Quota, Enabled: enabled, Quota: quota}
	if err := tx.Create(&audit).Error; err != nil {
		return err
	}
	policy.Enabled, policy.Quota = enabled, quota
	policy.Revision, policy.UpdatedBy, policy.UpdatedAt = audit.Revision, actorID, audit.CreatedAt
	return tx.Model(&AgentInvitationPolicy{}).Where("id = ?", 1).Updates(map[string]any{"enabled": enabled, "quota": quota, "revision": policy.Revision, "updated_by": actorID, "updated_at": policy.UpdatedAt}).Error
}

// resolveInviterForInsert snapshots only a live, recognized inviter under a row
// lock, so demotion/disable and registration have a definite serialization
// order. Invalid invitations are ignored; database errors abort registration.
func resolveInviterForInsert(tx *gorm.DB, userID, inviterID int, source string) (*AgentInvitation, error) {
	if inviterID <= 0 || inviterID == userID {
		return nil, nil
	}
	var inviter User
	err := lockForUpdate(tx).Select("id", "username", "role", "status").First(&inviter, inviterID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if inviter.Status != common.UserStatusEnabled {
		return nil, nil
	}
	switch inviter.Role {
	case common.RoleCommonUser, common.RoleAgentUser, common.RoleAdminUser, common.RoleRootUser:
	default:
		return nil, nil
	}
	invitation := &AgentInvitation{
		InviterId: inviter.Id, InviterUsername: inviter.Username, InviterRole: inviter.Role, Source: source,
		BonusReason: "inviter_not_agent",
	}
	profile, err := LockAgentProfileTx(tx, inviter.Id)
	if err != nil {
		return nil, err
	}
	if !profile.Allows(AgentCapabilitySales) {
		return invitation, nil
	}
	policy, err := readAgentInvitationPolicy(tx)
	if err != nil {
		return nil, err
	}
	if policy.Quota < 0 || policy.Quota > common.MaxWalletQuota {
		return nil, ErrAgentInvitationPolicy
	}
	invitation.PolicyRevision = policy.Revision
	switch {
	case !policy.Enabled:
		invitation.BonusReason = "policy_disabled"
	case policy.Quota == 0:
		invitation.BonusReason = "zero_quota"
	case !operation_setting.IsPaymentComplianceConfirmed():
		// Match the existing invitation-reward gate. Attribution still commits,
		// but later confirmation cannot retroactively award this registration.
		invitation.BonusReason = "payment_compliance_required"
	default:
		invitation.BonusQuota, invitation.BonusReason = policy.Quota, "granted"
	}
	return invitation, nil
}

// commitRegistrationInvitationTx is called only after a successful INSERT of a
// genuinely new user, never by login, account linking, profile update, or the
// post-commit finalizer. Attribution, counter, test bonus and ledger are atomic.
func commitRegistrationInvitationTx(tx *gorm.DB, user *User, invitation *AgentInvitation) error {
	if invitation == nil {
		return nil
	}
	invitation.UserId = user.Id
	if err := tx.Create(invitation).Error; err != nil {
		return err
	}
	if invitation.BonusQuota > 0 {
		result, err := CreditQuotaTx(tx, LedgerEntry{
			UserId: user.Id, Account: AccountBonus, Delta: invitation.BonusQuota,
			Reason:        LedgerReasonAgentInvitationBonus,
			ReferenceType: "agent_invitation", ReferenceId: strconv.Itoa(user.Id),
			IdempotencyKey: fmt.Sprintf("agent_invitation_bonus:%d", user.Id),
		})
		if err != nil {
			return err
		}
		user.QuotaBonus, user.Quota = result.TotalAfter, result.TotalAfter
	}
	return inviteUserWithTx(tx, invitation.InviterId)
}

// inviteUserWithTx only increments the existing legacy referral counter. It
// never credits an inviter's wallet or changes the existing consumption policy.
func inviteUserWithTx(tx *gorm.DB, inviterID int) error {
	result := tx.Model(&User{}).Where("id = ? AND aff_count < ?", inviterID, math.MaxInt32).
		Update("aff_count", gorm.Expr("aff_count + ?", 1))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func inviteUser(inviterID int) error { return inviteUserWithTx(DB, inviterID) }

// ListAgentInvitations returns a deliberately small DTO and scopes agents using
// the current database actor, not query parameters or a supplied role.
func ListAgentInvitations(actorID, page, pageSize int, inviterIDs ...int) ([]AgentInvitation, int64, error) {
	if actorID <= 0 {
		return nil, 0, ErrAgentInvitationPermission
	}
	page, pageSize = max(1, min(page, 1_000_000)), max(1, min(pageSize, 100))
	items := make([]AgentInvitation, 0)
	var total int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var actor User
		if err := lockForUpdate(tx).Select("id", "role", "status").First(&actor, actorID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAgentInvitationPermission
			}
			return err
		}
		if actor.Status != common.UserStatusEnabled {
			return ErrAgentInvitationPermission
		}
		query := tx.Model(&AgentInvitation{})
		switch actor.Role {
		case common.RoleAdminUser, common.RoleRootUser:
			if len(inviterIDs) > 0 && inviterIDs[0] > 0 {
				query = query.Where("inviter_id = ?", inviterIDs[0])
			}
		default:
			profile, err := LockAgentProfileTx(tx, actor.Id)
			if err != nil {
				return err
			}
			if !profile.Allows(AgentCapabilityHistory) {
				return ErrAgentInvitationPermission
			}
			query = query.Where("inviter_id = ?", actor.Id)
		}
		if err := query.Count(&total).Error; err != nil {
			return err
		}
		return query.Order("id DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&items).Error
	})
	return items, total, err
}

// PopulateAgentInvitationSummaries is for authenticated administrator user
// views. Legacy relationships are identified explicitly rather than inventing
// their historical role or awarding a retroactive bonus.
func PopulateAgentInvitationSummaries(users []*User) error {
	ids := make([]int, 0, len(users))
	byID := make(map[int]*User, len(users))
	for _, user := range users {
		if user != nil {
			user.Invitation = nil
			ids, byID[user.Id] = append(ids, user.Id), user
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var invitations []AgentInvitation
	if err := DB.Where("user_id IN ?", ids).Find(&invitations).Error; err != nil {
		return err
	}
	for _, invitation := range invitations {
		if user := byID[invitation.UserId]; user != nil {
			user.Invitation = &AgentInvitationSummary{
				InviterId: invitation.InviterId, InviterUsername: invitation.InviterUsername,
				InviterRole: invitation.InviterRole, Source: invitation.Source,
				BonusQuota: invitation.BonusQuota, BonusReason: invitation.BonusReason, CreatedAt: invitation.CreatedAt,
			}
		}
	}
	legacyIDs := make([]int, 0)
	for _, user := range users {
		if user != nil && user.Invitation == nil && user.InviterId > 0 {
			legacyIDs = append(legacyIDs, user.InviterId)
		}
	}
	if len(legacyIDs) == 0 {
		return nil
	}
	var inviters []User
	if err := DB.Unscoped().Select("id", "username").Where("id IN ?", legacyIDs).Find(&inviters).Error; err != nil {
		return err
	}
	names := make(map[int]string, len(inviters))
	for _, inviter := range inviters {
		names[inviter.Id] = inviter.Username
	}
	for _, user := range users {
		if user != nil && user.Invitation == nil && user.InviterId > 0 {
			user.Invitation = &AgentInvitationSummary{InviterId: user.InviterId, InviterUsername: names[user.InviterId], Source: "legacy", Legacy: true}
		}
	}
	return nil
}
