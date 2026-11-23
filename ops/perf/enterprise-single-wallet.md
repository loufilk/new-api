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

## Before admitting enterprise volume

- A completed SSE reply is not the ledger result. Record the exact request
  identifiers that emitted content and terminal usage. After stopping arrivals,
  drain and compare wallet changes, token usage, terminal financial owners,
  usage logs and user/channel aggregates by owner. Report any held, review, or
  unconfirmed result separately. Do not declare success from an HTTP 200 or
  from matching totals over different requests.
- Separate ordinary measured text-wallet recovery from unknown delivery,
  locally estimated usage, subscriptions, task callbacks, and realtime.
  Never infer a financial action from an error or usage-log display.
- Alert on due settlement age and owner-state backlog independently of
  process readiness. Check that a master worker is live. Drain recovery and
  check pending evidence before any downgrade. No automated downgrade or
  accounting action against old review rows is authorized by this document.

## PostgreSQL connection budget

`SQL_MAX_OPEN_CONNS` is a per-process main pool ceiling. When
`LOG_SQL_DSN` points to a separate database, its pool **inherits** that
ceiling unless `LOG_SQL_MAX_OPEN_CONNS` is explicitly set. If both databases
are on the same PostgreSQL server, both pools compete for the same server
connection capacity. Budget each server independently across all serving
replicas, background roles, separate log pools, observer connections,
migrations, and administrative reserve. Leave room for failover and avoid
holding a larger application queue than the request latency budget allows.

The stock Compose serving and migration services forward the eight optional
`SQL_MAX_*` and `LOG_SQL_MAX_*` values. Empty values keep the application
defaults. The managed one-shot migration requires at least two primary
connections. Check the effective numeric settings on each container; do
not dump the entire environment, which can contain credentials. Legacy
startup also logs `SQL pool log=… max_open=…`; the managed path is not
required to emit that message.
Measure PostgreSQL `max_connections`, peak application sessions, SQLSTATE
`53300` and Go pool wait together with row-lock wait. `group_config_changed`
errors can be caused by inability to read the authoritative version, not
just an actual group configuration change. A lower pool limit can eliminate
connection exhaustion while leaving a serialized single-wallet row as the
throughput bottleneck. A pool setting does not expand that row's capacity.

## Sample the existing wallet transaction

For diagnostic runs on PostgreSQL, set `WALLET_LOCK_TIMING_SAMPLE_PERMILLE`
to a small nonzero value (for example, `10` samples about 1% of transactions).
The default is off. Read the JSON after `wallet_lock_timing` in the process log
and split reservation, settlement and recovery settlement:

- `pool_and_begin_us`: connection checkout/setup plus BEGIN until the transaction
  callback entry, not pure pool wait. Compare with database/sql pool counters.
- `wallet_lock_query_us`: complete row-lock query wall time, including the
  round trip and result scan. Use PostgreSQL wait events to confirm contention.
- `after_lock_query_to_tx_return_us`: client-side remaining work and COMMIT or
  rollback return, not the exact server-side lock release timestamp. A missing
  field means the wallet lock was not acquired; separate acknowledged commit
  samples when interpreting the hold duration.

Keep the sample setting and count in test receipts. The report contains no
request IDs, account details, amounts, keys or raw SQL. Check PostgreSQL lock
waits alongside application telemetry; do not sum percentiles across stages.

## Prefunded wallet lanes: experimental proof versus release gates

The model experiment alone is **not proof of gateway capacity**. Test it in a
disposable PostgreSQL database with one synthetic user and one finite synthetic
Key. A lane is an internal partition of the same available paid balance and Key
budget, not a new account, a separate credit line, or a way to bypass API limits.
The separately tagged P4 gateway fixture below does not enable a production
billing mode. Existing configurations and production data are out of scope.

- Compare one lane with 4, 8 and 16 using the same funds, finite Key budget,
  exact arrivals, pool size, and storage medium. Record the effective total
  budget and assert per-request identity, not just equal sums. Measure
  reservation, dispatch, immutable usage recording and final debit, plus
  before/after close reconciliation. Count an unsent benchmark arrival as a
  failure to achieve the tier. Keep kernel timings separate from gateway SSE
  time-to-first-content and database timings from runs on a different medium.
- Transfer paid balance and finite Key budget atomically. Capacity transferred
  into a lane is unavailable to ordinary spending and is not yet usage. For
  each dimension reconcile original funding to available + outstanding holds
  + committed consumption. On close, reconcile returned cash and Key quota,
  the exact finalized request IDs and unique actuals. No use of bonus or
  reward balances or negative/over-limit arithmetic. Never accept a changed
  owner, epoch or frozen actual on replay; cap increases, if allowed, must
  acquire the extra hold on both budgets before dispatch.
- A committed hold does not mean the upstream was called. Only a newly
  committed dispatch transition can authorize the first send. If the outcome
  of that transition or actual upstream usage is ambiguous, preserve owner
  and exposure; do not issue a second send or free the hold from age alone.
  Admit refund only when no dispatch can occur, or with separately defined and
  verified no-delivery evidence. Block close and downgrade on unresolved
  exposure; disabling a flag is not an unwind.

The Go files require the explicit `enterprise_lane_lab` build tag. For
the model regression suite, point `TEST_MODEL_POSTGRES_DSN` and
`TEST_AUTH_REDIS_DSN` at disposable, dedicated local test processes and
run `go test -tags 'api_only enterprise_lane_lab' ./model -run '^TestEnterpriseLane' -count=1 -v`.
The tests create synthetic balances and perform schema and cache mutations.
A skip is not a PostgreSQL or Redis pass. Normal builds omit the tagged files;
neither environment variables nor the ordinary migration can activate them.

