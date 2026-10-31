package model

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const prepaidSecondFinanceID = 78005

type prepaidFixture struct {
	Owner, Other, Finance, Reviewer, Customer AuthSessionIdentity
	Policy                                    AgentPolicy
}

func setupPrepaidTest(t *testing.T) prepaidFixture {
	t.Helper()
	setupAgentRedemptionTest(t)
	require.True(t, common.UsingMainDatabase(common.DatabaseTypePostgreSQL), "prepaid financial acceptance requires the disposable PostgreSQL fixture; do not silently skip")
	require.NoError(t, DB.Model(&User{}).Where("id IN ?", []int{agentTestOwnerID, agentTestOtherID}).Update("role", common.RoleCommonUser).Error)
	require.NoError(t, DB.Create(&User{Id: prepaidSecondFinanceID, Username: "prepaid-reviewer", AffCode: "prepaid-reviewer", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}).Error)
	identities := map[int]AuthSessionIdentity{}
	for _, id := range []int{agentTestOwnerID, agentTestOtherID, agentTestAdminID, prepaidSecondFinanceID, agentTestRecipientID} {
		var user User
		require.NoError(t, DB.First(&user, id).Error)
		sid := fmt.Sprintf("prepaid-session-%d", id)
		require.NoError(t, DB.Create(&UserSession{SID: sid, UserID: id, Version: 1, UserAuthVersion: user.AuthVersion, Status: UserSessionStatusActive, RefreshHash: "fixture-digest", LoginMethod: "password", ExpiresAt: time.Now().Add(time.Hour).Unix()}).Error)
		require.NoError(t, DB.Create(&TwoFA{UserId: id, Secret: "fixture-only-noncredential", IsEnabled: true}).Error)
		identities[id] = AuthSessionIdentity{UserID: id, SessionID: sid, UserAuthVersion: user.AuthVersion, SessionVersion: 1}
	}
	policy := AgentPolicy{RequestID: "prepaid-fixture-policy", CreatedBy: agentTestAdminID, Reason: "fixture approved terms", RiskPolicyApproved: true, PurchaseEnabled: true, DeliveryEnabled: true, PurchaseRefundEnabled: true, PurchaseRefundTermsApproved: true, LegacyPurchaseReclassificationEnabled: true, ProcurementSelfUseEnabled: true, PurchaseCurrency: "USD", PurchaseCurrencyExponent: 2, PurchaseRateBPS: 9000, PurchaseQuotaNumerator: 10, PurchaseQuotaDenominator: 1, PurchaseMinimumQuota: 1, PurchaseMaximumQuota: common.MaxWalletQuota, QuoteLifetimeSeconds: 600, RefundFeeBPS: 300, CommissionCurrency: "USD", CommissionCurrencyExponent: 2}
	require.NoError(t, ValidateAgentPolicy(&policy))
	require.NoError(t, DB.Create(&policy).Error)
	require.NoError(t, DB.Create(&AgentPolicyState{ID: 1, CurrentPolicyID: policy.ID}).Error)
	return prepaidFixture{Owner: identities[agentTestOwnerID], Other: identities[agentTestOtherID], Finance: identities[agentTestAdminID], Reviewer: identities[prepaidSecondFinanceID], Customer: identities[agentTestRecipientID], Policy: policy}
}
func prepaidProof(t *testing.T, identity AuthSessionIdentity, action, requestID string, command any) AgentTransactionAuthorization {
	t.Helper()
	payload, err := common.Marshal(command)
	require.NoError(t, err)
	prepared, err := PrepareAgentPurchaseOperation(identity, action, requestID, payload)
	require.NoError(t, err)
	require.NotNil(t, prepared)
	hash, err := AgentTransactionContextHash(prepared.VerificationContext)
	require.NoError(t, err)
	raw, _, err := IssueAgentTransactionProof(identity, hash, "2fa")
	require.NoError(t, err)
	auth, err := PrepareAgentTransactionAuthorization(raw, identity, prepared.VerificationContext)
	require.NoError(t, err)
	return auth
}
func prepaidOrder(t *testing.T, f prepaidFixture, quota int, suffix string) *AgentPurchaseOrder {
	t.Helper()
	quote, err := CreateAgentPurchaseQuote(f.Owner, AgentPurchaseQuoteRequest{RequestID: "quote-" + suffix, Quota: quota})
	require.NoError(t, err)
	order, err := CreateAgentPurchaseOrder(f.Owner, AgentPurchaseCreateRequest{RequestID: "order-" + suffix, QuoteID: quote.ID})
	require.NoError(t, err)
	order, err = SubmitAgentPurchaseEvidence(f.Owner, AgentPurchaseCommand{RequestID: "evidence-" + suffix, OrderID: order.ID, Version: order.Version, EvidenceReference: "buyer-document-" + suffix})
	require.NoError(t, err)
	return order
}
func prepaidCredit(t *testing.T, f prepaidFixture, quota int, suffix string) *AgentPurchaseOrder {
	t.Helper()
	order := prepaidOrder(t, f, quota, suffix)
	payerReference := "dossier:synthetic-payer"
	command := AgentPurchaseCommand{PayerReference: payerReference, RequestID: "verify-" + suffix, OrderID: order.ID, Version: order.Version, EvidenceReference: "bank-statement-" + suffix, Provider: "bank_transfer", MerchantScope: "fixture-merchant", Environment: "live", ExternalReference: "statement-" + suffix, PaidMinor: order.PaidMinor, Currency: order.Currency, PaidAt: common.GetTimestamp()}
	auth := prepaidProof(t, f.Finance, "purchase.payment.record", command.RequestID, command)
	var err error
	order, err = RecordAgentPurchasePayment(auth, command)
	require.NoError(t, err)
	command = AgentPurchaseCommand{PayerReference: payerReference, RequestID: "review-" + suffix, OrderID: order.ID, Version: order.Version}
	auth = prepaidProof(t, f.Reviewer, "purchase.payment.confirm", command.RequestID, command)
	order, err = ConfirmAgentPurchasePayment(auth, command)
	require.NoError(t, err)
	return order
}
func assertPrepaidReconciled(t *testing.T) {
	t.Helper()
	rows, err := ReconcileAgentFunds(RedemptionScope{UserID: agentTestAdminID, Role: common.RoleAdminUser}, 0)
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestAgentPurchaseQuoteExactRoundingAndBounds(t *testing.T) {
	f := setupPrepaidTest(t)
	for _, tc := range []struct {
		quota int
		want  string
	}{{1, "1"}, {10, "1"}, {11, "1"}, {12, "2"}, {1000, "90"}} {
		paid, err := agentPurchaseQuotePrice(&f.Policy, tc.quota)
		require.NoError(t, err)
		assert.Equal(t, tc.want, paid)
	}
	_, err := agentPurchaseQuotePrice(&f.Policy, 0)
	require.Error(t, err)
	f.Policy.PurchaseQuotaNumerator = 1
	f.Policy.PurchaseQuotaDenominator = int64(common.MaxWalletQuota)
	_, err = agentPurchaseQuotePrice(&f.Policy, common.MaxWalletQuota)
	require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	f.Policy.PurchaseEnabled = false
	_, err = agentPurchaseQuotePrice(&f.Policy, 100)
	require.ErrorIs(t, err, ErrAgentFeatureDisabled)
}
func TestAgentPurchaseTwoIndependentFinanceAndPaymentPurpose(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidOrder(t, f, 1000, "two-finance")
	var accounts int64
	require.NoError(t, DB.Model(&AgentRedemptionAccount{}).Count(&accounts).Error)
	assert.Zero(t, accounts)
	command := AgentPurchaseCommand{RequestID: "verify-two-finance", OrderID: order.ID, Version: order.Version, EvidenceReference: "bank-statement", Provider: "bank_transfer", MerchantScope: "fixture", Environment: "live", ExternalReference: "two-finance-external", PaidMinor: order.PaidMinor, Currency: order.Currency, PaidAt: common.GetTimestamp()}
	_, err := PrepareAgentPurchaseContext(f.Owner, "purchase.payment.record", command)
	require.ErrorIs(t, err, ErrAgentPurchaseReview)
	auth := prepaidProof(t, f.Finance, "purchase.payment.record", command.RequestID, command)
	changed := command
	changed.PaidMinor = "91"
	_, err = RecordAgentPurchasePayment(auth, changed)
	require.Error(t, err)
	order, err = RecordAgentPurchasePayment(auth, command)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&AgentRedemptionAccount{}).Count(&accounts).Error)
	assert.Zero(t, accounts)
	review := AgentPurchaseCommand{RequestID: "review-two-finance", OrderID: order.ID, Version: order.Version}
	same := prepaidProof(t, f.Finance, "purchase.payment.confirm", review.RequestID, review)
	_, err = ConfirmAgentPurchasePayment(same, review)
	require.ErrorIs(t, err, ErrAgentPurchaseReview)
	reviewAuth := prepaidProof(t, f.Reviewer, "purchase.payment.confirm", review.RequestID, review)
	order, err = ConfirmAgentPurchasePayment(reviewAuth, review)
	require.NoError(t, err)
	assert.Equal(t, "credited", order.Status)
	replay, err := ConfirmAgentPurchasePayment(reviewAuth, review)
	require.NoError(t, err)
	assert.Equal(t, order.ID, replay.ID)
	assertAgentBudget(t, f.Owner.UserID, 1000, 0, 0, 1000)
	assertPrepaidReconciled(t)
	receipt := AgentPaymentReceipt{Provider: "bank_transfer", ProviderReference: command.ExternalReference, MerchantScope: "fixture", Environment: "live", Purpose: "topup", LocalOrderKey: "topup-other", OwnerUserID: f.Owner.UserID, Currency: "USD", CurrencyExponent: 2, PaidMinor: 90, VerifiedAt: common.GetTimestamp(), PaidAt: common.GetTimestamp()}
	err = DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimAgentPaymentReceiptTx(tx, &receipt); return err })
	require.ErrorIs(t, err, ErrAgentPaymentReceiptConflict)
	receipt.MerchantScope = "alias"
	receipt.Environment = "test"
	err = DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimAgentPaymentReceiptTx(tx, &receipt); return err })
	require.ErrorIs(t, err, ErrAgentPaymentReceiptConflict)
	assert.Zero(t, getUserBalances(t, f.Owner.UserID).Quota)
}

