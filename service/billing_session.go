package service

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// BillingSession — 统一计费会话
// ---------------------------------------------------------------------------

// BillingSession 封装单次请求的预扣费/结算/退款生命周期。
// 实现 relaycommon.BillingSettler 接口。
type BillingSession struct {
	relayInfo               *relaycommon.RelayInfo
	funding                 FundingSource
	preConsumedQuota        int  // 实际预扣额度（信任用户可能为 0）
	tokenConsumed           int  // 令牌额度实际扣减量
	extraReserved           int  // 发送前补充预扣的额度（订阅退款时单独回滚）
	trusted                 bool // 是否命中信任额度旁路
	fundingSettled          bool // 资金结算已成功提交（钱包可能经事务能力而非 FundingSource.Settle）
	settled                 bool // Settle 全部完成（资金 + 令牌）
	refunded                bool // durable refund ownership accepted, or completed for a custom source.
	taskOwned               bool
	actualSettled           int
	billingIdentity         *model.BillingFinalization
	terminalReceipt         *model.BillingFinalization
	settlementUncertain     bool
	settlementIntent        *int // Frozen observed charge, even if persisting it fails.
	reservationUncertain    bool
	subscriptionSupplements map[string]bool
	realtime                *realtimeBillingState
	lang                    string // Request language; keep custom funding factories and atomic ledger settlement.
	mu                      sync.Mutex
}

// 编译期能力断言。会话把资金来源的扣减回执转发给异步任务持久化路径，
// 使 WalletSessionDebitShares 无需断言 *BillingSession。
var (
	_ relaycommon.BillingSettler = (*BillingSession)(nil)
	_ moneyport.FundingReceipt   = (*BillingSession)(nil)
)

// transactionalSettlementFunding is an internal capability for funding sources
// whose settlement can participate in BillingSession's database transaction.
// Wallet sources must implement it; otherwise settlement fails closed instead
// of silently falling back to the old funding-then-token sequence.
type transactionalSettlementFunding interface {
	moneyport.FundingSource
	SettleInTx(tx *gorm.DB, delta int) (moneyport.SettlementResult, error)
	ApplySettlementResult(delta int, result moneyport.SettlementResult)
}

// Settle preserves the original actual-minus-preconsume arithmetic. Built-in
// wallets and subscriptions share atomic source/token terminal settlement.
func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reservationUncertain {
		if err := s.resolveReservationLocked(); err != nil {
			return err
		}
	}
	if s.settled {
		if s.actualSettled != actualQuota {
			return errors.New("billing settlement replay amount mismatch")
		}
		return nil
	}
	if s.taskOwned {
		return model.ErrBillingFinalized
	}
	if s.refunded {
		return model.ErrBillingFinalized
	}
	if s.durableTerminalSource() {
		return s.settleDurableLocked(actualQuota)
	}
	if s.funding.Source() == BillingSourceWallet {
		delta := actualQuota - s.preConsumedQuota
		if delta != 0 {
			funding, ok := s.funding.(transactionalSettlementFunding)
			if !ok {
				return fmt.Errorf("wallet funding source %T does not support transactional settlement", s.funding)
			}
			if err := s.settleWalletAndToken(funding, delta); err != nil {
				return err
			}
			s.fundingSettled = true
		}
		s.settled = true
		s.actualSettled = actualQuota
		return nil
	}
	// Official subscriptions and alternate funding retain their independent
	// source/token contract; the normal atomic branch above is wallet-only.
	delta := actualQuota - s.preConsumedQuota
	if delta == 0 {
		s.settled = true
		s.actualSettled = actualQuota
		return nil
	}
	if !s.fundingSettled {
		if err := s.funding.Settle(delta); err != nil {
			return err
		}
		s.fundingSettled = true
	}
	var tokenErr error
	if !s.relayInfo.IsPlayground {
		if delta > 0 {
			tokenErr = model.DecreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, delta)
		} else {
			tokenErr = model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, -delta)
		}
		if tokenErr != nil {
			common.SysLog(fmt.Sprintf("error adjusting token quota after custom funding settled (userId=%d, tokenId=%d, delta=%d): %s", s.relayInfo.UserId, s.relayInfo.TokenId, delta, tokenErr.Error()))
		}
	}
	if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(delta)
	}
	s.settled = true
	s.actualSettled = actualQuota
	return tokenErr
}