Do not promote this beyond the isolated fixture until the following are
verified together:

1. **Admission:** Explicit opt-in for eligible model and protocol, original
   IP, account, model and group restrictions, and atomic Key disable/exclusion
   across replicas and cached auth. Escrowed Key capacity must not be treated as
   spendable legacy quota. The same Key must not spend via unsupported paths.
2. **Accounting:** Reserve a true upper bound from the final wire payload and
   frozen price, not the prompt-only legacy estimate. Capture definitive
   provider usage durably before the client sees a successful terminal event.
   Same-owner replay must be idempotent; an over-cap charge, missing usage or
   ambiguous delivery cannot silently become zero or a refund.
3. **Visibility and rollback:** Verify per-request consume logs, user and
   channel reporting, dashboard consistency and lost-ACK recovery. Reconcile
   escrow before admitting an old executable. Old versions cannot interpret
   new financial states; unchanged readiness alone is not a safety signal.
4. **End-to-end performance:** Repeat the same finite-Key test with the
   gateway, local SSE fixture and normal log/counter projections. The
   experimental storage-path result cannot be used as proof of relay RPM or
   production readiness.

## P4: reproduce the isolated gateway fixture

- Build from a clean pinned revision with `go build -tags 'api_only enterprise_lane_lab'`.
  Record the executable SHA-256 and harness hash. Ordinary builds omit lab
  accounting; even tagged serving needs `ENTERPRISE_LANE_LAB_ENABLED=true` in
  the launch environment, not `.env` or the managed migration. Set
  `HTTP_BIND_ADDRESS=127.0.0.1` and `TRUSTED_PROXIES=none`. Use a disposable
  loopback PostgreSQL `enterprise_lane_*` database, optional loopback Redis,
  `LogConsumeEnabled=true`, and leave `LOG_SQL_DSN` unset.
- Use one dedicated, never-spent finite synthetic Key and one designated native
  OpenAI-type channel on a direct loopback HTTP origin, without proxies,
  overrides, plugins, conversion, or external credentials. Supply the manifest
  via `ENTERPRISE_LANE_LAB_TOKEN_ID`, `ENTERPRISE_LANE_LAB_CHANNEL_ID`,
  `ENTERPRISE_LANE_LAB_MODEL=gpt-4o`, `ENTERPRISE_LANE_LAB_COUNT`,
  `ENTERPRISE_LANE_LAB_PAID_PER_LANE` and `ENTERPRISE_LANE_LAB_TOKEN_PER_LANE`.
  Only the allowlisted `POST /v1/chat/completions` text SSE shape is supported;
  send an explicit bounded `max_tokens` and request `include_usage`. Unsupported
  routes for the bound Key must not fall through to legacy billing.
- With the isolated `relay_p4.py` fixture bundle, run `start --phase <phase>`
  with the clean tagged `--binary`, `--revision` and `--binary-sha256`. For any
  phase after `smoke`, pass the prior phase's passing `summary.json` via
  `--prerequisite`. The ladder is `smoke` (500 RPM / 10 s), `5k` (5,000 / 180 s),
  `10k30` (10,000 / 30 min), then `mixed2h` (four 30-min blocks with increasing
  long-stream share). In the mixed fixture the long profile's nominal 250 ms
  first-content delay plus 17.5 s tail stays below the 20 s upstream deadline;
  record observed lifetimes too. These are test plans, not achieved rates. Pin
  storage medium, pool settings, fixture profiles and accepted request/stream
  caps before running. Disclose missed offers, content TTFT, fixture-adjusted
  gateway TTFT, terminal SSE, and per-request reconciliation. Do not infer
  production or provider capacity from this loopback workload.
- Only verified final SSE usage is frozen with the request's actual charge.
  Client success terminal frames follow acknowledged settlement. The off-path
  projector uses one primary PostgreSQL transaction for each batch's unique
  per-request projection, consume log, grouped user/channel counters and
  pending-marker clear. Split log databases and broader legacy reporting such
  as `quota_data` exports are outside this parity claim. Require zero durable
  pending projections plus exact request IDs, charge, prompt/completion tokens,
  logs, counters and wallet/Key conservation before reporting a full pass.

### Separate cold stop and drain drill

After the timed load, test cold lifecycle in a separate disposable fixture; do
not run a heavy rollover/drain benchmark concurrently with timed ingress.
Retain terminal receipt history in the stopped lane and older epochs, and keep
a sibling lane active for the contention check. Test time under the parent
wallet lock and sibling request impact as well as exact conservation. The
`(lane_id, epoch)` index excludes unrelated history; exact drain still checks
the stopped lane's own retained receipts, so this is not a constant-time close.

The tagged, one-shot `ENTERPRISE_LANE_LAB_STOP=true` launch with the same
manifest atomically fences new holds, extensions and dispatch claims across
the lane bundle. Ordinary Key/user revocation is checked at ingress and again
before dispatch, but those reads are not the atomic stop barrier. A previously
claimed dispatch is not canceled by Stop; do not refund or resend it based on
age, a missing acknowledgment, or a disabled flag. Stop is not Drain: only a
stopped lane with no unresolved exposure and matching financial receipts can
return unused paid/Key escrow to the original owner. Finish reporting projection
separately and verify no unresolved or unprojected owners before considering an
older executable. Never start an ordinary or prior gateway on the lab database
while the escrow is live.
