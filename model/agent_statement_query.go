package model

import (
	"gorm.io/gorm"
)

type AgentStatementQuery struct {
	PeriodID int64
	ImportID int64
	LineID   int64
	State    string
	Offset   int
	Limit    int
}

func ListAgentStatements(identity AuthSessionIdentity, kind string, q AgentStatementQuery) (any, int64, error) {
	if q.Offset < 0 || q.Limit < 1 || q.Limit > 100 || q.PeriodID < 0 || q.ImportID < 0 || q.LineID < 0 {
		return nil, 0, ErrAgentStatementInvalid
	}
	var result any
	var total int64
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		switch kind {
		case "evidence":
			query := tx.Model(&AgentStatementEvidence{})
			if q.State != "" {
				if q.State != "proposed" && q.State != "confirmed" && q.State != "rejected" {
					return ErrAgentStatementInvalid
				}
				query = query.Joins("LEFT JOIN agent_statement_evidence_decisions d ON d.evidence_id = agent_statement_evidences.id")
				if q.State == "proposed" {
					query = query.Where("d.id IS NULL")
				} else {
					query = query.Where("d.state = ?", q.State)
				}
			}

			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []AgentStatementEvidence{}
			if err := query.Order("agent_statement_evidences.id DESC").Offset(q.Offset).Limit(q.Limit).Find(&rows).Error; err != nil {
				return err
			}
			views := []AgentStatementEvidenceView{}
			for _, row := range rows {
				view, err := readAgentStatementEvidenceViewTx(tx, row.ID)
				if err != nil {
					return err
				}
				views = append(views, view)
			}
			result = views
		case "periods":
			query := tx.Model(&AgentStatementPeriod{})
			if q.State != "" {
				query = query.Where("state = ?", q.State)
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []AgentStatementPeriod{}
			if err := query.Order("id DESC").Offset(q.Offset).Limit(q.Limit).Find(&rows).Error; err != nil {
				return err
			}
			result = rows
		case "imports":
			query := tx.Model(&AgentStatementImport{})
			if q.PeriodID > 0 {
				query = query.Where("period_id = ?", q.PeriodID)
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []AgentStatementImport{}
			if err := query.Order("id DESC").Offset(q.Offset).Limit(q.Limit).Find(&rows).Error; err != nil {
				return err
			}
			result = rows
		case "lines":
			query := tx.Model(&AgentStatementLine{})
			if q.PeriodID > 0 {
				query = query.Where("period_id = ?", q.PeriodID)
			}
			if q.ImportID > 0 {
				query = query.Where("import_id = ?", q.ImportID)
			}
			if q.LineID > 0 {
				query = query.Where("agent_statement_lines.id = ?", q.LineID)
			}
			if q.State != "" {
				query = query.Joins("JOIN agent_statement_reviews r ON r.line_id = agent_statement_lines.id").Where("r.state = ?", q.State)
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []AgentStatementLine{}
			if err := query.Order("agent_statement_lines.id DESC").Offset(q.Offset).Limit(q.Limit).Find(&rows).Error; err != nil {
				return err
			}
			views := []AgentStatementLineView{}
			for _, row := range rows {
				var review AgentStatementReview
				if err := tx.First(&review, "line_id = ?", row.ID).Error; err != nil {
					return err
				}
				views = append(views, AgentStatementLineView{AgentStatementLine: row, Review: review})
			}
			result = views
		case "matches":
			query := tx.Model(&AgentStatementMatch{})
			if q.LineID > 0 {
				query = query.Where("line_id = ?", q.LineID)
			}
			if q.PeriodID > 0 {
				query = query.Joins("JOIN agent_statement_lines l ON l.id = agent_statement_matches.line_id").Where("l.period_id = ?", q.PeriodID)
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []AgentStatementMatch{}
			if err := query.Order("agent_statement_matches.id DESC").Offset(q.Offset).Limit(q.Limit).Find(&rows).Error; err != nil {
				return err
			}
			result = rows
		case "exceptions":
			query := tx.Model(&AgentStatementException{})
			if q.PeriodID > 0 {
				query = query.Where("period_id = ?", q.PeriodID)
			}
			if q.LineID > 0 {
				query = query.Where("line_id = ?", q.LineID)
			}
			if q.State != "" {
				query = query.Where("state = ?", q.State)
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []AgentStatementException{}
			if err := query.Order("id DESC").Offset(q.Offset).Limit(q.Limit).Find(&rows).Error; err != nil {
				return err
			}
			result = rows
		case "audit":
			query := tx.Model(&AgentStatementAudit{})
			if q.PeriodID > 0 {
				query = query.Where("period_id = ?", q.PeriodID)
			}
			if q.LineID > 0 {
				query = query.Where("line_id = ?", q.LineID)
			}
			if err := query.Count(&total).Error; err != nil {
				return err
			}
			rows := []AgentStatementAudit{}
			if err := query.Order("id DESC").Offset(q.Offset).Limit(q.Limit).Find(&rows).Error; err != nil {
				return err
			}
			result = rows
		default:
			return ErrAgentStatementInvalid
		}
		return nil
	})
	return result, total, err
}
func GetAgentStatementImport(identity AuthSessionIdentity, id int64) (*AgentStatementImport, error) {
	var result AgentStatementImport
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		return tx.First(&result, id).Error
	})
	return &result, err
}
func GetAgentStatementPeriod(identity AuthSessionIdentity, id int64) (*AgentStatementPeriod, error) {
	var result AgentStatementPeriod
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		return tx.First(&result, id).Error
	})
	return &result, err
}

