package model

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 用户资金账本（TokenMix P0-4）
//
// 用户运行时资金拆分为 paid / bonus / reward 三个账户（quota units，整数）。
// reward 为邀请奖励划转入账后的可消费余额。users.quota 保留为余额合计的
// 兼容镜像。任何余额变更都必须通过本文件的统一事务入口（CreditQuota /
// DebitQuota / OverrideQuota / TransferQuota），在同一事务内维护余额列与镜像
// 列，并写入 balance_ledger 明细；任一步失败则整体回滚。数据库唯一约束保证
// 幂等键不会重复入账。
// ---------------------------------------------------------------------------

// LedgerAccount 用户资金账户标识。
type LedgerAccount string

const (
	AccountPaid   LedgerAccount = "paid"   // 充值/付费账户
	AccountBonus  LedgerAccount = "bonus"  // 注册赠金（仅 bonus_eligible 模型可用）
	AccountReward LedgerAccount = "reward" // 邀请奖励划转入账（全部模型可用）
)

// 账本 reason 常量。写入 balance_ledger.reason，用于审计与对账。
const (
	LedgerReasonTopup                = "topup"                 // 在线充值（Stripe/Creem/Waffo/Waffo Pancake/易支付）
	LedgerReasonAdminTopup           = "admin_topup"           // 管理员补单
	LedgerReasonRedemption           = "redemption"            // 兑换码
	LedgerReasonCheckin              = "checkin"               // 签到
	LedgerReasonAdminAdd             = "admin_add"             // 管理员加额
	LedgerReasonAdminSubtract        = "admin_subtract"        // 管理员减额
	LedgerReasonAdminOverride        = "admin_override"        // 管理员覆盖余额
	LedgerReasonSubscriptionPurchase = "subscription_purchase" // 余额购买订阅
	LedgerReasonBillingPreConsume    = "billing_pre_consume"   // 计费预扣
	LedgerReasonBillingReserve       = "billing_reserve"       // 计费补充预扣
	LedgerReasonBillingSettle        = "billing_settle"        // 计费结算差额
	LedgerReasonBillingRefund        = "billing_refund"        // 计费退款
	LedgerReasonTaskSettle           = "task_settle"           // 异步任务差额结算
	LedgerReasonTaskRefund           = "task_refund"           // 异步任务失败退款
	LedgerReasonNewUserGift          = "new_user_gift"         // 新用户注册赠额（旧 reason，兼容历史数据）
	LedgerReasonSignupBonus          = "signup_bonus"          // 新用户注册赠金（P1-1，进入 bonus 账户）
	LedgerReasonReferralTransfer     = "referral_transfer"     // 邀请奖励从 aff_quota 划转到 reward 账户
	LedgerReasonLegacyBackfill       = "legacy_backfill"       // 旧 quota 向 paid 账户迁移（不改变总余额）
	LedgerReasonRc24Reconcile        = "rc24_reconcile"        // 纯 rc.24 期间用量补记到账本
	LedgerReasonSystem               = "system"                // 系统内部调整
)

var (
	ErrLedgerInvalidEntry        = errors.New("invalid ledger entry")
	ErrLedgerInsufficientBalance = errors.New("insufficient balance")
	ErrLedgerIdempotencyConflict = errors.New("ledger idempotency key conflict: same key with different parameters")
	ErrMissingWalletDebitReceipt = errors.New("wallet funding: consumed quota has no valid debit receipt")
	// ErrLedgerDuplicate 仅表示并发插入幂等键撞库，由入口函数在事务回滚后转为重放/冲突判定。
	ErrLedgerDuplicate = errors.New("ledger duplicate idempotency key")
)

// ledgerAccountColumn 账户标识到 users 表余额列的映射。
var ledgerAccountColumn = map[LedgerAccount]string{
	AccountPaid:   "quota_paid",
	AccountBonus:  "quota_bonus",
	AccountReward: "quota_reward",
}

// BalanceLedgerOperation 记录一次资金操作的全局幂等头。
// 多账户操作会产生多条 BalanceLedger，但只会有一条 operation；唯一键在
// operation 表上，避免同一幂等键并发用于不同账户时绕过复合唯一约束。
type BalanceLedgerOperation struct {
	Id                 int    `json:"id" gorm:"primaryKey;autoIncrement"`
	IdempotencyKey     string `json:"idempotency_key" gorm:"type:varchar(255);not null;uniqueIndex"`
	UserId             int    `json:"user_id" gorm:"not null;index"`
	Operation          string `json:"operation" gorm:"type:varchar(16);not null"`
	OperationSignature string `json:"operation_signature" gorm:"type:char(64);not null"`
	TotalBefore        int    `json:"total_before" gorm:"type:bigint;not null"`
	TotalAfter         int    `json:"total_after" gorm:"type:bigint;not null"`
	CreatedAt          int64  `json:"created_at" gorm:"bigint;autoCreateTime;index"`
}

func (BalanceLedgerOperation) TableName() string {
	return "balance_ledger_operations"
}

// BalanceLedger 用户资金账本明细（balance_ledger 表）。
type BalanceLedger struct {
	Id             int    `json:"id" gorm:"primaryKey;autoIncrement"`
	OperationId    int    `json:"operation_id" gorm:"not null;index"`
	UserId         int    `json:"user_id" gorm:"not null;index:idx_ledger_user_time,priority:1"`
	Account        string `json:"account" gorm:"type:varchar(16);not null"`
	Delta          int    `json:"delta" gorm:"type:bigint;not null"`
	BalanceBefore  int    `json:"balance_before" gorm:"type:bigint;not null"`
	BalanceAfter   int    `json:"balance_after" gorm:"type:bigint;not null"`
	TotalBefore    int    `json:"total_before" gorm:"type:bigint;not null"`
	TotalAfter     int    `json:"total_after" gorm:"type:bigint;not null"`
	Reason         string `json:"reason" gorm:"type:varchar(64);not null"`
	ReferenceType  string `json:"reference_type" gorm:"type:varchar(64);index:idx_ledger_ref,priority:1"`
	ReferenceId    string `json:"reference_id" gorm:"type:varchar(128);index:idx_ledger_ref,priority:2"`
	RequestId      string `json:"request_id" gorm:"type:varchar(64);index"`
	OperatorId     int    `json:"operator_id" gorm:"default:0"`
	Metadata       string `json:"metadata" gorm:"type:text"`
	IdempotencyKey string `json:"idempotency_key" gorm:"type:varchar(255);not null;index"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;autoCreateTime;index:idx_ledger_user_time,priority:2"`
}

func (BalanceLedger) TableName() string {
	return "balance_ledger"
}

// LedgerShare 描述一次多账户入账（退款分账）中单个账户的份额。
type LedgerShare struct {
	Account LedgerAccount `json:"account"`
	Amount  int           `json:"amount"`
}

