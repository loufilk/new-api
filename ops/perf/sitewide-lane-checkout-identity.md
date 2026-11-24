# Dormant shared checkout identity for participating Epay paths

Reference base: efd9eae6d8123346e66d8b08ab83995b9c641dba. This stage
does not enable checkout, alter the ordinary registered Epay flow, add
a merchant request or form, migrate a deployed store, or register an
issuer. The only physical install is a disposable tagged PostgreSQL fixture.

## Claim before family locks and any form

A trusted dispatcher must first choose a stable opaque request ID and
trade number; neither is generated on a retry in this path. The atomic
creation claims both independent unique keys in one namespace,
`lane_checkout_identities_v1`, before family order or wallet locks. It
signs the exact schema identity, issuer, issuer generation, wallet epoch,
family, user, trade, request and quote. The canonical stores, relation
identities, permanent tables and both immediate unconditional unique
indexes must match. Current enrolled issuer ID, generation, wallet epoch
and key proof are checked while holding a share lock on the registry.
A new ID cannot displace a previously visible TopUp or SubscriptionOrder
of any status. Reservation and creation must share one transaction and
roll back together on any errors, including logical headroom rejection.
The PostgreSQL fixture also denies modifying/removing namespace rows.

The wallet top-up path puts the namespace reservation, signed Epay
binding and pending order into the same transaction. The bounded
cross-family counterpart is a transactional reservation primitive for
Epay subscriptions; no subscription HTTP path or business policy is
wired. Both participating families take the namespace first; a competing
identity therefore waits without taking a family wallet/order lock.
Exact replay may prove the same stored identity, but it is not a second
authorization to generate a form; changing issuer, epoch, family, user,
quote, request or trade cannot create a second order under either prior
key. An uncertain checkout commit cannot release a form. Capture/status/
retry must match both the original allocation and the original signed
order; old issuance is blocked after issuer rotation, while still-proven
historical orders can reconcile under current custody. Historical key
retention, revocation, and migration require release policy. No capture
issued before this namespace can be transparently adopted. A future
rollout over prior orders must drain them or retain a separate expressly
authorized historical recovery path before requiring this evidence.

## What this does not fence

Ordinary registered top-up and subscription creation are untouched.
A legacy writer, older process, other payment product or privileged SQL
that bypasses the common allocator can still create form/order state
outside it. Eligibility here is limited to participating writers; the
fixture's exact physical roster, append-only checks and locks are not a
claim of a deployed fleet fence. The legacy subscription mirror may
take User before TopUp while older Epay paths can take TopUp before
User; the common ordering is proven only for participating allocators,
not for a mixed fleet. Moving every issuer and trade table into a
reviewed physically enforced namespace, blocking and draining old
writers and resolving preexisting cross-family duplicates remain
activation gates. Other providers and manual credits are not newly
covered. This slice emits no checkout form, so no retry or disappearance
of that response is interpreted as provider-safe duplicate issuance.

Commission eligibility remains based on existing credit completion,
not capture or namespace-allocation time. No real money or live provider
was used in the test fixtures.
