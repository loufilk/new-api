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
	"gorm.io/gorm"
)

func TestAgentFundEconomicsCumulativeIntegerConservation(t *testing.T) {
	// Explicit commercial counterexamples and uneven/exact ratios. Every possible
	// partition of each small fixture is exhaustive, not randomized stress.
	for _, tc := range []struct {
		q int
		p int64
	}{{10, 7}, {20, 2}, {10, 1}, {7, 10}, {3, 2}, {4, 4}} {
		t.Run(fmt.Sprintf("Q%dP%d", tc.q, tc.p), func(t *testing.T) {
			for s := 0; s <= tc.q; s++ {
				for r := 0; r <= tc.q-s; r++ {
					c, l, err := agentFundEconomicTargets(tc.p, tc.q, s, r)
					require.NoError(t, err)
					assert.GreaterOrEqual(t, c, int64(0))
					assert.GreaterOrEqual(t, l, int64(0))
					assert.LessOrEqual(t, c+l, tc.p)
					// Safe small operands make this an independent exact-integer oracle.
					assert.Equal(t, (tc.p*int64(s)+int64(tc.q)-1)/int64(tc.q), c)
					assert.Equal(t, tc.p*int64(r)/int64(tc.q), l)
					if s+r == tc.q {
						assert.Equal(t, tc.p, c+l)
					}
				}
			}
		})
	}
	_, _, err := agentFundEconomicTargets(int64(common.MaxWalletQuota), common.MaxWalletQuota, common.MaxWalletQuota, 0)
	require.NoError(t, err)
	_, _, err = agentFundEconomicTargets(7, 10, 9, 2)
	require.Error(t, err)
}

func TestAgentFundEconomicsRepeatedCreditDrawSpendReleaseCannotRecycleFreeValuePostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 9, "economic-repeat")
	for i := range 3 {
		suffix := strconv.Itoa(i)
		draw := creditDraw(t, f, limit, 10, "economic-repeat-"+suffix)
		creditSelfUse(t, f, draw, 1, "economic-repeat-"+suffix)
		result := creditExecute(t, f.Owner, "credit.release", AgentCreditCommand{RequestID: "economic-release-" + suffix, ResourceID: draw.ID, Quota: 9})
		assert.Equal(t, "6", result.Resource.(map[string]any)["released_minor"])
		assertCreditAccount(t, f.Owner.UserID, 0, int64(i+1), 0)
		var e AgentFundEconomics
		require.NoError(t, DB.First(&e, "batch_id = ?", draw.BatchID).Error)
		assert.Equal(t, []int64{1, 6}, []int64{e.ChargedMinor, e.ReleasedMinor})
		assert.Equal(t, []int{1, 9}, []int{e.SpentQuota, e.ReleasedQuota})
	}
	c := AgentCreditCommand{RequestID: "economic-over-limit", Quota: 10, ExpectedLimitID: limit.ID}
	payload, _ := common.Marshal(c)
	_, err := PrepareAgentCreditOperation(f.Owner, "credit.draw", c.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentCreditRisk)
	assert.Equal(t, 3, getUserBalances(t, f.Owner.UserID).QuotaReward)
	findings, err := ReconcileAgentCreditLiabilities(f.Owner, 0)
	require.NoError(t, err)
	require.Empty(t, findings)
	// Repayment, not releasing unused quota, restores the consumed capacity.
	ars := creditARs(t, f.Owner.UserID)
	allocations := []AgentReceivableAllocationInput{}
	for _, ar := range ars {
		allocations = append(allocations, AgentReceivableAllocationInput{ReceivableID: ar.ID, AmountMinor: "1"})
	}
	repayment := creditRecordRepayment(t, f, allocations, 3, "economic-repay")
	creditExecute(t, f.Reviewer, "credit.repayment.confirm", AgentCreditCommand{RequestID: "economic-repay-confirm", ResourceID: repayment.ID, Version: repayment.Version, EvidenceReference: "verified-synthetic-bank"})
	creditDraw(t, f, limit, 10, "economic-after-repay")
	assertCreditAccount(t, f.Owner.UserID, 7, 0, 0)
}