// AgentStatementEvidenceDescriptor is intentionally NOT a payment callback or
// a payment success command. It points reviewers at the already confirmed bank
// evidence and exact existing business number. No external-reference secret or
// payout credential is returned. UNKNOWN requires its existing explicit result
// workflow; this descriptor cannot settle it.
type AgentStatementEvidenceDescriptor struct {
	LineID             int64  `json:"line_id"`
	MatchID            int64  `json:"match_id"`
	BusinessReference  string `json:"business_reference"`
	EvidenceReference  string `json:"evidence_reference"`
	SourceDigest       string `json:"source_digest"`
	Currency           string `json:"currency"`
	AmountMinor        int64  `json:"amount_minor,string"`
	ReconciliationOnly bool   `json:"reconciliation_only"`
}

func GetAgentStatementEvidence(identity AuthSessionIdentity, lineID int64) (*AgentStatementEvidenceDescriptor, error) {
	var result AgentStatementEvidenceDescriptor
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		var line AgentStatementLine
		if err := tx.First(&line, lineID).Error; err != nil {
			return err
		}
		var confirmed AgentStatementConfirmedMatch
		if err := tx.Where("line_id = ?", lineID).First(&confirmed).Error; err != nil {
			return err
		}
		var imported AgentStatementImport
		if err := tx.First(&imported, line.ImportID).Error; err != nil {
			return err
		}
		result = AgentStatementEvidenceDescriptor{LineID: line.ID, MatchID: confirmed.MatchID, BusinessReference: line.BusinessReference, EvidenceReference: line.EvidenceReference, SourceDigest: imported.SourceDigest, Currency: line.Currency, AmountMinor: line.AmountMinor, ReconciliationOnly: true}
		return nil
	})
	return &result, err
}

func GetAgentStatementLine(identity AuthSessionIdentity, id int64) (*AgentStatementLineView, error) {
	var view AgentStatementLineView
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		if err := tx.First(&view.AgentStatementLine, id).Error; err != nil {
			return err
		}
		return tx.First(&view.Review, "line_id = ?", id).Error
	})
	return &view, err
}
func GetAgentStatementMatch(identity AuthSessionIdentity, id int64) (*AgentStatementMatch, error) {
	var row AgentStatementMatch
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		return tx.First(&row, id).Error
	})
	return &row, err
}
func GetAgentStatementException(identity AuthSessionIdentity, id int64) (*AgentStatementException, error) {
	var row AgentStatementException
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		return tx.First(&row, id).Error
	})
	return &row, err
}
