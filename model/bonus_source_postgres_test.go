package model

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBonusSourcePostgres(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6; acceptance must have zero skips")
	}
	setupLedgerTestState(t)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(BonusSourceModels()...))
	}
	for _, schema := range BonusSourceModels() {
		statement := &gorm.Statement{DB: DB}
		require.NoError(t, statement.Parse(schema))
		require.NoError(t, DB.Exec("DELETE FROM "+statement.Schema.Table).Error)
	}
}
func sourceGrant(t *testing.T, userID, quota int, expiry int64, key string) *BonusSourceLot {
	t.Helper()
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := CreditQuotaTx(tx, LedgerEntry{UserId: userID, Account: AccountBonus, Delta: quota, Reason: "campaign_grant", IdempotencyKey: key, bonusGrant: &bonusSourceGrant{CampaignID: 1, ClaimID: 1, ExpiresAt: expiry, Revocable: true}})
		return err
	}))
	var lot BonusSourceLot
	require.NoError(t, DB.Order("id DESC").First(&lot).Error)
	return &lot
}
func sourceState(t *testing.T, id int64) BonusSourceState {
	t.Helper()
	var s BonusSourceState
	require.NoError(t, DB.First(&s, id).Error)
	return s
}
func sourceDebit(t *testing.T, userID, quota int, request string) {
	t.Helper()
	_, err := DebitQuota(LedgerEntry{UserId: userID, Delta: quota, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:preconsume:" + request}, AccountBonus, AccountReward, AccountPaid)
	require.NoError(t, err)
}
func sourceRefund(t *testing.T, userID, quota int, request string) {
	t.Helper()
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, _, err := CreditWalletRefundTx(tx, LedgerEntry{UserId: userID, Reason: LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: fmt.Sprintf("billing:settle_refund:%s:%d", request, quota)}, quota)
		return err
	}))
}

func TestBonusSourceBackfillIdempotentAndAnomaliesPostgres(t *testing.T) {
	setupBonusSourcePostgres(t)
	u := User{Username: "source-legacy", AffCode: "source-legacy", Status: common.UserStatusEnabled, Quota: 180, QuotaPaid: 50, QuotaBonus: 100, QuotaReward: 30}
	require.NoError(t, DB.Create(&u).Error)
	for range 2 {
		_, err := ReconcileBonusSources(t.Context(), 0, 100)
		require.NoError(t, err)
	}
	var lots []BonusSourceLot
	require.NoError(t, DB.Where("user_id = ?", u.Id).Find(&lots).Error)
	require.Len(t, lots, 1)
	assert.Equal(t, "legacy_nonexpiring", lots[0].Kind)
	assert.Zero(t, lots[0].ExpiresAt)
	assert.Equal(t, 100, sourceState(t, lots[0].ID).Available)
	assert.Equal(t, 180, getUserBalances(t, u.Id).Quota)
	var ledgerCount int64
	require.NoError(t, DB.Model(&BalanceLedger{}).Count(&ledgerCount).Error)
	assert.Zero(t, ledgerCount)
	require.NoError(t, DB.Model(&u).Updates(map[string]any{"quota_bonus": 101, "quota": 181}).Error)
	negative := User{Username: "source-negative", AffCode: "source-negative", Quota: -4, QuotaBonus: -4}
	require.NoError(t, DB.Create(&negative).Error)
	orphan := BonusSourceLot{UserID: 999999, SourceKey: "orphan-fixture", Kind: "legacy_nonexpiring", GrantQuota: 7, RefundTerms: "restore_original_expiry"}
	require.NoError(t, DB.Create(&orphan).Error)
	for range 2 {
		_, err := ReconcileBonusSources(t.Context(), 0, 100)
		require.NoError(t, err)
	}
	var anomalies []BonusSourceAnomaly
	require.NoError(t, DB.Find(&anomalies).Error)
	assert.Len(t, anomalies, 3)
	assert.Equal(t, 101, getUserBalances(t, u.Id).QuotaBonus)
	assert.Equal(t, -4, getUserBalances(t, negative.Id).QuotaBonus)
	_, err := DebitQuota(LedgerEntry{UserId: u.Id, Delta: 1, Reason: "test"}, AccountBonus)
	require.ErrorIs(t, err, ErrBonusSourceInvariant)
}