func TestAgentFundEconomicsNearTotalSpendAndZeroCarryPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	p := f.Policy
	p.ID = 0
	p.RequestID = "economic-q20-p2"
	p.CreditQuotaNumerator = 10
	p.CreditQuotaDenominator = 1
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	f.Policy = p
	limit := creditLimit(t, f, 10, "economic-neartotal")
	draw := creditDraw(t, f, limit, 20, "economic-neartotal")
	creditSelfUse(t, f, draw, 1, "economic-first")
	assertCreditAccount(t, f.Owner.UserID, 1, 1, 0)
	creditSelfUse(t, f, draw, 1, "economic-zero-carry")
	assertCreditAccount(t, f.Owner.UserID, 1, 1, 0)
	require.Len(t, creditARs(t, f.Owner.UserID), 1, "zero carry is legitimate only after cumulative ceil has already charged")
	creditSelfUse(t, f, draw, 17, "economic-nineteen")
	assertCreditAccount(t, f.Owner.UserID, 0, 2, 0)
	result := creditExecute(t, f.Owner, "credit.release", AgentCreditCommand{RequestID: "economic-zero-release", ResourceID: draw.ID, Quota: 1})
	assert.Equal(t, "0", result.Resource.(map[string]any)["released_minor"])
	assertCreditAccount(t, f.Owner.UserID, 0, 2, 0)
	var rows []AgentFundEconomicLedger
	require.NoError(t, DB.Where("batch_id = ? AND kind = ?", draw.BatchID, "spend").Order("id").Find(&rows).Error)
	require.Len(t, rows, 3)
	assert.Equal(t, []int64{1, 0, 1}, []int64{rows[0].ChargedDeltaMinor, rows[1].ChargedDeltaMinor, rows[2].ChargedDeltaMinor})
	findings, err := ReconcileAgentCreditLiabilities(f.Owner, 0)
	require.NoError(t, err)
	require.Empty(t, findings)
	require.ErrorIs(t, DB.Model(&AgentFundEconomicLedger{}).Where("id > 0").Update("charged_delta_minor", 0).Error, ErrAgentLedgerImmutable)
}

