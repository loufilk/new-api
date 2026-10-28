package model

import (
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	agentTestAdminID     = 78001
	agentTestOwnerID     = 78002
	agentTestOtherID     = 78003
	agentTestRecipientID = 78004
)

func setupAgentRedemptionTest(t *testing.T) (RedemptionScope, RedemptionScope) {
	t.Helper()
	setupLedgerTestState(t)
	require.NoError(t, DB.AutoMigrate(&Redemption{}, &AgentRedemptionAccount{}, &AgentRedemptionOperation{}, &AgentRedemptionLedger{}))
	for _, table := range []string{"redemptions", "agent_redemption_ledgers", "agent_redemption_operations", "agent_redemption_accounts"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() {
		for _, table := range []string{"redemptions", "agent_redemption_ledgers", "agent_redemption_operations", "agent_redemption_accounts"} {
			require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
		}
	})
	for _, user := range []User{
		{Id: agentTestAdminID, Username: "agent-budget-admin", AffCode: "agent-budget-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled},
		{Id: agentTestOwnerID, Username: "agent-budget-owner", AffCode: "agent-budget-owner", Role: common.RoleAgentUser, Status: common.UserStatusEnabled},
		{Id: agentTestOtherID, Username: "agent-budget-other", AffCode: "agent-budget-other", Role: common.RoleAgentUser, Status: common.UserStatusEnabled},
		{Id: agentTestRecipientID, Username: "agent-budget-recipient", AffCode: "agent-budget-recipient", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
	} {
		require.NoError(t, DB.Create(&user).Error)
	}
	return RedemptionScope{UserID: agentTestAdminID, Role: common.RoleAdminUser}, RedemptionScope{UserID: agentTestOwnerID, Role: common.RoleAgentUser}
}

func allocateAgentTestQuota(t *testing.T, admin RedemptionScope, agentID, amount int, requestID string) {
	t.Helper()
	_, err := AllocateAgentRedemptionQuota(admin, AgentQuotaAllocationRequest{AgentUserID: agentID, Delta: amount, Reason: "test budget", RequestID: requestID})
	require.NoError(t, err)
}

// Assert both aggregate buckets and every immutable delta: a valid total alone
// is insufficient because an incorrect allocation could hide an incorrect refund.
func assertAgentBudget(t *testing.T, agentID, available, reserved, spent, allocated int) {
	t.Helper()
	var account AgentRedemptionAccount
	require.NoError(t, DB.Where("agent_user_id = ?", agentID).First(&account).Error)
	assert.Equal(t, available, account.AvailableQuota)
	assert.Equal(t, reserved, account.ReservedQuota)
	assert.Equal(t, spent, account.SpentQuota)
	assert.Equal(t, allocated, account.AllocatedQuota)
	require.NoError(t, validateAgentAccount(&account))
	var ledger []AgentRedemptionLedger
	require.NoError(t, DB.Where("agent_user_id = ?", agentID).Order("id ASC").Find(&ledger).Error)
	var a, r, s, total int
	for _, row := range ledger {
		a, r, s, total = a+row.Delta, r+row.ReservedDelta, s+row.SpentDelta, total+row.AllocatedDelta
		assert.Equal(t, a, row.AvailableAfter)
		assert.Equal(t, r, row.ReservedAfter)
		assert.Equal(t, s, row.SpentAfter)
		assert.Equal(t, total, row.AllocatedAfter)
		assert.Equal(t, total, a+r+s)
		assert.GreaterOrEqual(t, a, 0)
		assert.GreaterOrEqual(t, r, 0)
		assert.GreaterOrEqual(t, s, 0)
	}
	assert.Equal(t, account.AvailableQuota, a)
	assert.Equal(t, account.ReservedQuota, r)
	assert.Equal(t, account.SpentQuota, s)
	assert.Equal(t, account.AllocatedQuota, total)
}

func TestAgentRedemptionBudgetLifecycleAndIdempotency(t *testing.T) {
	admin, agent := setupAgentRedemptionTest(t)
	allocateAgentTestQuota(t, admin, agent.UserID, 1000, "initial-allocation")
	request := RedemptionCreateRequest{Name: "batch", Quota: 100, Count: 3, RequestID: "create-batch-once"}
	keys, err := CreateRedemptionsScoped(agent, request)
	require.NoError(t, err)
	require.Len(t, keys, 3)
	assertAgentBudget(t, agent.UserID, 700, 300, 0, 1000)
	for _, id := range []int{agent.UserID, agentTestRecipientID} {
		assert.Zero(t, getUserBalances(t, id).Quota, "budget creation is not wallet credit")
	}
	replayed, err := CreateRedemptionsScoped(agent, request)
	require.NoError(t, err)
	assert.Equal(t, keys, replayed)
	request.Count = 2
	_, err = CreateRedemptionsScoped(agent, request)
	require.ErrorIs(t, err, ErrAgentQuotaConflict)
	rows, total, err := GetRedemptionsScoped(agent, 0, "", "", 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	first := rows[0]
	_, err = UpdateRedemptionScoped(agent, RedemptionUpdateRequest{ID: first.Id, Name: "edited", Quota: 150}, false)
	require.NoError(t, err)
	assertAgentBudget(t, agent.UserID, 650, 350, 0, 1000)
	for range 2 {
		_, err = UpdateRedemptionScoped(agent, RedemptionUpdateRequest{ID: first.Id, Status: common.RedemptionCodeStatusDisabled}, true)
		require.NoError(t, err)
		assertAgentBudget(t, agent.UserID, 800, 200, 0, 1000)
	}
	_, err = UpdateRedemptionScoped(agent, RedemptionUpdateRequest{ID: first.Id, Status: common.RedemptionCodeStatusEnabled}, true)
	require.NoError(t, err)
	assertAgentBudget(t, agent.UserID, 650, 350, 0, 1000)
	credited, err := Redeem(first.Key, agentTestRecipientID)
	require.NoError(t, err)
	assert.Equal(t, 150, credited)
	assertAgentBudget(t, agent.UserID, 650, 200, 150, 1000)
	_, err = UpdateRedemptionScoped(admin, RedemptionUpdateRequest{ID: first.Id, Status: common.RedemptionCodeStatusEnabled}, true)
	require.ErrorIs(t, err, ErrRedemptionUsedImmutable)
	_, err = UpdateRedemptionScoped(admin, RedemptionUpdateRequest{ID: first.Id, Name: "change used", Quota: 999}, false)
	require.ErrorIs(t, err, ErrRedemptionUsedImmutable)
	for range 2 {
		_, err = DeleteRedemptionsScoped(agent, []int{rows[1].Id}, false)
		require.NoError(t, err)
		assertAgentBudget(t, agent.UserID, 750, 100, 150, 1000)
	}
	require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", rows[2].Id).Update("expired_time", common.GetTimestamp()-1).Error)
	for i := range 2 {
		count, err := ReleaseExpiredAgentRedemptions(100)
		require.NoError(t, err)
		assert.EqualValues(t, 1-i, count)
		assertAgentBudget(t, agent.UserID, 850, 0, 150, 1000)
	}
	_, err = Redeem(rows[2].Key, agentTestRecipientID)
	require.ErrorIs(t, err, ErrRedeemFailed)
	assertAgentBudget(t, agent.UserID, 850, 0, 150, 1000)
	assert.Equal(t, 150, getUserBalances(t, agentTestRecipientID).QuotaPaid)
	var ledger AgentRedemptionLedger
	require.NoError(t, DB.First(&ledger).Error)
	require.ErrorIs(t, DB.Model(&ledger).Update("delta", 999).Error, ErrAgentLedgerImmutable)
	require.ErrorIs(t, DB.Delete(&ledger).Error, ErrAgentLedgerImmutable)
}

func TestAgentRedemptionScopeAndHistoricAccounts(t *testing.T) {
	admin, agent := setupAgentRedemptionTest(t)
	other := RedemptionScope{UserID: agentTestOtherID, Role: common.RoleAgentUser}
	accounts, total, err := GetAgentRedemptionAccounts(admin, 0, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, total, "new agents must be visible before first allocation")
	require.Len(t, accounts, 2)
	assert.NotEmpty(t, accounts[0].Username)
	allocateAgentTestQuota(t, admin, agent.UserID, 500, "owner-allocation")
	keys, err := CreateRedemptionsScoped(agent, RedemptionCreateRequest{Name: "owned", Quota: 100, Count: 1, RequestID: "create-owner-code"})
	require.NoError(t, err)
	var code Redemption
	require.NoError(t, DB.Where("key = ?", keys[0]).First(&code).Error)
	rows, total, err := GetRedemptionsScoped(other, agent.UserID, fmt.Sprint(code.Id), "", 0, 10)
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.Zero(t, total)
	_, err = GetRedemptionScoped(other, code.Id)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = UpdateRedemptionScoped(other, RedemptionUpdateRequest{ID: code.Id, Status: common.RedemptionCodeStatusDisabled}, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	count, err := DeleteRedemptionsScoped(other, []int{code.Id}, false)
	require.NoError(t, err)
	assert.Zero(t, count)
	_, _, err = GetAgentRedemptionLedger(other, agent.UserID, 0, 10)
	require.ErrorIs(t, err, ErrAgentRedemptionPermission)
	_, err = AllocateAgentRedemptionQuota(other, AgentQuotaAllocationRequest{AgentUserID: other.UserID, Delta: 100, Reason: "self allocate", RequestID: "self-allocation"})
	require.ErrorIs(t, err, ErrAgentRedemptionPermission)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("role", common.RoleCommonUser).Error)
	_, err = CreateRedemptionsScoped(agent, RedemptionCreateRequest{Name: "stale role", Quota: 100, Count: 1, RequestID: "stale-role-create"})
	require.ErrorIs(t, err, ErrAgentRedemptionPermission)
	_, err = UpdateRedemptionScoped(admin, RedemptionUpdateRequest{ID: code.Id, Status: common.RedemptionCodeStatusDisabled}, true)
	require.NoError(t, err)
	_, err = UpdateRedemptionScoped(admin, RedemptionUpdateRequest{ID: code.Id, Status: common.RedemptionCodeStatusEnabled}, true)
	require.ErrorIs(t, err, ErrAgentRedemptionUnavailable)
	allocateAgentTestQuota(t, admin, agent.UserID, -100, "reclaim-after-downgrade")
	accounts, total, err = GetAgentRedemptionAccounts(admin, agent.UserID, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	assert.Equal(t, 400, accounts[0].AvailableQuota)
	assertAgentBudget(t, agent.UserID, 400, 0, 0, 400)
}

func TestAgentRedemptionAllocationReplayConflictAndRollback(t *testing.T) {
	admin, agent := setupAgentRedemptionTest(t)
	request := AgentQuotaAllocationRequest{AgentUserID: agent.UserID, Delta: 100, Reason: "audit reason", RequestID: "allocation-idempotent"}
	first, err := AllocateAgentRedemptionQuota(admin, request)
	require.NoError(t, err)
	replayed, err := AllocateAgentRedemptionQuota(admin, request)
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)
	assert.Equal(t, first.Account, replayed.Account)
	for _, altered := range []AgentQuotaAllocationRequest{
		{AgentUserID: agent.UserID, Delta: 101, Reason: request.Reason, RequestID: request.RequestID},
		{AgentUserID: agentTestOtherID, Delta: request.Delta, Reason: request.Reason, RequestID: request.RequestID},
		{AgentUserID: agent.UserID, Delta: request.Delta, Reason: "changed intent", RequestID: request.RequestID},
	} {
		_, err := AllocateAgentRedemptionQuota(admin, altered)
		require.ErrorIs(t, err, ErrAgentQuotaConflict)
	}
	_, err = AllocateAgentRedemptionQuota(admin, AgentQuotaAllocationRequest{AgentUserID: agent.UserID, Delta: -101, Reason: "overdraw", RequestID: "allocation-overdraw"})
	require.ErrorIs(t, err, ErrAgentQuotaInsufficient)
	_, err = CreateRedemptionsScoped(agent, RedemptionCreateRequest{Name: "too large", Quota: 60, Count: 2, RequestID: "batch-overdraw"})
	require.ErrorIs(t, err, ErrAgentQuotaInsufficient)
	assertAgentBudget(t, agent.UserID, 100, 0, 0, 100)
	var count int64
	require.NoError(t, DB.Model(&Redemption{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&AgentRedemptionOperation{}).Count(&count).Error)
	assert.EqualValues(t, 1, count, "failed transactions must not retain operation headers")
	keys, err := CreateRedemptionsScoped(agent, RedemptionCreateRequest{Name: "rollback wallet", Quota: 100, Count: 1, RequestID: "wallet-overflow-code"})
	require.NoError(t, err)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agentTestRecipientID).Updates(map[string]any{"quota": common.MaxWalletQuota, "quota_paid": common.MaxWalletQuota}).Error)
	_, err = Redeem(keys[0], agentTestRecipientID)
	require.ErrorIs(t, err, ErrRedeemFailed)
	assertAgentBudget(t, agent.UserID, 0, 100, 0, 100)
	var code Redemption
	require.NoError(t, DB.Where("key = ?", keys[0]).First(&code).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
	assert.True(t, code.BudgetReserved)
	assert.Zero(t, code.RedeemedTime)
	assert.Equal(t, common.MaxWalletQuota, getUserBalances(t, agentTestRecipientID).QuotaPaid)
}

func TestAgentRedemptionPostgresConcurrentBudgetAndRedeem(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires PostgreSQL 18.6 TEST_MODEL_POSTGRES_DSN")
	}
	admin, agent := setupAgentRedemptionTest(t)
	allocateAgentTestQuota(t, admin, agent.UserID, 100, "concurrent-initial")
	start := make(chan struct{})
	results := make([]error, 2)
	keys := make([][]string, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			<-start
			keys[i], results[i] = CreateRedemptionsScoped(agent, RedemptionCreateRequest{Name: "concurrent", Quota: 100, Count: 1, RequestID: fmt.Sprintf("concurrent-create-%d", i)})
		})
	}
	close(start)
	wg.Wait()
	var winningKey string
	var success int
	for i, err := range results {
		if err == nil {
			success++
			winningKey = keys[i][0]
		} else {
			assert.ErrorIs(t, err, ErrAgentQuotaInsufficient)
		}
	}
	require.Equal(t, 1, success, "concurrent issuance must not oversell a shared budget")
	assertAgentBudget(t, agent.UserID, 0, 100, 0, 100)
	start = make(chan struct{})
	for i := range 2 {
		wg.Go(func() { <-start; _, results[i] = Redeem(winningKey, []int{agentTestRecipientID, agentTestOtherID}[i]) })
	}
	close(start)
	wg.Wait()
	success = 0
	for _, err := range results {
		if err == nil {
			success++
		}
	}
	require.Equal(t, 1, success, "one code cannot produce two wallet credits")
	assertAgentBudget(t, agent.UserID, 0, 0, 100, 100)
	var walletRows int64
	require.NoError(t, DB.Model(&BalanceLedger{}).Where("reason = ?", LedgerReasonRedemption).Count(&walletRows).Error)
	assert.EqualValues(t, 1, walletRows)
	// Independent administrator adjustments serialize on the same budget row.
	start = make(chan struct{})
	for i := range 2 {
		wg.Go(func() {
			<-start
			_, results[i] = AllocateAgentRedemptionQuota(admin, AgentQuotaAllocationRequest{AgentUserID: agent.UserID, Delta: 70, Reason: "concurrent allocation", RequestID: fmt.Sprintf("concurrent-allocation-%d", i)})
		})
	}
	close(start)
	wg.Wait()
	for _, err := range results {
		require.NoError(t, err)
	}
	assertAgentBudget(t, agent.UserID, 140, 0, 100, 240)
	start = make(chan struct{})
	for i := range 2 {
		wg.Go(func() {
			<-start
			_, results[i] = AllocateAgentRedemptionQuota(admin, AgentQuotaAllocationRequest{AgentUserID: agent.UserID, Delta: -100, Reason: "concurrent reclaim", RequestID: fmt.Sprintf("concurrent-reclaim-%d", i)})
		})
	}
	close(start)
	wg.Wait()
	success = 0
	for _, err := range results {
		if err == nil {
			success++
		} else {
			assert.ErrorIs(t, err, ErrAgentQuotaInsufficient)
		}
	}
	assert.Equal(t, 1, success)
	assertAgentBudget(t, agent.UserID, 40, 0, 100, 140)
}