func TestBonusSourcePartialRefundExpiryAndImmediatePaidFallbackPostgres(t *testing.T) {
	setupBonusSourcePostgres(t)
	now := common.GetTimestamp()
	oldClock := bonusSourceNow
	t.Cleanup(func() { bonusSourceNow = oldClock })
	bonusSourceNow = func() int64 { return now }
	u := User{Username: "source-expiry", AffCode: "source-expiry", Status: common.UserStatusEnabled, Quota: 400, QuotaPaid: 100, QuotaBonus: 200, QuotaReward: 100}
	require.NoError(t, DB.Create(&u).Error)
	lot := sourceGrant(t, u.Id, 100, now+100, "campaign-expiring")
	sourceDebit(t, u.Id, 80, "source-held")
	assert.Equal(t, BonusSourceState{LotID: lot.ID, UserID: u.Id, Available: 20, Outstanding: 80}, sourceState(t, lot.ID))
	sourceRefund(t, u.Id, 30, "source-held")
	assert.Equal(t, 50, sourceState(t, lot.ID).Available)
	assert.Equal(t, 50, sourceState(t, lot.ID).Outstanding)
	bonusSourceNow = func() int64 { return now + 200 }
	// The next account plan expires only this campaign's 50 available quota.
	_, err := DebitQuota(LedgerEntry{UserId: u.Id, Delta: 210, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: "after-expiry", RequestId: "after-expiry", IdempotencyKey: "billing:preconsume:after-expiry"}, AccountBonus, AccountReward, AccountPaid)
	require.NoError(t, err)
	user := getUserBalances(t, u.Id)
	assert.Equal(t, 0, user.QuotaBonus)
	assert.Equal(t, 90, user.QuotaReward)
	assert.Equal(t, 100, user.QuotaPaid)
	assertQuotaInvariant(t, user)
	// Original source restored despite expiry, then synchronously expired before
	// the immediately following debit; unrelated reward and paid stay usable.
	sourceRefund(t, u.Id, 50, "source-held")
	_, err = DebitQuota(LedgerEntry{UserId: u.Id, Delta: 100, Reason: "test", IdempotencyKey: "consume-after-expired-refund"}, AccountBonus, AccountReward, AccountPaid)
	require.NoError(t, err)
	state := sourceState(t, lot.ID)
	assert.Zero(t, state.Available)
	assert.Zero(t, state.Outstanding)
	assert.Equal(t, 100, state.Expired)
	user = getUserBalances(t, u.Id)
	assert.Equal(t, 90, user.QuotaPaid)
	assert.Zero(t, user.QuotaBonus)
	assert.Zero(t, user.QuotaReward)
	assertQuotaInvariant(t, user)
	// Exact replay cannot manufacture another refund or new source.
	sourceRefund(t, u.Id, 50, "source-held")
	assert.Equal(t, 90, getUserBalances(t, u.Id).Quota)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		user := getUserBalances(t, u.Id)
		return ensureBonusSourceAccountTx(tx, &user)
	}))
}