func (s *BillingSession) settleWalletAndToken(funding transactionalSettlementFunding, delta int) error {
	var result moneyport.SettlementResult
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		result = moneyport.SettlementResult{}
		err = model.DB.Transaction(func(tx *gorm.DB) error {
			var settleErr error
			result, settleErr = funding.SettleInTx(tx, delta)
			if settleErr != nil {
				return settleErr
			}
			// A replay means the original atomic transaction already applied both
			// wallet and token changes. Do not charge the token twice.
			if result.Replayed || s.relayInfo.IsPlayground {
				return nil
			}
			return model.SettleTokenQuotaDeltaTx(tx, s.relayInfo.TokenId, s.relayInfo.UserId, s.relayInfo.TokenKey, -delta)
		})
		if !errors.Is(err, model.ErrLedgerDuplicate) {
			break
		}
	}
	if err != nil {
		return err
	}
	funding.ApplySettlementResult(delta, result)
	return nil
}

// Refund 退还所有预扣费，幂等安全，异步执行。
func (s *BillingSession) Refund(c *gin.Context) {
	s.mu.Lock()
	if s.reservationUncertain {
		if err := s.resolveReservationLocked(); err != nil {
			common.SysError("billing refund held evidence is unresolved")
			s.mu.Unlock()
			return
		}
	}
	if !s.settled && !s.refunded && !s.taskOwned && s.settlementIntent == nil && s.realtime != nil && s.realtime.observed {
		// Partial delivery is not a failed unused hold. Settle measured aggregate
		// exactly once; failure/unknown ACK preserves the held evidence for review.
		s.realtime.final = true
		if err := s.settleDurableLocked(s.realtime.actual); err != nil {
			common.SysError("partial realtime settlement remains unresolved")
		}
		s.mu.Unlock()
		return
	}
	if s.settled || s.refunded || s.taskOwned || s.settlementIntent != nil {
		s.mu.Unlock()
		return
	}
	if s.durableTerminalSource() {
		// A trusted zero hold still has an owner to close. Transport must mark
		// ambiguous delivery before calling Refund; zero held is not zero usage.
		s.refundDurableLocked()
		s.mu.Unlock()
		return
	}
	if !s.needsRefundLocked() {
		s.mu.Unlock()
		return
	}
	s.refunded = true
	s.mu.Unlock()

	logger.LogInfo(c, common.LogText("user %d request failed, refunding the pre-consumed quota (token_quota=%s, funding=%s)",
		s.relayInfo.UserId,
		logger.FormatQuota(s.tokenConsumed),
		s.funding.Source(),
	))

	// 复制需要的值到闭包中
	tokenId := s.relayInfo.TokenId
	tokenKey := s.relayInfo.TokenKey
	isPlayground := s.relayInfo.IsPlayground
	tokenConsumed := s.tokenConsumed
	extraReserved := s.extraReserved
	subscriptionId := s.relayInfo.SubscriptionId
	funding := s.funding

	gopool.Go(func() {
		// 1) 退还资金来源
		if err := funding.Refund(); err != nil {
			common.SysLog(common.LogText("error refunding billing source: %s", err.Error()))
		}
		if extraReserved > 0 && funding.Source() == BillingSourceSubscription && subscriptionId > 0 {
			if err := model.PostConsumeUserSubscriptionDelta(subscriptionId, -int64(extraReserved)); err != nil {
				common.SysLog(common.LogText("error refunding subscription extra reserved quota: %s", err.Error()))
			}
		}
		// 2) 退还令牌额度
		if tokenConsumed > 0 && !isPlayground {
			if err := model.IncreaseTokenQuotaForRefund(tokenId, tokenKey, tokenConsumed); err != nil {
				common.SysLog(common.LogText("error refunding token quota: %s", err.Error()))
			}
		}
	})
}

// NeedsRefund 返回是否存在需要退还的预扣状态。
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsRefundLocked()
}

