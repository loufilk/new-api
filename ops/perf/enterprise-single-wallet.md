# Enterprise capacity: one wallet, one or many API keys

Objective: distinguish load on a single wallet from load on a shared API key
or upstream. The business target is **10,000 arrivals/minute for one account**.
A 20 s average stream at that rate implies roughly 3,333 in-flight streams.
Arrival rate, active streams, and tokens in/out are separate limits.

## Isolation and comparison

- Run against throwaway PostgreSQL 18.6 and Redis 8.8.0 using synthetic keys,
  receipts, accounts and balances. Use an HTTP streaming fixture on loopback.
  Do not expose the gateway to the public internet, attach production databases,
  use a paid model endpoint, or restore raw customer data.
- Pin and record the tested executable revision/digest, environment, CPU/memory
  allocations, Go/PG/Redis versions, fixture delays, channel/model, payload and
  stream lifetime. The baseline and candidate must differ only in the change
  under review. Keep optional dynamic routing, price policy, workers, Redis and
  database topology identical in each direct comparison.
- Open-loop clients must record **offered, dispatched, completed, rejected and
  locally dropped** requests. Keep generator scheduling delay and configured
  in-flight limits visible. If the generator cannot sustain offered load, do
  not report that step as achieved throughput.
- Compare (A) one account/one Key, (B) the same account/multiple Keys, and
  (C) multiple accounts. Keep the same arrival rate, input distribution,
  channel, mock first-token time and stream lifetime in each. Verify the fixture
  returns actual deltas and usage; do not count metadata or keepalive as text.

## Measurements

Measure p50/p95/p99 for ingress and pre-upstream time, pool acquisition and
connection reuse, first upstream event and first downstream content, and full
response duration. Measure actual client first visible text separately from
any gateway diagnostic that also counts reasoning/tools. Define stage
denominators and report sample count, instrumentation overhead, rejected
requests, and cancellations separately. Record SQL pool wait, PostgreSQL
lock waits/blockers, commit/WAL latency, Redis RTT and cache misses, and
resource use of the load generator as well as the service.

A and B both degrading relative to C implicates the shared account path,
but is not sufficient by itself to assign a cause. A alone suggests Key
or limiter contention. If ingress-to-send stays flat but mock receive time
or headers worsen, separate local harness saturation from transport wait.
Stream-tail and dashboard measurements can explain contention across
requests even when they do not delay their own request's first text.

## Gates

1. Run 500, 2,000, 5,000, 7,500 and 10,000 RPM, stopping at the first tier
   with unstable concurrency, errors or an overloaded driver. Re-run that
   tier for each credential topology and against the candidate.
2. At achieved 10,000 RPM run a 30-minute stable plateau and a separate
   two-hour workload with realistic input and stream lifetimes. Suggested
   same-fixture **additional gateway delay** budget: p95 <= 150 ms and
   p99 <= 300 ms; report normal-load success, throughput and fairness.
   A short mock experiment is not a production SLA.
3. Separately test overload, slow consumers, a drained replica, lost
   connections and recovery. Successful confirmed deliveries must reconcile
   with the durable request/ledger exactly once; in-flight unknown delivery
   is not a successful refund. Keep real upstream capacity approval and
   staged provider tests a separate gate.
