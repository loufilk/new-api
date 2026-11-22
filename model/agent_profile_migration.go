package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentMigrationIssue preserves the first anomalous historical snapshot. It is
// evidence for reconciliation, never permission to erase or cash out a balance.
type AgentMigrationIssue struct {
	ID        int64  `json:"id" gorm:"primaryKey"`
	IssueKey  string `json:"issue_key" gorm:"type:varchar(128);not null;uniqueIndex"`
	UserID    int    `json:"user_id" gorm:"not null;index"`
	Kind      string `json:"kind" gorm:"type:varchar(64);not null"`
	Snapshot  string `json:"snapshot" gorm:"type:text;not null"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentMigrationIssue) BeforeUpdate(*gorm.DB) error { return ErrAgentProfileImmutable }
func (*AgentMigrationIssue) BeforeDelete(*gorm.DB) error { return ErrAgentProfileImmutable }

// MigrateLegacyAgentProfiles is idempotent and never changes monetary columns,
// token status, PAT records, or user auth versions. It runs after the additive
// profile tables have been migrated, while new business traffic is not served.
func MigrateLegacyAgentProfiles(db *gorm.DB) error {
	if db == nil {
		return ErrAgentProfileInvalid
	}
	var ids []int
	if err := db.Raw("SELECT id FROM users WHERE role = ? UNION SELECT agent_user_id AS id FROM agent_redemption_accounts ORDER BY id", common.RoleAgentUser).Scan(&ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if err := db.Transaction(func(tx *gorm.DB) error {
			var user User
			err := lockForUpdate(tx.Unscoped()).Where("id = ?", id).First(&user).Error
			missing := errors.Is(err, gorm.ErrRecordNotFound)
			if err != nil && !missing {
				return err
			}
			var account AgentRedemptionAccount
			accountErr := tx.Where("agent_user_id = ?", id).First(&account).Error
			if accountErr != nil && !errors.Is(accountErr, gorm.ErrRecordNotFound) {
				return accountErr
			}
			issues := make(map[string]any)
			if missing || user.DeletedAt.Valid {
				issues["orphaned_agent_account"] = map[string]any{"user_missing": missing, "user_deleted": user.DeletedAt.Valid}
			}
			if accountErr == nil && validateAgentAccount(&account) != nil {
				issues["invalid_legacy_agent_budget"] = map[string]any{"available": account.AvailableQuota, "reserved": account.ReservedQuota, "spent": account.SpentQuota, "allocated": account.AllocatedQuota}
			}
			if !missing && (user.QuotaPaid < 0 || user.QuotaBonus < 0 || user.QuotaReward < 0 || user.Quota < 0 || user.AffQuota < 0) {
				issues["negative_legacy_user_balance"] = map[string]any{"paid": user.QuotaPaid, "bonus": user.QuotaBonus, "reward": user.QuotaReward, "quota": user.Quota, "aff_quota": user.AffQuota}
			}
			for kind, snapshot := range issues {
				encoded, err := common.Marshal(snapshot)
				if err != nil {
					return err
				}
				issue := AgentMigrationIssue{IssueKey: fmt.Sprintf("profile-v1:%d:%s", id, kind), UserID: id, Kind: kind, Snapshot: string(encoded)}
				if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "issue_key"}}, DoNothing: true}).Create(&issue).Error; err != nil {
					return err
				}
			}
			// Invalid historical owner IDs are evidence only, not identities
			// for which a usable entitlement can be created.
			if id <= 0 {
				return nil
			}
			profile, err := LockAgentProfileTx(tx, id)
			if err != nil {
				return err
			}
			if profile == nil {
				status := AgentProfileExited
				if !missing && !user.DeletedAt.Valid && user.Role == common.RoleAgentUser {
					status = AgentProfileActive
				}
				_, err = updateAgentProfileTx(tx, 0, id, AgentProfileMutation{Status: status, SalesFrozen: len(issues) > 0, SettlementFrozen: len(issues) > 0, Reason: "Legacy agent qualification migration; balances and credentials unchanged"})
				if err != nil {
					return err
				}
			}
			if profile != nil && len(issues) > 0 && (!profile.SalesFrozen || !profile.SettlementFrozen) {
				if _, err := updateAgentProfileTx(tx, 0, id, AgentProfileMutation{ExpectedVersion: profile.Version, Status: profile.Status, SalesFrozen: true, SettlementFrozen: true, Reason: "Unresolved legacy migration anomaly; balances unchanged"}); err != nil {
					return err
				}
			}
			if !missing && user.Role == common.RoleAgentUser {
				return tx.Unscoped().Model(&User{}).Where("id = ? AND role = ?", id, common.RoleAgentUser).Update("role", common.RoleCommonUser).Error
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
