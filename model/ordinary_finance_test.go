package model

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"sync"
	"testing"
)

func TestOrdinaryTopupPreservesPrincipalAndRetriesRewardSnapshot(t *testing.T) {
	setupLedgerTestState(t)
	require.NoError(t, DB.AutoMigrate(&PaymentReceipt{}))
	require.NoError(t, DB.Exec("DELETE FROM agent_payment_receipts").Error)
	previousEnabled, previousRate := common.AffiliateRewardEnabled, common.AffiliateRewardRate
	common.AffiliateRewardEnabled, common.AffiliateRewardRate = true, 0.05
	t.Cleanup(func() { common.AffiliateRewardEnabled, common.AffiliateRewardRate = previousEnabled, previousRate })
	inviter := User{Id: 97101, Username: "ordinary-reward-inviter", AffCode: "ordinary-inviter", Status: common.UserStatusEnabled, AffQuota: common.MaxWalletQuota}
	buyer := User{Id: 97102, Username: "ordinary-reward-buyer", AffCode: "ordinary-buyer", Status: common.UserStatusEnabled, InviterId: inviter.Id}
	require.NoError(t, DB.Create(&inviter).Error)
	require.NoError(t, DB.Create(&buyer).Error)
	order := TopUp{UserId: buyer.Id, TradeNo: "ordinary-atomic-topup", Amount: 2, Money: 2, PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay", Status: common.TopUpStatusPending}
	require.NoError(t, DB.Create(&order).Error)
	facts, ok := NewVerifiedPaymentFacts(PaymentProviderEpay, "synthetic-provider-payment", "synthetic-merchant", "live", "CNY", "2.00", false)
	require.True(t, ok)
	_, err := RechargeEpay(order.TradeNo, "alipay", "2.00", "127.0.0.1", facts)
	require.NoError(t, err, "invalid inviter balance cannot block verified customer principal")
	var after TopUp
	require.NoError(t, DB.First(&after, order.Id).Error)
	assert.Equal(t, common.TopUpStatusSuccess, after.Status)
	assert.Positive(t, after.PaymentReceiptID)
	assert.Equal(t, 1_000_000, getUserBalances(t, buyer.Id).QuotaPaid)
	_, err = ProcessAgentCommissionOutbox(t.Context(), 100)
	require.ErrorIs(t, err, ErrAgentCommissionInvariant)
	var event AgentCommissionOutbox
	require.NoError(t, DB.Where("reference_id = ?", order.TradeNo).First(&event).Error)
	assert.Equal(t, "failed", event.Status)
	originalPayload := event.Payload
	require.NoError(t, DB.Model(&User{}).Where("id = ?", inviter.Id).Update("aff_quota", 0).Error)
	common.AffiliateRewardRate = 0.5 // Retry must use payment-time terms, not this later rate.
	require.NoError(t, DB.Model(&event).Update("next_attempt_at", 0).Error)
	for range 2 {
		_, err = ProcessAgentCommissionOutbox(t.Context(), 100)
		require.NoError(t, err)
	}
	done, err := RechargeEpay(order.TradeNo, "alipay", "2.00", "127.0.0.1", facts)
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, 1_000_000, getUserBalances(t, buyer.Id).QuotaPaid)
	assert.Equal(t, 30_000, getUserBalances(t, inviter.Id).AffQuota)
	assert.Equal(t, 30_000, getUserBalances(t, inviter.Id).AffHistoryQuota)
	require.NoError(t, DB.First(&event, event.ID).Error)
	assert.Equal(t, originalPayload, event.Payload)
	assert.Equal(t, "done", event.Status)
	for _, schema := range []any{&PaymentReceipt{}, &AgentCommissionInbox{}, &ReferralCommission{}} {
		var count int64
		require.NoError(t, DB.Model(schema).Count(&count).Error)
		assert.EqualValues(t, 1, count)
	}
	other := order
	other.Id, other.TradeNo = 0, "ordinary-reused-provider-payment"
	require.NoError(t, DB.Create(&other).Error)
	_, err = RechargeEpay(other.TradeNo, "alipay", "2.00", "127.0.0.1", facts)
	require.ErrorIs(t, err, ErrPaymentReceiptConflict)
	after = TopUp{}
	require.NoError(t, DB.First(&after, other.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, after.Status)
	assert.Equal(t, 1_000_000, getUserBalances(t, buyer.Id).QuotaPaid)
}

func TestOrdinaryRewardEventFailureRollsBackPaymentReceiptAndPrincipal(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 97111, 0)
	order := TopUp{UserId: 97111, TradeNo: "ordinary-outbox-storage-failure", Amount: 2, Money: 2, PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay", Status: common.TopUpStatusPending}
	require.NoError(t, DB.Create(&order).Error)
	facts, ok := NewVerifiedPaymentFacts(PaymentProviderEpay, "synthetic-atomic-queue-payment", "synthetic-merchant", "live", "CNY", "2.00", false)
	require.True(t, ok)
	injected := errors.New("synthetic reward event storage failure")
	callback := "ordinary-reward-event-failure"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "agent_commission_outboxes" {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Create().Remove(callback) })
	_, err := RechargeEpay(order.TradeNo, "alipay", "2.00", "127.0.0.1", facts)
	require.ErrorIs(t, err, injected)
	var stored TopUp
	require.NoError(t, DB.First(&stored, order.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, stored.Status)
	assert.Zero(t, stored.PaymentReceiptID)
	assert.Zero(t, getUserBalances(t, 97111).QuotaPaid)
	for _, schema := range []any{&PaymentReceipt{}, &BalanceLedger{}, &AgentCommissionOutbox{}} {
		var count int64
		require.NoError(t, DB.Model(schema).Count(&count).Error)
		assert.Zero(t, count)
	}
	require.NoError(t, DB.Callback().Create().Remove(callback))
	_, err = RechargeEpay(order.TradeNo, "alipay", "2.00", "127.0.0.1", facts)
	require.NoError(t, err)
	assert.Equal(t, 1_000_000, getUserBalances(t, 97111).QuotaPaid)
}

func TestOrdinaryRedemptionRejectsAllRestrictedSourcesWithoutMutation(t *testing.T) {
	setupLedgerTestState(t)
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Unscoped().Where("1 = 1").Delete(&Redemption{}).Error)
	createLedgerTestUser(t, 97201, 100)
	for _, source := range []string{"budget_agent_id", "budget_reserved", "customer_user_id", "delivery_id", "source_protected"} {
		t.Run(source, func(t *testing.T) {
			row := Redemption{Key: common.GetUUID(), Name: "legacy-source", Quota: 50, Status: common.RedemptionCodeStatusEnabled}
			require.NoError(t, row.Insert())
			value := any(99)
			if source == "budget_reserved" || source == "source_protected" {
				value = true
			}
			require.NoError(t, DB.Model(&row).Update(source, value).Error)
			_, err := Redeem(row.Key, 97201)
			require.ErrorIs(t, err, ErrRedeemFailed)
			_, err = UpdateRedemption(RedemptionUpdateRequest{ID: row.Id, Status: common.RedemptionCodeStatusDisabled}, true)
			require.ErrorIs(t, err, ErrRedemptionRestrictedSource)
			_, err = DeleteRedemptions([]int{row.Id}, false)
			require.ErrorIs(t, err, ErrRedemptionRestrictedSource)
			var actual Redemption
			require.NoError(t, DB.First(&actual, row.Id).Error)
			assert.Equal(t, common.RedemptionCodeStatusEnabled, actual.Status)
			assert.Zero(t, actual.UsedUserId)
			assert.Zero(t, actual.RedeemedTime)
			assert.Equal(t, 100, getUserBalances(t, 97201).QuotaPaid)
			_, err = GetRedemptionById(row.Id)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		})
	}
	var count int64
	require.NoError(t, DB.Model(&BalanceLedger{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestOrdinaryRedemptionCreateReplayAndSessionRevocation(t *testing.T) {
	setupLedgerTestState(t)
	require.NoError(t, DB.AutoMigrate(&Redemption{}, &RedemptionMutation{}))
	require.NoError(t, DB.Exec("DELETE FROM redemption_mutations").Error)
	require.NoError(t, DB.Unscoped().Where("1 = 1").Delete(&Redemption{}).Error)
	admin := User{Id: 97301, Username: "ordinary-code-admin", AffCode: "ordinary-code-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, DB.Create(&admin).Error)
	identity := AuthSessionIdentity{UserID: admin.Id, SessionID: "ordinary-redemption-admin", UserAuthVersion: 1, SessionVersion: 1}
	require.NoError(t, DB.Create(&UserSession{SID: identity.SessionID, UserID: admin.Id, UserAuthVersion: 1, Version: 1, Status: UserSessionStatusActive, ExpiresAt: common.GetTimestamp() + 3600}).Error)
	request := RedemptionCreateRequest{Name: "ordinary-code", Quota: 100, Count: 2, RequestID: "ordinary-create"}
	first, err := CreateRedemptions(identity, request)
	require.NoError(t, err)
	replay, err := CreateRedemptions(identity, request)
	require.NoError(t, err)
	assert.Equal(t, first, replay)
	request.Quota++
	_, err = CreateRedemptions(identity, request)
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)
	request.Quota--
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", identity.SessionID).Update("status", UserSessionStatusRevoked).Error)
	_, err = CreateRedemptions(identity, request)
	require.ErrorIs(t, err, ErrRedemptionPermission)
	var count int64
	require.NoError(t, DB.Model(&Redemption{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}

func TestOrdinaryReferralTransferRevokedSessionRollsBack(t *testing.T) {
	setupLedgerTestState(t)
	user := User{Id: 97401, Username: "ordinary-transfer", AffCode: "ordinary-transfer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffQuota: 1_000_000}
	require.NoError(t, DB.Create(&user).Error)
	identity := AuthSessionIdentity{UserID: user.Id, SessionID: "ordinary-transfer-session", UserAuthVersion: 1, SessionVersion: 1}
	session := UserSession{SID: identity.SessionID, UserID: user.Id, UserAuthVersion: 1, Version: 1, Status: UserSessionStatusActive, ExpiresAt: common.GetTimestamp() + 3600}
	require.NoError(t, DB.Create(&session).Error)
	result, err := TransferAffQuota(user.Id, 500_000, "ordinary-transfer", identity)
	require.NoError(t, err)
	assert.False(t, result.Replayed)
	require.NoError(t, DB.Model(&session).Update("status", UserSessionStatusRevoked).Error)
	_, err = TransferAffQuota(user.Id, 500_000, "ordinary-transfer", identity)
	require.True(t, errors.Is(err, ErrUserSessionInactive))
	assert.Equal(t, 500_000, getUserBalances(t, user.Id).QuotaPaid)
	assert.Equal(t, 500_000, getUserBalances(t, user.Id).AffQuota)
}

func TestOrdinaryWalletRefundRestoresOriginalAccountsAndSource(t *testing.T) {
	setupLedgerTestState(t)
	user := User{Id: 97501, Username: "ordinary-source", AffCode: "ordinary-source", Status: common.UserStatusEnabled, Quota: 240, QuotaPaid: 100, QuotaBonus: 80, QuotaReward: 60}
	require.NoError(t, DB.Create(&user).Error)
	entry := LedgerEntry{UserId: user.Id, Delta: 150, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: "ordinary-source-request", RequestId: "ordinary-source-request", IdempotencyKey: "billing:preconsume:ordinary-source-request"}
	_, err := DebitQuota(entry, AccountBonus, AccountReward, AccountPaid)
	require.NoError(t, err)
	debited := getUserBalances(t, user.Id)
	assert.Zero(t, debited.QuotaBonus)
	assert.Zero(t, debited.QuotaReward)
	assert.Equal(t, 90, debited.QuotaPaid)
	for i := range 2 {
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
			result, _, err := CreditWalletRefundTx(tx, LedgerEntry{UserId: user.Id, Reason: LedgerReasonBillingRefund, ReferenceType: "relay", ReferenceId: entry.RequestId, RequestId: entry.RequestId, IdempotencyKey: "billing:refund:" + entry.RequestId}, 0)
			if err == nil {
				assert.Equal(t, i == 1, result.Replayed)
			}
			return err
		}))
	}
	restored := getUserBalances(t, user.Id)
	assert.Equal(t, user.QuotaPaid, restored.QuotaPaid)
	assert.Equal(t, user.QuotaBonus, restored.QuotaBonus)
	assert.Equal(t, user.QuotaReward, restored.QuotaReward)
	assertQuotaInvariant(t, restored)
	var states []BonusSourceState
	require.NoError(t, DB.Where("user_id = ?", user.Id).Find(&states).Error)
	require.Len(t, states, 1)
	assert.Equal(t, 80, states[0].Available)
	assert.Zero(t, states[0].Outstanding)
	var restorations int64
	require.NoError(t, DB.Model(&BonusSourceRestoration{}).Count(&restorations).Error)
	assert.EqualValues(t, 1, restorations)
}

func TestOrdinaryReferralWorkerConcurrentReplayIsAtomic(t *testing.T) {
	setupLedgerTestState(t)
	inviter := User{Id: 97601, Username: "ordinary-worker-inviter", AffCode: "ordinary-worker", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&inviter).Error)
	payload, err := common.Marshal(referralRewardSnapshot{UserID: 97602, InviterID: inviter.Id, Eligible: true, Enabled: true, Rate: "0.05", Ordinal: 1, PaidQuota: 1_000_000, PaidAt: common.GetTimestamp()})
	require.NoError(t, err)
	event := ReferralRewardOutbox{ReferenceID: "ordinary-concurrent-reward", SourceUserID: 97602, Payload: string(payload), Status: "pending"}
	require.NoError(t, DB.Create(&event).Error)
	callback := "ordinary-referral-inbox-failure"
	injected := errors.New("synthetic ordinary inbox storage failure")
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "referral_reward_inboxes" {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Create().Remove(callback) })
	err = processReferralRewardEvent(t.Context(), event.ID)
	require.ErrorIs(t, err, injected)
	assert.Zero(t, getUserBalances(t, inviter.Id).AffQuota)
	var count int64
	require.NoError(t, DB.Model(&ReferralCommission{}).Count(&count).Error)
	assert.Zero(t, count, "reward balance and reward record roll back with the inbox")
	require.NoError(t, DB.Callback().Create().Remove(callback))
	start := make(chan struct{})
	errs := make(chan error, 4)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() { <-start; errs <- processReferralRewardEvent(t.Context(), event.ID) })
	}
	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, 50_000, getUserBalances(t, inviter.Id).AffQuota)
	assert.Equal(t, 50_000, getUserBalances(t, inviter.Id).AffHistoryQuota)
	for _, schema := range []any{&ReferralCommission{}, &ReferralRewardInbox{}} {
		require.NoError(t, DB.Model(schema).Count(&count).Error)
		assert.EqualValues(t, 1, count)
	}
	require.NoError(t, DB.First(&event, event.ID).Error)
	assert.Equal(t, "done", event.Status)
	require.ErrorIs(t, DB.Model(&event).Update("payload", "{}").Error, ErrLedgerImmutable)
}

