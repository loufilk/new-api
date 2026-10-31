package wallet

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedAllocationFunding(t *testing.T, bonus, reward, paid int) (*gorm.DB, *Funding, moneyport.FactoryRequest) {
	t.Helper()
	db := setupWalletTestDB(t)
	u := model.User{Username: "allocation-user", AffCode: "allocation-user", Quota: bonus + reward + paid, QuotaBonus: bonus, QuotaReward: reward, QuotaPaid: paid, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&u).Error)
	request := moneyport.FactoryRequest{UserID: u.Id, RequestID: "allocation-request", AccountOrder: []string{"bonus", "reward", "paid"}}
	funding, err := NewFunding(request)
	require.NoError(t, err)
	return db, funding, request
}

func requireAllocationBalances(t *testing.T, db *gorm.DB, id, bonus, reward, paid int) {
	t.Helper()
	var user model.User
	require.NoError(t, db.First(&user, id).Error)
	require.Equal(t, bonus, user.QuotaBonus)
	require.Equal(t, reward, user.QuotaReward)
	require.Equal(t, paid, user.QuotaPaid)
	require.Equal(t, bonus+reward+paid, user.Quota)
}

func TestFundingAllocationPartialRefundReplayAndRemainder(t *testing.T) {
	db, funding, request := seedAllocationFunding(t, 1, 1, 1)
	require.NoError(t, funding.PreConsume(3))
	require.NoError(t, funding.Settle(-2))
	requireAllocationBalances(t, db, request.UserID, 1, 1, 0)
	require.Equal(t, []moneyport.AccountShare{{Account: "paid", Amount: 1}}, funding.Receipt())
	require.Equal(t, 1, funding.ReservedQuota())
	// The same operation replays the original credit, not a new allocation
	// from the now smaller receipt. Applying its result twice also stays net.
	require.NoError(t, funding.Settle(-2))
	requireAllocationBalances(t, db, request.UserID, 1, 1, 0)
	require.Equal(t, []moneyport.AccountShare{{Account: "paid", Amount: 1}}, funding.Receipt())
	require.NoError(t, funding.Refund())
	require.NoError(t, funding.Refund())
	requireAllocationBalances(t, db, request.UserID, 1, 1, 1)
	require.Empty(t, funding.Receipt())
	var credits int64
	require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Where("operation = ?", "credit").Count(&credits).Error)
	require.EqualValues(t, 2, credits)
}

func TestFundingAllocationReconstructionLoadsNetReceipt(t *testing.T) {
	db, first, request := seedAllocationFunding(t, 1, 1, 1)
	require.NoError(t, first.PreConsume(3))
	require.NoError(t, first.Settle(-2))

	reconstructed, err := NewFunding(request)
	require.NoError(t, err)
	// The earlier partial refund need not be explicitly replayed by the new
	// process. Replaying preconsume itself must load the authoritative net.
	require.NoError(t, reconstructed.PreConsume(3))
	require.Equal(t, []moneyport.AccountShare{{Account: "paid", Amount: 1}}, reconstructed.Receipt())
	require.NoError(t, reconstructed.Refund())
	requireAllocationBalances(t, db, request.UserID, 1, 1, 1)
	// The stale first instance also cannot repay the remaining unit twice.
	require.NoError(t, first.Refund())
	require.Empty(t, first.Receipt())
	requireAllocationBalances(t, db, request.UserID, 1, 1, 1)
}

func TestFundingAllocationSupplementAfterPartialRefundRemainsBounded(t *testing.T) {
	db, funding, request := seedAllocationFunding(t, 1, 1, 1)
	require.NoError(t, funding.PreConsume(3))
	require.NoError(t, funding.Settle(-2))
	require.NoError(t, funding.Settle(2))
	require.Equal(t, []moneyport.AccountShare{{Account: "bonus", Amount: 1}, {Account: "reward", Amount: 1}, {Account: "paid", Amount: 1}}, funding.Receipt())
	require.NoError(t, funding.Settle(-1))
	require.Equal(t, []moneyport.AccountShare{{Account: "reward", Amount: 1}, {Account: "paid", Amount: 1}}, funding.Receipt())
	require.NoError(t, funding.Refund())
	requireAllocationBalances(t, db, request.UserID, 1, 1, 1)
}