func TestBonusSourceConcurrentDebitsAndRefundCeilingPostgres(t *testing.T) {
	setupBonusSourcePostgres(t)
	createLedgerTestUser(t, 8181, 0)
	lot := sourceGrant(t, 8181, 100, common.GetTimestamp()+3600, "parallel-source")
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"parallel-a", "parallel-b"} {
		wg.Go(func() {
			<-start
			_, err := DebitQuota(LedgerEntry{UserId: 8181, Delta: 70, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: key, RequestId: key, IdempotencyKey: "billing:preconsume:" + key}, AccountBonus)
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	success, insufficient := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrLedgerInsufficientBalance)
			insufficient++
		}
	}
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, insufficient)
	assert.Equal(t, 30, sourceState(t, lot.ID).Available)
	var allocation BonusSourceAllocation
	require.NoError(t, DB.Where("lot_id = ?", lot.ID).First(&allocation).Error)
	entry := LedgerEntry{UserId: 8181, Reason: LedgerReasonBillingRefund, ReferenceType: "relay", ReferenceId: allocation.ReferenceID, RequestId: allocation.ReferenceID, IdempotencyKey: "billing:refund:" + allocation.ReferenceID}
	for range 2 {
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { _, _, err := CreditWalletRefundTx(tx, entry, 0); return err }))
	}
	assert.Equal(t, 100, sourceState(t, lot.ID).Available)
	_, err := CreditQuota(LedgerEntry{UserId: 8181, Account: AccountBonus, Delta: 1, Reason: LedgerReasonBillingRefund, ReferenceType: "relay", ReferenceId: allocation.ReferenceID, IdempotencyKey: "forged-extra-refund"})
	require.ErrorIs(t, err, ErrMissingWalletDebitReceipt)
	assert.Equal(t, 100, getUserBalances(t, 8181).QuotaBonus)
	_, err = TransferQuota(LedgerEntry{UserId: 8181, Account: AccountBonus, Delta: 1, Reason: "test"}, AccountPaid)
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
}

func TestBonusSourceUnknownHoldAndTaskOriginalMappingPostgres(t *testing.T) {
	setupBonusSourcePostgres(t)
	require.NoError(t, DB.AutoMigrate(&BillingFinalization{}))
	createLedgerTestUser(t, 8182, 0)
	lot := sourceGrant(t, 8182, 100, common.GetTimestamp()+100, "unknown-source")
	sourceDebit(t, 8182, 80, "unknown-source-request")
	owner := BillingFinalization{RequestID: "unknown-source-request", UserID: 8182, Source: "wallet", SourceHeld: 80, PreConsumed: 80, Bonus: 80, State: BillingDeliveryUnknown}
	require.NoError(t, DB.Create(&owner).Error)
	_, err := RequestBillingRefund(context.Background(), owner)
	require.ErrorIs(t, err, ErrBillingFinalized)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, 8182).Error; err != nil {
			return err
		}
		return expireBonusSourcesTx(tx, &user, common.GetTimestamp()+200)
	}))
	assert.Equal(t, 80, sourceState(t, lot.ID).Outstanding)
	assert.Equal(t, 20, sourceState(t, lot.ID).Expired)
	require.NoError(t, DB.First(&owner, "request_id = ?", owner.RequestID).Error)
	assert.Equal(t, BillingDeliveryUnknown, owner.State)
	// A separate task's refund follows its persisted original relay request, not
	// the unrelated still-UNKNOWN hold or an arbitrary present wallet balance.
	lot2 := sourceGrant(t, 8182, 60, common.GetTimestamp()+1000, "task-source")
	sourceDebit(t, 8182, 60, "task-original")
	task := Task{TaskID: "source-task", UserId: 8182, Quota: 60, PrivateData: TaskPrivateData{RequestId: "task-original"}}
	require.NoError(t, DB.Create(&task).Error)
	_, err = CreditQuotaShares(LedgerEntry{UserId: 8182, Reason: LedgerReasonTaskRefund, ReferenceType: "task", ReferenceId: task.TaskID, RequestId: task.TaskID, IdempotencyKey: "task:refund:" + task.TaskID}, []LedgerShare{{Account: AccountBonus, Amount: 60}})
	require.NoError(t, err)
	assert.Equal(t, 60, sourceState(t, lot2.ID).Available)
	assert.Equal(t, 80, sourceState(t, lot.ID).Outstanding)
}