func TestAgentFundIntervalCostsConserveAfterReleaseAndSplit(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 11, "intervals")
	var batch AgentFundBatch
	require.NoError(t, DB.First(&batch, order.FundBatchID).Error)
	assert.Equal(t, "1", batch.OriginalPaidMinor)
	for cut := 1; cut < 11; cut++ {
		left, err := AgentFundSliceCost(&batch, 0, cut)
		require.NoError(t, err)
		right, err := AgentFundSliceCost(&batch, cut, 11-cut)
		require.NoError(t, err)
		assert.EqualValues(t, 1, left+right)
	}
	scope, err := ResolveRedemptionScope(f.Owner.UserID)
	require.NoError(t, err)
	keys, err := CreateRedemptionsScoped(scope, RedemptionCreateRequest{Name: "cost source", Quota: 6, Count: 1, RequestID: "interval-code"})
	require.NoError(t, err)
	var code Redemption
	require.NoError(t, DB.Where(commonKeyCol+" = ?", keys[0]).First(&code).Error)
	assert.True(t, code.SourceProtected)
	_, err = DeleteRedemptionsScoped(scope, []int{code.Id}, false)
	require.ErrorIs(t, err, ErrAgentDeliveryProtected)
	_, err = UpdateRedemptionScoped(scope, RedemptionUpdateRequest{ID: code.Id, Name: "edit", Quota: 1}, false)
	require.ErrorIs(t, err, ErrAgentDeliveryProtected)
	_, err = Redeem(keys[0], f.Customer.UserID)
	require.NoError(t, err)
	assertAgentBudget(t, f.Owner.UserID, 5, 0, 6, 11)
	assertPrepaidReconciled(t)
}