func TestFundingReserveUsesDurableReceiptInsteadOfStaleNoOp(t *testing.T) {
	db, first, request := seedAllocationFunding(t, 1, 1, 1)
	require.NoError(t, first.PreConsume(3))
	second, err := NewFunding(request)
	require.NoError(t, err)
	require.NoError(t, second.PreConsume(3))
	require.NoError(t, second.Settle(-2))
	require.Equal(t, 3, first.ReservedQuota(), "the other object is deliberately stale")
	require.NoError(t, first.Reserve(3))
	requireAllocationBalances(t, db, request.UserID, 0, 0, 0)
	require.Equal(t, []moneyport.AccountShare{{Account: "bonus", Amount: 1}, {Account: "reward", Amount: 1}, {Account: "paid", Amount: 1}}, first.Receipt())
	require.NoError(t, first.Reserve(3))
	require.NoError(t, first.Refund())
	requireAllocationBalances(t, db, request.UserID, 1, 1, 1)
}

func TestFundingAllocationRollbackDoesNotChangeReceipt(t *testing.T) {
	db, funding, request := seedAllocationFunding(t, 1, 1, 1)
	require.NoError(t, funding.PreConsume(3))
	before := funding.Receipt()
	injected := errors.New("injected owner write failure after wallet credit")
	err := db.Transaction(func(tx *gorm.DB) error {
		result, err := funding.SettleInTx(tx, -2)
		require.NoError(t, err)
		require.Equal(t, []moneyport.AccountShare{{Account: "paid", Amount: 1}}, result.OutstandingShares)
		require.Equal(t, before, funding.Receipt(), "uncommitted result must not mutate the in-memory receipt")
		return injected
	})
	require.ErrorIs(t, err, injected)
	requireAllocationBalances(t, db, request.UserID, 0, 0, 0)
	require.Equal(t, before, funding.Receipt())
	var credits int64
	require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Where("operation = ?", "credit").Count(&credits).Error)
	require.Zero(t, credits)
	require.NoError(t, funding.Settle(-2))
	require.NoError(t, funding.Refund())
	requireAllocationBalances(t, db, request.UserID, 1, 1, 1)
}

func TestFundingAllocationIndependentDebitsAreNotRefunded(t *testing.T) {
	for _, purpose := range []string{"explicit_fee", "realtime_segment"} {
		t.Run(purpose, func(t *testing.T) {
			db, funding, request := seedAllocationFunding(t, 1, 1, 5)
			require.NoError(t, funding.PreConsume(3))
			entry := model.LedgerEntry{UserId: request.UserID, Delta: 2, Reason: model.LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: request.RequestID, RequestId: request.RequestID, IdempotencyKey: fmt.Sprintf("billing:settle:%s:2", request.RequestID)}
			if purpose == "explicit_fee" {
				entry.SetMetadata(map[string]any{"independent_fee": "violation_fee"})
			} else {
				entry.IdempotencyKey = fmt.Sprintf("billing:settle:%s:wss:1:2", request.RequestID)
			}
			_, err := model.DebitQuota(entry, model.AccountPaid)
			require.NoError(t, err)
			require.NoError(t, funding.Refund())
			requireAllocationBalances(t, db, request.UserID, 1, 1, 3)
		})
	}
}