func TestAgentRedemptionLegacyUsedCodeCannotResurrect(t *testing.T) {
	admin, _ := setupAgentRedemptionTest(t)
	legacy := Redemption{UserId: admin.UserID, Key: "00000000000000000000000000000099", Name: "legacy", Quota: 25, Status: common.RedemptionCodeStatusEnabled}
	require.NoError(t, legacy.Insert())
	_, err := Redeem(legacy.Key, agentTestRecipientID)
	require.NoError(t, err)
	_, err = UpdateRedemptionScoped(admin, RedemptionUpdateRequest{ID: legacy.Id, Status: common.RedemptionCodeStatusEnabled}, true)
	require.ErrorIs(t, err, ErrRedemptionUsedImmutable)
	// Even a previously corrupted legacy status cannot bypass durable use markers.
	require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", legacy.Id).Update("status", common.RedemptionCodeStatusEnabled).Error)
	_, err = Redeem(legacy.Key, agentTestRecipientID)
	require.ErrorIs(t, err, ErrRedeemFailed)
	_, err = UpdateRedemptionScoped(admin, RedemptionUpdateRequest{ID: legacy.Id, Name: "new value", Quota: 50}, false)
	require.ErrorIs(t, err, ErrRedemptionUsedImmutable)
	assert.Equal(t, 25, getUserBalances(t, agentTestRecipientID).QuotaPaid)
	var budgets, budgetRows int64
	require.NoError(t, DB.Model(&AgentRedemptionAccount{}).Count(&budgets).Error)
	require.NoError(t, DB.Model(&AgentRedemptionLedger{}).Count(&budgetRows).Error)
	assert.Zero(t, budgets, "legacy codes must not be silently charged to agent budgets")
	assert.Zero(t, budgetRows)
}