func TestAgentDeliveryClaimBindingConfirmationAndFreezeProtection(t *testing.T) {
	f := setupPrepaidTest(t)
	prepaidCredit(t, f, 1000, "delivery")
	command := AgentDeliveryCommand{RequestID: "delivery-create-one", Name: "customer quota", Quota: 400, ExpiresAt: common.GetTimestamp() + 60}
	auth := prepaidProof(t, f.Owner, "delivery.create", command.RequestID, command)
	delivery, err := CreateAgentDelivery(auth, command)
	require.NoError(t, err)
	require.NotEmpty(t, delivery.ClaimCode)
	_, err = Redeem(delivery.ClaimCode, f.Customer.UserID)
	require.ErrorIs(t, err, ErrRedeemFailed)
	claim := AgentDeliveryCommand{RequestID: "delivery-claim-one", ClaimCode: delivery.ClaimCode}
	delivery, err = ClaimAgentDelivery(f.Customer, claim)
	require.NoError(t, err)
	assert.Equal(t, "claimed", delivery.State)
	assert.Zero(t, getUserBalances(t, f.Customer.UserID).Quota)
	assertAgentBudget(t, f.Owner.UserID, 600, 400, 0, 1000)
	scope, err := ResolveRedemptionScope(f.Owner.UserID)
	require.NoError(t, err)
	_, err = UpdateRedemptionScoped(scope, RedemptionUpdateRequest{ID: delivery.RedemptionID, Status: common.RedemptionCodeStatusDisabled}, true)
	require.ErrorIs(t, err, ErrAgentDeliveryProtected)
	_, err = DeleteRedemptionsScoped(scope, []int{delivery.RedemptionID}, false)
	require.ErrorIs(t, err, ErrAgentDeliveryProtected)
	require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", delivery.RedemptionID).Update("expired_time", common.GetTimestamp()-1).Error)
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Updates(map[string]any{"sales_frozen": true, "status": AgentProfileExited, "version": 2}).Error)
	released, err := ReleaseExpiredAgentRedemptions(100)
	require.NoError(t, err)
	assert.Zero(t, released)
	confirm := AgentDeliveryCommand{RequestID: "delivery-confirm-one", DeliveryID: delivery.ID, Version: delivery.Version}
	_, err = ConfirmAgentDelivery(f.Other, confirm)
	require.Error(t, err)
	delivery, err = ConfirmAgentDelivery(f.Customer, confirm)
	require.NoError(t, err)
	assert.Equal(t, "redeemed", delivery.State)
	_, err = ConfirmAgentDelivery(f.Customer, confirm)
	require.NoError(t, err)
	assert.Equal(t, 400, getUserBalances(t, f.Customer.UserID).QuotaPaid)
	assertAgentBudget(t, f.Owner.UserID, 600, 0, 400, 1000)
	assertPrepaidReconciled(t)
}

func TestAgentDeliveryClaimCancelRaceOnlyOneWinner(t *testing.T) {
	f := setupPrepaidTest(t)
	prepaidCredit(t, f, 1000, "claim-race")
	create := AgentDeliveryCommand{RequestID: "delivery-race-create", Name: "race", Quota: 100}
	delivery, err := CreateAgentDelivery(prepaidProof(t, f.Owner, "delivery.create", create.RequestID, create), create)
	require.NoError(t, err)
	cancel := AgentDeliveryCommand{RequestID: "delivery-race-cancel", DeliveryID: delivery.ID, Version: delivery.Version, CustomerObligationsResolved: true, EvidenceReference: "verified-no-outstanding-customer"}
	auth := prepaidProof(t, f.Owner, "delivery.cancel", cancel.RequestID, cancel)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	wg.Go(func() {
		<-start
		_, err := ClaimAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "delivery-race-claim", ClaimCode: delivery.ClaimCode})
		errs <- err
	})
	wg.Go(func() { <-start; _, err := CancelAgentDelivery(auth, cancel); errs <- err })
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	assertPrepaidReconciled(t)
}

