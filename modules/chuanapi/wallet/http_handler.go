package wallet

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/paymentcore"

	"github.com/gin-gonic/gin"
)

// WalletRuleSummary 是独立 Dashboard 使用的赠金与邀请奖励规则快照。
type WalletRuleSummary struct {
	SignupBonusEnabled   bool    `json:"signup_bonus_enabled"`
	SignupBonusQuota     int     `json:"signup_bonus_quota"`
	SignupBonusProvider  string  `json:"signup_bonus_provider"`
	ReferralBonusEnabled bool    `json:"referral_bonus_enabled"`
	ReferralBonusRate    float64 `json:"referral_bonus_rate"`
}

func walletRuleSummary() WalletRuleSummary {
	return WalletRuleSummary{
		SignupBonusEnabled:   true,
		SignupBonusQuota:     common.QuotaRound(common.LinuxDOSignupBonusUSD * common.QuotaPerUnit),
		SignupBonusProvider:  "linuxdo",
		ReferralBonusEnabled: common.AffiliateRewardEnabled,
		ReferralBonusRate:    common.AffiliateRewardRate,
	}
}

// GetWalletSummary 公开钱包三账户：paid、bonus 与划转后的邀请奖励 reward。
func GetWalletSummary(c *gin.Context) {
	userId := c.GetInt("id")
	paid, bonus, reward, err := model.GetUserAccountBalances(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"total":  paid + bonus + reward,
			"paid":   paid,
			"bonus":  bonus,
			"reward": reward,
			"rules":  walletRuleSummary(),
		},
	})
}

type LedgerRowDTO struct {
	Id            int    `json:"id"`
	Account       string `json:"account"`
	Delta         int    `json:"delta"`
	BalanceBefore int    `json:"before"`
	BalanceAfter  int    `json:"after"`
	TotalBefore   int    `json:"total_before"`
	TotalAfter    int    `json:"total_after"`
	Reason        string `json:"reason"`
	ReferenceType string `json:"reference_type"`
	ReferenceId   string `json:"reference"`
	RequestId     string `json:"request_id"`
	OperatorId    int    `json:"operator_id"`
	CreatedAt     int64  `json:"created_at"`
}

func GetWalletLedger(c *gin.Context) {
	userId := c.GetInt("id")
	account := c.Query("account")
	if account != "" && account != string(model.AccountPaid) && account != string(model.AccountBonus) && account != string(model.AccountReward) {
		common.ApiErrorMsg(c, "account 仅支持 paid、bonus 或 reward")
		return
	}
	page, ok := getPageParam(c)
	if !ok {
		return
	}
	filter := model.LedgerQueryFilter{
		UserId:          userId,
		Account:         account,
		AllowedAccounts: []model.LedgerAccount{model.AccountPaid, model.AccountBonus, model.AccountReward},
		Reason:          c.Query("reason"),
		Page:            page,
		PageSize:        getPageSizeParam(c),
	}
	if v := c.Query("start_time"); v != "" {
		ts, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			common.ApiErrorMsg(c, "start_time 无效")
			return
		}
		filter.StartTime = ts
	}
	if v := c.Query("end_time"); v != "" {
		ts, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			common.ApiErrorMsg(c, "end_time 无效")
			return
		}
		filter.EndTime = ts
	}
	ledgerPage, err := model.QueryUserLedger(filter)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	rows := make([]LedgerRowDTO, 0, len(ledgerPage.Rows))
	for _, row := range ledgerPage.Rows {
		rows = append(rows, LedgerRowDTO{
			Id: row.Id, Account: row.Account, Delta: row.Delta,
			BalanceBefore: row.BalanceBefore, BalanceAfter: row.BalanceAfter,
			TotalBefore: row.TotalBefore, TotalAfter: row.TotalAfter,
			Reason: row.Reason, ReferenceType: row.ReferenceType,
			ReferenceId: row.ReferenceId, RequestId: row.RequestId,
			OperatorId: row.OperatorId, CreatedAt: row.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"items": rows, "total": ledgerPage.Total},
	})
}

// ReferralCommissionRowDTO 是返佣记录的脱敏视图：只展示当前用户自己的奖励，
// 来源好友仅返回脱敏用户名。
type ReferralCommissionRowDTO struct {
	Id           int     `json:"id"`
	SourceUserId int     `json:"source_user_id"`
	SourceName   string  `json:"source_name"`
	PaidQuota    int     `json:"paid_quota"`
	RewardQuota  int     `json:"reward_quota"`
	Rate         float64 `json:"rate"`
	CreatedAt    int64   `json:"created_at"`
}