func TestAgentFundEconomicsHistoricalRoundingExceptionPreservesBoundZeroPrincipalCustomerPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	limit := creditLimit(t, f, 100, "economic-legacy")
	draw := creditDraw(t, f, limit, 10, "economic-legacy")
	c := AgentDeliveryCommand{RequestID: "economic-legacy-bound", Name: "Protected buyer", Quota: 2}
	delivery, err := CreateAgentDelivery(prepaidProof(t, f.Owner, "delivery.create", c.RequestID, c), c)
	require.NoError(t, err)
	delivery, err = ClaimAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "economic-legacy-claim", ClaimCode: delivery.ClaimCode})
	require.NoError(t, err)
	// Recreate immutable facts from the previous interval-only implementation.
	// Old spend [0,1) costs 0; release priced intervals costs 7; already-bound
	// zero-cost [3,4),[6,7) remain owed. Never retroactively assign that lost 7.
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		for _, table := range []string{"agent_fund_economics", "agent_fund_economic_ledgers", "agent_fund_economic_anomalies", "agent_fund_slices"} {
			if err := tx.Exec("DELETE FROM "+table+" WHERE batch_id = ?", draw.BatchID).Error; err != nil {
				return err
			}
		}
		slices := []AgentFundSlice{
			{StartQuota: 0, Quota: 1, State: AgentSliceSpent},
			{StartQuota: 1, Quota: 2, State: AgentSliceReclaimed},
			{StartQuota: 3, Quota: 1, State: AgentSliceReserved, RedemptionID: delivery.RedemptionID},
			{StartQuota: 4, Quota: 2, State: AgentSliceReclaimed},
			{StartQuota: 6, Quota: 1, State: AgentSliceReserved, RedemptionID: delivery.RedemptionID},
			{StartQuota: 7, Quota: 3, State: AgentSliceReclaimed},
		}
		for i := range slices {
			slices[i].BatchID = draw.BatchID
			slices[i].AgentUserID = f.Owner.UserID
		}
		if err := tx.Create(&slices).Error; err != nil {
			return err
		}
		a, err := lockAgentCreditAccountTx(tx, f.Owner.UserID, "USD", 2, false)
		if err != nil {
			return err
		}
		if err := appendAgentCreditLedgerTx(tx, a, AgentCreditLedger{EventKey: "historical-economic-release", Kind: "release", DrawID: draw.ID, ReservedDelta: -7}); err != nil {
			return err
		}
		return tx.Model(&AgentRedemptionAccount{}).Where("agent_user_id = ?", f.Owner.UserID).Updates(map[string]any{"allocated_quota": 3, "available_quota": 0, "reserved_quota": 2, "spent_quota": 1}).Error
	}))
	for range 2 {
		require.NoError(t, MigrateAgentFundEconomicsWithDB(DB))
	}
	var before AgentFundEconomics
	require.NoError(t, DB.First(&before, "batch_id = ?", draw.BatchID).Error)
	assert.Equal(t, AgentFundEconomicLegacyException, before.Scheme)
	assert.Equal(t, []int64{0, 7}, []int64{before.ChargedMinor, before.ReleasedMinor})
	var n int64
	require.NoError(t, DB.Model(&AgentFundEconomicLedger{}).Where("batch_id = ?", draw.BatchID).Count(&n).Error)
	assert.EqualValues(t, 1, n, "repeat migration preserves a single baseline")
	next := AgentCreditCommand{RequestID: "economic-legacy-risk-block", Quota: 1, ExpectedLimitID: limit.ID}
	payload, _ := common.Marshal(next)
	_, err = PrepareAgentCreditOperation(f.Owner, "credit.draw", next.RequestID, payload)
	require.ErrorIs(t, err, ErrAgentCreditRisk)
	// The live customer confirmation works despite zero historical reserve.
	delivery, err = ConfirmAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "economic-legacy-confirm", DeliveryID: delivery.ID, Version: delivery.Version})
	require.NoError(t, err)
	assert.Equal(t, "redeemed", delivery.State)
	assert.Equal(t, 2, getUserBalances(t, f.Customer.UserID).QuotaReward)
	assertCreditAccount(t, f.Owner.UserID, 0, 0, 0)
	require.Empty(t, creditARs(t, f.Owner.UserID), "no invented historical debt")
	var after AgentFundEconomics
	require.NoError(t, DB.First(&after, "batch_id = ?", draw.BatchID).Error)
	assert.Equal(t, 3, after.SpentQuota)
	assert.Equal(t, before.ChargedMinor, after.ChargedMinor)
	assert.Equal(t, AgentFundEconomicLegacyException, after.Scheme)
	findings, err := ReconcileAgentCreditLiabilities(f.Owner, 0)
	require.NoError(t, err)
	require.NotEmpty(t, findings, "grandfathered loss is never reported clean")
	require.NoError(t, DB.Model(&AgentFundEconomicAnomaly{}).Where("batch_id = ?", draw.BatchID).Count(&n).Error)
	assert.EqualValues(t, 3, n, "baseline and both protected shortfall transitions remain immutable")
}

