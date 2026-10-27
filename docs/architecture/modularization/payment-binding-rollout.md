# Payment order binding rollout

## Purpose

`chuanapi_payment_order_bindings` is a server-owned payment snapshot. New Waffo
and Waffo Pancake orders persist their local order and binding in one database
transaction. A callback is allowed to settle only when its provider facts match
that snapshot.

This intentionally makes pre-existing pending orders without a binding fail
closed. The deployment process must therefore inventory those orders before
enabling the new callback path in production.

## Pre-deployment gate

1. Take and verify the normal production database backup.
2. Run the application migration in a staging/canary database first and verify
   `chuanapi_payment_order_bindings` exists with `trade_no` as its primary key.
3. Run `bin/audit_payment_binding_pending.sql` against the production database
   in read-only mode.
4. Save the result as deployment evidence. Zero rows is the preferred release
   state.

The inventory query covers pending `top_ups` and `subscription_orders` for
`waffo` and `waffo_pancake` that have no binding.

## If legacy pending rows exist

Do **not** synthesize a binding from a newly received webhook. The callback is
untrusted input even when its signature is valid for the provider; the binding
exists specifically to anchor the callback to facts captured before settlement.

Use one of these operational outcomes per order:

- let an unpaid order expire/cancel under the existing business process; or
- reconcile it against authoritative provider-side records and create a reviewed
  one-off recovery plan using the original merchant/store/environment, upstream
  payment identity, amount, currency and buyer identity.

Any one-off binding backfill must be produced from trusted provider/order
records, peer-reviewed, backed up, and executed separately from the callback
request. There is intentionally no generic "accept and learn from webhook"
fallback in application code.

## Callback failure semantics

Permanent callback mismatches (wrong store/environment/amount/currency/buyer or
missing legacy binding) are acknowledged without settlement so they do not
create an endless provider retry loop.

Transient binding database lookup/update failures are retryable and return a
5xx response. After storage recovers, resending the same valid callback must
settle once; repeated successful callbacks remain idempotent at the local order
and ledger layers.

## Migration verification matrix

For every supported database, run and record:

- fresh schema migration;
- upgrade from a schema without the newer binding columns;
- repeated migration (idempotency);
- duplicate `trade_no` rejection;
- local order + binding transaction rollback when binding persistence fails;
- two-process/multi-connection callback contention against the same order.

SQLite coverage lives in
`modules/chuanapi/payment/order_binding_migration_test.go`. SQLite is not a
substitute for the MySQL/PostgreSQL concurrency and DDL matrix.

## Rollback note

Do not drop the binding table during an application rollback. Preserve its rows
as payment evidence. If rolling back to code that does not enforce bindings,
disable or isolate Waffo/Waffo Pancake callback traffic first; otherwise the
rollback can re-open the weaker callback acceptance behavior this change was
designed to remove.
