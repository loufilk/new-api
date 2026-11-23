package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func makeRecoveryDue(t *testing.T, request string) model.BillingFinalization {
	t.Helper()
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Where("request_id = ?", request).Update("next_retry_at", common.GetTimestamp()-1).Error)
	return lifecycleOwner(t, request)
}

func assertRecoveryAccounting(t *testing.T, actual int) {
	t.Helper()
	assertSettlementBalances(t, 1000-actual, 1000-actual)
	var user model.User
	require.NoError(t, model.DB.First(&user, 93201).Error)
	require.Equal(t, actual, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)
}

func TestBillingSettlementRecoveryPostgres(t *testing.T) {
	openTaskPollingSQLFixture(t, "postgres")
	require.NoError(t, model.DB.AutoMigrate(&model.BillingFinalization{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.ModelBonusEligibleAudit{}, &model.SubscriptionPlan{}, &model.SystemTask{}, &model.SystemTaskLock{}, &model.Midjourney{}))
	t.Run("additive_upgrade_twice_keeps_legacy_inert", func(t *testing.T) {
		legacy := model.BillingFinalization{RequestID: "legacy-settlement-review", UserID: 123456, Source: "wallet", State: model.BillingSettlementReview, ActualQuota: 79}
		require.NoError(t, model.DB.Create(&legacy).Error)
		require.NoError(t, model.DB.Migrator().DropColumn(&model.BillingFinalization{}, "SettlementRecovery"))
		require.NoError(t, model.DB.Migrator().DropColumn(&model.BillingFinalization{}, "SettlementLogRetryAt"))
		for range 2 {
			require.NoError(t, model.DB.AutoMigrate(&model.BillingFinalization{}))
		}
		var stored model.BillingFinalization
		require.NoError(t, model.DB.First(&stored, "request_id = ?", legacy.RequestID).Error)
		require.Equal(t, legacy.State, stored.State)
		require.Equal(t, 79, stored.ActualQuota)
		require.Empty(t, stored.SettlementRecovery)
		require.Zero(t, stored.SettlementLogRetryAt)
		require.NoError(t, model.DB.Model(&stored).Update("next_retry_at", common.GetTimestamp()-1).Error)
		rows, err := model.GetReadyBillingSettlements(t.Context(), 32)
		require.NoError(t, err)
		require.Empty(t, rows)
		require.NoError(t, model.DB.Delete(&stored).Error)
	})
	for _, actual := range []int{60, 180} {
		t.Run(fmt.Sprintf("observed_%d_atomic_replay", actual), func(t *testing.T) {
			s, _ := settlementDeadlineSession(t, fmt.Sprintf("recovery-direct-%d", actual), true, BillingSourceWallet)
			s.relayInfo.UserSetting.QuotaWarningEnabled = boolPtr(false)
			s.relayInfo.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 93205}
			require.NoError(t, model.DB.Create(&model.Channel{Id: 93205, Name: "recovery-channel"}).Error)
			t.Cleanup(func() { require.NoError(t, model.DB.Delete(&model.Channel{}, 93205).Error) })
			owned, err := settleObservedTextBilling(testGinContext(t), s.relayInfo, actual, true)
			require.True(t, owned)
			require.NoError(t, err)
			assertRecoveryAccounting(t, actual)
			owned, err = settleObservedTextBilling(testGinContext(t), s.relayInfo, actual, true)
			require.True(t, owned)
			require.NoError(t, err)
			assertRecoveryAccounting(t, actual)
			var channel model.Channel
			require.NoError(t, model.DB.First(&channel, 93205).Error)
			require.EqualValues(t, actual, channel.UsedQuota)
			receipt := lifecycleOwner(t, s.relayInfo.RequestId)
			require.NotEmpty(t, receipt.SettlementRecovery)
			require.Zero(t, receipt.SettlementLogRetryAt, "normal success creates no log projection backlog")
			require.Error(t, model.RequestRecoverableBillingSettlement(t.Context(), receipt, actual+1, *s.settlementRecovery))
			wrong := *s.settlementRecovery
			wrong.AccountOrder = []string{"bonus", "reward", "paid"}
			require.Error(t, model.RequestRecoverableBillingSettlement(t.Context(), receipt, actual, wrong))
		})
	}
	t.Run("transient_then_worker_and_duplicate_worker", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-transient", true, BillingSourceWallet)
		s.relayInfo.UserSetting.QuotaWarningEnabled = boolPtr(false)
		var hits atomic.Int32
		const hook = "recovery-transient-token"
		require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
			if tx.Statement.Table == "tokens" {
				hits.Add(1)
				tx.AddError(&pgconn.PgError{Code: "40001", Message: "synthetic retryable"})
			}
		}))
		t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(hook) })
		owned, err := settleObservedTextBilling(testGinContext(t), s.relayInfo, 180, true)
		require.True(t, owned)
		require.Error(t, err)
		require.EqualValues(t, 3, hits.Load())
		receipt := lifecycleOwner(t, s.relayInfo.RequestId)
		require.NotEmpty(t, receipt.SettlementRecovery)
		require.Positive(t, receipt.NextRetryAt)
		assertSettlementBalances(t, 900, 900)
		require.NoError(t, model.DB.Callback().Update().Remove(hook))
		receipt = makeRecoveryDue(t, receipt.RequestID)
		var wg sync.WaitGroup
		results := make(chan error, 8)
		for range 8 {
			wg.Go(func() { results <- recoverBillingSettlement(t.Context(), receipt) })
		}
		wg.Wait()
		close(results)
		for err := range results {
			require.NoError(t, err)
		}
		require.Equal(t, model.BillingSettled, lifecycleOwner(t, receipt.RequestID).State)
		assertRecoveryAccounting(t, 180)
		require.NoError(t, s.Settle(180))
		assertRecoveryAccounting(t, 180)
		s.Refund(testGinContext(t))
		assertRecoveryAccounting(t, 180)
	})
	t.Run("deadline_before_intent_uses_bounded_evidence_tail", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-admission-deadline", true, BillingSourceWallet)
		s.relayInfo.UserSetting.QuotaWarningEnabled = boolPtr(false)
		original := model.DB
		pool := installSettlementDeadlinePool(t, "driver")
		owned, err := settleObservedTextBilling(testGinContext(t), s.relayInfo, 180, true)
		close(pool.released)
		require.True(t, owned)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		model.DB = original
		receipt := lifecycleOwner(t, s.relayInfo.RequestId)
		require.Equal(t, model.BillingSettlementReview, receipt.State)
		require.NotEmpty(t, receipt.SettlementRecovery)
		require.Positive(t, receipt.NextRetryAt)
		require.Equal(t, 180, receipt.ActualQuota)
		assertSettlementBalances(t, 900, 900)
		require.NoError(t, recoverBillingSettlement(t.Context(), makeRecoveryDue(t, receipt.RequestID)))
		assertRecoveryAccounting(t, 180)
	})
	t.Run("settlement_commit_lost_ack_never_recharges", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-commit-lost-ack", true, BillingSourceWallet)
		s.relayInfo.UserSetting.QuotaWarningEnabled = boolPtr(false)
		original := model.DB
		pool := installSettlementDeadlinePool(t, "committed_lost_ack", 1)
		owned, err := settleObservedTextBilling(testGinContext(t), s.relayInfo, 180, true)
		close(pool.released)
		require.True(t, owned)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		model.DB = original
		receipt := lifecycleOwner(t, s.relayInfo.RequestId)
		require.Equal(t, model.BillingSettled, receipt.State)
		assertRecoveryAccounting(t, 180)
		require.NoError(t, recoverBillingSettlement(t.Context(), receipt))
		require.NoError(t, s.Settle(180))
		assertRecoveryAccounting(t, 180)
	})
	t.Run("historical_unknown_and_ownership_fail_closed", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-unknown", true, BillingSourceWallet)
		receipt := *s.terminalReceipt
		recovery := model.BillingSettlementRecovery{Version: 1, AccountOrder: s.funding.(moneyport.SettlementPolicyReporter).SettlementAccountOrder()}
		require.NoError(t, model.MarkBillingDeliveryUnknown(t.Context(), receipt))
		require.Error(t, model.RequestRecoverableBillingSettlement(t.Context(), receipt, 180, recovery))
		require.NoError(t, recoverBillingSettlement(t.Context(), receipt))
		assertSettlementBalances(t, 900, 900)
	})
	t.Run("replacement_token_and_policy_remain_manual", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-replaced-token", true, BillingSourceWallet)
		receipt := *s.terminalReceipt
		recovery := model.BillingSettlementRecovery{Version: 1, AccountOrder: s.funding.(moneyport.SettlementPolicyReporter).SettlementAccountOrder()}
		require.NoError(t, model.RequestRecoverableBillingSettlement(t.Context(), receipt, 180, recovery))
		require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 93202).Update("key", "replacement-not-original").Error)
		require.Error(t, recoverBillingSettlement(t.Context(), makeRecoveryDue(t, receipt.RequestID)))
		stored := lifecycleOwner(t, receipt.RequestID)
		require.Equal(t, model.BillingSettlementReview, stored.State)
		require.Zero(t, stored.NextRetryAt)
		assertSettlementBalances(t, 900, 900)
	})
	t.Run("cancelled_pass_does_not_spend_attempts", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-cancelled", true, BillingSourceWallet)
		receipt := *s.terminalReceipt
		recovery := model.BillingSettlementRecovery{Version: 1, AccountOrder: s.funding.(moneyport.SettlementPolicyReporter).SettlementAccountOrder()}
		require.NoError(t, model.RequestRecoverableBillingSettlement(t.Context(), receipt, 180, recovery))
		makeRecoveryDue(t, receipt.RequestID)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := RecoverBillingSettlements(ctx, 32)
		require.True(t, errors.Is(err, context.Canceled))
		require.Zero(t, lifecycleOwner(t, receipt.RequestID).Attempts)
	})
}

