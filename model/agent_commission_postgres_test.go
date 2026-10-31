package model

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type commissionFixture struct {
	root, inviter, buyer   User
	rootIdentity, identity AuthSessionIdentity
	policy                 AgentPolicy
}

func setupAgentCommissionPostgres(t *testing.T) commissionFixture {
	t.Helper()
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("opt-in PostgreSQL fixture; acceptance runner must configure TEST_MODEL_POSTGRES_DSN and require zero skips")
	}
	root, inviter, rootIdentity, identity := setupAgentProfileSecurityTest(t)
	for _, schema := range AgentPlatformModels() {
		statement := &gorm.Statement{DB: DB}
		require.NoError(t, statement.Parse(schema))
		require.NoError(t, DB.Exec("DELETE FROM "+statement.Schema.Table).Error)
	}
	require.NoError(t, DB.AutoMigrate(AgentPlatformModels()...))
	require.NoError(t, DB.AutoMigrate(AgentPlatformModels()...))
	policy := AgentPolicy{RequestID: "fixture-policy", CreatedBy: root.Id, Reason: "Synthetic acceptance fixture", RiskPolicyApproved: true, CommissionEnabled: true, CommissionCutoverAt: common.GetTimestamp() - 10, CommissionCurrency: "USD", CommissionCurrencyExponent: 2, CommissionUserRateBPS: 500, CommissionAgentRateBPS: 500, CommissionReferralCapMinor: 3000, CommissionMaturitySeconds: 0, CommissionReserveBPS: 2000, WithdrawalEnabled: true, CommissionConversionEnabled: true, WithdrawalMinimumMinor: 20, WithdrawalCalendarMonthLimit: 10, WithdrawalFeeBPS: 300, ConversionQuotaNumerator: 5000, ConversionQuotaDenominator: 1, QuoteLifetimeSeconds: 300}
	require.NoError(t, DB.Create(&policy).Error)
	require.NoError(t, DB.Create(&AgentPolicyState{ID: 1, CurrentPolicyID: policy.ID, CommissionCutoverAt: policy.CommissionCutoverAt}).Error)
	buyer := User{Username: "commission-buyer", AffCode: "commission-buyer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, InviterId: inviter.Id}
	require.NoError(t, DB.Create(&buyer).Error)
	oldEnabled, oldRate := common.AffiliateRewardEnabled, common.AffiliateRewardRate
	common.AffiliateRewardEnabled = true
	common.AffiliateRewardRate = .05
	t.Cleanup(func() { common.AffiliateRewardEnabled, common.AffiliateRewardRate = oldEnabled, oldRate })
	return commissionFixture{root: root, inviter: inviter, buyer: buyer, rootIdentity: rootIdentity, identity: identity, policy: policy}
}

func commissionPaidTopup(t *testing.T, f commissionFixture, key string, paid int64) TopUp {
	t.Helper()
	order := TopUp{UserId: f.buyer.Id, TradeNo: key, Amount: 200, Money: 200, PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, Status: common.TopUpStatusPending, CreateTime: common.GetTimestamp()}
	require.NoError(t, DB.Create(&order).Error)
	facts, ok := NewAgentVerifiedPaymentFacts(PaymentProviderStripe, "pi_"+key, "synthetic-merchant", "live", "USD", fmt.Sprint(paid), true)
	require.True(t, ok)
	require.NoError(t, Recharge(key, "synthetic-customer", "127.0.0.1", facts))
	require.NoError(t, DB.First(&order, order.Id).Error)
	return order
}

func commissionDrain(t *testing.T) {
	t.Helper()
	_, err := ProcessAgentCommissionOutbox(context.Background(), 200)
	require.NoError(t, err)
	_, err = MatureAgentCommissionAccounts(context.Background(), 200)
	require.NoError(t, err)
}
func commissionAccount(t *testing.T, f commissionFixture) AgentCommissionAccount {
	t.Helper()
	var a AgentCommissionAccount
	require.NoError(t, DB.Where("user_id = ? AND currency = ?", f.inviter.Id, "USD").First(&a).Error)
	require.NoError(t, validateAgentCommissionAccount(&a))
	return a
}
func commissionProof(t *testing.T, identity AuthSessionIdentity, action, requestID string, input any) AgentTransactionAuthorization {
	t.Helper()
	payload, err := common.Marshal(input)
	require.NoError(t, err)
	prepared, err := PrepareAgentCommissionOperation(identity, action, requestID, payload)
	require.NoError(t, err)
	_, auth := issueAgentProfileTestProof(t, identity, prepared.VerificationContext)
	return auth
}

func commissionRecipient(t *testing.T, f commissionFixture) *AgentCommissionRecipient {
	t.Helper()
	input := AgentCommissionRecipientRequest{RequestID: "recipient-create", Label: "Synthetic payout file", RecipientReference: "synthetic-offline-beneficiary-001"}
	auth := commissionProof(t, f.identity, "recipient.change", input.RequestID, input)
	recipient, err := ChangeAgentCommissionRecipient(auth, input)
	require.NoError(t, err)
	return recipient
}

func commissionRefund(t *testing.T, f commissionFixture, order TopUp, reference string, cumulative int64) {
	t.Helper()
	request := AgentCommissionRefundRequest{RequestID: "record-" + reference, TopUpReferenceID: order.TradeNo, RefundReference: reference, CumulativeRefundedMinor: cumulative, EvidenceReference: "synthetic-refund-proof-" + reference}
	auth := commissionProof(t, f.rootIdentity, "commission.refund.record", request.RequestID, request)
	_, err := RecordAgentCommissionRefund(auth, request)
	require.NoError(t, err)
}

