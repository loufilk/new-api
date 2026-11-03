package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Audit is immutable, contains no claim code/session/proof, and preserves every
// external reconciliation observation rather than overwriting an unknown result.
type AgentPurchaseAudit struct {
	ID                int    `json:"id" gorm:"primaryKey"`
	EventKey          string `json:"-" gorm:"type:varchar(160);not null;uniqueIndex"`
	ResourceType      string `json:"resource_type" gorm:"type:varchar(32);not null;index"`
	ResourceID        int    `json:"resource_id" gorm:"not null;index"`
	AgentUserID       int    `json:"agent_user_id" gorm:"not null;index"`
	ActorUserID       int    `json:"actor_user_id" gorm:"not null"`
	Action            string `json:"action" gorm:"type:varchar(64);not null"`
	FromState         string `json:"from_state" gorm:"type:varchar(32);not null"`
	ToState           string `json:"to_state" gorm:"type:varchar(32);not null"`
	Currency          string `json:"currency" gorm:"type:varchar(3)"`
	AmountMinor       string `json:"amount_minor" gorm:"type:varchar(32);not null"`
	Quota             int    `json:"quota" gorm:"type:bigint;not null"`
	BusinessKey       string `json:"business_key" gorm:"type:varchar(96)"`
	EvidenceReference string `json:"evidence_reference" gorm:"type:varchar(255)"`
	ExternalReference string `json:"external_reference" gorm:"type:varchar(255)"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentPurchaseAudit) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentPurchaseAudit) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }
func appendAgentPurchaseAuditTx(tx *gorm.DB, audit AgentPurchaseAudit) error {
	if audit.AgentUserID <= 0 || audit.ActorUserID <= 0 || audit.ResourceID <= 0 || audit.EventKey == "" || audit.Action == "" || audit.Quota < 0 || audit.Quota > common.MaxWalletQuota {
		return ErrAgentQuotaInvalid
	}
	if audit.AmountMinor == "" {
		audit.AmountMinor = "0"
	}
	return tx.Create(&audit).Error
}
func GetAgentPurchaseAudit(scope RedemptionScope, owner, start, count int) ([]AgentPurchaseAudit, int64, error) {
	if !scope.valid() {
		return nil, 0, ErrAgentRedemptionPermission
	}
	if !scope.IsAdmin() {
		owner = scope.UserID
	}
	query := DB.Model(&AgentPurchaseAudit{})
	if owner > 0 {
		query = query.Where("agent_user_id = ?", owner)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]AgentPurchaseAudit, 0)
	err := query.Order("id DESC").Offset(max(start, 0)).Limit(min(max(count, 1), 100)).Find(&rows).Error
	if !scope.IsAdmin() {
		for i := range rows {
			rows[i].EvidenceReference = ""
			rows[i].ExternalReference = ""
		}
	}
	return rows, total, err
}
