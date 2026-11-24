# Final 32/96/8 readiness rehearsal plan

Not an execution receipt, activation approval, or result from the existing
tagged single-route fixture. Run this only against a final feature-complete
build, disposable production-equivalent PostgreSQL 18.6, synthetic balances,
and loopback-only upstreams. An unknown or incomplete gate is not a pass.

## Admission semantics and connection capacity

- Publish the scope of all three settings before load. The existing lab gate
  was **96 per process**, not a 96 aggregate site cap. Two replicas can admit
  192 in aggregate with independent 96 limiters. If 96 is meant as a site cap,
  require independently tested cross-replica admission with no hidden setting
  reinterpretation, and repeat this plan after choosing the real scope.
- For each actual PG instance separately, determine ordinary capacity from
  max_connections less ordinary/reserved-superuser reservations. At each
  moment count the MaxOpen for every distinct app primary pool, log pool,
  and separately pooled migration, projection, repair and monitoring work
  reaching that instance. Distinct DSNs against one server still count
  twice; truly shared pool pointers count once. Include explicitly measured
  external peak and predeclared recovery/spare allowance.
- The bound must hold for two serving replicas, one serving plus the still
  connected draining replica, and any allowed same-version surge. A
  process counts until its pool closes, not until it leaves the load
  balancer. Repeat the calculation for a separate log server if deployed.
  App workers using the same pool do not add another MaxOpen; nevertheless
  prove their progress under saturation rather than treating spare
  connections as reserved service capacity.

## Paired tests and measurable acceptance

- Predeclare normal control p99/availability, outstanding owner-recovery age,
  admitted/dropped/error limits, PG spare/pool wait limits, and completion
  tolerance before starting. Verify the load driver can pace both classes.
  Baseline one replica, then offer the same mixed profile to two identical
  final revisions: one hot wallet distributed across existing finite Keys
  at 5,000 offered RPM plus explicitly chosen ordinary/low-volume classes.
  Record per-class and per-replica offered, admitted, dropped and completed;
  report latency distributions including ordinary control p99 separately
  from the hot wallet. Load-driver queueing is not service throughput.
- Run short smoke, then a stable 30-minute target plateau plus full drain
  where practical. Count egress, known completions, unknowns, all DB
  connection errors, pool waiting count/duration deltas for main and logs,
  actual server occupancy/slots, and pending recovery count/oldest age.
  Show the ordinary control remains inside its predeclared budget and
  recovery continues making progress. Separate a planned overload denial
  test from unexpected rejections at the target.
- Remove replica A from balancing while it owns known in-flight streams.
  Route new work to replica B, drain A, and verify exact settlement and
  closed pools. If allowed, add a same-version surge or replacement and
  retest capacity and controls. No mixed-version rollout is implied.
- Reconcile each request by canonical owner, exact original Key generation,
  wallet ledger, held/spent/returned values, usage log and counters. Confirm
  one terminal projection for each known completion, no silent response
  reformats or duplicate egress, and no payment/settlement by an ordinary
  fallback after an owned claim. Deliberately created unknowns must retain
  exact durable evidence and funds, never TTL-refund or replay.
- Keep sanitized command/build/hash, PG config, per-phase statistics, capacity
  budget and reconciliation as a release receipt. Do not infer live 5,000 RPM,
  the cluster-wide 96 cap, or deployment readiness from any current
  single-route test.