func TestAgentCommissionPrincipalOutboxAtomicAndLegacyFailureRecoveryPostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	// No verified receipt means the pre-cutover consumption reward, not a
	// speculative cash entitlement. A broken inviter is isolated by the outbox.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.inviter.Id).Update("aff_quota", -9).Error)
	order := TopUp{UserId: f.buyer.Id, TradeNo: "legacy-isolation", Amount: 2, Money: 2, PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay", Status: common.TopUpStatusPending}
	require.NoError(t, DB.Create(&order).Error)
	require.NoError(t, DB.Exec(`CREATE FUNCTION commission_fail_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic durable event failure'; END $$`).Error)
	require.NoError(t, DB.Exec(`CREATE TRIGGER commission_fail_outbox BEFORE INSERT ON agent_commission_outboxes FOR EACH ROW EXECUTE FUNCTION commission_fail_outbox()`).Error)
	_, err := RechargeEpay(order.TradeNo, "alipay", "2.00", "")
	require.Error(t, err)
	require.NoError(t, DB.First(&order, order.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
	assert.Zero(t, getUserBalances(t, f.buyer.Id).Quota)
	require.NoError(t, DB.Exec(`DROP TRIGGER commission_fail_outbox ON agent_commission_outboxes`).Error)
	require.NoError(t, DB.Exec(`DROP FUNCTION commission_fail_outbox()`).Error)
	_, err = RechargeEpay(order.TradeNo, "alipay", "2.00", "")
	require.NoError(t, err)
	principal := getUserBalances(t, f.buyer.Id).Quota
	assert.Positive(t, principal)
	_, err = ProcessAgentCommissionOutbox(context.Background(), 10)
	require.ErrorIs(t, err, ErrAgentCommissionInvariant)
	assert.Equal(t, principal, getUserBalances(t, f.buyer.Id).Quota)
	var event AgentCommissionOutbox
	require.NoError(t, DB.Where("reference_id = ?", order.TradeNo).First(&event).Error)
	assert.Equal(t, "failed", event.Status)
	// A later configuration change cannot revoke this already captured legacy
	// entitlement. Repair the bad balance, then retry the same durable event.
	common.AffiliateRewardEnabled = false
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.inviter.Id).Update("aff_quota", 0).Error)
	input := AgentCommissionRetryRequest{RequestID: "retry-legacy", EventID: event.ID, Reason: "Synthetic bad balance repaired"}
	auth := commissionProof(t, f.rootIdentity, "commission.outbox.retry", input.RequestID, input)
	_, err = RetryAgentCommissionOutbox(auth, input)
	require.NoError(t, err)
	commissionDrain(t)
	commissionDrain(t)
	assert.Equal(t, principal/20, getUserBalances(t, f.inviter.Id).AffQuota)
	var rewards int64
	require.NoError(t, DB.Model(&ReferralCommission{}).Count(&rewards).Error)
	assert.EqualValues(t, 1, rewards)
	var cash int64
	require.NoError(t, DB.Model(&AgentCommissionAward{}).Count(&cash).Error)
	assert.Zero(t, cash)
}

func TestAgentCommissionRealPaidSnapshotConcurrentCapAndNoDoubleRewardPostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	first := commissionPaidTopup(t, f, "cap-a", 40000)
	second := commissionPaidTopup(t, f, "cap-b", 40000)
	// Consumer must use the captured version even after an administrative rate
	// change, and profile toggles cannot change the lifetime cap key.
	changed := f.policy
	changed.ID = 0
	changed.RequestID = "later-policy"
	changed.CommissionUserRateBPS = 100
	require.NoError(t, DB.Create(&changed).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", changed.ID).Error)
	var events []AgentCommissionOutbox
	require.NoError(t, DB.Order("id").Find(&events).Error)
	require.Len(t, events, 2)
	start := make(chan struct{})
	errs := make(chan error, 4)
	var wg sync.WaitGroup
	for _, id := range []int64{events[0].ID, events[1].ID, events[0].ID, events[1].ID} {
		wg.Go(func() { <-start; errs <- processAgentCommissionEvent(context.Background(), id) })
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	a := commissionAccount(t, f)
	assert.EqualValues(t, 3000, a.EarnedMinor)
	var awards []AgentCommissionAward
	require.NoError(t, DB.Find(&awards).Error)
	require.Len(t, awards, 2)
	assert.EqualValues(t, 3000, awards[0].RewardMinor+awards[1].RewardMinor)
	for _, award := range awards {
		assert.EqualValues(t, 40000, award.PaidMinor)
		assert.EqualValues(t, 500, award.RateBPS)
		assert.Equal(t, f.policy.ID, award.PolicyVersion)
	}
	var legacy int64
	require.NoError(t, DB.Model(&ReferralCommission{}).Count(&legacy).Error)
	assert.Zero(t, legacy)
	require.NoError(t, DB.Create(&AgentProfile{UserID: f.inviter.Id, Status: AgentProfileExited, Version: 8}).Error)
	commissionPaidTopup(t, f, "cap-c", 40000)
	commissionDrain(t)
	assert.EqualValues(t, 3000, commissionAccount(t, f).EarnedMinor)
	// Reusing one actual provider payment for a second local order is rejected.
	third := TopUp{UserId: f.buyer.Id, TradeNo: "duplicate-provider-payment", Amount: 2, Money: 2, PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, Status: common.TopUpStatusPending}
	require.NoError(t, DB.Create(&third).Error)
	facts := first.verifiedAgentPaymentFacts()
	require.Error(t, Recharge(third.TradeNo, "synthetic-customer", "", facts))
	require.NoError(t, DB.First(&third, third.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, third.Status)
	assert.NotEqual(t, first.PaymentReceiptID, second.PaymentReceiptID)
}

func TestAgentCommissionConversionReservePartialRefundDebtAndReplayPostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	order := commissionPaidTopup(t, f, "convert-original", 10000)
	commissionDrain(t)
	a := commissionAccount(t, f)
	assert.EqualValues(t, 500, a.EarnedMinor)
	assert.EqualValues(t, 400, a.AvailableMinor)
	assert.EqualValues(t, 100, a.ReservedMinor)
	input := AgentCommissionQuoteRequest{RequestID: "wallet-convert", Target: "wallet", Currency: "USD", AmountMinor: 400}
	quote, err := CreateAgentCommissionQuote(f.identity, input)
	require.NoError(t, err)
	execute := AgentCommissionExecuteRequest{RequestID: input.RequestID, QuoteID: quote.ID}
	auth := commissionProof(t, f.identity, quote.Action, input.RequestID, execute)
	before := getUserBalances(t, f.inviter.Id)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { <-start; _, err := ExecuteAgentCommissionConversion(auth, execute); results <- err })
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	after := getUserBalances(t, f.inviter.Id)
	assert.Equal(t, before.QuotaReward+2_000_000, after.QuotaReward)
	assert.Equal(t, before.AffQuota, after.AffQuota)
	commissionRefund(t, f, order, "half", 5000)
	commissionDrain(t)
	a = commissionAccount(t, f)
	assert.EqualValues(t, 250, a.ReversedMinor)
	assert.EqualValues(t, 150, a.DebtMinor)
	assert.Zero(t, a.ReservedMinor)
	commissionRefund(t, f, order, "full", 10000)
	commissionDrain(t)
	a = commissionAccount(t, f)
	assert.EqualValues(t, 500, a.ReversedMinor)
	assert.EqualValues(t, 400, a.DebtMinor)
	commissionRefund(t, f, order, "old-half-after-full", 5000)
	commissionDrain(t)
	assert.EqualValues(t, 500, commissionAccount(t, f).ReversedMinor)
	assert.Equal(t, before.AffQuota, getUserBalances(t, f.inviter.Id).AffQuota)
	var eventCount int64
	require.NoError(t, DB.Model(&AgentCommissionOutbox{}).Where("kind = ?", "payment").Count(&eventCount).Error)
	assert.EqualValues(t, 1, eventCount, "wallet conversion never re-enters the recharge event stream")
	bad := AgentCommissionQuoteRequest{RequestID: "debt-convert", Target: "wallet", Currency: "USD", AmountMinor: 1}
	debtQuote, err := CreateAgentCommissionQuote(f.identity, bad)
	require.NoError(t, err)
	debtExecute := AgentCommissionExecuteRequest{RequestID: bad.RequestID, QuoteID: debtQuote.ID}
	debtAuth := commissionProof(t, f.identity, debtQuote.Action, bad.RequestID, debtExecute)
	_, err = ExecuteAgentCommissionConversion(debtAuth, debtExecute)
	require.ErrorIs(t, err, ErrAgentCommissionDebt)
	report, err := ReconcileAgentCommissions(f.rootIdentity, 0)
	require.NoError(t, err)
	assert.Empty(t, report.InvariantViolations)
}

