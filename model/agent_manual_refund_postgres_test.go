package model

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func manualRefundPending(t *testing.T, f prepaidFixture, order *AgentPurchaseOrder, quota int, key string) *AgentPurchaseRefund {
	t.Helper()
	r := prepaidRefund(t, f, order.FundBatchID, quota, key)
	r = prepaidRefundAdvance(t, f, r, "purchase.refund.approve", "approve-"+key, AgentPurchaseRefundCommand{Decision: "approve", EvidenceReference: "independent-liability-review"})
	return prepaidRefundAdvance(t, f, r, "purchase.refund.submit", "submit-"+key, AgentPurchaseRefundCommand{BusinessKey: r.BusinessKey})
}

func TestAgentManualRefundRequiresActualFactsAndIndependentFinalChecker(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "manual-facts")
	r := manualRefundPending(t, f, order, 500, "manual-facts")
	instruction, err := GetAgentManualRefundInstruction(f.Finance, "purchase", int64(r.ID))
	require.NoError(t, err)
	assert.True(t, instruction.ManualOnly)
	assert.Equal(t, "original_payer_dossier", instruction.RecipientKind)
	assert.Equal(t, "dossier:synthetic-payer", instruction.PayerDossierReference)
	assert.Empty(t, instruction.OriginalPaymentReference)
	_, err = GetAgentManualRefundInstruction(f.Owner, "purchase", int64(r.ID))
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	pat := f.Finance
	pat.SessionID = "pat:1"
	_, err = GetAgentManualRefundInstruction(pat, "purchase", int64(r.ID))
	require.Error(t, err)
	success := AgentPurchaseRefundCommand{RequestID: "manual-success", RefundID: r.ID, Version: r.Version, BusinessKey: r.BusinessKey, Outcome: "success", EvidenceReference: "checked-bank-statement", ExternalReference: "manual-bank-reference"}
	_, err = PrepareAgentPurchaseRefundContext(f.Reviewer, "purchase.refund.resolve", success)
	require.ErrorIs(t, err, ErrAgentRefundInvalid)
	success.PayoutPaidAt = r.CreatedAt - 1
	_, err = PrepareAgentPurchaseRefundContext(f.Reviewer, "purchase.refund.resolve", success)
	require.ErrorIs(t, err, ErrAgentRefundInvalid)
	success.PayoutPaidAt = common.GetTimestamp() + 100
	_, err = PrepareAgentPurchaseRefundContext(f.Reviewer, "purchase.refund.resolve", success)
	require.ErrorIs(t, err, ErrAgentRefundInvalid)
	success.PayoutPaidAt = common.GetTimestamp()
	_, err = TransitionAgentPurchaseRefund(prepaidProof(t, f.Finance, "purchase.refund.resolve", success.RequestID, success), "purchase.refund.resolve", success)
	require.ErrorIs(t, err, ErrAgentPurchaseReview)
	failure := success
	failure.RequestID = "manual-self-failure"
	failure.Outcome = "explicit_failed"
	failure.NoPaymentConfirmed = true
	failure.PayoutPaidAt = 0
	_, err = TransitionAgentPurchaseRefund(prepaidProof(t, f.Finance, "purchase.refund.resolve", failure.RequestID, failure), "purchase.refund.resolve", failure)
	require.ErrorIs(t, err, ErrAgentPurchaseReview)
	result, err := TransitionAgentPurchaseRefund(prepaidProof(t, f.Reviewer, "purchase.refund.resolve", success.RequestID, success), "purchase.refund.resolve", success)
	require.NoError(t, err)
	assert.Equal(t, "succeeded", result.State)
	var receipt AgentOutboundPaymentReceipt
	require.NoError(t, DB.Where("business_key = ?", r.BusinessKey).First(&receipt).Error)
	assert.Equal(t, success.PayoutPaidAt, receipt.PaidAt)
	assert.Equal(t, instruction.RecipientDigest, receipt.BeneficiaryDigest)
	assert.Equal(t, f.Reviewer.UserID, receipt.VerifiedBy)
	assertPrepaidReconciled(t)
}

