package model

import (
	"strconv"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgentCreditTest(t *testing.T) prepaidFixture {
	t.Helper()
	f := setupPrepaidTest(t)
	require.True(t, common.UsingMainDatabase(common.DatabaseTypePostgreSQL), "credit/deposit financial acceptance MUST use real PostgreSQL")
	models := append(append(AgentCreditModels(), AgentDepositModels()...), BonusSourceModels()...)
	require.NoError(t, DB.AutoMigrate(models...))
	for _, m := range models {
		s := &gorm.Statement{DB: DB}
		require.NoError(t, s.Parse(m))
		require.NoError(t, DB.Exec("DELETE FROM "+s.Schema.Table).Error)
	}
	t.Cleanup(func() {
		for _, m := range models {
			s := &gorm.Statement{DB: DB}
			require.NoError(t, s.Parse(m))
			require.NoError(t, DB.Exec("DELETE FROM "+s.Schema.Table).Error)
		}
	})
	p := f.Policy
	p.ID = 0
	p.RequestID = "credit-fixture-policy"
	p.CreditEnabled = true
	p.CreditTermsApproved = true
	p.CreditRecognitionMode = AgentCreditRecognitionSourceSpend
	p.CreditInterestMode = "none"
	p.CreditCurrency = "USD"
	p.CreditCurrencyExponent = 2
	p.CreditMaximumLimitMinor = 100000
	p.CreditTermSeconds = 86400
	p.CreditMaximumTermSeconds = 30 * 86400
	p.CreditQuotaNumerator = 10
	p.CreditQuotaDenominator = 7
	p.CreditRateBPS = 10000
	p.CreditSelfUseEnabled = true
	p.DepositEnabled = true
	p.DepositTermsApproved = true
	p.DepositOffsetEnabled = true
	p.DepositRefundEnabled = true
	p.DepositCurrency = "USD"
	p.DepositCurrencyExponent = 2
	p.DepositMaximumMinor = 100000
	require.NoError(t, ValidateAgentPolicy(&p))
	require.NoError(t, ValidateAgentCreditPolicy(&p))
	require.NoError(t, ValidateAgentDepositPolicy(&p))
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	f.Policy = p
	return f
}
func creditProof(t *testing.T, id AuthSessionIdentity, action string, c AgentCreditCommand) AgentTransactionAuthorization {
	t.Helper()
	b, err := common.Marshal(c)
	require.NoError(t, err)
	p, err := PrepareAgentCreditOperation(id, action, c.RequestID, b)
	require.NoError(t, err)
	require.NotNil(t, p)
	_, auth := issueAgentProfileTestProof(t, id, p.VerificationContext)
	return auth
}
func creditExecute(t *testing.T, id AuthSessionIdentity, action string, c AgentCreditCommand) *AgentOperationStatus {
	t.Helper()
	r, err := ExecuteAgentCreditOperation(creditProof(t, id, action, c), action, c)
	require.NoError(t, err)
	return r
}
func creditLimit(t *testing.T, f prepaidFixture, minor int64, suffix string) AgentCreditLimitVersion {
	t.Helper()
	c := AgentCreditCommand{RequestID: "limit-request-" + suffix, AgentUserID: f.Owner.UserID, AmountMinor: strconv.FormatInt(minor, 10), TermSeconds: 86400, ExpiresAt: common.GetTimestamp() + 30*86400, Reason: "Explicit synthetic agreement", EvidenceReference: "synthetic-credit-contract"}
	r := creditExecute(t, f.Owner, "credit.limit.request", c)
	request := r.Resource.(AgentCreditLimitRequest)
	c = AgentCreditCommand{RequestID: "limit-approve-" + suffix, ResourceID: request.ID, Version: request.Version, RiskApproved: true, EvidenceReference: "synthetic-independent-risk-review"}
	creditExecute(t, f.Finance, "credit.limit.approve", c)
	var limit AgentCreditLimitVersion
	require.NoError(t, DB.Where("request_id = ?", request.ID).First(&limit).Error)
	return limit
}
func creditDraw(t *testing.T, f prepaidFixture, limit AgentCreditLimitVersion, quota int, suffix string) AgentCreditDraw {
	t.Helper()
	r := creditExecute(t, f.Owner, "credit.draw", AgentCreditCommand{RequestID: "draw-" + suffix, Quota: quota, ExpectedLimitID: limit.ID})
	return r.Resource.(AgentCreditDraw)
}
func creditSelfUse(t *testing.T, f prepaidFixture, draw AgentCreditDraw, quota int, suffix string) {
	t.Helper()
	c := AgentProcurementSelfUseRequest{RequestID: "credit-self-use-" + suffix, BatchID: draw.BatchID, Quota: quota}
	_, err := AgentProcurementSelfUse(prepaidProof(t, f.Owner, "procurement.self_use", c.RequestID, c), c)
	require.NoError(t, err)
}
func assertCreditAccount(t *testing.T, owner int, reserved, outstanding, writtenOff int64) {
	t.Helper()
	var a AgentCreditAccount
	require.NoError(t, DB.Where("agent_user_id = ?", owner).First(&a).Error)
	assert.Equal(t, []int64{reserved, outstanding, writtenOff}, []int64{a.ReservedMinor, a.OutstandingMinor, a.WrittenOffMinor})
	var rows []AgentCreditLedger
	require.NoError(t, DB.Where("agent_user_id = ?", owner).Order("id").Find(&rows).Error)
	var r, o, w int64
	for _, e := range rows {
		r += e.ReservedDelta
		o += e.OutstandingDelta
		w += e.WrittenOffDelta
		assert.Equal(t, []int64{r, o, w}, []int64{e.ReservedAfter, e.OutstandingAfter, e.WrittenOffAfter})
		assert.GreaterOrEqual(t, r, int64(0))
		assert.GreaterOrEqual(t, o, int64(0))
		assert.GreaterOrEqual(t, w, int64(0))
	}
	assert.Equal(t, []int64{r, o, w}, []int64{reserved, outstanding, writtenOff})
	var ars []AgentReceivable
	require.NoError(t, DB.Where("agent_user_id = ?", owner).Find(&ars).Error)
	var total, writeoff int64
	for i := range ars {
		left, err := agentReceivableRemaining(&ars[i])
		require.NoError(t, err)
		total += left
		writeoff += ars[i].WrittenOffMinor
	}
	assert.Equal(t, outstanding, total)
	assert.Equal(t, writtenOff, writeoff)
}
func creditRecordRepayment(t *testing.T, f prepaidFixture, alloc []AgentReceivableAllocationInput, amount int64, suffix string) AgentCreditRepayment {
	t.Helper()
	c := AgentCreditCommand{RequestID: "repayment-record-" + suffix, AgentUserID: f.Owner.UserID, AmountMinor: strconv.FormatInt(amount, 10), Currency: "USD", CurrencyExponent: 2, Provider: "bank_transfer", MerchantScope: "fixture-bank", Environment: "live", ExternalReference: "repayment-external-" + suffix, PaidAt: common.GetTimestamp(), EvidenceReference: "synthetic-verified-statement", Allocations: alloc}
	r := creditExecute(t, f.Finance, "credit.repayment.record", c)
	return r.Resource.(AgentCreditRepayment)
}
func creditARs(t *testing.T, owner int) []AgentReceivable {
	t.Helper()
	var ars []AgentReceivable
	require.NoError(t, DB.Where("agent_user_id = ?", owner).Order("id").Find(&ars).Error)
	return ars
}

func TestAgentCreditExplicitPolicyTermsAndIntegerPricePostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	for _, mutate := range []func(*AgentPolicy){func(p *AgentPolicy) { p.CreditRecognitionMode = "" }, func(p *AgentPolicy) { p.CreditInterestMode = "compound" }, func(p *AgentPolicy) { p.CreditTermsApproved = false }, func(p *AgentPolicy) { p.CreditCurrencyExponent = 0 }, func(p *AgentPolicy) { p.CreditQuotaNumerator = 0 }, func(p *AgentPolicy) { p.CreditMaximumTermSeconds = 0 }} {
		p := f.Policy
		mutate(&p)
		require.Error(t, ValidateAgentCreditPolicy(&p))
		require.Error(t, ValidateAgentPolicy(&p))
	}
	for _, tc := range []struct {
		quota int
		want  int64
	}{{1, 1}, {2, 2}, {3, 3}, {10, 7}, {11, 8}} {
		got, err := agentCreditDrawPrice(&f.Policy, tc.quota)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
	p := f.Policy
	p.CreditQuotaNumerator = 1
	p.CreditQuotaDenominator = int64(common.MaxWalletQuota)
	_, err := agentCreditDrawPrice(&p, common.MaxWalletQuota)
	require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	p = AgentPolicy{Reason: "all off"}
	require.NoError(t, ValidateAgentPolicy(&p))
	_, err = agentCreditDrawPrice(&p, 10)
	require.ErrorIs(t, err, ErrAgentFeatureDisabled)
}
func TestAgentCreditDrawSplitRecognitionReleaseAndNoncashWalletPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "split")
	draw := creditDraw(t, f, limit, 10, "split")
	assertCreditAccount(t, f.Owner.UserID, 7, 0, 0)
	assertAgentBudget(t, f.Owner.UserID, 10, 0, 0, 10)
	var b AgentFundBatch
	require.NoError(t, DB.First(&b, draw.BatchID).Error)
	assert.Equal(t, AgentFundCredit, b.SourceKind)
	assert.Equal(t, AccountReward, b.WalletAccount)
	assert.False(t, b.CashRefundable)
	assert.Zero(t, b.PaymentReceiptID)
	_, err := PrepareAgentPurchaseRefundContext(f.Owner, "purchase.refund.request", AgentPurchaseRefundCommand{RequestID: "illegal-credit-cash", BatchID: b.ID, Quota: 2})
	require.ErrorIs(t, err, ErrAgentRefundInvalid)
	creditSelfUse(t, f, draw, 2, "split-a")
	creditSelfUse(t, f, draw, 3, "split-b")
	assertCreditAccount(t, f.Owner.UserID, 3, 4, 0)
	release := AgentCreditCommand{RequestID: "release-split", ResourceID: draw.ID, Quota: 5}
	auth := creditProof(t, f.Owner, "credit.release", release)
	r, err := ExecuteAgentCreditOperation(auth, "credit.release", release)
	require.NoError(t, err)
	retry, err := ExecuteAgentCreditOperation(auth, "credit.release", release)
	require.NoError(t, err)
	assert.Equal(t, r.ResourceID, retry.ResourceID)
	assertCreditAccount(t, f.Owner.UserID, 0, 4, 0)
	assertAgentBudget(t, f.Owner.UserID, 0, 0, 5, 5)
	assertPrepaidReconciled(t)
	wallet := getUserBalances(t, f.Owner.UserID)
	assert.Equal(t, 5, wallet.QuotaReward)
	assert.Zero(t, wallet.QuotaPaid)
	assert.Zero(t, wallet.QuotaBonus)
	ars := creditARs(t, f.Owner.UserID)
	require.Len(t, ars, 2)
	assert.Equal(t, []int64{2, 2}, []int64{ars[0].PrincipalMinor, ars[1].PrincipalMinor})
	assert.Equal(t, ars[0].RecognizedAt+86400, ars[0].DueAt)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(append(AgentCreditModels(), AgentDepositModels()...)...))
	}
	assertCreditAccount(t, f.Owner.UserID, 0, 4, 0)
	require.ErrorIs(t, DB.Model(&AgentCreditLedger{}).Where("id > 0").Update("outstanding_delta", 999).Error, ErrAgentLedgerImmutable)
}
func TestAgentCreditIndependentLimitVersionAndConcurrentCapPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 7, "race")
	commands := []AgentCreditCommand{{RequestID: "draw-race-a", Quota: 10, ExpectedLimitID: limit.ID}, {RequestID: "draw-race-b", Quota: 10, ExpectedLimitID: limit.ID}}
	auths := []AgentTransactionAuthorization{creditProof(t, f.Owner, "credit.draw", commands[0]), creditProof(t, f.Owner, "credit.draw", commands[1])}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range commands {
		wg.Go(func() {
			<-start
			_, err := ExecuteAgentCreditOperation(auths[i], "credit.draw", commands[i])
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
			require.ErrorIs(t, err, ErrAgentCreditRisk)
		}
	}
	assert.Equal(t, 1, success)
	assertCreditAccount(t, f.Owner.UserID, 7, 0, 0)
	c := AgentCreditCommand{RequestID: "limit-change-request", AmountMinor: "6", TermSeconds: 86400, ExpiresAt: common.GetTimestamp() + 86400, ExpectedLimitID: limit.ID, Reason: "proposed reduction", EvidenceReference: "fixture-contract"}
	request := creditExecute(t, f.Owner, "credit.limit.request", c).Resource.(AgentCreditLimitRequest)
	approval := AgentCreditCommand{RequestID: "limit-change-approve", ResourceID: request.ID, Version: request.Version, RiskApproved: true, EvidenceReference: "fixture-review"}
	payload, _ := common.Marshal(approval)
	_, err := PrepareAgentCreditOperation(f.Owner, "credit.limit.approve", approval.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentPurchaseReview)
	_, err = PrepareAgentCreditOperation(f.Finance, "credit.limit.approve", approval.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentCreditRisk)
	var versions int64
	require.NoError(t, DB.Model(&AgentCreditLimitVersion{}).Count(&versions).Error)
	assert.EqualValues(t, 1, versions)
}
func TestAgentCreditOverduePreservesBoundCustomerAndPaidSourcesPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "protected")
	draw := creditDraw(t, f, limit, 100, "protected")
	c := AgentDeliveryCommand{RequestID: "credit-bound-create", Name: "Protected customer", Quota: 40, ExpiresAt: common.GetTimestamp() + 3600}
	delivery, err := CreateAgentDelivery(prepaidProof(t, f.Owner, "delivery.create", c.RequestID, c), c)
	require.NoError(t, err)
	delivery, err = ClaimAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "credit-bound-claim", ClaimCode: delivery.ClaimCode})
	require.NoError(t, err)
	var code Redemption
	require.NoError(t, DB.First(&code, delivery.RedemptionID).Error)
	assert.True(t, code.SourceProtected)
	creditSelfUse(t, f, draw, 20, "protected-overdue")
	ars := creditARs(t, f.Owner.UserID)
	require.Len(t, ars, 1)
	now := common.GetTimestamp()
	require.NoError(t, DB.Model(&AgentReceivable{}).Where("id = ?", ars[0].ID).Updates(map[string]any{"recognized_at": now - 86401, "due_at": now - 1}).Error)
	next := AgentCreditCommand{RequestID: "overdue-new-draw", Quota: 1, ExpectedLimitID: limit.ID}
	payload, _ := common.Marshal(next)
	_, err = PrepareAgentCreditOperation(f.Owner, "credit.draw", next.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentCreditRisk)
	// Separate prepaid procurement remains usable; FIFO must skip restricted credit.
	prepaidCredit(t, f, 100, "overdue-prepaid")
	paid := AgentDeliveryCommand{RequestID: "overdue-paid-delivery", Name: "Prepaid unaffected", Quota: 50}
	_, err = CreateAgentDelivery(prepaidProof(t, f.Owner, "delivery.create", paid.RequestID, paid), paid)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Updates(map[string]any{"sales_frozen": true, "status": AgentProfileExited, "version": 2}).Error)
	policy := f.Policy
	policy.ID = 0
	policy.RequestID = "credit-disabled-recovery"
	policy.CreditEnabled = false
	policy.CreditSelfUseEnabled = false
	require.NoError(t, DB.Create(&policy).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", policy.ID).Error)
	delivery, err = ConfirmAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "credit-bound-confirm", DeliveryID: delivery.ID, Version: delivery.Version})
	require.NoError(t, err)
	assert.Equal(t, "redeemed", delivery.State)
	assertCreditAccount(t, f.Owner.UserID, 28, 42, 0)
	wallet := getUserBalances(t, f.Customer.UserID)
	assert.Equal(t, 40, wallet.QuotaReward)
	assert.Zero(t, wallet.QuotaPaid)
	assertPrepaidReconciled(t)
}
func TestAgentCreditManualRepaymentPartialInvoiceReceiptAndLostAckPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "repay")
	draw := creditDraw(t, f, limit, 10, "repay")
	creditSelfUse(t, f, draw, 10, "repay")
	ar := creditARs(t, f.Owner.UserID)[0]
	invoice := creditExecute(t, f.Finance, "credit.invoice.issue", AgentCreditCommand{RequestID: "invoice-repay", ResourceID: ar.ID, Version: ar.Version, InvoiceNumber: "SYNTHETIC-INVOICE-001", EvidenceReference: "fixture-invoice"}).Resource.(AgentReceivableInvoice)
	assert.Equal(t, ar.DueAt, invoice.DueAt)
	assertCreditAccount(t, f.Owner.UserID, 0, 7, 0)
	r := creditRecordRepayment(t, f, []AgentReceivableAllocationInput{{ReceivableID: ar.ID, AmountMinor: "3"}}, 3, "partial")
	assertCreditAccount(t, f.Owner.UserID, 0, 7, 0)
	c := AgentCreditCommand{RequestID: "repayment-confirm-partial", ResourceID: r.ID, Version: r.Version, EvidenceReference: "independent-bank-check"}
	b, _ := common.Marshal(c)
	_, err := PrepareAgentCreditOperation(f.Finance, "credit.repayment.confirm", c.RequestID, b)
	require.ErrorIs(t, err, ErrAgentPurchaseReview)
	auth := creditProof(t, f.Reviewer, "credit.repayment.confirm", c)
	_, err = ExecuteAgentCreditOperation(auth, "credit.repayment.confirm", c)
	require.NoError(t, err)
	_, err = ExecuteAgentCreditOperation(auth, "credit.repayment.confirm", c)
	require.NoError(t, err)
	assertCreditAccount(t, f.Owner.UserID, 0, 4, 0)
	status, found, err := ReplayAgentCreditOperation(f.Reviewer, "credit.repayment.confirm", c.RequestID, b)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "confirmed", status.(*AgentOperationStatus).ResourceState)
	c.EvidenceReference = "changed-on-retry"
	b, _ = common.Marshal(c)
	_, _, err = ReplayAgentCreditOperation(f.Reviewer, "credit.repayment.confirm", c.RequestID, b)
	require.ErrorIs(t, err, ErrAgentQuotaConflict)
	var receipt AgentPaymentReceipt
	require.NoError(t, DB.First(&receipt, r.ReceiptID).Error)
	receipt.Purpose = "topup"
	receipt.LocalOrderKey = "would-double-credit"
	_, err = ClaimAgentPaymentReceiptTx(DB, &receipt)
	require.ErrorIs(t, err, ErrAgentPaymentReceiptConflict)
	wallet := getUserBalances(t, f.Owner.UserID)
	assert.Equal(t, 10, wallet.QuotaReward)
	var commissions, topups int64
	require.NoError(t, DB.Model(&AgentCommissionOutbox{}).Count(&commissions).Error)
	require.NoError(t, DB.Model(&TopUp{}).Count(&topups).Error)
	assert.Zero(t, commissions)
	assert.Zero(t, topups)
}
func TestAgentCreditAdjustmentsNeverPretendCashOrRenewWrittenOffRiskPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "writeoff")
	draw := creditDraw(t, f, limit, 10, "writeoff")
	creditSelfUse(t, f, draw, 10, "writeoff")
	ar := creditARs(t, f.Owner.UserID)[0]
	row := creditExecute(t, f.Finance, "credit.adjustment.request", AgentCreditCommand{RequestID: "writeoff-request", ResourceID: ar.ID, Version: ar.Version, AmountMinor: "4", AdjustmentKind: "write_off", Reason: "Manual documented uncollectible exposure", EvidenceReference: "fixture-loss-evidence"}).Resource.(AgentReceivableAdjustment)
	approval := AgentCreditCommand{RequestID: "writeoff-approve", ResourceID: row.ID, Version: row.Version, Reason: "Independent loss review", EvidenceReference: "fixture-loss-check"}
	payload, _ := common.Marshal(approval)
	_, err := PrepareAgentCreditOperation(f.Finance, "credit.adjustment.approve", approval.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentPurchaseReview)
	creditExecute(t, f.Reviewer, "credit.adjustment.approve", approval)
	assertCreditAccount(t, f.Owner.UserID, 0, 3, 4)
	c := AgentCreditCommand{RequestID: "writeoff-cannot-draw", Quota: 1, ExpectedLimitID: limit.ID}
	payload, _ = common.Marshal(c)
	_, err = PrepareAgentCreditOperation(f.Owner, "credit.draw", c.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentCreditRisk)
	ar = creditARs(t, f.Owner.UserID)[0]
	assert.Zero(t, ar.PaidMinor)
	row = creditExecute(t, f.Finance, "credit.adjustment.request", AgentCreditCommand{RequestID: "reverse-writeoff-request", ResourceID: ar.ID, Version: ar.Version, AmountMinor: "4", AdjustmentKind: "reverse_write_off", Reason: "Corrected loss classification", EvidenceReference: "fixture-correction"}).Resource.(AgentReceivableAdjustment)
	creditExecute(t, f.Reviewer, "credit.adjustment.approve", AgentCreditCommand{RequestID: "reverse-writeoff-approve", ResourceID: row.ID, Version: row.Version, Reason: "Independent correction review", EvidenceReference: "fixture-correction-check"})
	assertCreditAccount(t, f.Owner.UserID, 0, 7, 0)
	ar = creditARs(t, f.Owner.UserID)[0]
	row = creditExecute(t, f.Finance, "credit.adjustment.request", AgentCreditCommand{RequestID: "correction-request", ResourceID: ar.ID, Version: ar.Version, AmountMinor: "1", AdjustmentKind: "correction_credit", Reason: "Documented principal recognition correction", EvidenceReference: "fixture-principal-correction"}).Resource.(AgentReceivableAdjustment)
	creditExecute(t, f.Reviewer, "credit.adjustment.approve", AgentCreditCommand{RequestID: "correction-approve", ResourceID: row.ID, Version: row.Version, Reason: "Independent principal correction", EvidenceReference: "fixture-principal-check"})
	assertCreditAccount(t, f.Owner.UserID, 0, 6, 0)
	var receipts int64
	require.NoError(t, DB.Model(&AgentPaymentReceipt{}).Count(&receipts).Error)
	assert.Zero(t, receipts)
	assert.Equal(t, 10, getUserBalances(t, f.Owner.UserID).QuotaReward)
}
func TestAgentCreditStaleProofRevocationAndRequestBindingPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "proof")
	c := AgentCreditCommand{RequestID: "credit-proof-draw", Quota: 10, ExpectedLimitID: limit.ID}
	auth := creditProof(t, f.Owner, "credit.draw", c)
	changed := c
	changed.Quota = 11
	_, err := ExecuteAgentCreditOperation(auth, "credit.draw", changed)
	require.ErrorIs(t, err, ErrAgentTransactionProof)
	assertCreditAccount(t, f.Owner.UserID, 0, 0, 0)
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Update("version", 2).Error)
	_, err = ExecuteAgentCreditOperation(auth, "credit.draw", c)
	require.Error(t, err)
	assertCreditAccount(t, f.Owner.UserID, 0, 0, 0)
	auth = creditProof(t, f.Owner, "credit.draw", c)
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", f.Owner.SessionID).Update("version", 2).Error)
	_, err = ExecuteAgentCreditOperation(auth, "credit.draw", c)
	require.Error(t, err)
	assertCreditAccount(t, f.Owner.UserID, 0, 0, 0)
	payload, _ := common.Marshal(c)
	pat := f.Owner
	pat.SessionID = ""
	_, err = PrepareAgentCreditOperation(pat, "credit.draw", c.RequestID, payload)
	require.Error(t, err)
	var draws int64
	require.NoError(t, DB.Model(&AgentCreditDraw{}).Count(&draws).Error)
	assert.Zero(t, draws)
}
func TestAgentCreditConcurrentRepaymentAndReallocationRecoveryPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "repay-race")
	draw := creditDraw(t, f, limit, 20, "repay-race")
	creditSelfUse(t, f, draw, 10, "repay-race-a")
	creditSelfUse(t, f, draw, 10, "repay-race-b")
	ars := creditARs(t, f.Owner.UserID)
	r1 := creditRecordRepayment(t, f, []AgentReceivableAllocationInput{{ReceivableID: ars[0].ID, AmountMinor: "7"}}, 7, "race-a")
	r2 := creditRecordRepayment(t, f, []AgentReceivableAllocationInput{{ReceivableID: ars[0].ID, AmountMinor: "7"}}, 7, "race-b")
	commands := []AgentCreditCommand{{RequestID: "repay-race-confirm-a", ResourceID: r1.ID, Version: r1.Version, EvidenceReference: "bank-check-a"}, {RequestID: "repay-race-confirm-b", ResourceID: r2.ID, Version: r2.Version, EvidenceReference: "bank-check-b"}}
	auths := []AgentTransactionAuthorization{creditProof(t, f.Reviewer, "credit.repayment.confirm", commands[0]), creditProof(t, f.Reviewer, "credit.repayment.confirm", commands[1])}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range commands {
		wg.Go(func() {
			<-start
			_, err := ExecuteAgentCreditOperation(auths[i], "credit.repayment.confirm", commands[i])
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
			require.ErrorIs(t, err, ErrAgentCreditState)
		}
	}
	assert.Equal(t, 1, success)
	assertCreditAccount(t, f.Owner.UserID, 0, 7, 0)
	var pending AgentCreditRepayment
	require.NoError(t, DB.Where("state = ?", "recorded").First(&pending).Error)
	creditExecute(t, f.Finance, "credit.repayment.reallocate", AgentCreditCommand{RequestID: "repay-reallocate", ResourceID: pending.ID, Version: pending.Version, Allocations: []AgentReceivableAllocationInput{{ReceivableID: ars[1].ID, AmountMinor: "7"}}, EvidenceReference: "bank-original-receipt-unchanged"})
	require.NoError(t, DB.First(&pending, pending.ID).Error)
	creditExecute(t, f.Reviewer, "credit.repayment.confirm", AgentCreditCommand{RequestID: "repay-reallocated-confirm", ResourceID: pending.ID, Version: pending.Version, EvidenceReference: "independent-reallocation-check"})
	assertCreditAccount(t, f.Owner.UserID, 0, 0, 0)
}
func TestAgentCreditRejectsNegativeLegacyAccountAndCrossOwnerReadPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "anomaly")
	require.NoError(t, DB.Model(&AgentCreditAccount{}).Where("agent_user_id = ?", f.Owner.UserID).Update("reserved_minor", -1).Error)
	c := AgentCreditCommand{RequestID: "anomaly-draw", Quota: 10, ExpectedLimitID: limit.ID}
	b, _ := common.Marshal(c)
	_, err := PrepareAgentCreditOperation(f.Owner, "credit.draw", c.RequestID, b)
	require.ErrorIs(t, err, ErrAgentCreditInvariant)
	_, err = GetAgentCreditView(f.Other, f.Owner.UserID, 0, 10)
	require.Error(t, err)
	var stored AgentCreditAccount
	require.NoError(t, DB.Where("agent_user_id = ?", f.Owner.UserID).First(&stored).Error)
	assert.EqualValues(t, -1, stored.ReservedMinor)
}

