package model

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReferralTransferIdempotencyKeyMaxLength 是划转幂等键的长度上限；模型层与
// HTTP 层共用同一校验，防止绕过控制器直接调用模型时写入异常键。
const ReferralTransferIdempotencyKeyMaxLength = 64

// maxWalletPage 是钱包类分页查询的页码上限。按最大 pageSize=200 计算偏移量
// 上限为 2e8，任何平台都不会发生 int 溢出。返佣记录超过上限明确失败；账本
// 分页把它作为有界回退，避免构造回绕后的负偏移查询。
const maxWalletPage = 1_000_000

const (
	// ReferralCommissionMaxTopUpEvents 是每位受邀用户产生邀请奖励的成功在线
	// 余额充值次数上限：好友前三次充值，各按有效比例一次性返佣。
	ReferralCommissionMaxTopUpEvents = 3

	// ReferralReferenceTopUp 是唯一合格返佣来源：成功在线余额充值。
	ReferralReferenceTopUp = "topup"
)

var (
	ErrReferralTransferInvalid = errors.New("invalid referral transfer request")
)

// ReferralCommission 承载一次成功在线余额充值对应的一次性返佣凭证。
// 只记录已发生的返佣事实，不含冻结/解锁/消费对账状态；同一充值引用通过
// 唯一索引保证幂等。
type ReferralCommission struct {
	Id            int     `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId        int     `json:"user_id" gorm:"not null;index:idx_referral_commission_user,priority:1"`
	SourceUserId  int     `json:"source_user_id" gorm:"not null;index"`
	ReferenceType string  `json:"reference_type" gorm:"type:varchar(32);not null;uniqueIndex:idx_referral_commission_ref,priority:1"`
	ReferenceId   string  `json:"reference_id" gorm:"type:varchar(128);not null;uniqueIndex:idx_referral_commission_ref,priority:2"`
	PaidQuota     int     `json:"paid_quota" gorm:"type:bigint;not null"`
	RewardQuota   int     `json:"reward_quota" gorm:"type:bigint;not null"`
	Rate          float64 `json:"rate" gorm:"type:decimal(10,6);not null"`
	CreatedAt     int64   `json:"created_at" gorm:"bigint;autoCreateTime;index:idx_referral_commission_user,priority:2"`
}

func (ReferralCommission) TableName() string {
	return "referral_commissions"
}

// RecordReferralCommissionForTopUpTx 在一次成功在线余额充值结算事务内创建返佣
// 凭证，并在同一事务内把奖励记入邀请人的 aff_quota（待使用）与 aff_history
// （累计）。前三次成功充值计次：计次基于订单（top_ups）而非新凭证，历史订单
// 同样计入；subscription 关联 top_ups、兑换码、管理员加额、余额买订阅以及没有
// 在线支付凭证的管理员补单都不合格。
//
// 只有 reason=LedgerReasonTopup（真实在线支付回调）才产生奖励；管理员补单的
// admin_topup 直接跳过。奖励计算或邀请人余额异常（负值、超过钱包上限）返回错误，
// 由调用方整体回滚订单、充值账本与返佣，绝不静默放弃应发奖励后完成充值。
// 开关开启且本次确有应发奖励时，非有限或越界（含负值）比例同样返回错误整体
// 回滚；合法 0 比例视为无奖励。有效充值金额向下取整为 0 时视为无奖励，属于
// 正常业务结果而非异常。
// 重复结算由订单状态翻转、账本幂等头与唯一幂等身份保证只生成一次。
func RecordReferralCommissionForTopUpTx(tx *gorm.DB, sourceUserId, paidQuota int, referenceType, referenceId, reason string) error {
	if tx == nil {
		return errors.New("referral commission: transaction must not be nil")
	}
	if !common.AffiliateRewardEnabled {
		return nil
	}
	rate := common.AffiliateRewardRate
	// 合法 0 比例不产生奖励，是正常业务结果。NaN/±Inf/负值等非法比例不能在
	// 这里被 `<= 0` 提前吞掉，必须留到确认本次应发奖励后返回错误。
	if rate == 0 {
		return nil
	}
	if referenceType != ReferralReferenceTopUp || referenceId == "" || paidQuota <= 0 {
		return nil
	}
	// 管理员补单/直接加额没有在线支付凭证，不合格也不占用线上充值计次。
	if reason != LedgerReasonTopup {
		return nil
	}

	// 锁定被邀请人（充值用户）行，使同一用户并发充值的计次串行化。
	var source User
	if err := lockForUpdate(tx).Select("id", "status", "inviter_id").Where("id = ?", sourceUserId).First(&source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if source.Status != common.UserStatusEnabled || source.InviterId <= 0 || source.InviterId == sourceUserId {
		return nil
	}

	// 本次 reference 对应的订单必须自身合格，不能只看历史计次就发奖。
	orderQualifies, err := currentTopUpOrderQualifiesTx(tx, sourceUserId, referenceId)
	if err != nil {
		return err
	}
	if !orderQualifies {
		return nil
	}

	topUpEvents, err := countQualifyingTopUpEventsTx(tx, sourceUserId)
	if err != nil {
		return err
	}
	if topUpEvents <= 0 || topUpEvents > ReferralCommissionMaxTopUpEvents {
		return nil
	}

	// 本次确有应发奖励：任何非有限或越界比例（含负有限值）都必须返回错误让
	// 订单、充值与返佣整体回滚，而不是静默漏发后把充值做成成功。
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
		return fmt.Errorf("referral commission: invalid rate %v for topup %s", rate, referenceId)
	}
	rewardDecimal := decimal.NewFromInt(int64(paidQuota)).Mul(decimal.NewFromFloat(rate)).Truncate(0)
	rewardQuota, err := common.WalletQuotaFromDecimalStrict(rewardDecimal)
	if err != nil {
		return fmt.Errorf("referral commission: convert reward for topup %s: %w", referenceId, err)
	}
	if rewardQuota <= 0 {
		// 有效但极小的充值金额向下取整为 0：无奖励，属于正常结果。
		return nil
	}

	var inviter User
	if err := lockForUpdate(tx).Select("id", "status", "aff_quota", "aff_history").Where("id = ?", source.InviterId).First(&inviter).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if inviter.Status != common.UserStatusEnabled {
		return nil
	}
	// 非法余额（负值）与任一字段将超过钱包上限都必须整体回滚，而不是跳过奖励。
	if inviter.AffQuota < 0 || inviter.AffHistoryQuota < 0 {
		return fmt.Errorf("referral commission: inviter %d has negative aff balance (quota=%d history=%d)",
			inviter.Id, inviter.AffQuota, inviter.AffHistoryQuota)
	}
	if inviter.AffQuota > common.MaxWalletQuota-rewardQuota || inviter.AffHistoryQuota > common.MaxWalletQuota-rewardQuota {
		return fmt.Errorf("referral commission: inviter %d aff balance would overflow (quota=%d history=%d reward=%d)",
			inviter.Id, inviter.AffQuota, inviter.AffHistoryQuota, rewardQuota)
	}

	commission := &ReferralCommission{
		UserId:        inviter.Id,
		SourceUserId:  sourceUserId,
		ReferenceType: referenceType,
		ReferenceId:   referenceId,
		PaidQuota:     paidQuota,
		RewardQuota:   rewardQuota,
		Rate:          rate,
		CreatedAt:     common.GetTimestamp(),
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(commission)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// 同一充值引用的并发/重复结算：已存在凭证，不重复入账。
		return nil
	}
	// 条件更新同时防负值与上限：任何一项不满足则返回错误整体回滚。
	update := tx.Model(&User{}).
		Where("id = ? AND aff_quota >= 0 AND aff_history >= 0 AND aff_quota <= ? AND aff_history <= ?",
			inviter.Id, common.MaxWalletQuota-rewardQuota, common.MaxWalletQuota-rewardQuota).
		Updates(map[string]interface{}{
			"aff_quota":   gorm.Expr("aff_quota + ?", rewardQuota),
			"aff_history": gorm.Expr("aff_history + ?", rewardQuota),
		})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected == 0 {
		return fmt.Errorf("referral commission: inviter %d aff balance changed during award", inviter.Id)
	}
	return nil
}

// qualifyingTopUpOrderScope 是"真实成功在线余额充值订单"的共享谓词：
//   - status=success 且 Amount/Money 均为正（排除负金额、0 元与无实付伪订单）；
//   - 排除余额账户支付（provider/method 任一为 balance）；
//   - 至少存在一个非空支付 provider/method 凭证（老版本订单可能 provider 为空
//     但留下已知线上 method 与正实付，仍合格；两者皆空的伪订单不合格）；
//   - 排除与 subscription_orders 关联的 upsert 订单（外部订阅）。
func qualifyingTopUpOrderScope(tx *gorm.DB) *gorm.DB {
	return tx.
		Where("top_ups.status = ?", common.TopUpStatusSuccess).
		Where("top_ups.amount > 0 AND top_ups.money > 0").
		Where("COALESCE(top_ups.payment_method, '') <> ?", PaymentMethodBalance).
		Where("COALESCE(top_ups.payment_provider, '') <> ?", PaymentProviderBalance).
		Where("COALESCE(top_ups.payment_provider, '') <> '' OR COALESCE(top_ups.payment_method, '') <> ''").
		Where("NOT EXISTS (SELECT 1 FROM subscription_orders WHERE subscription_orders.trade_no = top_ups.trade_no)")
}

// currentTopUpOrderQualifiesTx 核实本次返佣引用对应的成功订单确实是在线支付：
// 必须属于该被邀请人且满足 qualifyingTopUpOrderScope（正实付、有支付凭证、
// 非余额账户、未关联外部订阅）。
func currentTopUpOrderQualifiesTx(tx *gorm.DB, sourceUserId int, referenceId string) (bool, error) {
	var count int64
	err := qualifyingTopUpOrderScope(tx.Model(&TopUp{})).
		Where("top_ups.user_id = ? AND top_ups.trade_no = ?", sourceUserId, referenceId).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// countQualifyingTopUpEventsTx 统计被邀请人的历史合格充值次数（含本次，调用前
// 本次订单已翻转为 success）。计次口径为成功实付在线余额订单：
//   - 只计 status=success 且 Amount/Money 均为正、带支付凭证的 top_ups；
//   - 排除与 subscription_orders 关联的 top_ups（外部订阅，金额为 0）；
//   - 排除余额账户支付标记；
//   - 排除没有在线支付凭证的管理员补单（账本中存在 admin_topup 记录）；管理员
//     直接加额、兑换码不产生 top_ups，天然不计次；
//   - 没有账本记录的存量在线成功订单仍按订单行计次（有证据的历史订单）。
func countQualifyingTopUpEventsTx(tx *gorm.DB, sourceUserId int) (int, error) {
	var count int64
	err := qualifyingTopUpOrderScope(tx.Model(&TopUp{})).
		Where("top_ups.user_id = ?", sourceUserId).
		Where("NOT EXISTS (SELECT 1 FROM balance_ledger WHERE balance_ledger.reason = ? AND balance_ledger.reference_type = ? AND balance_ledger.reference_id = top_ups.trade_no)", LedgerReasonAdminTopup, ReferralReferenceTopUp).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// ListReferralCommissions 分页返回指定用户（邀请人）的返佣凭证，按时间倒序。
// 页码上限防止 (page-1)*pageSize 溢出；超过上限明确失败而不是执行无界偏移。
func ListReferralCommissions(userId, page, pageSize int) ([]ReferralCommission, int64, error) {
	if userId <= 0 || page > maxWalletPage {
		return nil, 0, ErrReferralTransferInvalid
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	var total int64
	if err := DB.Model(&ReferralCommission{}).Where("user_id = ?", userId).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []ReferralCommission
	if err := DB.Where("user_id = ?", userId).
		Order("created_at desc, id desc").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ValidReferralTransferIdempotencyKey 校验划转幂等键：非空、不超过 64 字符、
// 只允许 JSON 安全字符集 [A-Za-z0-9._:-]。模型层与 HTTP 层共用，防止绕过
// 控制器直接调用模型时把超长或带控制字符的键写入账本。
func ValidReferralTransferIdempotencyKey(key string) bool {
	if key == "" || len(key) > ReferralTransferIdempotencyKeyMaxLength {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == ':':
		default:
			return false
		}
	}
	return true
}

// TransferAffQuota 把待使用邀请奖励 aff_quota 划转到可消费的 reward 账户，
// 同一事务内写账本并减少 aff_quota；aff_history 为累计值，不因划转减少。
//
// 幂等：IdempotencyKey 由用户提供、后端按用户命名空间化。同键同金额重放返回
// 原始结果（Replayed=true）且不再扣减 aff_quota；同键不同金额返回
// ErrLedgerIdempotencyConflict。余额不足、正数、上限与键格式校验失败不产生任何变动。
func TransferAffQuota(userId, amount int, idempotencyKey string) (*LedgerResult, error) {
	key := strings.TrimSpace(idempotencyKey)
	if userId <= 0 || amount <= 0 || amount > common.MaxWalletQuota || !ValidReferralTransferIdempotencyKey(key) {
		return nil, ErrReferralTransferInvalid
	}
	if common.QuotaPerUnit <= 0 || float64(amount) < common.QuotaPerUnit {
		return nil, ErrReferralTransferInvalid
	}
	operationKey := fmt.Sprintf("aff_transfer:%d:%s", userId, key)
	entry := LedgerEntry{
		UserId:         userId,
		Account:        AccountReward,
		Delta:          amount,
		Reason:         LedgerReasonReferralTransfer,
		ReferenceType:  "aff_transfer",
		ReferenceId:    key,
		IdempotencyKey: operationKey,
	}
	var result *LedgerResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "aff_quota").Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		applied, err := CreditQuotaTx(tx, entry)
		if err != nil {
			return err
		}
		result = applied
		if applied.Replayed {
			return nil
		}
		if user.AffQuota < amount {
			return ErrLedgerInsufficientBalance
		}
		update := tx.Model(&User{}).Where("id = ? AND aff_quota >= ?", userId, amount).
			UpdateColumn("aff_quota", gorm.Expr("aff_quota - ?", amount))
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return ErrLedgerInsufficientBalance
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	RefreshUserQuotaCache(userId, result.TotalAfter)
	return result, nil
}
