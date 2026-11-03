package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const AgentStatementMaximumBodyBytes = 64 << 10
const AgentStatementMaximumRows = 200

var (
	ErrAgentStatementInvalid    = errors.New("Invalid manual bank statement request")
	ErrAgentStatementConflict   = errors.New("Statement state changed or external transaction is already reconciled")
	ErrAgentStatementReview     = errors.New("Statement reconciliation requires independent maker and checker")
	ErrAgentStatementUnresolved = errors.New("Statement has unresolved lines or exceptions; no funds were changed")
)

// No raw bank account, credentials or customer identity are stored here. These
// are references to a separately controlled dossier/evidence registry.
type AgentStatementPeriod struct {
	ID                      int64  `json:"id" gorm:"primaryKey"`
	AccountDossierReference string `json:"account_dossier_reference" gorm:"type:varchar(128);not null;index"`
	Currency                string `json:"currency" gorm:"type:varchar(3);not null"`
	StartsAt                int64  `json:"starts_at" gorm:"not null"`
	EndsAt                  int64  `json:"ends_at" gorm:"not null"`
	State                   string `json:"state" gorm:"type:varchar(16);not null"`
	Version                 int64  `json:"version" gorm:"not null"`
	CreatedBy               int    `json:"created_by" gorm:"not null"`
	ClosedBy                int    `json:"closed_by"`
	CreatedAt               int64  `json:"created_at" gorm:"autoCreateTime"`
}
type AgentStatementImport struct {
	ID                      int64  `json:"id" gorm:"primaryKey"`
	PeriodID                int64  `json:"period_id" gorm:"not null;index"`
	SourceDigest            string `json:"source_digest" gorm:"type:char(64);not null;uniqueIndex"`
	ParsedDigest            string `json:"parsed_digest" gorm:"type:char(64);not null"`
	SourceReference         string `json:"source_reference" gorm:"type:varchar(128);not null"`
	AccountDossierReference string `json:"account_dossier_reference" gorm:"type:varchar(128);not null;index"`
	RowCount                int    `json:"row_count" gorm:"not null"`
	ImportedBy              int    `json:"imported_by" gorm:"not null"`
	CreatedAt               int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentStatementImport) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentStatementImport) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentStatementLineInput struct {
	ExternalReference string `json:"external_reference"`
	OccurredAt        int64  `json:"occurred_at"`
	Direction         string `json:"direction"`
	Currency          string `json:"currency"`
	AmountMinor       int64  `json:"amount_minor,string"`
	BusinessReference string `json:"business_reference"`
	BeneficiaryDigest string `json:"beneficiary_digest"`
	EvidenceReference string `json:"evidence_reference"`
}
type AgentStatementLine struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	ImportID          int64  `json:"import_id" gorm:"not null;uniqueIndex:idx_agent_statement_import_row,priority:1"`
	RowNumber         int    `json:"row_number" gorm:"not null;uniqueIndex:idx_agent_statement_import_row,priority:2"`
	PeriodID          int64  `json:"period_id" gorm:"not null;index"`
	ExternalKey       string `json:"-" gorm:"type:char(64);not null;index"`
	ExternalReference string `json:"-" gorm:"type:varchar(128);not null"`
	OccurredAt        int64  `json:"occurred_at" gorm:"not null"`
	Direction         string `json:"direction" gorm:"type:varchar(8);not null"`
	Currency          string `json:"currency" gorm:"type:varchar(3);not null"`
	AmountMinor       int64  `json:"amount_minor,string" gorm:"not null"`
	BusinessReference string `json:"business_reference" gorm:"type:varchar(128);not null"`
	BeneficiaryDigest string `json:"beneficiary_digest" gorm:"type:varchar(64)"`
	EvidenceReference string `json:"evidence_reference" gorm:"type:varchar(128);not null"`
	RowDigest         string `json:"row_digest" gorm:"type:char(64);not null"`
}