// AllocateWalletRefund returns an incremental refund from the still-outstanding
// part of a debit receipt. refunded is the cumulative amount already credited to
// each original account, not another desired refund. Callers must persist or
// atomically update that receipt together with the credit; allocation alone does
// not make a refund idempotent. Missing or inconsistent provenance fails closed.
func AllocateWalletRefund(original, refunded []LedgerShare, amount int) ([]LedgerShare, error) {
	if amount < 0 || amount > common.MaxWalletQuota {
		return nil, fmt.Errorf("%w: refund amount out of range", ErrLedgerInvalidEntry)
	}
	remaining := append([]LedgerShare(nil), original...)
	index := make(map[LedgerAccount]int, len(remaining))
	total := 0
	for i, share := range remaining {
		if !validAccount(share.Account) || share.Amount <= 0 || share.Amount > common.MaxWalletQuota-total {
			return nil, fmt.Errorf("%w: invalid original wallet receipt", ErrLedgerInvalidEntry)
		}
		if _, exists := index[share.Account]; exists {
			return nil, fmt.Errorf("%w: duplicate original wallet account", ErrLedgerInvalidEntry)
		}
		index[share.Account] = i
		total += share.Amount
	}
	seen := make(map[LedgerAccount]bool, len(refunded))
	for _, share := range refunded {
		i, exists := index[share.Account]
		if !exists || seen[share.Account] || share.Amount <= 0 || share.Amount > remaining[i].Amount {
			return nil, fmt.Errorf("%w: refund exceeds original wallet account", ErrLedgerInvalidEntry)
		}
		seen[share.Account] = true
		remaining[i].Amount -= share.Amount
		total -= share.Amount
	}
	if amount > total {
		return nil, fmt.Errorf("%w: refund exceeds outstanding wallet receipt", ErrLedgerInvalidEntry)
	}
	if amount == 0 {
		return nil, nil
	}
	allocated := make([]int, len(remaining))
	remainder := amount
	for i, share := range remaining {
		// The wallet bound fits uint64, but amount*share may not. The exact
		// 128-bit product avoids both signed overflow and float rounding.
		hi, lo := bits.Mul64(uint64(share.Amount), uint64(amount))
		part, _ := bits.Div64(hi, lo, uint64(total))
		allocated[i] = int(part) // part <= share.Amount <= MaxWalletQuota.
		remainder -= allocated[i]
	}
	// Sum of floors leaves fewer units than accounts. Give at most one to
	// each account in receipt order, never above that account's capacity.
	for i, share := range remaining {
		if remainder > 0 && allocated[i] < share.Amount {
			allocated[i]++
			remainder--
		}
	}
	out := make([]LedgerShare, 0, len(remaining))
	for i, share := range remaining {
		if allocated[i] > 0 {
			out = append(out, LedgerShare{Account: share.Account, Amount: allocated[i]})
		}
	}
	return out, nil
}

// WalletRequestReceiptTx reads the outstanding principal of a relay request
// under its user's accounting lock. Only canonical, internally consistent
// billing operations are accepted. Independent fees/realtime segments require
// their existing explicit evidence; an unrecognized historical row is never
// guessed to be refundable paid balance.
func WalletRequestReceiptTx(tx *gorm.DB, userID int, requestID string) ([]LedgerShare, error) {
	shares, _, err := walletRequestReceiptTx(tx, userID, requestID)
	return shares, err
}

func walletRequestReceiptTx(tx *gorm.DB, userID int, requestID string) ([]LedgerShare, *User, error) {
	if tx == nil || userID <= 0 || strings.TrimSpace(requestID) == "" {
		return nil, nil, ErrMissingWalletDebitReceipt
	}
	var user User
	if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
		return nil, nil, err
	}
	var rows []BalanceLedger
	if err := lockForUpdate(tx).Where("user_id = ? AND reference_type = ? AND reference_id = ?", userID, "relay", requestID).Order("id").Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	checked := make(map[string]bool)
	remaining := make(map[LedgerAccount]int)
	var order []LedgerAccount
	totalRemaining := 0
	foundDebit := false
	identity := &BillingFinalization{UserID: userID, RequestID: requestID}
	for _, row := range rows {
		if checked[row.IdempotencyKey] {
			continue
		}
		checked[row.IdempotencyKey] = true
		if independent, err := verifiedIndependentBillingDebitTx(tx, identity, row); err != nil {
			return nil, nil, err
		} else if independent {
			continue
		}
		op, details, err := loadLedgerOperation(lockForUpdate(tx), row.IdempotencyKey)
		if err != nil {
			return nil, nil, err
		}
		debit := row.Delta < 0
		operation := string(ledgerOpCredit)
		if debit {
			operation = string(ledgerOpDebit)
		}
		if op.UserId != userID || op.Id != row.OperationId || op.Operation != operation || len(details) == 0 {
			return nil, nil, fmt.Errorf("%w: wallet operation identity mismatch", ErrMissingWalletDebitReceipt)
		}
		total := 0
		seen := make(map[LedgerAccount]bool)
		for _, detail := range details {
			account := LedgerAccount(detail.Account)
			if detail.UserId != userID || detail.RequestId != requestID || detail.ReferenceType != "relay" || detail.ReferenceId != requestID || detail.OperationId != op.Id || detail.IdempotencyKey != row.IdempotencyKey || detail.Reason != row.Reason || detail.Metadata != "" || !validAccount(account) || seen[account] || detail.Delta == 0 || (detail.Delta < 0) != debit || detail.Delta < -common.MaxWalletQuota || detail.Delta > common.MaxWalletQuota || detail.TotalBefore != op.TotalBefore || detail.TotalAfter != op.TotalAfter || detail.BalanceBefore < 0 || detail.BalanceBefore > common.MaxWalletQuota || detail.BalanceAfter < 0 || detail.BalanceAfter > common.MaxWalletQuota || detail.BalanceAfter-detail.BalanceBefore != detail.Delta {
				return nil, nil, fmt.Errorf("%w: wallet operation rows mismatch", ErrMissingWalletDebitReceipt)
			}
			seen[account] = true
			amount := detail.Delta
			if debit {
				amount = -amount
			}
			if amount > common.MaxWalletQuota-total {
				return nil, nil, fmt.Errorf("%w: wallet operation amount out of range", ErrMissingWalletDebitReceipt)
			}
			total += amount
		}
		if op.TotalBefore < 0 || op.TotalBefore > common.MaxWalletQuota || op.TotalAfter < 0 || op.TotalAfter > common.MaxWalletQuota || debit && op.TotalBefore-op.TotalAfter != total || !debit && op.TotalAfter-op.TotalBefore != total {
			return nil, nil, fmt.Errorf("%w: wallet operation totals mismatch", ErrMissingWalletDebitReceipt)
		}
		expectedKey := ""
		switch row.Reason {
		case LedgerReasonBillingPreConsume:
			if debit {
				expectedKey = "billing:preconsume:" + requestID
			}
		case LedgerReasonBillingReserve:
			prefix := "billing:reserve:" + requestID + ":"
			if debit && strings.HasPrefix(row.IdempotencyKey, prefix) {
				target, err := strconv.ParseInt(strings.TrimPrefix(row.IdempotencyKey, prefix), 10, 64)
				if err == nil && target > 0 && target <= common.MaxWalletQuota && int64(total) <= target {
					expectedKey = fmt.Sprintf("%s%d", prefix, target)
				}
			}
		case LedgerReasonBillingSettle:
			if debit {
				expectedKey = fmt.Sprintf("billing:settle:%s:%d", requestID, total)
			} else {
				expectedKey = fmt.Sprintf("billing:settle_refund:%s:%d", requestID, total)
			}
		case LedgerReasonBillingRefund:
			if !debit {
				expectedKey = "billing:refund:" + requestID
			}
		}
		if expectedKey == "" || expectedKey != row.IdempotencyKey {
			return nil, nil, fmt.Errorf("%w: noncanonical wallet operation", ErrMissingWalletDebitReceipt)
		}
		for _, detail := range details {
			account := LedgerAccount(detail.Account)
			if debit {
				if -detail.Delta > common.MaxWalletQuota-totalRemaining {
					return nil, nil, fmt.Errorf("%w: wallet receipt amount out of range", ErrMissingWalletDebitReceipt)
				}
				if _, exists := remaining[account]; !exists {
					order = append(order, account)
				}
				remaining[account] -= detail.Delta
				totalRemaining -= detail.Delta
				foundDebit = true
			} else {
				if detail.Delta > remaining[account] {
					return nil, nil, fmt.Errorf("%w: historical wallet refund exceeds source", ErrMissingWalletDebitReceipt)
				}
				remaining[account] -= detail.Delta
				totalRemaining -= detail.Delta
			}
		}
	}
	if !foundDebit {
		return nil, nil, ErrMissingWalletDebitReceipt
	}
	out := make([]LedgerShare, 0, len(order))
	for _, account := range order {
		if remaining[account] > 0 {
			out = append(out, LedgerShare{Account: account, Amount: remaining[account]})
		}
	}
	return out, &user, nil
}

