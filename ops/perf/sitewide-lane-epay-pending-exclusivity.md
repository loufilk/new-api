# Dormant Epay pre-checkout order ownership

Reference base: 0a10b9565c789e0554db5c838b1c73fcaab1b932. This slice
does not register an Epay endpoint, change ordinary checkout, advertise a
provider form, schedule a retry, create a live issuer, migrate a deployed
store, or call any merchant.

## Creation before provider form

The private scoped creation command must run before generating or making
payable any provider form. It only accepts an unissued, new local ordinary
Epay draft with the exact original user, trade ID, method, CNY minor amount,
merchant, environment and quote. It cannot promote an existing order from
any status, even if no callback has arrived. The canonical order row lock
and unique insertion protect both sides of that decision. After creating
the new status-fenced local row, it rechecks logical wallet headroom under
the original user and slot locks. The order and a versioned immutable HMAC
binding (lane_epay_order_bindings_v1) commit together. A known rejection
or uncertain commit does not release a payable draft to the caller.

The HMAC includes the exact order Primary Key, user, namespace, wallet
epoch, merchant/payment/environment/quote, original amounts and creation
time. The trusted dedicated key has no ordinary constructor, configuration
default, or user-controlled path. Test fixtures use only an explicit
synthetic key and DDL in a disposable schema. Future signing key custody,
key retention and the active physical catalogue remain required; no
authorization is conferred by knowledge of the market quote. HMAC verification
always occurs with the correct order lock (or a consistent read snapshot).
Retried capture and recovery cannot switch local merchant, wallet epoch,
order or key after a quote, or adopt a partial restoration. A missed quote
or insufficient capacity rolls back everything. A later room race is still
handled by the earlier durable capture-waiting state, not by this preflight.

## Ordinary writer exclusion and remaining scope

The new row starts in lane_epay_pending_capture, not ordinary pending.
Ordinary Epay, manual completion and pending/expiry writers reject that
and captured_waiting_credit under the same existing order row lock.
The ordinary top-up ledger helper independently rejects a reserved status.
The subscription top-up mirror also uses the exact row lock and rejects a
reserved capture or positive standalone quoted Epay order, including after
success. Creation refuses a known subscription collision, but this is not
a global cross-product trade namespace. Other provider handlers cannot
credit the Epay row under their existing provider and pending-state checks. Ordinary top-up status/routing and SQL outside those two explicit statuses
remain the same; the existing subscription-mirror lookup takes a row lock
and rejects a collision with a standalone quoted Epay order. Scoped processing continues to
require both the transaction's custody authority and the dedicated signed
binding, before any capture receipt or credit. At successful credit the
existing success status and exact normal paid ledger, log and commission
event are retained. Credit completion (not local receipt recorded time)
continues to be the timestamp used by the existing commission rule;
this is preservation, not a change of business eligibility while parked.
The capture and credit timestamps remain independently visible for review.

These checks do not fence arbitrary SQL, an already-running incompatible
writer, stale full-row updates outside the audited flows or a different
provider's merchant capture. A completed legacy replay cannot post another
principal under transaction/status/ledger keys, but enabling callbacks
requires a durable dispatcher to select exact signed ownership even after
success and to preserve provider ACK semantics. A finite no-traffic
cutover, physical write fencing for old roles, the versioned catalogue and
key persistence, separate provider/manual audits, operator reconciliation
and explicit commission/expiration/reversal policy still block activation.
No real payment, refund, production credential, remote branch or deployment
was used.
