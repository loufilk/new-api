package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentExpansionPolicyRequiresExplicitNonInterestTerms(t *testing.T) {
	base := AgentPolicy{Reason: "No expansion products enabled"}
	require.NoError(t, ValidateAgentPolicy(&base))
	for _, enabled := range []bool{base.CreditEnabled, base.CreditSelfUseEnabled, base.DepositEnabled, base.DepositOffsetEnabled, base.DepositRefundEnabled, base.CampaignEnabled, base.AgreementPricingEnabled, base.ManualReconciliationEnabled, base.GovernanceEnabled, base.GovernanceRequiredForActivation, base.CreditGovernanceRequired} {
		assert.False(t, enabled)
	}
	credit := AgentPolicy{Reason: "Explicit synthetic non-interest credit", RiskPolicyApproved: true, CreditEnabled: true, CreditTermsApproved: true, CreditRecognitionMode: "source_spend", CreditInterestMode: "none", CreditCurrency: "USD", CreditCurrencyExponent: 2, CreditMaximumLimitMinor: 10000, CreditTermSeconds: 86400, CreditMaximumTermSeconds: 30 * 86400, CreditQuotaNumerator: 100, CreditQuotaDenominator: 1, CreditRateBPS: 10000}
	require.NoError(t, ValidateAgentPolicy(&credit))
	for name, mutate := range map[string]func(*AgentPolicy){
		"risk":                   func(p *AgentPolicy) { p.RiskPolicyApproved = false },
		"terms":                  func(p *AgentPolicy) { p.CreditTermsApproved = false },
		"recognition missing":    func(p *AgentPolicy) { p.CreditRecognitionMode = "" },
		"issue is not spend":     func(p *AgentPolicy) { p.CreditRecognitionMode = "code_issue" },
		"interest missing":       func(p *AgentPolicy) { p.CreditInterestMode = "" },
		"compound not supported": func(p *AgentPolicy) { p.CreditInterestMode = "compound" },
		"rate overflow":          func(p *AgentPolicy) { p.CreditRateBPS = 10001 },
		"term overflow":          func(p *AgentPolicy) { p.CreditTermSeconds = p.CreditMaximumTermSeconds + 1 },
		"currency":               func(p *AgentPolicy) { p.CreditCurrency = "ZZZ" },
		"currency exponent":      func(p *AgentPolicy) { p.CreditCurrencyExponent = 0 },
		"amount overflow":        func(p *AgentPolicy) { p.CreditMaximumLimitMinor = int64(common.MaxWalletQuota) + 1 },
		"no divisor":             func(p *AgentPolicy) { p.CreditQuotaDenominator = 0 },
		"disabled self-use":      func(p *AgentPolicy) { p.CreditEnabled = false; p.CreditSelfUseEnabled = true },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := credit
			mutate(&candidate)
			assert.ErrorIs(t, ValidateAgentPolicy(&candidate), ErrAgentPolicyInvalid)
		})
	}
	deposit := AgentPolicy{Reason: "Explicit independent synthetic liability", RiskPolicyApproved: true, DepositEnabled: true, DepositTermsApproved: true, DepositCurrency: "USD", DepositCurrencyExponent: 2, DepositMaximumMinor: 10000}
	require.NoError(t, ValidateAgentPolicy(&deposit))
	assert.False(t, deposit.DepositOffsetEnabled, "receiving a deposit does not authorize offset")
	assert.False(t, deposit.DepositRefundEnabled, "receiving a deposit does not activate new refund instructions")
	for _, mutate := range []func(*AgentPolicy){func(p *AgentPolicy) { p.DepositTermsApproved = false }, func(p *AgentPolicy) { p.DepositEnabled = false; p.DepositOffsetEnabled = true }, func(p *AgentPolicy) { p.DepositEnabled = false; p.DepositRefundEnabled = true }, func(p *AgentPolicy) { p.DepositMaximumMinor = 0 }} {
		candidate := deposit
		mutate(&candidate)
		assert.ErrorIs(t, ValidateAgentPolicy(&candidate), ErrAgentPolicyInvalid)
	}
}

func TestAgentExpansionPolicyControlsAreIndependent(t *testing.T) {
	for name, mutate := range map[string]func(*AgentPolicy){
		"campaign without terms":                   func(p *AgentPolicy) { p.CampaignEnabled = true },
		"agreement without purchase":               func(p *AgentPolicy) { p.AgreementPricingEnabled = true; p.AgreementMinimumRateBPS = 8800 },
		"agreement without floor":                  func(p *AgentPolicy) { p.AgreementPricingEnabled = true },
		"governance activation without enrollment": func(p *AgentPolicy) { p.GovernanceRequiredForActivation = true },
		"credit governance without enrollment":     func(p *AgentPolicy) { p.CreditGovernanceRequired = true },
		"negative inactivity":                      func(p *AgentPolicy) { p.GovernanceInactivitySeconds = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := AgentPolicy{Reason: "Independent expansion terms", RiskPolicyApproved: true}
			mutate(&candidate)
			assert.ErrorIs(t, ValidateAgentPolicy(&candidate), ErrAgentPolicyInvalid)
		})
	}
	for _, mutate := range []func(*AgentPolicy){func(p *AgentPolicy) { p.CampaignEnabled = true; p.CampaignTermsApproved = true }, func(p *AgentPolicy) { p.ManualReconciliationEnabled = true }, func(p *AgentPolicy) { p.GovernanceEnabled = true }} {
		candidate := AgentPolicy{Reason: "Explicit non-cash product", RiskPolicyApproved: true}
		mutate(&candidate)
		require.NoError(t, ValidateAgentPolicy(&candidate))
		assert.False(t, candidate.WithdrawalEnabled)
		assert.False(t, candidate.CreditEnabled)
		assert.False(t, candidate.DepositOffsetEnabled)
		candidate.RiskPolicyApproved = false
		assert.ErrorIs(t, ValidateAgentPolicy(&candidate), ErrAgentPolicyInvalid)
	}
}

func TestAgentExpansionTransactionActionsHaveNarrowAuthority(t *testing.T) {
	for _, action := range []string{"campaign.propose", "governance.override"} {
		rule, ok := agentTransactionActionRule(action)
		require.True(t, ok)
		assert.True(t, rule.root)
		assert.True(t, rule.administrator)
	}
	for _, action := range []string{"credit.limit.approve", "credit.repayment.confirm", "deposit.receipt.confirm", "deposit.refund.resolve", "agreement.approve", "statement.import", "statement.evidence.propose", "statement.evidence.confirm", "statement.evidence.reject", "statement.period.close", "campaign.review.approve", "governance.review"} {
		rule, ok := agentTransactionActionRule(action)
		require.True(t, ok)
		assert.True(t, rule.administrator)
		assert.Equal(t, AgentCapabilityAdmin, rule.capability)
	}
	for _, action := range []string{"credit.draw", "credit.limit.request"} {
		rule, ok := agentTransactionActionRule(action)
		require.True(t, ok)
		assert.Equal(t, AgentCapabilitySales, rule.capability)
		assert.False(t, rule.optionalProfile)
	}
	for _, action := range []string{"credit.release", "deposit.offset.request", "deposit.refund.request"} {
		rule, ok := agentTransactionActionRule(action)
		require.True(t, ok)
		assert.Equal(t, AgentCapabilitySettlement, rule.capability)
	}
	for _, action := range []string{"credit.auto.approve", "deposit.auto.offset", "statement.evidence.override", "payout.execute", "site.connect", "campaign.cash", "governance.activate"} {
		_, ok := agentTransactionActionRule(action)
		assert.False(t, ok, action)
	}
}