func TestAgentPrepaidCumulativeCostBlocksSpendRefundArbitrage(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		q, spend                int
		rate                    int
		paid, charged, released int64
	}{
		{"zero_prefix", 10, 1, 7000, 7, 1, 6}, {"near_total", 20, 19, 1000, 2, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setupPrepaidTest(t)
			p := f.Policy
			p.ID = 0
			p.RequestID = "carry-policy-" + tc.name
			p.PurchaseQuotaNumerator = 1
			p.PurchaseRateBPS = int64(tc.rate)
			p.RefundFeeBPS = 0
			require.NoError(t, ValidateAgentPolicy(&p))
			require.NoError(t, DB.Create(&p).Error)
			require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
			for iteration := range 2 {
				key := fmt.Sprintf("carry-%s-%d", tc.name, iteration)
				order := prepaidCredit(t, f, tc.q, key)
				require.Equal(t, strconv.FormatInt(tc.paid, 10), order.PaidMinor)
				cmd := AgentProcurementSelfUseRequest{RequestID: "use-" + key, BatchID: order.FundBatchID, Quota: tc.spend}
				_, err := AgentProcurementSelfUse(prepaidProof(t, f.Owner, "procurement.self_use", cmd.RequestID, cmd), cmd)
				require.NoError(t, err)
				var e AgentFundEconomics
				require.NoError(t, DB.First(&e, "batch_id = ?", order.FundBatchID).Error)
				assert.Equal(t, tc.charged, e.ChargedMinor)
				if tc.released == 0 {
					req := AgentPurchaseRefundCommand{RequestID: "refund-zero-" + key, BatchID: order.FundBatchID, Quota: tc.q - tc.spend}
					_, err = PrepareAgentPurchaseRefundContext(f.Owner, "purchase.refund.request", req)
					require.ErrorIs(t, err, ErrAgentRefundInvalid)
				} else {
					r := manualRefundPending(t, f, order, tc.q-tc.spend, key)
					require.Equal(t, strconv.FormatInt(tc.released, 10), r.PrincipalMinor)
					r = prepaidRefundAdvance(t, f, r, "purchase.refund.resolve", "resolve-"+key, AgentPurchaseRefundCommand{BusinessKey: r.BusinessKey, Outcome: "success", PayoutPaidAt: common.GetTimestamp(), EvidenceReference: "independently-paid", ExternalReference: "outgoing-" + key})
					assert.Equal(t, "succeeded", r.State)
				}
				require.NoError(t, DB.First(&e, "batch_id = ?", order.FundBatchID).Error)
				assert.Equal(t, tc.released, e.ReleasedMinor)
				assert.Equal(t, tc.paid, e.ChargedMinor+e.ReleasedMinor)
			}
			assert.Equal(t, 2*tc.spend, getUserBalances(t, f.Owner.UserID).Quota)
			assertPrepaidReconciled(t)
		})
	}
}

func TestAgentPrepaidRefundCumulativeFeeAndUnknownCarry(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "fee-carry")
	gross, fees := []string{"29", "30", "31"}, []string{"0", "1", "1"}
	for i, q := range []int{333, 333, 334} {
		key := fmt.Sprintf("fee-%d", i)
		r := manualRefundPending(t, f, order, q, key)
		require.Equal(t, gross[i], r.PrincipalMinor)
		require.Equal(t, fees[i], r.FeeMinor)
		if i == 0 {
			r = prepaidRefundAdvance(t, f, r, "purchase.refund.resolve", "unknown-fee", AgentPurchaseRefundCommand{BusinessKey: r.BusinessKey, Outcome: "unknown", EvidenceReference: "bank-lookup-pending"})
			req := AgentPurchaseRefundCommand{RequestID: "parallel-refund", BatchID: order.FundBatchID, Quota: 1}
			_, err := PrepareAgentPurchaseRefundContext(f.Owner, "purchase.refund.request", req)
			require.ErrorIs(t, err, ErrAgentRefundUnknown)
			var before AgentFundEconomics
			require.NoError(t, DB.First(&before, "batch_id = ?", order.FundBatchID).Error)
			assert.Zero(t, before.ReleasedMinor)
			r = prepaidRefundAdvance(t, f, r, "purchase.refund.resolve", "failed-fee", AgentPurchaseRefundCommand{BusinessKey: r.BusinessKey, Outcome: "explicit_failed", NoPaymentConfirmed: true, EvidenceReference: "independent-bank-confirms-unpaid"})
			require.Equal(t, "failed", r.State)
			var after AgentFundEconomics
			require.NoError(t, DB.First(&after, "batch_id = ?", order.FundBatchID).Error)
			assert.Zero(t, after.ReleasedMinor)
			assert.Zero(t, after.ReleasedQuota)
			r = manualRefundPending(t, f, order, q, "fee-retry")
			require.Equal(t, gross[i], r.PrincipalMinor)
			require.Equal(t, fees[i], r.FeeMinor)
		}
		r = prepaidRefundAdvance(t, f, r, "purchase.refund.resolve", "paid-"+key, AgentPurchaseRefundCommand{BusinessKey: r.BusinessKey, Outcome: "success", PayoutPaidAt: common.GetTimestamp(), EvidenceReference: "independent-bank-paid", ExternalReference: "fee-bank-" + key})
		require.Equal(t, "succeeded", r.State)
	}
	var e AgentFundEconomics
	require.NoError(t, DB.First(&e, "batch_id = ?", order.FundBatchID).Error)
	assert.Equal(t, int64(90), e.ReleasedMinor)
	assert.Equal(t, 1000, e.ReleasedQuota)
	var rows []AgentOutboundPaymentReceipt
	require.NoError(t, DB.Where("owner_user_id = ? AND purpose = ?", f.Owner.UserID, "purchase_refund").Find(&rows).Error)
	var paid int64
	for _, r := range rows {
		paid += r.PaidMinor
	}
	assert.Equal(t, int64(88), paid)
	assertAgentBudget(t, f.Owner.UserID, 0, 0, 0, 0)
	assertPrepaidReconciled(t)
}