func prepaidRefund(t *testing.T, f prepaidFixture, batchID, quota int, suffix string) *AgentPurchaseRefund {
	t.Helper()
	command := AgentPurchaseRefundCommand{RequestID: "refund-request-" + suffix, BatchID: batchID, Quota: quota}
	refund, err := RequestAgentPurchaseRefund(prepaidProof(t, f.Owner, "purchase.refund.request", command.RequestID, command), command)
	require.NoError(t, err)
	return refund
}
func prepaidRefundAdvance(t *testing.T, f prepaidFixture, refund *AgentPurchaseRefund, action, suffix string, command AgentPurchaseRefundCommand) *AgentPurchaseRefund {
	t.Helper()
	command.RequestID = "refund-" + suffix
	command.RefundID = refund.ID
	command.Version = refund.Version
	actor := f.Finance
	if action == "purchase.refund.resolve" && command.Outcome != "unknown" {
		actor = f.Reviewer
	}
	result, err := TransitionAgentPurchaseRefund(prepaidProof(t, actor, action, command.RequestID, command), action, command)
	require.NoError(t, err)
	return result
}
func TestAgentPurchaseRefundUnknownFreezesAndOriginalCostSuccess(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "refund-unknown")
	refund := prepaidRefund(t, f, order.FundBatchID, 500, "unknown")
	assert.Equal(t, "45", refund.PrincipalMinor)
	assert.Equal(t, "1", refund.FeeMinor)
	assert.Equal(t, "44", refund.NetMinor)
	assertAgentBudget(t, f.Owner.UserID, 500, 500, 0, 1000)
	refund = prepaidRefundAdvance(t, f, refund, "purchase.refund.approve", "approve-unknown", AgentPurchaseRefundCommand{Decision: "approve", EvidenceReference: "independent-obligations-review"})
	refund = prepaidRefundAdvance(t, f, refund, "purchase.refund.submit", "submit-unknown", AgentPurchaseRefundCommand{BusinessKey: refund.BusinessKey})
	refund = prepaidRefundAdvance(t, f, refund, "purchase.refund.resolve", "resolve-unknown", AgentPurchaseRefundCommand{BusinessKey: refund.BusinessKey, Outcome: "unknown", EvidenceReference: "bank-query-pending"})
	assert.Equal(t, "payment_unknown", refund.State)
	assertAgentBudget(t, f.Owner.UserID, 500, 500, 0, 1000)
	resubmit := AgentPurchaseRefundCommand{RequestID: "refund-illegal-resubmit", RefundID: refund.ID, Version: refund.Version, BusinessKey: refund.BusinessKey}
	_, err := TransitionAgentPurchaseRefund(prepaidProof(t, f.Finance, "purchase.refund.submit", resubmit.RequestID, resubmit), "purchase.refund.submit", resubmit)
	require.ErrorIs(t, err, ErrAgentRefundUnknown)
	bad := AgentPurchaseRefundCommand{RequestID: "refund-failure-no-confirm", RefundID: refund.ID, Version: refund.Version, BusinessKey: refund.BusinessKey, Outcome: "explicit_failed", EvidenceReference: "no-final-evidence"}
	_, err = TransitionAgentPurchaseRefund(prepaidProof(t, f.Reviewer, "purchase.refund.resolve", bad.RequestID, bad), "purchase.refund.resolve", bad)
	require.ErrorIs(t, err, ErrAgentRefundUnknown)
	refund = prepaidRefundAdvance(t, f, refund, "purchase.refund.resolve", "resolve-success", AgentPurchaseRefundCommand{BusinessKey: refund.BusinessKey, Outcome: "success", PayoutPaidAt: common.GetTimestamp(), EvidenceReference: "verified-bank-receipt", ExternalReference: "refund-bank-transaction"})
	assert.Equal(t, "succeeded", refund.State)
	assertAgentBudget(t, f.Owner.UserID, 500, 0, 0, 500)
	assertPrepaidReconciled(t)
}
func TestAgentPurchaseRefundExplicitFailureRestoresExactIntervals(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "refund-failed")
	refund := prepaidRefund(t, f, order.FundBatchID, 333, "failed")
	var before []AgentFundSlice
	require.NoError(t, DB.Where("refund_id = ?", refund.ID).Find(&before).Error)
	refund = prepaidRefundAdvance(t, f, refund, "purchase.refund.approve", "approve-failed", AgentPurchaseRefundCommand{Decision: "approve", EvidenceReference: "reviewed"})
	refund = prepaidRefundAdvance(t, f, refund, "purchase.refund.submit", "submit-failed", AgentPurchaseRefundCommand{BusinessKey: refund.BusinessKey})
	refund = prepaidRefundAdvance(t, f, refund, "purchase.refund.resolve", "resolve-failed", AgentPurchaseRefundCommand{BusinessKey: refund.BusinessKey, Outcome: "explicit_failed", EvidenceReference: "bank-confirmed-no-payment", NoPaymentConfirmed: true})
	for _, original := range before {
		var restored AgentFundSlice
		require.NoError(t, DB.First(&restored, original.ID).Error)
		assert.Equal(t, original.StartQuota, restored.StartQuota)
		assert.Equal(t, original.Quota, restored.Quota)
		assert.Equal(t, AgentSliceAvailable, restored.State)
		assert.Zero(t, restored.RefundID)
	}
	assertAgentBudget(t, f.Owner.UserID, 1000, 0, 0, 1000)
	assertPrepaidReconciled(t)
}
func TestAgentProcurementSelfUsePreservesPaidVersusCommissionReward(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "self-use")
	request := AgentProcurementSelfUseRequest{RequestID: "purchase-self-use", BatchID: order.FundBatchID, Quota: 200}
	_, err := AgentProcurementSelfUse(prepaidProof(t, f.Owner, "procurement.self_use", request.RequestID, request), request)
	require.NoError(t, err)
	err = DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		return CreditAgentCommissionBatchTx(tx, f.Owner.UserID, "commission-source-transfer", "USD", 20, 200, f.Policy.ID)
	})
	require.NoError(t, err)
	var batch AgentFundBatch
	require.NoError(t, DB.Where("source_kind = ?", AgentFundCommission).First(&batch).Error)
	assert.False(t, batch.CashRefundable)
	refund := AgentPurchaseRefundCommand{RequestID: "commission-illegal-refund", BatchID: batch.ID, Quota: 100}
	_, err = PrepareAgentPurchaseRefundContext(f.Owner, "purchase.refund.request", refund)
	require.ErrorIs(t, err, ErrAgentRefundInvalid)
	request = AgentProcurementSelfUseRequest{RequestID: "commission-self-use", BatchID: batch.ID, Quota: 100}
	_, err = AgentProcurementSelfUse(prepaidProof(t, f.Owner, "procurement.self_use", request.RequestID, request), request)
	require.NoError(t, err)
	wallet := getUserBalances(t, f.Owner.UserID)
	assert.Equal(t, 200, wallet.QuotaPaid)
	assert.Equal(t, 100, wallet.QuotaReward)
	assert.Zero(t, wallet.QuotaBonus)
	assertPrepaidReconciled(t)
}

func TestAgentPrepaidLegacyMigrationDoesNotCreditOrCashify(t *testing.T) {
	f := setupPrepaidTest(t)
	require.NoError(t, DB.Create(&AgentRedemptionAccount{AgentUserID: f.Owner.UserID, AvailableQuota: 60, ReservedQuota: 30, SpentQuota: 10, AllocatedQuota: 100}).Error)
	require.NoError(t, DB.Create(&Redemption{UserId: f.Owner.UserID, Key: common.GetUUID(), Name: "historical bearer", Quota: 30, Status: common.RedemptionCodeStatusEnabled, BudgetAgentID: f.Owner.UserID, BudgetReserved: true, BudgetVersion: 1}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.Owner.UserID).Updates(map[string]any{"quota": 60, "quota_paid": 10, "quota_bonus": 20, "quota_reward": 30, "aff_quota": -7}).Error)
	before := getUserBalances(t, f.Owner.UserID)
	for range 2 {
		require.NoError(t, MigrateLegacyAgentFundsWithDB(DB))
	}
	after := getUserBalances(t, f.Owner.UserID)
	assert.Equal(t, before.Quota, after.Quota)
	assert.Equal(t, before.QuotaPaid, after.QuotaPaid)
	assert.Equal(t, before.QuotaBonus, after.QuotaBonus)
	assert.Equal(t, before.QuotaReward, after.QuotaReward)
	assert.Equal(t, before.AffQuota, after.AffQuota)
	var batches []AgentFundBatch
	require.NoError(t, DB.Where("agent_user_id = ?", f.Owner.UserID).Find(&batches).Error)
	require.Len(t, batches, 1)
	assert.Equal(t, AgentFundLegacy, batches[0].SourceKind)
	assert.False(t, batches[0].CashRefundable)
	assert.Equal(t, "0", batches[0].OriginalPaidMinor)
	assertPrepaidReconciled(t)
	_, err := PrepareAgentPurchaseRefundContext(f.Owner, "purchase.refund.request", AgentPurchaseRefundCommand{RequestID: "illegal-legacy-refund", BatchID: batches[0].ID, Quota: 30})
	require.ErrorIs(t, err, ErrAgentRefundInvalid)
	require.NoError(t, DB.Create(&AgentRedemptionAccount{AgentUserID: 999991, AvailableQuota: -1, AllocatedQuota: -1}).Error)
	for range 2 {
		require.NoError(t, MigrateLegacyAgentFundsWithDB(DB))
	}
	var anomalies int64
	require.NoError(t, DB.Model(&AgentFundAnomaly{}).Where("agent_user_id = ?", 999991).Count(&anomalies).Error)
	assert.EqualValues(t, 1, anomalies)
	var corrupt AgentRedemptionAccount
	require.NoError(t, DB.First(&corrupt, 999991).Error)
	assert.Equal(t, -1, corrupt.AvailableQuota)
}