func TestAgentFundEconomicsConcurrentProtectedRedemptionsShareRoundingCarryPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	p := f.Policy
	p.ID = 0
	p.RequestID = "economic-concurrent-policy"
	p.CreditQuotaNumerator = 10
	p.CreditQuotaDenominator = 1
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	f.Policy = p
	limit := creditLimit(t, f, 10, "economic-concurrent")
	draw := creditDraw(t, f, limit, 20, "economic-concurrent")
	deliveries := make([]*AgentDelivery, 0, 2)
	for i := range 2 {
		suffix := strconv.Itoa(i)
		c := AgentDeliveryCommand{RequestID: "economic-parallel-create-" + suffix, Name: "Protected buyer", Quota: 1}
		d, err := CreateAgentDelivery(prepaidProof(t, f.Owner, "delivery.create", c.RequestID, c), c)
		require.NoError(t, err)
		d, err = ClaimAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "economic-parallel-claim-" + suffix, ClaimCode: d.ClaimCode})
		require.NoError(t, err)
		deliveries = append(deliveries, d)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range deliveries {
		wg.Go(func() {
			<-start
			d := deliveries[i]
			_, err := ConfirmAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "economic-parallel-confirm-" + strconv.Itoa(i), DeliveryID: d.ID, Version: d.Version})
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assertCreditAccount(t, f.Owner.UserID, 1, 1, 0)
	assert.Equal(t, 2, getUserBalances(t, f.Customer.UserID).QuotaReward)
	require.Len(t, creditARs(t, f.Owner.UserID), 1)
	creditExecute(t, f.Owner, "credit.release", AgentCreditCommand{RequestID: "economic-concurrent-release", ResourceID: draw.ID, Quota: 18})
	assertCreditAccount(t, f.Owner.UserID, 0, 1, 0)
	findings, err := ReconcileAgentCreditLiabilities(f.Owner, 0)
	require.NoError(t, err)
	require.Empty(t, findings)
}

func TestAgentFundEconomicsDoesNotFreezeHistoricalNoncashCommissionQuotaPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		return CreditAgentCommissionBatchTx(tx, f.Owner.UserID, "economic-commission-source", "USD", 7, 10, f.Policy.ID)
	}))
	var b AgentFundBatch
	require.NoError(t, DB.Where("source_kind = ?", AgentFundCommission).First(&b).Error)
	first := AgentProcurementSelfUseRequest{RequestID: "economic-commission-first", BatchID: b.ID, Quota: 1}
	result, err := AgentProcurementSelfUse(prepaidProof(t, f.Owner, "procurement.self_use", first.RequestID, first), first)
	require.NoError(t, err)
	assert.Equal(t, "0", result.CostMinor)
	// Noncash commission has no reclaim-to-credit or cash refund path. Its old
	// interval-zero history must not impose a new restriction on earned quota.
	for range 2 {
		require.NoError(t, MigrateAgentFundEconomicsWithDB(DB))
	}
	last := AgentProcurementSelfUseRequest{RequestID: "economic-commission-last", BatchID: b.ID, Quota: 9}
	result, err = AgentProcurementSelfUse(prepaidProof(t, f.Owner, "procurement.self_use", last.RequestID, last), last)
	require.NoError(t, err)
	assert.Equal(t, "7", result.CostMinor)
	assert.Equal(t, 10, getUserBalances(t, f.Owner.UserID).QuotaReward)
	assert.Zero(t, getUserBalances(t, f.Owner.UserID).QuotaPaid)
	refund := AgentPurchaseRefundCommand{RequestID: "economic-commission-no-cash", BatchID: b.ID, Quota: 1}
	_, err = PrepareAgentPurchaseRefundContext(f.Owner, "purchase.refund.request", refund)
	require.ErrorIs(t, err, ErrAgentRefundInvalid)
	var n int64
	require.NoError(t, DB.Model(&AgentFundEconomics{}).Where("batch_id = ?", b.ID).Count(&n).Error)
	assert.Zero(t, n)
	require.Empty(t, creditARs(t, f.Owner.UserID))
	assertPrepaidReconciled(t)
}

