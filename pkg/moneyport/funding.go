// Package moneyport 定义资金来源的能力接口（capability port）。
//
// 计费编排层（service.BillingSession）通过这些接口与具体资金来源交互，
// 不再断言 *WalletFunding / *SubscriptionFunding 等实现类型：新增资金来源
// 只需实现所需能力，编排层无需修改。
//
// 叶子包约束：本包只依赖标准库，严禁 import model / service / controller /
// router / relay。因此这里的 DTO 与账本类型（model.LedgerShare）同构但独立，
// 由 service 层在边界做转换。能力接口只收录已有调用点证明其必要性的方法。
package moneyport

// AccountShare 描述一次资金变动在单个账户上的份额。
// Account 使用字符串而非账户枚举，避免叶子包依赖账本类型。
type AccountShare struct {
	Account string `json:"account"`
	Amount  int    `json:"amount"`
}

// SettlementResult is the host-independent result returned by a funding
// implementation when it participates in a caller-owned database transaction.
// It contains only facts BillingSession needs after commit.
type SettlementResult struct {
	Replayed          bool
	TotalAfter        int
	DebitShares       []AccountShare
	CreditShares      []AccountShare
	OutstandingShares []AccountShare
	ReceiptKnown      bool
	SourceID          int
	SourcePeriod      int64
	SourceHeld        int
	OriginalAmount    int
	AmountTotal       int64
	AmountUsedAfter   int64
	SourcePlanID      int
	SourcePlanTitle   string
	SnapshotUnknown   bool // Current allowance no longer belongs to the reserved period.
}

func (r SettlementResult) Clone() SettlementResult {
	r.DebitShares = append([]AccountShare(nil), r.DebitShares...)
	r.CreditShares = append([]AccountShare(nil), r.CreditShares...)
	r.OutstandingShares = append([]AccountShare(nil), r.OutstandingShares...)
	return r
}

// ReservableFunding 资金来源可把预扣额度抬升到目标值。
//
// 调用点：BillingSession.Reserve —— 上游重估用量后逐级补充预扣。
type ReservableFunding interface {
	// Reserve 把预扣额度补到 target（绝对目标值，不是增量）。
	// target 不高于当前预扣额度时必须是 no-op 并返回 nil，
	// 使同一 target 的重复调用天然幂等。
	Reserve(target int) error
}

// OutstandingReserveReporter 资金来源可报告它自己会退还的预扣额度。
//
// 调用点：BillingSession.needsRefundLocked —— 判断失败请求是否仍需退预扣。
// 未实现该能力的资金来源按 0 处理（即无独立于令牌额度的待退预扣）。
type OutstandingReserveReporter interface {
	// OutstandingReserve 返回该资金来源自身 Refund 会退还的预扣额度，
	// 无待退预扣时返回 0。只统计自己负责退还的部分：由编排层单独回滚的
	// 补充预扣不得计入，否则同一笔额度会被两条退款路径各算一次。
	OutstandingReserve() int
}

// FundingReceipt 资金来源可回执各账户的实际扣减份额。
//
// 调用点：BillingSession.WalletDebitShares —— 异步任务持久化扣减份额，
// 退款按原账户精确退回。无份额记录或不按账户拆分的资金来源返回 nil。
type FundingReceipt interface {
	// Receipt 返回累计扣减份额。返回值必须是副本，
	// 调用方修改它不得影响资金来源的内部状态。
	Receipt() []AccountShare
}