// CreditWalletRefundTx atomically allocates a refund from authoritative request
// history and returns its post-credit outstanding receipt. A zero amount means
// "all remaining" only for the canonical failure-refund operation. Replays use
// the stored credit shares rather than reapportioning an already reduced source.
func CreditWalletRefundTx(tx *gorm.DB, entry LedgerEntry, amount int) (*LedgerResult, []LedgerShare, error) {
	if entry.ReferenceType != "relay" || entry.ReferenceId != entry.RequestId || amount < 0 || amount > common.MaxWalletQuota || entry.Metadata != "" {
		return nil, nil, ErrLedgerInvalidEntry
	}
	expectedKey := "billing:refund:" + entry.RequestId
	if entry.Reason == LedgerReasonBillingSettle && amount > 0 {
		expectedKey = fmt.Sprintf("billing:settle_refund:%s:%d", entry.RequestId, amount)
	} else if entry.Reason != LedgerReasonBillingRefund {
		return nil, nil, ErrLedgerInvalidEntry
	}
	if entry.IdempotencyKey != expectedKey {
		return nil, nil, ErrLedgerInvalidEntry
	}
	outstanding, user, err := walletRequestReceiptTx(tx, entry.UserId, entry.RequestId)
	if err != nil {
		return nil, nil, err
	}
	operation, rows, err := loadLedgerOperation(lockForUpdate(tx), entry.IdempotencyKey)
	if err == nil {
		shares := make([]LedgerShare, 0, len(rows))
		total := 0
		for _, row := range rows {
			if row.Delta <= 0 || row.Delta > common.MaxWalletQuota-total {
				return nil, nil, ErrLedgerIdempotencyConflict
			}
			total += row.Delta
			shares = append(shares, LedgerShare{Account: LedgerAccount(row.Account), Amount: row.Delta})
		}
		entry.Delta, entry.CreditShares = total, shares
		if len(shares) == 0 || amount > 0 && amount != total || !verifyReplay(operation, entry, ledgerOpCredit) {
			return nil, nil, ErrLedgerIdempotencyConflict
		}
		return replayResult(operation, rows), outstanding, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	total := sharesTotal(outstanding) // receipt validation already bounded the sum.
	if amount == 0 {
		amount = total
	}
	if amount == 0 {
		return &LedgerResult{Replayed: true, TotalBefore: user.Quota, TotalAfter: user.Quota}, outstanding, nil
	}
	shares, err := AllocateWalletRefund(outstanding, nil, amount)
	if err != nil {
		return nil, nil, err
	}
	result, err := CreditQuotaSharesTx(tx, entry, shares)
	if err != nil {
		return nil, nil, err
	}
	remaining, err := AllocateWalletRefund(outstanding, shares, total-amount)
	return result, remaining, err
}

// LedgerEntry 描述一次账户资金变动。IdempotencyKey 为空时：
//   - ReferenceType + ReferenceId 均非空：派生稳定幂等键 `{op}:{refType}:{refId}`；
//   - 否则生成带调用方语义的随机唯一操作键（无法跨进程重放幂等，见 P0-4 汇总）。
//
// 对 OverrideQuota，Delta 表示目标绝对余额（覆盖值）而非变动量。
type LedgerEntry struct {
	UserId         int
	Account        LedgerAccount
	Delta          int
	Reason         string
	ReferenceType  string
	ReferenceId    string
	RequestId      string
	OperatorId     int
	Metadata       string // 任意 JSON 字符串
	IdempotencyKey string
	DebitOrder     []LedgerAccount // 仅内部使用：参与 debit 幂等签名
	TargetAccount  LedgerAccount   // 仅内部使用：参与 transfer 幂等签名
	CreditShares   []LedgerShare   // 仅内部使用：参与多账户 credit 幂等签名
}

// SetMetadata 便捷方法：将 map 序列化为 metadata JSON 字符串。
func (e *LedgerEntry) SetMetadata(metadata map[string]interface{}) {
	if len(metadata) == 0 {
		e.Metadata = ""
		return
	}
	bytes, err := common.Marshal(metadata)
	if err == nil {
		e.Metadata = string(bytes)
	}
}

// LedgerResult 账本操作结果；Replayed=true 表示命中了幂等重放（未重复入账）。
type LedgerResult struct {
	Replayed    bool
	Rows        []BalanceLedger
	TotalBefore int
	TotalAfter  int
}

// accountDelta 单个账户的单次变动。
type accountDelta struct {
	account LedgerAccount
	delta   int
}

func (u *User) AccountBalance(account LedgerAccount) int {
	switch account {
	case AccountPaid:
		return u.QuotaPaid
	case AccountBonus:
		return u.QuotaBonus
	case AccountReward:
		return u.QuotaReward
	}
	return 0
}

func validAccount(account LedgerAccount) bool {
	_, ok := ledgerAccountColumn[account]
	return ok
}

// ledgerOpKind 区分幂等键派生前缀与重放校验规则的账本操作类型。
type ledgerOpKind string

const (
	ledgerOpCredit   ledgerOpKind = "credit"
	ledgerOpDebit    ledgerOpKind = "debit"
	ledgerOpOverride ledgerOpKind = "override"
	ledgerOpTransfer ledgerOpKind = "transfer"
)

func (e LedgerEntry) resolveKey(op ledgerOpKind) (string, error) {
	if e.IdempotencyKey != "" {
		return e.IdempotencyKey, nil
	}
	if e.ReferenceType != "" && e.ReferenceId != "" {
		return fmt.Sprintf("%s:%s:%s", op, e.ReferenceType, e.ReferenceId), nil
	}
	return randomLedgerKey(), nil
}

func randomLedgerKey() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("auto:%d", common.GetTimestamp())
	}
	return "auto:" + hex.EncodeToString(buf)
}