func TestAgentCreditPolicyVersionInvalidatesPreparedDrawAndExactReplayRevocationPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "policy-fence")
	c := AgentCreditCommand{RequestID: "credit-policy-fence-draw", Quota: 10, ExpectedLimitID: limit.ID}
	auth := creditProof(t, f.Owner, "credit.draw", c)
	p := f.Policy
	p.ID = 0
	p.RequestID = "credit-policy-fence-new-version"
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	_, err := ExecuteAgentCreditOperation(auth, "credit.draw", c)
	require.ErrorIs(t, err, ErrAgentTransactionProof)
	assertCreditAccount(t, f.Owner.UserID, 0, 0, 0)
	auth = creditProof(t, f.Owner, "credit.draw", c)
	_, err = ExecuteAgentCreditOperation(auth, "credit.draw", c)
	require.NoError(t, err)
	assertCreditAccount(t, f.Owner.UserID, 7, 0, 0)
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", f.Owner.SessionID).Update("version", 2).Error)
	_, err = ExecuteAgentCreditOperation(auth, "credit.draw", c)
	require.Error(t, err)
	assertCreditAccount(t, f.Owner.UserID, 7, 0, 0)
}
func TestAgentCreditConcurrentSharedReceiptPurposeCannotFundDepositAndRepayPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "purpose-race")
	draw := creditDraw(t, f, limit, 10, "purpose-race")
	creditSelfUse(t, f, draw, 10, "purpose-race")
	ar := creditARs(t, f.Owner.UserID)[0]
	credit := AgentCreditCommand{RequestID: "purpose-race-repayment", AgentUserID: f.Owner.UserID, AmountMinor: "7", Currency: "USD", CurrencyExponent: 2, Provider: "bank_transfer", MerchantScope: "bank", Environment: "live", ExternalReference: "same-bank-payment-purpose-race", PaidAt: common.GetTimestamp(), EvidenceReference: "verified-bank-receipt", Allocations: []AgentReceivableAllocationInput{{ReceivableID: ar.ID, AmountMinor: "7"}}}
	deposit := AgentDepositCommand{RequestID: "purpose-race-deposit", AgentUserID: f.Owner.UserID, AmountMinor: "7", Currency: "USD", CurrencyExponent: 2, Provider: "bank_transfer", MerchantScope: "bank-alias", Environment: "live", ExternalReference: credit.ExternalReference, PaidAt: credit.PaidAt, EvidenceReference: "verified-bank-receipt", PayerReference: "controlled-payer"}
	ca := creditProof(t, f.Finance, "credit.repayment.record", credit)
	da := depositProof(t, f.Finance, "deposit.receipt.record", deposit)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		_, err := ExecuteAgentCreditOperation(ca, "credit.repayment.record", credit)
		errs <- err
	})
	wg.Go(func() {
		<-start
		_, err := ExecuteAgentDepositOperation(da, "deposit.receipt.record", deposit)
		errs <- err
	})
	close(start)
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrAgentPaymentReceiptConflict)
		}
	}
	assert.Equal(t, 1, success)
	var receipts int64
	require.NoError(t, DB.Model(&AgentPaymentReceipt{}).Count(&receipts).Error)
	assert.EqualValues(t, 1, receipts)
	assertCreditAccount(t, f.Owner.UserID, 0, 7, 0)
}