func (s *BillingSession) needsRefundLocked() bool {
	if s.settled || s.refunded || s.fundingSettled || s.taskOwned || s.settlementIntent != nil {
		// fundingSettled 时资金来源已提交结算，不能再退预扣费
		return false
	}
	if s.durableTerminalSource() && s.terminalReceipt != nil && s.terminalReceipt.State == model.BillingHeld {
		return true // Includes zero-held requests whose terminal owner is unclosed.
	}
	if s.tokenConsumed > 0 {
		return true
	}
	if s.terminalReceipt != nil && s.terminalReceipt.SourceHeld > 0 {
		return true
	}
	// 订阅可能在 tokenConsumed=0 时仍预扣了额度
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.preConsumed > 0 {
		return true
	}
	return false
}

// GetPreConsumedQuota 返回实际预扣的额度。
func (s *BillingSession) GetPreConsumedQuota() int {
	return s.preConsumedQuota
}

// Receipt 转发资金来源的扣减回执（实现 moneyport.FundingReceipt）。
// 资金来源不按账户拆分扣减（如订阅）时返回 nil。
func (s *BillingSession) Receipt() []moneyport.AccountShare {
	receipt, ok := s.funding.(moneyport.FundingReceipt)
	if !ok {
		return nil
	}
	return receipt.Receipt()
}

// WalletDebitShares 返回钱包计费累计扣减的各账户份额（预扣 + 追扣 + 结算补扣），
// 供异步任务持久化，退款按原账户精确退回。非钱包计费返回 nil。
func (s *BillingSession) WalletDebitShares() []model.LedgerShare {
	return ledgerSharesFromReceipt(s.Receipt())
}

// WalletSessionDebitShares 从 RelayInfo 的计费会话提取钱包累计扣减份额
// （异步任务持久化用）；无计费会话或非钱包计费返回 nil。
func WalletSessionDebitShares(relayInfo *relaycommon.RelayInfo) []model.LedgerShare {
	if relayInfo == nil || relayInfo.Billing == nil {
		return nil
	}
	receipt, ok := relayInfo.Billing.(moneyport.FundingReceipt)
	if !ok {
		return nil
	}
	return ledgerSharesFromReceipt(receipt.Receipt())
}

// ledgerSharesFromReceipt 在 service 边界把 moneyport 叶子 DTO 转回账本份额，
// 使 TaskPrivateData.WalletDebit 的外部契约保持 model.LedgerShare 不变。
func ledgerSharesFromReceipt(shares []moneyport.AccountShare) []model.LedgerShare {
	if len(shares) == 0 {
		return nil
	}
	out := make([]model.LedgerShare, 0, len(shares))
	for _, share := range shares {
		out = append(out, model.LedgerShare{Account: model.LedgerAccount(share.Account), Amount: share.Amount})
	}
	return out
}

func (s *BillingSession) Reserve(targetQuota int) error {
	return s.reserve(targetQuota, false)
}

// ReserveBeforeSubmit never uses the trusted-user bypass: a known task retry
// price must be covered before the new upstream request is sent.
func (s *BillingSession) ReserveBeforeSubmit(targetQuota int) error {
	return s.reserve(targetQuota, true)
}

func (s *BillingSession) reserve(targetQuota int, requireAvailableQuota bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settlementIntent != nil {
		return model.ErrBillingFinalized
	}
	if s.reservationUncertain {
		if err := s.resolveReservationLocked(); err != nil {
			return err
		}
	}

	if s.relayInfo != nil {
		_, imageRequest := s.relayInfo.Request.(*dto.ImageRequest)
		requireAvailableQuota = requireAvailableQuota || imageRequest || s.relayInfo.ImageRequestCount > 0
	}
	if s.settlementUncertain {
		if err := s.resolveSettlementBeforeReserveLocked(); err != nil {
			return err
		}
	}
	if s.taskOwned {
		return model.ErrBillingFinalized
	}
	if s.settled || s.refunded || s.trusted && !requireAvailableQuota || targetQuota <= s.preConsumedQuota {
		return nil
	}

	delta := targetQuota - s.preConsumedQuota
	if delta <= 0 {
		return nil
	}
	if s.durableTerminalSource() {
		if err := s.reserveDurableLocked(targetQuota, false); err != nil {
			return err
		}
		if requireAvailableQuota {
			s.trusted = false
		}
		return nil
	}

	if err := s.reserveToken(delta); err != nil {
		return err
	}
	if err := s.reserveFunding(delta); err != nil {
		// 与初始 preConsume 保持同一顺序：先令牌、后资金。资金失败时只需
		// 回滚令牌，避免“资金 reserve 已回滚后，同目标重试命中旧 debit
		// 幂等重放却未再次扣款”的生命周期漏洞。
		if delta > 0 && !s.relayInfo.IsPlayground {
			if rollbackErr := model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, delta); rollbackErr != nil {
				common.SysLog(fmt.Sprintf("error rolling back token quota after reserve funding failure (userId=%d, tokenId=%d, amount=%d): %s",
					s.relayInfo.UserId, s.relayInfo.TokenId, delta, rollbackErr.Error()))
			}
		}
		return err
	}

	s.preConsumedQuota += delta
	s.tokenConsumed += delta
	s.extraReserved += delta
	if requireAvailableQuota {
		s.trusted = false
	}
	s.syncRelayInfo()
	return nil
}

