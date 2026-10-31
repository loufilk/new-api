package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgreementStatementTest(t *testing.T) prepaidFixture {
	t.Helper()
	f := setupPrepaidTest(t)
	for _, models := range [][]any{AgentAgreementModels(), AgentStatementModels()} {
		require.NoError(t, DB.AutoMigrate(models...))
		for _, m := range models {
			st := &gorm.Statement{DB: DB}
			require.NoError(t, st.Parse(m))
			require.NoError(t, DB.Exec("DELETE FROM "+st.Schema.Table).Error)
		}
	}
	t.Cleanup(func() {
		for _, models := range [][]any{AgentAgreementModels(), AgentStatementModels()} {
			for _, m := range models {
				st := &gorm.Statement{DB: DB}
				require.NoError(t, st.Parse(m))
				require.NoError(t, DB.Exec("DELETE FROM "+st.Schema.Table).Error)
			}
		}
	})
	p := f.Policy
	p.ID = 0
	p.RequestID = "agreement-statement-test-policy"
	p.AgreementPricingEnabled = true
	p.AgreementMinimumRateBPS = 1
	p.ManualReconciliationEnabled = true
	require.NoError(t, ValidateAgentPolicy(&p))
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	f.Policy = p
	return f
}
func agreementStatementProof(t *testing.T, identity AuthSessionIdentity, action, requestID string, command any) AgentTransactionAuthorization {
	t.Helper()
	payload, err := common.Marshal(command)
	require.NoError(t, err)
	var prepared *AgentPreparedOperation
	if len(action) > 10 && action[:10] == "agreement." {
		prepared, err = PrepareAgentAgreementOperation(identity, action, requestID, payload)
	} else {
		prepared, err = PrepareAgentStatementOperation(identity, action, requestID, payload)
	}
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
func testAgreementTerms(owner int, key string) AgentAgreementTerms {
	scope := "agreement"
	if owner == 0 {
		scope = "tier"
	}
	now := common.GetTimestamp()
	return AgentAgreementTerms{AgreementKey: key, TermsVersion: 1, Scope: scope, AgentUserID: owner, Currency: "USD", CurrencyExponent: 2, QuotaNumerator: 10, QuotaDenominator: 1, BaseRateBPS: 9000, EffectiveFrom: now - 60, EffectiveUntil: now + 3600, DossierReference: "contract:" + key, Tiers: []AgentAgreementTier{{Key: "large", Basis: "quota", Minimum: 1000, Maximum: 2000, RateBPS: 8800}}}
}
func proposeTestAgreement(t *testing.T, f prepaidFixture, terms AgentAgreementTerms) *AgentAgreementView {
	t.Helper()
	c := AgentAgreementCommand{RequestID: "propose-" + terms.AgreementKey, Reason: "Synthetic independently reviewed terms", Terms: terms}
	auth := agreementStatementProof(t, f.Finance, "agreement.propose", c.RequestID, c)
	v, err := ExecuteAgentAgreement(auth, "agreement.propose", c)
	require.NoError(t, err)
	return v
}
func approveTestAgreement(t *testing.T, f prepaidFixture, v *AgentAgreementView) *AgentAgreementView {
	t.Helper()
	c := AgentAgreementCommand{RequestID: "approve-" + v.AgreementKey, AgreementID: v.ID, ExpectedVersion: v.Approval.Version, Reason: "Synthetic independent commercial approval"}
	auth := agreementStatementProof(t, f.Reviewer, "agreement.approve", c.RequestID, c)
	v, err := ExecuteAgentAgreement(auth, "agreement.approve", c)
	require.NoError(t, err)
	return v
}
func TestAgentAgreementThresholdValidationAndExactRounding(t *testing.T) {
	terms := testAgreementTerms(1, "terms-boundaries")
	require.NoError(t, ValidateAgentAgreementTerms(terms))
	for _, tc := range []struct {
		name   string
		change func(*AgentAgreementTerms)
	}{
		{"overlap", func(t *AgentAgreementTerms) {
			t.Tiers = append(t.Tiers, AgentAgreementTier{Key: "overlap", Basis: "quota", Minimum: 1999, Maximum: 3000, RateBPS: 8000})
		}},
		{"mixed-basis", func(t *AgentAgreementTerms) {
			t.Tiers = append(t.Tiers, AgentAgreementTier{Key: "other", Basis: "face_minor", Minimum: 2000, Maximum: 3000, RateBPS: 8000})
		}},
		{"unknown-currency", func(t *AgentAgreementTerms) { t.Currency = "ZZZ" }},
		{"unknown-actual-paid-semantics", func(t *AgentAgreementTerms) { t.Tiers[0].Basis = "actual_paid_minor" }},
		{"zero-ratio", func(t *AgentAgreementTerms) { t.QuotaNumerator = 0 }},
		{"bad-window", func(t *AgentAgreementTerms) { t.EffectiveUntil = t.EffectiveFrom }},
		{"zero-rate", func(t *AgentAgreementTerms) { t.BaseRateBPS = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := terms
			copy.Tiers = append([]AgentAgreementTier(nil), terms.Tiers...)
			tc.change(&copy)
			require.Error(t, ValidateAgentAgreementTerms(copy))
		})
	}
	// One final ceiling, not ceil(face) followed by another discount ceiling.
	got, err := agentPriceMinor(11, 10, 1, 8800)
	require.NoError(t, err)
	assert.EqualValues(t, 1, got)
	_, err = agentPriceMinor(common.MaxWalletQuota, 1, int64(common.MaxWalletQuota), 10000)
	require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
}
func TestAgentAgreementPrecedenceWindowsSnapshotsAndStaleQuotesPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	global := approveTestAgreement(t, f, proposeTestAgreement(t, f, testAgreementTerms(0, "global-tier")))
	for _, tc := range []struct {
		quota     int
		rate      int64
		paid, hit string
	}{{999, 9000, "90", "global_tier_base"}, {1000, 8800, "88", "global_tier"}, {1999, 8800, "176", "global_tier"}, {2000, 9000, "180", "global_tier_base"}} {
		price, err := ResolveAgentPurchasePriceTx(DB, &f.Policy, f.Owner.UserID, tc.quota, common.GetTimestamp())
		require.NoError(t, err)
		assert.Equal(t, tc.rate, price.RateBPS)
		assert.Equal(t, tc.paid, price.PaidMinor)
		assert.Equal(t, tc.hit, price.HitReason)
	}
	before, err := ResolveAgentPurchasePriceTx(DB, &f.Policy, f.Owner.UserID, 1000, global.EffectiveFrom-1)
	require.NoError(t, err)
	assert.Zero(t, before.AgreementID)
	after, err := ResolveAgentPurchasePriceTx(DB, &f.Policy, f.Owner.UserID, 1000, global.EffectiveUntil)
	require.NoError(t, err)
	assert.Zero(t, after.AgreementID)
	stale, err := CreateAgentPurchaseQuote(f.Owner, AgentPurchaseQuoteRequest{RequestID: "before-personal", Quota: 1000})
	require.NoError(t, err)
	terms := testAgreementTerms(f.Owner.UserID, "personal-tier")
	terms.BaseRateBPS = 8000
	terms.Tiers[0].RateBPS = 7500
	personal := approveTestAgreement(t, f, proposeTestAgreement(t, f, terms))
	_, err = CreateAgentPurchaseOrder(f.Owner, AgentPurchaseCreateRequest{RequestID: "stale-personal", QuoteID: stale.ID})
	require.ErrorIs(t, err, ErrAgentAgreementConflict)
	quote, err := CreateAgentPurchaseQuote(f.Owner, AgentPurchaseQuoteRequest{RequestID: "personal-price", Quota: 1000})
	require.NoError(t, err)
	assert.Equal(t, "75", quote.PaidMinor)
	assert.EqualValues(t, 7500, quote.RateBPS)
	order, err := CreateAgentPurchaseOrder(f.Owner, AgentPurchaseCreateRequest{RequestID: "personal-order", QuoteID: quote.ID})
	require.NoError(t, err)
	var snap AgentPriceSnapshot
	require.NoError(t, common.UnmarshalJsonStr(order.PriceSnapshot, &snap))
	assert.Equal(t, personal.ID, snap.AgreementID)
	assert.EqualValues(t, 1, snap.TierVersion)
	assert.Equal(t, "personal_agreement_tier", snap.HitReason)
	c := AgentAgreementCommand{RequestID: "revoke-personal", AgreementID: personal.ID, ExpectedVersion: personal.Approval.Version, Reason: "Synthetic contract revoked prospectively"}
	auth := agreementStatementProof(t, f.Finance, "agreement.revoke", c.RequestID, c)
	_, err = ExecuteAgentAgreement(auth, "agreement.revoke", c)
	require.NoError(t, err)
	fresh, err := ResolveAgentPurchasePriceTx(DB, &f.Policy, f.Owner.UserID, 1000, common.GetTimestamp())
	require.NoError(t, err)
	assert.Equal(t, "88", fresh.PaidMinor)
	require.NoError(t, DB.First(order, order.ID).Error)
	assert.Equal(t, "75", order.PaidMinor)
	assert.Equal(t, quote.PriceSnapshot, order.PriceSnapshot)
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Update("version", 2).Error)
	_, err = CreateAgentPurchaseOrder(f.Owner, AgentPurchaseCreateRequest{RequestID: "profile-stale", QuoteID: quote.ID})
	require.Error(t, err)
}
func TestAgentAgreementIndependentReviewConflictPolicyProfileAndReplayPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	v := proposeTestAgreement(t, f, testAgreementTerms(f.Owner.UserID, "review-one"))
	c := AgentAgreementCommand{RequestID: "self-review", AgreementID: v.ID, ExpectedVersion: 1, Reason: "Synthetic review attempt"}
	auth := agreementStatementProof(t, f.Finance, "agreement.approve", c.RequestID, c)
	_, err := ExecuteAgentAgreement(auth, "agreement.approve", c)
	require.ErrorIs(t, err, ErrAgentAgreementReview)
	v = approveTestAgreement(t, f, v)
	other := proposeTestAgreement(t, f, testAgreementTerms(f.Owner.UserID, "review-overlap"))
	c = AgentAgreementCommand{RequestID: "overlap-review", AgreementID: other.ID, ExpectedVersion: 1, Reason: "Synthetic overlapping review"}
	auth = agreementStatementProof(t, f.Reviewer, "agreement.approve", c.RequestID, c)
	_, err = ExecuteAgentAgreement(auth, "agreement.approve", c)
	require.ErrorIs(t, err, ErrAgentAgreementConflict)
	// Approval proof binds the live subject profile; a profile change cannot
	// revive old terms even when the currently approved agreement is revoked.
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Update("version", 2).Error)
	_, err = ExecuteAgentAgreement(auth, "agreement.approve", c)
	require.Error(t, err)
	c = AgentAgreementCommand{RequestID: "approve-" + v.AgreementKey, AgreementID: v.ID, ExpectedVersion: 1, Reason: "Synthetic independent commercial approval"}
	payload, err := common.Marshal(c)
	require.NoError(t, err)
	_, found, err := ReplayAgentAgreementOperation(f.Reviewer, "agreement.approve", c.RequestID, payload)
	require.NoError(t, err)
	assert.True(t, found)
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", f.Reviewer.SessionID).Update("status", UserSessionStatusRevoked).Error)
	_, _, err = ReplayAgentAgreementOperation(f.Reviewer, "agreement.approve", c.RequestID, payload)
	require.Error(t, err)
	_, err = GetAgentAgreement(f.Owner, v.ID)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
}
func TestAgentAgreementBasePaidThresholdNonCircularAndMigrationTwicePostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	terms := testAgreementTerms(f.Owner.UserID, "paid-basis")
	terms.Tiers = []AgentAgreementTier{{Key: "paid-threshold", Basis: "base_paid_minor", Minimum: 90, Maximum: 180, RateBPS: 8000}}
	approveTestAgreement(t, f, proposeTestAgreement(t, f, terms))
	for _, tc := range []struct {
		q    int
		rate int64
		paid string
	}{{989, 8000, "80"}, {1000, 8000, "80"}, {1989, 9000, "180"}, {2000, 9000, "180"}} {
		price, err := ResolveAgentPurchasePriceTx(DB, &f.Policy, f.Owner.UserID, tc.q, common.GetTimestamp())
		require.NoError(t, err)

		assert.Equal(t, tc.rate, price.RateBPS)
		assert.Equal(t, tc.paid, price.PaidMinor)
	}
	quote, err := CreateAgentPurchaseQuote(f.Owner, AgentPurchaseQuoteRequest{RequestID: "migration-quote", Quota: 1000})
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(AgentAgreementModels()...))
		require.NoError(t, DB.AutoMigrate(AgentStatementModels()...))
		require.NoError(t, DB.AutoMigrate(&AgentPurchaseQuote{}, &AgentPurchaseOrder{}))
	}
	var preserved AgentPurchaseQuote
	require.NoError(t, DB.First(&preserved, quote.ID).Error)
	assert.Equal(t, quote.PriceSnapshot, preserved.PriceSnapshot)
	p := f.Policy
	p.ID = 0
	p.RequestID = "changed-pricing-policy"
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	_, err = CreateAgentPurchaseOrder(f.Owner, AgentPurchaseCreateRequest{RequestID: "old-policy-order", QuoteID: quote.ID})
	require.ErrorIs(t, err, ErrAgentPolicyChanged)
}

