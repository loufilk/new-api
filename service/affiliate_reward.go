package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ---------------------------------------------------------------------------
// 钱包成本记录（P1-2）— 日志 other 中反映实际 wallet account split
//
// 邀请奖励（前三次成功在线充值按比例）在充值结算事务内由 model 层生成，
// 不依赖消费侧调用；本文件只负责钱包成本日志。
// ---------------------------------------------------------------------------

// FillWalletCostLogInfo 把该请求钱包账户的最终净份额写入日志 other：
//   - other["wallet_quota_deducted"] = 净消费总额（quota units）
//   - other["wallet_cost"] = {"paid": n, "bonus": m, "reward": k}（quota units）
//
// 净份额从 balance_ledger 汇总（预扣/追扣/结算/退款全部计入），退款后即反映
// 最终净份额。订阅消费调用方不会走到这里，不伪造钱包成本。
// 返回 paid 净消费额。
func FillWalletCostLogInfo(other *model.LogOther, userId int, refs [][2]string) (paidNet int) {
	if other == nil || userId <= 0 || len(refs) == 0 {
		return 0
	}
	cost, err := model.NetCostByReferences(userId, refs)
	if err != nil {
		common.SysError(fmt.Sprintf("query wallet net cost failed: user=%d err=%s", userId, err.Error()))
		return 0
	}
	// 账本净变动为负（扣减）；取反得到各账户净消费。
	paidNet = -cost[model.AccountPaid]
	if paidNet < 0 {
		paidNet = 0
	}
	bonusNet := -cost[model.AccountBonus]
	if bonusNet < 0 {
		bonusNet = 0
	}
	rewardNet := -cost[model.AccountReward]
	if rewardNet < 0 {
		rewardNet = 0
	}
	total := paidNet + bonusNet + rewardNet
	other.SetPublic("wallet_quota_deducted", total)
	if paidNet > 0 || bonusNet > 0 || rewardNet > 0 {
		walletCost := make(map[string]int)
		if paidNet > 0 {
			walletCost["paid"] = paidNet
		}
		if bonusNet > 0 {
			walletCost["bonus"] = bonusNet
		}
		if rewardNet > 0 {
			walletCost["reward"] = rewardNet
		}
		other.SetPublic("wallet_cost", walletCost)
	}
	return paidNet
}

// RelayWalletCostRefs 返回同步请求在账本中的引用（relay 引用）。
func RelayWalletCostRefs(relayInfo *relaycommon.RelayInfo) [][2]string {
	if relayInfo == nil || relayInfo.RequestId == "" {
		return nil
	}
	return [][2]string{{"relay", relayInfo.RequestId}}
}

// TaskWalletCostRefs returns the durable wallet references for an async task.
// It remains available to cost-log and reconciliation callers even though new
// referral rewards are no longer created from usage consumption.
func TaskWalletCostRefs(task *model.Task) [][2]string {
	if task == nil || task.TaskID == "" {
		return nil
	}
	refs := make([][2]string, 0, 2)
	if requestID := task.PrivateData.EffectiveRequestID(); requestID != "" {
		refs = append(refs, [2]string{"relay", requestID})
	}
	refs = append(refs, [2]string{"task", task.TaskID})
	return refs
}
