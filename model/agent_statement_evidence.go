package model

import (
	"errors"
	"strconv"

	"gorm.io/gorm"
)

// Supplemental evidence records are additive. They never edit a historical
// receipt, decide a payment outcome, or override a preexisting bound fact.
type AgentStatementEvidence struct {
	ID                 int64  `json:"id" gorm:"primaryKey"`
	ReceiptType        string `json:"receipt_type" gorm:"type:varchar(24);not null;uniqueIndex:idx_statement_evidence_version,priority:1"`
	ReceiptID          int64  `json:"receipt_id" gorm:"not null;uniqueIndex:idx_statement_evidence_version,priority:2"`
	Version            int64  `json:"version" gorm:"not null;uniqueIndex:idx_statement_evidence_version,priority:3"`
	DossierReference   string `json:"-" gorm:"type:varchar(128);not null"`
	CounterpartyDigest string `json:"counterparty_digest" gorm:"type:char(64);not null"`
	PaidAt             int64  `json:"paid_at" gorm:"not null"`
	EvidenceReference  string `json:"evidence_reference" gorm:"type:varchar(128);not null"`
	BaseFactsDigest    string `json:"base_facts_digest" gorm:"type:char(64);not null"`
	OwnerUserID        int    `json:"owner_user_id" gorm:"not null"`
	ProposedBy         int    `json:"proposed_by" gorm:"not null"`
	PolicyVersion      int64  `json:"policy_version" gorm:"not null"`
	CreatedAt          int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentStatementEvidence) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentStatementEvidence) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentStatementEvidenceDecision struct {
	ConfirmedReceiptKey *string `json:"-" gorm:"type:varchar(64);uniqueIndex"`
	ID                  int64   `json:"id" gorm:"primaryKey"`
	EvidenceID          int64   `json:"evidence_id" gorm:"not null;uniqueIndex"`
	State               string  `json:"state" gorm:"type:varchar(16);not null"`
	ReviewedBy          int     `json:"reviewed_by" gorm:"not null"`
	CreatedAt           int64   `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentStatementEvidenceDecision) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentStatementEvidenceDecision) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentStatementEvidenceView struct {
	AgentStatementEvidence
	State    string                          `json:"state"`
	Decision *AgentStatementEvidenceDecision `json:"decision,omitempty"`
}

func confirmedAgentStatementEvidenceTx(tx *gorm.DB, kind string, id int64) (*AgentStatementEvidence, error) {
	var rows []AgentStatementEvidence
	err := tx.Model(&AgentStatementEvidence{}).Joins("JOIN agent_statement_evidence_decisions d ON d.evidence_id = agent_statement_evidences.id").Where("agent_statement_evidences.receipt_type = ? AND agent_statement_evidences.receipt_id = ? AND d.state = ?", kind, id, "confirmed").Limit(2).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) > 1 {
		return nil, ErrAgentStatementConflict
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// Snapshot uses raw receipt facts, not supplementary facts, to prevent a
// proposal from circularly validating itself or overriding subsequent facts.
type agentStatementEvidenceBase struct {
	Target             agentStatementTarget `json:"target"`
	CounterpartyDigest string               `json:"counterparty_digest"`
	PaidAt             int64                `json:"paid_at"`
}

func agentStatementEvidenceBaseTx(tx *gorm.DB, kind string, id int64) (agentStatementEvidenceBase, error) {
	var base agentStatementEvidenceBase
	target, err := agentStatementTargetRawTx(tx, kind, id)
	if err != nil {
		return base, err
	}
	base.Target = target
	if !target.Verified || target.ReceiptID <= 0 {
		return base, ErrAgentStatementUnresolved
	}
	switch kind {
	case "incoming_receipt":
		var r AgentPaymentReceipt
		if err := tx.First(&r, id).Error; err != nil {
			return base, err
		}
		base.CounterpartyDigest = r.CounterpartyDigest
		base.PaidAt = r.PaidAt
	case "outgoing_receipt":
		var r AgentOutboundPaymentReceipt
		if err := tx.First(&r, id).Error; err != nil {
			return base, err
		}
		base.CounterpartyDigest = r.BeneficiaryDigest
		base.PaidAt = r.PaidAt
	default:
		return base, ErrAgentStatementInvalid
	}
	return base, nil
}
func readAgentStatementEvidenceViewTx(tx *gorm.DB, id int64) (AgentStatementEvidenceView, error) {
	var view AgentStatementEvidenceView
	if err := tx.First(&view.AgentStatementEvidence, id).Error; err != nil {
		return view, err
	}
	var decision AgentStatementEvidenceDecision
	err := tx.Where("evidence_id = ?", id).First(&decision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		view.State = "proposed"
		return view, nil
	}
	if err != nil {
		return view, err
	}
	view.State = decision.State
	view.Decision = &decision
	return view, nil
}
func agentStatementEvidenceContextTx(tx *gorm.DB, identity AuthSessionIdentity, p *AgentPolicy, action string, c AgentStatementCommand) (AgentTransactionContext, error) {
	kind, id := c.ReceiptType, c.ReceiptID
	version := int64(0)
	targetID := kind + ":" + strconv.FormatInt(id, 10)
	if action != "statement.evidence.propose" {
		view, err := readAgentStatementEvidenceViewTx(tx, c.EvidenceID)
		if err != nil {
			return AgentTransactionContext{}, err
		}
		if view.State != "proposed" || view.Version != c.ExpectedVersion {
			return AgentTransactionContext{}, ErrAgentStatementConflict
		}
		kind = view.ReceiptType
		id = view.ReceiptID
		version = view.Version
		targetID = "evidence:" + strconv.FormatInt(view.ID, 10)
	}
	base, err := agentStatementEvidenceBaseTx(tx, kind, id)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	return agentBusinessContextTx(tx, p, identity.UserID, action, c.RequestID, base.Target.Currency, strconv.FormatInt(base.Target.AmountMinor, 10), 0, "statement_evidence", targetID, version, struct {
		Command AgentStatementCommand      `json:"command"`
		Base    agentStatementEvidenceBase `json:"base"`
	}{c, base})
}
func executeAgentStatementEvidenceTx(tx *gorm.DB, actor int, p *AgentPolicy, action string, c AgentStatementCommand) (AgentStatementResult, error) {
	var proposal AgentStatementEvidence
	if action == "statement.evidence.propose" {
		base, err := agentStatementEvidenceBaseTx(tx, c.ReceiptType, c.ReceiptID)
		if err != nil {
			return AgentStatementResult{}, err
		}
		if actor == base.Target.OwnerUserID {
			return AgentStatementResult{}, ErrAgentStatementReview
		}
		if base.CounterpartyDigest != "" && base.PaidAt > 0 {
			return AgentStatementResult{}, ErrAgentStatementConflict
		}
		digest := agentStatementDigest([]byte(c.DossierReference))
		if base.CounterpartyDigest != "" && base.CounterpartyDigest != digest || base.PaidAt > 0 && base.PaidAt != c.PaidAt {
			return AgentStatementResult{}, ErrAgentStatementConflict
		}
		// A frozen withdrawal/original-sender payee is also a non-overridable fact.
		if c.ReceiptType == "outgoing_receipt" && base.Target.BeneficiaryDigest != "" && base.Target.BeneficiaryDigest != digest {
			return AgentStatementResult{}, ErrAgentStatementConflict
		}
		existing, err := confirmedAgentStatementEvidenceTx(tx, c.ReceiptType, c.ReceiptID)
		if err != nil {
			return AgentStatementResult{}, err
		}
		if existing != nil {
			return AgentStatementResult{}, ErrAgentStatementConflict
		}
		var last AgentStatementEvidence
		err = tx.Where("receipt_type = ? AND receipt_id = ?", c.ReceiptType, c.ReceiptID).Order("version DESC").First(&last).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return AgentStatementResult{}, err
		}
		if last.ID > 0 {
			view, err := readAgentStatementEvidenceViewTx(tx, last.ID)
			if err != nil {
				return AgentStatementResult{}, err
			}
			if view.State == "proposed" {
				return AgentStatementResult{}, ErrAgentStatementConflict
			}
		}
		facts, err := AgentTransactionParametersHash(base)
		if err != nil {
			return AgentStatementResult{}, err
		}
		proposal = AgentStatementEvidence{ReceiptType: c.ReceiptType, ReceiptID: c.ReceiptID, Version: last.Version + 1, DossierReference: c.DossierReference, CounterpartyDigest: digest, PaidAt: c.PaidAt, EvidenceReference: c.EvidenceReference, BaseFactsDigest: facts, OwnerUserID: base.Target.OwnerUserID, ProposedBy: actor, PolicyVersion: p.ID}
		if err := tx.Create(&proposal).Error; err != nil {
			return AgentStatementResult{}, err
		}
		return AgentStatementResult{ResourceType: "statement_evidence", ResourceID: proposal.ID, State: "proposed"}, nil
	}
	view, err := readAgentStatementEvidenceViewTx(tx, c.EvidenceID)
	if err != nil {
		return AgentStatementResult{}, err
	}
	proposal = view.AgentStatementEvidence
	if actor == proposal.ProposedBy || actor == proposal.OwnerUserID {
		return AgentStatementResult{}, ErrAgentStatementReview
	}
	if view.State != "proposed" || view.Version != c.ExpectedVersion {
		return AgentStatementResult{}, ErrAgentStatementConflict
	}
	state := "rejected"
	if action == "statement.evidence.confirm" {
		if proposal.PolicyVersion != p.ID {
			return AgentStatementResult{}, ErrAgentPolicyChanged
		}
		// Checker independently supplies the same documentary and dossier facts.
		if c.DossierReference != proposal.DossierReference || c.PaidAt != proposal.PaidAt || c.EvidenceReference != proposal.EvidenceReference {
			return AgentStatementResult{}, ErrAgentStatementConflict
		}
		base, err := agentStatementEvidenceBaseTx(tx, proposal.ReceiptType, proposal.ReceiptID)
		if err != nil {
			return AgentStatementResult{}, err
		}
		facts, err := AgentTransactionParametersHash(base)
		if err != nil {
			return AgentStatementResult{}, err
		}
		if facts != proposal.BaseFactsDigest {
			return AgentStatementResult{}, ErrAgentStatementConflict
		}
		existing, err := confirmedAgentStatementEvidenceTx(tx, proposal.ReceiptType, proposal.ReceiptID)
		if err != nil {
			return AgentStatementResult{}, err
		}
		if existing != nil {
			return AgentStatementResult{}, ErrAgentStatementConflict
		}
		state = "confirmed"
	}
	var confirmedKey *string
	if state == "confirmed" {
		key := agentStatementDigest([]byte(proposal.ReceiptType + ":" + strconv.FormatInt(proposal.ReceiptID, 10)))
		confirmedKey = &key
	}
	if err := tx.Create(&AgentStatementEvidenceDecision{ConfirmedReceiptKey: confirmedKey, EvidenceID: proposal.ID, State: state, ReviewedBy: actor}).Error; err != nil {
		return AgentStatementResult{}, err
	}
	return AgentStatementResult{ResourceType: "statement_evidence", ResourceID: proposal.ID, State: state}, nil
}
func GetAgentStatementSupplementaryEvidence(identity AuthSessionIdentity, id int64) (*AgentStatementEvidenceView, error) {
	var view AgentStatementEvidenceView
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		var err error
		view, err = readAgentStatementEvidenceViewTx(tx, id)
		return err
	})
	return &view, err
}

// ResolveAgentPaymentCounterpartyDigestTx is a READ-ONLY bridge for an existing
// manual refund's own authorization workflow. It returns only proven facts of
// a final incoming receipt, never changes a receipt or moves funds. The caller
// must perform its normal owner-bound authorization and independent approval.
func ResolveAgentPaymentCounterpartyDigestTx(tx *gorm.DB, receiptID int64) (string, error) {
	if tx == nil || receiptID <= 0 {
		return "", ErrAgentStatementInvalid
	}
	base, err := agentStatementEvidenceBaseTx(tx, "incoming_receipt", receiptID)
	if err != nil {
		return "", err
	}
	if base.CounterpartyDigest != "" {
		if !validAgentStatementDigest(base.CounterpartyDigest) {
			return "", ErrAgentStatementConflict
		}
		return base.CounterpartyDigest, nil
	}
	supplement, err := confirmedAgentStatementEvidenceTx(tx, "incoming_receipt", receiptID)
	if err != nil {
		return "", err
	}
	if supplement == nil || !validAgentStatementDigest(supplement.CounterpartyDigest) {
		return "", ErrAgentStatementUnresolved
	}
	return supplement.CounterpartyDigest, nil
}
