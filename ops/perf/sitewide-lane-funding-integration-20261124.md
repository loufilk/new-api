# Ordinary funding integration: dispatch seam and remaining boundaries

Development base: `1766c6fa2c7f5ca20e73ea7314f81d84927fe098`.
This is not production activation, a shard-count setting, a migration or deployment.
No current funding factory or `BillingSession` implements the new optional port.

## Implemented seam

`relay/common.BillingHTTPDispatchClaimer` can be implemented by a future, already
bound execution owner. The common HTTP sender invokes it after normal preflight
and before the attempted-send flag or `http.Client.Do`. A request-local sticky
marker is set before the call, including before an unacknowledged COMMIT error.
A repeated attempt is rejected, even if the `RelayInfo.Billing` field is replaced.
`PreserveFailedRequestBilling` and `RefundFailedRequestBilling` do not retry,
select ordinary funding or automatically refund such an owner. This includes DNS
failure, explicit provider rejection and client cancellation after the claim.
These cases need the owner's specific reconciliation, not ordinary refund logic.

Scope is deliberately narrow: native OpenAI Chat POST via the common HTTP sender,
nonempty body, inbound `/v1/chat/completions`, outbound `/chat/completions` suffix,
no query and no protocol conversion. The HTTP capability is explicitly rejected
by the shared WebSocket sender. This shape check is **not** admission authority:
plugins, custom adaptors and alternate transports still need separate review and
must not bind this owner merely because their URL has that suffix. No header
selects or enables it. The normal session remains unchanged, with no extra SQL,
Redis, background work or changed funding selection.

The existing shared-client redirect override remains in force. Only an owned
path also loses `Request.GetBody`; idempotency and authorization headers, body,
length, timeouts and the ordinary replay policy are preserved. Tests exercise a
written-body HTTP/1 disconnect and HTTP/2 REFUSED_STREAM. This establishes those
specific transport contracts, **not globally exactly-once provider execution**.
Connection recovery before any body is sent remains the HTTP transport's concern;
other transport implementations and upstream behavior require their own evidence.
For an owned streaming path, the local keepalive begins only after the dispatch
claim is acknowledged, so a slow claim cannot first emit an SSE 200 response.

## Exact current boundaries

- `PrepareRequestBilling`: normal request parse/token estimate/pricing comes first.
- `NewBillingSession`: billing preference, subscription/wallet choice, existing
  quota reads and wallet account order. Those balance reads are not lane proof.
- `BillingSession.preConsume` / `reserveDurableLocked`: host-held receipt plus
  `transactionalReservationFunding` and `ReserveBillingTokenTx` in the same
  `ReserveBillingFinalization` transaction.
- `BillingSession.Settle` and `settleDurableWithAccountingLocked`: original-token,
  source and terminal owner commit together; observed text recovery may add usage
  counters in that transaction.
- `settleObservedTextBilling`, `PreserveUnknownBillingDelivery`, realtime/task
  helpers and settlement recovery currently depend on concrete `*BillingSession`
  methods and source-specific durable facts, not just `BillingSettler`.
- Wallet cost logs expect ordinary wallet ledger references. Lab lane usage has
  its own idempotent projection and cannot silently use these ordinary references.

## Safe next integration sequence

1. **Owner arbitration first.** A canonical `billing_finalizations.request_id`
   marker, immutable lane execution receipt and dual shard hold must commit in
   one transaction. Owner route, original key fingerprint, lane/epoch and cap are
   frozen. A known unsupported request before any owner attempt may remain ordinary;
   a conflict, uncertain claim, replay or bound owner cannot switch route. Pin the
   request-local owner at reservation time, not only at the dispatch hook.
2. **Transaction-authoritative paid policy.** Preserve `moneyport.SourceWallet`
   externally. V1 is conservative wallet-only, finite concrete original key, no
   actual eligible bonus/reward, and native verified no-retry Chat route.
   Ordinary `WalletDebitOrder` and policy still decide semantics. Read the proof
   under the same authority/transaction as the hold. Escrow-time paid balance or
   observation snapshots are insufficient: a later bonus/reward credit can change
   which account ordinary billing would consume. A per-request user lock is a
   correct baseline but not a scalable hot path. Removing it requires an explicit
   funding-policy generation fenced by every relevant credit/policy writer.
3. **Do not wrap lane funding as ordinary HostBillingFinalizationSource.** The
   current host reservation unconditionally debits the original token. A lane
   has already escrowed original paid/key balances, so a simple MoneyPort swap
   would debit twice. Add an explicit lane execution capability/route that owns
   both shard balances and excludes that inherited original-token operation.
4. **Real price and measured usage.** Use normal `ModelPriceHelper` output and
   verified maximum exposure, then normal measured text pricing. Do not reuse
   the laboratory frozen synthetic rate. Before activation, verify immutable
   blinded final-wire facts (digest and exact cap inputs) after plugins and
   parameter overrides, using a bounded read-only clone proven equal to the
   bytes actually sent. Neither a mutable `*http.Request` nor raw credential
   headers belong in the durable claim contract; unsupported clones stay
   ordinary before binding. The current thin context-only seam does not yet
   supply this evidence. Keep tiered, asynchronous, multimodal,
   plugin and ambiguous usage cases ordinary until their contracts are covered.
   A normal retry budget greater than zero is not transparent under this one-way
   seam; exclude it until safe retries within the same owner are implemented.
5. **Projection ownership.** Add an explicit measured-text settlement capability
   returning whether this owner durably owns counters/log projection. Freeze
   actual quota and immutable usage under that owner before money movement.
   Suppress ordinary counters only on that explicit result, including replay.
   The lane's pending projection must survive process loss, settle idempotently,
   and use a correct wallet-cost representation without invented per-request
   ordinary ledger entries. Never run both the lane projector and legacy counters.
6. **Recovery/unknown delivery.** Route preservation and recovery to the frozen
   execution owner. No process-local flag alone proves non-delivery or successful
   settlement. Unknown commit and output-without-usage remain held/review, never
   ordinary refunds. Startup, drain, logical balances, existing-key generations
   and cross-mode owner tests remain activation gates from the other workstreams.

## Verification scope

Tests substitute only the not-yet-installed durable claimer. They exercise the
actual common HTTP entry point and existing retry/refund entry points with
synthetic loopback providers, not production accounts or real funds. They cover
ordinary/no capability, header non-opt-in, successful single claim, refused/lost
ACK, repeated send after source replacement, unsupported route/WebSocket, exact
body/headers, redirects, HTTP/1 and HTTP/2 written-body replay, terminal failures,
and slow-claim SSE output. PostgreSQL is not changed by this slice. End-to-end
owner-to-real-transport integration still requires the production-capable owner,
policy, funding and usage projection implementation above.