func TestAgentPrepaidConcurrentRefundSelfUseAndIssuanceDoNotOverspend(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "concurrent")
	refund := AgentPurchaseRefundCommand{RequestID: "concurrent-refund", BatchID: order.FundBatchID, Quota: 700}
	refundAuth := prepaidProof(t, f.Owner, "purchase.refund.request", refund.RequestID, refund)
	self := AgentProcurementSelfUseRequest{RequestID: "concurrent-self-use", BatchID: order.FundBatchID, Quota: 700}
	selfAuth := prepaidProof(t, f.Owner, "procurement.self_use", self.RequestID, self)
	delivery := AgentDeliveryCommand{RequestID: "concurrent-delivery", Name: "concurrent", Quota: 700}
	deliveryAuth := prepaidProof(t, f.Owner, "delivery.create", delivery.RequestID, delivery)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	start := make(chan struct{})
	wg.Go(func() { <-start; _, err := RequestAgentPurchaseRefund(refundAuth, refund); errs <- err })
	wg.Go(func() { <-start; _, err := AgentProcurementSelfUse(selfAuth, self); errs <- err })
	wg.Go(func() { <-start; _, err := CreateAgentDelivery(deliveryAuth, delivery); errs <- err })
	close(start)
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	require.Equal(t, 1, success)
	var account AgentRedemptionAccount
	require.NoError(t, DB.First(&account, f.Owner.UserID).Error)
	assert.Equal(t, 300, account.AvailableQuota)
	assert.Equal(t, 1000, account.AllocatedQuota)
	assertPrepaidReconciled(t)
}

func TestAgentPrepaidRollbackRestoresSourceAndAuthorization(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "rollback")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.Owner.UserID).Updates(map[string]any{"quota": common.MaxWalletQuota, "quota_paid": common.MaxWalletQuota}).Error)
	request := AgentProcurementSelfUseRequest{RequestID: "self-use-rollback", BatchID: order.FundBatchID, Quota: 100}
	auth := prepaidProof(t, f.Owner, "procurement.self_use", request.RequestID, request)
	_, err := AgentProcurementSelfUse(auth, request)
	require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	assertAgentBudget(t, f.Owner.UserID, 1000, 0, 0, 1000)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.Owner.UserID).Updates(map[string]any{"quota": 0, "quota_paid": 0}).Error)
	result, err := AgentProcurementSelfUse(auth, request)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(9), result.CostMinor)
	assertPrepaidReconciled(t)
}

func TestAgentFundHistoricalReclassificationFreezesWithoutCreatingQuota(t *testing.T) {
	f := setupPrepaidTest(t)
	require.NoError(t, DB.Create(&AgentRedemptionAccount{AgentUserID: f.Owner.UserID, AvailableQuota: 1000, AllocatedQuota: 1000}).Error)
	require.NoError(t, MigrateLegacyAgentFunds())
	var legacy AgentFundBatch
	require.NoError(t, DB.Where("agent_user_id = ? AND source_kind = ?", f.Owner.UserID, AgentFundLegacy).First(&legacy).Error)
	command := AgentFundReclassificationCommand{RequestID: "legacy-record-proof", AgentUserID: f.Owner.UserID, LegacyBatchID: legacy.ID, Quota: 600, PaidMinor: "54", Currency: "USD", CurrencyExponent: 2, Provider: "bank_transfer", MerchantScope: "fixture-merchant", Environment: "live", ExternalReference: "historic-real-procurement-payment", PaidAt: common.GetTimestamp() - 30*86400, EvidenceReference: "archived-procurement-statement", HistoricalProcurementConfirmed: true, NotWalletTopupConfirmed: true}
	auth := prepaidProof(t, f.Finance, "purchase.legacy.record", command.RequestID, command)
	record, err := RecordAgentFundReclassification(auth, command)
	require.NoError(t, err)
	var account AgentRedemptionAccount
	require.NoError(t, DB.First(&account, f.Owner.UserID).Error)
	assert.Equal(t, 1000, account.AllocatedQuota)
	assert.Equal(t, 400, account.AvailableQuota)
	assert.Equal(t, 600, account.ReservedQuota)
	assert.Zero(t, getUserBalances(t, f.Owner.UserID).Quota)
	assertPrepaidReconciled(t)
	confirm := AgentFundReclassificationCommand{RequestID: "legacy-confirm-proof", ReclassificationID: record.ID, Version: record.Version, Decision: "approve", EvidenceReference: "second-independent-archived-review"}
	_, err = PrepareAgentFundReclassificationOperation(f.Finance, "purchase.legacy.confirm", confirm.RequestID, confirm)
	require.ErrorIs(t, err, ErrAgentPurchaseReview)
	record, err = ConfirmAgentFundReclassification(prepaidProof(t, f.Reviewer, "purchase.legacy.confirm", confirm.RequestID, confirm), confirm)
	require.NoError(t, err)
	require.NoError(t, DB.First(&account, f.Owner.UserID).Error)
	assert.Equal(t, 1000, account.AllocatedQuota)
	assert.Equal(t, 1000, account.AvailableQuota)
	assert.Zero(t, account.ReservedQuota)
	assert.Zero(t, getUserBalances(t, f.Owner.UserID).Quota)
	var purchase AgentFundBatch
	require.NoError(t, DB.First(&purchase, record.FundBatchID).Error)
	assert.Equal(t, AgentFundPurchase, purchase.SourceKind)
	assert.True(t, purchase.CashRefundable)
	assert.Equal(t, "54", purchase.OriginalPaidMinor)
	refund := prepaidRefund(t, f, purchase.ID, 300, "historical-refund")
	assert.Equal(t, "27", refund.PrincipalMinor)
	assertPrepaidReconciled(t)
	// Neither a promotion nor a commission batch can be relabeled as purchase.
	command.RequestID = "legacy-invalid-repeat"
	command.LegacyBatchID = purchase.ID
	_, err = PrepareAgentFundReclassificationOperation(f.Finance, "purchase.legacy.record", command.RequestID, command)
	require.ErrorIs(t, err, ErrAgentPurchaseInvalid)
}