// NewAutoLedgerKey 生成带调用方命名空间前缀的随机操作键。
// 适用于缺少稳定业务键的调用点（如管理员手工加额）；此类键不保证跨进程重放幂等。
func NewAutoLedgerKey(namespace string) string {
	if namespace == "" {
		namespace = "op"
	}
	return namespace + ":" + randomLedgerKey()
}

// ---------------------------------------------------------------------------
// 统一事务入口
// ---------------------------------------------------------------------------

// CreditQuota 单账户入账（正数），自建事务；成功后刷新 Redis 额度缓存。
func CreditQuota(entry LedgerEntry) (*LedgerResult, error) {
	if err := validateCreditEntry(entry); err != nil {
		return nil, err
	}
	if entry.Delta == 0 {
		return &LedgerResult{}, nil
	}
	key, err := entry.resolveKey(ledgerOpCredit)
	if err != nil {
		return nil, err
	}
	result, err := runLedgerTx(entry, key, ledgerOpCredit, func(tx *gorm.DB, user *User) ([]accountDelta, error) {
		return []accountDelta{{account: entry.Account, delta: entry.Delta}}, nil
	})
	if err != nil {
		return nil, err
	}
	RefreshUserQuotaCache(entry.UserId, result.TotalAfter)
	return result, nil
}

// CreditQuotaTx 在调用方事务内单账户入账。调用方负责在自身事务提交后刷新缓存。
func CreditQuotaTx(tx *gorm.DB, entry LedgerEntry) (*LedgerResult, error) {
	if err := validateCreditEntry(entry); err != nil {
		return nil, err
	}
	if entry.Delta == 0 {
		return &LedgerResult{}, nil
	}
	key, err := entry.resolveKey(ledgerOpCredit)
	if err != nil {
		return nil, err
	}
	return applyLedgerTx(tx, entry, key, ledgerOpCredit, func(tx *gorm.DB, user *User) ([]accountDelta, error) {
		return []accountDelta{{account: entry.Account, delta: entry.Delta}}, nil
	})
}

// CreditQuotaShares 按份额一次性向多个账户入账（自建事务），总入账额 = 各份额之和。
// 用于结算/退款必须按原始扣款账户精确退回的场景；份额参与幂等签名，
// 同键不同份额的重复请求会被判定为参数冲突。
func CreditQuotaShares(entry LedgerEntry, shares []LedgerShare) (*LedgerResult, error) {
	if err := validateCreditSharesEntry(entry, shares); err != nil {
		return nil, err
	}
	entry.Delta = sharesTotal(shares)
	entry.CreditShares = append([]LedgerShare(nil), shares...)
	if entry.Delta == 0 {
		return &LedgerResult{}, nil
	}
	key, err := entry.resolveKey(ledgerOpCredit)
	if err != nil {
		return nil, err
	}
	result, err := runLedgerTx(entry, key, ledgerOpCredit, func(tx *gorm.DB, user *User) ([]accountDelta, error) {
		return sharesToDeltas(shares)
	})
	if err != nil {
		return nil, err
	}
	RefreshUserQuotaCache(entry.UserId, result.TotalAfter)
	return result, nil
}

// CreditQuotaSharesTx 在调用方事务内按份额多账户入账。调用方负责在自身事务提交后刷新缓存。
func CreditQuotaSharesTx(tx *gorm.DB, entry LedgerEntry, shares []LedgerShare) (*LedgerResult, error) {
	if err := validateCreditSharesEntry(entry, shares); err != nil {
		return nil, err
	}
	entry.Delta = sharesTotal(shares)
	entry.CreditShares = append([]LedgerShare(nil), shares...)
	if entry.Delta == 0 {
		return &LedgerResult{}, nil
	}
	key, err := entry.resolveKey(ledgerOpCredit)
	if err != nil {
		return nil, err
	}
	return applyLedgerTx(tx, entry, key, ledgerOpCredit, func(tx *gorm.DB, user *User) ([]accountDelta, error) {
		return sharesToDeltas(shares)
	})
}

func validateCreditSharesEntry(entry LedgerEntry, shares []LedgerShare) error {
	if entry.UserId <= 0 || entry.Reason == "" {
		return ErrLedgerInvalidEntry
	}
	if len(shares) == 0 {
		return fmt.Errorf("%w: empty credit shares", ErrLedgerInvalidEntry)
	}
	seen := make(map[LedgerAccount]struct{}, len(shares))
	for _, share := range shares {
		if !validAccount(share.Account) {
			return fmt.Errorf("%w: unknown share account %q", ErrLedgerInvalidEntry, share.Account)
		}
		if _, ok := seen[share.Account]; ok {
			return fmt.Errorf("%w: duplicate credit share account %q", ErrLedgerInvalidEntry, share.Account)
		}
		seen[share.Account] = struct{}{}
		if share.Amount <= 0 {
			return fmt.Errorf("%w: non-positive credit share for %q", ErrLedgerInvalidEntry, share.Account)
		}
	}
	return nil
}

func sharesTotal(shares []LedgerShare) int {
	total := 0
	for _, share := range shares {
		total += share.Amount
	}
	return total
}

func sharesToDeltas(shares []LedgerShare) ([]accountDelta, error) {
	deltas := make([]accountDelta, 0, len(shares))
	for _, share := range shares {
		deltas = append(deltas, accountDelta{account: share.Account, delta: share.Amount})
	}
	return deltas, nil
}

// DebitQuota 钱包扣款（正数），自建事务；成功后刷新 Redis 额度缓存。
// order 指定账户扣减顺序（可配置/显式传入），默认仅为 paid；
// 任一账户或合计均不会因扣减而变为负数，余额不足返回 ErrLedgerInsufficientBalance。
// 本阶段不依据 bonus_eligible 做消费顺序判断。
func DebitQuota(entry LedgerEntry, order ...LedgerAccount) (*LedgerResult, error) {
	order = defaultDebitOrder(order)
	entry.DebitOrder = append([]LedgerAccount(nil), order...)
	if err := validateDebitEntry(entry, order); err != nil {
		return nil, err
	}
	if entry.Delta == 0 {
		return &LedgerResult{}, nil
	}
	key, err := entry.resolveKey(ledgerOpDebit)
	if err != nil {
		return nil, err
	}
	result, err := runLedgerTx(entry, key, ledgerOpDebit, func(tx *gorm.DB, user *User) ([]accountDelta, error) {
		return planDebit(user, entry.Delta, order)
	})
	if err != nil {
		return nil, err
	}
	RefreshUserQuotaCache(entry.UserId, result.TotalAfter)
	return result, nil
}