func TestBonusSourceHistoricalRefundCutoverAndMissingSchemaFailClosedPostgres(t *testing.T) {
	setupBonusSourcePostgres(t)
	user := User{Username: "historical-source", AffCode: "historical-source", Status: common.UserStatusEnabled, Quota: 100, QuotaBonus: 100}
	require.NoError(t, DB.Create(&user).Error)
	request := "historical-source-request"
	debitEntry := LedgerEntry{UserId: user.Id, Delta: 70, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:preconsume:" + request, DebitOrder: []LedgerAccount{AccountBonus}}
	debit := BalanceLedgerOperation{IdempotencyKey: debitEntry.IdempotencyKey, UserId: user.Id, Operation: "debit", OperationSignature: ledgerOperationSignature(debitEntry, ledgerOpDebit), TotalBefore: 150, TotalAfter: 80}
	require.NoError(t, DB.Create(&debit).Error)
	require.NoError(t, DB.Create(&BalanceLedger{OperationId: debit.Id, UserId: user.Id, Account: "bonus", Delta: -70, BalanceBefore: 150, BalanceAfter: 80, TotalBefore: 150, TotalAfter: 80, Reason: debitEntry.Reason, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: debit.IdempotencyKey}).Error)
	creditEntry := LedgerEntry{UserId: user.Id, Delta: 20, Reason: LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:settle_refund:" + request + ":20", CreditShares: []LedgerShare{{Account: AccountBonus, Amount: 20}}}
	credit := BalanceLedgerOperation{IdempotencyKey: creditEntry.IdempotencyKey, UserId: user.Id, Operation: "credit", OperationSignature: ledgerOperationSignature(creditEntry, ledgerOpCredit), TotalBefore: 80, TotalAfter: 100}
	require.NoError(t, DB.Create(&credit).Error)
	require.NoError(t, DB.Create(&BalanceLedger{OperationId: credit.Id, UserId: user.Id, Account: "bonus", Delta: 20, BalanceBefore: 80, BalanceAfter: 100, TotalBefore: 80, TotalAfter: 100, Reason: creditEntry.Reason, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: credit.IdempotencyKey}).Error)
	for range 2 {
		_, err := ReconcileBonusSources(t.Context(), 0, 100)
		require.NoError(t, err)
	}
	sourceRefund(t, user.Id, 50, request)
	var lot BonusSourceLot
	require.NoError(t, DB.Where("kind = ? AND user_id = ?", "legacy_debit", user.Id).First(&lot).Error)
	assert.Zero(t, lot.ExpiresAt)
	state := sourceState(t, lot.ID)
	assert.Equal(t, 50, state.Available)
	assert.Equal(t, 20, state.LegacyRestored)
	assert.Zero(t, state.Outstanding)
	assert.Equal(t, 150, getUserBalances(t, user.Id).QuotaBonus)
	for range 2 {
		_, err := ReconcileBonusSources(t.Context(), 0, 100)
		require.NoError(t, err)
	}
	var anomalies int64
	require.NoError(t, DB.Model(&BonusSourceAnomaly{}).Count(&anomalies).Error)
	assert.Zero(t, anomalies)
	// Removing a required schema table cannot turn tracking off. PostgreSQL DDL
	// rolls back together with the rejected wallet operation in this fixture.
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DROP TABLE bonus_source_accounts").Error; err != nil {
			return err
		}
		_, err := CreditQuotaTx(tx, LedgerEntry{UserId: user.Id, Account: AccountPaid, Delta: 1, Reason: "test", IdempotencyKey: "schema-missing-credit"})
		return err
	})
	require.Error(t, err)
	assert.Equal(t, 150, getUserBalances(t, user.Id).Quota)
	var operations int64
	require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", "schema-missing-credit").Count(&operations).Error)
	assert.Zero(t, operations)
}