func TestAgentCreditReadOnlyLiabilityReconciliationDetectsLedgerAndSourceAnomaliesPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "reconcile")
	draw := creditDraw(t, f, limit, 10, "reconcile")
	creditSelfUse(t, f, draw, 5, "reconcile")
	depositReceipt(t, f, 20, "reconcile")
	findings, err := ReconcileAgentCreditLiabilities(f.Owner, 0)
	require.NoError(t, err)
	require.Empty(t, findings)
	// Simulate pre-existing corruption without using a production mutation API.
	require.NoError(t, DB.Exec("UPDATE agent_credit_ledgers SET outstanding_after = outstanding_after + 1 WHERE kind = ?", "recognition").Error)
	findings, err = ReconcileAgentCreditLiabilities(f.Finance, f.Owner.UserID)
	require.NoError(t, err)
	require.NotEmpty(t, findings)
	assert.Equal(t, "credit_journal_mismatch", findings[0].Code)
	var a AgentCreditAccount
	require.NoError(t, DB.Where("agent_user_id = ?", f.Owner.UserID).First(&a).Error)
	assert.EqualValues(t, 4, a.OutstandingMinor)
	assert.EqualValues(t, 3, a.ReservedMinor)
}
func TestAgentCreditGovernanceGateBlocksOnlyNewCreditNotBoundCustomerPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	require.NoError(t, DB.AutoMigrate(AgentGovernanceModels()...))
	limit := creditLimit(t, f, 100, "governance")
	creditDraw(t, f, limit, 10, "governance")
	c := AgentDeliveryCommand{RequestID: "governance-bound-create", Name: "Protected buyer", Quota: 10}
	delivery, err := CreateAgentDelivery(prepaidProof(t, f.Owner, "delivery.create", c.RequestID, c), c)
	require.NoError(t, err)
	delivery, err = ClaimAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "governance-bound-claim", ClaimCode: delivery.ClaimCode})
	require.NoError(t, err)
	p := f.Policy
	p.ID = 0
	p.RequestID = "governance-required-new-credit"
	p.GovernanceEnabled = true
	p.CreditGovernanceRequired = true
	require.NoError(t, ValidateAgentPolicy(&p))
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	draw := AgentCreditCommand{RequestID: "governance-blocked-draw", Quota: 1, ExpectedLimitID: limit.ID}
	payload, _ := common.Marshal(draw)
	_, err = PrepareAgentCreditOperation(f.Owner, "credit.draw", draw.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentGovernanceRequired)
	_, err = ConfirmAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "governance-bound-confirm", DeliveryID: delivery.ID, Version: delivery.Version})
	require.NoError(t, err)
	assertCreditAccount(t, f.Owner.UserID, 0, 7, 0)
	ar := creditARs(t, f.Owner.UserID)[0]
	repay := creditRecordRepayment(t, f, []AgentReceivableAllocationInput{{ReceivableID: ar.ID, AmountMinor: "7"}}, 7, "governance")
	creditExecute(t, f.Reviewer, "credit.repayment.confirm", AgentCreditCommand{RequestID: "governance-repay-confirm", ResourceID: repay.ID, Version: repay.Version, EvidenceReference: "manual-verified-repayment"})
	assertCreditAccount(t, f.Owner.UserID, 0, 0, 0)
}
