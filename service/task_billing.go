package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// LogTaskConsumption 记录任务消费日志和统计信息（仅记录，不涉及实际扣费）。
// 实际扣费已由 BillingSession（PreConsumeBilling + SettleBilling）完成。
func LogTaskConsumption(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task) {
	tokenName := c.GetString("token_name")
	logContent := []*common.Message{common.NewMessage("Action {{action}}", map[string]any{"action": info.Action})}
	// 支持任务仅按次计费
	if common.StringsContains(constant.TaskPricePatches, info.GetBillingModelName()) {
		logContent = append(logContent, common.NewMessage("Billed per request"))
	} else {
		var contents []string
		if otherRatios := info.PriceData.OtherRatios(); len(otherRatios) > 0 {
			for key, ra := range otherRatios {
				if 1.0 != ra {
					contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
				}
			}
		}
		if snap := info.TieredBillingSnapshot; snap != nil {
			for key, value := range snap.UsageFacts {
				contents = append(contents, fmt.Sprintf("%s: %v", key, value))
			}
		}
		if len(contents) > 0 {
			logContent = append(logContent, common.NewMessage("Billing parameters: {{params}}", map[string]any{"params": strings.Join(contents, ", ")}))
		}
	}
	other := model.NewLogOther()
	other.SetPublic("is_task", true)
	other.SetPublic("request_path", c.Request.URL.Path)
	if taskDeliveredInline(c, task) {
		other.SetPublic("task_sync", true)
	}
	other.SetPublic("model_price", info.PriceData.ModelPrice)
	if info.PriceData.ModelRatio > 0 {
		other.SetPublic("model_ratio", info.PriceData.ModelRatio)
	}
	other.SetPublic("group_ratio", info.PriceData.GroupRatioInfo.GroupRatio)
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other.SetPublic("user_group_ratio", info.PriceData.GroupRatioInfo.GroupSpecialRatio)
	}
	if info.IsModelMapped {
		other.SetPublic("is_model_mapped", true)
		other.SetAdmin("upstream_model_name", info.UpstreamModelName)
	}
	if snap := info.TieredBillingSnapshot; snap != nil {
		other.SetPublic("billing_mode", "tiered_expr")
		other.SetPublic("expr_b64", base64.StdEncoding.EncodeToString([]byte(snap.ExprString)))
		other.SetPublic("matched_tier", snap.EstimatedTier)
		if len(snap.UsageFacts) > 0 {
			other.SetPublic("usage_facts", snap.UsageFacts)
		}
		setTaskImageCount(other, snap.UsageFacts["image_count"])
	} else {
		setTaskImageCount(other, info.PriceData.OtherRatios()["image_count"])
	}
	appendTaskLogInfo(task, other)
	attachQuotaSaturation(c, info, other)
	// P1-2：钱包计费任务在提交日志中记录账户净份额（此时反映预扣+提交结算；
	// 异步差额结算另行记录 task 引用账本与差额日志）。
	if info.BillingSource == BillingSourceWallet {
		refs := RelayWalletCostRefs(info)
		FillWalletCostLogInfo(other, info.UserId, refs)
	}
	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId: info.ChannelId,
		ModelName: info.GetBillingModelName(),
		TokenName: tokenName,
		Quota:     info.PriceData.Quota,
		Content:   logContent,
		TokenId:   info.TokenId,
		Group:     info.UsingGroup,
		Other:     other,
	})
}

// ---------------------------------------------------------------------------
// 异步任务计费辅助函数
// ---------------------------------------------------------------------------

// taskIsSubscription identifies the persisted source, never a runtime preference.
func taskIsSubscription(task *model.Task) bool {
	return task.PrivateData.BillingSource == BillingSourceSubscription && task.PrivateData.SubscriptionId > 0
}

func taskAdjustFunding(task *model.Task, delta int, commit func(*gorm.DB, []model.LedgerShare, bool) error) ([]model.LedgerShare, error) {
	var order []model.LedgerAccount
	if delta > 0 && !taskIsSubscription(task) {
		order = taskDebitOrder(task)
	}
	key := "task:refund:" + task.TaskID
	if delta > 0 {
		key = "task:settle:" + task.TaskID
	}
	var result *model.LedgerResult
	var nextShares []model.LedgerShare
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		err = model.DB.Transaction(func(tx *gorm.DB) error {
			nextShares, result, err = taskAdjustFundingTx(tx, task, delta, key, order)
			if err != nil {
				return err
			}
			if commit != nil {
				return commit(tx, nextShares, result != nil && result.Replayed)
			}
			return nil
		})
		if !errors.Is(err, model.ErrLedgerDuplicate) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	if result != nil {
		model.RefreshUserQuotaCache(task.UserId, result.TotalAfter)
	}
	return nextShares, nil
}