func TestBonusSourceRealtimeIndependentFeeAndOverrideProvenancePostgres(t *testing.T) {
	setupBonusSourcePostgres(t)
	createLedgerTestUser(t, 8183, 0)
	lot := sourceGrant(t, 8183, 200, common.GetTimestamp()+3600, "source-independent")
	fee := LedgerEntry{UserId: 8183, Delta: 30, Reason: LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: "independent-request", RequestId: "independent-request", IdempotencyKey: "billing:settle:independent-request:30"}
	fee.SetMetadata(map[string]any{"independent_fee": "violation_fee"})
	_, err := DebitQuota(fee, AccountBonus)
	require.NoError(t, err)
	sourceDebit(t, 8183, 70, "independent-request")
	sourceRefund(t, 8183, 70, "independent-request")
	state := sourceState(t, lot.ID)
	assert.Equal(t, 170, state.Available)
	assert.Equal(t, 30, state.Outstanding)
	// Independent realtime segment is likewise not a refundable request hold.
	_, err = DebitQuota(LedgerEntry{UserId: 8183, Delta: 20, Reason: LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: "wss-request", RequestId: "wss-request", IdempotencyKey: "billing:settle:wss-request:wss:1:20"}, AccountBonus)
	require.NoError(t, err)
	sourceDebit(t, 8183, 40, "wss-request")
	sourceRefund(t, 8183, 40, "wss-request")
	assert.Equal(t, 50, sourceState(t, lot.ID).Outstanding)
	_, err = OverrideQuota(LedgerEntry{UserId: 8183, Account: AccountBonus, Delta: 100, Reason: LedgerReasonAdminOverride, IdempotencyKey: "source-admin-override"})
	require.NoError(t, err)
	state = sourceState(t, lot.ID)
	assert.Equal(t, 100, state.Available)
	assert.Equal(t, 50, state.Outstanding)
	assert.Equal(t, 50, state.Revoked)
	var removals []BonusSourceRemoval
	require.NoError(t, DB.Where("lot_id = ?", lot.ID).Find(&removals).Error)
	require.Len(t, removals, 1)
	assert.Equal(t, LedgerReasonAdminOverride, removals[0].Kind)
	assert.Equal(t, 50, removals[0].Quota)
}

func TestBonusSourceWalletBoundsNeverOverflowOrChangeUnrelatedAccountsPostgres(t *testing.T) {
	setupBonusSourcePostgres(t)
	user := User{Username: "source-boundary", AffCode: "source-boundary", Status: common.UserStatusEnabled, Quota: common.MaxWalletQuota, QuotaPaid: common.MaxWalletQuota - 20, QuotaBonus: 10, QuotaReward: 10}
	require.NoError(t, DB.Create(&user).Error)
	maxInt := int(^uint(0) >> 1)
	for _, account := range []LedgerAccount{AccountPaid, AccountBonus, AccountReward} {
		_, err := CreditQuota(LedgerEntry{UserId: user.Id, Account: account, Delta: maxInt, Reason: "oversized-credit", IdempotencyKey: "overflow-" + string(account)})
		require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
		_, err = OverrideQuota(LedgerEntry{UserId: user.Id, Account: account, Delta: common.MaxWalletQuota, Reason: LedgerReasonAdminOverride, IdempotencyKey: "override-" + string(account)})
		require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	}
	_, err := CreditQuotaShares(LedgerEntry{UserId: user.Id, Reason: "oversized-shares"}, []LedgerShare{{Account: AccountBonus, Amount: maxInt}, {Account: AccountPaid, Amount: maxInt}})
	require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	stored := getUserBalances(t, user.Id)
	assert.Equal(t, user.Quota, stored.Quota)
	assert.Equal(t, user.QuotaPaid, stored.QuotaPaid)
	assert.Equal(t, user.QuotaBonus, stored.QuotaBonus)
	assert.Equal(t, user.QuotaReward, stored.QuotaReward)
	var operations int64
	require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Where("user_id = ?", user.Id).Count(&operations).Error)
	assert.Zero(t, operations)
}