func (*AgentStatementLine) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentStatementLine) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentStatementReview struct {
	LineID     int64  `json:"line_id" gorm:"primaryKey;autoIncrement:false"`
	State      string `json:"state" gorm:"type:varchar(16);not null;index"`
	Version    int64  `json:"version" gorm:"not null"`
	MatchID    int64  `json:"match_id"`
	ReviewedBy int    `json:"reviewed_by"`
}
type AgentStatementMatch struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	LineID            int64  `json:"line_id" gorm:"not null;index"`
	TargetType        string `json:"target_type" gorm:"type:varchar(32);not null"`
	TargetID          int64  `json:"target_id" gorm:"not null"`
	TargetDigest      string `json:"target_digest" gorm:"type:char(64);not null"`
	BusinessReference string `json:"business_reference" gorm:"type:varchar(128);not null"`
	OwnerUserID       int    `json:"owner_user_id" gorm:"not null"`
	ProposedBy        int    `json:"proposed_by" gorm:"not null"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentStatementMatch) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentStatementMatch) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

// The confirmed registry is reconciliation evidence ONLY. Its two unique keys
// preclude both one external line closing two obligations and two imports
// closing the same obligation. It never creates a receipt or wallet movement.
type AgentStatementConfirmedMatch struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	MatchID       int64  `json:"match_id" gorm:"not null;uniqueIndex"`
	LineID        int64  `json:"line_id" gorm:"not null;uniqueIndex"`
	ExternalKey   string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	ObligationKey string `json:"obligation_key" gorm:"type:char(64);not null;uniqueIndex"`
	ConfirmedBy   int    `json:"confirmed_by" gorm:"not null"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentStatementConfirmedMatch) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentStatementConfirmedMatch) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }

type AgentStatementException struct {
	ReplacementLineID           int64  `json:"replacement_line_id"`
	ResolutionEvidenceReference string `json:"resolution_evidence_reference" gorm:"type:varchar(128)"`
	ID                          int64  `json:"id" gorm:"primaryKey"`
	LineID                      int64  `json:"line_id" gorm:"not null;index"`
	PeriodID                    int64  `json:"period_id" gorm:"not null;index"`
	Code                        string `json:"code" gorm:"type:varchar(32);not null"`
	State                       string `json:"state" gorm:"type:varchar(16);not null"`
	RaisedBy                    int    `json:"raised_by" gorm:"not null"`
	ResolvedBy                  int    `json:"resolved_by"`
	Resolution                  string `json:"resolution" gorm:"type:text"`
	CreatedAt                   int64  `json:"created_at" gorm:"autoCreateTime"`
}
type AgentStatementAudit struct {
	ID           int64  `json:"id" gorm:"primaryKey"`
	Action       string `json:"action" gorm:"type:varchar(40);not null"`
	RequestID    string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	ActorUserID  int    `json:"actor_user_id" gorm:"not null"`
	Signature    string `json:"-" gorm:"type:char(64);not null"`
	PeriodID     int64  `json:"period_id" gorm:"not null;index"`
	LineID       int64  `json:"line_id"`
	ResourceType string `json:"resource_type" gorm:"type:varchar(32);not null"`
	ResourceID   int64  `json:"resource_id" gorm:"not null"`
	ResultJSON   string `json:"-" gorm:"type:text;not null"`
	Reason       string `json:"reason" gorm:"type:varchar(255);not null"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentStatementAudit) BeforeUpdate(*gorm.DB) error { return ErrAgentLedgerImmutable }
func (*AgentStatementAudit) BeforeDelete(*gorm.DB) error { return ErrAgentLedgerImmutable }
func AgentStatementModels() []any {
	return []any{&AgentStatementPeriod{}, &AgentStatementImport{}, &AgentStatementLine{}, &AgentStatementReview{}, &AgentStatementMatch{}, &AgentStatementConfirmedMatch{}, &AgentStatementException{}, &AgentStatementAudit{}, &AgentStatementEvidence{}, &AgentStatementEvidenceDecision{}}
}

type AgentStatementCommand struct {
	ReplacementLineID       int64                     `json:"replacement_line_id"`
	ReceiptType             string                    `json:"receipt_type"`
	ReceiptID               int64                     `json:"receipt_id"`
	EvidenceID              int64                     `json:"evidence_id"`
	DossierReference        string                    `json:"dossier_reference"`
	PaidAt                  int64                     `json:"paid_at"`
	EvidenceReference       string                    `json:"evidence_reference"`
	RequestID               string                    `json:"request_id"`
	PeriodID                int64                     `json:"period_id"`
	ExpectedVersion         int64                     `json:"expected_version"`
	Reason                  string                    `json:"reason"`
	AccountDossierReference string                    `json:"account_dossier_reference"`
	Currency                string                    `json:"currency"`
	StartsAt                int64                     `json:"starts_at"`
	EndsAt                  int64                     `json:"ends_at"`
	SourceReference         string                    `json:"source_reference"`
	SourceDigest            string                    `json:"source_digest"`
	CSV                     string                    `json:"csv"`
	Lines                   []AgentStatementLineInput `json:"lines"`
	LineID                  int64                     `json:"line_id"`
	MatchID                 int64                     `json:"match_id"`
	TargetType              string                    `json:"target_type"`
	TargetID                int64                     `json:"target_id"`
	ExceptionID             int64                     `json:"exception_id"`
	Resolution              string                    `json:"resolution"`
}
type AgentStatementResult struct {
	ResourceType  string `json:"resource_type"`
	ResourceID    int64  `json:"resource_id"`
	State         string `json:"state"`
	PeriodID      int64  `json:"period_id"`
	LineID        int64  `json:"line_id"`
	ExceptionCode string `json:"exception_code,omitempty"`
}
type AgentStatementLineView struct {
	AgentStatementLine
	Review AgentStatementReview `json:"review"`
}

func agentStatementDigest(data []byte) string {
	v := sha256.Sum256(data)
	return hex.EncodeToString(v[:])
}
func validAgentStatementDigest(s string) bool {
	d, e := hex.DecodeString(s)
	return e == nil && len(d) == 32 && strings.ToLower(s) == s
}
func validateAgentStatementLine(l AgentStatementLineInput) error {
	if !validAgentDossierReference(l.ExternalReference) || !validAgentDossierReference(l.EvidenceReference) || !validAgentDossierReference(l.BusinessReference) || l.OccurredAt <= 0 || l.OccurredAt > common.GetTimestamp() || l.AmountMinor <= 0 || l.AmountMinor > int64(common.MaxWalletQuota) || (l.Direction != "incoming" && l.Direction != "outgoing") {
		return ErrAgentStatementInvalid
	}
	if _, ok := AgentCurrencyExponent(l.Currency); !ok {
		return ErrAgentStatementInvalid
	}
	if l.BeneficiaryDigest != "" && !validAgentStatementDigest(l.BeneficiaryDigest) {
		return ErrAgentStatementInvalid
	}
	if l.Direction == "outgoing" && l.BeneficiaryDigest == "" {
		return ErrAgentStatementInvalid
	}
	return nil
}
func ParseAgentStatementCSV(raw string) ([]AgentStatementLineInput, error) {
	if len(raw) == 0 || len(raw) > 48<<10 || !utf8.ValidString(raw) || strings.ContainsRune(raw, '\x00') {
		return nil, ErrAgentStatementInvalid
	}
	r := csv.NewReader(strings.NewReader(raw))
	r.FieldsPerRecord = 8
	r.ReuseRecord = false
	header, err := r.Read()
	if err != nil {
		return nil, ErrAgentStatementInvalid
	}
	if strings.Join(header, ",") != "external_reference,occurred_at,direction,currency,amount_minor,business_reference,beneficiary_digest,evidence_reference" {
		return nil, ErrAgentStatementInvalid
	}
	rows := []AgentStatementLineInput{}
	for {
		values, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(rows) >= AgentStatementMaximumRows {
			return nil, ErrAgentStatementInvalid
		}
		at, err := strconv.ParseInt(values[1], 10, 64)
		if err != nil || strconv.FormatInt(at, 10) != values[1] {
			return nil, ErrAgentStatementInvalid
		}
		amount, err := strconv.ParseInt(values[4], 10, 64)
		if err != nil || strconv.FormatInt(amount, 10) != values[4] {
			return nil, ErrAgentStatementInvalid
		}
		line := AgentStatementLineInput{ExternalReference: values[0], OccurredAt: at, Direction: values[2], Currency: values[3], AmountMinor: amount, BusinessReference: values[5], BeneficiaryDigest: values[6], EvidenceReference: values[7]}
		if validateAgentStatementLine(line) != nil {
			return nil, ErrAgentStatementInvalid
		}
		rows = append(rows, line)
	}
	if len(rows) == 0 {
		return nil, ErrAgentStatementInvalid
	}
	return rows, nil
}
func normalizeAgentStatementCommand(action string, c AgentStatementCommand) (AgentStatementCommand, error) {
	if validateAgentRequestID(c.RequestID) != nil || !validAgentCommercialReason(c.Reason) {
		return c, ErrAgentStatementInvalid
	}
	switch action {
	case "statement.evidence.propose":
		if (c.ReceiptType != "incoming_receipt" && c.ReceiptType != "outgoing_receipt") || c.ReceiptID <= 0 || !validAgentDossierReference(c.DossierReference) || !validAgentDossierReference(c.EvidenceReference) || c.PaidAt <= 0 || c.PaidAt > common.GetTimestamp() {
			return c, ErrAgentStatementInvalid
		}
	case "statement.evidence.confirm", "statement.evidence.reject":
		if c.EvidenceID <= 0 || c.ExpectedVersion <= 0 {
			return c, ErrAgentStatementInvalid
		}
		if action == "statement.evidence.confirm" && (!validAgentDossierReference(c.DossierReference) || !validAgentDossierReference(c.EvidenceReference) || c.PaidAt <= 0 || c.PaidAt > common.GetTimestamp()) {
			return c, ErrAgentStatementInvalid
		}
	case "statement.period.create":
		if c.PeriodID != 0 || c.ExpectedVersion != 0 || !validAgentDossierReference(c.AccountDossierReference) || c.StartsAt <= 0 || c.EndsAt <= c.StartsAt || c.EndsAt-c.StartsAt > 366*86400 {
			return c, ErrAgentStatementInvalid
		}
		if _, ok := AgentCurrencyExponent(c.Currency); !ok {
			return c, ErrAgentStatementInvalid
		}
	case "statement.import":
		if c.PeriodID <= 0 || c.ExpectedVersion <= 0 || !validAgentDossierReference(c.SourceReference) {
			return c, ErrAgentStatementInvalid
		}
		var source []byte
		if c.CSV != "" {
			if len(c.Lines) > 0 {
				return c, ErrAgentStatementInvalid
			}
			source = []byte(c.CSV)
			rows, err := ParseAgentStatementCSV(c.CSV)
			if err != nil {
				return c, err
			}
			c.Lines = rows
			c.CSV = ""
		} else {
			source, _ = common.Marshal(c.Lines)
		}
		if len(source) > 48<<10 || len(c.Lines) < 1 || len(c.Lines) > AgentStatementMaximumRows {
			return c, ErrAgentStatementInvalid
		}
		for _, l := range c.Lines {
			if err := validateAgentStatementLine(l); err != nil {
				return c, err
			}
		}
		digest := agentStatementDigest(source)
		if c.SourceDigest != "" && c.SourceDigest != digest {
			return c, ErrAgentStatementInvalid
		}
		c.SourceDigest = digest
	case "statement.match.propose":
		if c.LineID <= 0 || c.TargetID <= 0 || c.ExpectedVersion <= 0 {
			return c, ErrAgentStatementInvalid
		}
		switch c.TargetType {
		case "incoming_receipt", "outgoing_receipt", "purchase", "purchase_refund", "withdrawal", "credit_repayment", "deposit", "deposit_refund":
		default:
			return c, ErrAgentStatementInvalid
		}
	case "statement.match.confirm", "statement.match.reject":
		if c.LineID <= 0 || c.MatchID <= 0 || c.ExpectedVersion <= 0 {
			return c, ErrAgentStatementInvalid
		}
	case "statement.exception.resolve":
		if c.LineID <= 0 || c.ExceptionID <= 0 || c.ExpectedVersion <= 0 || (c.Resolution != "retry" && c.Resolution != "exclude_duplicate" && c.Resolution != "exclude_unrelated" && c.Resolution != "exclude_invalid_import") {
			return c, ErrAgentStatementInvalid
		}
		if c.Resolution == "exclude_invalid_import" && (c.ReplacementLineID <= 0 || !validAgentDossierReference(c.EvidenceReference)) {
			return c, ErrAgentStatementInvalid
		}
	case "statement.period.close", "statement.period.reopen":
		if c.PeriodID <= 0 || c.ExpectedVersion <= 0 {
			return c, ErrAgentStatementInvalid
		}
	default:
		return c, ErrAgentStatementInvalid
	}
	return c, nil
}

// The source digest for CSV is the exact original bytes. Internal replay and
// preparation always normalize from the original payload, never re-normalize a
// parsed command against a different serialization.
func decodeAgentStatementCommand(action, requestID string, payload []byte) (AgentStatementCommand, error) {
	var c AgentStatementCommand
	if len(payload) > AgentStatementMaximumBodyBytes || common.DecodeJsonStrict(bytes.NewReader(payload), &c) != nil {
		return c, ErrAgentStatementInvalid
	}
	c.RequestID = requestID
	return normalizeAgentStatementCommand(action, c)
}