// taskAdjustFundingTx has no out-of-transaction writes. The caller supplies a
// stable lifecycle key and commits the receipt, token and task intent together.
func taskAdjustFundingTx(tx *gorm.DB, task *model.Task, delta int, key string, order []model.LedgerAccount) ([]model.LedgerShare, *model.LedgerResult, error) {
	shares := append([]model.LedgerShare(nil), task.PrivateData.WalletDebit...)
	if taskIsSubscription(task) {
		identity, err := model.GetSubscriptionBillingIdentityTx(tx, task.PrivateData.EffectiveRequestID(), task.UserId, task.PrivateData.SubscriptionId, task.PrivateData.SubscriptionQuotaPeriod, task.PrivateData.SubscriptionReservedAmount)
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, model.AdjustSubscriptionTaskQuotaTx(tx, identity, int64(task.Quota), int64(task.Quota+delta))
	}
	if err := model.ValidateTaskWalletReceipt(shares, task.Quota); err != nil {
		return nil, nil, err
	}
	if delta == 0 {
		return shares, nil, nil
	}
	entry := model.LedgerEntry{
		UserId: task.UserId, Account: model.AccountPaid, ReferenceType: "task",
		ReferenceId: task.TaskID, RequestId: task.TaskID, IdempotencyKey: key,
	}
	if delta > 0 {
		entry.Delta = delta
		entry.Reason = model.LedgerReasonTaskSettle
		result, err := model.DebitQuotaTx(tx, entry, order...)
		if err != nil {
			return nil, nil, err
		}
		return appendTaskShares(shares, result), result, nil
	}
	entry.Reason = model.LedgerReasonTaskRefund
	refunded, err := model.AllocateWalletRefund(shares, nil, -delta)
	if err != nil {
		return nil, nil, err
	}
	result, err := model.CreditQuotaSharesTx(tx, entry, refunded)
	if err != nil {
		return nil, nil, err
	}
	return subtractTaskShares(shares, refunded), result, nil
}

// taskDebitOrder 返回任务钱包扣减顺序：依据任务记录的扣减份额（首个账户优先），
// 无份额记录（旧数据/纯 paid）时按模型赠金资格重新判定。
func taskDebitOrder(task *model.Task) []model.LedgerAccount {
	if len(task.PrivateData.WalletDebit) > 0 {
		order := make([]model.LedgerAccount, 0, 3)
		for _, share := range task.PrivateData.WalletDebit {
			if !slices.Contains(order, share.Account) {
				order = append(order, share.Account)
			}
		}
		// Receipts contain only accounts actually used by pre-consumption. If
		// that exhausted bonus/reward, settlement must still reach the remaining
		// generally spendable funds without granting new bonus eligibility.
		for _, account := range []model.LedgerAccount{model.AccountReward, model.AccountPaid} {
			if !slices.Contains(order, account) {
				order = append(order, account)
			}
		}
		return order
	}
	return WalletDebitOrder(&relaycommon.RelayInfo{BillingModelName: taskModelName(task)})
}

// appendTaskShares 把一次扣减结果的各账户份额累计进任务份额列表。
func appendTaskShares(current []model.LedgerShare, result *model.LedgerResult) []model.LedgerShare {
	if result == nil {
		return current
	}
	index := make(map[model.LedgerAccount]int, len(current))
	next := append([]model.LedgerShare(nil), current...)
	for i, share := range next {
		index[share.Account] = i
	}
	for _, row := range result.Rows {
		if row.Delta >= 0 {
			continue
		}
		account := model.LedgerAccount(row.Account)
		if i, ok := index[account]; ok {
			next[i].Amount += -row.Delta
		} else {
			index[account] = len(next)
			next = append(next, model.LedgerShare{Account: account, Amount: -row.Delta})
		}
	}
	return next
}

func subtractTaskShares(current, refunded []model.LedgerShare) []model.LedgerShare {
	remaining := make(map[model.LedgerAccount]int, len(current))
	for _, share := range current {
		remaining[share.Account] += share.Amount
	}
	for _, share := range refunded {
		remaining[share.Account] -= share.Amount
	}
	next := make([]model.LedgerShare, 0, len(current))
	for _, share := range current {
		if amount := remaining[share.Account]; amount > 0 {
			next = append(next, model.LedgerShare{Account: share.Account, Amount: amount})
		}
	}
	return next
}

// ApplyTaskQuota commits one terminal task decision. Only the task's durable
// final operation, not a wallet-only replay, proves that every financial side
// committed. Callers may safely retry after a rollback or an uncertain COMMIT.
func ApplyTaskQuota(ctx context.Context, task *model.Task, actualQuota int, refund bool) (bool, error) {
	return applyTaskQuota(ctx, task, actualQuota, refund, nil, nil)
}

func applyTaskQuota(ctx context.Context, task *model.Task, actualQuota int, refund bool, reason *common.Message, clamps []*common.QuotaClamp) (bool, error) {
	if task == nil || task.ID <= 0 || actualQuota < 0 || actualQuota > common.MaxQuota || refund && actualQuota != 0 {
		return false, errors.New("invalid task financial adjustment")
	}
	if task.HasLegacyBillingIntent() || task.PrivateData.FinancialReviewReason != "" {
		return false, model.ErrTaskFinancialReview
	}
	// The policy lookup can use model.DB, so never perform it inside the main
	// transaction. Nonempty locked receipts determine their own account order.
	order := taskDebitOrder(task)
	var persisted *model.Task
	var result *model.LedgerResult
	var display, export *model.RecordTaskBillingLogParams
	applied := false
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		persisted, err = model.LoadTaskFinancialTx(tx, task.ID, task.UserId)
		if err != nil {
			return err
		}
		if persisted.TaskID == "" || persisted.TaskID != task.TaskID || persisted.HasLegacyBillingIntent() || persisted.PrivateData.FinancialReviewReason != "" || persisted.PrivateData.FinancialVersion != 2 || !persisted.PrivateData.SubmissionAccounted || persisted.PrivateData.TokenId < 0 {
			return model.ErrTaskFinancialReview
		}
		if persisted.SettlementPending != (persisted.PrivateData.FinancialIntent != nil) {
			return fmt.Errorf("%w: incomplete current task intent", model.ErrTaskFinancialReview)
		}
		if persisted.PrivateData.TokenId > 0 && len(persisted.PrivateData.TokenFingerprint) != 64 || persisted.PrivateData.TokenId == 0 && persisted.PrivateData.TokenFingerprint != "" {
			return fmt.Errorf("%w: original task token identity is missing", model.ErrTaskFinancialReview)
		}
		if persisted.PrivateData.FinancialOperationID != "" {
			if persisted.PrivateData.FinancialActualQuota != actualQuota || persisted.Quota != actualQuota {
				return fmt.Errorf("%w: conflicting terminal task amount", model.ErrTaskFinancialReview)
			}
			return nil
		}
		if refund && persisted.Status == model.TaskStatusSuccess {
			return fmt.Errorf("%w: conflicting terminal task status", model.ErrTaskFinancialReview)
		}
		// Some existing provider policies charge measured partial usage even on
		// FAILURE. Only the exact persisted terminal decision authorizes that;
		// callers cannot reinterpret an ordinary failed task as a new charge.
		if !refund && persisted.Status == model.TaskStatusFailure {
			intent := persisted.PrivateData.FinancialIntent
			if intent == nil || intent.Kind != "settle" || intent.PreviousQuota != persisted.Quota || intent.ActualQuota != actualQuota || intent.OperationID == "" {
				return fmt.Errorf("%w: failure has no matching usage decision", model.ErrTaskFinancialReview)
			}
		}
		previousQuota := persisted.Quota
		if previousQuota < 0 || previousQuota > common.MaxQuota {
			return model.ErrTaskFinancialReview
		}
		kind := "settle"
		if refund {
			kind = "refund"
		}
		operationID := fmt.Sprintf("task:financial:%d:%d", persisted.ID, actualQuota)
		if intent := persisted.PrivateData.FinancialIntent; intent != nil {
			if persisted.PrivateData.FinancialVersion != 2 || intent.Kind != kind || intent.PreviousQuota != previousQuota || intent.ActualQuota != actualQuota || intent.OperationID == "" {
				return fmt.Errorf("%w: task intent mismatch", model.ErrTaskFinancialReview)
			}
			operationID = intent.OperationID
			if intent.Reason != nil {
				reason = intent.Reason
			}
			if intent.Clamp != nil {
				clamps = append(clamps, intent.Clamp)
			}
		}
		if len(operationID) > 255 || persisted.PrivateData.BillingSource != "" && persisted.PrivateData.BillingSource != BillingSourceWallet && !taskIsSubscription(persisted) {
			return model.ErrTaskFinancialReview
		}
		if !taskIsSubscription(persisted) {
			if err := model.RejectTaskSourceOnlyReplayTx(tx, persisted.UserId, operationID, "task:settle:"+persisted.TaskID, "task:refund:"+persisted.TaskID); err != nil {
				return err
			}
		}
		delta := actualQuota - previousQuota
		if len(persisted.PrivateData.WalletDebit) > 0 {
			order = taskDebitOrder(persisted)
		}
		if err := model.LockFinancialUserTx(tx, persisted.UserId); err != nil {
			return err
		}
		shares, ledgerResult, err := taskAdjustFundingTx(tx, persisted, delta, operationID, order)
		if err != nil {
			return err
		}
		if ledgerResult != nil && ledgerResult.Replayed {
			return fmt.Errorf("%w: source-only task replay", model.ErrTaskFinancialReview)
		}
		result = ledgerResult
		if err := model.ApplyBillingTokenDeltaTx(tx, &model.BillingFinalization{UserID: persisted.UserId, TokenID: persisted.PrivateData.TokenId, TokenFingerprint: persisted.PrivateData.TokenFingerprint}, -delta); err != nil {
			return err
		}
		if err := model.ApplyTaskUsageDeltaTx(tx, persisted.UserId, persisted.ChannelId, delta, 0); err != nil {
			return err
		}
		persisted.Quota = actualQuota
		persisted.PrivateData.WalletDebit = shares
		persisted.PrivateData.FinancialVersion = 2
		persisted.PrivateData.FinancialOperationID = operationID
		persisted.PrivateData.FinancialActualQuota = actualQuota
		persisted.PrivateData.FinancialIntent = nil
		persisted.SettlementPending = false
		if err := persisted.UpdateBillingStateTx(tx, previousQuota); err != nil {
			return err
		}
		if delta != 0 {
			logType, logQuota := model.LogTypeConsume, delta
			if delta < 0 {
				logType, logQuota = model.LogTypeRefund, -delta
			}
			other := taskBillingOther(persisted)
			other.SetPublic("pre_consumed_quota", previousQuota)
			other.SetPublic("actual_quota", actualQuota)
			if refund && reason != nil {
				other.SetPublic("reason", reason.Error())
			}
			for _, clamp := range clamps {
				attachQuotaSaturationToOther(other, clamp)
			}
			params := model.RecordTaskBillingLogParams{UserId: persisted.UserId, LogType: logType, ChannelId: persisted.ChannelId, ModelName: taskModelName(persisted), Quota: logQuota, TokenId: persisted.PrivateData.TokenId, Group: persisted.Group, Other: other, NodeName: persisted.PrivateData.NodeName, OperationID: operationID}
			if reason != nil {
				params.Content = []*common.Message{reason}
			}
			logged, err := model.RecordTaskBillingLogTx(tx, params)
			if err != nil {
				return err
			}
			if !logged {
				display = &params
			} else {
				export = &params
			}
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	*task = *persisted
	if result != nil {
		model.RefreshUserQuotaCache(task.UserId, result.TotalAfter)
	}
	// A separate log store is display-only: emit only after an acknowledged
	// new commit, never on replay or uncertain commit recovery.
	if applied && display != nil {
		model.RecordTaskBillingLog(*display)
	} else if applied && export != nil {
		model.RecordTaskQuotaExport(ctx, *export)
	}
	return applied, nil
}

// taskBillingOther 从 task 的 BillingContext 构建日志 Other 字段。
func taskBillingOther(task *model.Task) *model.LogOther {
	other := model.NewLogOther()
	// 前端列表按 is_task 选择任务档位渲染分支,缺失会把差额/退款日志误判成普通 relay 动态计费
	other.SetPublic("is_task", true)
	if bc := task.PrivateData.BillingContext; bc != nil {
		other.SetPublic("model_price", bc.ModelPrice)
		if bc.ModelRatio > 0 {
			other.SetPublic("model_ratio", bc.ModelRatio)
		}
		other.SetPublic("group_ratio", bc.GroupRatio)
		if priceData := taskBillingContextPriceData(bc); priceData != nil {
			for k, v := range priceData.OtherRatios() {
				if !other.SetPublic(k, v) {
					common.SysError(common.LogText("task billing other ratio key rejected: %s", k))
				}
			}
		}
		if snap := bc.TieredSnapshot; snap != nil {
			other.SetPublic("billing_mode", "tiered_expr")
			other.SetPublic("expr_b64", base64.StdEncoding.EncodeToString([]byte(snap.ExprString)))
			other.SetPublic("matched_tier", snap.EstimatedTier)
			if len(snap.UsageFacts) > 0 {
				other.SetPublic("usage_facts", snap.UsageFacts)
			}
			setTaskImageCount(other, snap.UsageFacts["image_count"])
		} else if priceData := taskBillingContextPriceData(bc); priceData != nil {
			setTaskImageCount(other, priceData.OtherRatios()["image_count"])
		}
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other.SetPublic("is_model_mapped", true)
		other.SetAdmin("upstream_model_name", props.UpstreamModelName)
	}
	appendTaskLogInfo(task, other)
	return other
}

// setTaskImageCount publishes the billed image quantity of an image task as
// other.image_count, the same field the HTTP image relay writes, so the log
// detail shows one "billable image count" regardless of the serving path. The
// value is a host-validated count fact (at most dto.MaxImageN), never a quota.
func setTaskImageCount(other *model.LogOther, value any) {
	count, ok := value.(float64)
	if !ok || count < 0 || count > float64(dto.MaxImageN) {
		return
	}
	other.SetPublic("image_count", common.QuotaRound(count))
}

// taskDeliveredInline reports whether the submitting HTTP request itself
// returns the task deliverable: the upstream completed immediately, or the
// request came through the synchronous OpenAI Images protocol, where the host
// waits for an asynchronous upstream task before answering. The usage log
// presents such requests as synchronous instead of as asynchronous jobs.
func taskDeliveredInline(c *gin.Context, task *model.Task) bool {
	if task != nil && (task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure) {
		return true
	}
	pinnedValue, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint)
	if !exists {
		return false
	}
	pinned, ok := pinnedValue.(jsplugin.PinnedEndpoint)
	return ok && pinned.Protocol == jsplugin.ProtocolOpenAIImage
}

func appendTaskLogInfo(task *model.Task, other *model.LogOther) {
	if task == nil || other == nil {
		return
	}
	if task.TaskID != "" {
		other.SetPublic("task_id", task.TaskID)
	}
	if task.PrivateData.ResultDiscarded {
		// The result was delivered inline and the upstream snapshot was not
		// persisted, so no artifact can be retrieved for this task.
		other.SetPublic("result_discarded", true)
	}
	if task.PrivateData.Execution != nil {
		AppendTaskPluginAuditInfo(other, task.PrivateData.Execution.TaskPlugin)
	}
	if task.PrivateData.UpstreamTaskID == "" && task.PrivateData.NodeName == "" {
		return
	}
	if task.PrivateData.UpstreamTaskID != "" {
		other.SetRoot("upstream_task_id", task.PrivateData.UpstreamTaskID)
	}
	if task.PrivateData.NodeName != "" {
		other.SetRoot("node_name", task.PrivateData.NodeName)
	}
}

func taskBillingContextPriceData(bc *model.TaskBillingContext) *types.PriceData {
	if bc == nil || len(bc.OtherRatios) == 0 {
		return nil
	}
	priceData := &types.PriceData{}
	if !priceData.ReplaceOtherRatios(bc.OtherRatios) {
		return nil
	}
	return priceData
}

// taskModelName 返回任务的计费模型身份（BillingContext.BillingModelName 优先，
// 旧任务回退 OriginModelName；再回退 Properties.OriginModelName）。
// 计费重算、赠金资格与任务日志均使用该身份，保证与提交时价格口径一致。
func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil {
		if name := bc.EffectiveBillingModelName(); name != "" {
			return name
		}
	}
	return task.Properties.OriginModelName
}

// RefundTaskQuota refunds the current outstanding charge, not a submission
// snapshot. Every financial effect and the final marker commits atomically.
func RefundTaskQuota(ctx context.Context, task *model.Task, reason string) bool {
	_, err := applyTaskQuota(ctx, task, 0, true, common.NewMessage(reason), nil)
	if err != nil {
		logger.LogWarn(ctx, "task refund requires retry or reconciliation: "+err.Error())
		return false
	}
	return true
}

// RecalculateTaskQuota keeps pricing unchanged; only the application of its
// computed final amount is transactional and exactly once.
func RecalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason *common.Message, clamps ...*common.QuotaClamp) {
	if _, err := applyTaskQuota(ctx, task, actualQuota, false, reason, clamps); err != nil {
		logger.LogError(ctx, "failed to commit task financial adjustment: "+err.Error())
	}
}

// RecalculateTaskQuotaByTokens 根据实际 token 消耗重新计费（异步差额结算）。
// 当任务成功且返回了 totalTokens 时，根据模型倍率和分组倍率重新计算实际扣费额度，
// 与预扣费的差额进行补扣或退还。支持钱包和订阅计费来源。
func RecalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) bool {
	actualQuota, reason, clamp, ok := taskQuotaByTokens(task, totalTokens)
	if !ok {
		return false
	}
	RecalculateTaskQuota(ctx, task, actualQuota, reason, clamp)
	return true
}

