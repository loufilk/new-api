# Isolated 32-lane gateway run profile

This is a disposable loopback capacity diagnostic, not a deployment profile,
production instruction, or measured performance result. Build clean source
with `api_only enterprise_lane_lab`; enable the lab explicitly. Ordinary builds
and ordinary tagged launches are unchanged. Use one synthetic account, one
never-spent finite synthetic Key, and one native `gpt-4o` SSE fixture on loopback
with no external upstream, production data, or trusted proxies.

| Setting | Isolated value |
| --- | ---: |
| `ENTERPRISE_LANE_LAB_COUNT` | 32 |
| `ENTERPRISE_LANE_LAB_ADMISSION_GLOBAL` / `ENTERPRISE_LANE_LAB_ADMISSION_PER_LANE` | 96 / 8 |
| `ENTERPRISE_LANE_LAB_ADMISSION_WAIT_MS` | 35 |
| `ENTERPRISE_LANE_LAB_ADMISSION_QUEUE_GLOBAL` / `ENTERPRISE_LANE_LAB_ADMISSION_QUEUE_PER_LANE` | 32 / 2 |
| `SQL_MAX_OPEN_CONNS` / `SQL_MAX_IDLE_CONNS` | 120 / 24 |
| PostgreSQL `max_connections` | 150 |
| `ENTERPRISE_LANE_LAB_PAID_PER_LANE` / `ENTERPRISE_LANE_LAB_TOKEN_PER_LANE` | 18,750,000 each |

Use PostgreSQL 18.6 with `fsync`, `synchronous_commit`, and `full_page_writes`
on, together with the pinned Redis 8.8 fixture. Keep consume logging enabled
and leave `LOG_SQL_DSN` unset: logging and projection share the primary
database. Read back actual PG durability, service pool and server capacity;
allow for observer, background and reserved connections. The pool limit must
be strictly above 96. The extra pool capacity is headroom, not a reservation
of SQL connections for settlement. Keep existing slow-SQL warnings unchanged
and optional request timing sampling off for the matched diagnostic.

Start both synthetic balances at 800,000,000 quota units. Atomic funding
transfers 600,000,000 paid units and 600,000,000 finite-Key units into the 32
lanes, leaving 200,000,000 of each outside the bundle. Do not change lane count
on any funded manifest; create a fresh disposable fixture. This is not online
resharding. Never fall back to unpartitioned spending for the bound Key,
credit unknown upstream outcomes, or emit terminal success before the debit
commits.

Use the exact commit, executable SHA-256, fixture and driver hashes, and
qualify the current host with the driver before any gateway run. Run the
strict serial diagnostic only: 100-request smoke, 5,000 RPM for 180 seconds
(15,000 requests), then 10,000 RPM for 240 seconds (40,000 requests). Stop at
any failed stage: no missing arrivals, pacing misses above 250 ms, failed
responses, or incomplete streams; gateway-added first-content P99 must be at
most 1 s, and fast-stream end-to-end P99 at most 2 s. Reconcile every admitted
ID to exactly one terminal billing owner and matching charge, usage/log
projection, Key and wallet totals, and user/channel counters; require zero
pending or unresolved owners after shutdown. Report a pacing failure as such
even if admitted requests reconcile. Compared with the 16-lane setup, lane
count, active limits and database connection budgets all change, so this
cannot isolate which factor improves performance. No 30-minute or production
qualification follows from a short pass.