func TestAgentCommissionUnknownWithdrawalFreezeRefundAndFailureRecoveryPostgres(t *testing.T) {
	for _, outcome := range []string{"failed", "succeeded"} {
		t.Run(outcome, func(t *testing.T) {
			f := setupAgentCommissionPostgres(t)
			order := commissionPaidTopup(t, f, "withdraw-original", 10000)
			commissionDrain(t)
			recipient := commissionRecipient(t, f)
			q, err := CreateAgentCommissionQuote(f.identity, AgentCommissionQuoteRequest{RequestID: "withdraw-request", Target: "withdrawal", Currency: "USD", AmountMinor: 300, RecipientID: recipient.ID, RecipientVersion: recipient.Version})
			require.NoError(t, err)
			execute := AgentCommissionExecuteRequest{RequestID: q.RequestID, QuoteID: q.ID}
			auth := commissionProof(t, f.identity, q.Action, q.RequestID, execute)
			w, err := RequestAgentCommissionWithdrawal(auth, execute)
			require.NoError(t, err)
			assert.EqualValues(t, 9, w.FeeMinor)
			assert.EqualValues(t, 291, w.NetMinor)
			for _, step := range []struct{ action, status, decision string }{{"commission.withdraw.approve", "", "approve"}, {"commission.withdraw.submit", "", ""}, {"commission.withdraw.resolve", "unknown", ""}} {
				input := AgentCommissionWithdrawalAction{RequestID: strings.ReplaceAll(step.action, ".", "-"), WithdrawalID: w.ID, Version: w.Version, Decision: step.decision, Status: step.status, Reason: "Synthetic independent review"}
				if step.status != "" {
					input.EvidenceReference = "synthetic-missing-confirmation"
				}
				auth := commissionProof(t, f.rootIdentity, step.action, input.RequestID, input)
				w, err = ActOnAgentCommissionWithdrawal(auth, step.action, input)
				require.NoError(t, err)
			}
			businessNo := w.BusinessNo
			assert.Equal(t, "unknown", w.Status)
			assert.EqualValues(t, 300, commissionAccount(t, f).HeldMinor)
			commissionRefund(t, f, order, "withdraw-refund", 10000)
			commissionDrain(t)
			a := commissionAccount(t, f)
			assert.EqualValues(t, 300, a.DebtMinor)
			assert.EqualValues(t, 300, a.HeldMinor)
			// Unknown money cannot be freed by another submit request or a timeout.
			invalid := AgentCommissionWithdrawalAction{RequestID: "unknown-submit-again", WithdrawalID: w.ID, Version: w.Version, Reason: "Do not pay twice"}
			_, err = PrepareAgentCommissionOperation(f.rootIdentity, "commission.withdraw.submit", invalid.RequestID, mustCommissionJSON(t, invalid))
			require.Error(t, err)
			falseFailure := AgentCommissionWithdrawalAction{RequestID: "unconfirmed-failure", WithdrawalID: w.ID, Version: w.Version, Status: "failed", Reason: "Timeout is not proof", EvidenceReference: "timeout"}
			_, err = PrepareAgentCommissionOperation(f.rootIdentity, "commission.withdraw.resolve", falseFailure.RequestID, mustCommissionJSON(t, falseFailure))
			require.Error(t, err)
			// Freezing the agent and closing new business does not block accounting
			// for a payout already sent; the exact unknown obligation remains intact.
			require.NoError(t, DB.Create(&AgentProfile{UserID: f.inviter.Id, Status: AgentProfileExited, SettlementFrozen: true, Version: 1}).Error)
			closed := f.policy
			closed.ID = 0
			closed.RequestID = "closed-policy"
			closed.WithdrawalEnabled = false
			closed.CommissionConversionEnabled = false
			require.NoError(t, DB.Create(&closed).Error)
			require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", closed.ID).Error)
			resolve := AgentCommissionWithdrawalAction{RequestID: "resolve-unknown", WithdrawalID: w.ID, Version: w.Version, Status: outcome, Reason: "Verified independent settlement result", EvidenceReference: "synthetic-final-payout-evidence", FailureConfirmedUnpaid: outcome == "failed"}
			if outcome == "succeeded" {
				resolve.PayoutProvider = "bank_transfer"
				resolve.PayoutReference = "synthetic-outbound-transfer"
				resolve.PayoutMerchantScope = "synthetic-settlement-bank"
				resolve.PayoutEnvironment = "live"
			}
			resolveAuth := commissionProof(t, f.rootIdentity, "commission.withdraw.resolve", resolve.RequestID, resolve)
			w, err = ActOnAgentCommissionWithdrawal(resolveAuth, "commission.withdraw.resolve", resolve)
			require.NoError(t, err)
			assert.Equal(t, businessNo, w.BusinessNo)
			a = commissionAccount(t, f)
			assert.Zero(t, a.HeldMinor)
			assert.Zero(t, a.AvailableMinor)
			if outcome == "failed" {
				assert.Zero(t, a.DebtMinor)
				assert.EqualValues(t, 300, a.DebtRecoveredMinor)
			} else {
				assert.EqualValues(t, 300, a.DebtMinor)
				assert.EqualValues(t, 300, a.TransferredMinor)
			}
			_, err = ActOnAgentCommissionWithdrawal(resolveAuth, "commission.withdraw.resolve", resolve)
			require.NoError(t, err, "exact replay preserves a terminal result without a second payment")
			report, err := ReconcileAgentCommissions(f.rootIdentity, 0)
			require.NoError(t, err)
			assert.Empty(t, report.InvariantViolations)
			assert.Zero(t, report.UnknownWithdrawals)
		})
	}
}

func mustCommissionJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := common.Marshal(value)
	require.NoError(t, err)
	return data
}

func TestAgentCommissionRefundBeforePaymentProcessingAndRecipientChangePostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	order := commissionPaidTopup(t, f, "out-of-order-original", 10000)
	commissionRefund(t, f, order, "early-refund", 5000)
	var refund AgentCommissionOutbox
	require.NoError(t, DB.Where("kind = ?", "refund").First(&refund).Error)
	require.Error(t, processAgentCommissionEvent(context.Background(), refund.ID))
	var inbox int64
	require.NoError(t, DB.Model(&AgentCommissionInbox{}).Where("event_id = ?", refund.ID).Count(&inbox).Error)
	assert.Zero(t, inbox)
	commissionDrain(t)
	assert.EqualValues(t, 250, commissionAccount(t, f).ReversedMinor)
	recipient := commissionRecipient(t, f)
	quote, err := CreateAgentCommissionQuote(f.identity, AgentCommissionQuoteRequest{RequestID: "old-recipient-withdraw", Target: "withdrawal", Currency: "USD", AmountMinor: 100, RecipientID: recipient.ID, RecipientVersion: 1})
	require.NoError(t, err)
	execute := AgentCommissionExecuteRequest{RequestID: quote.RequestID, QuoteID: quote.ID}
	auth := commissionProof(t, f.identity, quote.Action, quote.RequestID, execute)
	change := AgentCommissionRecipientRequest{RequestID: "recipient-change", RecipientID: recipient.ID, Version: 1, Label: "Changed beneficiary record", RecipientReference: "synthetic-other-beneficiary"}
	changeAuth := commissionProof(t, f.identity, "recipient.change", change.RequestID, change)
	changed, err := ChangeAgentCommissionRecipient(changeAuth, change)
	require.NoError(t, err)
	assert.EqualValues(t, 2, changed.Version)
	_, err = RequestAgentCommissionWithdrawal(auth, execute)
	require.ErrorIs(t, err, ErrAgentCommissionConflict)
	_, total, err := ListAgentCommissionRecords(f.identity, "recipients_current", 0, 1, 100, "", false)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	_, err = GetAgentCommissionSummary(f.identity, f.root.Id)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	var withdrawals int64
	require.NoError(t, DB.Model(&AgentCommissionWithdrawal{}).Count(&withdrawals).Error)
	assert.Zero(t, withdrawals)
}

func TestAgentCommissionMaturityAndTestPaymentsRemainNonCashPostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	later := f.policy
	later.ID = 0
	later.RequestID = "maturity-policy"
	later.CommissionMaturitySeconds = 86400
	require.NoError(t, DB.Create(&later).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", later.ID).Error)
	commissionPaidTopup(t, f, "not-mature", 10000)
	commissionDrain(t)
	a := commissionAccount(t, f)
	assert.EqualValues(t, 500, a.PendingMinor)
	assert.Zero(t, a.AvailableMinor)
	require.NoError(t, DB.Model(&AgentCommissionAward{}).Where("user_id = ?", f.inviter.Id).Update("matures_at", time.Now().Add(-time.Second).Unix()).Error)
	commissionDrain(t)
	a = commissionAccount(t, f)
	assert.Zero(t, a.PendingMinor)
	assert.EqualValues(t, 400, a.AvailableMinor)
	assert.EqualValues(t, 100, a.ReservedMinor)
	order := TopUp{UserId: f.buyer.Id, TradeNo: "test-payment", Amount: 20, Money: 20, PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, Status: common.TopUpStatusPending}
	require.NoError(t, DB.Create(&order).Error)
	facts, ok := NewAgentVerifiedPaymentFacts(PaymentProviderStripe, "pi_test-only", "synthetic-merchant", "test", "USD", "10000", true)
	require.True(t, ok)
	require.NoError(t, Recharge(order.TradeNo, "", "", facts))
	commissionDrain(t)
	assert.EqualValues(t, 500, commissionAccount(t, f).EarnedMinor)
	_, ok = NewAgentVerifiedPaymentFacts(PaymentProviderStripe, "cs_not-a-payment", "merchant", "live", "USD", "10000", true)
	assert.False(t, ok)
	_, ok = NewAgentVerifiedPaymentFacts(PaymentProviderStripe, "pi_unknown-currency", "merchant", "live", "ZZZ", "10000", true)
	assert.False(t, ok)
}