func TestAgentManualRefundOriginalProviderRouteNeverBecomesBankFallback(t *testing.T) {
	for _, provider := range []string{PaymentProviderStripe, "bank_transfer"} {
		t.Run(provider, func(t *testing.T) {
			f := setupPrepaidTest(t)
			order := prepaidOrder(t, f, 1000, "original-"+provider)
			originalReference := "pi_synthetic_original_" + provider
			cmd := AgentPurchaseCommand{RequestID: "original-record-" + provider, OrderID: order.ID, Version: order.Version, EvidenceReference: "document:original-payment", Provider: provider, MerchantScope: "fixture-merchant", Environment: "live", ExternalReference: originalReference, PaidMinor: order.PaidMinor, Currency: order.Currency, PaidAt: common.GetTimestamp()}
			var err error
			order, err = RecordAgentPurchasePayment(prepaidProof(t, f.Finance, "purchase.payment.record", cmd.RequestID, cmd), cmd)
			require.NoError(t, err)
			cmd = AgentPurchaseCommand{RequestID: "original-confirm-" + provider, OrderID: order.ID, Version: order.Version}
			order, err = ConfirmAgentPurchasePayment(prepaidProof(t, f.Reviewer, "purchase.payment.confirm", cmd.RequestID, cmd), cmd)
			require.NoError(t, err)
			r := prepaidRefund(t, f, order.FundBatchID, 500, "original-"+provider)
			r = prepaidRefundAdvance(t, f, r, "purchase.refund.approve", "original-approve-"+provider, AgentPurchaseRefundCommand{Decision: "approve", EvidenceReference: "independent-review"})
			if provider == "bank_transfer" {
				_, err = GetAgentManualRefundInstruction(f.Finance, "purchase", int64(r.ID))
				require.ErrorIs(t, err, ErrAgentStatementUnresolved)
				submit := AgentPurchaseRefundCommand{RequestID: "original-bank-submit", RefundID: r.ID, Version: r.Version, BusinessKey: r.BusinessKey}
				_, err = PrepareAgentPurchaseRefundContext(f.Finance, "purchase.refund.submit", submit)
				require.ErrorIs(t, err, ErrAgentStatementUnresolved)
				// Missing original payer facts are recovered only by independently verified
				// immutable evidence. The new statement-import feature remains disabled.
				assert.False(t, f.Policy.ManualReconciliationEnabled)
				var receipt AgentPaymentReceipt
				require.NoError(t, DB.First(&receipt, order.PaymentReceiptID).Error)
				evidence := AgentStatementCommand{RequestID: "original-bank-evidence", ReceiptType: "incoming_receipt", ReceiptID: receipt.ID, DossierReference: "dossier:recovered-original-payer", PaidAt: receipt.PaidAt, EvidenceReference: "proof:original-payer-bank", Reason: "Synthetic original payer documentary recovery"}
				result := statementExecuteTest(t, f.Finance, "statement.evidence.propose", evidence)
				statementExecuteTest(t, f.Reviewer, "statement.evidence.confirm", AgentStatementCommand{RequestID: "original-bank-evidence-confirm", EvidenceID: result.ResourceID, ExpectedVersion: 1, DossierReference: evidence.DossierReference, PaidAt: evidence.PaidAt, EvidenceReference: evidence.EvidenceReference, Reason: "Synthetic independent original payer review"})
				instruction, err := GetAgentManualRefundInstruction(f.Finance, "purchase", int64(r.ID))
				require.NoError(t, err)
				assert.Equal(t, evidence.DossierReference, instruction.PayerDossierReference)
				assert.Empty(t, instruction.OriginalPaymentReference)
				r = prepaidRefundAdvance(t, f, r, "purchase.refund.submit", "recovered-bank-submit", AgentPurchaseRefundCommand{BusinessKey: r.BusinessKey})
				r = prepaidRefundAdvance(t, f, r, "purchase.refund.resolve", "recovered-bank-paid", AgentPurchaseRefundCommand{BusinessKey: r.BusinessKey, Outcome: "success", PayoutPaidAt: common.GetTimestamp(), EvidenceReference: "proof:original-payer-refund", ExternalReference: "bank-refund-original"})
				assert.Equal(t, "succeeded", r.State)
				require.NoError(t, DB.First(&receipt, receipt.ID).Error)
				assert.Empty(t, receipt.CounterpartyDigest, "historical receipt is never overwritten")
			} else {
				instruction, err := GetAgentManualRefundInstruction(f.Finance, "purchase", int64(r.ID))
				require.NoError(t, err)
				assert.Equal(t, "original_provider_payment", instruction.RecipientKind)
				assert.Equal(t, originalReference, instruction.OriginalPaymentReference)
				assert.Empty(t, instruction.PayerDossierReference)
				r = prepaidRefundAdvance(t, f, r, "purchase.refund.submit", "original-provider-submit", AgentPurchaseRefundCommand{BusinessKey: r.BusinessKey})
				resolve := AgentPurchaseRefundCommand{RequestID: "original-provider-paid", RefundID: r.ID, Version: r.Version, BusinessKey: r.BusinessKey, Outcome: "success", PayoutPaidAt: common.GetTimestamp(), EvidenceReference: "proof:original-provider-reversal", ExternalReference: "re_synthetic_refund"}
				_, err = PrepareAgentPurchaseRefundContext(f.Reviewer, "purchase.refund.resolve", resolve)
				require.ErrorIs(t, err, ErrAgentRefundInvalid)
				resolve.OriginalPaymentReference = "pi_wrong_original"
				_, err = PrepareAgentPurchaseRefundContext(f.Reviewer, "purchase.refund.resolve", resolve)
				require.ErrorIs(t, err, ErrAgentRefundInvalid)
				resolve.OriginalPaymentReference = originalReference
				r, err = TransitionAgentPurchaseRefund(prepaidProof(t, f.Reviewer, "purchase.refund.resolve", resolve.RequestID, resolve), "purchase.refund.resolve", resolve)
				require.NoError(t, err)
				assert.Equal(t, "succeeded", r.State)
				var payout AgentOutboundPaymentReceipt
				require.NoError(t, DB.Where("business_key = ?", r.BusinessKey).First(&payout).Error)
				assert.Equal(t, provider, payout.Provider)
				assert.Equal(t, "fixture-merchant", payout.MerchantScope)
				assert.Equal(t, instruction.RecipientDigest, payout.BeneficiaryDigest)
			}
			assertPrepaidReconciled(t)
		})
	}
}