// ---------------------------------------------------------------------------
// PreConsume — 统一预扣费入口（含信任额度旁路）
// ---------------------------------------------------------------------------

// preConsume 执行预扣费：信任检查 -> 令牌预扣 -> 资金来源预扣。
// 资金失败时补偿令牌；补偿失败/提交 ACK 不确定仍需原有对账（非跨表原子预扣）。
func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	s.captureBillingIdentity()
	effectiveQuota := quota

	// ---- 信任额度旁路 ----
	if s.shouldTrust(c) {
		s.trusted = true
		effectiveQuota = 0
		logger.LogInfo(c, common.LogText("user %d has enough quota, trusted without pre-consume (funding=%s)", s.relayInfo.UserId, s.funding.Source()))
	} else if effectiveQuota > 0 {
		logger.LogInfo(c, common.LogText("user %d needs pre-consume %s (funding=%s)", s.relayInfo.UserId, logger.FormatQuota(effectiveQuota), s.funding.Source()))
	}

	var fundingErr error
	if s.durableTerminalSource() {
		fundingErr = s.reserveDurableLocked(effectiveQuota, true)
		if fundingErr == nil {
			return nil
		}
		if errors.Is(fundingErr, model.ErrBillingTokenInsufficient) {
			return types.NewErrorWithStatusCode(fundingErr, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
	} else {
		// ---- 1) Alternate funding retains its declared independent contract. ----
		if effectiveQuota > 0 {
			if err := PreConsumeTokenQuota(s.relayInfo, effectiveQuota); err != nil {
				return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
			}
			s.tokenConsumed = effectiveQuota
		}

		// ---- 2) 预扣资金来源 ----
		fundingErr = s.funding.PreConsume(effectiveQuota)
	}
	if err := fundingErr; err != nil {
		// 预扣费失败，回滚令牌额度
		if !s.durableTerminalSource() && s.tokenConsumed > 0 && !s.relayInfo.IsPlayground {
			if rollbackErr := model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, s.tokenConsumed); rollbackErr != nil {
				common.SysLog(common.LogText("error rolling back token quota (userId=%d, tokenId=%d, amount=%d, fundingErr=%s): %s",
					s.relayInfo.UserId, s.relayInfo.TokenId, s.tokenConsumed, err.Error(), rollbackErr.Error()))
			}
			s.tokenConsumed = 0
		}
		// 模型不支持赠金支付且付费余额不足：返回清晰错误，不得静默扣赠金
		if errors.Is(err, ErrModelBonusNotSupported) {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		// TODO: model 层应定义哨兵错误（如 ErrNoActiveSubscription），用 errors.Is 替代字符串匹配
		if errors.Is(err, moneyport.ErrWalletInsufficientBalance) {
			userQuota, quotaErr := model.GetUserQuota(s.relayInfo.UserId, false)
			if quotaErr != nil {
				userQuota = 0
			}
			return types.NewErrorWithStatusCode(
				errors.New(i18n.T(c, i18n.MsgQuotaUserInsufficient, map[string]any{"Remaining": logger.FormatQuota(userQuota)})),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "no active subscription") || strings.Contains(errMsg, "subscription quota insufficient") {
			return types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgQuotaSubscriptionInsufficient, map[string]any{"Error": errMsg})), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}

	s.preConsumedQuota = effectiveQuota

	// ---- 同步 RelayInfo 兼容字段 ----
	s.syncRelayInfo()

	return nil
}

func (s *BillingSession) reserveFunding(delta int) error {
	// 不具备补充预扣能力的资金来源 fail-closed：明确报错，不得静默放行，
	// 否则令牌额度已扣而资金侧未扣，会形成免费用量。
	reservable, ok := s.funding.(moneyport.ReservableFunding)
	if !ok {
		return types.NewError(fmt.Errorf("unsupported funding source: %s", s.funding.Source()), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	// 传目标预扣值而非增量。一次请求可能逐级 Reserve 多次：钱包幂等键必须包含
	// 目标预扣值（仅用 request_id 会让第二次合法增量与第一次发生冲突），
	// 订阅则把目标值换算回自身的补充增量。
	// 与旧行为不同：余额不足时扣减失败并返回错误，不再产生负余额欠费；
	// 预扣前的新建会话已做余额校验，仅极端并发/上游重估场景会命中。
	err := reservable.Reserve(s.preConsumedQuota + delta)
	if err == nil {
		return nil
	}
	if s.funding.Source() == BillingSourceSubscription {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("订阅额度不足或未配置订阅: %s", err.Error()),
			types.ErrorCodeInsufficientUserQuota,
			http.StatusForbidden,
			types.ErrOptionWithSkipRetry(),
			types.ErrOptionWithNoRecordErrorLog(),
		)
	}
	if errors.Is(err, ErrModelBonusNotSupported) || errors.Is(err, moneyport.ErrWalletInsufficientBalance) {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
}

func (s *BillingSession) reserveToken(delta int) error {
	if delta <= 0 || s.relayInfo.IsPlayground {
		return nil
	}
	if err := PreConsumeTokenQuota(s.relayInfo, delta); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return nil
}

// shouldTrust 统一信任额度检查，适用于钱包和订阅。
func (s *BillingSession) shouldTrust(c *gin.Context) bool {
	// 异步任务（ForcePreConsume=true）必须预扣全额，不允许信任旁路
	if s.relayInfo.ForcePreConsume {
		return false
	}

	trustQuota := operation_setting.GetQuotaSetting().TrustQuotaUSD * common.QuotaPerUnit
	if trustQuota <= 0 || math.IsNaN(trustQuota) || math.IsInf(trustQuota, 0) {
		return false
	}

	// 检查令牌是否充足
	tokenTrusted := s.relayInfo.TokenUnlimited
	if !tokenTrusted {
		tokenQuota := c.GetInt("token_quota")
		tokenTrusted = float64(tokenQuota) > trustQuota
	}
	if !tokenTrusted {
		return false
	}

	switch s.funding.Source() {
	case BillingSourceWallet:
		return float64(s.relayInfo.UserQuota) > trustQuota
	case BillingSourceSubscription:
		// 订阅不能启用信任旁路。原因：
		// 1. PreConsumeUserSubscription 要求 amount>0 来创建预扣记录并锁定订阅
		// 2. SubscriptionFunding.PreConsume 忽略参数，始终用 s.amount 预扣
		// 3. 若信任旁路将 effectiveQuota 设为 0，会导致 preConsumedQuota 与实际订阅预扣不一致
		return false
	default:
		return false
	}
}

// syncRelayInfo 将 BillingSession 的状态同步到 RelayInfo 的兼容字段上。
func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()

	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionQuotaPeriod = sub.quotaPeriod
		info.SubscriptionPreConsumed = sub.preConsumed + int64(s.extraReserved)
		info.SubscriptionPostDelta = 0
		info.SubscriptionSnapshotUnknown = sub.SnapshotUnknown
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter + int64(s.extraReserved)
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
	} else {
		info.SubscriptionId = 0
		info.SubscriptionQuotaPeriod = 0
		info.SubscriptionPreConsumed = 0
	}
}

// ---------------------------------------------------------------------------
// NewBillingSession 工厂 — 根据计费偏好创建会话并处理回退
// ---------------------------------------------------------------------------

// NewBillingSession 根据用户计费偏好创建 BillingSession，处理 subscription_first / wallet_first 的回退。
func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(fmt.Errorf("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	pref := common.NormalizeBillingPreference(relayInfo.UserSetting.BillingPreference)

	// 钱包路径需要先检查用户额度
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if userQuota <= 0 {
			return nil, types.NewErrorWithStatusCode(
				errors.New(i18n.T(c, i18n.MsgQuotaUserInsufficient, map[string]any{"Remaining": logger.FormatQuota(userQuota)})),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		relayInfo.UserQuota = userQuota

		// P0-5：按模型赠金资格决定钱包账户顺序与预扣前校验。
		// eligible 模型检查 bonus+reward+paid；非 eligible 模型允许 reward+paid
		//（邀请奖励不受赠金限制），不足时返回明确错误，不得静默扣减赠金。
		debitOrder := WalletDebitOrder(relayInfo)
		paid, bonus, reward, balanceErr := model.GetUserAccountBalances(relayInfo.UserId)
		if balanceErr != nil {
			return nil, types.NewError(balanceErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if WalletBonusEligible(relayInfo) {
			if paid+bonus+reward < preConsumedQuota {
				return nil, types.NewErrorWithStatusCode(
					fmt.Errorf("预扣费额度失败, 用户剩余额度: %s, 需要预扣费额度: %s", logger.FormatQuota(paid+bonus+reward), logger.FormatQuota(preConsumedQuota)),
					types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
					types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
			}
		} else if paid+reward < preConsumedQuota {
			return nil, types.NewErrorWithStatusCode(
				ErrModelBonusNotSupported,
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}

		accountOrder := make([]string, 0, len(debitOrder))
		for _, account := range debitOrder {
			accountOrder = append(accountOrder, string(account))
		}
		funding, factoryErr := createFundingSource(moneyport.SourceWallet, moneyport.FactoryRequest{
			UserID: relayInfo.UserId, RequestID: relayInfo.RequestId, BillingModel: relayInfo.GetBillingModelName(),
			Amount: preConsumedQuota, AccountOrder: accountOrder,
		})
		if factoryErr != nil {
			return nil, types.NewError(factoryErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		session := &BillingSession{
			lang: i18n.StatedLang(c), relayInfo: relayInfo, funding: funding}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		subConsume := int64(preConsumedQuota)
		if subConsume <= 0 {
			subConsume = 1
		}
		funding, factoryErr := createFundingSource(moneyport.SourceSubscription, moneyport.FactoryRequest{
			UserID: relayInfo.UserId, RequestID: relayInfo.RequestId, BillingModel: relayInfo.GetBillingModelName(), Amount: int(subConsume),
		})
		if factoryErr != nil {
			return nil, types.NewError(factoryErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		session := &BillingSession{
			lang: i18n.StatedLang(c), relayInfo: relayInfo, funding: funding}
		// 必须传 subConsume 而非 preConsumedQuota，保证 SubscriptionFunding.amount、
		// preConsume 参数和 FinalPreConsumedQuota 三者一致，避免订阅多扣费。
		if apiErr := session.preConsume(c, int(subConsume)); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	switch pref {
	case "subscription_only":
		return trySubscription()
	case "wallet_only":
		return tryWallet()
	case "wallet_first":
		session, err := tryWallet()
		if err != nil {
			if err.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				return trySubscription()
			}
			return nil, err
		}
		return session, nil
	case "subscription_first":
		fallthrough
	default:
		hasSub, subCheckErr := model.HasActiveUserSubscription(relayInfo.UserId)
		if subCheckErr != nil {
			return nil, types.NewError(subCheckErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if !hasSub {
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr != nil {
			if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				// 仅当用户的活跃订阅允许钱包回退时才回退到钱包，否则返回订阅额度不足错误
				allowOverflow, overflowErr := model.UserActiveSubscriptionsAllowWalletOverflow(relayInfo.UserId)
				if overflowErr != nil {
					return nil, types.NewError(overflowErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
				}
				if allowOverflow {
					return tryWallet()
				}
				return nil, apiErr
			}
			return nil, apiErr
		}
		return session, nil
	}
}