func TestAgentCommissionProcurementConversionNoDiscountNoCashRefundAndStaleProfilePostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	order := commissionPaidTopup(t, f, "procurement-original", 10000)
	commissionDrain(t)
	require.NoError(t, DB.Create(&AgentProfile{UserID: f.inviter.Id, Status: AgentProfileActive, Version: 1}).Error)
	input := AgentCommissionQuoteRequest{RequestID: "commission-procurement", Target: "procurement", Currency: "USD", AmountMinor: 200}
	quote, err := CreateAgentCommissionQuote(f.identity, input)
	require.NoError(t, err)
	execute := AgentCommissionExecuteRequest{RequestID: input.RequestID, QuoteID: quote.ID}
	auth := commissionProof(t, f.identity, quote.Action, input.RequestID, execute)
	before := getUserBalances(t, f.inviter.Id)
	result, err := ExecuteAgentCommissionConversion(auth, execute)
	require.NoError(t, err)
	assert.Equal(t, 1_000_000, result.Quota, "only the immutable conversion ratio applies; no procurement discount")
	var batch AgentFundBatch
	require.NoError(t, DB.Where("agent_user_id = ? AND source_kind = ?", f.inviter.Id, AgentFundCommission).First(&batch).Error)
	assert.False(t, batch.CashRefundable)
	assert.Equal(t, AccountReward, batch.WalletAccount)
	assert.Equal(t, "200", batch.OriginalPaidMinor)
	assert.Equal(t, 2, batch.CurrencyExponent)
	assert.Equal(t, f.policy.ID, batch.PolicyVersion)
	assert.NotEqual(t, "{}", batch.PolicySnapshot)
	var account AgentRedemptionAccount
	require.NoError(t, DB.Where("agent_user_id = ?", f.inviter.Id).First(&account).Error)
	assert.Equal(t, 1_000_000, account.AvailableQuota)
	assert.Equal(t, before.Quota, getUserBalances(t, f.inviter.Id).Quota, "procurement transfer does not simultaneously credit personal wallet")
	// A new quote is invalidated by exit even if its proof was freshly issued.
	input.RequestID = "stale-profile-procurement"
	input.AmountMinor = 100
	stale, err := CreateAgentCommissionQuote(f.identity, input)
	require.NoError(t, err)
	execute = AgentCommissionExecuteRequest{RequestID: input.RequestID, QuoteID: stale.ID}
	auth = commissionProof(t, f.identity, stale.Action, input.RequestID, execute)
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.inviter.Id).Updates(map[string]any{"status": AgentProfileExited, "version": 2}).Error)
	_, err = ExecuteAgentCommissionConversion(auth, execute)
	require.Error(t, err)
	assert.EqualValues(t, 200, commissionAccount(t, f).AvailableMinor)
	commissionRefund(t, f, order, "procurement-full-refund", 10000)
	commissionDrain(t)
	assert.EqualValues(t, 200, commissionAccount(t, f).DebtMinor)
	require.NoError(t, DB.First(&batch, batch.ID).Error)
	assert.False(t, batch.CashRefundable)
}

func TestAgentCommissionDistinctConcurrentSpendsCannotOverdrawAndProofRollbackPostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	commissionPaidTopup(t, f, "concurrent-spend-original", 10000)
	commissionDrain(t)
	type spend struct {
		input AgentCommissionExecuteRequest
		auth  AgentTransactionAuthorization
	}
	spends := make([]spend, 2)
	for i := range spends {
		key := fmt.Sprintf("distinct-spend-%d", i)
		quote, err := CreateAgentCommissionQuote(f.identity, AgentCommissionQuoteRequest{RequestID: key, Target: "wallet", Currency: "USD", AmountMinor: 300})
		require.NoError(t, err)
		spends[i].input = AgentCommissionExecuteRequest{RequestID: key, QuoteID: quote.ID}
		spends[i].auth = commissionProof(t, f.identity, quote.Action, key, spends[i].input)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, s := range spends {
		wg.Go(func() { <-start; _, err := ExecuteAgentCommissionConversion(s.auth, s.input); results <- err })
	}
	close(start)
	wg.Wait()
	close(results)
	successes, failures := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrAgentCommissionInsufficient)
			failures++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, failures)
	assert.EqualValues(t, 100, commissionAccount(t, f).AvailableMinor)
	// A DB failure after financial proof consumption must roll back both proof
	// and all wallet/lots/operation writes, allowing exactly one later retry.
	quote, err := CreateAgentCommissionQuote(f.identity, AgentCommissionQuoteRequest{RequestID: "rollback-proof-spend", Target: "wallet", Currency: "USD", AmountMinor: 100})
	require.NoError(t, err)
	execute := AgentCommissionExecuteRequest{RequestID: quote.RequestID, QuoteID: quote.ID}
	auth := commissionProof(t, f.identity, quote.Action, quote.RequestID, execute)
	before := getUserBalances(t, f.inviter.Id)
	require.NoError(t, DB.Exec(`CREATE FUNCTION commission_fail_ledger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic post-proof ledger failure'; END $$`).Error)
	require.NoError(t, DB.Exec(`CREATE TRIGGER commission_fail_ledger BEFORE INSERT ON agent_commission_ledgers FOR EACH ROW EXECUTE FUNCTION commission_fail_ledger()`).Error)
	_, err = ExecuteAgentCommissionConversion(auth, execute)
	require.Error(t, err)
	assert.Equal(t, before.Quota, getUserBalances(t, f.inviter.Id).Quota)
	assert.EqualValues(t, 100, commissionAccount(t, f).AvailableMinor)
	var count int64
	require.NoError(t, DB.Model(&AgentCommissionOperation{}).Where("request_id = ?", execute.RequestID).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Exec(`DROP TRIGGER commission_fail_ledger ON agent_commission_ledgers`).Error)
	require.NoError(t, DB.Exec(`DROP FUNCTION commission_fail_ledger()`).Error)
	_, err = ExecuteAgentCommissionConversion(auth, execute)
	require.NoError(t, err)
	_, err = ExecuteAgentCommissionConversion(auth, execute)
	require.NoError(t, err)
	assert.Equal(t, before.Quota+500000, getUserBalances(t, f.inviter.Id).Quota)
	assert.Zero(t, commissionAccount(t, f).AvailableMinor)
}

