package model

import "gorm.io/gorm"

// The review reason is retained as an immutable fact, not just hidden behind a
// request hash. Sensitive dossier, bank and proof-document references are never
// copied here; only their hashes belong in the operator-visible audit.
type AgentCreditAudit struct {
	ID             int64  `json:"id" gorm:"primaryKey"`
	RequestID      string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	AgentUserID    int    `json:"agent_user_id" gorm:"index"`
	ActorUserID    int    `json:"actor_user_id"`
	Action         string `json:"action" gorm:"type:varchar(64);not null"`
	ResourceType   string `json:"resource_type" gorm:"type:varchar(32)"`
	ResourceID     string `json:"resource_id" gorm:"type:varchar(32)"`
	State          string `json:"state" gorm:"type:varchar(32)"`
	PolicyVersion  int64  `json:"policy_version"`
	ProfileVersion int64  `json:"profile_version"`
	Reason         string `json:"reason" gorm:"type:varchar(255)"`
	EvidenceHash   string `json:"evidence_hash" gorm:"type:varchar(64)"`
	ParametersHash string `json:"parameters_hash" gorm:"type:varchar(64)"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCreditAudit) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentCreditAudit) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }
