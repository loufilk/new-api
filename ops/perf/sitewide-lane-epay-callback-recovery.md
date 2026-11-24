# Epay captured receipt acknowledgement and bounded recovery (dormant)

This is an internal development seam, not a new route, scheduler, worker,
startup registration, or release authority. The registered Epay handler
always uses the original nil path with the same request/response and
payment-code/ordinary behavior. The optional path can never be selected by
a header, URL parameter, raw store identifier, or ordinary callback facts.
No active provider is contacted in these tests.

## Candidate contract

- Future trusted issuance must prove an exclusive ordinary top-up order with
  its pinned merchant/method/environment/quote and original wallet epoch.
  Never attach this bound sink to payment-code, subscription, bonuses,
  invoice, agent, legacy unquoted, or ambiguous orders. Once selected, the
  scoped handler does not use any alternate/raw wallet fallback.
- The same actual Epay parser, terminal signature and signed merchant checks
  precede the sink. Scoped malformed or duplicate form values are refused.
  Only the successful model result with a positive immutable receipt ID in
  captured_waiting_credit or credited state permits the candidate `success`
  provider acknowledgement. Waiting acknowledges accepted evidence, never
  completion of principal. Unsupported/uncertain outcome and any unexpected
  DB failure return `fail`, even if non-authoritative result fields suggest
  apparent success. This protocol interpretation is exercised only with
  signed local fixtures. Merchant-specific acknowledgement/retry semantics
  still need approval and independent validation before any route wiring.
- The opaque model handle has no untagged constructor. It requires exact
  transaction authority before order lookup for capture, status and retry,
  preserving the same configured co-located PostgreSQL MAIN/LOG guard.
  Status uses one consistent snapshot of receipt/order/ledger/log/outbox and
  has no credit side effects. Recovery is bounded to eight explicit already
  authorized handles under an eight-second shared deadline; attempts include
  failures. No table sweep, provider query, automatic retry, or background
  work is added.
- The checkout can expire before verified capture; existing stale failure
  and expiry writers require a pending order and must not change a durable
  captured_waiting_credit record. An expired/failed order with late capture
  remains a separate review problem, not silently revived. Never delete,
  auto-expire or auto-refund captured liabilities, or issue automatic
  provider cancellation. Route remains off until the staff reconciliation
  and expiry/refund policy is agreed and tested.

## Commission and timing release gate

The operator view separates local receipt-recorded time (immutable
PaymentReceipt.PaidAt in this implementation; not a provider clock) from
credit completion time (TopUp.CompleteTime, zero while waiting). The
existing commission outbox creation uses the successful order's
CompleteTime and current eligibility at posting. Reconciliation in the
prior dormant model calls that original helper only when principal
commits; this patch does not change rewards, eligibility or time rules.
A parked payment may cross a referral eligibility window or policy change.
Before real activation, decide explicitly whether those business rules
anchor at capture or credit and how to preserve the original approved
eligibility snapshot, without altering any already-earned award or
creating two events. The two timestamps and one immutable receipt make
the difference auditable. A missing release-bound issuer, exact provider
acknowledgement policy, staff alert/recovery and retention policy, or
cross-provider/manual compatibility continue to block default sitewide
activation. No approval to change business eligibility is implied here.