func TestOrdinaryBonusMaintenanceExpiresOnlyAvailableAndKeepsRefundProvenance(t *testing.T) {
	setupLedgerTestState(t)
	createLedgerTestUser(t, 97611, 0)
	now := common.GetTimestamp()
	oldClock := bonusSourceNow
	t.Cleanup(func() { bonusSourceNow = oldClock })
	bonusSourceNow = func() int64 { return now }
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := CreditQuotaTx(tx, LedgerEntry{UserId: 97611, Account: AccountBonus, Delta: 100, Reason: "ordinary_test_grant", IdempotencyKey: "ordinary-expiring-source", bonusGrant: &bonusSourceGrant{ExpiresAt: now + 60}})
		return err
	}))
	var lot BonusSourceLot
	require.NoError(t, DB.Where("user_id = ?", 97611).First(&lot).Error)
	assert.Zero(t, lot.CampaignID)
	assert.Zero(t, lot.ClaimID)
	sourceDebit(t, 97611, 60, "ordinary-expiring-hold")
	bonusSourceNow = func() int64 { return now + 120 }
	for i := range 2 {
		report, err := RunBonusSourceMaintenance(t.Context(), 100)
		require.NoError(t, err)
		assert.Equal(t, 1-i, report.ExpiredLots)
	}
	state := sourceState(t, lot.ID)
	assert.Zero(t, state.Available)
	assert.Equal(t, 60, state.Outstanding, "unknown delivery holds are not cancelled or expired")
	assert.Equal(t, 40, state.Expired)
	assert.Zero(t, getUserBalances(t, 97611).QuotaBonus)
	sourceRefund(t, 97611, 60, "ordinary-expiring-hold")
	for range 2 {
		_, err := RunBonusSourceMaintenance(t.Context(), 100)
		require.NoError(t, err)
	}
	state = sourceState(t, lot.ID)
	assert.Zero(t, state.Available)
	assert.Zero(t, state.Outstanding)
	assert.Equal(t, 100, state.Expired)
	assert.Zero(t, getUserBalances(t, 97611).QuotaBonus)
	var count int64
	require.NoError(t, DB.Model(&BonusSourceLot{}).Where("user_id = ?", 97611).Count(&count).Error)
	assert.EqualValues(t, 1, count, "a refund restores its original expiry instead of issuing a fresh source")
	require.NoError(t, DB.Model(&BonusSourceRestoration{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
