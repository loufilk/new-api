package model

// AgentOperationSummary is the server-owned display paired with exactly one
// transaction authorization context. Labels are descriptions, never credentials.
type AgentOperationSummary struct {
	Action           string `json:"action"`
	AgentUserID      int    `json:"agent_user_id"`
	Currency         string `json:"currency"`
	CurrencyExponent int    `json:"currency_exponent"`
	AmountMinor      string `json:"amount_minor"`
	FeeMinor         string `json:"fee_minor"`
	NetMinor         string `json:"net_minor"`
	Quota            int64  `json:"quota"`
	TargetLabel      string `json:"target_label"`
	RecipientLabel   string `json:"recipient_label"`
	PolicyVersion    int64  `json:"policy_version"`
}

type AgentPreparedOperation struct {
	VerificationContext AgentTransactionContext `json:"verification_context"`
	Summary             AgentOperationSummary   `json:"summary"`
	StatusURL           string                  `json:"status_url"`
}

// Completed means the requested database transition committed. It does not mean
// an external payment succeeded: ResourceState may still be payment_unknown.
// Not-observed never proves a disconnected request failed or permits a new
// request ID. Clients must reconcile or retry the identical idempotency key.
type AgentOperationStatus struct {
	RequestID     string `json:"request_id"`
	Action        string `json:"action"`
	Status        string `json:"status"`
	ResourceType  string `json:"resource_type"`
	ResourceID    string `json:"resource_id"`
	ResourceState string `json:"resource_state"`
	Resource      any    `json:"resource,omitempty"`
}

// AgentPlatformModels is the compatibility boundary for the platform's additive
// schema. Wallet and legacy redemption tables remain their original owners.
func AgentPlatformModels() []any {
	return []any{
		&AgentPolicyState{}, &AgentPolicy{}, &AgentPolicyChange{},
		&AgentProfile{}, &AgentProfileAudit{}, &AgentProfileOperation{}, &AgentInvitationPolicyOperation{}, &AgentMigrationIssue{},
		&AgentPaymentReceipt{}, &AgentOutboundPaymentReceipt{}, &AgentFundBatch{}, &AgentFundSlice{}, &AgentFundLedger{}, &AgentFundAnomaly{}, &AgentFundReclassification{},
		&AgentPurchaseQuote{}, &AgentPurchaseOrder{}, &AgentPurchaseRefund{}, &AgentDelivery{}, &AgentPurchaseAudit{},
		&AgentCommissionAccount{}, &AgentCommissionCap{}, &AgentCommissionAward{}, &AgentCommissionDebt{}, &AgentCommissionLedger{},
		&AgentCommissionOutbox{}, &AgentCommissionInbox{}, &AgentCommissionRefundFact{},
		&AgentCommissionOperation{}, &AgentCommissionRecipient{}, &AgentCommissionQuote{}, &AgentCommissionConversion{}, &AgentCommissionAllocation{},
		&AgentCommissionWithdrawal{}, &AgentCommissionWithdrawalAudit{}, &AgentCommissionPayoutEvidence{},
	}
}