func TestAgentCommissionReserveReleaseRootOnlyAndOldAffConsumptionUnderFreezePostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	commissionPaidTopup(t, f, "reserve-original", 10000)
	commissionDrain(t)
	input := AgentCommissionReserveRequest{RequestID: "release-risk-reserve", UserID: f.inviter.Id, Currency: "USD", AmountMinor: 100, Reason: "Independent risk review complete"}
	_, err := PrepareAgentCommissionOperation(f.identity, "commission.reserve.release", input.RequestID, mustCommissionJSON(t, input))
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	prepared, err := PrepareAgentCommissionOperation(f.rootIdentity, "commission.reserve.release", input.RequestID, mustCommissionJSON(t, input))
	require.NoError(t, err)
	_, userAuth := issueAgentProfileTestProof(t, f.identity, prepared.VerificationContext)
	_, err = ReleaseAgentCommissionReserve(userAuth, input)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	assert.EqualValues(t, 100, commissionAccount(t, f).ReservedMinor)
	auth := commissionProof(t, f.rootIdentity, "commission.reserve.release", input.RequestID, input)
	_, err = ReleaseAgentCommissionReserve(auth, input)
	require.NoError(t, err)
	restored, replayed, err := ReplayAgentCommissionOperation(f.rootIdentity, "commission.reserve.release", input.RequestID, input)
	require.NoError(t, err)
	assert.True(t, replayed)
	require.NotNil(t, restored)
	assert.EqualValues(t, 500, commissionAccount(t, f).AvailableMinor)
	require.NoError(t, DB.Create(&AgentProfile{UserID: f.inviter.Id, Status: AgentProfileExited, SalesFrozen: true, SettlementFrozen: true, Version: 1}).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", 0).Error)
	// The old legal aff entitlement remains consumption-only even with all new
	// cash features disabled and the business profile frozen. Old key syntax stays.
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 20
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })
	legacy := AgentLegacyCommissionRequest{RequestID: "x.1", Quota: 20}
	auth = commissionProof(t, f.identity, "commission.convert.legacy", legacy.RequestID, legacy)
	before := getUserBalances(t, f.inviter.Id)
	_, err = TransferAffQuota(f.inviter.Id, legacy.Quota, legacy.RequestID, auth)
	require.NoError(t, err)
	after := getUserBalances(t, f.inviter.Id)
	assert.Equal(t, before.AffQuota-20, after.AffQuota)
	assert.Equal(t, before.QuotaReward+20, after.QuotaReward)
	recovery, err := FindAgentCommissionOperation(f.identity, legacy.RequestID)
	require.NoError(t, err)
	require.NotNil(t, recovery)
	assert.Equal(t, "completed", recovery.Status)
	assert.Equal(t, "commission.convert.legacy", recovery.Action)
	foreign, err := FindAgentCommissionOperation(f.rootIdentity, legacy.RequestID)
	require.NoError(t, err)
	assert.Nil(t, foreign)

}

func TestAgentCommissionVerifiedProviderRefundBeforePaymentReplayAndCumulativePostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	input := AgentVerifiedProviderRefund{Provider: PaymentProviderStripe, EventReference: "evt_early_half", PaymentReference: "pi_provider-late", MerchantScope: "synthetic-merchant", Environment: "live", Currency: "USD", CurrencyExponent: 2, ChargePaidMinor: 10000, CumulativeRefundedMinor: 5000}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return EnqueueAgentCommissionProviderRefundTx(tx, input) }))
	var event AgentCommissionOutbox
	require.NoError(t, DB.Where("kind = ?", "provider_refund").First(&event).Error)
	require.Error(t, processAgentCommissionEvent(context.Background(), event.ID), "original receipt absent must remain retryable")
	var inbox int64
	require.NoError(t, DB.Model(&AgentCommissionInbox{}).Count(&inbox).Error)
	assert.Zero(t, inbox)
	_, err := ProcessAgentCommissionOutbox(context.Background(), 10)
	require.Error(t, err)
	retry := AgentCommissionRetryRequest{RequestID: "retry-unmapped-provider-refund", EventID: event.ID, Reason: "Original payment webhook is delayed"}
	auth := commissionProof(t, f.rootIdentity, "commission.outbox.retry", retry.RequestID, retry)
	_, err = RetryAgentCommissionOutbox(auth, retry)
	require.NoError(t, err, "root can safely schedule an unmapped immutable event without inventing an owner")
	commissionPaidTopup(t, f, "provider-late", 10000)
	require.NoError(t, processAgentCommissionEvent(context.Background(), event.ID))
	commissionDrain(t)
	assert.EqualValues(t, 250, commissionAccount(t, f).ReversedMinor)
	// Duplicate webhook is the same immutable inbox; a changed body with the
	// same provider event ID is a conflict rather than an updated instruction.
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return EnqueueAgentCommissionProviderRefundTx(tx, input) }))
	tampered := input
	tampered.CumulativeRefundedMinor = 6000
	require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { return EnqueueAgentCommissionProviderRefundTx(tx, tampered) }), ErrAgentCommissionConflict)
	input.EventReference = "evt_late_full"
	input.CumulativeRefundedMinor = 10000
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return EnqueueAgentCommissionProviderRefundTx(tx, input) }))
	commissionDrain(t)
	commissionDrain(t)
	assert.EqualValues(t, 500, commissionAccount(t, f).ReversedMinor)
	input.EventReference = "evt_stale_partial"
	input.CumulativeRefundedMinor = 2500
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return EnqueueAgentCommissionProviderRefundTx(tx, input) }))
	commissionDrain(t)
	commissionDrain(t)
	assert.EqualValues(t, 500, commissionAccount(t, f).ReversedMinor)
	input.EventReference = "evt_excess_refund"
	input.CumulativeRefundedMinor = 10001
	require.Error(t, DB.Transaction(func(tx *gorm.DB) error { return EnqueueAgentCommissionProviderRefundTx(tx, input) }))
}

