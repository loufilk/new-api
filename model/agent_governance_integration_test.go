package model

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func governancePrepaidPolicy(t *testing.T, f prepaidFixture, limits AgentGovernanceRiskLimits) commissionFixture {
	t.Helper()
	p := f.Policy
	p.ID = 0
	p.RequestID = "governance-prepaid-policy"
	p.GovernanceEnabled = true
	p.GovernanceRequiredForActivation = true
	p.CreditGovernanceRequired = true
	p.GovernanceInactivitySeconds = 86400
	require.NoError(t, ValidateAgentPolicy(&p))
	require.NoError(t, DB.Create(&p).Error)
	require.NoError(t, DB.Model(&AgentPolicyState{}).Where("id = 1").Update("current_policy_id", p.ID).Error)
	var owner, finance User
	require.NoError(t, DB.First(&owner, f.Owner.UserID).Error)
	require.NoError(t, DB.First(&finance, f.Finance.UserID).Error)
	gf := commissionFixture{inviter: owner, root: finance, identity: f.Owner, rootIdentity: f.Finance, policy: p}
	governanceApprove(t, gf, limits)
	return gf
}
func TestAgentGovernanceDeliveryCancellationGrossCapAndBoundCustomerRedemptionPostgres(t *testing.T) {
	f := setupPrepaidTest(t)
	prepaidCredit(t, f, 1000, "governance-real-delivery")
	governancePrepaidPolicy(t, f, AgentGovernanceRiskLimits{Enabled: true, DailyIssuanceQuota: 100, DailyTransferQuota: 50})
	c := AgentDeliveryCommand{RequestID: "governance-delivery-first", Name: "Reviewed delivery", Quota: 60}
	auth := prepaidProof(t, f.Owner, "delivery.create", c.RequestID, c)
	first, err := CreateAgentDelivery(auth, c)
	require.NoError(t, err)
	replay, err := CreateAgentDelivery(auth, c)
	require.NoError(t, err)
	assert.Equal(t, first.ID, replay.ID)
	// Separate business actions may use the same client key. Their risk usage
	// must remain distinct rather than deduplicating solely by raw request ID.
	batch := RedemptionCreateRequest{RequestID: c.RequestID, Name: "Cross-action key", Quota: 60, Count: 1}
	scope, err := ResolveRedemptionScope(f.Owner.UserID)
	require.NoError(t, err)
	scope.Identity = &f.Owner
	batchAuth := prepaidProof(t, f.Owner, "redemption.create", batch.RequestID, batch)
	scope.Authorization = &batchAuth
	_, err = CreateRedemptionsScoped(scope, batch)
	require.ErrorIs(t, err, ErrAgentGovernanceRiskLimit)

	cancel := AgentDeliveryCommand{RequestID: "governance-delivery-cancel", DeliveryID: first.ID, Version: first.Version, CustomerObligationsResolved: true, EvidenceReference: "synthetic-no-customer-obligation"}
	_, err = CancelAgentDelivery(prepaidProof(t, f.Owner, "delivery.cancel", cancel.RequestID, cancel), cancel)
	require.NoError(t, err)
	assertAgentBudget(t, f.Owner.UserID, 1000, 0, 0, 1000)
	c.RequestID = "governance-delivery-over-cap"
	_, err = CreateAgentDelivery(prepaidProof(t, f.Owner, "delivery.create", c.RequestID, c), c)
	require.ErrorIs(t, err, ErrAgentGovernanceRiskLimit)
	assertAgentBudget(t, f.Owner.UserID, 1000, 0, 0, 1000)
	c.RequestID = "governance-delivery-bound"
	c.Quota = 40
	bound, err := CreateAgentDelivery(prepaidProof(t, f.Owner, "delivery.create", c.RequestID, c), c)
	require.NoError(t, err)
	claimed, err := ClaimAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "governance-customer-claim", ClaimCode: bound.ClaimCode})
	require.NoError(t, err)
	before := getUserBalances(t, f.Customer.UserID)
	require.NoError(t, DB.Model(&AgentGovernance{}).Where("user_id = ?", f.Owner.UserID).Updates(map[string]any{"reviewed_at": common.GetTimestamp() - 86401, "review_due_at": common.GetTimestamp() - 1}).Error)
	profile, err := GetAgentProfile(f.Owner.UserID)
	require.NoError(t, err)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := UpdateAgentProfileTx(tx, f.Finance, f.Owner.UserID, AgentProfileMutation{ExpectedVersion: profile.Version, Status: AgentProfileActive, SalesFrozen: true, SettlementFrozen: true, Reason: "Independent business freeze, not customer cancellation"})
		return err
	}))
	confirmed, err := ConfirmAgentDelivery(f.Customer, AgentDeliveryCommand{RequestID: "governance-customer-confirm", DeliveryID: claimed.ID, Version: claimed.Version})
	require.NoError(t, err)
	assert.Equal(t, "redeemed", confirmed.State)
	after := getUserBalances(t, f.Customer.UserID)
	assert.Equal(t, before.Quota+40, after.Quota)
	assert.Equal(t, before.InviterId, after.InviterId)
	assert.Equal(t, before.AuthVersion, after.AuthVersion)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, f.Owner) }))
	view, err := GetAgentGovernance(f.Owner, f.Owner.UserID, false)
	require.NoError(t, err)
	assert.True(t, view.ReviewRequired)
	assert.Equal(t, "100", view.DailyRisk.GrossIssuanceQuota)
	assertAgentBudget(t, f.Owner.UserID, 960, 0, 40, 1000)
	assertPrepaidReconciled(t)
}
func governanceLegacyUpdate(t *testing.T, identity AuthSessionIdentity, c AgentLegacyRedemptionUpdateRequest) (*Redemption, error) {
	t.Helper()
	scope, err := ResolveRedemptionScope(identity.UserID)
	require.NoError(t, err)
	scope.Identity = &identity
	auth := prepaidProof(t, identity, "redemption.update", c.RequestID, c)
	scope.Authorization = &auth
	return UpdateRedemptionScoped(scope, c.RedemptionUpdateRequest, c.StatusOnly)
}
func TestAgentGovernanceLegacyIssuanceIncreaseDisableReenableCannotCycleRiskPostgres(t *testing.T) {
	f := setupPrepaidTest(t)
	allocateAgentTestQuota(t, RedemptionScope{UserID: f.Finance.UserID, Role: common.RoleAdminUser}, f.Owner.UserID, 1000, "governance-promo-allocation")
	governancePrepaidPolicy(t, f, AgentGovernanceRiskLimits{Enabled: true, DailyIssuanceQuota: 100, DailyTransferQuota: 50})
	scope, err := ResolveRedemptionScope(f.Owner.UserID)
	require.NoError(t, err)
	scope.Identity = &f.Owner
	request := RedemptionCreateRequest{RequestID: "governance-legacy-create", Name: "Legacy issued code", Quota: 60, Count: 1}
	auth := prepaidProof(t, f.Owner, "redemption.create", request.RequestID, request)
	scope.Authorization = &auth
	keys, err := CreateRedemptionsScoped(scope, request)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	replay, err := CreateRedemptionsScoped(scope, request)
	require.NoError(t, err)
	assert.Equal(t, keys, replay)
	var code Redemption
	require.NoError(t, DB.Where(commonKeyCol+" = ?", keys[0]).First(&code).Error)
	changed, err := governanceLegacyUpdate(t, f.Owner, AgentLegacyRedemptionUpdateRequest{RedemptionUpdateRequest: RedemptionUpdateRequest{ID: code.Id, Version: int64(code.BudgetVersion), RequestID: "governance-legacy-increase", Name: code.Name, Quota: 80}})
	require.NoError(t, err)
	disabled, err := governanceLegacyUpdate(t, f.Owner, AgentLegacyRedemptionUpdateRequest{RedemptionUpdateRequest: RedemptionUpdateRequest{ID: code.Id, Version: int64(changed.BudgetVersion), RequestID: "governance-legacy-disable", Status: common.RedemptionCodeStatusDisabled}, StatusOnly: true})
	require.NoError(t, err)
	_, err = governanceLegacyUpdate(t, f.Owner, AgentLegacyRedemptionUpdateRequest{RedemptionUpdateRequest: RedemptionUpdateRequest{ID: code.Id, Version: int64(disabled.BudgetVersion), RequestID: "governance-legacy-reenable-over", Status: common.RedemptionCodeStatusEnabled}, StatusOnly: true})
	require.ErrorIs(t, err, ErrAgentGovernanceRiskLimit)
	resized, err := governanceLegacyUpdate(t, f.Owner, AgentLegacyRedemptionUpdateRequest{RedemptionUpdateRequest: RedemptionUpdateRequest{ID: code.Id, Version: int64(disabled.BudgetVersion), RequestID: "governance-disabled-resize", Name: code.Name, Quota: 20}})
	require.NoError(t, err)
	enabled, err := governanceLegacyUpdate(t, f.Owner, AgentLegacyRedemptionUpdateRequest{RedemptionUpdateRequest: RedemptionUpdateRequest{ID: code.Id, Version: int64(resized.BudgetVersion), RequestID: "governance-small-reenable", Status: common.RedemptionCodeStatusEnabled}, StatusOnly: true})
	require.NoError(t, err)
	assert.True(t, enabled.BudgetReserved)
	view, err := GetAgentGovernance(f.Owner, f.Owner.UserID, false)
	require.NoError(t, err)
	assert.Equal(t, "100", view.DailyRisk.GrossIssuanceQuota)
	assert.Equal(t, "0", *view.DailyRisk.RemainingIssuanceQuota)
	assertAgentBudget(t, f.Owner.UserID, 980, 20, 0, 1000)
}
func TestAgentGovernanceActivationGateAlsoProtectsLegacyManagementEntryPostgres(t *testing.T) {
	f := setupAgentGovernancePostgres(t)
	err := DB.Transaction(func(tx *gorm.DB) error {
		_, err := UpdateAgentProfileTx(tx, f.rootIdentity, f.inviter.Id, AgentProfileMutation{Status: AgentProfileActive, Reason: "New activation without approved dossier"})
		return err
	})
	require.ErrorIs(t, err, ErrAgentGovernanceRequired)
	err = DB.Transaction(func(tx *gorm.DB) error {
		_, err := ApplyUserManagementActionTx(tx, f.root.Id, f.inviter, "set_agent", f.rootIdentity)
		return err
	})
	require.ErrorIs(t, err, ErrAgentGovernanceRequired)
	governanceApprove(t, f, AgentGovernanceRiskLimits{})
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := ApplyUserManagementActionTx(tx, f.root.Id, f.inviter, "set_agent", f.rootIdentity)
		return err
	}))
	stored := getUserBalances(t, f.inviter.Id)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Equal(t, f.inviter.AuthVersion, stored.AuthVersion)
	assert.Equal(t, f.inviter.Quota, stored.Quota)
	require.NoError(t, DB.Model(&AgentGovernance{}).Where("user_id = ?", f.inviter.Id).Update("review_due_at", common.GetTimestamp()-1).Error)
	profile, err := GetAgentProfile(f.inviter.Id)
	require.NoError(t, err)
	// Expired review does not deactivate the old profile or forbid a freeze.
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := UpdateAgentProfileTx(tx, f.rootIdentity, f.inviter.Id, AgentProfileMutation{ExpectedVersion: profile.Version, Status: AgentProfileActive, SalesFrozen: true, Reason: "Protect existing business while awaiting review"})
		return err
	}))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := ApplyUserManagementActionTx(tx, f.root.Id, stored, "unset_agent", f.rootIdentity)
		return err
	}))
	err = DB.Transaction(func(tx *gorm.DB) error {
		_, err := ApplyUserManagementActionTx(tx, f.root.Id, stored, "set_agent", f.rootIdentity)
		return err
	})
	require.ErrorIs(t, err, ErrAgentGovernanceRequired)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, f.identity) }))
}