func TestFundingAllocationUnknownAndOvercreditedHistoryFailsClosed(t *testing.T) {
	for _, history := range []string{"unknown_key", "legacy_overcredit"} {
		t.Run(history, func(t *testing.T) {
			db, funding, request := seedAllocationFunding(t, 1, 1, 2)
			require.NoError(t, funding.PreConsume(3))
			if history == "unknown_key" {
				_, err := model.DebitQuota(model.LedgerEntry{UserId: request.UserID, Delta: 1, Reason: model.LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: request.RequestID, RequestId: request.RequestID, IdempotencyKey: "unknown-historical-fee"}, model.AccountPaid)
				require.NoError(t, err)
			} else {
				entry := model.LedgerEntry{UserId: request.UserID, Account: model.AccountPaid, Reason: model.LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: request.RequestID, RequestId: request.RequestID, IdempotencyKey: fmt.Sprintf("billing:settle_refund:%s:2", request.RequestID)}
				var liveBefore, liveAfter model.User
				require.NoError(t, db.First(&liveBefore, request.UserID).Error)
				var ledgerBefore, operationsBefore, ledgerAfter, operationsAfter int64
				require.NoError(t, db.Model(&model.BalanceLedger{}).Count(&ledgerBefore).Error)
				require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&operationsBefore).Error)
				_, err := model.CreditQuotaShares(entry, []model.LedgerShare{{Account: model.AccountBonus, Amount: 2}})
				require.ErrorIs(t, err, ErrMissingDebitReceipt, "the live source guard must reject bonus 2 refunded against bonus 1")
				require.NoError(t, db.First(&liveAfter, request.UserID).Error)
				require.Equal(t, liveBefore, liveAfter)
				require.NoError(t, db.Model(&model.BalanceLedger{}).Count(&ledgerAfter).Error)
				require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&operationsAfter).Error)
				require.Equal(t, ledgerBefore, ledgerAfter)
				require.Equal(t, operationsBefore, operationsAfter)

				// Reproduce the corrupt history a pre-cutover binary could leave,
				// using direct records ONLY inside this disposable fixture. Never
				// weaken the live API to manufacture an impossible new overcredit.
				// All canonical identities and row totals remain valid: only the
				// historical bonus refund exceeds its original one-unit debit.
				signature := sha256.Sum256([]byte(fmt.Sprintf("op=credit|user=%d|account=paid|target=|delta=2|order=|shares=bonus:2|reason=%s|ref_type=relay|ref_id=%s|operator=0", request.UserID, entry.Reason, request.RequestID)))
				operation := model.BalanceLedgerOperation{IdempotencyKey: entry.IdempotencyKey, UserId: request.UserID, Operation: "credit", OperationSignature: fmt.Sprintf("%x", signature), TotalBefore: liveBefore.Quota, TotalAfter: liveBefore.Quota + 2}
				require.NoError(t, db.Create(&operation).Error)
				require.NoError(t, db.Create(&model.BalanceLedger{OperationId: operation.Id, UserId: request.UserID, Account: string(model.AccountBonus), Delta: 2, BalanceBefore: liveBefore.QuotaBonus, BalanceAfter: liveBefore.QuotaBonus + 2, TotalBefore: operation.TotalBefore, TotalAfter: operation.TotalAfter, Reason: entry.Reason, ReferenceType: entry.ReferenceType, ReferenceId: entry.ReferenceId, RequestId: entry.RequestId, IdempotencyKey: entry.IdempotencyKey}).Error)
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", request.UserID).Updates(map[string]any{"quota": operation.TotalAfter, "quota_bonus": liveBefore.QuotaBonus + 2}).Error)
				requireAllocationBalances(t, db, request.UserID, 2, 0, 1)
			}
			var before model.User
			require.NoError(t, db.First(&before, request.UserID).Error)
			var rowsBefore int64
			require.NoError(t, db.Model(&model.BalanceLedger{}).Count(&rowsBefore).Error)
			err := funding.Refund()
			require.ErrorIs(t, err, ErrMissingDebitReceipt)
			if history == "legacy_overcredit" {
				require.ErrorContains(t, err, "historical wallet refund exceeds source")
			}
			var after model.User
			require.NoError(t, db.First(&after, request.UserID).Error)
			require.Equal(t, before, after)
			var rowsAfter int64
			require.NoError(t, db.Model(&model.BalanceLedger{}).Count(&rowsAfter).Error)
			require.Equal(t, rowsBefore, rowsAfter)
		})
	}
}

func TestFundingReservationInTxRollbackLeavesNoReceipt(t *testing.T) {
	db, funding, request := seedAllocationFunding(t, 1, 1, 1)
	injected := errors.New("injected token reservation failure")
	err := db.Transaction(func(tx *gorm.DB) error {
		result, err := funding.PreConsumeInTx(tx, 2)
		require.NoError(t, err)
		require.Equal(t, 2, result.SourceHeld)
		require.Empty(t, funding.Receipt())
		return injected
	})
	require.ErrorIs(t, err, injected)
	require.Empty(t, funding.Receipt())
	requireAllocationBalances(t, db, request.UserID, 1, 1, 1)
	var operations int64
	require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&operations).Error)
	require.Zero(t, operations)
	require.NoError(t, funding.PreConsume(2))
	before := funding.Receipt()
	err = db.Transaction(func(tx *gorm.DB) error {
		result, err := funding.ReserveInTx(tx, 3)
		require.NoError(t, err)
		require.Equal(t, 3, result.SourceHeld)
		require.Equal(t, before, funding.Receipt())
		return injected
	})
	require.ErrorIs(t, err, injected)
	require.Equal(t, before, funding.Receipt())
	requireAllocationBalances(t, db, request.UserID, 0, 0, 1)
}
