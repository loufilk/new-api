# Billing Contract Freeze

This contract records current semantics before Wallet extraction. It does not
claim the whole billing lifecycle is atomic merely because selected settlement
paths are atomic.

## Operations

Every funding implementation must preserve the observable semantics of:

- **PreConsume** — initial request reservation/charge.
- **Reserve** — raise a reservation to an absolute target; repeating the same or
  lower target is a no-op.
- **Settle** — apply final or incremental delta.
- **Refund** — return outstanding pre-consumption after request failure.
- **Receipt** — return a defensive copy of actual account debit distribution.
- **Replay** — repeat the same operation identity without duplicate mutation.

Every operation contract must state operation identity, wallet/subscription
mutation, token mutation, atomicity boundary, retry behavior and the state left
after failure.

## Current atomicity matrix at `5afacf7da`

| Scenario | Funding mutation | Token mutation | Atomicity |
| --- | --- | --- | --- |
| PostConsume wallet settle | wallet debit/credit | token debit/credit | **YES**, one DB transaction |
| BillingSession wallet settle | wallet debit/credit | token debit/credit | **YES**, one DB transaction; missing transactional capability fails closed |
| WSS segment settle | wallet delta | token delta | **YES for the operation-scoped wallet settlement path** |
| Wallet PreConsume | wallet debit | token debit | **NO**, ordered steps with compensation |
| Wallet Reserve | wallet debit | token debit | **NO**, token-first with rollback when funding fails |
| Failure Refund | wallet/subscription credit | token credit | **NO**, asynchronous/compensating steps |
| Task final settle | selected funding contract | token contract | follows the selected funding path and persisted operation identity |
| Subscription settle | subscription usage delta | token delta | **NO**, funding then token update |

## Operation identity

Request-level settlement cannot assume that `request_id` alone is always the
operation identity. WSS and other incremental settlement must include a stable
segment/operation suffix. Async Task settlement must persist enough identity to
resume after restart without inventing a new charge identity.

The final port design must make idempotency identity explicit rather than hide it
inside a concrete Wallet implementation.

## Request, WSS and Task boundaries

- Request billing follows PreConsume -> optional Reserve -> Settle, or Refund on
  terminal failure.
- WSS uses one request identity plus distinct segment identities. A repeated
  segment may replay; a different segment must not collide with it.
- Async Task persists billing/funding identity and safe request-time pricing
  facts before returning the task handle. Polling restores those facts and then
  follows the selected funding settlement/refund contract.

## Transaction capability migration requirement

The current service-private `transactionalSettlementFunding` cannot cross a Go
package boundary because it uses unexported methods and returns root
`model.LedgerResult`. Before WalletFunding moves out of `service`, Phase 3/4 must
replace that shape with a public structural capability or a host adapter using a
host-independent settlement result DTO. The Wallet module must be able to
participate in the host transaction without importing root `service` or root
`model` business types.