func TestBillingSettlementRecoveryProjectionPostgres(t *testing.T) {
	openTaskPollingSQLFixture(t, "postgres")
	require.NoError(t, model.DB.AutoMigrate(&model.BillingFinalization{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.ModelBonusEligibleAudit{}, &model.SubscriptionPlan{}))
	t.Run("existing_log_corrected_idempotently_no_export_guess", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-log", true, BillingSourceWallet)
		receipt := *s.terminalReceipt
		recovery := model.BillingSettlementRecovery{Version: 1, AccountOrder: s.funding.(moneyport.SettlementPolicyReporter).SettlementAccountOrder(), LogExpected: true}
		require.NoError(t, model.RequestRecoverableBillingSettlement(t.Context(), receipt, 180, recovery))
		other := model.NewLogOther()
		other.SetPublic("billing_status", "settlement_unconfirmed")
		other.SetAdmin("billing_calculated_quota", 180)
		log := model.Log{UserId: receipt.UserID, RequestId: receipt.RequestID, TokenId: receipt.TokenID, Type: model.LogTypeError, Quota: 0, PromptTokens: 80, CompletionTokens: 20, Other: other.JSONString()}
		require.NoError(t, model.LOG_DB.Create(&log).Error)
		t.Cleanup(func() { require.NoError(t, model.LOG_DB.Delete(&log).Error) })
		require.NoError(t, recoverBillingSettlement(t.Context(), makeRecoveryDue(t, receipt.RequestID)))
		receipt = lifecycleOwner(t, receipt.RequestID)
		require.Positive(t, receipt.SettlementLogRetryAt)
		require.NoError(t, model.ReconcileBillingSettlementLog(t.Context(), receipt))
		require.NoError(t, model.ReconcileBillingSettlementLog(t.Context(), receipt))
		var projected model.Log
		require.NoError(t, model.LOG_DB.First(&projected, log.Id).Error)
		require.Equal(t, model.LogTypeConsume, projected.Type)
		require.Equal(t, 180, projected.Quota)
		require.Equal(t, 80, projected.PromptTokens)
		require.Equal(t, 20, projected.CompletionTokens)
		require.Contains(t, projected.Other, `"billing_quota_data_reconciliation":"required"`)
		require.NotContains(t, projected.Other, `"paid_cost"`)
		require.Zero(t, lifecycleOwner(t, receipt.RequestID).SettlementLogRetryAt)
		assertRecoveryAccounting(t, 180)
	})
	t.Run("missing_or_mismatching_log_stays_pending_no_money_replay", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-log-missing", true, BillingSourceWallet)
		receipt := *s.terminalReceipt
		recovery := model.BillingSettlementRecovery{Version: 1, AccountOrder: s.funding.(moneyport.SettlementPolicyReporter).SettlementAccountOrder(), LogExpected: true}
		require.NoError(t, model.RequestRecoverableBillingSettlement(t.Context(), receipt, 180, recovery))
		require.NoError(t, recoverBillingSettlement(t.Context(), makeRecoveryDue(t, receipt.RequestID)))
		receipt = lifecycleOwner(t, receipt.RequestID)
		require.Error(t, model.ReconcileBillingSettlementLog(t.Context(), receipt))
		require.Positive(t, lifecycleOwner(t, receipt.RequestID).SettlementLogRetryAt)
		assertRecoveryAccounting(t, 180)
		other := model.NewLogOther()
		other.SetPublic("billing_status", "settlement_unconfirmed")
		other.SetAdmin("billing_calculated_quota", 181)
		log := model.Log{UserId: receipt.UserID, RequestId: receipt.RequestID, TokenId: receipt.TokenID, Type: model.LogTypeError, Other: other.JSONString()}
		require.NoError(t, model.LOG_DB.Create(&log).Error)
		t.Cleanup(func() { require.NoError(t, model.LOG_DB.Delete(&log).Error) })
		require.Error(t, model.ReconcileBillingSettlementLog(t.Context(), receipt))
		require.NoError(t, model.LOG_DB.First(&log, log.Id).Error)
		require.Equal(t, model.LogTypeError, log.Type)
		assertRecoveryAccounting(t, 180)
	})
	t.Run("original_paid_policy_does_not_gain_bonus_eligibility", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-original-policy", true, BillingSourceWallet)
		receipt := *s.terminalReceipt
		recovery := model.BillingSettlementRecovery{Version: 1, AccountOrder: s.funding.(moneyport.SettlementPolicyReporter).SettlementAccountOrder()}
		require.NotContains(t, recovery.AccountOrder, "bonus")
		require.NoError(t, model.RequestRecoverableBillingSettlement(t.Context(), receipt, 1050, recovery))
		require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", receipt.UserID).Updates(map[string]any{"quota": 1900, "quota_bonus": 1000}).Error)
		require.NoError(t, model.DB.Model(&model.Model{}).Where("model_name = ?", "settlement-deadline-model").Update("bonus_eligible", true).Error)
		model.InvalidateModelBonusEligibleCache()
		require.Error(t, recoverBillingSettlement(t.Context(), makeRecoveryDue(t, receipt.RequestID)))
		stored := lifecycleOwner(t, receipt.RequestID)
		require.Equal(t, model.BillingSettlementReview, stored.State)
		require.Zero(t, stored.NextRetryAt)
		assertUserBalances(t, receipt.UserID, 1000, 900, 0)
	})
	t.Run("retry_budget_is_bounded", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-budget", true, BillingSourceWallet)
		receipt := *s.terminalReceipt
		recovery := model.BillingSettlementRecovery{Version: 1, AccountOrder: s.funding.(moneyport.SettlementPolicyReporter).SettlementAccountOrder()}
		require.NoError(t, model.RequestRecoverableBillingSettlement(t.Context(), receipt, 180, recovery))
		for range model.BillingSettlementRecoveryMaxAttempts {
			require.NoError(t, model.RecordBillingSettlementFailure(t.Context(), receipt, 180, context.DeadlineExceeded))
		}
		stored := lifecycleOwner(t, receipt.RequestID)
		require.Zero(t, stored.NextRetryAt)
		require.Equal(t, model.BillingSettlementRecoveryMaxAttempts, stored.Attempts)
		require.NoError(t, recoverBillingSettlement(t.Context(), makeRecoveryDue(t, receipt.RequestID)))
		assertSettlementBalances(t, 900, 900)
	})
}

