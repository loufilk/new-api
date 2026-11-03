package model

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// ReferralTransferIdempotencyKeyMaxLength 是划转幂等键的长度上限；模型层与
// HTTP 层共用同一校验，防止绕过控制器直接调用模型时写入异常键。
const ReferralTransferIdempotencyKeyMaxLength = 64

// maxWalletPage 是钱包类分页查询的页码上限。按最大 pageSize=200 计算偏移量
// 上限为 2e8，任何平台都不会发生 int 溢出。返佣记录超过上限明确失败；账本
// 分页把它作为有界回退，避免构造回绕后的负偏移查询。
const maxWalletPage = 1_000_000

const (
	// ReferralReferenceTopUp 是唯一合格返佣来源：成功在线余额充值。
	ReferralReferenceTopUp                  = "topup"
	ReferralCommissionFreezeSeconds   int64 = 5 * 24 * 60 * 60
	ReferralOrdinaryRate                    = 0.03
	ReferralAgentRate                       = 0.05
	ReferralCommissionStatusFrozen          = "frozen"
	ReferralCommissionStatusReleased        = "released"
	ReferralCommissionStatusCancelled       = "cancelled"
)

var (
	ErrReferralTransferInvalid = errors.New("invalid referral transfer request")
)

// ReferralCommission records one online top-up reward and its five-day hold.
type ReferralCommission struct {
	Id            int     `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId        int     `json:"user_id" gorm:"not null;index:idx_referral_commission_user,priority:1"`
	SourceUserId  int     `json:"source_user_id" gorm:"not null;index"`
	ReferenceType string  `json:"reference_type" gorm:"type:varchar(32);not null;uniqueIndex:idx_referral_commission_ref,priority:1"`
	ReferenceId   string  `json:"reference_id" gorm:"type:varchar(128);not null;uniqueIndex:idx_referral_commission_ref,priority:2"`
	PaidQuota     int     `json:"paid_quota" gorm:"type:bigint;not null"`
	RewardQuota   int     `json:"reward_quota" gorm:"type:bigint;not null"`
	RefundedMinor int64   `json:"refunded_minor" gorm:"type:bigint;not null;default:0"`
	ReversedQuota int     `json:"reversed_quota" gorm:"type:bigint;not null;default:0"`
	Rate          float64 `json:"rate" gorm:"type:decimal(10,6);not null"`
	Status        string  `json:"status" gorm:"type:varchar(16);not null;default:'released';index"`
	MaturesAt     int64   `json:"matures_at" gorm:"bigint;not null;default:0;index"`
	CreatedAt     int64   `json:"created_at" gorm:"bigint;autoCreateTime;index:idx_referral_commission_user,priority:2"`
}

type ReferralSummary struct {
	AvailableQuota int64   `json:"available_quota"`
	TotalQuota     int     `json:"total_quota"`
	InviteCount    int     `json:"invite_count"`
	Rate           float64 `json:"rate"`
}

func (ReferralCommission) TableName() string {
	return "referral_commissions"
}

// RecordReferralCommissionForTopUpTx creates one five-day frozen reward for a
// qualifying online top-up. Ordinary inviters receive 3%; active agents receive
// 5%. Rewards are uncapped and remain in aff_quota until transferred.
//
// 只有 reason=LedgerReasonTopup（真实在线支付回调）才产生奖励；管理员补单的
// admin_topup 直接跳过。奖励计算或邀请人余额异常（负值、超过钱包上限）返回错误，
// 由调用方整体回滚订单、充值账本与返佣，绝不静默放弃应发奖励后完成充值。
// 重复结算由订单状态翻转、账本幂等头与唯一幂等身份保证只生成一次。
func RecordReferralCommissionForTopUpTx(tx *gorm.DB, sourceUserId, paidQuota int, referenceType, referenceId, reason string) error {
	if tx == nil {
		return errors.New("referral commission: transaction must not be nil")
	}
	rate := ReferralOrdinaryRate
	if referenceType != ReferralReferenceTopUp || referenceId == "" || paidQuota <= 0 {
		return nil
	}
	// Administrative credits have no online payment entitlement.
	if reason != LedgerReasonTopup {
		return nil
	}

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

	orderQualifies, err := currentTopUpOrderQualifiesTx(tx, sourceUserId, referenceId)
	if err != nil {
		return err
	}
	if !orderQualifies {
		return nil
	}
	var topUp TopUp
	if err := tx.Select("complete_time").Where("trade_no = ? AND user_id = ?", referenceId, sourceUserId).First(&topUp).Error; err != nil {
		return err
	}
	active, err := activeAgentSalesTx(tx, source.InviterId)
	if err != nil {
		return err
	}
	if active {
		rate = ReferralAgentRate
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
	// Invitation balance may be negative after a post-maturity refund. New rewards
	// first recover that balance; only the cumulative amount has an upper bound.
	if inviter.AffQuota < -common.MaxWalletQuota || inviter.AffHistoryQuota < 0 {
		return fmt.Errorf("referral commission: inviter %d has invalid aff balance (quota=%d history=%d)",
			inviter.Id, inviter.AffQuota, inviter.AffHistoryQuota)
	}
	if inviter.AffQuota > common.MaxWalletQuota-rewardQuota || inviter.AffHistoryQuota > common.MaxWalletQuota-rewardQuota {
		return fmt.Errorf("referral commission: inviter %d aff balance would overflow (quota=%d history=%d reward=%d)",
			inviter.Id, inviter.AffQuota, inviter.AffHistoryQuota, rewardQuota)
	}

	paidAt := topUp.CompleteTime
	if paidAt <= 0 {
		paidAt = common.GetTimestamp()
	}
	commission := &ReferralCommission{
		UserId:        inviter.Id,
		SourceUserId:  sourceUserId,
		ReferenceType: referenceType,
		ReferenceId:   referenceId,
		PaidQuota:     paidQuota,
		RewardQuota:   rewardQuota,
		Rate:          rate,
		Status:        ReferralCommissionStatusFrozen,
		MaturesAt:     paidAt + ReferralCommissionFreezeSeconds,
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
	// Conditional updates preserve the permitted refund debt floor and wallet ceiling.
	update := tx.Model(&User{}).
		Where("id = ? AND aff_quota >= ? AND aff_history >= 0 AND aff_quota <= ? AND aff_history <= ?",
			inviter.Id, -common.MaxWalletQuota, common.MaxWalletQuota-rewardQuota, common.MaxWalletQuota-rewardQuota).
		Updates(map[string]any{
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
		Where("top_ups.agent_simple_top_up = ?", false).
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

func GetReferralSummary(userID int) (*ReferralSummary, error) {
	if userID <= 0 {
		return nil, ErrReferralTransferInvalid
	}
	var summary ReferralSummary
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Select("id", "status", "aff_quota", "aff_history", "aff_count").Where("id = ?", userID).First(&user).Error; err != nil {
			return err
		}
		available, err := referralAvailableQuotaTx(tx, userID)
		if err != nil {
			return err
		}
		summary = ReferralSummary{AvailableQuota: available, TotalQuota: user.AffHistoryQuota, InviteCount: user.AffCount, Rate: ReferralOrdinaryRate}
		active, err := activeAgentSalesTx(tx, userID)
		if err != nil {
			return err
		}
		if user.Status == common.UserStatusEnabled && active {
			summary.Rate = ReferralAgentRate
		}
		return nil
	})
	return &summary, err
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

// TransferAffQuota 把已解冻邀请奖励 aff_quota 划转到可消费的 paid 账户，
// 同一事务内写账本并减少 aff_quota；aff_history 为累计值，不因划转减少。
//
// 幂等：IdempotencyKey 由用户提供、后端按用户命名空间化。同键同金额重放返回
// 原始结果（Replayed=true）且不再扣减 aff_quota；同键不同金额返回
// ErrLedgerIdempotencyConflict。余额不足、正数、上限与键格式校验失败不产生任何变动。
func TransferAffQuota(userId, amount int, idempotencyKey string, identities ...AuthSessionIdentity) (*LedgerResult, error) {
	key := strings.TrimSpace(idempotencyKey)
	if userId <= 0 || amount <= 0 || amount > common.MaxWalletQuota || !ValidReferralTransferIdempotencyKey(key) {
		return nil, ErrReferralTransferInvalid
	}
	operationKey := fmt.Sprintf("aff_transfer:%d:%s", userId, key)
	entry := LedgerEntry{
		UserId:         userId,
		Account:        AccountPaid,
		Delta:          amount,
		Reason:         LedgerReasonReferralTransfer,
		ReferenceType:  "aff_transfer",
		ReferenceId:    key,
		IdempotencyKey: operationKey,
	}
	var result *LedgerResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		// HTTP callers bind the transfer to a current browser session. Internal
		// accounting callers may omit this receipt; they never accept client input.
		if len(identities) > 0 {
			if len(identities) != 1 || identities[0].UserID != userId {
				return ErrUserSessionInactive
			}
			if _, token := ParseAccessTokenSessionID(identities[0].SessionID); token {
				return ErrUserSessionInactive
			}
			if err := ValidateAuthSessionWithTx(tx.Session(&gorm.Session{Logger: tx.Logger.LogMode(gormlogger.Silent)}), identities[0]); err != nil {
				return err
			}
		}
		var user User
		if err := lockForUpdate(tx).Select("id", "aff_quota").Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		// Preserve a pre-A07 completed transfer's original account on retry.
		// CreditQuotaTx still validates the complete immutable operation signature.
		var prior BalanceLedger
		priorErr := tx.Where("idempotency_key = ?", operationKey).First(&prior).Error
		if priorErr != nil && !errors.Is(priorErr, gorm.ErrRecordNotFound) {
			return priorErr
		}
		if priorErr == nil && prior.Account == string(AccountReward) {
			entry.Account = AccountReward
		}
		applied, err := CreditQuotaTx(tx, entry)
		if err != nil {
			return err
		}
		result = applied
		if applied.Replayed {
			return nil
		}
		available, err := referralAvailableQuotaTx(tx, userId)
		if err != nil {
			return err
		}
		if available < int64(amount) {
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

func TransferAffQuotaForSession(identity AuthSessionIdentity, amount int, key string) (*LedgerResult, error) {
	return TransferAffQuota(identity.UserID, amount, key, identity)
}

// GetAvailableReferralQuota returns the portion that is outside the five-day
// hold. It deliberately derives from immutable reward rows so the UI cannot
// mistake gross accumulated rewards for a transferable balance.
func GetAvailableReferralQuota(userID int) (int, error) {
	if userID <= 0 {
		return 0, ErrReferralTransferInvalid
	}
	available, err := referralAvailableQuotaTx(DB, userID)
	if err != nil {
		return 0, err
	}
	if available > int64(common.MaxWalletQuota) || available < -int64(common.MaxWalletQuota) {
		return 0, ErrReferralTransferInvalid
	}
	return int(available), nil
}

func referralAvailableQuotaTx(tx *gorm.DB, userID int) (int64, error) {
	var user User
	if err := lockForUpdate(tx).Select("id", "aff_quota").Where("id = ?", userID).First(&user).Error; err != nil {
		return 0, err
	}
	// Locking reads see current committed holds even under MySQL REPEATABLE READ.
	var rewards []ReferralCommission
	if err := lockForUpdate(tx).Select("reward_quota", "reversed_quota").
		Where("user_id = ? AND status = ? AND matures_at > ?", userID, ReferralCommissionStatusFrozen, common.GetTimestamp()).
		Find(&rewards).Error; err != nil {
		return 0, err
	}
	var frozen int64
	for _, reward := range rewards {
		if reward.ReversedQuota < 0 || reward.RewardQuota < reward.ReversedQuota || frozen > int64(common.MaxWalletQuota)-int64(reward.RewardQuota-reward.ReversedQuota) {
			return 0, ErrAgentCommissionInvariant
		}
		frozen += int64(reward.RewardQuota - reward.ReversedQuota)
	}
	return int64(user.AffQuota) - frozen, nil
}

func MatureReferralCommissions(ctx context.Context, limit int) (int, error) {
	limit = min(max(limit, 1), 500)
	var rows []ReferralCommission
	if err := DB.WithContext(ctx).Where("status = ? AND matures_at > 0 AND matures_at <= ?", ReferralCommissionStatusFrozen, common.GetTimestamp()).Order("id").Limit(limit).Find(&rows).Error; err != nil {
		return 0, err
	}
	count := 0
	for i := range rows {
		id := rows[i].Id
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var row ReferralCommission
			if err := lockForUpdate(tx).First(&row, id).Error; err != nil {
				return err
			}
			if row.Status != ReferralCommissionStatusFrozen || row.MaturesAt > common.GetTimestamp() {
				return nil
			}
			return tx.Model(&row).Where("id = ? AND status = ?", row.Id, ReferralCommissionStatusFrozen).Updates(map[string]any{"status": ReferralCommissionStatusReleased}).Error
		})
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

const ReferralCommissionMaxTopUpEvents = 3

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