func TestAgentFundReclassificationRejectRestoresAndKeepsAudit(t *testing.T) {
	f := setupPrepaidTest(t)
	require.NoError(t, DB.Create(&AgentRedemptionAccount{AgentUserID: f.Owner.UserID, AvailableQuota: 100, AllocatedQuota: 100}).Error)
	require.NoError(t, MigrateLegacyAgentFunds())
	var legacy AgentFundBatch
	require.NoError(t, DB.First(&legacy).Error)
	command := AgentFundReclassificationCommand{RequestID: "legacy-to-reject", AgentUserID: f.Owner.UserID, LegacyBatchID: legacy.ID, Quota: 80, PaidMinor: "8", Currency: "USD", CurrencyExponent: 2, Provider: "bank_transfer", MerchantScope: "fixture", Environment: "live", ExternalReference: "historical-reject-reference", PaidAt: common.GetTimestamp() - 100, EvidenceReference: "archived-doc", HistoricalProcurementConfirmed: true, NotWalletTopupConfirmed: true}
	record, err := RecordAgentFundReclassification(prepaidProof(t, f.Finance, "purchase.legacy.record", command.RequestID, command), command)
	require.NoError(t, err)
	confirm := AgentFundReclassificationCommand{RequestID: "legacy-review-reject", ReclassificationID: record.ID, Version: record.Version, Decision: "reject", EvidenceReference: "independent-policy-rejection"}
	record, err = ConfirmAgentFundReclassification(prepaidProof(t, f.Reviewer, "purchase.legacy.confirm", confirm.RequestID, confirm), confirm)
	require.NoError(t, err)
	assert.Equal(t, "rejected", record.State)
	var account AgentRedemptionAccount
	require.NoError(t, DB.First(&account, f.Owner.UserID).Error)
	assert.Equal(t, 100, account.AvailableQuota)
	assert.Equal(t, 100, account.AllocatedQuota)
	assert.Zero(t, account.ReservedQuota)
	var audits []AgentPurchaseAudit
	require.NoError(t, DB.Where("resource_type = ? AND resource_id = ?", "fund_reclassification", record.ID).Find(&audits).Error)
	require.Len(t, audits, 2)
	require.ErrorIs(t, DB.Model(&audits[0]).Update("evidence_reference", "tampered").Error, ErrAgentLedgerImmutable)
	assertPrepaidReconciled(t)
}

func TestAgentFundOutboundReceiptCannotSettleTwoProducts(t *testing.T) {
	f := setupPrepaidTest(t)
	digest, err := AgentTransactionParametersHash("verified-bank-payout")
	require.NoError(t, err)
	receipt := AgentOutboundPaymentReceipt{Provider: "bank_transfer", ProviderReference: "outgoing-one-reference", MerchantScope: "fixture", Environment: "live", Purpose: "purchase_refund", BusinessKey: "refund-one", OwnerUserID: f.Owner.UserID, Currency: "USD", CurrencyExponent: 2, PaidMinor: 100, VerifiedBy: f.Finance.UserID, EvidenceHash: digest}
	for range 2 {
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimAgentOutboundPaymentReceiptTx(tx, &receipt); return err }))
	}
	receipt.Purpose = "withdrawal"
	receipt.BusinessKey = "withdrawal-two"
	err = DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimAgentOutboundPaymentReceiptTx(tx, &receipt); return err })
	require.ErrorIs(t, err, ErrAgentOutboundPaymentConflict)
	receipt.MerchantScope = "renamed-merchant"
	receipt.Environment = "test"
	err = DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimAgentOutboundPaymentReceiptTx(tx, &receipt); return err })
	require.ErrorIs(t, err, ErrAgentOutboundPaymentConflict)
}

func TestAgentFundRefundFeeConservationAcrossEverySplit(t *testing.T) {
	batch := AgentFundBatch{OriginalQuota: 1000, OriginalPaidMinor: "90"}
	for cut := 1; cut < 1000; cut++ {
		first, err := AgentFundSliceRefundFee(&batch, 0, cut, 300)
		require.NoError(t, err)
		second, err := AgentFundSliceRefundFee(&batch, cut, 1000-cut, 300)
		require.NoError(t, err)
		assert.EqualValues(t, 2, first+second)
	}
}

func TestAgentFundReconciliationPaginationAndOrphans(t *testing.T) {
	f := setupPrepaidTest(t)
	prepaidCredit(t, f, 100, "pagination")
	require.NoError(t, DB.Create(&AgentRedemptionAccount{AgentUserID: 999981, AvailableQuota: -2, AllocatedQuota: -2}).Error)
	require.NoError(t, DB.Create(&AgentFundSlice{BatchID: 999982, AgentUserID: 999982, Quota: 1, State: AgentSliceReserved, RedemptionID: 999982}).Error)
	admin := RedemptionScope{UserID: f.Finance.UserID, Role: common.RoleAdminUser}
	cursor := 0
	seen := map[int]bool{}
	codes := map[string]bool{}
	for {
		page, err := ReconcileAgentFundsPage(admin, 0, cursor, 1)
		require.NoError(t, err)
		require.Len(t, page.CheckedAgentIDs, 1)
		assert.False(t, seen[page.CheckedAgentIDs[0]])
		seen[page.CheckedAgentIDs[0]] = true
		for _, anomaly := range page.Anomalies {
			codes[anomaly.Code] = true
		}
		if page.NextCursor == 0 {
			break
		}
		require.Greater(t, page.NextCursor, cursor)
		cursor = page.NextCursor
	}
	assert.Len(t, seen, 3)
	assert.True(t, codes["invalid_aggregate"])
	assert.True(t, codes["orphan_slice"])
}