func TestMeasuredTextRecoveryGate(t *testing.T) {
	ctx := testGinContext(t)
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI, IsStream: true}
	require.False(t, canRecoverObservedTextSettlement(ctx, info, nil))
	require.False(t, canRecoverObservedTextSettlement(ctx, info, &dto.Usage{}))
	require.True(t, canRecoverObservedTextSettlement(ctx, info, &dto.Usage{TotalTokens: 100}))
	require.False(t, canRecoverObservedTextSettlement(ctx, info, &dto.Usage{TotalTokens: 100, BillingUsage: &dto.BillingUsage{Estimated: true}}))
	common.SetContextKey(ctx, constant.ContextKeyLocalCountTokens, true)
	require.False(t, canRecoverObservedTextSettlement(ctx, info, &dto.Usage{TotalTokens: 100}))
}

func TestObservedTextRecoveryRouteScope(t *testing.T) {
	ctx := testGinContext(t)
	usage := &dto.Usage{PromptTokens: 1, TotalTokens: 1}
	for _, mode := range []int{relayconstant.RelayModeUnknown, relayconstant.RelayModeCompletions, relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits, relayconstant.RelayModeEmbeddings, relayconstant.RelayModeRerank, relayconstant.RelayModeAudioSpeech, relayconstant.RelayModeAlphaSearch, relayconstant.RelayModeResponses, relayconstant.RelayModeGemini} {
		info := &relaycommon.RelayInfo{RelayMode: mode, RelayFormat: types.RelayFormatOpenAI, IsStream: true}
		require.False(t, canRecoverObservedTextSettlement(ctx, info, usage), "mode %d", mode)
	}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI, IsStream: false}
	require.False(t, canRecoverObservedTextSettlement(ctx, info, usage))
	info.IsStream = true
	info.FinalRequestRelayFormat = types.RelayFormatClaude
	require.False(t, canRecoverObservedTextSettlement(ctx, info, usage))
	info.FinalRequestRelayFormat = types.RelayFormatOpenAI
	require.True(t, canRecoverObservedTextSettlement(ctx, info, usage))
	info.IsPlayground = true
	require.False(t, canRecoverObservedTextSettlement(ctx, info, usage))
}

