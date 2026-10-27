package moneyport

// HostBillingFinalizationSource explicitly opts a funding implementation into
// the host's terminal-only refund recovery. Wallet receipts must come from the
// host relay ledger (original preconsume/reserve operations); subscriptions must
// expose the host's original period-bound receipt. A Source() name alone does
// not establish this contract. Unmarked extension implementations keep their
// existing settlement/refund contract and are not scanned by the host worker.
type HostBillingFinalizationSource interface {
	UsesHostBillingFinalization() bool
}