// GetReferralRewards 分页返回当前用户的返佣记录（新表 referral_commissions）。
func GetReferralRewards(c *gin.Context) {
	userId := c.GetInt("id")
	page, ok := getPageParam(c)
	if !ok {
		return
	}
	commissions, total, err := model.ListReferralCommissions(userId, page, getPageSizeParam(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	sourceIds := make([]int, 0, len(commissions))
	for _, commission := range commissions {
		sourceIds = append(sourceIds, commission.SourceUserId)
	}
	usernames, err := model.GetUsernamesByIds(sourceIds)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	rows := make([]ReferralCommissionRowDTO, 0, len(commissions))
	for _, commission := range commissions {
		rows = append(rows, ReferralCommissionRowDTO{
			Id:           commission.Id,
			SourceUserId: commission.SourceUserId,
			SourceName:   maskReferralSourceName(usernames[commission.SourceUserId]),
			PaidQuota:    commission.PaidQuota,
			RewardQuota:  commission.RewardQuota,
			Rate:         commission.Rate,
			CreatedAt:    commission.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"items": rows, "total": total},
	})
}

// AffiliateTransferRequest 是划转请求体；quota 以整数 quota units 表示。
type AffiliateTransferRequest struct {
	Quota *int64 `json:"quota"`
}

// transferRequestBodyMaxBytes 限制划转请求体大小：请求只有一个整数字段，
// 有界读取避免超大 JSON 占用内存。
const transferRequestBodyMaxBytes = 4 * 1024

// TransferAffQuota 把待使用邀请奖励划转到 wallet reward 账户。
// 新客户端必须发送 Idempotency-Key：同键同金额重放返回原结果，不重复扣加；
// 同键不同金额明确冲突失败。保留 payment compliance 与用户级限流（路由层）。
func TransferAffQuota(c *gin.Context) {
	if !paymentcore.RequireCompliance(c) {
		return
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if !model.ValidReferralTransferIdempotencyKey(idempotencyKey) {
		common.ApiErrorMsg(c, "缺少或无效的 Idempotency-Key")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, transferRequestBodyMaxBytes)
	var request AffiliateTransferRequest
	if err := common.DecodeJsonStrict(c.Request.Body, &request); err != nil || request.Quota == nil {
		common.ApiErrorMsg(c, "请求参数错误")
		return
	}
	amount := *request.Quota
	if amount <= 0 {
		common.ApiErrorMsg(c, "划转额度必须为正整数")
		return
	}
	if amount > int64(common.MaxWalletQuota) {
		common.ApiErrorMsg(c, "划转额度超出上限")
		return
	}
	if common.QuotaPerUnit <= 0 || amount < int64(common.QuotaPerUnit) {
		common.ApiErrorI18n(c, i18n.MsgUserTransferQuotaMinimum, map[string]any{"Min": logger.FormatQuota(int(common.QuotaPerUnit))})
		return
	}
	result, err := model.TransferAffQuota(c.GetInt("id"), int(amount), idempotencyKey)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrLedgerInsufficientBalance):
			common.ApiErrorMsg(c, "待使用邀请奖励不足")
		case errors.Is(err, model.ErrLedgerIdempotencyConflict):
			common.ApiErrorMsg(c, "Idempotency-Key 与已处理的划转参数不一致")
		default:
			common.SysError("affiliate transfer failed: " + err.Error())
			common.ApiErrorI18n(c, i18n.MsgUserTransferFailed, map[string]any{"Error": "请稍后重试"})
		}
		return
	}
	if result != nil && !result.Replayed {
		model.RecordLog(c.GetInt("id"), model.LogTypeSystem, common.NewMessage("Referral rewards transferred to balance: {{quota}}", map[string]any{"quota": logger.FormatQuota(int(amount))}))
	}
	common.ApiSuccessI18n(c, i18n.MsgUserTransferSuccess, nil)
}

// maskReferralSourceName 只保留来源好友用户名的首字符，避免向邀请人泄露完整身份。
func maskReferralSourceName(username string) string {
	runes := []rune(strings.TrimSpace(username))
	if len(runes) == 0 {
		return "***"
	}
	return string(runes[0]) + "***"
}

// maxWalletPageParam 是钱包类分页查询的页码上限；按最大 page_size=200 计算
// 偏移量上限 2e8，任何平台都不会发生 int 溢出。超大页码明确失败。
const maxWalletPageParam = 1_000_000

func getPageParam(c *gin.Context) (int, bool) {
	page, err := strconv.Atoi(c.DefaultQuery("p", "1"))
	if err != nil || page <= 0 {
		return 1, true
	}
	if page > maxWalletPageParam {
		common.ApiErrorMsg(c, "分页参数超出范围")
		return 0, false
	}
	return page, true
}

func getPageSizeParam(c *gin.Context) int {
	size, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil || size <= 0 {
		return 20
	}
	if size > 200 {
		return 200
	}
	return size
}