func TestBillingSettlementRecoveryMixedRefundPostgres(t *testing.T) {
	openTaskPollingSQLFixture(t, "postgres")
	require.NoError(t, model.DB.AutoMigrate(&model.BillingFinalization{}))
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	setting := operation_setting.GetQuotaSetting()
	oldTrust := setting.TrustQuotaUSD
	setting.TrustQuotaUSD = 0
	t.Cleanup(func() { setting.TrustQuotaUSD = oldTrust })
	seedBonusUser(t, 93301, 300, 400)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 93301).Updates(map[string]any{"quota_reward": 300, "quota": 1000}).Error)
	seedToken(t, 93302, 93301, "synthetic-mixed-recovery-token", 2000)
	info := &relaycommon.RelayInfo{UserId: 93301, TokenId: 93302, TokenKey: "synthetic-mixed-recovery-token", RequestId: "mixed-recovery-refund", UserSetting: dto.UserSetting{QuotaWarningEnabled: boolPtr(false)}}
	funding, err := createFundingSource(BillingSourceWallet, moneyport.FactoryRequest{UserID: info.UserId, RequestID: info.RequestId, AccountOrder: []string{"bonus", "reward", "paid"}})
	require.NoError(t, err)
	s := &BillingSession{relayInfo: info, funding: funding}
	info.Billing = s
	ctx := testGinContext(t)
	ctx.Set("token_quota", 2000)
	require.Nil(t, s.preConsume(ctx, 650))
	receipt := *s.terminalReceipt
	require.Positive(t, receipt.Paid)
	require.Positive(t, receipt.Bonus)
	require.Positive(t, receipt.Reward)
	var original []model.LedgerShare
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		original, err = model.WalletRequestReceiptTx(tx, 93301, info.RequestId)
		return err
	}))
	expected, err := model.AllocateWalletRefund(original, nil, 300)
	require.NoError(t, err)
	recovery := model.BillingSettlementRecovery{Version: 1, AccountOrder: funding.(moneyport.SettlementPolicyReporter).SettlementAccountOrder()}
	require.NoError(t, model.RequestRecoverableBillingSettlement(t.Context(), receipt, 350, recovery))
	require.NoError(t, recoverBillingSettlement(t.Context(), makeRecoveryDue(t, receipt.RequestID)))
	var token model.Token
	require.NoError(t, model.DB.First(&token, 93302).Error)
	require.Equal(t, 1650, token.RemainQuota)
	require.Equal(t, 350, token.UsedQuota)
	u := getUserBalances(t, 93301)
	require.Equal(t, 650, u.Quota)
	require.Equal(t, 350, u.UsedQuota)
	require.Equal(t, 1, u.RequestCount)
	var credits []model.BalanceLedger
	require.NoError(t, model.DB.Where("request_id = ? AND delta > 0", receipt.RequestID).Find(&credits).Error)
	got := map[string]int{}
	for _, row := range credits {
		got[row.Account] += row.Delta
	}
	for _, share := range expected {
		require.Equal(t, share.Amount, got[string(share.Account)])
	}
	require.NoError(t, recoverBillingSettlement(t.Context(), receipt))
	require.Equal(t, u, getUserBalances(t, 93301))
}