// DebitQuotaTx 在调用方事务内钱包扣款。调用方负责在自身事务提交后刷新缓存。
func DebitQuotaTx(tx *gorm.DB, entry LedgerEntry, order ...LedgerAccount) (*LedgerResult, error) {
	order = defaultDebitOrder(order)
	entry.DebitOrder = append([]LedgerAccount(nil), order...)
	if err := validateDebitEntry(entry, order); err != nil {
		return nil, err
	}
	if entry.Delta == 0 {
		return &LedgerResult{}, nil
	}
	key, err := entry.resolveKey(ledgerOpDebit)
	if err != nil {
		return nil, err
	}
	return applyLedgerTx(tx, entry, key, ledgerOpDebit, func(tx *gorm.DB, user *User) ([]accountDelta, error) {
		return planDebit(user, entry.Delta, order)
	})
}

// defaultDebitOrder 未显式指定扣减顺序时，默认仅为 paid（兼容既有余额语义）。
func defaultDebitOrder(order []LedgerAccount) []LedgerAccount {
	if len(order) == 0 {
		return []LedgerAccount{AccountPaid}
	}
	return order
}

// OverrideQuota 将单账户余额覆盖为 entry.Delta（目标绝对余额），自建事务。
// 仅调整该账户与镜像 quota，其他账户不受影响。
func OverrideQuota(entry LedgerEntry) (*LedgerResult, error) {
	if err := validateOverrideEntry(entry); err != nil {
		return nil, err
	}
	key, err := entry.resolveKey(ledgerOpOverride)
	if err != nil {
		return nil, err
	}
	result, err := runLedgerTx(entry, key, ledgerOpOverride, func(tx *gorm.DB, user *User) ([]accountDelta, error) {
		delta := entry.Delta - user.AccountBalance(entry.Account)
		if delta == 0 {
			return nil, nil
		}
		return []accountDelta{{account: entry.Account, delta: delta}}, nil
	})
	if err != nil {
		return nil, err
	}
	RefreshUserQuotaCache(entry.UserId, result.TotalAfter)
	return result, nil
}

// OverrideQuotaTx 在调用方事务内覆盖单账户余额。调用方负责在自身事务提交后刷新缓存。
func OverrideQuotaTx(tx *gorm.DB, entry LedgerEntry) (*LedgerResult, error) {
	if err := validateOverrideEntry(entry); err != nil {
		return nil, err
	}
	key, err := entry.resolveKey(ledgerOpOverride)
	if err != nil {
		return nil, err
	}
	return applyLedgerTx(tx, entry, key, ledgerOpOverride, func(tx *gorm.DB, user *User) ([]accountDelta, error) {
		delta := entry.Delta - user.AccountBalance(entry.Account)
		if delta == 0 {
			return nil, nil
		}
		return []accountDelta{{account: entry.Account, delta: delta}}, nil
	})
}

// TransferQuota 账户间转账：entry.Account 转出 entry.Delta 到 target 账户，自建事务。
func TransferQuota(entry LedgerEntry, target LedgerAccount) (*LedgerResult, error) {
	entry.TargetAccount = target
	if err := validateTransferEntry(entry, target); err != nil {
		return nil, err
	}
	if entry.Delta == 0 {
		return &LedgerResult{}, nil
	}
	key, err := entry.resolveKey(ledgerOpTransfer)
	if err != nil {
		return nil, err
	}
	result, err := runLedgerTx(entry, key, ledgerOpTransfer, func(tx *gorm.DB, user *User) ([]accountDelta, error) {
		return planTransfer(user, entry.Account, target, entry.Delta)
	})
	if err != nil {
		return nil, err
	}
	RefreshUserQuotaCache(entry.UserId, result.TotalAfter)
	return result, nil
}

// TransferQuotaTx 在调用方事务内账户间转账。调用方负责在自身事务提交后刷新缓存。
func TransferQuotaTx(tx *gorm.DB, entry LedgerEntry, target LedgerAccount) (*LedgerResult, error) {
	entry.TargetAccount = target
	if err := validateTransferEntry(entry, target); err != nil {
		return nil, err
	}
	if entry.Delta == 0 {
		return &LedgerResult{}, nil
	}
	key, err := entry.resolveKey(ledgerOpTransfer)
	if err != nil {
		return nil, err
	}
	return applyLedgerTx(tx, entry, key, ledgerOpTransfer, func(tx *gorm.DB, user *User) ([]accountDelta, error) {
		return planTransfer(user, entry.Account, target, entry.Delta)
	})
}

// ---------------------------------------------------------------------------
// 内部实现
// ---------------------------------------------------------------------------

func validateCreditEntry(entry LedgerEntry) error {
	if entry.UserId <= 0 || !validAccount(entry.Account) || entry.Reason == "" {
		return ErrLedgerInvalidEntry
	}
	if entry.Delta < 0 {
		return fmt.Errorf("%w: negative credit delta", ErrLedgerInvalidEntry)
	}
	return nil
}

func validateDebitEntry(entry LedgerEntry, order []LedgerAccount) error {
	if entry.UserId <= 0 || entry.Reason == "" {
		return ErrLedgerInvalidEntry
	}
	if entry.Delta < 0 {
		return fmt.Errorf("%w: negative debit delta", ErrLedgerInvalidEntry)
	}
	seen := make(map[LedgerAccount]struct{}, len(order))
	for _, acc := range order {
		if !validAccount(acc) {
			return fmt.Errorf("%w: unknown account %q", ErrLedgerInvalidEntry, acc)
		}
		if _, ok := seen[acc]; ok {
			return fmt.Errorf("%w: duplicate debit account %q", ErrLedgerInvalidEntry, acc)
		}
		seen[acc] = struct{}{}
	}
	return nil
}

func validateOverrideEntry(entry LedgerEntry) error {
	if entry.UserId <= 0 || !validAccount(entry.Account) || entry.Reason == "" {
		return ErrLedgerInvalidEntry
	}
	if entry.Delta < 0 {
		return fmt.Errorf("%w: negative override balance", ErrLedgerInvalidEntry)
	}
	return nil
}

func validateTransferEntry(entry LedgerEntry, target LedgerAccount) error {
	if entry.UserId <= 0 || !validAccount(entry.Account) || !validAccount(target) || entry.Reason == "" {
		return ErrLedgerInvalidEntry
	}
	if entry.Account == target {
		return fmt.Errorf("%w: transfer between the same account", ErrLedgerInvalidEntry)
	}
	if entry.Delta < 0 {
		return fmt.Errorf("%w: negative transfer amount", ErrLedgerInvalidEntry)
	}
	return nil
}

// planDebit 按 order 顺序从各账户扣减，任一账户或合计不扣为负。
func planDebit(user *User, amount int, order []LedgerAccount) ([]accountDelta, error) {
	if amount <= 0 {
		return nil, nil
	}
	remaining := amount
	var deltas []accountDelta
	for _, acc := range order {
		if remaining == 0 {
			break
		}
		balance := user.AccountBalance(acc)
		if balance <= 0 {
			continue
		}
		take := balance
		if take > remaining {
			take = remaining
		}
		deltas = append(deltas, accountDelta{account: acc, delta: -take})
		remaining -= take
	}
	if remaining > 0 {
		return nil, ErrLedgerInsufficientBalance
	}
	return deltas, nil
}

