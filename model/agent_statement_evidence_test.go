package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func legacyStatementWithdrawal(t *testing.T, f prepaidFixture, key, state string, paidAt int64, bound bool) (AgentCommissionWithdrawal, *AgentOutboundPaymentReceipt) {
	t.Helper()
	digest := agentStatementDigest([]byte("dossier:legacy-payee"))
	w := AgentCommissionWithdrawal{BusinessNo: key, UserID: f.Owner.UserID, RequestID: "request-" + key, QuoteID: "quote-" + key, Currency: "USD", CurrencyExponent: 2, AmountMinor: 90, NetMinor: 90, RecipientID: "synthetic-recipient", RecipientVersion: 1, RecipientDigest: digest, RecipientLabel: "Synthetic legacy payee", RecipientReference: "dossier:legacy-payee", RecipientMasked: "synthetic", Status: state, Version: 1}
	require.NoError(t, DB.Create(&w).Error)
	candidate := AgentOutboundPaymentReceipt{Provider: "bank_transfer", ProviderReference: "external-" + key, MerchantScope: "fixture-merchant", Environment: "live", Purpose: "withdrawal", BusinessKey: key, OwnerUserID: f.Owner.UserID, Currency: "USD", CurrencyExponent: 2, PaidMinor: 90, VerifiedBy: f.Finance.UserID, EvidenceHash: strings.Repeat("a", 64), PaidAt: paidAt}
	if bound {
		candidate.BeneficiaryDigest = digest
	}
	var receipt *AgentOutboundPaymentReceipt
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		receipt, err = ClaimAgentOutboundPaymentReceiptTx(tx, &candidate)
		return err
	}))
	w.PayoutReceiptID = receipt.ID
	require.NoError(t, DB.Save(&w).Error)
	return w, receipt
}
func TestAgentStatementLegacySupplementIndependentEvidenceWithoutMoneyPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	withdrawal, receipt := legacyStatementWithdrawal(t, f, "legacy-succeeded", "succeeded", 0, false)

	period := statementPeriodTest(t, f, "fixture-merchant", "legacy-evidence-period")
	line := statementImportTest(t, f, period.ID, "legacy-evidence-import", AgentStatementLineInput{ExternalReference: receipt.ProviderReference, OccurredAt: common.GetTimestamp(), Direction: "outgoing", Currency: "USD", AmountMinor: 90, BusinessReference: withdrawal.BusinessNo, BeneficiaryDigest: withdrawal.RecipientDigest, EvidenceReference: "proof:legacy-bank-statement"})
	matched := statementExecuteTest(t, f.Finance, "statement.match.propose", AgentStatementCommand{RequestID: "legacy-unbound-match", LineID: line.ID, ExpectedVersion: 1, TargetType: "withdrawal", TargetID: withdrawal.ID, Reason: "Synthetic legacy payment requires real evidence"})
	assert.Equal(t, "beneficiary_conflict", matched.ExceptionCode)
	command := AgentStatementCommand{RequestID: "legacy-supplement", ReceiptType: "outgoing_receipt", ReceiptID: receipt.ID, DossierReference: "dossier:legacy-payee", PaidAt: line.OccurredAt, EvidenceReference: "proof:legacy-original-payee-and-bank-date", Reason: "Synthetic supplementary verified legacy evidence"}
	out := statementExecuteTest(t, f.Finance, "statement.evidence.propose", command)
	view, err := GetAgentStatementSupplementaryEvidence(f.Reviewer, out.ResourceID)
	require.NoError(t, err)
	assert.Equal(t, "proposed", view.State)
	confirm := AgentStatementCommand{RequestID: "legacy-supplement-confirm", EvidenceID: view.ID, ExpectedVersion: view.Version, DossierReference: command.DossierReference, PaidAt: command.PaidAt, EvidenceReference: command.EvidenceReference, Reason: "Synthetic independent documentary review"}
	auth := agreementStatementProof(t, f.Finance, "statement.evidence.confirm", confirm.RequestID, confirm)
	_, err = ExecuteAgentStatement(auth, "statement.evidence.confirm", confirm)
	require.ErrorIs(t, err, ErrAgentStatementReview)
	auth = agreementStatementProof(t, f.Reviewer, "statement.evidence.confirm", confirm.RequestID, confirm)
	mutated := confirm
	mutated.DossierReference = "dossier:wrong-recipient"
	_, err = ExecuteAgentStatement(auth, "statement.evidence.confirm", mutated)
	require.ErrorIs(t, err, ErrAgentTransactionProof)
	result, err := ExecuteAgentStatement(auth, "statement.evidence.confirm", confirm)
	require.NoError(t, err)
	assert.Equal(t, "confirmed", result.State)
	// Receipt remains exactly historical; evidence supplies missing reconciliation
	// facts but does not change any payment status or monetary balance.
	require.NoError(t, DB.First(receipt, receipt.ID).Error)
	assert.Empty(t, receipt.BeneficiaryDigest)
	assert.Zero(t, receipt.PaidAt)
	var exception AgentStatementException
	require.NoError(t, DB.Where("line_id = ?", line.ID).First(&exception).Error)
	review := readStatementReview(t, line.ID)
	statementExecuteTest(t, f.Reviewer, "statement.exception.resolve", AgentStatementCommand{RequestID: "legacy-evidence-retry", LineID: line.ID, ExceptionID: exception.ID, ExpectedVersion: review.Version, Resolution: "retry", Reason: "Synthetic confirmed evidence now permits exact review"})
	review = readStatementReview(t, line.ID)
	matched = statementExecuteTest(t, f.Finance, "statement.match.propose", AgentStatementCommand{RequestID: "legacy-bound-match", LineID: line.ID, ExpectedVersion: review.Version, TargetType: "outgoing_receipt", TargetID: receipt.ID, Reason: "Synthetic exact supplemented legacy match"})
	assert.Equal(t, "proposed", matched.State)
	review = readStatementReview(t, line.ID)
	statementExecuteTest(t, f.Reviewer, "statement.match.confirm", AgentStatementCommand{RequestID: "legacy-bound-confirm", LineID: line.ID, MatchID: matched.ResourceID, ExpectedVersion: review.Version, Reason: "Synthetic independent final reconciliation"})
	require.NoError(t, DB.First(&withdrawal, withdrawal.ID).Error)
	assert.Equal(t, "succeeded", withdrawal.Status)
	assert.Zero(t, getUserBalances(t, f.Owner.UserID).Quota)
	var count int64
	require.NoError(t, DB.Model(&AgentStatementEvidence{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, DB.Model(&AgentStatementConfirmedMatch{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	encoded, err := common.Marshal(view)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), command.DossierReference)
}
func TestAgentStatementSupplementCannotOverrideOrGuessUnknownPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	unknown, receipt := legacyStatementWithdrawal(t, f, "legacy-unknown", "unknown", 0, false)
	command := AgentStatementCommand{RequestID: "unknown-supplement", ReceiptType: "outgoing_receipt", ReceiptID: receipt.ID, DossierReference: "dossier:legacy-payee", PaidAt: common.GetTimestamp(), EvidenceReference: "proof:unknown-not-final", Reason: "Synthetic unknown evidence cannot settle payment"}
	payload, err := common.Marshal(command)
	require.NoError(t, err)
	_, err = PrepareAgentStatementOperation(f.Finance, "statement.evidence.propose", command.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentStatementUnresolved)
	require.NoError(t, DB.First(&unknown, unknown.ID).Error)
	assert.Equal(t, "unknown", unknown.Status)
	_, bound := legacyStatementWithdrawal(t, f, "legacy-bound", "succeeded", common.GetTimestamp(), true)
	command.RequestID = "bound-supplement"
	command.ReceiptID = bound.ID
	auth := agreementStatementProof(t, f.Finance, "statement.evidence.propose", command.RequestID, command)
	_, err = ExecuteAgentStatement(auth, "statement.evidence.propose", command)
	require.ErrorIs(t, err, ErrAgentStatementConflict)
	_, partial := legacyStatementWithdrawal(t, f, "legacy-partial", "succeeded", 0, true)
	command.RequestID = "partial-wrong-payee"
	command.ReceiptID = partial.ID
	command.DossierReference = "dossier:wrong-payee"
	auth = agreementStatementProof(t, f.Finance, "statement.evidence.propose", command.RequestID, command)
	_, err = ExecuteAgentStatement(auth, "statement.evidence.propose", command)
	require.ErrorIs(t, err, ErrAgentStatementConflict)
	command.RequestID = "partial-correct-payee"
	command.DossierReference = "dossier:legacy-payee"
	out := statementExecuteTest(t, f.Finance, "statement.evidence.propose", command)
	rejected := statementExecuteTest(t, f.Reviewer, "statement.evidence.reject", AgentStatementCommand{RequestID: "partial-evidence-reject", EvidenceID: out.ResourceID, ExpectedVersion: 1, Reason: "Synthetic rejected documentary sufficiency"})
	assert.Equal(t, "rejected", rejected.State)
	command.RequestID = "partial-evidence-v2"
	command.EvidenceReference = "proof:additional-original-document"
	out = statementExecuteTest(t, f.Finance, "statement.evidence.propose", command)
	view, err := GetAgentStatementSupplementaryEvidence(f.Reviewer, out.ResourceID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, view.Version)
	var count int64
	require.NoError(t, DB.Model(&AgentStatementConfirmedMatch{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAgentStatementLegacyEvidenceRecoveryOnlyWhenNewImportDisabledPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	_, receipt := legacyStatementWithdrawal(t, f, "recovery-only-final", "succeeded", 0, false)
	p := f.Policy
	p.ID = 0
	p.RequestID = "new-statement-features-disabled"
	p.ManualReconciliationEnabled = false
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	c := AgentStatementCommand{RequestID: "disabled-legacy-recovery", ReceiptType: "outgoing_receipt", ReceiptID: receipt.ID, DossierReference: "dossier:legacy-payee", PaidAt: common.GetTimestamp(), EvidenceReference: "proof:existing-final-payment", Reason: "Synthetic existing rights recovered by independent proof"}
	out := statementExecuteTest(t, f.Finance, "statement.evidence.propose", c)
	statementExecuteTest(t, f.Reviewer, "statement.evidence.confirm", AgentStatementCommand{RequestID: "disabled-recovery-confirm", EvidenceID: out.ResourceID, ExpectedVersion: 1, DossierReference: c.DossierReference, PaidAt: c.PaidAt, EvidenceReference: c.EvidenceReference, Reason: "Synthetic independent legacy fact recovery"})
	blocked := AgentStatementCommand{RequestID: "disabled-new-period", AccountDossierReference: "fixture-merchant", Currency: "USD", StartsAt: common.GetTimestamp() - 60, EndsAt: common.GetTimestamp() + 60, Reason: "Synthetic new statement remains disabled"}
	payload, err := common.Marshal(blocked)
	require.NoError(t, err)
	_, err = PrepareAgentStatementOperation(f.Finance, "statement.period.create", blocked.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentFeatureDisabled)
	p.ID = 0
	p.RequestID = "risk-not-approved-for-recovery"
	p.RiskPolicyApproved = false
	p.PurchaseEnabled = false
	p.AgreementPricingEnabled = false
	p.DeliveryEnabled = false
	p.PurchaseRefundEnabled = false
	p.LegacyPurchaseReclassificationEnabled = false
	p.ProcurementSelfUseEnabled = false
	require.NoError(t, ValidateAgentPolicy(&p))
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	c.RequestID = "disabled-risk-recovery"
	payload, err = common.Marshal(c)
	require.NoError(t, err)
	_, err = PrepareAgentStatementOperation(f.Finance, "statement.evidence.propose", c.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentFeatureDisabled)
}

func TestAgentStatementIncomingRecoveryBridgePreservesOriginalReceiptPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	p := f.Policy
	p.ID = 0
	p.RequestID = "legacy-purchase-without-new-statements"
	p.ManualReconciliationEnabled = false
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	f.Policy = p
	order := prepaidOrder(t, f, 1000, "legacy-payer-recovery")
	// Explicit pre-feature receipt fixture: do not use the modern helper which
	// now records a real controlled payer dossier for all newly paid orders.
	payment := AgentPurchaseCommand{RequestID: "legacy-unbound-payment", OrderID: order.ID, Version: order.Version, EvidenceReference: "document:legacy-payment", Provider: "bank_transfer", MerchantScope: "fixture-merchant", Environment: "live", ExternalReference: "legacy-unbound-bank-transaction", PaidMinor: order.PaidMinor, Currency: order.Currency, PaidAt: common.GetTimestamp()}
	var err error
	order, err = RecordAgentPurchasePayment(prepaidProof(t, f.Finance, "purchase.payment.record", payment.RequestID, payment), payment)
	require.NoError(t, err)
	approval := AgentPurchaseCommand{RequestID: "legacy-unbound-confirm", OrderID: order.ID, Version: order.Version}
	order, err = ConfirmAgentPurchasePayment(prepaidProof(t, f.Reviewer, "purchase.payment.confirm", approval.RequestID, approval), approval)
	require.NoError(t, err)
	var receipt AgentPaymentReceipt
	require.NoError(t, DB.First(&receipt, order.PaymentReceiptID).Error)
	assert.Empty(t, receipt.CounterpartyDigest)
	_, err = ResolveAgentPaymentCounterpartyDigestTx(DB, receipt.ID)
	require.ErrorIs(t, err, ErrAgentStatementUnresolved)
	c := AgentStatementCommand{RequestID: "legacy-payer-document", ReceiptType: "incoming_receipt", ReceiptID: receipt.ID, DossierReference: "dossier:verified-original-payer", PaidAt: receipt.PaidAt, EvidenceReference: "proof:original-payer-bank-document", Reason: "Synthetic finalized purchase original payer recovery"}
	wrong := c
	wrong.RequestID = "legacy-payer-wrong-date"
	wrong.PaidAt--
	auth := agreementStatementProof(t, f.Finance, "statement.evidence.propose", wrong.RequestID, wrong)
	_, err = ExecuteAgentStatement(auth, "statement.evidence.propose", wrong)
	require.ErrorIs(t, err, ErrAgentStatementConflict)
	out := statementExecuteTest(t, f.Finance, "statement.evidence.propose", c)
	statementExecuteTest(t, f.Reviewer, "statement.evidence.confirm", AgentStatementCommand{RequestID: "legacy-payer-document-confirm", EvidenceID: out.ResourceID, ExpectedVersion: 1, DossierReference: c.DossierReference, PaidAt: c.PaidAt, EvidenceReference: c.EvidenceReference, Reason: "Synthetic independent original payer document verification"})
	digest, err := ResolveAgentPaymentCounterpartyDigestTx(DB, receipt.ID)
	require.NoError(t, err)
	assert.Equal(t, agentStatementDigest([]byte(c.DossierReference)), digest)
	require.NoError(t, DB.First(&receipt, receipt.ID).Error)
	assert.Empty(t, receipt.CounterpartyDigest, "supplement never overwrites original immutable receipt")
	assertAgentBudget(t, f.Owner.UserID, 1000, 0, 0, 1000)
	assert.Zero(t, getUserBalances(t, f.Owner.UserID).Quota)
}

func TestAgentStatementFailedObligationCannotBeSettledByBankMatchPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	failed, receipt := legacyStatementWithdrawal(t, f, "failed-bank-obligation", "failed", common.GetTimestamp(), true)
	period := statementPeriodTest(t, f, "fixture-merchant", "failed-bank-period")
	line := statementImportTest(t, f, period.ID, "failed-bank-import", AgentStatementLineInput{ExternalReference: receipt.ProviderReference, OccurredAt: receipt.PaidAt, Direction: "outgoing", Currency: receipt.Currency, AmountMinor: receipt.PaidMinor, BusinessReference: receipt.BusinessKey, BeneficiaryDigest: receipt.BeneficiaryDigest, EvidenceReference: "proof:contradictory-bank-evidence"})
	out := statementExecuteTest(t, f.Finance, "statement.match.propose", AgentStatementCommand{RequestID: "failed-bank-match", LineID: line.ID, ExpectedVersion: 1, TargetType: "withdrawal", TargetID: failed.ID, Reason: "Synthetic failed obligation cannot become success by matching"})
	assert.Equal(t, "state_conflict", out.ExceptionCode)
	c := AgentStatementCommand{RequestID: "failed-evidence-proposal", ReceiptType: "outgoing_receipt", ReceiptID: receipt.ID, DossierReference: "dossier:legacy-payee", PaidAt: receipt.PaidAt, EvidenceReference: "proof:failed-no-success-guess", Reason: "Synthetic supplemental evidence cannot guess success"}
	payload, err := common.Marshal(c)
	require.NoError(t, err)
	_, err = PrepareAgentStatementOperation(f.Finance, "statement.evidence.propose", c.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentStatementUnresolved)
	require.NoError(t, DB.First(&failed, failed.ID).Error)
	assert.Equal(t, "failed", failed.Status)
	var count int64
	require.NoError(t, DB.Model(&AgentStatementConfirmedMatch{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAgentStatementLegacyReclassificationFinalReceiptRecoveryPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	require.NoError(t, DB.Create(&AgentRedemptionAccount{AgentUserID: f.Owner.UserID, AvailableQuota: 1000, AllocatedQuota: 1000}).Error)
	require.NoError(t, MigrateLegacyAgentFunds())
	var legacy AgentFundBatch
	require.NoError(t, DB.Where("agent_user_id = ? AND source_kind = ?", f.Owner.UserID, AgentFundLegacy).First(&legacy).Error)
	command := AgentFundReclassificationCommand{RequestID: "statement-legacy-record", AgentUserID: f.Owner.UserID, LegacyBatchID: legacy.ID, Quota: 600, PaidMinor: "54", Currency: "USD", CurrencyExponent: 2, Provider: "bank_transfer", MerchantScope: "fixture-merchant", Environment: "live", ExternalReference: "legacy-real-bank-record", PaidAt: common.GetTimestamp() - 60, EvidenceReference: "document:historical-procurement", HistoricalProcurementConfirmed: true, NotWalletTopupConfirmed: true}
	record, err := RecordAgentFundReclassification(prepaidProof(t, f.Finance, "purchase.legacy.record", command.RequestID, command), command)
	require.NoError(t, err)
	_, err = ResolveAgentPaymentCounterpartyDigestTx(DB, record.PaymentReceiptID)
	require.ErrorIs(t, err, ErrAgentStatementUnresolved, "unconfirmed reclassification cannot gain payment authority")
	confirm := AgentFundReclassificationCommand{RequestID: "statement-legacy-confirm", ReclassificationID: record.ID, Version: record.Version, Decision: "approve", EvidenceReference: "document:independent-legacy-review"}
	record, err = ConfirmAgentFundReclassification(prepaidProof(t, f.Reviewer, "purchase.legacy.confirm", confirm.RequestID, confirm), confirm)
	require.NoError(t, err)
	c := AgentStatementCommand{RequestID: "legacy-reclass-payer-evidence", ReceiptType: "incoming_receipt", ReceiptID: record.PaymentReceiptID, DossierReference: "dossier:legacy-procurement-payer", PaidAt: command.PaidAt, EvidenceReference: "document:original-payer-verification", Reason: "Synthetic confirmed legacy reclassification payer evidence"}
	out := statementExecuteTest(t, f.Finance, "statement.evidence.propose", c)
	statementExecuteTest(t, f.Reviewer, "statement.evidence.confirm", AgentStatementCommand{RequestID: "legacy-reclass-payer-confirm", EvidenceID: out.ResourceID, ExpectedVersion: 1, DossierReference: c.DossierReference, PaidAt: c.PaidAt, EvidenceReference: c.EvidenceReference, Reason: "Synthetic independent original payer verification"})
	digest, err := ResolveAgentPaymentCounterpartyDigestTx(DB, record.PaymentReceiptID)
	require.NoError(t, err)
	assert.Equal(t, agentStatementDigest([]byte(c.DossierReference)), digest)
	period := statementPeriodTest(t, f, "fixture-merchant", "legacy-reclass-period")
	var receipt AgentPaymentReceipt
	require.NoError(t, DB.First(&receipt, record.PaymentReceiptID).Error)
	line := statementImportTest(t, f, period.ID, "legacy-reclass-import", AgentStatementLineInput{ExternalReference: receipt.ProviderReference, OccurredAt: receipt.PaidAt, Direction: "incoming", Currency: receipt.Currency, AmountMinor: receipt.PaidMinor, BusinessReference: receipt.LocalOrderKey, EvidenceReference: "statement:historic-procurement"})
	match := statementExecuteTest(t, f.Finance, "statement.match.propose", AgentStatementCommand{RequestID: "legacy-reclass-match", LineID: line.ID, ExpectedVersion: 1, TargetType: "incoming_receipt", TargetID: receipt.ID, Reason: "Synthetic exact confirmed reclassification match"})
	review := readStatementReview(t, line.ID)
	statementExecuteTest(t, f.Reviewer, "statement.match.confirm", AgentStatementCommand{RequestID: "legacy-reclass-match-confirm", LineID: line.ID, MatchID: match.ResourceID, ExpectedVersion: review.Version, Reason: "Synthetic independent historical reconciliation"})
	var account AgentRedemptionAccount
	require.NoError(t, DB.Where("agent_user_id = ?", f.Owner.UserID).First(&account).Error)
	assert.Equal(t, 1000, account.AvailableQuota)
	assert.Zero(t, account.ReservedQuota)
	assert.Zero(t, account.SpentQuota)
	assert.Equal(t, 1000, account.AllocatedQuota)
	assert.Zero(t, getUserBalances(t, f.Owner.UserID).Quota)
}

func TestAgentStatementIncomingPurposeAndIndependentFinalityPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	for _, purpose := range []string{"deposit", "credit_repayment", "topup"} {
		t.Run(purpose, func(t *testing.T) {
			now := common.GetTimestamp()
			candidate := AgentPaymentReceipt{Provider: "bank_transfer", ProviderReference: "purpose-external-" + purpose, MerchantScope: "fixture-merchant", Environment: "live", Purpose: purpose, LocalOrderKey: purpose + ":synthetic-finality", OwnerUserID: f.Owner.UserID, Currency: "USD", CurrencyExponent: 2, PaidMinor: 90, PaidAt: now, VerifiedAt: now, VerifiedBy: f.Finance.UserID, EvidenceHash: strings.Repeat("a", 64)}
			var receipt *AgentPaymentReceipt
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
				var err error
				receipt, err = ClaimAgentPaymentReceiptTx(tx, &candidate)
				return err
			}))
			var business any
			switch purpose {
			case "deposit":
				business = &AgentDepositReceipt{AgentUserID: f.Owner.UserID, PaymentReceiptID: receipt.ID, AmountMinor: 90, Currency: "USD", CurrencyExponent: 2, RecordedBy: f.Finance.UserID, State: "recorded", Version: 1}
			case "credit_repayment":
				business = &AgentCreditRepayment{AgentUserID: f.Owner.UserID, ReceiptID: receipt.ID, AmountMinor: 90, Currency: "USD", CurrencyExponent: 2, RecordedBy: f.Finance.UserID, State: "recorded", Version: 1}
			}
			if business != nil {
				require.NoError(t, DB.Create(business).Error)
			}
			target, err := agentStatementTargetRawTx(DB, "incoming_receipt", receipt.ID)
			require.NoError(t, err)
			assert.False(t, target.Verified, "a receipt alone is not a final agent obligation")
			if business == nil {
				_, err = ResolveAgentPaymentCounterpartyDigestTx(DB, receipt.ID)
				require.ErrorIs(t, err, ErrAgentStatementUnresolved)
				return
			}
			require.NoError(t, DB.Model(business).Updates(map[string]any{"state": "confirmed", "confirmed_by": f.Finance.UserID}).Error)
			target, err = agentStatementTargetRawTx(DB, "incoming_receipt", receipt.ID)
			require.NoError(t, err)
			assert.False(t, target.Verified, "self-approved records cannot become bank matches")
			require.NoError(t, DB.Model(business).Update("confirmed_by", f.Reviewer.UserID).Error)
			target, err = agentStatementTargetRawTx(DB, "incoming_receipt", receipt.ID)
			require.NoError(t, err)
			assert.True(t, target.Verified)
			require.NoError(t, DB.Model(business).Update("currency_exponent", 3).Error)
			_, err = agentStatementTargetRawTx(DB, "incoming_receipt", receipt.ID)
			require.ErrorIs(t, err, ErrAgentStatementConflict, "original minor unit semantics must agree")
		})
	}
	wallet := getUserBalances(t, f.Owner.UserID)
	assert.Zero(t, wallet.Quota)
}