func taskQuotaByTokens(task *model.Task, totalTokens int) (int, *common.Message, *common.QuotaClamp, bool) {
	if totalTokens <= 0 {
		return 0, nil, nil, false
	}

	modelName := taskModelName(task)

	// 获取模型价格和倍率
	modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(modelName)
	// 只有配置了倍率(非固定价格)时才按 token 重新计费
	if !hasRatioSetting || modelRatio <= 0 {
		return 0, nil, nil, false
	}

	// 获取用户和组的倍率信息
	group := task.Group
	if group == "" {
		user, err := model.GetUserById(task.UserId, false)
		if err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return 0, nil, nil, false
	}

	groupRatio := ratio_setting.GetGroupRatio(group)
	userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group)

	var finalGroupRatio float64
	if hasUserGroupRatio {
		finalGroupRatio = userGroupRatio
	} else {
		finalGroupRatio = groupRatio
	}

	// 计算 OtherRatios 乘积（视频折扣、时长等）
	otherMultiplier := 1.0
	if priceData := taskBillingContextPriceData(task.PrivateData.BillingContext); priceData != nil {
		otherMultiplier = priceData.OtherRatioMultiplier()
	}

	// 计算实际应扣费额度: totalTokens * modelRatio * groupRatio * otherMultiplier（饱和转换，防止溢出成负数）
	actualQuota, clamp := common.QuotaFromFloatChecked(float64(totalTokens) * modelRatio * finalGroupRatio * otherMultiplier)

	reason := common.NewMessage("Recalculated from tokens: tokens {{tokens}}, model ratio {{model_ratio}}, group ratio {{group_ratio}}, other multiplier {{other_multiplier}}", map[string]any{
		"tokens":           totalTokens,
		"model_ratio":      fmt.Sprintf("%.2f", modelRatio),
		"group_ratio":      fmt.Sprintf("%.2f", finalGroupRatio),
		"other_multiplier": fmt.Sprintf("%.4f", otherMultiplier),
	})
	return actualQuota, reason, clamp, true
}

// EvaluateTaskCompletionUsage evaluates actual facts against the frozen task
// expression. It neither mutates the snapshot nor moves funds: synchronous
// submission and polling have different persistence and settlement barriers.
func EvaluateTaskCompletionUsage(snap *billingexpr.BillingSnapshot, facts map[string]any) (billingexpr.TieredResult, map[string]any, error) {
	if snap == nil {
		return billingexpr.TieredResult{}, nil, fmt.Errorf("task billing snapshot is missing")
	}
	usage := make(map[string]any, len(snap.UsageFacts)+len(facts))
	maps.Copy(usage, snap.UsageFacts)
	maps.Copy(usage, facts)
	request := billingexpr.RequestInput{Usage: usage}
	if snap.RequestInput != nil {
		request.Headers = maps.Clone(snap.RequestInput.Headers)
		request.Params = maps.Clone(snap.RequestInput.Params)
		request.Timestamp = snap.RequestInput.Timestamp
		request.ImageCount = snap.RequestInput.ImageCount
	} else if billingexpr.UsesRequestContext(snap.ExprString) {
		return billingexpr.TieredResult{}, usage, fmt.Errorf("task billing snapshot has no request-time pricing context")
	}
	result, err := billingexpr.ComputeTieredQuotaWithRequest(snap, billingexpr.TokenParams{}, request)
	if err == nil && (result.ActualQuotaBeforeGroup < 0 || math.IsNaN(result.ActualQuotaBeforeGroup)) {
		err = fmt.Errorf("task completion expression produced an invalid cost")
	}
	return result, usage, err
}