func TestAgentManualRefundApprovedUnsubmittedCancellationPreservesCashCarry(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "cancel-approved")
	r := prepaidRefund(t, f, order.FundBatchID, 333, "cancel-approved")
	r = prepaidRefundAdvance(t, f, r, "purchase.refund.approve", "cancel-approved-review", AgentPurchaseRefundCommand{Decision: "approve", EvidenceReference: "independent-original-approval"})
	r = prepaidRefundAdvance(t, f, r, "purchase.refund.reject", "cancel-approved-cancel", AgentPurchaseRefundCommand{Decision: "reject", EvidenceReference: "independently-confirmed-never-submitted"})
	assert.Equal(t, "rejected", r.State)
	assertAgentBudget(t, f.Owner.UserID, 1000, 0, 0, 1000)
	var e AgentFundEconomics
	require.NoError(t, DB.First(&e, "batch_id = ?", order.FundBatchID).Error)
	assert.Zero(t, e.ReleasedMinor)
	assert.Zero(t, e.ReleasedQuota)
	var payouts int64
	require.NoError(t, DB.Model(&AgentOutboundPaymentReceipt{}).Count(&payouts).Error)
	assert.Zero(t, payouts)
	r = manualRefundPending(t, f, order, 333, "cancel-pending-forbidden")
	bad := AgentPurchaseRefundCommand{RequestID: "reject-pending-forbidden", RefundID: r.ID, Version: r.Version, Decision: "reject", EvidenceReference: "not-proof-of-nonpayment"}
	_, err := TransitionAgentPurchaseRefund(prepaidProof(t, f.Reviewer, "purchase.refund.reject", bad.RequestID, bad), "purchase.refund.reject", bad)
	require.ErrorIs(t, err, ErrAgentRefundState)
	assertAgentBudget(t, f.Owner.UserID, 667, 333, 0, 1000)
	assertPrepaidReconciled(t)
}