func TestAgentAgreementRootRateFloorCannotBeBypassedByUnitsPostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	p := f.Policy
	p.AgreementMinimumRateBPS = 8000
	terms := testAgreementTerms(f.Owner.UserID, "minimum-rate-test")
	require.NoError(t, validateAgentAgreementPolicy(terms, &p))
	terms.Tiers[0].RateBPS = 7999
	require.ErrorIs(t, validateAgentAgreementPolicy(terms, &p), ErrAgentAgreementInvalid)
	terms.Tiers[0].RateBPS = 8800
	terms.QuotaNumerator = 100
	require.ErrorIs(t, validateAgentAgreementPolicy(terms, &p), ErrAgentAgreementInvalid)
	terms.QuotaNumerator = 20
	terms.QuotaDenominator = 2
	require.NoError(t, validateAgentAgreementPolicy(terms, &p), "equivalent approved units are accepted exactly")
	terms.BaseRateBPS = 7999
	require.ErrorIs(t, validateAgentAgreementPolicy(terms, &p), ErrAgentAgreementInvalid)
}

func TestAgentAgreementApprovalInvalidatedByCurrentPolicyAndProfilePostgres(t *testing.T) {
	f := setupAgreementStatementTest(t)
	pending := proposeTestAgreement(t, f, testAgreementTerms(f.Owner.UserID, "profile-stale-terms"))
	require.NoError(t, DB.Model(&AgentProfile{}).Where("user_id = ?", f.Owner.UserID).Update("version", 2).Error)
	c := AgentAgreementCommand{RequestID: "profile-stale-approval", AgreementID: pending.ID, ExpectedVersion: 1, Reason: "Synthetic stale profile must be reapproved"}
	auth := agreementStatementProof(t, f.Reviewer, "agreement.approve", c.RequestID, c)
	_, err := ExecuteAgentAgreement(auth, "agreement.approve", c)
	require.ErrorIs(t, err, ErrAgentProfileConflict)
	// A new revision under current profile may be approved; obsolete-profile
	// agreements cannot block correctly reapproved replacement terms forever.
	current := proposeTestAgreement(t, f, testAgreementTerms(f.Owner.UserID, "profile-current-terms"))
	approveTestAgreement(t, f, current)
	futureTerms := testAgreementTerms(f.Owner.UserID, "policy-stale-terms")
	futureTerms.EffectiveFrom += 7200
	futureTerms.EffectiveUntil += 7200
	future := proposeTestAgreement(t, f, futureTerms)
	p := f.Policy
	p.ID = 0
	p.RequestID = "next-commercial-policy"
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	c = AgentAgreementCommand{RequestID: "policy-stale-approval", AgreementID: future.ID, ExpectedVersion: 1, Reason: "Synthetic stale policy must be proposed anew"}
	auth = agreementStatementProof(t, f.Reviewer, "agreement.approve", c.RequestID, c)
	_, err = ExecuteAgentAgreement(auth, "agreement.approve", c)
	require.ErrorIs(t, err, ErrAgentAgreementConflict)
}