func planTransfer(user *User, from, to LedgerAccount, amount int) ([]accountDelta, error) {
	if amount <= 0 {
		return nil, nil
	}
	if user.AccountBalance(from) < amount {
		return nil, ErrLedgerInsufficientBalance
	}
	return []accountDelta{
		{account: from, delta: -amount},
		{account: to, delta: amount},
	}, nil
}

// runLedgerTx 启动独立事务执行账本操作；插入撞幂等键时回滚并转为重放/冲突判定。
func runLedgerTx(entry LedgerEntry, key string, op ledgerOpKind, plan ledgerPlanFunc) (*LedgerResult, error) {
	var result *LedgerResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = applyLedgerTx(tx, entry, key, op, plan)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrLedgerDuplicate) {
			return replayOrConflict(entry, key, op)
		}
		return nil, err
	}
	return result, nil
}

type ledgerPlanFunc func(tx *gorm.DB, user *User) ([]accountDelta, error)

func ledgerOperationSignature(entry LedgerEntry, op ledgerOpKind) string {
	order := make([]string, 0, len(entry.DebitOrder))
	for _, account := range entry.DebitOrder {
		order = append(order, string(account))
	}
	shares := make([]string, 0, len(entry.CreditShares))
	for _, share := range entry.CreditShares {
		shares = append(shares, fmt.Sprintf("%s:%d", share.Account, share.Amount))
	}
	payload := fmt.Sprintf(
		"op=%s|user=%d|account=%s|target=%s|delta=%d|order=%s|shares=%s|reason=%s|ref_type=%s|ref_id=%s|operator=%d",
		op,
		entry.UserId,
		entry.Account,
		entry.TargetAccount,
		entry.Delta,
		strings.Join(order, ","),
		strings.Join(shares, ","),
		entry.Reason,
		entry.ReferenceType,
		entry.ReferenceId,
		entry.OperatorId,
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func loadLedgerOperation(tx *gorm.DB, key string) (*BalanceLedgerOperation, []BalanceLedger, error) {
	var operation BalanceLedgerOperation
	// The caller may pass a locking query. Clone its statement for each table
	// while keeping the same transaction and locking clause.
	result := tx.Session(&gorm.Session{}).Where("idempotency_key = ?", key).Limit(1).Find(&operation)
	if result.Error != nil {
		return nil, nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil, gorm.ErrRecordNotFound
	}
	var rows []BalanceLedger
	if err := tx.Session(&gorm.Session{}).Where("operation_id = ?", operation.Id).Order("id asc").Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	return &operation, rows, nil
}

func replayResult(operation *BalanceLedgerOperation, rows []BalanceLedger) *LedgerResult {
	return &LedgerResult{
		Replayed:    true,
		Rows:        rows,
		TotalBefore: operation.TotalBefore,
		TotalAfter:  operation.TotalAfter,
	}
}

func verifyReplay(operation *BalanceLedgerOperation, entry LedgerEntry, op ledgerOpKind) bool {
	return operation != nil &&
		operation.UserId == entry.UserId &&
		operation.Operation == string(op) &&
		operation.OperationSignature == ledgerOperationSignature(entry, op)
}

// normalizeLegacyQuotaTx 兼容尚未执行启动迁移的夹具/滚动节点：若只有旧 quota
// 有值，则在当前事务内完成 paid 回填并留下迁移账本，不改变总余额。
func normalizeLegacyQuotaTx(tx *gorm.DB, user *User) error {
	if user.Quota == 0 || user.QuotaPaid != 0 || user.QuotaBonus != 0 || user.QuotaReward != 0 {
		return nil
	}
	// Validate before creating even an idempotency header. A malformed legacy
	// balance must remain untouched for explicit review, never become usable
	// paid credit merely because a migration or the first debit touched it.
	if user.Quota < 0 {
		return fmt.Errorf("%w: negative legacy quota for user %d", ErrLedgerInvalidEntry, user.Id)
	}
	if user.Quota > common.MaxWalletQuota {
		return fmt.Errorf("%w: legacy quota for user %d exceeds max %d", ErrWalletQuotaLimitExceeded, user.Id, common.MaxWalletQuota)
	}
	key := fmt.Sprintf("migration:legacy_quota:%d", user.Id)
	entry := LedgerEntry{
		UserId:         user.Id,
		Account:        AccountPaid,
		Delta:          user.Quota,
		Reason:         LedgerReasonLegacyBackfill,
		ReferenceType:  "user",
		ReferenceId:    fmt.Sprintf("%d", user.Id),
		IdempotencyKey: key,
	}
	signature := ledgerOperationSignature(entry, ledgerOpCredit)
	operation := BalanceLedgerOperation{
		IdempotencyKey:     key,
		UserId:             user.Id,
		Operation:          string(ledgerOpCredit),
		OperationSignature: signature,
		TotalBefore:        user.Quota,
		TotalAfter:         user.Quota,
		CreatedAt:          common.GetTimestamp(),
	}
	if err := tx.Create(&operation).Error; err != nil {
		if isDuplicateKeyError(err) {
			return ErrLedgerDuplicate
		}
		return err
	}
	if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("quota_paid", user.Quota).Error; err != nil {
		return err
	}
	row := BalanceLedger{
		OperationId:    operation.Id,
		UserId:         user.Id,
		Account:        string(AccountPaid),
		Delta:          user.Quota,
		BalanceBefore:  0,
		BalanceAfter:   user.Quota,
		TotalBefore:    user.Quota,
		TotalAfter:     user.Quota,
		Reason:         entry.Reason,
		ReferenceType:  entry.ReferenceType,
		ReferenceId:    entry.ReferenceId,
		IdempotencyKey: key,
		CreatedAt:      operation.CreatedAt,
	}
	if err := tx.Create(&row).Error; err != nil {
		return err
	}
	user.QuotaPaid = user.Quota
	return nil
}

// applyLedgerTx 在事务内执行：行锁 -> 幂等重放判定 -> 余额更新 -> 账本插入。
// 余额更新与账本插入任一步失败都会导致调用方整体回滚。
func applyLedgerTx(tx *gorm.DB, entry LedgerEntry, key string, op ledgerOpKind, plan ledgerPlanFunc) (*LedgerResult, error) {
	user := User{}
	if err := lockForUpdate(tx).Where("id = ?", entry.UserId).First(&user).Error; err != nil {
		return nil, err
	}

	// 加锁后读取全局幂等头。已存在时只接受完全相同的操作签名。
	existingOperation, existingRows, err := loadLedgerOperation(lockForUpdate(tx), key)
	if err == nil {
		if verifyReplay(existingOperation, entry, op) {
			return replayResult(existingOperation, existingRows), nil
		}
		return nil, fmt.Errorf("%w: key=%s user=%d", ErrLedgerIdempotencyConflict, key, entry.UserId)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	if err := normalizeLegacyQuotaTx(tx, &user); err != nil {
		if errors.Is(err, ErrLedgerDuplicate) {
			return nil, err
		}
		return nil, err
	}

	deltas, err := plan(tx, &user)
	if err != nil {
		return nil, err
	}

	totalBefore := user.QuotaPaid + user.QuotaBonus + user.QuotaReward
	totalAfter := totalBefore
	updates := map[string]interface{}{}
	balanceBefore := map[LedgerAccount]int{}
	for _, d := range deltas {
		balanceBefore[d.account] = user.AccountBalance(d.account)
		updates[ledgerAccountColumn[d.account]] = balanceBefore[d.account] + d.delta
		totalAfter += d.delta
	}
	// rc.26 对齐 wallet 上限：任何入账后用户三账户总额不得超过 JS 安全整数域。
	// 负向（扣款/结算退款为负 delta 场景由 debit 路径处理）不受此约束。
	if op == ledgerOpCredit && totalAfter > common.MaxWalletQuota {
		return nil, fmt.Errorf("%w: max %d", ErrWalletQuotaLimitExceeded, common.MaxWalletQuota)
	}
	createdAt := common.GetTimestamp()
	operation := BalanceLedgerOperation{
		IdempotencyKey:     key,
		UserId:             entry.UserId,
		Operation:          string(op),
		OperationSignature: ledgerOperationSignature(entry, op),
		TotalBefore:        totalBefore,
		TotalAfter:         totalAfter,
		CreatedAt:          createdAt,
	}
	if err := tx.Create(&operation).Error; err != nil {
		if isDuplicateKeyError(err) {
			return nil, ErrLedgerDuplicate
		}
		return nil, err
	}
	if len(deltas) == 0 {
		// 无实际变动也写入幂等头，确保相同键后续不能被不同参数复用。
		return &LedgerResult{TotalBefore: totalBefore, TotalAfter: totalAfter}, nil
	}

	updates["quota"] = totalAfter
	if err := tx.Model(&User{}).Where("id = ?", entry.UserId).Updates(updates).Error; err != nil {
		return nil, err
	}

	rows := make([]BalanceLedger, 0, len(deltas))
	for _, d := range deltas {
		row := BalanceLedger{
			OperationId:    operation.Id,
			UserId:         entry.UserId,
			Account:        string(d.account),
			Delta:          d.delta,
			BalanceBefore:  balanceBefore[d.account],
			BalanceAfter:   balanceBefore[d.account] + d.delta,
			TotalBefore:    totalBefore,
			TotalAfter:     totalAfter,
			Reason:         entry.Reason,
			ReferenceType:  entry.ReferenceType,
			ReferenceId:    entry.ReferenceId,
			RequestId:      entry.RequestId,
			OperatorId:     entry.OperatorId,
			Metadata:       entry.Metadata,
			IdempotencyKey: key,
			CreatedAt:      createdAt,
		}
		if err := tx.Create(&row).Error; err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return &LedgerResult{Rows: rows, TotalBefore: totalBefore, TotalAfter: totalAfter}, nil
}

// replayOrConflict 在事务回滚后（如 SQLite 并发撞唯一键）于事务外重读幂等键，
// 判定是重放还是参数冲突。
func replayOrConflict(entry LedgerEntry, key string, op ledgerOpKind) (*LedgerResult, error) {
	operation, rows, err := loadLedgerOperation(DB, key)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrLedgerDuplicate
		}
		return nil, err
	}
	if verifyReplay(operation, entry, op) {
		return replayResult(operation, rows), nil
	}
	return nil, fmt.Errorf("%w: key=%s user=%d", ErrLedgerIdempotencyConflict, key, entry.UserId)
}

func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "unique index")
}