func TestAgentCommissionNewMaturityAndReleasedReserveRepayDebtWithoutRegrantPostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	original := commissionPaidTopup(t, f, "debt-original", 10000)
	commissionDrain(t)
	quote, err := CreateAgentCommissionQuote(f.identity, AgentCommissionQuoteRequest{RequestID: "debt-wallet-transfer", Target: "wallet", Currency: "USD", AmountMinor: 400})
	require.NoError(t, err)
	execute := AgentCommissionExecuteRequest{RequestID: quote.RequestID, QuoteID: quote.ID}
	auth := commissionProof(t, f.identity, quote.Action, quote.RequestID, execute)
	_, err = ExecuteAgentCommissionConversion(auth, execute)
	require.NoError(t, err)
	commissionRefund(t, f, original, "original-chargeback", 10000)
	commissionDrain(t)
	assert.EqualValues(t, 400, commissionAccount(t, f).DebtMinor)
	replacement := commissionPaidTopup(t, f, "replacement-award", 5000)
	commissionDrain(t)
	a := commissionAccount(t, f)
	assert.EqualValues(t, 200, a.DebtMinor)
	assert.EqualValues(t, 200, a.DebtRecoveredMinor)
	assert.Zero(t, a.AvailableMinor)
	assert.EqualValues(t, 50, a.ReservedMinor)
	release := AgentCommissionReserveRequest{RequestID: "release-to-recover-debt", UserID: f.inviter.Id, Currency: "USD", AmountMinor: 50, Reason: "Independent risk reserve release"}
	auth = commissionProof(t, f.rootIdentity, "commission.reserve.release", release.RequestID, release)
	_, err = ReleaseAgentCommissionReserve(auth, release)
	require.NoError(t, err)
	a = commissionAccount(t, f)
	assert.EqualValues(t, 150, a.DebtMinor)
	assert.EqualValues(t, 250, a.DebtRecoveredMinor)
	assert.Zero(t, a.AvailableMinor)
	assert.Zero(t, a.ReservedMinor)
	// Refunding the award used for debt recovery cannot count its prior recovery
	// twice. The original external transfer remains exactly the outstanding debt.
	commissionRefund(t, f, replacement, "replacement-chargeback", 5000)
	commissionDrain(t)
	a = commissionAccount(t, f)
	assert.EqualValues(t, 400, a.DebtMinor)
	assert.EqualValues(t, 750, a.ReversedMinor)
	assert.EqualValues(t, 400, a.TransferredMinor)
	report, err := ReconcileAgentCommissions(f.rootIdentity, 0)
	require.NoError(t, err)
	assert.Empty(t, report.InvariantViolations)
	var cap AgentCommissionCap
	require.NoError(t, DB.Where("user_id = ? AND source_user_id = ?", f.inviter.Id, f.buyer.Id).First(&cap).Error)
	assert.EqualValues(t, 750, cap.EarnedMinor)
}

func TestAgentCommissionPrivilegedRecoveryRejectsCurrentRoleDowngradePostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	order := commissionPaidTopup(t, f, "recovery-original", 10000)
	commissionDrain(t)
	input := AgentCommissionReserveRequest{RequestID: "privileged-recovery", UserID: f.inviter.Id, Currency: "USD", AmountMinor: 100, Reason: "Synthetic reviewed release"}
	auth := commissionProof(t, f.rootIdentity, "commission.reserve.release", input.RequestID, input)
	_, err := ReleaseAgentCommissionReserve(auth, input)
	require.NoError(t, err)
	status, err := FindAgentCommissionOperation(f.rootIdentity, input.RequestID)
	require.NoError(t, err)
	require.NotNil(t, status)
	commissionRefund(t, f, order, "root-recovery-refund", 1000)
	// An otherwise current browser session is not a permanent administrative
	// capability. Recovery and exact HTTP-style replay both recheck live role.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.root.Id).Update("role", common.RoleAdminUser).Error)
	_, err = FindAgentCommissionOperation(f.rootIdentity, input.RequestID)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	_, _, err = ReplayAgentCommissionOperation(f.rootIdentity, "commission.reserve.release", input.RequestID, input)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	_, err = ReleaseAgentCommissionReserve(auth, input)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	_, err = FindAgentCommissionOperation(f.rootIdentity, "record-root-recovery-refund")
	require.ErrorIs(t, err, ErrAgentProfilePermission)
}