func TestBonusSourceLegacyCrossReferenceRefundCutoverPostgres(t *testing.T) {
	for _, variant := range []string{"task", "task_to_relay", "mj_task", "mj_numeric_to_public"} {
		t.Run(variant, func(t *testing.T) {
			refType := "task"
			if variant == "mj_task" || variant == "mj_numeric_to_public" {
				refType = "mj_task"
			}
			priorReference := ""
			priorRequest := ""
			setupBonusSourcePostgres(t)
			user := User{Username: "review-legacy-" + refType, AffCode: "review-legacy-" + refType, Status: common.UserStatusEnabled, Quota: 40, QuotaBonus: 40}
			require.NoError(t, DB.Create(&user).Error)
			original := "review-original-" + refType
			business := "review-task-" + refType
			if refType == "task" {
				task := Task{TaskID: business, UserId: user.Id, Quota: 60, PrivateData: TaskPrivateData{RequestId: original}}
				require.NoError(t, DB.Create(&task).Error)
			} else {
				require.NoError(t, DB.AutoMigrate(&Midjourney{}))
				mj := Midjourney{MjId: business, UserId: user.Id, Quota: 60, BillingRequestID: original, BillingOriginalQuota: 100}
				require.NoError(t, DB.Create(&mj).Error)
				if variant == "mj_numeric_to_public" {
					priorReference = fmt.Sprint(mj.Id)
				}
			}
			debitEntry := LedgerEntry{UserId: user.Id, Delta: 100, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: original, RequestId: original, IdempotencyKey: "billing:preconsume:" + original, DebitOrder: []LedgerAccount{AccountBonus}}
			debit := BalanceLedgerOperation{IdempotencyKey: debitEntry.IdempotencyKey, UserId: user.Id, Operation: "debit", OperationSignature: ledgerOperationSignature(debitEntry, ledgerOpDebit), TotalBefore: 100, TotalAfter: 0}
			require.NoError(t, DB.Create(&debit).Error)
			require.NoError(t, DB.Create(&BalanceLedger{OperationId: debit.Id, UserId: user.Id, Account: "bonus", Delta: -100, BalanceBefore: 100, BalanceAfter: 0, TotalBefore: 100, TotalAfter: 0, Reason: debitEntry.Reason, ReferenceType: "relay", ReferenceId: original, RequestId: original, IdempotencyKey: debit.IdempotencyKey}).Error)
			if priorReference == "" {
				priorReference = business
				priorRequest = business
			}
			prior := LedgerEntry{UserId: user.Id, Delta: 40, Reason: LedgerReasonTaskRefund, ReferenceType: refType, ReferenceId: priorReference, RequestId: priorRequest, IdempotencyKey: "review-historical-refund:" + refType, CreditShares: []LedgerShare{{Account: AccountBonus, Amount: 40}}}
			credit := BalanceLedgerOperation{IdempotencyKey: prior.IdempotencyKey, UserId: user.Id, Operation: "credit", OperationSignature: ledgerOperationSignature(prior, ledgerOpCredit), TotalBefore: 0, TotalAfter: 40}
			require.NoError(t, DB.Create(&credit).Error)
			require.NoError(t, DB.Create(&BalanceLedger{OperationId: credit.Id, UserId: user.Id, Account: "bonus", Delta: 40, BalanceBefore: 0, BalanceAfter: 40, TotalBefore: 0, TotalAfter: 40, Reason: prior.Reason, ReferenceType: refType, ReferenceId: priorReference, RequestId: priorRequest, IdempotencyKey: credit.IdempotencyKey}).Error)
			_, err := ReconcileBonusSources(t.Context(), 0, 100)
			require.NoError(t, err)
			final := LedgerEntry{UserId: user.Id, Reason: LedgerReasonTaskRefund, ReferenceType: refType, ReferenceId: business, RequestId: business, IdempotencyKey: "review-final-refund:" + refType}
			if variant == "task_to_relay" {
				final.ReferenceType = "relay"
				final.ReferenceId = original
				final.RequestId = original
			}
			for range 2 {
				_, err = CreditQuotaShares(final, []LedgerShare{{Account: AccountBonus, Amount: 60}})
				require.NoError(t, err)
			}
			var lot BonusSourceLot
			require.NoError(t, DB.Where("kind = ? AND user_id = ?", "legacy_debit", user.Id).First(&lot).Error)
			state := sourceState(t, lot.ID)
			assert.Zero(t, lot.ExpiresAt)
			assert.Equal(t, 60, state.Available)
			var allocation BonusSourceAllocation
			require.NoError(t, DB.Where("lot_id = ?", lot.ID).First(&allocation).Error)
			assert.Equal(t, 100, allocation.Amount)
			assert.Equal(t, 100, allocation.Refunded)
			var restorations []BonusSourceRestoration
			require.NoError(t, DB.Where("allocation_id = ?", allocation.ID).Order("ledger_id").Find(&restorations).Error)
			require.Len(t, restorations, 2)
			assert.Equal(t, 40, restorations[0].Quota)
			assert.Equal(t, 60, restorations[1].Quota)
			_, err = CreditQuotaShares(final, []LedgerShare{{Account: AccountBonus, Amount: 59}})
			require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)
			for range 2 {
				_, err = ReconcileBonusSources(t.Context(), 0, 100)
				require.NoError(t, err)
			}
			assert.Equal(t, 40, state.LegacyRestored, "historical task/MJ refund was already in the baseline and must not remain refundable")
			assert.Zero(t, state.Outstanding, "fully refunded original source cannot retain phantom outstanding quota")
			assert.Equal(t, 100, getUserBalances(t, user.Id).QuotaBonus)
			extra := final
			extra.IdempotencyKey = "review-illegal-extra-refund:" + refType
			_, err = CreditQuotaShares(extra, []LedgerShare{{Account: AccountBonus, Amount: 1}})
			assert.ErrorIs(t, err, ErrMissingWalletDebitReceipt, "the central bonus source ceiling must include historical cross-reference refunds")
			assert.Equal(t, 100, getUserBalances(t, user.Id).QuotaBonus, "no source-less overcredit after original cumulative refund reached 100")
		})
	}
}

