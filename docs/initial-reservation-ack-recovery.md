# Initial reservation acknowledgement loss: recovery boundary

## Scope

A first built-in wallet/subscription reservation atomically stores its original
source shares or subscription period, original Token identity, and `held_v2`
owner. If the real SQL COMMIT succeeds but its acknowledgement is lost, the HTTP
request still fails and must not dispatch upstream. Before returning that error,
only the invocation that prepared a **fresh initial** receipt may synchronously
request a refund through the existing unique-owner barrier. The original request
error is preserved; Task conversion also preserves its skip-retry instruction.
Existing-owner replays and later reserve increments do not gain this authority.
This guarantee relies on the current HTTP middleware generating a unique server
request ID; it is not a general exactly-once contract for external callers that
concurrently reuse the same ID.
Free-model, trusted-zero, price, account-order and subscription policies do not
change. There is no new table, state, external service or general held-row worker.

The attempt result explicitly separates `Applied` (acknowledged COMMIT) from
`FreshInitialPrepared` (the complete candidate was prepared before COMMIT). Neither
an error nor an in-memory candidate alone proves that money was committed.
`RequestBillingRefund` performs the existing conflict/locking owner lookup and
checks the complete receipt and authoritative source evidence. An absent owner
creates only a private claim that is rolled back; it cannot manufacture a credit.

## Failure matrix

| Boundary | Durable result / required action |
| --- | --- |
| Source, Token or owner write fails before a complete candidate | Initial transaction rolls back; no recovery authority is granted |
| Initial COMMIT actually rolls back | Refund owner barrier establishes absence; no credit or new held owner |
| Initial COMMIT succeeds, ACK lost; admission succeeds | `refund_ready_v2`; existing immediate/background worker refunds original source and Token atomically |
| Admission COMMIT succeeds, ACK lost | Existing ready queue remains recoverable without recreating the request |
| Admission read/write fails | No guessed refund; best-effort held-row review metadata and sanitized high-priority event |
| Admission and review metadata both unavailable | Existing held owner remains evidence; high-priority request-correlated event if process/log sink survives |
| Process exits after first COMMIT but before admission commits | May remain `held_v2`; **not guaranteed automatic recovery**; operator reconciliation required |
| Process exits after admission COMMIT | Existing queue survives; normal worker can resume |
| Same-ID live-owner replay loses its read-only COMMIT ACK | No refund admission; it may belong to an already dispatched request |
| Token identity changes before execution | Atomic refund rolls back and becomes review-required; replacement Token is never credited |
| Subscription period rolls over | Old-period receipt is finalized; the new allowance is not credited |

This is not end-to-end exactly-once delivery. Database outages, process exits and
an acknowledged upstream operation cannot be made atomic with HTTP dispatch by
moving a defer. Never refund all `held_v2` rows or infer rollback from age/errors.

## Review and rollout gate

Search existing application error logs for
`event=billing_initial_reservation_refund_unresolved priority=high` and its
`request_id`, fixed `stage`, classified `code`/`class`, and
`action=manual_reconciliation`. Raw driver errors, credentials and Token
fingerprints are excluded from this recovery event. If metadata persistence is
available, the same owner carries `last_error_code=initial_refund_unconfirmed`
and `escalated_at`. The existing read-only report **function** adds
`initial_reservation_refund_requires_review`; this is not a new HTTP endpoint.

An operator with an existing authorized read-only PostgreSQL connection can
inspect a specific frozen request ID with a bound parameter (do not interpolate
untrusted input):

```sql
SELECT request_id, user_id, source, state, source_held, token_held,
       pre_consumed, paid, bonus, reward, subscription_id, subscription_period,
       first_error_code, last_error_code, escalated_at, created_at, updated_at
FROM billing_finalizations
WHERE request_id = $1;
```

Correlate owner, original source receipt, Token identity and upstream/task
acceptance evidence before any separately authorized correction. Unknown
upstream delivery remains review-only. Replaying a request or adding a guessed
refund is not a reconciliation procedure.

Before upgrading a 0.9.3 deployment, drain or explicitly reconcile in-flight
Task/Midjourney work, pending refunds and unsettled accounting. Historical rows
without v2 ownership/original Token/subscription-period evidence must not be
promoted into the v2 queue by guessing. This change does not scan real production,
certify old-binary rollback, or reconcile historical production data. Synthetic
upgrade fixtures do not satisfy that production gate. No production fault
injection is authorized by these tests.

## Focused regression entry points

- `controller.TestRelayInitialReservationCommitACKRecoveryHTTP`: real HTTP Relay
  control flow, server RequestId middleware, original failure, zero upstream
  dispatch, wallet three-account/subscription recovery, real COMMIT rejection.
- `controller.TestTaskInitialReservationCommitACKRecoveryHTTP`: real Task
  controller and `RelayTaskSubmit`, retry budget above zero, zero upstream task,
  existing worker recovery (selected auth/channel/plugin context is fixture data).
- `service.TestInitialReservationRecoveryPostgres`: native PostgreSQL owner
  replay, admission/read/write/ACK failure, metadata outage, leased worker,
  original Token, subscription period and trusted-zero boundaries.

These require execution evidence from the final source pin. Test source alone is
not a passing result. These focused recovery tests use PostgreSQL 18.6 and disable
Redis; they do not verify Redis behavior. Actual Redis 8.8 coverage belongs to
separate existing test evidence. No MySQL or older PostgreSQL compatibility claim
is made here.