func TestAgentGovernanceProcurementSelfUseGrossTransferCapRollsBackWalletPostgres(t *testing.T) {
	f := setupPrepaidTest(t)
	order := prepaidCredit(t, f, 1000, "governance-self-use")
	governancePrepaidPolicy(t, f, AgentGovernanceRiskLimits{Enabled: true, DailyIssuanceQuota: 100, DailyTransferQuota: 50})
	c := AgentProcurementSelfUseRequest{RequestID: "governance-self-use-first", BatchID: order.FundBatchID, Quota: 40}
	auth := prepaidProof(t, f.Owner, "procurement.self_use", c.RequestID, c)
	_, err := AgentProcurementSelfUse(auth, c)
	require.NoError(t, err)
	_, err = AgentProcurementSelfUse(auth, c)
	require.NoError(t, err)
	before := getUserBalances(t, f.Owner.UserID)
	assert.Equal(t, 40, before.QuotaPaid)
	c.RequestID = "governance-self-use-over-cap"
	c.Quota = 20
	_, err = AgentProcurementSelfUse(prepaidProof(t, f.Owner, "procurement.self_use", c.RequestID, c), c)
	require.ErrorIs(t, err, ErrAgentGovernanceRiskLimit)
	after := getUserBalances(t, f.Owner.UserID)
	assert.Equal(t, before.Quota, after.Quota)
	assert.Equal(t, before.QuotaPaid, after.QuotaPaid)
	assertAgentBudget(t, f.Owner.UserID, 960, 0, 40, 1000)
	view, err := GetAgentGovernance(f.Owner, f.Owner.UserID, false)
	require.NoError(t, err)
	assert.Equal(t, "40", view.DailyRisk.GrossTransferQuota)
	assert.Equal(t, "10", *view.DailyRisk.RemainingTransferQuota)
}
func TestAgentGovernanceCommissionWalletAndProcurementShareGrossTransferCapPostgres(t *testing.T) {
	f := setupAgentGovernancePostgres(t)
	governanceApprove(t, f, AgentGovernanceRiskLimits{Enabled: true, DailyIssuanceQuota: 100, DailyTransferQuota: 150000})
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := UpdateAgentProfileTx(tx, f.rootIdentity, f.inviter.Id, AgentProfileMutation{Status: AgentProfileActive, Reason: "Separate approved activation"})
		return err
	}))
	commissionPaidTopup(t, f, "governance-conversion-principal", 10000)
	commissionDrain(t)
	before := getUserBalances(t, f.inviter.Id)
	for i, input := range []AgentCommissionQuoteRequest{{RequestID: "governance-wallet-conversion", Target: "wallet", Currency: "USD", AmountMinor: 20}, {RequestID: "governance-procurement-conversion", Target: "procurement", Currency: "USD", AmountMinor: 10}, {RequestID: "governance-conversion-over-cap", Target: "wallet", Currency: "USD", AmountMinor: 1}} {
		quote, err := CreateAgentCommissionQuote(f.identity, input)
		require.NoError(t, err)
		command := AgentCommissionExecuteRequest{RequestID: input.RequestID, QuoteID: quote.ID}
		auth := commissionProof(t, f.identity, "commission.convert."+input.Target, input.RequestID, command)
		_, err = ExecuteAgentCommissionConversion(auth, command)
		if i == 2 {
			require.ErrorIs(t, err, ErrAgentGovernanceRiskLimit)
		} else {
			require.NoError(t, err)
			_, err = ExecuteAgentCommissionConversion(auth, command)
			require.NoError(t, err)
		}
	}
	after := getUserBalances(t, f.inviter.Id)
	assert.Equal(t, before.Quota+100000, after.Quota)
	assert.Equal(t, before.QuotaReward+100000, after.QuotaReward)
	account := commissionAccount(t, f)
	assert.EqualValues(t, 30, account.TransferredMinor)
	var budget AgentRedemptionAccount
	require.NoError(t, DB.First(&budget, f.inviter.Id).Error)
	assert.Equal(t, 50000, budget.AvailableQuota)
	view, err := GetAgentGovernance(f.identity, f.inviter.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "150000", view.DailyRisk.GrossTransferQuota)
	assert.Equal(t, "0", *view.DailyRisk.RemainingTransferQuota)
}

