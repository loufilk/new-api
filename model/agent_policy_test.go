package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentPolicyDefaultsDoNotEnableNewCashRights(t *testing.T) {
	policy := AgentPolicy{Reason: "Keep new financial products disabled"}
	require.NoError(t, ValidateAgentPolicy(&policy))
	assert.False(t, policy.PurchaseEnabled)
	assert.False(t, policy.PurchaseRefundEnabled)
	assert.False(t, policy.CommissionEnabled)
	assert.False(t, policy.WithdrawalEnabled)
	assert.False(t, policy.CommissionConversionEnabled)
	for _, change := range []func(*AgentPolicy){
		func(p *AgentPolicy) { p.PurchaseEnabled = true },
		func(p *AgentPolicy) { p.LegacyPurchaseReclassificationEnabled = true },
		func(p *AgentPolicy) { p.PurchaseRefundEnabled = true },
		func(p *AgentPolicy) { p.CommissionEnabled = true },
		func(p *AgentPolicy) { p.WithdrawalEnabled = true },
		func(p *AgentPolicy) { p.CommissionConversionEnabled = true },
	} {
		candidate := policy
		change(&candidate)
		assert.ErrorIs(t, ValidateAgentPolicy(&candidate), ErrAgentPolicyInvalid, "each new product requires explicit risk-policy approval")
	}
}

func TestAgentPolicyValidatesPurchaseAndSettlementIndependently(t *testing.T) {
	purchase := AgentPolicy{Reason: "Synthetic test policy", RiskPolicyApproved: true, PurchaseEnabled: true, PurchaseRefundTermsApproved: true, PurchaseCurrency: "USD", PurchaseCurrencyExponent: 2, PurchaseRateBPS: 10000, PurchaseQuotaNumerator: 100, PurchaseQuotaDenominator: 1, PurchaseMinimumQuota: 100, PurchaseMaximumQuota: 1000, QuoteLifetimeSeconds: 300}
	require.NoError(t, ValidateAgentPolicy(&purchase))
	assert.False(t, purchase.WithdrawalEnabled)
	for _, change := range []func(*AgentPolicy){
		func(p *AgentPolicy) { p.PurchaseRateBPS = 0 },
		func(p *AgentPolicy) { p.PurchaseQuotaDenominator = 0 },
		func(p *AgentPolicy) { p.PurchaseCurrency = "usd" },
		func(p *AgentPolicy) { p.PurchaseCurrencyExponent = 7 },
		func(p *AgentPolicy) { p.PurchaseCurrencyExponent = 6 },
		func(p *AgentPolicy) { p.PurchaseCurrency = "ZZZ" },
		func(p *AgentPolicy) { p.PurchaseMaximumQuota = 99 },
		func(p *AgentPolicy) { p.RefundFeeBPS = 10001 },
		func(p *AgentPolicy) { p.CommissionReserveBPS = -1 },
		func(p *AgentPolicy) { p.QuoteLifetimeSeconds = 0 },
	} {
		candidate := purchase
		change(&candidate)
		assert.ErrorIs(t, ValidateAgentPolicy(&candidate), ErrAgentPolicyInvalid)
	}
	cash := AgentPolicy{Reason: "Synthetic test settlement policy", RiskPolicyApproved: true, CommissionCurrency: "JPY", CommissionCurrencyExponent: 0, WithdrawalEnabled: true, WithdrawalMinimumMinor: 100, WithdrawalCalendarMonthLimit: 1}
	require.NoError(t, ValidateAgentPolicy(&cash))
	cash.CommissionConversionEnabled = true
	assert.ErrorIs(t, ValidateAgentPolicy(&cash), ErrAgentPolicyInvalid, "conversion needs its own explicit locked ratio")
	cash.ConversionQuotaNumerator = 5
	cash.ConversionQuotaDenominator = 2
	require.NoError(t, ValidateAgentPolicy(&cash))
}

func TestAgentMoneyRatioPreservesExactMinorUnitsAndOverflowBoundary(t *testing.T) {
	for _, tc := range []struct {
		amount, numerator, denominator int64
		ceil                           bool
		want                           int64
	}{
		{5, 3, 2, false, 7}, {5, 3, 2, true, 8}, {0, 3, 2, true, 0},
		{int64(common.MaxWalletQuota), int64(common.MaxWalletQuota), int64(common.MaxWalletQuota), false, int64(common.MaxWalletQuota)},
		{100, 1, 10000, false, 0}, {100, 1, 10000, true, 1},
	} {
		actual, err := AgentMoneyRatio(tc.amount, tc.numerator, tc.denominator, tc.ceil)
		require.NoError(t, err)
		assert.Equal(t, tc.want, actual)
	}
	_, err := AgentMoneyRatio(int64(common.MaxWalletQuota), 2, 1, false)
	assert.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	for _, tc := range [][3]int64{{-1, 1, 1}, {1, -1, 1}, {1, 1, 0}, {1, 1, -1}} {
		_, err := AgentMoneyRatio(tc[0], tc[1], tc[2], false)
		assert.ErrorIs(t, err, ErrAgentPolicyInvalid)
	}
}