func TestBillingSettlementRecoveryConnectionCapacityPostgres(t *testing.T) {
	openTaskPollingSQLFixture(t, "postgres")
	require.NoError(t, model.DB.AutoMigrate(&model.BillingFinalization{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.ModelBonusEligibleAudit{}, &model.SubscriptionPlan{}))
	capacity := &pgconn.PgError{Code: "53300", Message: "synthetic too many connections"}
	require.False(t, model.IsTransientBillingSettlementError(capacity), "do not retry a connection-capacity error inline")
	t.Run("frozen_explicit_retries_only_after_backoff", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-capacity-explicit", true, BillingSourceWallet)
		s.relayInfo.UserSetting.QuotaWarningEnabled = boolPtr(false)
		var hits atomic.Int32
		const hook = "recovery-capacity-token"
		require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
			if tx.Statement.Table == "tokens" && hits.Add(1) == 1 {
				tx.AddError(fmt.Errorf("synthetic wrapped PostgreSQL failure: %w", capacity))
			}
		}))
		t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(hook) })
		owned, err := settleObservedTextBilling(testGinContext(t), s.relayInfo, 180, true)
		require.True(t, owned)
		require.ErrorIs(t, err, capacity)
		require.EqualValues(t, 1, hits.Load(), "overload cannot cause inline retries")
		receipt := lifecycleOwner(t, s.relayInfo.RequestId)
		require.Equal(t, model.BillingSettlementReview, receipt.State)
		require.Equal(t, 180, receipt.ActualQuota)
		require.Equal(t, "transient_database", receipt.LastErrorCode)
		require.Equal(t, 1, receipt.Attempts)
		require.Equal(t, receipt.LastAttemptAt+2, receipt.NextRetryAt)
		require.Zero(t, receipt.EscalatedAt)
		assertSettlementBalances(t, 900, 900)
		// Assert the production delay above, then make the lease check independent
		// of this fixture's elapsed time on a busy test host.
		require.NoError(t, model.DB.Model(&receipt).Update("next_retry_at", common.GetTimestamp()+60).Error)
		claimed, err := model.ClaimBillingSettlementRecovery(t.Context(), receipt.RequestID)
		require.NoError(t, err)
		require.Nil(t, claimed, "worker must respect the scheduled retry time")
		require.NoError(t, recoverBillingSettlement(t.Context(), makeRecoveryDue(t, receipt.RequestID)))
		require.EqualValues(t, 2, hits.Load())
		assertRecoveryAccounting(t, 180)
	})
	t.Run("legacy_review_and_unfrozen_hold_never_gain_authority", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-capacity-legacy", true, BillingSourceWallet)
		receipt := *s.terminalReceipt
		require.Error(t, model.RecordBillingSettlementFailure(t.Context(), receipt, 180, capacity))
		stored := lifecycleOwner(t, receipt.RequestID)
		require.Equal(t, model.BillingHeld, stored.State)
		require.Zero(t, stored.ActualQuota)
		require.Zero(t, stored.NextRetryAt)
		require.Zero(t, stored.Attempts)
		require.NoError(t, model.RequestBillingSettlement(t.Context(), receipt, 180))
		require.NoError(t, model.RecordBillingSettlementFailure(t.Context(), receipt, 180, capacity))
		stored = lifecycleOwner(t, receipt.RequestID)
		require.Equal(t, model.BillingSettlementReview, stored.State)
		require.Equal(t, 180, stored.ActualQuota)
		require.Empty(t, stored.SettlementRecovery)
		require.Equal(t, "review_required", stored.LastErrorClass)
		require.Zero(t, stored.NextRetryAt)
		require.Positive(t, stored.EscalatedAt)
		assertSettlementBalances(t, 900, 900)
	})
	t.Run("typed_only_and_retry_budget", func(t *testing.T) {
		s, _ := settlementDeadlineSession(t, "recovery-capacity-budget", true, BillingSourceWallet)
		receipt := *s.terminalReceipt
		recovery := model.BillingSettlementRecovery{Version: 1, AccountOrder: s.funding.(moneyport.SettlementPolicyReporter).SettlementAccountOrder()}
		require.NoError(t, model.RequestRecoverableBillingSettlement(t.Context(), receipt, 180, recovery))
		require.NoError(t, model.RecordBillingSettlementFailure(t.Context(), receipt, 180, errors.New("synthetic SQLSTATE 53300")))
		require.Zero(t, lifecycleOwner(t, receipt.RequestID).NextRetryAt, "text never authorizes an automatic retry")
		for i := 1; i < model.BillingSettlementRecoveryMaxAttempts; i++ {
			require.NoError(t, model.RecordBillingSettlementFailure(t.Context(), receipt, 180, capacity))
			stored := lifecycleOwner(t, receipt.RequestID)
			require.Equal(t, i+1, stored.Attempts)
			if i+1 < model.BillingSettlementRecoveryMaxAttempts {
				require.Equal(t, stored.LastAttemptAt+min(int64(60), int64(2)<<min(i, 5)), stored.NextRetryAt)
			} else {
				require.Zero(t, stored.NextRetryAt)
				require.Positive(t, stored.EscalatedAt)
			}
		}
		require.NoError(t, recoverBillingSettlement(t.Context(), makeRecoveryDue(t, receipt.RequestID)))
		assertSettlementBalances(t, 900, 900)
	})
}