func TestAgentRedemptionBoundaryValuesAndLiveActorAuthorization(t *testing.T) {
	admin, agent := setupAgentRedemptionTest(t)
	allocateAgentTestQuota(t, admin, agent.UserID, common.MaxWalletQuota, "max-budget-allocation")
	_, err := AllocateAgentRedemptionQuota(admin, AgentQuotaAllocationRequest{AgentUserID: agent.UserID, Delta: 1, Reason: "overflow", RequestID: "overflow-allocation"})
	require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	_, err = CreateRedemptionsScoped(agent, RedemptionCreateRequest{Name: "overflow product", Quota: common.MaxWalletQuota, Count: 2, RequestID: "overflow-batch-product"})
	require.ErrorIs(t, err, ErrRedemptionInvalidRequest)
	keys, err := CreateRedemptionsScoped(agent, RedemptionCreateRequest{Name: "exact boundary", Quota: common.MaxWalletQuota, Count: 1, RequestID: "max-value-single-code"})
	require.NoError(t, err)
	assertAgentBudget(t, agent.UserID, 0, common.MaxWalletQuota, 0, common.MaxWalletQuota)
	rows, _, err := GetRedemptionsScoped(agent, 0, "", "", 0, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	_, err = DeleteRedemptionsScoped(agent, []int{rows[0].Id}, false)
	require.NoError(t, err)
	assertAgentBudget(t, agent.UserID, common.MaxWalletQuota, 0, 0, common.MaxWalletQuota)
	// Revoking the issuing user's status must block a stale in-flight session.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("status", common.UserStatusDisabled).Error)
	_, err = CreateRedemptionsScoped(agent, RedemptionCreateRequest{Name: "disabled", Quota: 1, Count: 1, RequestID: "disabled-create-code"})
	require.ErrorIs(t, err, ErrAgentRedemptionPermission)
	// A stale administrator session cannot add, reclaim or replay an adjustment.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", admin.UserID).Update("role", common.RoleCommonUser).Error)
	for _, request := range []AgentQuotaAllocationRequest{
		{AgentUserID: agent.UserID, Delta: -1, Reason: "stale admin", RequestID: "stale-admin-negative"},
		{AgentUserID: agent.UserID, Delta: common.MaxWalletQuota, Reason: "test budget", RequestID: "max-budget-allocation"},
	} {
		_, err := AllocateAgentRedemptionQuota(admin, request)
		require.ErrorIs(t, err, ErrAgentRedemptionPermission)
	}
	assertAgentBudget(t, agent.UserID, common.MaxWalletQuota, 0, 0, common.MaxWalletQuota)
	assert.Len(t, keys, 1)
}

func TestAgentRedemptionExpiryOnRedeemAndReactivation(t *testing.T) {
	admin, agent := setupAgentRedemptionTest(t)
	allocateAgentTestQuota(t, admin, agent.UserID, 200, "expiry-initial-budget")
	keys, err := CreateRedemptionsScoped(agent, RedemptionCreateRequest{Name: "expiry", Quota: 200, Count: 1, RequestID: "expiry-issued-code"})
	require.NoError(t, err)
	var row Redemption
	require.NoError(t, DB.Where("key = ?", keys[0]).First(&row).Error)
	require.NoError(t, DB.Model(&row).Update("expired_time", common.GetTimestamp()-1).Error)
	for range 2 {
		_, err := Redeem(keys[0], agentTestRecipientID)
		require.ErrorIs(t, err, ErrRedeemFailed)
		assertAgentBudget(t, agent.UserID, 200, 0, 0, 200)
	}
	_, err = UpdateRedemptionScoped(agent, RedemptionUpdateRequest{ID: row.Id, Name: row.Name, Quota: 200, ExpiredTime: common.GetTimestamp() + 3600}, false)
	require.NoError(t, err)
	assertAgentBudget(t, agent.UserID, 0, 200, 0, 200)
	// Previously issued codes remain redeemable after the issuer is demoted.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", agent.UserID).Update("role", common.RoleCommonUser).Error)
	_, err = Redeem(keys[0], agentTestRecipientID)
	require.NoError(t, err)
	assertAgentBudget(t, agent.UserID, 0, 0, 200, 200)
}

func TestAgentRedemptionPostgresConcurrentRefundAndCrossAccountDelete(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires PostgreSQL 18.6 TEST_MODEL_POSTGRES_DSN")
	}
	admin, agent := setupAgentRedemptionTest(t)
	other := RedemptionScope{UserID: agentTestOtherID, Role: common.RoleAgentUser}
	allocateAgentTestQuota(t, admin, agent.UserID, 400, "cross-delete-owner-budget")
	allocateAgentTestQuota(t, admin, other.UserID, 400, "cross-delete-other-budget")
	// Interleave code IDs across owners; concurrent admin batches have opposite
	// account encounter order unless all budgets are locked in numeric order.
	ids := make([]int, 4)
	for i, owner := range []RedemptionScope{agent, other, other, agent} {
		keys, err := CreateRedemptionsScoped(owner, RedemptionCreateRequest{Name: "cross delete", Quota: 100, Count: 1, RequestID: fmt.Sprintf("cross-delete-create-%d", i)})
		require.NoError(t, err)
		var row Redemption
		require.NoError(t, DB.Where("key = ?", keys[0]).First(&row).Error)
		ids[i] = row.Id
	}
	start := make(chan struct{})
	results := make([]error, 2)
	var wg sync.WaitGroup
	for i, batch := range [][]int{{ids[0], ids[2]}, {ids[1], ids[3]}} {
		wg.Go(func() { <-start; _, results[i] = DeleteRedemptionsScoped(admin, batch, false) })
	}
	close(start)
	wg.Wait()
	for _, err := range results {
		require.NoError(t, err, "consistent account lock order must avoid deadlock")
	}
	assertAgentBudget(t, agent.UserID, 400, 0, 0, 400)
	assertAgentBudget(t, other.UserID, 400, 0, 0, 400)
	keys, err := CreateRedemptionsScoped(agent, RedemptionCreateRequest{Name: "refund race", Quota: 100, Count: 1, RequestID: "refund-race-create"})
	require.NoError(t, err)
	var row Redemption
	require.NoError(t, DB.Where("key = ?", keys[0]).First(&row).Error)
	require.NoError(t, DB.Model(&row).Update("expired_time", common.GetTimestamp()-1).Error)
	start = make(chan struct{})
	results = make([]error, 3)
	wg.Go(func() { <-start; _, results[0] = ReleaseExpiredAgentRedemptions(100) })
	wg.Go(func() { <-start; _, results[1] = DeleteRedemptionsScoped(agent, []int{row.Id}, false) })
	wg.Go(func() {
		<-start
		_, results[2] = UpdateRedemptionScoped(agent, RedemptionUpdateRequest{ID: row.Id, Status: common.RedemptionCodeStatusDisabled}, true)
	})
	close(start)
	wg.Wait()
	for _, err := range results {
		if err != nil {
			require.ErrorIs(t, err, gorm.ErrRecordNotFound, "only a deletion winning the race may prevent a later status update")
		}
	}
	assertAgentBudget(t, agent.UserID, 400, 0, 0, 400)
	var refundEvents int64
	require.NoError(t, DB.Model(&AgentRedemptionLedger{}).Where("redemption_id = ? AND delta > 0", row.Id).Count(&refundEvents).Error)
	assert.EqualValues(t, 1, refundEvents, "delete, disable and expiry must share the same reservation receipt")
}