func TestAgentPurchaseBackedBearerNeedsExplicitResponsibilityRelease(t *testing.T) {
	f := setupPrepaidTest(t)
	prepaidCredit(t, f, 1000, "bearer-cancel")
	scope, err := ResolveRedemptionScope(f.Owner.UserID)
	require.NoError(t, err)
	keys, err := CreateRedemptionsScoped(scope, RedemptionCreateRequest{Name: "bearer", Quota: 100, Count: 1, RequestID: "backed-bearer-create"})
	require.NoError(t, err)
	var code Redemption
	require.NoError(t, DB.Where(commonKeyCol+" = ?", keys[0]).First(&code).Error)
	require.NoError(t, DB.Model(&code).Update("expired_time", common.GetTimestamp()-1).Error)
	released, err := ReleaseExpiredAgentRedemptions(100)
	require.NoError(t, err)
	assert.Zero(t, released)
	command := AgentDeliveryCommand{RequestID: "backed-bearer-cancel", RedemptionID: code.Id, Version: int64(code.BudgetVersion), EvidenceReference: "buyer-unpaid-and-obligations-reviewed"}
	_, err = PrepareAgentPurchaseOperation(f.Owner, "delivery.cancel", command.RequestID, mustPrepaidJSON(t, command))
	require.ErrorIs(t, err, ErrAgentDeliveryResponsibility)
	command.CustomerObligationsResolved = true
	_, err = CancelAgentLegacyCode(prepaidProof(t, f.Owner, "delivery.cancel", command.RequestID, command), command)
	require.NoError(t, err)
	assertAgentBudget(t, f.Owner.UserID, 1000, 0, 0, 1000)
	assertPrepaidReconciled(t)
}
func mustPrepaidJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func TestAgentPurchaseRejectsSandboxMoneyAndFrozenRefundCanBeRejected(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidOrder(t, f, 100, "reject-test-money")
	payment := AgentPurchaseCommand{RequestID: "record-sandbox-payment", OrderID: order.ID, Version: order.Version, EvidenceReference: "synthetic-statement", Provider: "bank_transfer", MerchantScope: "fixture", Environment: "test", ExternalReference: "sandbox-fake-value", PaidMinor: order.PaidMinor, Currency: order.Currency, PaidAt: common.GetTimestamp()}
	auth := prepaidProof(t, f.Finance, "purchase.payment.record", payment.RequestID, payment)
	_, err := RecordAgentPurchasePayment(auth, payment)
	require.ErrorIs(t, err, ErrAgentPurchaseInvalid)
	var batchCount int64
	require.NoError(t, DB.Model(&AgentFundBatch{}).Count(&batchCount).Error)
	assert.Zero(t, batchCount)
	realOrder := prepaidCredit(t, f, 1000, "frozen-reject")
	refund := prepaidRefund(t, f, realOrder.FundBatchID, 400, "frozen-reject")
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Updates(map[string]any{"settlement_frozen": true, "version": 2}).Error)
	command := AgentPurchaseRefundCommand{RequestID: "frozen-refund-reject", RefundID: refund.ID, Version: refund.Version, Decision: "reject", EvidenceReference: "independent-rejection-no-payout"}
	rejected, err := TransitionAgentPurchaseRefund(prepaidProof(t, f.Finance, "purchase.refund.reject", command.RequestID, command), "purchase.refund.reject", command)
	require.NoError(t, err)
	assert.Equal(t, "rejected", rejected.State)
	assertAgentBudget(t, f.Owner.UserID, 1000, 0, 0, 1000)
	assertPrepaidReconciled(t)
}

func TestAgentPurchaseExactReplayUsesSignatureBeforeChangedDraft(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "replay-boundary")
	request := AgentProcurementSelfUseRequest{RequestID: "replay-boundary-self-use", BatchID: order.FundBatchID, Quota: 600}
	_, err := AgentProcurementSelfUse(prepaidProof(t, f.Owner, "procurement.self_use", request.RequestID, request), request)
	require.NoError(t, err)
	// Current availability is only 400, so reconstructing a fresh 600-quote would
	// fail; exact replay instead proves the completed original signature.
	result, replayed, err := ReplayAgentPurchaseOperation(f.Owner, "procurement.self_use", request.RequestID, mustPrepaidJSON(t, request))
	require.NoError(t, err)
	require.True(t, replayed)
	assert.True(t, result.(AgentProcurementSelfUseResult).Replayed)
	request.Quota = 601
	_, _, err = ReplayAgentPurchaseOperation(f.Owner, "procurement.self_use", request.RequestID, mustPrepaidJSON(t, request))
	require.ErrorIs(t, err, ErrAgentQuotaConflict)
	request.Quota = 600
	_, _, err = ReplayAgentPurchaseOperation(f.Other, "procurement.self_use", request.RequestID, mustPrepaidJSON(t, request))
	require.ErrorIs(t, err, ErrAgentQuotaConflict)
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", f.Owner.SessionID).Update("status", UserSessionStatusRevoked).Error)
	_, _, err = ReplayAgentPurchaseOperation(f.Owner, "procurement.self_use", request.RequestID, mustPrepaidJSON(t, request))
	require.Error(t, err)
	assertAgentBudget(t, f.Owner.UserID, 400, 0, 600, 1000)
}

func TestAgentPurchaseOperationRecoveryRedactsFinanceDocuments(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "history-private")
	result, err := FindAgentPurchaseOperation(f.Owner, "order-history-private")
	require.NoError(t, err)
	require.NotNil(t, result)
	recovered := result.Resource.(AgentPurchaseOrder)
	assert.Empty(t, recovered.VerificationEvidence)
	assert.Equal(t, order.ID, recovered.ID)
	quote, err := CreateAgentPurchaseQuote(f.Owner, AgentPurchaseQuoteRequest{RequestID: "quote-history-private", Quota: 1000})
	require.NoError(t, err)
	replay, err := CreateAgentPurchaseOrder(f.Owner, AgentPurchaseCreateRequest{RequestID: "order-history-private", QuoteID: quote.ID})
	require.NoError(t, err)
	assert.Empty(t, replay.VerificationEvidence)
	evidence, err := SubmitAgentPurchaseEvidence(f.Owner, AgentPurchaseCommand{RequestID: "evidence-history-private", OrderID: order.ID, Version: 1, EvidenceReference: "buyer-document-history-private"})
	require.NoError(t, err)
	assert.Empty(t, evidence.VerificationEvidence)
	refund := prepaidRefund(t, f, order.FundBatchID, 300, "history-private")
	refund = prepaidRefundAdvance(t, f, refund, "purchase.refund.approve", "history-private-approve", AgentPurchaseRefundCommand{Decision: "approve", EvidenceReference: "private-third-party-statement"})
	status, err := FindAgentPurchaseOperation(f.Owner, "refund-request-history-private")
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Empty(t, status.Resource.(AgentPurchaseRefund).EvidenceReference)
	rows, _, err := GetAgentPurchaseAudit(RedemptionScope{UserID: f.Owner.UserID, Role: common.RoleCommonUser, HasProfile: true}, f.Owner.UserID, 0, 100)
	require.NoError(t, err)
	for _, row := range rows {
		assert.Empty(t, row.EvidenceReference)
		assert.Empty(t, row.ExternalReference)
	}
}