func TestAgentGovernanceRecoveryAndContributionReadsSerializeBeforeCreditRiskLocksPostgres(t *testing.T) {
	f := setupAgentCreditTest(t)
	gf := governancePrepaidPolicy(t, f, AgentGovernanceRiskLimits{})
	require.NoError(t, DB.AutoMigrate(&AgentInvitation{}))
	limit := creditLimit(t, f, 100, "governance-lock-order")
	command := AgentCreditCommand{RequestID: "governance-concurrent-credit-draw", Quota: 10, ExpectedLimitID: limit.ID}
	auth := creditProof(t, f.Owner, "credit.draw", command)
	submit := governanceCommand(gf)
	payload, err := common.Marshal(submit)
	require.NoError(t, err)
	// Pause a real credit transaction while it holds governance eligibility.
	// Recovery/history readers must queue at policy, not retain user locks while
	// waiting for that governance row. This is deterministic lock choreography,
	// not a timing/throughput assertion or repeated stress loop.
	held := make(chan struct{})
	release := make(chan struct{})
	atPolicy := make(chan struct{}, 3)
	var blocked atomic.Bool
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register("test:governance-credit-lock", func(tx *gorm.DB) {
		if tx.Error == nil && tx.Statement.Table == "agent_governances" && blocked.CompareAndSwap(false, true) {
			close(held)
			<-release
		}
	}))
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register("test:governance-reader-policy-lock", func(tx *gorm.DB) {
		if blocked.Load() && tx.Statement.Table == "agent_policy_states" {
			select {
			case atPolicy <- struct{}{}:
			default:
			}
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Query().Remove("test:governance-credit-lock"))
		require.NoError(t, DB.Callback().Query().Remove("test:governance-reader-policy-lock"))
	})
	outcomes := make(chan error, 4)
	go func() { _, err := ExecuteAgentCreditOperation(auth, "credit.draw", command); outcomes <- err }()
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		unblock()
		t.Fatal("credit transaction did not reach its governance lock")
	}
	go func() {
		_, found, err := ReplayAgentGovernanceOperation(f.Owner, "governance.submit", submit.RequestID, payload)
		if err == nil && !found {
			err = ErrAgentGovernanceConflict
		}
		outcomes <- err
	}()
	go func() {
		status, err := FindAgentGovernanceOperation(f.Owner, submit.RequestID)
		if err == nil && status == nil {
			err = ErrAgentGovernanceConflict
		}
		outcomes <- err
	}()
	go func() { _, err := GetAgentContributions(f.Owner, f.Owner.UserID, 1, 20, false); outcomes <- err }()
	reached := 0
	for reached < 3 {
		select {
		case <-atPolicy:
			reached++
		case <-time.After(10 * time.Second):
			unblock()
			t.Errorf("only %d recovery/history readers queued at policy before credit governance", reached)
			reached = 3
		}
	}
	unblock()
	for range 4 {
		select {
		case err := <-outcomes:
			require.NoError(t, err)
		case <-time.After(15 * time.Second):
			t.Fatal("credit and governance recovery/history did not converge")
		}
	}
	var draws int64
	require.NoError(t, DB.Model(&AgentCreditDraw{}).Where("agent_user_id = ?", f.Owner.UserID).Count(&draws).Error)
	assert.EqualValues(t, 1, draws)
}