// RefreshUserQuotaCache 在数据库事务成功提交后失效 Redis 用户缓存。
// 不直接异步写 totalQuota：并发事务的回调执行顺序不等于提交顺序，旧值可能覆盖
// 新值；删除缓存后由下一次读取从数据库回填，可避免余额缓存倒退。
func RefreshUserQuotaCache(userId int, _ int) {
	if !common.RedisEnabled {
		return
	}
	if userId <= 0 {
		return
	}
	if err := invalidateUserCache(userId); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate user quota cache after ledger change: user=%d err=%s", userId, err.Error()))
	}
}

// RefreshUserQuotaCacheWithContext retains invalidation-only semantics while
// letting bounded refund recovery include Redis in its operation deadline.
func RefreshUserQuotaCacheWithContext(ctx context.Context, userId int) {
	if !common.RedisEnabled || userId <= 0 {
		return
	}
	if err := common.RDB.Del(ctx, getUserCacheKey(userId)).Err(); err != nil {
		code, class := BillingRefundErrorCode(err)
		common.SysLog(fmt.Sprintf("failed to invalidate user quota cache after refund: user=%d code=%s class=%s", userId, code, class))
	}
}

// ---------------------------------------------------------------------------
// 幂等回填与对账
// ---------------------------------------------------------------------------

// BackfillLegacyQuotaAccounts 将旧版 users.quota 余额一次性回填到 paid 账户，
// 使用包级 DB。保留给不持有 *gorm.DB 句柄的既有调用方。
func BackfillLegacyQuotaAccounts() (int64, error) {
	return BackfillLegacyQuotaAccountsWithDB(DB)
}

// BackfillLegacyQuotaAccountsWithDB 将旧版 users.quota 余额一次性回填到 paid 账户：
// 仅处理三个账户均为 0 且 quota 非 0 的存量用户，bonus/reward 保持 0。
// 每个用户在同一事务内写 migration operation + ledger 明细 + paid 余额，重复执行
// 无副作用（已回填用户不再满足条件，幂等头也有全局唯一键）。
//
// db 由调用方显式注入，候选查询与每个用户的事务都只走这一个句柄，从不回退到包级
// DB。迁移注册表在 AutoMigrate 之后用同一个句柄驱动本步骤，因此在包级 DB 尚未赋值
// 或与迁移目标库不同的情况下（迁移编排、测试夹具）语义仍然正确。
func BackfillLegacyQuotaAccountsWithDB(db *gorm.DB) (int64, error) {
	if db == nil {
		return 0, errors.New("backfill legacy quota accounts: database must not be nil")
	}
	var candidates []User
	if err := db.Select("id", "quota", "quota_paid", "quota_bonus", "quota_reward").
		Where("quota_paid = 0 AND quota_bonus = 0 AND quota_reward = 0 AND quota <> 0").
		Order("id asc").Find(&candidates).Error; err != nil {
		return 0, err
	}
	var affected int64
	for _, candidate := range candidates {
		migrated := false
		err := db.Transaction(func(tx *gorm.DB) error {
			var user User
			if err := lockForUpdate(tx).Where("id = ?", candidate.Id).First(&user).Error; err != nil {
				return err
			}
			if user.Quota == 0 || user.QuotaPaid != 0 || user.QuotaBonus != 0 || user.QuotaReward != 0 {
				return nil
			}
			if err := normalizeLegacyQuotaTx(tx, &user); err != nil {
				return err
			}
			migrated = true
			return nil
		})
		if err != nil {
			return affected, err
		}
		if migrated {
			affected++
		}
	}
	return affected, nil
}

