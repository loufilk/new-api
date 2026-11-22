package model

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

func agentStatementContextTx(tx *gorm.DB, identity AuthSessionIdentity, p *AgentPolicy, action string, c AgentStatementCommand) (AgentTransactionContext, error) {
	if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
		return AgentTransactionContext{}, err
	}
	if !p.RiskPolicyApproved {
		return AgentTransactionContext{}, ErrAgentFeatureDisabled
	}
	// Recovery only: finalized historical receipt facts may be independently
	// documented even while NEW statement importing/matching is disabled.
	if strings.HasPrefix(action, "statement.evidence.") {
		return agentStatementEvidenceContextTx(tx, identity, p, action, c)
	}
	if !p.ManualReconciliationEnabled {
		return AgentTransactionContext{}, ErrAgentFeatureDisabled
	}
	owner := identity.UserID
	currency := c.Currency
	amount := "0"
	targetID := c.AccountDossierReference
	version := int64(0)
	periodVersion := int64(0)
	targetDigest := ""
	if action != "statement.period.create" {
		var period AgentStatementPeriod
		if c.LineID > 0 {
			var line AgentStatementLine
			if err := tx.First(&line, c.LineID).Error; err != nil {
				return AgentTransactionContext{}, err
			}
			var review AgentStatementReview
			if err := tx.First(&review, "line_id = ?", line.ID).Error; err != nil {
				return AgentTransactionContext{}, err
			}
			if review.Version != c.ExpectedVersion {
				return AgentTransactionContext{}, ErrAgentStatementConflict
			}
			if err := tx.First(&period, line.PeriodID).Error; err != nil {
				return AgentTransactionContext{}, err
			}
			version = review.Version
			targetID = "line:" + strconv.FormatInt(line.ID, 10)
			currency = line.Currency
			amount = strconv.FormatInt(line.AmountMinor, 10)
			kind := c.TargetType
			id := c.TargetID
			if action == "statement.match.confirm" || action == "statement.match.reject" {
				var match AgentStatementMatch
				if err := tx.First(&match, c.MatchID).Error; err != nil {
					return AgentTransactionContext{}, err
				}
				if match.LineID != line.ID || review.MatchID != match.ID {
					return AgentTransactionContext{}, ErrAgentStatementConflict
				}
				kind = match.TargetType
				id = match.TargetID
			}
			if kind != "" {
				target, err := agentStatementTargetTx(tx, kind, id)
				if err != nil {
					return AgentTransactionContext{}, err
				}
				targetDigest, err = AgentTransactionParametersHash(target)
				if err != nil {
					return AgentTransactionContext{}, err
				}
				// Reconciliation is an administrator's evidence action, not a
				// customer funds action. The complete target (including owner)
				// is bound in targetDigest, even if the owner was deleted.
			}
		} else {
			if err := tx.First(&period, c.PeriodID).Error; err != nil {
				return AgentTransactionContext{}, err
			}
			if period.Version != c.ExpectedVersion {
				return AgentTransactionContext{}, ErrAgentStatementConflict
			}
			version = period.Version
			targetID = "period:" + strconv.FormatInt(period.ID, 10)
			currency = period.Currency
		}
		if action != "statement.period.reopen" && period.State != "open" {
			return AgentTransactionContext{}, ErrAgentStatementConflict
		}
		periodVersion = period.Version
	}
	parameters := struct {
		Command       AgentStatementCommand `json:"command"`
		PeriodVersion int64                 `json:"period_version"`
		TargetDigest  string                `json:"target_digest"`
	}{c, periodVersion, targetDigest}
	return agentBusinessContextTx(tx, p, owner, action, c.RequestID, currency, amount, 0, "agent_statement", targetID, version, parameters)
}
func PrepareAgentStatementOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (*AgentPreparedOperation, error) {
	c, err := decodeAgentStatementCommand(action, requestID, payload)
	if err != nil {
		return nil, err
	}
	var context AgentTransactionContext
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		p, e := ReadCurrentAgentPolicyTx(tx)
		if e != nil {
			return e
		}
		context, e = agentStatementContextTx(tx, identity, p, action, c)
		return e
	})
	if err != nil {
		return nil, err
	}
	exponent, _ := AgentCurrencyExponent(context.Currency)
	return &AgentPreparedOperation{VerificationContext: context, Summary: AgentOperationSummary{Action: action, AgentUserID: context.AgentUserID, Currency: context.Currency, CurrencyExponent: exponent, AmountMinor: context.AmountMinor, FeeMinor: "0", NetMinor: context.AmountMinor, TargetLabel: "Manual reconciliation only: " + context.TargetID, PolicyVersion: context.PolicyVersion}, StatusURL: "/api/agent-platform/operations/" + requestID}, nil
}
func ExecuteAgentStatement(auth AgentTransactionAuthorization, action string, input AgentStatementCommand) (*AgentStatementResult, error) {
	c, err := normalizeAgentStatementCommand(action, input)
	if err != nil {
		return nil, err
	}
	signature, err := AgentTransactionParametersHash(c)
	if err != nil {
		return nil, err
	}
	var result AgentStatementResult
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		p, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if err := requireAgentCommercialAdminTx(tx, auth.Identity()); err != nil {
			return err
		}
		var previous AgentStatementAudit
		err = tx.Where("request_id = ?", c.RequestID).First(&previous).Error
		if err == nil {
			if previous.ActorUserID != auth.Identity().UserID || previous.Action != action || previous.Signature != signature {
				return ErrAgentQuotaConflict
			}
			return common.UnmarshalJsonStr(previous.ResultJSON, &result)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		context, err := agentStatementContextTx(tx, auth.Identity(), p, action, c)
		if err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, context); err != nil {
			return err
		}
		actor := auth.Identity().UserID
		if strings.HasPrefix(action, "statement.evidence.") {
			result, err = executeAgentStatementEvidenceTx(tx, actor, p, action, c)
			if err != nil {
				return err
			}
		} else if action == "statement.period.create" {
			var overlaps int64
			if err := tx.Model(&AgentStatementPeriod{}).Where("account_dossier_reference = ? AND currency = ? AND starts_at < ? AND ends_at > ?", c.AccountDossierReference, c.Currency, c.EndsAt, c.StartsAt).Count(&overlaps).Error; err != nil {
				return err
			}
			if overlaps > 0 {
				return ErrAgentStatementConflict
			}
			period := AgentStatementPeriod{AccountDossierReference: c.AccountDossierReference, Currency: c.Currency, StartsAt: c.StartsAt, EndsAt: c.EndsAt, State: "open", Version: 1, CreatedBy: actor}
			if err := tx.Create(&period).Error; err != nil {
				return err
			}
			result = AgentStatementResult{ResourceType: "statement_period", ResourceID: period.ID, PeriodID: period.ID, State: period.State}
		} else {
			var period AgentStatementPeriod
			var line AgentStatementLine
			var review AgentStatementReview
			if c.LineID > 0 {
				if err := tx.First(&line, c.LineID).Error; err != nil {
					return err
				}
				if err := tx.First(&review, "line_id = ?", line.ID).Error; err != nil {
					return err
				}
				c.PeriodID = line.PeriodID
			}
			if err := tx.First(&period, c.PeriodID).Error; err != nil {
				return err
			}
			switch action {
			case "statement.import":
				result, err = importAgentStatementTx(tx, actor, period, c)
				if err != nil {
					return err
				}
			case "statement.match.propose":
				if review.State != "unmatched" {
					return ErrAgentStatementConflict
				}
				target, err := agentStatementTargetTx(tx, c.TargetType, c.TargetID)
				if err != nil {
					return err
				}
				if actor == target.OwnerUserID {
					return ErrAgentStatementReview
				}
				mismatch := agentStatementMismatch(line, period, target)
				if mismatch != "" {
					if err := raiseAgentStatementExceptionTx(tx, line, actor, mismatch); err != nil {
						return err
					}
					review.State = "exception"
					result = AgentStatementResult{ResourceType: "statement_line", ResourceID: line.ID, LineID: line.ID, PeriodID: period.ID, State: "exception", ExceptionCode: mismatch}
				} else {
					digest, err := AgentTransactionParametersHash(target)
					if err != nil {
						return err
					}
					match := AgentStatementMatch{LineID: line.ID, TargetType: c.TargetType, TargetID: c.TargetID, TargetDigest: digest, BusinessReference: target.BusinessReference, OwnerUserID: target.OwnerUserID, ProposedBy: actor}
					if err := tx.Create(&match).Error; err != nil {
						return err
					}
					review.State = "proposed"
					review.MatchID = match.ID
					result = AgentStatementResult{ResourceType: "statement_match", ResourceID: match.ID, LineID: line.ID, PeriodID: period.ID, State: "proposed"}
				}
			case "statement.match.confirm", "statement.match.reject":
				if review.State != "proposed" || review.MatchID != c.MatchID {
					return ErrAgentStatementConflict
				}
				var match AgentStatementMatch
				if err := tx.First(&match, c.MatchID).Error; err != nil {
					return err
				}
				var imported AgentStatementImport
				if err := tx.First(&imported, line.ImportID).Error; err != nil {
					return err
				}
				if actor == match.ProposedBy || actor == imported.ImportedBy || actor == match.OwnerUserID {
					return ErrAgentStatementReview
				}
				if action == "statement.match.reject" {
					review.State = "unmatched"
					review.MatchID = 0
					review.ReviewedBy = actor
					result = AgentStatementResult{ResourceType: "statement_line", ResourceID: line.ID, LineID: line.ID, PeriodID: period.ID, State: "unmatched"}
					break
				}
				target, err := agentStatementTargetTx(tx, match.TargetType, match.TargetID)
				if err != nil {
					return err
				}
				digest, err := AgentTransactionParametersHash(target)
				if err != nil {
					return err
				}
				mismatch := agentStatementMismatch(line, period, target)
				if mismatch == "" && digest != match.TargetDigest {
					mismatch = "state_conflict"
				}
				var conflicts int64
				if err := tx.Model(&AgentStatementConfirmedMatch{}).Where("external_key = ? OR obligation_key = ?", line.ExternalKey, agentStatementObligationKey(target)).Count(&conflicts).Error; err != nil {
					return err
				}
				if conflicts > 0 {
					mismatch = "duplicate"
				}
				if mismatch != "" {
					if err := raiseAgentStatementExceptionTx(tx, line, actor, mismatch); err != nil {
						return err
					}
					review.State = "exception"
					result = AgentStatementResult{ResourceType: "statement_line", ResourceID: line.ID, LineID: line.ID, PeriodID: period.ID, State: "exception", ExceptionCode: mismatch}
					break
				}
				confirmed := AgentStatementConfirmedMatch{MatchID: match.ID, LineID: line.ID, ExternalKey: line.ExternalKey, ObligationKey: agentStatementObligationKey(target), ConfirmedBy: actor}
				if err := tx.Create(&confirmed).Error; err != nil {
					return err
				}
				review.State = "confirmed"
				review.ReviewedBy = actor
				result = AgentStatementResult{ResourceType: "statement_match", ResourceID: match.ID, LineID: line.ID, PeriodID: period.ID, State: "confirmed"}
			case "statement.exception.resolve":
				if review.State != "exception" {
					return ErrAgentStatementConflict
				}
				var exception AgentStatementException
				if err := tx.First(&exception, c.ExceptionID).Error; err != nil {
					return err
				}
				if exception.LineID != line.ID || exception.State != "open" {
					return ErrAgentStatementConflict
				}
				var imported AgentStatementImport
				if err := tx.First(&imported, line.ImportID).Error; err != nil {
					return err
				}
				if actor == exception.RaisedBy || actor == imported.ImportedBy {
					return ErrAgentStatementReview
				}
				switch c.Resolution {
				case "retry":
					review.State = "unmatched"
					review.MatchID = 0
				case "exclude_duplicate":
					if exception.Code != "duplicate" {
						return ErrAgentStatementInvalid
					}
					var count int64
					if err := tx.Model(&AgentStatementConfirmedMatch{}).Where("external_key = ? AND line_id <> ?", line.ExternalKey, line.ID).Count(&count).Error; err != nil {
						return err
					}
					if count != 1 {
						return ErrAgentStatementUnresolved
					}
					review.State = "excluded"
					var originalMatch AgentStatementConfirmedMatch
					if err := tx.Where("external_key = ? AND line_id <> ?", line.ExternalKey, line.ID).First(&originalMatch).Error; err != nil {
						return err
					}
					var originalLine AgentStatementLine
					if err := tx.First(&originalLine, originalMatch.LineID).Error; err != nil {
						return err
					}
					if !sameAgentStatementEconomics(line, originalLine) {
						return ErrAgentStatementUnresolved
					}
				case "exclude_invalid_import":
					var correct AgentStatementConfirmedMatch
					if err := tx.Where("line_id = ? AND external_key = ?", c.ReplacementLineID, line.ExternalKey).First(&correct).Error; err != nil {
						return ErrAgentStatementUnresolved
					}
					if correct.LineID == line.ID {
						return ErrAgentStatementConflict
					}
					review.State = "excluded"
				case "exclude_unrelated":
					// A mismatch against a real obligation cannot be written off as unrelated.
					// Only an importer-declared unallocated bank item may be excluded.
					if exception.Code != "unallocated" || !stringsHasUnallocatedPrefix(line.BusinessReference) {
						return ErrAgentStatementInvalid
					}
					review.State = "excluded"
				}
				exception.State = "resolved"
				exception.ResolvedBy = actor
				exception.Resolution = c.Resolution + ": " + c.Reason
				exception.ReplacementLineID = c.ReplacementLineID
				exception.ResolutionEvidenceReference = c.EvidenceReference
				if err := tx.Save(&exception).Error; err != nil {
					return err
				}
				review.ReviewedBy = actor
				result = AgentStatementResult{ResourceType: "statement_line", ResourceID: line.ID, LineID: line.ID, PeriodID: period.ID, State: review.State}
			case "statement.period.close":
				if actor == period.CreatedBy {
					return ErrAgentStatementReview
				}
				var made int64
				if err := tx.Model(&AgentStatementImport{}).Where("period_id = ? AND imported_by = ?", period.ID, actor).Count(&made).Error; err != nil {
					return err
				}
				if made > 0 {
					return ErrAgentStatementReview
				}
				var unresolved int64
				if err := tx.Model(&AgentStatementReview{}).Joins("JOIN agent_statement_lines l ON l.id = agent_statement_reviews.line_id").Where("l.period_id = ? AND agent_statement_reviews.state NOT IN ?", period.ID, []string{"confirmed", "excluded"}).Count(&unresolved).Error; err != nil {
					return err
				}
				if unresolved > 0 {
					return ErrAgentStatementUnresolved
				}
				if err := tx.Model(&AgentStatementException{}).Where("period_id = ? AND state = ?", period.ID, "open").Count(&unresolved).Error; err != nil {
					return err
				}
				if unresolved > 0 {
					return ErrAgentStatementUnresolved
				}
				period.State = "closed"
				period.ClosedBy = actor
				result = AgentStatementResult{ResourceType: "statement_period", ResourceID: period.ID, PeriodID: period.ID, State: "closed"}
			case "statement.period.reopen":
				if period.State != "closed" {
					return ErrAgentStatementConflict
				}
				if actor == period.ClosedBy {
					return ErrAgentStatementReview
				}
				period.State = "open"
				period.ClosedBy = 0
				result = AgentStatementResult{ResourceType: "statement_period", ResourceID: period.ID, PeriodID: period.ID, State: "open"}
			}
			if c.LineID > 0 {
				review.Version++
				if err := tx.Save(&review).Error; err != nil {
					return err
				}
			}
			period.Version++
			if err := tx.Save(&period).Error; err != nil {
				return err
			}
		}
		encoded, err := common.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Create(&AgentStatementAudit{Action: action, RequestID: c.RequestID, ActorUserID: actor, Signature: signature, PeriodID: result.PeriodID, LineID: result.LineID, ResourceType: result.ResourceType, ResourceID: result.ResourceID, ResultJSON: string(encoded), Reason: c.Reason}).Error
	})
	return &result, err
}
func stringsHasUnallocatedPrefix(s string) bool { return len(s) > 12 && s[:12] == "unallocated:" }
func importAgentStatementTx(tx *gorm.DB, actor int, period AgentStatementPeriod, c AgentStatementCommand) (AgentStatementResult, error) {
	var existing AgentStatementImport
	err := tx.Where("source_digest = ?", c.SourceDigest).First(&existing).Error
	parsed, errHash := AgentTransactionParametersHash(c.Lines)
	if errHash != nil {
		return AgentStatementResult{}, errHash
	}
	if err == nil {
		if existing.PeriodID != period.ID || existing.AccountDossierReference != period.AccountDossierReference || existing.ParsedDigest != parsed {
			return AgentStatementResult{}, ErrAgentStatementConflict
		}
		return AgentStatementResult{ResourceType: "statement_import", ResourceID: existing.ID, PeriodID: period.ID, State: "imported"}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentStatementResult{}, err
	}
	imported := AgentStatementImport{PeriodID: period.ID, SourceDigest: c.SourceDigest, ParsedDigest: parsed, SourceReference: c.SourceReference, AccountDossierReference: period.AccountDossierReference, RowCount: len(c.Lines), ImportedBy: actor}
	if err := tx.Create(&imported).Error; err != nil {
		return AgentStatementResult{}, err
	}
	for i, input := range c.Lines {
		if input.Currency != period.Currency || input.OccurredAt < period.StartsAt || input.OccurredAt >= period.EndsAt {
			return AgentStatementResult{}, ErrAgentStatementInvalid
		}
		digest, err := AgentTransactionParametersHash(input)
		if err != nil {
			return AgentStatementResult{}, err
		}
		// Exactly the incoming/outgoing shared receipt namespace, independent of
		// importer account aliases. Direction is NOT part of identity either.
		key := agentStatementDigest([]byte("bank_transfer\x00" + input.ExternalReference))
		var duplicates int64
		if err := tx.Model(&AgentStatementLine{}).Where("external_key = ?", key).Count(&duplicates).Error; err != nil {
			return AgentStatementResult{}, err
		}
		line := AgentStatementLine{ImportID: imported.ID, RowNumber: i + 1, PeriodID: period.ID, ExternalKey: key, ExternalReference: input.ExternalReference, OccurredAt: input.OccurredAt, Direction: input.Direction, Currency: input.Currency, AmountMinor: input.AmountMinor, BusinessReference: input.BusinessReference, BeneficiaryDigest: input.BeneficiaryDigest, EvidenceReference: input.EvidenceReference, RowDigest: digest}
		duplicateConflict := false
		if duplicates > 0 {
			var previous AgentStatementLine
			if err := tx.Where("external_key = ?", key).Order("id ASC").First(&previous).Error; err != nil {
				return AgentStatementResult{}, err
			}
			duplicateConflict = !sameAgentStatementEconomics(line, previous)
		}
		if err := tx.Create(&line).Error; err != nil {
			return AgentStatementResult{}, err
		}
		review := AgentStatementReview{LineID: line.ID, State: "unmatched", Version: 1}
		code := ""
		if duplicates > 0 {
			code = "duplicate"
			if duplicateConflict {
				code = "duplicate_conflict"
			}
		} else if stringsHasUnallocatedPrefix(input.BusinessReference) {
			code = "unallocated"
		}
		if code != "" {
			review.State = "exception"
			if err := raiseAgentStatementExceptionTx(tx, line, actor, code); err != nil {
				return AgentStatementResult{}, err
			}
		}
		if err := tx.Create(&review).Error; err != nil {
			return AgentStatementResult{}, err
		}
	}
	return AgentStatementResult{ResourceType: "statement_import", ResourceID: imported.ID, PeriodID: period.ID, State: "imported"}, nil
}
func ReplayAgentStatementOperation(identity AuthSessionIdentity, action, requestID string, payload []byte) (any, bool, error) {
	c, err := decodeAgentStatementCommand(action, requestID, payload)
	if err != nil {
		return nil, false, err
	}
	signature, err := AgentTransactionParametersHash(c)
	if err != nil {
		return nil, false, err
	}
	var result AgentStatementResult
	found := false
	err = agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		var prior AgentStatementAudit
		err := tx.Where("request_id = ?", requestID).First(&prior).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		if prior.ActorUserID != identity.UserID || prior.Action != action || prior.Signature != signature {
			return ErrAgentQuotaConflict
		}
		found = true
		return common.UnmarshalJsonStr(prior.ResultJSON, &result)
	})
	return &result, found, err
}
func FindAgentStatementOperation(identity AuthSessionIdentity, requestID string) (*AgentOperationStatus, error) {
	var result *AgentOperationStatus
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		var audit AgentStatementAudit
		err := tx.Where("request_id = ? AND actor_user_id = ?", requestID, identity.UserID).First(&audit).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := requireAgentCommercialAdminTx(tx, identity); err != nil {
			return err
		}
		var resource AgentStatementResult
		if err := common.UnmarshalJsonStr(audit.ResultJSON, &resource); err != nil {
			return err
		}
		result = &AgentOperationStatus{RequestID: requestID, Action: audit.Action, Status: "completed", ResourceType: resource.ResourceType, ResourceID: strconv.FormatInt(resource.ResourceID, 10), ResourceState: resource.State, Resource: resource}
		return nil
	})
	return result, err
}

// Proof-document references may differ across copies; economic bank facts may
// not. A conflicting import needs explicitly documented correction review.
func sameAgentStatementEconomics(a, b AgentStatementLine) bool {
	return a.ExternalKey == b.ExternalKey && a.Direction == b.Direction && a.Currency == b.Currency && a.AmountMinor == b.AmountMinor && a.OccurredAt/86400 == b.OccurredAt/86400 && a.BusinessReference == b.BusinessReference && a.BeneficiaryDigest == b.BeneficiaryDigest
}
