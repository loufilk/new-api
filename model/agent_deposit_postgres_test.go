package model

import (
	"strconv"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func depositProof(t *testing.T, id AuthSessionIdentity, action string, c AgentDepositCommand) AgentTransactionAuthorization {
	t.Helper()
	b, err := common.Marshal(c)
	require.NoError(t, err)
	p, err := PrepareAgentDepositOperation(id, action, c.RequestID, b)
	require.NoError(t, err)
	require.NotNil(t, p)
	_, auth := issueAgentProfileTestProof(t, id, p.VerificationContext)
	return auth
}
func depositExecute(t *testing.T, id AuthSessionIdentity, action string, c AgentDepositCommand) *AgentOperationStatus {
	t.Helper()
	r, err := ExecuteAgentDepositOperation(depositProof(t, id, action, c), action, c)
	require.NoError(t, err)
	return r
}
func depositReceipt(t *testing.T, f prepaidFixture, amount int64, suffix string) AgentDepositReceipt {
	t.Helper()
	c := AgentDepositCommand{PayerReference: "controlled-payer-fixture", RequestID: "deposit-record-" + suffix, AgentUserID: f.Owner.UserID, AmountMinor: strconv.FormatInt(amount, 10), Currency: "USD", CurrencyExponent: 2, Provider: "bank_transfer", MerchantScope: "fixture-deposit-bank", Environment: "live", ExternalReference: "deposit-external-" + suffix, PaidAt: common.GetTimestamp(), EvidenceReference: "verified-original-sender-statement"}
	r := depositExecute(t, f.Finance, "deposit.receipt.record", c).Resource.(AgentDepositReceipt)
	c = AgentDepositCommand{PayerReference: "controlled-payer-fixture", RequestID: "deposit-confirm-" + suffix, ResourceID: r.ID, Version: r.Version, EvidenceReference: "independent-deposit-verification"}
	r = depositExecute(t, f.Reviewer, "deposit.receipt.confirm", c).Resource.(AgentDepositReceipt)
	return r
}
func assertDepositAccount(t *testing.T, owner int, received, available, held, offset, refunded int64) {
	t.Helper()
	var a AgentDepositAccount
	require.NoError(t, DB.Where("agent_user_id = ?", owner).First(&a).Error)
	want := []int64{received, available, held, offset, refunded}
	assert.Equal(t, want, []int64{a.ReceivedMinor, a.AvailableMinor, a.HeldMinor, a.OffsetMinor, a.RefundedMinor})
	require.NoError(t, validateAgentDepositAccount(&a))
	var ledgers []AgentDepositLedger
	require.NoError(t, DB.Where("agent_user_id = ?", owner).Order("id").Find(&ledgers).Error)
	totals := make([]int64, 5)
	for _, e := range ledgers {
		for i, v := range []int64{e.ReceivedDelta, e.AvailableDelta, e.HeldDelta, e.OffsetDelta, e.RefundedDelta} {
			totals[i] += v
			assert.GreaterOrEqual(t, totals[i], int64(0))
		}
		assert.Equal(t, totals, []int64{e.ReceivedAfter, e.AvailableAfter, e.HeldAfter, e.OffsetAfter, e.RefundedAfter})
		assert.Equal(t, totals[0], totals[1]+totals[2]+totals[3]+totals[4])
	}
	assert.Equal(t, want, totals)
	var deposits []AgentDepositReceipt
	require.NoError(t, DB.Where("agent_user_id = ? AND state = ?", owner, "confirmed").Find(&deposits).Error)
	totals = make([]int64, 5)
	for i := range deposits {
		r := &deposits[i]
		require.NoError(t, validateAgentDepositReceipt(r))
		for j, v := range []int64{r.AmountMinor, r.AvailableMinor, r.HeldMinor, r.OffsetMinor, r.RefundedMinor} {
			totals[j] += v
		}
	}
	assert.Equal(t, want, totals)
}
func depositRefundRequest(t *testing.T, f prepaidFixture, r AgentDepositReceipt, amount int64, suffix string) AgentDepositOperationRequest {
	t.Helper()
	return depositExecute(t, f.Owner, "deposit.refund.request", AgentDepositCommand{RequestID: "deposit-refund-request-" + suffix, DepositID: r.ID, AmountMinor: strconv.FormatInt(amount, 10), Reason: "Explicit source deposit refund"}).Resource.(AgentDepositOperationRequest)
}
func depositRefundAdvance(t *testing.T, f prepaidFixture, r AgentDepositOperationRequest, action, suffix string, c AgentDepositCommand) AgentDepositOperationRequest {
	t.Helper()
	c.RequestID = "deposit-refund-" + suffix
	c.ResourceID = r.ID
	c.Version = r.Version
	if c.EvidenceReference == "" {
		c.EvidenceReference = "independent-offline-payment-evidence"
	}
	if c.Reason == "" && (action == "deposit.refund.approve" || action == "deposit.refund.reject") {
		c.Reason = "Independent review"
	}
	id := f.Finance
	if action == "deposit.refund.resolve" {
		id = f.Reviewer
	}
	return depositExecute(t, id, action, c).Resource.(AgentDepositOperationRequest)
}

func TestAgentDepositIndependentReceiptPurposeAndNoWalletCreditPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	c := AgentDepositCommand{PayerReference: "controlled-payer-fixture", RequestID: "deposit-independent-record", AgentUserID: f.Owner.UserID, AmountMinor: "50", Currency: "USD", CurrencyExponent: 2, Provider: "bank_transfer", MerchantScope: "fixture-bank", Environment: "live", ExternalReference: "deposit-independent-external", PaidAt: common.GetTimestamp(), EvidenceReference: "verified-independent-deposit"}
	r := depositExecute(t, f.Finance, "deposit.receipt.record", c).Resource.(AgentDepositReceipt)
	var n int64
	require.NoError(t, DB.Model(&AgentDepositAccount{}).Count(&n).Error)
	assert.Zero(t, n)
	confirm := AgentDepositCommand{PayerReference: "controlled-payer-fixture", RequestID: "deposit-independent-confirm", ResourceID: r.ID, Version: r.Version, EvidenceReference: "second-finance-check"}
	b, _ := common.Marshal(confirm)
	_, err := PrepareAgentDepositOperation(f.Finance, "deposit.receipt.confirm", confirm.RequestID, b)
	require.ErrorIs(t, err, ErrAgentPurchaseReview)
	auth := depositProof(t, f.Reviewer, "deposit.receipt.confirm", confirm)
	_, err = ExecuteAgentDepositOperation(auth, "deposit.receipt.confirm", confirm)
	require.NoError(t, err)
	_, err = ExecuteAgentDepositOperation(auth, "deposit.receipt.confirm", confirm)
	require.NoError(t, err)
	assertDepositAccount(t, f.Owner.UserID, 50, 50, 0, 0, 0)
	var receipt AgentPaymentReceipt
	require.NoError(t, DB.First(&receipt, r.PaymentReceiptID).Error)
	receipt.Purpose = "credit_repayment"
	receipt.LocalOrderKey = "illegal-reuse-deposit"
	_, err = ClaimAgentPaymentReceiptTx(DB, &receipt)
	require.ErrorIs(t, err, ErrAgentPaymentReceiptConflict)
	assert.Zero(t, getUserBalances(t, f.Owner.UserID).Quota)
	require.NoError(t, DB.Model(&AgentRedemptionAccount{}).Count(&n).Error)
	assert.Zero(t, n)
	require.NoError(t, DB.Model(&AgentCommissionOutbox{}).Count(&n).Error)
	assert.Zero(t, n)
	c.RequestID = "deposit-test-cannot-credit"
	c.ExternalReference = "test-deposit"
	c.Environment = "test"
	b, _ = common.Marshal(c)
	_, err = PrepareAgentDepositOperation(f.Finance, "deposit.receipt.record", c.RequestID, b)
	require.Error(t, err)
}
func TestAgentDepositExplicitOffsetDoesNotSeizeQuotaOrPretendRepaymentPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "offset")
	draw := creditDraw(t, f, limit, 10, "offset")
	creditSelfUse(t, f, draw, 10, "offset")
	ar := creditARs(t, f.Owner.UserID)[0]
	deposit := depositReceipt(t, f, 50, "offset")
	request := AgentDepositCommand{RequestID: "deposit-offset-request", DepositID: deposit.ID, AmountMinor: "3", Allocations: []AgentReceivableAllocationInput{{ReceivableID: ar.ID, AmountMinor: "3"}}, Reason: "Owner explicitly consents to named receivable offset"}
	r := depositExecute(t, f.Owner, "deposit.offset.request", request).Resource.(AgentDepositOperationRequest)
	assertDepositAccount(t, f.Owner.UserID, 50, 47, 3, 0, 0)
	assertCreditAccount(t, f.Owner.UserID, 0, 7, 0)
	approve := AgentDepositCommand{RequestID: "deposit-offset-approve", ResourceID: r.ID, Version: r.Version, Reason: "Independent offset approval", EvidenceReference: "signed-offset-authorization"}
	b, _ := common.Marshal(approve)
	_, err := PrepareAgentDepositOperation(f.Owner, "deposit.offset.approve", approve.RequestID, b)
	require.ErrorIs(t, err, ErrAgentPurchaseReview)
	depositExecute(t, f.Finance, "deposit.offset.approve", approve)
	assertDepositAccount(t, f.Owner.UserID, 50, 47, 0, 3, 0)
	assertCreditAccount(t, f.Owner.UserID, 0, 4, 0)
	ar = creditARs(t, f.Owner.UserID)[0]
	assert.EqualValues(t, 3, ar.OffsetMinor)
	assert.Zero(t, ar.PaidMinor)
	assert.Equal(t, 10, getUserBalances(t, f.Owner.UserID).QuotaReward)
	assertAgentBudget(t, f.Owner.UserID, 0, 0, 10, 10)
}
func TestAgentDepositRefundUnknownFrozenUntilVerifiedUniqueSuccessPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	deposit := depositReceipt(t, f, 50, "unknown")
	r := depositRefundRequest(t, f, deposit, 30, "unknown")
	assert.NotEmpty(t, r.RecipientDigest)
	assert.EqualValues(t, 1, r.RecipientVersion)
	r = depositRefundAdvance(t, f, r, "deposit.refund.approve", "approve-unknown", AgentDepositCommand{})
	r = depositRefundAdvance(t, f, r, "deposit.refund.submit", "submit-unknown", AgentDepositCommand{BusinessKey: r.BusinessKey})
	assertDepositAccount(t, f.Owner.UserID, 50, 20, 30, 0, 0)
	c := AgentDepositCommand{RequestID: "deposit-refund-resolve-self", ResourceID: r.ID, Version: r.Version, BusinessKey: r.BusinessKey, Outcome: "unknown", EvidenceReference: "bank-timeout"}
	b, _ := common.Marshal(c)
	_, err := PrepareAgentDepositOperation(f.Finance, "deposit.refund.resolve", c.RequestID, b)
	require.ErrorIs(t, err, ErrAgentPurchaseReview)
	r = depositRefundAdvance(t, f, r, "deposit.refund.resolve", "resolve-unknown", AgentDepositCommand{BusinessKey: r.BusinessKey, Outcome: "unknown"})
	assert.Equal(t, "payment_unknown", r.State)
	assertDepositAccount(t, f.Owner.UserID, 50, 20, 30, 0, 0)
	c = AgentDepositCommand{RequestID: "deposit-refund-unproven-failure", ResourceID: r.ID, Version: r.Version, BusinessKey: r.BusinessKey, Outcome: "failure", EvidenceReference: "timeout-not-failure"}
	b, _ = common.Marshal(c)
	_, err = PrepareAgentDepositOperation(f.Reviewer, "deposit.refund.resolve", c.RequestID, b)
	require.ErrorIs(t, err, ErrAgentRefundUnknown)
	c = AgentDepositCommand{RequestID: "deposit-refund-success", ResourceID: r.ID, Version: r.Version, BusinessKey: r.BusinessKey, Outcome: "success", Provider: "bank_transfer", MerchantScope: "fixture-bank", Environment: "live", ExternalReference: "unique-deposit-payout", PaidAt: common.GetTimestamp(), EvidenceReference: "independently-verified-success"}
	auth := depositProof(t, f.Reviewer, "deposit.refund.resolve", c)
	_, err = ExecuteAgentDepositOperation(auth, "deposit.refund.resolve", c)
	require.NoError(t, err)
	_, err = ExecuteAgentDepositOperation(auth, "deposit.refund.resolve", c)
	require.NoError(t, err)
	assertDepositAccount(t, f.Owner.UserID, 50, 20, 0, 0, 30)
	var payout AgentOutboundPaymentReceipt
	require.NoError(t, DB.Where("business_key = ?", r.BusinessKey).First(&payout).Error)
	assert.Equal(t, r.RecipientDigest, payout.BeneficiaryDigest)
	assert.Equal(t, c.PaidAt, payout.PaidAt)
	payout.Purpose = "withdrawal"
	payout.BusinessKey = "different-obligation-same-bank-transfer"
	_, err = ClaimAgentOutboundPaymentReceiptTx(DB, &payout)
	require.ErrorIs(t, err, ErrAgentOutboundPaymentConflict)
}
func TestAgentDepositDefiniteUnpaidFailureReleasesOnlyOriginalHoldPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	deposit := depositReceipt(t, f, 50, "failed")
	r := depositRefundRequest(t, f, deposit, 20, "failed")
	r = depositRefundAdvance(t, f, r, "deposit.refund.approve", "approve-failed", AgentDepositCommand{})
	r = depositRefundAdvance(t, f, r, "deposit.refund.submit", "submit-failed", AgentDepositCommand{BusinessKey: r.BusinessKey})
	// Disable entry policies; authoritative UNKNOWN recovery must remain possible.
	p := f.Policy
	p.ID = 0
	p.RequestID = "deposit-disabled-recovery"
	p.DepositEnabled = false
	p.DepositRefundEnabled = false
	p.DepositOffsetEnabled = false
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	r = depositRefundAdvance(t, f, r, "deposit.refund.resolve", "resolve-failed", AgentDepositCommand{BusinessKey: r.BusinessKey, Outcome: "failure", NoPaymentConfirmed: true, EvidenceReference: "definite-bank-rejection-no-money-sent"})
	assert.Equal(t, "failed", r.State)
	assertDepositAccount(t, f.Owner.UserID, 50, 50, 0, 0, 0)
	var n int64
	require.NoError(t, DB.Model(&AgentOutboundPaymentReceipt{}).Count(&n).Error)
	assert.Zero(t, n)
}
func TestAgentDepositConcurrentHoldsCannotOverdrawOrReuseReceiptPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	deposit := depositReceipt(t, f, 50, "hold-race")
	commands := []AgentDepositCommand{{RequestID: "hold-race-one", DepositID: deposit.ID, AmountMinor: "30", Reason: "first requested refund"}, {RequestID: "hold-race-two", DepositID: deposit.ID, AmountMinor: "30", Reason: "second requested refund"}}
	auths := []AgentTransactionAuthorization{depositProof(t, f.Owner, "deposit.refund.request", commands[0]), depositProof(t, f.Owner, "deposit.refund.request", commands[1])}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range commands {
		wg.Go(func() {
			<-start
			_, err := ExecuteAgentDepositOperation(auths[i], "deposit.refund.request", commands[i])
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			require.Error(t, err)
		}
	}
	assert.Equal(t, 1, success)
	assertDepositAccount(t, f.Owner.UserID, 50, 20, 30, 0, 0)
	var requests []AgentDepositOperationRequest
	require.NoError(t, DB.Find(&requests).Error)
	require.Len(t, requests, 1)
	c := AgentDepositCommand{RequestID: "hold-race-reject", ResourceID: requests[0].ID, Version: requests[0].Version, Reason: "Owner cancellation independently reviewed", EvidenceReference: "fixture-rejection"}
	depositExecute(t, f.Finance, "deposit.refund.reject", c)
	assertDepositAccount(t, f.Owner.UserID, 50, 50, 0, 0, 0)
}
func TestAgentDepositSourceIsolationStrictParametersAndReadPagesPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	d1 := depositReceipt(t, f, 10, "source-one")
	depositReceipt(t, f, 20, "source-two")
	c := AgentDepositCommand{RequestID: "cannot-borrow-other-deposit", DepositID: d1.ID, AmountMinor: "11", Reason: "Original source only"}
	payload, _ := common.Marshal(c)
	_, err := PrepareAgentDepositOperation(f.Owner, "deposit.refund.request", c.RequestID, payload)
	require.Error(t, err)
	c.AmountMinor = "5"
	c.Outcome = "success"
	payload, _ = common.Marshal(c)
	_, err = PrepareAgentDepositOperation(f.Owner, "deposit.refund.request", c.RequestID, payload)
	require.Error(t, err)
	c.Outcome = ""
	c.RequestID = "request-bound-id"
	payload, _ = common.Marshal(c)
	_, err = PrepareAgentDepositOperation(f.Owner, "deposit.refund.request", "different-id", payload)
	require.Error(t, err)
	view, err := GetAgentDepositView(f.Owner, 0, 0, 1)
	require.NoError(t, err)
	require.Len(t, view.Receipts, 1)
	assert.True(t, view.HasMore)
	assert.Equal(t, 1, view.NextOffset)
	view2, err := GetAgentDepositView(f.Owner, 0, view.NextOffset, 1)
	require.NoError(t, err)
	require.Len(t, view2.Receipts, 1)
	assert.NotEqual(t, view.Receipts[0].ID, view2.Receipts[0].ID)
	_, err = GetAgentDepositView(f.Other, f.Owner.UserID, 0, 10)
	require.Error(t, err)
	assertDepositAccount(t, f.Owner.UserID, 30, 30, 0, 0, 0)
	require.NoError(t, DB.Model(&AgentDepositAccount{}).Where("agent_user_id = ?", f.Owner.UserID).Update("available_minor", -1).Error)
	_, err = GetAgentDepositView(f.Owner, 0, 0, 10)
	require.ErrorIs(t, err, ErrAgentDepositInvariant)
}

