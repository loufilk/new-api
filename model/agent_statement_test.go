package model

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func statementExecuteTest(t *testing.T, identity AuthSessionIdentity, action string, c AgentStatementCommand) *AgentStatementResult {
	t.Helper()
	auth := agreementStatementProof(t, identity, action, c.RequestID, c)
	out, err := ExecuteAgentStatement(auth, action, c)
	require.NoError(t, err)
	return out
}
func statementPeriodTest(t *testing.T, f prepaidFixture, account, key string) *AgentStatementPeriod {
	t.Helper()
	now := common.GetTimestamp()
	out := statementExecuteTest(t, f.Finance, "statement.period.create", AgentStatementCommand{RequestID: key, Reason: "Synthetic bank review period", AccountDossierReference: account, Currency: "USD", StartsAt: now - 3600, EndsAt: now + 3600})
	period, err := GetAgentStatementPeriod(f.Finance, out.ResourceID)
	require.NoError(t, err)
	return period
}
func statementImportTest(t *testing.T, f prepaidFixture, periodID int64, key string, line AgentStatementLineInput) *AgentStatementLineView {
	t.Helper()
	period, err := GetAgentStatementPeriod(f.Finance, periodID)
	require.NoError(t, err)
	out := statementExecuteTest(t, f.Finance, "statement.import", AgentStatementCommand{RequestID: key, Reason: "Synthetic manually entered bank statement", PeriodID: period.ID, ExpectedVersion: period.Version, SourceReference: "evidence:" + key, Lines: []AgentStatementLineInput{line}})
	rows, total, err := ListAgentStatements(f.Finance, "lines", AgentStatementQuery{ImportID: out.ResourceID, Limit: 10})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	result := rows.([]AgentStatementLineView)
	require.Len(t, result, 1)
	return &result[0]
}
func statementLineForPurchase(t *testing.T, order *AgentPurchaseOrder) AgentStatementLineInput {
	t.Helper()
	var receipt AgentPaymentReceipt
	require.NoError(t, DB.First(&receipt, order.PaymentReceiptID).Error)
	return AgentStatementLineInput{ExternalReference: receipt.ProviderReference, OccurredAt: receipt.PaidAt, Direction: "incoming", Currency: receipt.Currency, AmountMinor: receipt.PaidMinor, BusinessReference: receipt.LocalOrderKey, EvidenceReference: "statement-proof:" + strconv.Itoa(order.ID)}
}
func readStatementReview(t *testing.T, lineID int64) AgentStatementReview {
	t.Helper()
	var r AgentStatementReview
	require.NoError(t, DB.First(&r, "line_id = ?", lineID).Error)
	return r
}
func TestAgentStatementStrictCSVAndInputBounds(t *testing.T) {
	header := "external_reference,occurred_at,direction,currency,amount_minor,business_reference,beneficiary_digest,evidence_reference\n"
	valid := fmt.Sprintf("bank-txn,%d,incoming,USD,90,purchase:1,,evidence:one\n", common.GetTimestamp())
	rows, err := ParseAgentStatementCSV(header + valid)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 90, rows[0].AmountMinor)
	for _, raw := range []string{"\ufeff" + header + valid, header + strings.Replace(valid, ",90,", ",9e1,", 1), header + strings.Replace(valid, ",90,", ",090,", 1), header + strings.Replace(valid, ",90,", ",-90,", 1), header + strings.Replace(valid, "bank-txn", "=CMD()", 1), header + strings.Replace(valid, "bank-txn", "\"broken", 1), header + strings.Replace(valid, "incoming", "outgoing", 1), header + strings.Repeat(valid, AgentStatementMaximumRows+1), strings.Repeat("x", 49<<10)} {
		_, err := ParseAgentStatementCSV(raw)
		require.Error(t, err)
	}
	c := AgentStatementCommand{RequestID: "digest-invalid", PeriodID: 1, ExpectedVersion: 1, Reason: "Synthetic CSV digest mismatch", SourceReference: "source:one", CSV: header + valid, SourceDigest: strings.Repeat("0", 64)}
	_, err = normalizeAgentStatementCommand("statement.import", c)
	require.ErrorIs(t, err, ErrAgentStatementInvalid)
	c.SourceDigest = ""
	canonical, err := normalizeAgentStatementCommand("statement.import", c)
	require.NoError(t, err)
	assert.Equal(t, agentStatementDigest([]byte(c.CSV)), canonical.SourceDigest)
	assert.Empty(t, canonical.CSV)
}
func TestAgentStatementManualImportIndependentMatchNoMoneyAndReplayPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	order := prepaidCredit(t, f, 1000, "statement-happy")
	period := statementPeriodTest(t, f, "fixture-merchant", "statement-period")
	line := statementImportTest(t, f, period.ID, "statement-import", statementLineForPurchase(t, order))
	before := getUserBalances(t, f.Owner.UserID)
	var ledgerBefore int64
	require.NoError(t, DB.Model(&AgentFundLedger{}).Count(&ledgerBefore).Error)
	candidates, err := GetAgentStatementCandidates(f.Finance, line.ID)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Empty(t, candidates[0].ExceptionCode)
	proposed := statementExecuteTest(t, f.Finance, "statement.match.propose", AgentStatementCommand{RequestID: "statement-match", LineID: line.ID, ExpectedVersion: line.Review.Version, Reason: "Synthetic exact purchase match", TargetType: "purchase", TargetID: int64(order.ID)})
	review := readStatementReview(t, line.ID)
	c := AgentStatementCommand{RequestID: "statement-confirm", LineID: line.ID, MatchID: proposed.ResourceID, ExpectedVersion: review.Version, Reason: "Synthetic independent match confirmation"}
	self := agreementStatementProof(t, f.Finance, "statement.match.confirm", c.RequestID, c)
	_, err = ExecuteAgentStatement(self, "statement.match.confirm", c)
	require.ErrorIs(t, err, ErrAgentStatementReview)
	auth := agreementStatementProof(t, f.Reviewer, "statement.match.confirm", c.RequestID, c)
	// Identical concurrent HTTP retries consume one proof/confirmation only.
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { <-start; _, e := ExecuteAgentStatement(auth, "statement.match.confirm", c); errs <- e })
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, "confirmed", readStatementReview(t, line.ID).State)
	var confirmations, ledgerAfter int64
	require.NoError(t, DB.Model(&AgentStatementConfirmedMatch{}).Count(&confirmations).Error)
	assert.EqualValues(t, 1, confirmations)
	require.NoError(t, DB.Model(&AgentFundLedger{}).Count(&ledgerAfter).Error)
	assert.Equal(t, ledgerBefore, ledgerAfter)
	assert.Equal(t, before.Quota, getUserBalances(t, f.Owner.UserID).Quota)
	assertAgentBudget(t, f.Owner.UserID, 1000, 0, 0, 1000)
	evidence, err := GetAgentStatementEvidence(f.Reviewer, line.ID)
	require.NoError(t, err)
	assert.True(t, evidence.ReconciliationOnly)
	assert.Equal(t, line.BusinessReference, evidence.BusinessReference)
	payload, err := common.Marshal(c)
	require.NoError(t, err)
	_, found, err := ReplayAgentStatementOperation(f.Reviewer, "statement.match.confirm", c.RequestID, payload)
	require.NoError(t, err)
	assert.True(t, found)
	changed := c
	changed.Reason = "Changed exact replay must fail"
	payload, err = common.Marshal(changed)
	require.NoError(t, err)
	_, _, err = ReplayAgentStatementOperation(f.Reviewer, "statement.match.confirm", c.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentQuotaConflict)
	period, err = GetAgentStatementPeriod(f.Finance, period.ID)
	require.NoError(t, err)
	closeCommand := AgentStatementCommand{RequestID: "statement-close", PeriodID: period.ID, ExpectedVersion: period.Version, Reason: "Synthetic fully reconciled period"}
	closed := statementExecuteTest(t, f.Reviewer, "statement.period.close", closeCommand)
	assert.Equal(t, "closed", closed.State)
	period, err = GetAgentStatementPeriod(f.Finance, period.ID)
	require.NoError(t, err)
	reopen := AgentStatementCommand{RequestID: "statement-reopen", PeriodID: period.ID, ExpectedVersion: period.Version, Reason: "Synthetic independent reopening for review"}
	statementExecuteTest(t, f.Finance, "statement.period.reopen", reopen)
	var audits int64
	require.NoError(t, DB.Model(&AgentStatementAudit{}).Where("period_id = ?", period.ID).Count(&audits).Error)
	assert.EqualValues(t, 6, audits)
}
func TestAgentStatementDuplicateImportsExceptionsAndOneObligationPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	order := prepaidCredit(t, f, 1000, "statement-duplicate")
	period := statementPeriodTest(t, f, "fixture-merchant", "duplicate-period")
	input := statementLineForPurchase(t, order)
	original := statementImportTest(t, f, period.ID, "original-import", input)
	proposed := statementExecuteTest(t, f.Finance, "statement.match.propose", AgentStatementCommand{RequestID: "original-match", LineID: original.ID, ExpectedVersion: 1, Reason: "Synthetic original payment match", TargetType: "incoming_receipt", TargetID: order.PaymentReceiptID})
	review := readStatementReview(t, original.ID)
	statementExecuteTest(t, f.Reviewer, "statement.match.confirm", AgentStatementCommand{RequestID: "original-confirm", LineID: original.ID, MatchID: proposed.ResourceID, ExpectedVersion: review.Version, Reason: "Synthetic independent original match"})
	// A second import with a different source/row digest cannot reuse one bank
	// transaction even with a different evidence dossier or target endpoint.
	input.EvidenceReference = "statement-proof:duplicate-copy"
	duplicate := statementImportTest(t, f, period.ID, "duplicate-import", input)
	assert.Equal(t, "exception", duplicate.Review.State)
	var exception AgentStatementException
	require.NoError(t, DB.Where("line_id = ?", duplicate.ID).First(&exception).Error)
	assert.Equal(t, "duplicate", exception.Code)
	_, err := PrepareAgentStatementOperation(f.Finance, "statement.match.propose", "duplicate-match", []byte(fmt.Sprintf(`{"line_id":%d,"expected_version":1,"target_type":"purchase","target_id":%d,"reason":"Synthetic duplicate"}`, duplicate.ID, order.ID)))
	require.NoError(t, err)
	c := AgentStatementCommand{RequestID: "duplicate-exclude", LineID: duplicate.ID, ExceptionID: exception.ID, ExpectedVersion: 1, Resolution: "exclude_duplicate", Reason: "Synthetic copy linked to original confirmed bank transaction"}
	statementExecuteTest(t, f.Reviewer, "statement.exception.resolve", c)
	var count int64
	require.NoError(t, DB.Model(&AgentStatementConfirmedMatch{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	// Same source content with a fresh request is a source-idempotent result.
	period, err = GetAgentStatementPeriod(f.Finance, period.ID)
	require.NoError(t, err)
	reimport := AgentStatementCommand{RequestID: "source-reimport", PeriodID: period.ID, ExpectedVersion: period.Version, SourceReference: "source:reimport", Lines: []AgentStatementLineInput{input}, Reason: "Synthetic byte-identical source replay"}
	out := statementExecuteTest(t, f.Finance, "statement.import", reimport)
	assert.Equal(t, duplicate.ImportID, out.ResourceID)
	require.NoError(t, DB.Model(&AgentStatementImport{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}
func TestAgentStatementMismatchUnknownWrongBeneficiaryAndClosurePostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	order := prepaidCredit(t, f, 1000, "statement-mismatch")
	period := statementPeriodTest(t, f, "fixture-merchant", "mismatch-period")
	input := statementLineForPurchase(t, order)
	input.AmountMinor++
	line := statementImportTest(t, f, period.ID, "amount-import", input)
	out := statementExecuteTest(t, f.Finance, "statement.match.propose", AgentStatementCommand{RequestID: "amount-match", LineID: line.ID, ExpectedVersion: 1, TargetType: "purchase", TargetID: int64(order.ID), Reason: "Synthetic wrong amount proposed"})
	assert.Equal(t, "amount_conflict", out.ExceptionCode)
	period, err := GetAgentStatementPeriod(f.Finance, period.ID)
	require.NoError(t, err)
	closeCommand := AgentStatementCommand{RequestID: "unresolved-close", PeriodID: period.ID, ExpectedVersion: period.Version, Reason: "Synthetic unresolved close attempt"}
	auth := agreementStatementProof(t, f.Reviewer, "statement.period.close", closeCommand.RequestID, closeCommand)
	_, err = ExecuteAgentStatement(auth, "statement.period.close", closeCommand)
	require.ErrorIs(t, err, ErrAgentStatementUnresolved)
	var exception AgentStatementException
	require.NoError(t, DB.Where("line_id = ?", line.ID).First(&exception).Error)
	review := readStatementReview(t, line.ID)
	resolve := AgentStatementCommand{RequestID: "invalid-exclusion", LineID: line.ID, ExceptionID: exception.ID, ExpectedVersion: review.Version, Resolution: "exclude_unrelated", Reason: "Synthetic mismatch cannot be written off"}
	auth = agreementStatementProof(t, f.Reviewer, "statement.exception.resolve", resolve.RequestID, resolve)
	_, err = ExecuteAgentStatement(auth, "statement.exception.resolve", resolve)
	require.ErrorIs(t, err, ErrAgentStatementInvalid)
	// UNKNOWN remains exactly UNKNOWN and no receipt/settlement is guessed.
	digest := strings.Repeat("a", 64)
	withdrawal := AgentCommissionWithdrawal{BusinessNo: "unknown-business", UserID: f.Owner.UserID, RequestID: "unknown-withdrawal", QuoteID: "unknown-quote", Currency: "USD", CurrencyExponent: 2, AmountMinor: 90, NetMinor: 90, RecipientID: "synthetic-recipient", RecipientVersion: 1, RecipientDigest: digest, RecipientLabel: "Synthetic payee", RecipientReference: "dossier:payee", RecipientMasked: "synthetic", Status: "unknown", Version: 1}
	require.NoError(t, DB.Create(&withdrawal).Error)
	unknown := statementImportTest(t, f, period.ID, "unknown-import", AgentStatementLineInput{ExternalReference: "unknown-bank-record", OccurredAt: common.GetTimestamp(), Direction: "outgoing", Currency: "USD", AmountMinor: 90, BusinessReference: withdrawal.BusinessNo, BeneficiaryDigest: digest, EvidenceReference: "proof:unknown"})
	result := statementExecuteTest(t, f.Finance, "statement.match.propose", AgentStatementCommand{RequestID: "unknown-match", LineID: unknown.ID, ExpectedVersion: 1, TargetType: "withdrawal", TargetID: withdrawal.ID, Reason: "Synthetic unknown must remain frozen"})
	assert.Equal(t, "state_conflict", result.ExceptionCode)
	require.NoError(t, DB.First(&withdrawal, withdrawal.ID).Error)
	assert.Equal(t, "unknown", withdrawal.Status)
	assert.Zero(t, withdrawal.PayoutReceiptID)
	// Pure mismatch evaluator also refuses exact money to a wrong beneficiary.
	target := agentStatementTarget{Direction: "outgoing", BusinessReference: "withdrawal:1", ExternalReference: "txn:1", AccountReference: "fixture-merchant", OwnerUserID: f.Owner.UserID, Currency: "USD", AmountMinor: 90, BeneficiaryDigest: digest, ReceiptID: 1, State: "succeeded", Verified: true}
	bank := AgentStatementLine{Direction: "outgoing", BusinessReference: target.BusinessReference, ExternalReference: target.ExternalReference, Currency: "USD", AmountMinor: 90, BeneficiaryDigest: strings.Repeat("b", 64)}
	assert.Equal(t, "beneficiary_conflict", agentStatementMismatch(bank, *period, target))
	bank.Currency = "EUR"
	assert.Equal(t, "currency_conflict", agentStatementMismatch(bank, *period, target))
}
func TestAgentStatementCurrentPrivilegesProofMutationAndImmutabilityPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	period := statementPeriodTest(t, f, "fixture-merchant", "access-period")
	c := AgentStatementCommand{RequestID: "access-import", PeriodID: period.ID, ExpectedVersion: period.Version, SourceReference: "source:access", Reason: "Synthetic authorization binding", Lines: []AgentStatementLineInput{{ExternalReference: "access-txn", OccurredAt: common.GetTimestamp(), Direction: "incoming", Currency: "USD", AmountMinor: 10, BusinessReference: "unallocated:misc", EvidenceReference: "proof:access"}}}
	auth := agreementStatementProof(t, f.Finance, "statement.import", c.RequestID, c)
	mutated := c
	mutated.Lines = append([]AgentStatementLineInput(nil), c.Lines...)
	mutated.Lines[0].AmountMinor = 11
	_, err := ExecuteAgentStatement(auth, "statement.import", mutated)
	require.ErrorIs(t, err, ErrAgentTransactionProof)
	out, err := ExecuteAgentStatement(auth, "statement.import", c)
	require.NoError(t, err)
	var imported AgentStatementImport
	require.NoError(t, DB.First(&imported, out.ResourceID).Error)
	require.ErrorIs(t, DB.Model(&imported).Update("source_reference", "changed").Error, ErrAgentLedgerImmutable)
	var line AgentStatementLine
	require.NoError(t, DB.Where("import_id = ?", imported.ID).First(&line).Error)
	require.ErrorIs(t, DB.Model(&line).Update("amount_minor", 999).Error, ErrAgentLedgerImmutable)
	require.ErrorIs(t, DB.Delete(&line).Error, ErrAgentLedgerImmutable)
	_, _, err = ListAgentStatements(f.Owner, "imports", AgentStatementQuery{Limit: 10})
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.Finance.UserID).Update("role", common.RoleCommonUser).Error)
	_, err = GetAgentStatementImport(f.Finance, imported.ID)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.Finance.UserID).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", f.Finance.SessionID).Update("status", UserSessionStatusRevoked).Error)
	payload, err := common.Marshal(c)
	require.NoError(t, err)
	_, _, err = ReplayAgentStatementOperation(f.Finance, "statement.import", c.RequestID, payload)
	require.Error(t, err)
	require.NoError(t, DB.Delete(&User{}, f.Reviewer.UserID).Error)
	_, _, err = ListAgentStatements(f.Reviewer, "lines", AgentStatementQuery{Limit: 10})
	require.Error(t, err)
}