func TestAgentCommissionRecipientFailureNeverLogsRawDestinationOrConsumesProofPostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	input := AgentCommissionRecipientRequest{RequestID: "private-recipient-failure", Label: "Offline beneficiary", RecipientReference: "synthetic-private-destination-do-not-log"}
	auth := commissionProof(t, f.identity, "recipient.change", input.RequestID, input)
	require.NoError(t, DB.Exec(`CREATE FUNCTION commission_fail_recipient() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic recipient persistence failure'; END $$`).Error)
	require.NoError(t, DB.Exec(`CREATE TRIGGER commission_fail_recipient BEFORE INSERT ON agent_commission_recipients FOR EACH ROW EXECUTE FUNCTION commission_fail_recipient()`).Error)
	original := DB
	var captured bytes.Buffer
	DB = DB.Session(&gorm.Session{Logger: gormlogger.New(log.New(&captured, "", 0), gormlogger.Config{LogLevel: gormlogger.Info})})
	t.Cleanup(func() { DB = original })
	_, err := ChangeAgentCommissionRecipient(auth, input)
	require.Error(t, err)
	assert.NotContains(t, captured.String(), input.RecipientReference)
	assert.NotContains(t, captured.String(), auth.token)
	require.NoError(t, DB.Exec(`DROP TRIGGER commission_fail_recipient ON agent_commission_recipients`).Error)
	require.NoError(t, DB.Exec(`DROP FUNCTION commission_fail_recipient()`).Error)
	_, err = ChangeAgentCommissionRecipient(auth, input)
	require.NoError(t, err, "failed money transaction rolls back the consumed proof")
	var count int64
	require.NoError(t, DB.Model(&AgentCommissionRecipient{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	assert.NotContains(t, captured.String(), input.RecipientReference)
}

func TestAgentCommissionPreparationRejectsUnknownContradictoryAndTrailingFieldsPostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	for _, payload := range []string{
		`{"request_id":"strict-recipient","label":"Synthetic","recipient_reference":"synthetic-offline","user_id":999}`,
		`{"request_id":"different-request","label":"Synthetic","recipient_reference":"synthetic-offline"}`,
		`{"request_id":"strict-recipient","label":"Synthetic","recipient_reference":"synthetic-offline"} {}`,
	} {
		_, err := PrepareAgentCommissionOperation(f.identity, "recipient.change", "strict-recipient", []byte(payload))
		require.Error(t, err)
	}
	var count int64
	require.NoError(t, DB.Model(&AgentCommissionRecipient{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAgentCommissionManualInstructionsRequireApprovedSubmittedIndependentLiveFinancePostgres(t *testing.T) {
	f := setupAgentCommissionPostgres(t)
	commissionPaidTopup(t, f, "manual-instructions-payment", 10000)
	commissionDrain(t)
	recipient := commissionRecipient(t, f)
	quote, err := CreateAgentCommissionQuote(f.identity, AgentCommissionQuoteRequest{RequestID: "manual-instructions-withdraw", Target: "withdrawal", Currency: "USD", AmountMinor: 300, RecipientID: recipient.ID, RecipientVersion: recipient.Version})
	require.NoError(t, err)
	execute := AgentCommissionExecuteRequest{RequestID: quote.RequestID, QuoteID: quote.ID}
	auth := commissionProof(t, f.identity, quote.Action, quote.RequestID, execute)
	withdrawal, err := RequestAgentCommissionWithdrawal(auth, execute)
	require.NoError(t, err)
	_, err = GetAgentCommissionPayoutInstruction(f.rootIdentity, withdrawal.ID)
	require.Error(t, err, "applied drafts never expose registry IDs")
	approve := AgentCommissionWithdrawalAction{RequestID: "manual-instructions-approve", WithdrawalID: withdrawal.ID, Version: withdrawal.Version, Decision: "approve", Reason: "Independent approval"}
	auth = commissionProof(t, f.rootIdentity, "commission.withdraw.approve", approve.RequestID, approve)
	withdrawal, err = ActOnAgentCommissionWithdrawal(auth, "commission.withdraw.approve", approve)
	require.NoError(t, err)
	_, err = GetAgentCommissionPayoutInstruction(f.rootIdentity, withdrawal.ID)
	require.Error(t, err, "approval alone is not the MFA-protected submit boundary")
	submit := AgentCommissionWithdrawalAction{RequestID: "manual-instructions-submit", WithdrawalID: withdrawal.ID, Version: withdrawal.Version, Reason: "Manual external settlement in progress"}
	auth = commissionProof(t, f.rootIdentity, "commission.withdraw.submit", submit.RequestID, submit)
	withdrawal, err = ActOnAgentCommissionWithdrawal(auth, "commission.withdraw.submit", submit)
	require.NoError(t, err)
	require.NotNil(t, withdrawal.ManualInstruction)
	assert.Equal(t, "291", withdrawal.ManualInstruction.AmountMinor)
	assert.Equal(t, recipient.Reference, withdrawal.ManualInstruction.RecipientRegistryID)
	assert.True(t, withdrawal.ManualInstruction.ManualOnly)
	instruction, err := GetAgentCommissionPayoutInstruction(f.rootIdentity, withdrawal.ID)
	require.NoError(t, err)
	assert.Equal(t, withdrawal.BusinessNo, instruction.BusinessNo)
	_, err = GetAgentCommissionPayoutInstruction(f.identity, withdrawal.ID)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	ordinary, err := GetAgentCommissionWithdrawal(f.identity, withdrawal.ID)
	require.NoError(t, err)
	encoded, err := common.Marshal(ordinary)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), recipient.Reference)
	recovered, err := FindAgentCommissionOperation(f.rootIdentity, submit.RequestID)
	require.NoError(t, err)
	encoded, err = common.Marshal(recovered)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), recipient.Reference, "generic recovery remains masked")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.root.Id).Update("role", common.RoleCommonUser).Error)
	_, err = GetAgentCommissionPayoutInstruction(f.rootIdentity, withdrawal.ID)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	// No read changed payout state or allocated money a second time.
	a := commissionAccount(t, f)
	assert.EqualValues(t, 300, a.HeldMinor)
	assert.Zero(t, a.TransferredMinor)
}
