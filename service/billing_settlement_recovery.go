package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Only ordinary measured text usage has a fully reproducible accounting body.
// Unknown usage, custom sources, task callbacks and realtime keep their existing
// fail-closed/manual boundary. The returned flag suppresses legacy async usage
// counters even when the owner has already committed through another worker.
func settleObservedTextBilling(ctx *gin.Context, info *relaycommon.RelayInfo, actual int, observed bool) (bool, error) {
	s, ok := info.Billing.(*BillingSession)
	if !ok || !observed || s.funding.Source() != BillingSourceWallet || !s.durableTerminalSource() {
		return false, SettleBilling(ctx, info, actual)
	}
	policy, ok := s.funding.(moneyport.SettlementPolicyReporter)
	if !ok {
		return false, SettleBilling(ctx, info, actual)
	}
	s.mu.Lock()
	if s.realtime != nil || s.taskOwned || s.settled && s.settlementRecovery == nil {
		s.mu.Unlock()
		return false, SettleBilling(ctx, info, actual)
	}
	if s.settlementRecovery == nil {
		channelID := 0
		if info.ChannelMeta != nil {
			channelID = info.ChannelId
		}
		s.settlementRecovery = &model.BillingSettlementRecovery{Version: 1, AccountOrder: policy.SettlementAccountOrder(), ChannelID: channelID, LogExpected: common.LogConsumeEnabled}
	}
	s.mu.Unlock()
	return true, SettleBilling(ctx, info, actual)
}

// RecoverBillingSettlements bounds one pass and its concurrency. It performs
// SQL-only settlement of explicit current snapshots, never provider replay or
// a refund. The caller owns periodic scheduling and shutdown cancellation.
func RecoverBillingSettlements(ctx context.Context, limit int) (int, error) {
	rows, err := model.GetReadyBillingSettlements(ctx, limit)
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	jobs := make(chan model.BillingFinalization, len(rows))
	for range min(4, len(rows)) {
		wg.Go(func() {
			defer func() {
				if recover() != nil {
					common.SysError("billing settlement recovery worker panicked; durable lease retained")
				}
			}()
			for receipt := range jobs {
				if ctx.Err() != nil {
					return
				}
				operationCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := recoverBillingSettlement(operationCtx, receipt)
				cancel()
				if err != nil {
					code, class := model.BillingSettlementFailureCode(err)
					common.SysError(fmt.Sprintf("billing settlement recovery incomplete: request_id=%q code=%s class=%s", receipt.RequestID, code, class))
				}
			}
		})
	}
	processed := 0
send:
	for _, receipt := range rows {
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			break send
		case jobs <- receipt:
			processed++
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() == nil {
		logs, scanErr := model.GetReadyBillingSettlementLogs(ctx, limit)
		if scanErr != nil {
			return processed, scanErr
		}
		for _, receipt := range logs {
			if ctx.Err() != nil {
				break
			}
			logCtx, cancel := context.WithTimeout(ctx, time.Second)
			err := model.ReconcileBillingSettlementLog(logCtx, receipt)
			cancel()
			if err != nil {
				common.SysError("billing settled usage log projection remains pending")
			}
		}
	}
	return processed, ctx.Err()
}

func recoverBillingSettlement(ctx context.Context, receipt model.BillingFinalization) error {
	claimed, err := model.ClaimBillingSettlementRecovery(ctx, receipt.RequestID)
	if err != nil || claimed == nil {
		return err
	}
	receipt = *claimed
	recovery, err := model.DecodeBillingSettlementRecovery(receipt)
	if err == nil {
		var source moneyport.FundingSource
		source, err = createFundingSource(BillingSourceWallet, moneyport.FactoryRequest{UserID: receipt.UserID, RequestID: receipt.RequestID, AccountOrder: recovery.AccountOrder})
		if err == nil {
			funding, ok := source.(transactionalSettlementFunding)
			policy, policyOK := source.(moneyport.SettlementPolicyReporter)
			capability, durable := source.(moneyport.HostBillingFinalizationSource)
			if !ok || !policyOK || !durable || !capability.UsesHostBillingFinalization() {
				err = errors.New("funding source lacks settlement recovery capability")
			} else if !slices.Equal(policy.SettlementAccountOrder(), recovery.AccountOrder) {
				err = errors.New("funding source changed the frozen settlement policy")
			} else {
				delta := receipt.ActualQuota - receipt.PreConsumed
				_, err = model.RecoverBillingFinalization(ctx, receipt, receipt.ActualQuota, recovery, func(tx *gorm.DB) error {
					if delta != 0 {
						result, err := funding.SettleInTx(tx, delta)
						if err != nil {
							return err
						}
						if result.Replayed {
							return errors.New("source settlement exists without atomic terminal owner")
						}
					}
					if err := model.ApplyBillingTokenDeltaTx(tx, &receipt, -delta); err != nil {
						return err
					}
					return model.ApplyTaskUsageDeltaTx(tx, receipt.UserID, recovery.ChannelID, receipt.ActualQuota, 1)
				})
				model.RefreshUserQuotaCacheAfterBillingFinalization(receipt.UserID)
			}
		}
	}
	if err != nil {
		// Driver cancellation can surface as sql.ErrTxDone/57014 rather than
		// wrapping the deadline. Corroborate it with this operation's context.
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		// A lost COMMIT acknowledgement must not reopen the settled owner.
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if recordErr := model.RecordBillingSettlementFailure(recordCtx, receipt, receipt.ActualQuota, err); recordErr != nil {
			common.SysError("failed to record billing settlement recovery metadata")
		}
	}
	return err
}

// Read the immutable provider evidence before fixed-price paths synthesize an
// estimate. Empty, locally counted or explicitly estimated usage is not new
// recovery authority, even if existing pricing bills a flat/tool fee.
func canRecoverObservedTextSettlement(ctx *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if info == nil || info.IsPlayground || !info.IsStream || info.RelayMode != relayconstant.RelayModeChatCompletions || info.RelayFormat != types.RelayFormatOpenAI || info.GetFinalRequestRelayFormat() != types.RelayFormatOpenAI {
		return false
	}
	if usage == nil || common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens) || usage.BillingUsage != nil && usage.BillingUsage.Estimated {
		return false
	}
	u := effectiveBillingUsage(usage)
	return u != nil && (u.TotalTokens > 0 || u.PromptTokens > 0 || u.CompletionTokens > 0 || u.InputTokens > 0 || u.OutputTokens > 0 || u.PromptTokensDetails.CachedTokens > 0 || u.PromptTokensDetails.CachedCreationTokens > 0 || u.ClaudeCacheCreation5mTokens > 0 || u.ClaudeCacheCreation1hTokens > 0)
}