func TestAgentFundEconomicsMigrationPreservesHistoricalFeeAndApprovedQuoteExceptionsPostgres(t *testing.T) {
	for _, state := range []string{"succeeded", "approved", "payment_unknown"} {
		t.Run(state, func(t *testing.T) {
			f := setupAgentCreditTest(t)
			p := f.Policy
			p.ID = 0
			p.RequestID = "economic-historical-fee-policy"
			p.PurchaseQuotaNumerator = 1
			p.PurchaseQuotaDenominator = 1
			p.PurchaseRateBPS = 10000
			require.NoError(t, DB.Create(&p).Error)
			require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
			f.Policy = p
			order := prepaidCredit(t, f, 100, "economic-historical-fee")
			use := AgentProcurementSelfUseRequest{RequestID: "economic-historical-spend", BatchID: order.FundBatchID, Quota: 34}
			_, err := AgentProcurementSelfUse(prepaidProof(t, f.Owner, "procurement.self_use", use.RequestID, use), use)
			require.NoError(t, err)
			cmd := AgentPurchaseRefundCommand{RequestID: "economic-historical-refund", BatchID: order.FundBatchID, Quota: 33}
			refund, err := RequestAgentPurchaseRefund(prepaidProof(t, f.Owner, "purchase.refund.request", cmd.RequestID, cmd), cmd)
			require.NoError(t, err)
			// The previous interval fee charged floor(67*.03)-floor(34*.03)=1,
			// but the cumulative refund fee is floor(33*.03)=0. Neither an old
			// actual payment nor an approved/UNKNOWN responsibility may be repriced.
			require.NoError(t, DB.Exec("UPDATE agent_purchase_refunds SET fee_minor = '1', net_minor = '32', state = ? WHERE id = ?", state, refund.ID).Error)
			if state == "succeeded" {
				require.NoError(t, DB.Model(&AgentFundSlice{}).Where("refund_id = ?", refund.ID).Update("state", AgentSliceRefunded).Error)
				require.NoError(t, DB.Model(&AgentRedemptionAccount{}).Where("agent_user_id = ?", f.Owner.UserID).Updates(map[string]any{"allocated_quota": 67, "available_quota": 33, "reserved_quota": 0, "spent_quota": 34}).Error)
			}
			for _, table := range []string{"agent_fund_economics", "agent_fund_economic_ledgers"} {
				require.NoError(t, DB.Exec("DELETE FROM "+table+" WHERE batch_id = ?", order.FundBatchID).Error)
			}
			for range 2 {
				require.NoError(t, MigrateAgentFundEconomicsWithDB(DB))
			}
			var e AgentFundEconomics
			require.NoError(t, DB.First(&e, "batch_id = ?", order.FundBatchID).Error)
			assert.Equal(t, AgentFundEconomicLegacyException, e.Scheme)
			assert.EqualValues(t, 34, e.ChargedMinor)
			if state == "succeeded" {
				assert.EqualValues(t, 33, e.ReleasedMinor)
			} else {
				assert.Zero(t, e.ReleasedMinor)
			}
			var unchanged AgentPurchaseRefund
			require.NoError(t, DB.First(&unchanged, refund.ID).Error)
			assert.Equal(t, []string{state, "33", "1", "32"}, []string{unchanged.State, unchanged.PrincipalMinor, unchanged.FeeMinor, unchanged.NetMinor})
			next := AgentProcurementSelfUseRequest{RequestID: "economic-history-no-new", BatchID: order.FundBatchID, Quota: 1}
			payload, _ := common.Marshal(next)
			_, err = PrepareAgentPurchaseOperation(f.Owner, "procurement.self_use", next.RequestID, payload)
			require.ErrorIs(t, err, ErrAgentFundEconomicReview)
			assert.Equal(t, 34, getUserBalances(t, f.Owner.UserID).QuotaPaid, "migration cannot retrocharge the wallet")
		})
	}
}