func preparePolicyPublicationTest(t *testing.T, identity AuthSessionIdentity, request AgentPolicyPublishRequest) AgentTransactionAuthorization {
	t.Helper()
	payload, err := common.Marshal(request)
	require.NoError(t, err)
	prepared, err := PrepareAgentPolicyOperation(identity, "policy.publish", request.RequestID, payload)
	require.NoError(t, err)
	_, auth := issueAgentProfileTestProof(t, identity, prepared.VerificationContext)
	return auth
}

func TestAgentPolicyPublicationSnapshotCutoverAndExactReplayPostgres(t *testing.T) {
	_, _, identity, other := setupAgentProfileSecurityTest(t)
	initial := AgentPolicyPublishRequest{RequestID: "policy-initial-request", Policy: AgentPolicy{Reason: "Synthetic commissioning", RiskPolicyApproved: true, CommissionEnabled: true, CommissionCurrency: "USD", CommissionCurrencyExponent: 2, CommissionUserRateBPS: 200, CommissionReferralCapMinor: 500}}
	auth := preparePolicyPublicationTest(t, identity, initial)
	first, replayed, err := PublishAgentPolicy(identity, initial, auth)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Positive(t, first.CommissionCutoverAt)
	assert.Equal(t, identity.UserID, first.CreatedBy)
	disabled := AgentPolicyPublishRequest{RequestID: "policy-disabled-request", ExpectedVersion: first.ID, Policy: AgentPolicy{Reason: "Disable new accrual only"}}
	next, _, err := PublishAgentPolicy(identity, disabled, preparePolicyPublicationTest(t, identity, disabled))
	require.NoError(t, err)
	assert.Equal(t, first.CommissionCutoverAt, next.CommissionCutoverAt, "switching off cannot reset prior qualifying history")
	again, replayed, err := PublishAgentPolicy(identity, initial, auth)
	require.NoError(t, err)
	require.True(t, replayed)
	assert.Equal(t, first.ID, again.ID)
	current, err := GetCurrentAgentPolicy()
	require.NoError(t, err)
	assert.Equal(t, next.ID, current.ID, "old successful replay must not restore old current pointer")
	altered := initial
	altered.Policy.Reason = "Different terms under same request"
	_, _, err = PublishAgentPolicy(identity, altered, auth)
	assert.ErrorIs(t, err, ErrAgentQuotaConflict)
	status, err := FindAgentPolicyOperation(other, initial.RequestID)
	require.NoError(t, err)
	assert.Nil(t, status, "another user cannot recover private policy request")
	enable := initial
	enable.RequestID = "policy-reenable-request"
	enable.ExpectedVersion = next.ID
	third, _, err := PublishAgentPolicy(identity, enable, preparePolicyPublicationTest(t, identity, enable))
	require.NoError(t, err)
	assert.Equal(t, first.CommissionCutoverAt, third.CommissionCutoverAt)
	assert.ErrorIs(t, DB.Model(first).Update("commission_referral_cap_minor", 1).Error, ErrAgentLedgerImmutable)
	assert.ErrorIs(t, DB.Delete(first).Error, ErrAgentLedgerImmutable)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", identity.UserID).Update("role", common.RoleCommonUser).Error)
	status, err = FindAgentPolicyOperation(identity, initial.RequestID)
	assert.ErrorIs(t, err, ErrAgentProfilePermission)
	assert.Nil(t, status, "former root cannot recover confidential policy audit through status endpoint")
}

func TestAgentPolicyConcurrentPublicationHasOneWinnerPostgres(t *testing.T) {
	_, _, identity, _ := setupAgentProfileSecurityTest(t)
	requests := []AgentPolicyPublishRequest{
		{RequestID: "policy-concurrent-one", Policy: AgentPolicy{Reason: "Synthetic one"}},
		{RequestID: "policy-concurrent-two", Policy: AgentPolicy{Reason: "Synthetic two"}},
	}
	auths := []AgentTransactionAuthorization{preparePolicyPublicationTest(t, identity, requests[0]), preparePolicyPublicationTest(t, identity, requests[1])}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := range requests {
		go func(index int) {
			<-start
			_, _, err := PublishAgentPolicy(identity, requests[index], auths[index])
			results <- err
		}(i)
	}
	close(start)
	wins, stale := 0, 0
	for range requests {
		err := <-results
		if err == nil {
			wins++
		} else {
			require.ErrorIs(t, err, ErrAgentPolicyChanged)
			stale++
		}
	}
	assert.Equal(t, 1, wins)
	assert.Equal(t, 1, stale)
	var count int64
	require.NoError(t, DB.Model(&AgentPolicy{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestAgentPolicyRejectsUnknownFieldsAndUnprivilegedPublicationPostgres(t *testing.T) {
	_, _, root, user := setupAgentProfileSecurityTest(t)
	_, err := PrepareAgentPolicyOperation(root, "policy.publish", "policy-strict-request", []byte(`{"expected_version":0,"policy":{"reason":"Synthetic","unknown_cash_switch":true}}`))
	assert.ErrorIs(t, err, ErrAgentPolicyInvalid)
	request := AgentPolicyPublishRequest{RequestID: "policy-user-request", Policy: AgentPolicy{Reason: "Synthetic"}}
	payload, err := common.Marshal(request)
	require.NoError(t, err)
	_, err = PrepareAgentPolicyOperation(user, "policy.publish", request.RequestID, payload)
	assert.ErrorIs(t, err, ErrAgentProfilePermission)
}