// FindQuotaAccountMismatches 返回 quota 与三账户合计不一致的用户（对账/诊断用）。
func FindQuotaAccountMismatches() ([]User, error) {
	var users []User
	err := DB.Select("id", "username", "quota", "quota_paid", "quota_bonus", "quota_reward").
		Where("quota <> quota_paid + quota_bonus + quota_reward").
		Find(&users).Error
	if err != nil {
		return nil, err
	}
	return users, nil
}

// ReconcileQuotaAccounts 修复 quota 镜像与三账户合计不一致的行：quota 是
// 派生镜像，向合计对齐（quota = paid + bonus + reward）。返回修复行数。
func ReconcileQuotaAccounts() (int64, error) {
	users, err := FindQuotaAccountMismatches()
	if err != nil {
		return 0, err
	}
	var fixed int64
	for _, user := range users {
		total := user.QuotaPaid + user.QuotaBonus + user.QuotaReward
		if total == user.Quota {
			continue
		}
		res := DB.Model(&User{}).Where("id = ?", user.Id).Update("quota", total)
		if res.Error != nil {
			return fixed, res.Error
		}
		fixed += res.RowsAffected
	}
	return fixed, nil
}

// ---------------------------------------------------------------------------
// 账本查询（P1-2 钱包分账 API 与返利对账）
// ---------------------------------------------------------------------------

// LedgerQueryFilter 用户账本分页查询过滤条件。Account 仅接受已注册账户标识，
// Reason 与时间范围可选；查询强制限定 UserId，防止越权读取他人账本。
type LedgerQueryFilter struct {
	UserId          int
	Account         string
	AllowedAccounts []LedgerAccount
	Reason          string
	StartTime       int64
	EndTime         int64
	Page            int
	PageSize        int
}

// LedgerPage 账本分页结果。
type LedgerPage struct {
	Rows  []BalanceLedger
	Total int64
}

// QueryUserLedger 分页查询指定用户的 balance_ledger 明细，按创建时间倒序。
// 返回的 Rows 中 BalanceBefore/BalanceAfter 为该账户的变动前后余额，
// TotalBefore/TotalAfter 为三账户合计镜像，Delta 为本次变动量。
func QueryUserLedger(filter LedgerQueryFilter) (*LedgerPage, error) {
	if filter.UserId <= 0 {
		return nil, fmt.Errorf("%w: missing user id", ErrLedgerInvalidEntry)
	}
	base := DB.Model(&BalanceLedger{}).Where("user_id = ?", filter.UserId)
	if len(filter.AllowedAccounts) > 0 {
		accounts := make([]string, 0, len(filter.AllowedAccounts))
		for _, account := range filter.AllowedAccounts {
			if !validAccount(account) {
				return nil, fmt.Errorf("%w: unknown allowed account %q", ErrLedgerInvalidEntry, account)
			}
			accounts = append(accounts, string(account))
		}
		base = base.Where("account IN ?", accounts)
	}
	if filter.Account != "" {
		if !validAccount(LedgerAccount(filter.Account)) {
			return nil, fmt.Errorf("%w: unknown account %q", ErrLedgerInvalidEntry, filter.Account)
		}
		base = base.Where("account = ?", filter.Account)
	}
	if filter.Reason != "" {
		base = base.Where("reason = ?", filter.Reason)
	}
	if filter.StartTime > 0 {
		base = base.Where("created_at >= ?", filter.StartTime)
	}
	if filter.EndTime > 0 {
		base = base.Where("created_at <= ?", filter.EndTime)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, err
	}
	page, pageSize := normalizeLedgerPagination(filter.Page, filter.PageSize)
	var rows []BalanceLedger
	if err := base.Order("created_at desc, id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, err
	}
	return &LedgerPage{Rows: rows, Total: total}, nil
}

func normalizeLedgerPagination(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	// 有界回退：超大页码不得让 (page-1)*pageSize 溢出成负偏移。
	if page > maxWalletPage {
		page = maxWalletPage
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize
}

// AccountNetCost 单次请求/任务在各钱包账户的原始账本净变动（带符号：
// 负数=净扣减，正数=净入账）。从 balance_ledger 汇总同一引用
// （reference_type + reference_id）的全部流水，天然包含预扣、追扣、结算与
// 退款，退款后即反映最终净份额。调用方按需要取反得到净消费。
func AccountNetCost(userId int, referenceType, referenceId string) (map[LedgerAccount]int, error) {
	return accountNetCostWithDB(DB, userId, referenceType, referenceId)
}

// AccountNetCostTx is the transactional variant used by wallet cost reporting.
func AccountNetCostTx(tx *gorm.DB, userId int, referenceType, referenceId string) (map[LedgerAccount]int, error) {
	if tx == nil {
		return nil, fmt.Errorf("nil database transaction")
	}
	return accountNetCostWithDB(tx, userId, referenceType, referenceId)
}

func accountNetCostWithDB(db *gorm.DB, userId int, referenceType, referenceId string) (map[LedgerAccount]int, error) {
	result := make(map[LedgerAccount]int)
	if userId <= 0 || referenceType == "" || referenceId == "" {
		return result, nil
	}
	type accountSum struct {
		Account string
		Delta   int64
	}
	var sums []accountSum
	err := db.Model(&BalanceLedger{}).
		Select("account, SUM(delta) AS delta").
		Where("user_id = ? AND reference_type = ? AND reference_id = ?", userId, referenceType, referenceId).
		Group("account").
		Scan(&sums).Error
	if err != nil {
		return nil, err
	}
	for _, sum := range sums {
		account := LedgerAccount(sum.Account)
		if !validAccount(account) {
			continue
		}
		delta := sum.Delta
		if delta > int64(math.MaxInt32) {
			delta = int64(math.MaxInt32)
		}
		if delta < int64(math.MinInt32) {
			delta = int64(math.MinInt32)
		}
		result[account] = int(delta)
	}
	return result, nil
}

// NetCostByReferences 汇总多个引用的账户净消费（同一请求可能跨引用记账，
// 例如异步任务预扣在 relay 引用、结算在 task 引用）。
func NetCostByReferences(userId int, refs [][2]string) (map[LedgerAccount]int, error) {
	return netCostByReferencesWithDB(DB, userId, refs)
}

// NetCostByReferencesTx is the transactional variant used by wallet cost reporting.
func NetCostByReferencesTx(tx *gorm.DB, userId int, refs [][2]string) (map[LedgerAccount]int, error) {
	if tx == nil {
		return nil, fmt.Errorf("nil database transaction")
	}
	return netCostByReferencesWithDB(tx, userId, refs)
}

func netCostByReferencesWithDB(db *gorm.DB, userId int, refs [][2]string) (map[LedgerAccount]int, error) {
	result := make(map[LedgerAccount]int)
	for _, ref := range refs {
		cost, err := accountNetCostWithDB(db, userId, ref[0], ref[1])
		if err != nil {
			return nil, err
		}
		for account, delta := range cost {
			result[account] += delta
		}
	}
	return result, nil
}