func TestAgentStatementConcurrentImportAndOriginalCurrencyDatePostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	period := statementPeriodTest(t, f, "fixture-merchant", "concurrent-period")
	input := AgentStatementLineInput{ExternalReference: "concurrent-bank-record", OccurredAt: common.GetTimestamp(), Direction: "incoming", Currency: "USD", AmountMinor: 123, BusinessReference: "unallocated:manual-review", EvidenceReference: "evidence:concurrent-source"}
	c := AgentStatementCommand{RequestID: "concurrent-import", PeriodID: period.ID, ExpectedVersion: period.Version, SourceReference: "source:concurrent", Reason: "Synthetic simultaneous exact source imports", Lines: []AgentStatementLineInput{input}}
	auth := agreementStatementProof(t, f.Finance, "statement.import", c.RequestID, c)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { <-start; _, err := ExecuteAgentStatement(auth, "statement.import", c); errs <- err })
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, DB.Model(&AgentStatementImport{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, DB.Model(&AgentStatementLine{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	period, err := GetAgentStatementPeriod(f.Finance, period.ID)
	require.NoError(t, err)
	c.RequestID = "wrong-currency-import"
	c.ExpectedVersion = period.Version
	c.Lines = append([]AgentStatementLineInput(nil), c.Lines...)
	c.Lines[0].Currency = "JPY"
	auth = agreementStatementProof(t, f.Finance, "statement.import", c.RequestID, c)
	_, err = ExecuteAgentStatement(auth, "statement.import", c)
	require.ErrorIs(t, err, ErrAgentStatementInvalid)
	line := AgentStatementLine{Direction: "incoming", BusinessReference: "purchase:1", Currency: "USD", AmountMinor: 123, ExternalReference: "txn:date", OccurredAt: 172800}
	target := agentStatementTarget{Direction: "incoming", BusinessReference: line.BusinessReference, Currency: line.Currency, AmountMinor: 123, ExternalReference: line.ExternalReference, AccountReference: period.AccountDossierReference, ReceiptID: 1, Verified: true, PaidAt: 86400}
	assert.Equal(t, "date_conflict", agentStatementMismatch(line, *period, target))
	target.PaidAt = 0
	assert.Equal(t, "date_unverified", agentStatementMismatch(line, *period, target))
	target.PaidAt = line.OccurredAt + 60
	assert.Empty(t, agentStatementMismatch(line, *period, target))
}

func TestAgentStatementConflictingDuplicateRequiresDocumentedCorrectionPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	order := prepaidCredit(t, f, 1000, "conflicting-duplicate")
	period := statementPeriodTest(t, f, "fixture-merchant", "conflicting-duplicate-period")
	input := statementLineForPurchase(t, order)
	original := statementImportTest(t, f, period.ID, "correct-import-first", input)
	proposed := statementExecuteTest(t, f.Finance, "statement.match.propose", AgentStatementCommand{RequestID: "correct-first-match", LineID: original.ID, ExpectedVersion: 1, TargetType: "purchase", TargetID: int64(order.ID), Reason: "Synthetic correct original statement"})
	review := readStatementReview(t, original.ID)
	statementExecuteTest(t, f.Reviewer, "statement.match.confirm", AgentStatementCommand{RequestID: "correct-first-confirm", LineID: original.ID, MatchID: proposed.ResourceID, ExpectedVersion: review.Version, Reason: "Synthetic independent correct statement review"})
	input.AmountMinor++
	bad := statementImportTest(t, f, period.ID, "conflicting-copy", input)
	var exception AgentStatementException
	require.NoError(t, DB.Where("line_id = ?", bad.ID).First(&exception).Error)
	assert.Equal(t, "duplicate_conflict", exception.Code)
	c := AgentStatementCommand{RequestID: "conflicting-blind-exclusion", LineID: bad.ID, ExceptionID: exception.ID, ExpectedVersion: 1, Resolution: "exclude_duplicate", Reason: "Synthetic conflicting duplicate must not disappear"}
	auth := agreementStatementProof(t, f.Reviewer, "statement.exception.resolve", c.RequestID, c)
	_, err := ExecuteAgentStatement(auth, "statement.exception.resolve", c)
	require.ErrorIs(t, err, ErrAgentStatementInvalid)
	c.RequestID = "documented-import-correction"
	c.Resolution = "exclude_invalid_import"
	c.ReplacementLineID = original.ID
	c.EvidenceReference = "document:verified-original-statement"
	c.Reason = "Synthetic transcription error independently checked against original"
	out := statementExecuteTest(t, f.Reviewer, "statement.exception.resolve", c)
	assert.Equal(t, "excluded", out.State)
	var count int64
	require.NoError(t, DB.Model(&AgentStatementConfirmedMatch{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, DB.First(&bad.AgentStatementLine, bad.ID).Error)
	assert.EqualValues(t, 91, bad.AmountMinor, "imported incorrect row stays immutable for audit")
}

func TestAgentStatementDeletedOwnerHistoryStillRequiresCurrentFinanceAndExactTargetPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	order := prepaidCredit(t, f, 1000, "deleted-owner-history")
	period := statementPeriodTest(t, f, "fixture-merchant", "deleted-owner-period")
	line := statementImportTest(t, f, period.ID, "deleted-owner-import", statementLineForPurchase(t, order))
	require.NoError(t, DB.Delete(&User{}, f.Owner.UserID).Error)
	c := AgentStatementCommand{RequestID: "deleted-owner-match", LineID: line.ID, ExpectedVersion: 1, TargetType: "purchase", TargetID: int64(order.ID), Reason: "Synthetic historical bank evidence for deleted customer"}
	payload, err := common.Marshal(c)
	require.NoError(t, err)
	prepared, err := PrepareAgentStatementOperation(f.Finance, "statement.match.propose", c.RequestID, payload)
	require.NoError(t, err)
	assert.Equal(t, f.Finance.UserID, prepared.VerificationContext.AgentUserID)
	_, err = PrepareAgentStatementOperation(f.Other, "statement.match.propose", c.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	auth := agreementStatementProof(t, f.Finance, "statement.match.propose", c.RequestID, c)
	changed := c
	changed.TargetType = "incoming_receipt"
	changed.TargetID = order.PaymentReceiptID
	_, err = ExecuteAgentStatement(auth, "statement.match.propose", changed)
	require.ErrorIs(t, err, ErrAgentTransactionProof)
	proposed, err := ExecuteAgentStatement(auth, "statement.match.propose", c)
	require.NoError(t, err)
	review := readStatementReview(t, line.ID)
	out := statementExecuteTest(t, f.Reviewer, "statement.match.confirm", AgentStatementCommand{RequestID: "deleted-owner-confirm", LineID: line.ID, MatchID: proposed.ResourceID, ExpectedVersion: review.Version, Reason: "Synthetic independent historical reconciliation only"})
	assert.Equal(t, "confirmed", out.State)
	var deleted User
	require.NoError(t, DB.Unscoped().First(&deleted, f.Owner.UserID).Error)
	assert.True(t, deleted.DeletedAt.Valid)
	assert.Zero(t, deleted.Quota)
}