func setupAgentEconomicLegacyInflightPair(t *testing.T) (prepaidFixture, *AgentPurchaseOrder, []AgentPurchaseRefund) {
	t.Helper()
	f := setupAgentCreditTest(t)
	p := f.Policy
	p.ID = 0
	p.RequestID = "economic-inflight-policy"
	p.PurchaseQuotaNumerator = 1
	p.PurchaseQuotaDenominator = 1
	p.PurchaseRateBPS = 7000
	p.RefundFeeBPS = 0
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	f.Policy = p
	order := prepaidCredit(t, f, 10, "economic-inflight")
	use := AgentProcurementSelfUseRequest{RequestID: "economic-inflight-old-spend", BatchID: order.FundBatchID, Quota: 1}
	_, err := AgentProcurementSelfUse(prepaidProof(t, f.Owner, "procurement.self_use", use.RequestID, use), use)
	require.NoError(t, err)
	var b AgentFundBatch
	require.NoError(t, DB.First(&b, order.FundBatchID).Error)
	var receipt AgentPaymentReceipt
	require.NoError(t, DB.First(&receipt, b.PaymentReceiptID).Error)
	refunds := []AgentPurchaseRefund{}
	for i := range 2 {
		r := AgentPurchaseRefund{RequestID: fmt.Sprintf("historical-inflight-%d", i), AgentUserID: f.Owner.UserID, BatchID: b.ID, PurchaseOrderID: b.PurchaseOrderID, PaymentReceiptID: b.PaymentReceiptID, Quota: 1, Currency: b.Currency, CurrencyExponent: b.CurrencyExponent, PrincipalMinor: "1", FeeMinor: "0", NetMinor: "1", PolicyVersion: b.PolicyVersion, PolicySnapshot: b.PolicySnapshot, State: "payment_unknown", Version: 4, BusinessKey: fmt.Sprintf("historical-fixed-business-%d", i), RequestedBy: f.Owner.UserID, ApprovedBy: f.Finance.UserID, SubmittedBy: f.Finance.UserID, SubmittedAt: common.GetTimestamp(), RecipientDigest: receipt.CounterpartyDigest, RecipientKind: "original_payer_dossier"}
		require.NoError(t, DB.Create(&r).Error)
		refunds = append(refunds, r)
	}
	require.NoError(t, DB.Exec("DELETE FROM agent_fund_slices WHERE batch_id = ? AND state = ?", b.ID, AgentSliceAvailable).Error)
	slices := []AgentFundSlice{{BatchID: b.ID, AgentUserID: b.AgentUserID, StartQuota: 1, Quota: 1, State: AgentSliceRefundFrozen, RefundID: refunds[0].ID}, {BatchID: b.ID, AgentUserID: b.AgentUserID, StartQuota: 2, Quota: 1, State: AgentSliceRefundFrozen, RefundID: refunds[1].ID}, {BatchID: b.ID, AgentUserID: b.AgentUserID, StartQuota: 3, Quota: 7, State: AgentSliceAvailable}}
	require.NoError(t, DB.Create(&slices).Error)
	require.NoError(t, DB.Model(&AgentRedemptionAccount{}).Where("agent_user_id = ?", f.Owner.UserID).Updates(map[string]any{"available_quota": 7, "reserved_quota": 2}).Error)
	for _, table := range []string{"agent_fund_economics", "agent_fund_economic_ledgers"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table+" WHERE batch_id = ?", b.ID).Error)
	}
	for range 2 {
		require.NoError(t, MigrateAgentFundEconomicsWithDB(DB))
	}
	var n int64
	require.NoError(t, DB.Model(&AgentFundLegacyRefundAuthority{}).Where("batch_id = ?", b.ID).Count(&n).Error)
	require.EqualValues(t, 2, n)
	return f, order, refunds
}

