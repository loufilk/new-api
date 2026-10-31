package service

import (
	"context"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// taskSubmissionLog keeps main-DB consume logs under the same terminal owner
// as funds and usage. Separate log stores remain best-effort display projections.
type taskSubmissionLog struct {
	params model.RecordTaskBillingLogParams
	joined bool
}

func newTaskSubmissionLog(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task) *taskSubmissionLog {
	if !common.LogConsumeEnabled {
		return nil
	}
	params := taskConsumptionLogParams(c, info, task)
	log := &taskSubmissionLog{params: model.RecordTaskBillingLogParams{
		OperationID: "task:submit:" + info.RequestId,
		RequestID:   info.RequestId, UpstreamRequestID: c.GetString(common.UpstreamRequestIdKey),
		UserId: info.UserId, LogType: model.LogTypeConsume, Content: params.Content,
		ChannelId: params.ChannelId, ModelName: params.ModelName, Quota: task.Quota,
		TokenId: params.TokenId, TokenName: params.TokenName, Group: params.Group, Other: params.Other, NodeName: task.PrivateData.NodeName,
	}}
	if info.UserSetting.RecordIpLog {
		log.params.IP = c.ClientIP()
	}
	return log
}

func (l *taskSubmissionLog) writeTx(tx *gorm.DB, task *model.Task) error {
	if l == nil {
		return nil
	}
	if task.PrivateData.BillingSource == BillingSourceWallet {
		cost := make(map[string]int)
		total := 0
		for _, share := range task.PrivateData.WalletDebit {
			if share.Amount > 0 {
				cost[string(share.Account)] += share.Amount
				total += share.Amount
			}
		}
		l.params.Other.SetPublic("wallet_quota_deducted", total)
		if len(cost) > 0 {
			l.params.Other.SetPublic("wallet_cost", cost)
		}
	}
	var err error
	l.joined, err = model.RecordTaskBillingLogTx(tx, l.params)
	return err
}

func (l *taskSubmissionLog) afterCommit(ctx context.Context, applied bool) {
	if l == nil || !applied {
		return
	}
	if l.joined {
		model.RecordTaskQuotaExport(ctx, l.params)
	} else {
		model.RecordTaskBillingLog(l.params)
	}
}
