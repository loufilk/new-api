package moneyport

// SettlementPolicyReporter is optional. A wallet implementation opts in only
// when its ordinary SettleInTx can be reconstructed using this exact immutable
// debit order and the authoritative receipt. Custom funding stays manual.
type SettlementPolicyReporter interface {
	SettlementAccountOrder() []string
}