func TestAgentFundEconomicsLegacyMultipleInflightFinalizeOriginalMoneyInEitherOrderPostgres(t *testing.T) {
	for _, tc := range []struct {
		name   string
		order  []int
		failed int
	}{{"forward", []int{0, 1}, -1}, {"reverse", []int{1, 0}, -1}, {"success_then_unpaid", []int{1, 0}, 0}, {"unpaid_then_success", []int{0, 1}, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			f, order, refunds := setupAgentEconomicLegacyInflightPair(t)
			var before AgentFundEconomics
			require.NoError(t, DB.First(&before, "batch_id = ?", order.FundBatchID).Error)
			require.Equal(t, AgentFundEconomicLegacyException, before.Scheme)
			assert.Zero(t, before.ChargedMinor)
			successes := 0
			for _, i := range tc.order {
				r := refunds[i]
				suffix := fmt.Sprintf("economic-legacy-%s-%d", tc.name, i)
				cmd := AgentPurchaseRefundCommand{BusinessKey: r.BusinessKey, Outcome: "success", PayoutPaidAt: common.GetTimestamp(), EvidenceReference: "independent-verified-old-bank-payment", ExternalReference: "bank-" + suffix}
				if i == tc.failed {
					cmd.Outcome = "explicit_failed"
					cmd.PayoutPaidAt = 0
					cmd.NoPaymentConfirmed = true
					cmd.ExternalReference = ""
				} else {
					successes++
				}
				result := prepaidRefundAdvance(t, f, &r, "purchase.refund.resolve", suffix, cmd)
				if i == tc.failed {
					assert.Equal(t, "failed", result.State)
				} else {
					assert.Equal(t, "succeeded", result.State)
				}
				var e AgentFundEconomics
				require.NoError(t, DB.First(&e, "batch_id = ?", order.FundBatchID).Error)
				assert.Equal(t, successes, e.ReleasedQuota)
				assert.EqualValues(t, successes, e.ReleasedMinor)
				assert.Zero(t, e.ChargedMinor)
				assert.Equal(t, AgentFundEconomicLegacyException, e.Scheme)
			}
			var receipts []AgentOutboundPaymentReceipt
			require.NoError(t, DB.Where("owner_user_id = ? AND purpose = ?", f.Owner.UserID, "purchase_refund").Find(&receipts).Error)
			require.Len(t, receipts, successes)
			for _, r := range receipts {
				assert.EqualValues(t, 1, r.PaidMinor)
				assert.Equal(t, f.Reviewer.UserID, r.VerifiedBy)
				assert.NotEmpty(t, r.BeneficiaryDigest)
			}
			assert.Equal(t, 1, getUserBalances(t, f.Owner.UserID).QuotaPaid)
			var rows []AgentFundEconomicLedger
			require.NoError(t, DB.Where("batch_id = ? AND kind = ?", order.FundBatchID, "legacy_refund_finalize").Find(&rows).Error)
			assert.Len(t, rows, successes)
			findings, err := ReconcileAgentCreditLiabilities(f.Owner, 0)
			require.NoError(t, err)
			require.NotEmpty(t, findings, "historic loss remains visible")
			var b AgentFundBatch
			require.NoError(t, DB.First(&b, order.FundBatchID).Error)
			require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { _, err := PreviewAgentFundEconomicReleaseTx(tx, &b, 1); return err }), ErrAgentFundEconomicReview)
		})
	}
}

func TestAgentFundEconomicsLegacyInflightAuthorityRejectsMutatedObligationAndSourcesPostgres(t *testing.T) {
	f, _, refunds := setupAgentEconomicLegacyInflightPair(t)
	original := refunds[0]
	for _, tc := range []struct {
		column string
		value  any
	}{{"principal_minor", "2"}, {"fee_minor", "1"}, {"net_minor", "2"}, {"business_key", "new-bank-instruction"}, {"submitted_by", f.Reviewer.UserID}, {"recipient_digest", strings.Repeat("a", 64)}, {"quota", 2}, {"state", "approved"}} {
		t.Run(tc.column, func(t *testing.T) {
			require.NoError(t, DB.Exec("UPDATE agent_purchase_refunds SET "+tc.column+" = ? WHERE id = ?", tc.value, original.ID).Error)
			var changed AgentPurchaseRefund
			require.NoError(t, DB.First(&changed, original.ID).Error)
			require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAgentFundRefundQuoteTx(tx, &changed, true) }), ErrAgentFundEconomicReview)
			require.NoError(t, DB.Model(&AgentPurchaseRefund{}).Where("id = ?", original.ID).Select("principal_minor", "fee_minor", "net_minor", "business_key", "submitted_by", "recipient_digest", "quota", "state").Updates(&original).Error)
		})
	}
	var source AgentFundSlice
	require.NoError(t, DB.Where("refund_id = ?", original.ID).First(&source).Error)
	require.NoError(t, DB.Model(&source).Update("state", AgentSliceAvailable).Error)
	require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAgentFundRefundQuoteTx(tx, &original, true) }), ErrAgentFundEconomicReview)
	require.NoError(t, DB.Model(&source).Update("state", AgentSliceRefundFrozen).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAgentFundRefundQuoteTx(tx, &original, true) }))
	require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAgentFundRefundQuoteTx(tx, &original, false) }), ErrAgentFundEconomicReview)
	require.ErrorIs(t, DB.Model(&AgentFundLegacyRefundAuthority{}).Where("refund_id = ?", original.ID).Update("principal_minor", 2).Error, ErrAgentLedgerImmutable)
}