func TestAgentDepositFrozenVerifiedPayerDossierNotSourceIdentityPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	c := AgentDepositCommand{RequestID: "payer-binding-record", AgentUserID: f.Owner.UserID, AmountMinor: "10", Currency: "USD", CurrencyExponent: 2, Provider: "bank_transfer", MerchantScope: "bank", Environment: "live", ExternalReference: "payer-binding-external", PaidAt: common.GetTimestamp(), EvidenceReference: "verified-original-payer", PayerReference: "controlled-dossier:PAYER-A"}
	missing := c
	missing.PayerReference = ""
	b, _ := common.Marshal(missing)
	_, err := PrepareAgentDepositOperation(f.Finance, "deposit.receipt.record", c.RequestID, b)
	require.Error(t, err)
	row := depositExecute(t, f.Finance, "deposit.receipt.record", c).Resource.(AgentDepositReceipt)
	assert.Equal(t, AgentDepositPayerDigest(c.PayerReference), row.PayerDigest)
	confirm := AgentDepositCommand{RequestID: "payer-binding-confirm", ResourceID: row.ID, Version: row.Version, PayerReference: "controlled-dossier:PAYER-B", EvidenceReference: "independent-payer-review"}
	b, _ = common.Marshal(confirm)
	_, err = PrepareAgentDepositOperation(f.Reviewer, "deposit.receipt.confirm", confirm.RequestID, b)
	require.Error(t, err)
	confirm.PayerReference = c.PayerReference
	row = depositExecute(t, f.Reviewer, "deposit.receipt.confirm", confirm).Resource.(AgentDepositReceipt)
	r := depositRefundRequest(t, f, row, 5, "payer-binding")
	assert.Equal(t, row.PayerDigest, r.RecipientDigest)
	view, err := GetAgentDepositView(f.Owner, 0, 0, 25)
	require.NoError(t, err)
	encoded, err := common.Marshal(view)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), c.PayerReference)
	assert.NotContains(t, string(encoded), c.ExternalReference)
	assert.NotContains(t, string(encoded), c.EvidenceReference)
	status, err := FindAgentCreditOperation(f.Finance, c.RequestID)
	require.NoError(t, err)
	encoded, err = common.Marshal(status)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), c.PayerReference)
	require.NoError(t, DB.Model(&AgentDepositReceipt{}).Where("id = ?", row.ID).Updates(map[string]any{"payer_reference": "", "payer_digest": ""}).Error)
	approve := AgentDepositCommand{RequestID: "payer-unbound-blocked-approve", ResourceID: r.ID, Version: r.Version, Reason: "Cannot guess missing history", EvidenceReference: "manual-review"}
	b, _ = common.Marshal(approve)
	_, err = PrepareAgentDepositOperation(f.Finance, "deposit.refund.approve", approve.RequestID, b)
	require.ErrorIs(t, err, ErrAgentDepositInvariant)
	var account AgentDepositAccount
	require.NoError(t, DB.Where("agent_user_id = ?", f.Owner.UserID).First(&account).Error)
	assert.EqualValues(t, 5, account.HeldMinor)
	assert.EqualValues(t, 5, account.AvailableMinor)
}