func TestAgentOwnFinancialCodeCannotBypassSelfUseAuthorization(t *testing.T) {
	f := setupPrepaidTest(t)
	prepaidCredit(t, f, 1000, "own-code")
	scope, err := ResolveRedemptionScope(f.Owner.UserID)
	require.NoError(t, err)
	keys, err := CreateRedemptionsScoped(scope, RedemptionCreateRequest{Name: "own bearer", Quota: 100, Count: 1, RequestID: "own-bearer-request"})
	require.NoError(t, err)
	_, err = Redeem(keys[0], f.Owner.UserID)
	require.ErrorIs(t, err, ErrRedeemFailed)
	create := AgentDeliveryCommand{RequestID: "own-delivery-create", Name: "own delivery", Quota: 100}
	delivery, err := CreateAgentDelivery(prepaidProof(t, f.Owner, "delivery.create", create.RequestID, create), create)
	require.NoError(t, err)
	_, err = ClaimAgentDelivery(f.Owner, AgentDeliveryCommand{RequestID: "own-delivery-claim", ClaimCode: delivery.ClaimCode})
	require.ErrorIs(t, err, ErrAgentDeliveryConfirmationRequired)
	assert.Zero(t, getUserBalances(t, f.Owner.UserID).Quota)
	assertAgentBudget(t, f.Owner.UserID, 800, 200, 0, 1000)
	assertPrepaidReconciled(t)
}

func TestAgentAccountProjectionUsesProfileNotLegacyRole(t *testing.T) {
	f := setupPrepaidTest(t)
	admin := RedemptionScope{UserID: f.Finance.UserID, Role: common.RoleAdminUser}
	rows, _, err := GetAgentRedemptionAccounts(admin, f.Owner.UserID, 0, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, common.RoleCommonUser, rows[0].UserRole)
	assert.True(t, rows[0].ActiveAgent)
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Update("sales_frozen", true).Error)
	rows, _, err = GetAgentRedemptionAccounts(admin, f.Owner.UserID, 0, 10)
	require.NoError(t, err)
	assert.False(t, rows[0].ActiveAgent)
}

func TestAgentDeliveryRecoveryOnlyOriginalOwnerAndOnlyBeforeBinding(t *testing.T) {
	f := setupPrepaidTest(t)
	prepaidCredit(t, f, 500, "delivery-recovery")
	create := AgentDeliveryCommand{RequestID: "delivery-lost-response", Name: "recover delivery", Quota: 100}
	delivery, err := CreateAgentDelivery(prepaidProof(t, f.Owner, "delivery.create", create.RequestID, create), create)
	require.NoError(t, err)
	originalCode := delivery.ClaimCode
	status, err := FindAgentPurchaseOperation(f.Owner, create.RequestID)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, originalCode, status.Resource.(AgentDelivery).ClaimCode)
	foreign, err := FindAgentPurchaseOperation(f.Other, create.RequestID)
	require.NoError(t, err)
	assert.Nil(t, foreign)
	finance, err := FindAgentPurchaseOperation(f.Finance, create.RequestID)
	require.NoError(t, err)
	assert.Nil(t, finance, "another admin may not recover the seller's claim credential")
	rows, _, err := GetAgentDeliveries(f.Owner, false, 0, 10)
	require.NoError(t, err)
	for _, row := range rows {
		assert.Empty(t, row.ClaimCode)
	}
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Update("status", "invalid").Error)
	_, err = FindAgentPurchaseOperation(f.Owner, create.RequestID)
	require.ErrorIs(t, err, ErrAgentRedemptionPermission)
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Update("status", AgentProfileActive).Error)
	_, err = ClaimAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "delivery-recovery-claim", ClaimCode: originalCode})
	require.NoError(t, err)
	status, err = FindAgentPurchaseOperation(f.Owner, create.RequestID)
	require.NoError(t, err)
	assert.Empty(t, status.Resource.(AgentDelivery).ClaimCode)
	replay, replayed, err := ReplayAgentPurchaseOperation(f.Owner, "delivery.create", create.RequestID, mustPrepaidJSON(t, create))
	require.NoError(t, err)
	require.True(t, replayed)
	assert.Empty(t, replay.(AgentDelivery).ClaimCode)
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", f.Owner.SessionID).Update("status", UserSessionStatusRevoked).Error)
	_, err = FindAgentPurchaseOperation(f.Owner, create.RequestID)
	require.Error(t, err)
}

func TestAgentProcurementSelfUseRequiresBothSalesAndSettlement(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "self-use-freeze")
	request := AgentProcurementSelfUseRequest{RequestID: "self-use-before-freeze", BatchID: order.FundBatchID, Quota: 100}
	auth := prepaidProof(t, f.Owner, "procurement.self_use", request.RequestID, request)
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Updates(map[string]any{"settlement_frozen": true, "version": 2}).Error)
	_, err := PrepareAgentPurchaseOperation(f.Owner, "procurement.self_use", request.RequestID, mustPrepaidJSON(t, request))
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	_, err = AgentProcurementSelfUse(auth, request)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	assertAgentBudget(t, f.Owner.UserID, 1000, 0, 0, 1000)
	assert.Zero(t, getUserBalances(t, f.Owner.UserID).Quota)
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Updates(map[string]any{"settlement_frozen": false, "version": 3}).Error)
	_, err = AgentProcurementSelfUse(prepaidProof(t, f.Owner, "procurement.self_use", request.RequestID, request), request)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Updates(map[string]any{"settlement_frozen": true, "sales_frozen": true, "version": 4}).Error)
	_, replayed, err := ReplayAgentPurchaseOperation(f.Owner, "procurement.self_use", request.RequestID, mustPrepaidJSON(t, request))
	require.NoError(t, err)
	assert.True(t, replayed)
	assertAgentBudget(t, f.Owner.UserID, 900, 0, 100, 1000)
}