func TestBonusSourceMJAmbiguousLegacyAliasesFailClosedPostgres(t *testing.T) {
	setupBonusSourcePostgres(t)
	require.NoError(t, DB.AutoMigrate(&Midjourney{}))
	createLedgerTestUser(t, 8190, 0)
	lot := sourceGrant(t, 8190, 100, 0, "mj-alias-source")
	sourceDebit(t, 8190, 100, "mj-alias-original")
	original := Midjourney{UserId: 8190, MjId: "mj-public-original", BillingRequestID: "mj-alias-original"}
	require.NoError(t, DB.Create(&original).Error)
	collision := Midjourney{UserId: 8190, MjId: fmt.Sprint(original.Id), BillingRequestID: "unrelated-mj-original"}
	require.NoError(t, DB.Create(&collision).Error)
	_, err := CreditQuotaShares(LedgerEntry{UserId: 8190, Reason: LedgerReasonTaskRefund, ReferenceType: "mj_task", ReferenceId: original.MjId, RequestId: original.MjId, IdempotencyKey: "mj-alias-ambiguous-refund"}, []LedgerShare{{Account: AccountBonus, Amount: 100}})
	require.ErrorIs(t, err, ErrBonusSourceInvariant)
	assert.Zero(t, getUserBalances(t, 8190).QuotaBonus)
	assert.Equal(t, 100, sourceState(t, lot.ID).Outstanding)
	var restored int64
	require.NoError(t, DB.Model(&BonusSourceRestoration{}).Count(&restored).Error)
	assert.Zero(t, restored)
}

func TestBonusSourceNewRefundIndependentOfUnrelatedLegacyOrphanPostgres(t *testing.T) {
	setupBonusSourcePostgres(t)
	createLedgerTestUser(t, 8191, 0)
	// Deliberately orphaned pre-cutover history must not become entitlement.
	require.NoError(t, DB.Create(&BalanceLedger{UserId: 8191, Account: "bonus", Delta: 4, Reason: LedgerReasonTaskRefund, ReferenceType: "task", ReferenceId: "missing-unrelated-old-task", RequestId: "missing-unrelated-old-task", IdempotencyKey: "orphan-old-task-refund"}).Error)
	lot := sourceGrant(t, 8191, 100, 0, "independent-new-source")
	sourceDebit(t, 8191, 70, "independent-new-request")
	for range 2 {
		sourceRefund(t, 8191, 70, "independent-new-request")
	}
	assert.Equal(t, 100, getUserBalances(t, 8191).QuotaBonus)
	assert.Equal(t, 100, sourceState(t, lot.ID).Available)
	assert.Zero(t, sourceState(t, lot.ID).Outstanding)
	var oldLots int64
	require.NoError(t, DB.Model(&BonusSourceLot{}).Where("user_id = ? AND kind = ?", 8191, "legacy_debit").Count(&oldLots).Error)
	assert.Zero(t, oldLots, "unrelated untrusted legacy history must never mint sources or wallet credits")
}
