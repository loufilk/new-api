# Offline wallet/CPU stage comparison

`wallet_cpu_report.py baseline.json candidate.json` is an offline report, not
telemetry collection, a load driver, a pay-path change, or a release gate. Run
it only on sanitized evidence from matching isolated trials. The server does
not load this script, allocate samples for it, or acquire new locks when it is
absent. No trace/log producer is altered and no provider or DB is contacted.

## Input and interpretation

Supply JSON objects with `version:1` and:

- `cohort`: `window_ms`, `scheduled`, `wallets`, `keys`, `replicas`,
  `wallet_sample_permille`, `relay_sample_permille`, and `shape_sha256` (hash
  a canonical, **synthetic** request-mix manifest; not a credential or actual
  request). Both runs should otherwise use controlled hardware/builds and one
  explicitly documented treatment. The fingerprint is not echoed. Counts
  `requests.issued`, `requests.resolved` and `requests.failed` include rejected
  and unsuccessful requests; do not omit scheduling gaps or open streams.
- `wallet`: array of the JSON objects already produced by
  `WALLET_LOCK_TIMING_SAMPLE_PERMILLE` after stripping the message prefix. Split
  reservation, settlement, recovery, and acknowledged/nonacknowledged
  transactions. `pool_and_begin_us` includes checkout/setup **and BEGIN**;
  `wallet_lock_query_us` is full row-probe wall time, not pure lock wait;
  `after_lock_query_to_tx_return_us` extends through the client commit/rollback
  return, not the server's exact lock-release moment.
- `relay`: array of sanitized existing `relay_timing` JSON objects. Only
  `status`, `relay_api_error`, and optional `billing_prepare_ms` are read.
  Billing preparation is a blended wall phase, including estimation, quote,
  and DB precharge. An opened HTTP 200 stream can still fail; keep actual
  stream outcomes separately. No header, first event or first usable delta
  is measured as client-visible text here. Report client-observed visible
  text TTFT separately and preserve missing/no-text/error outcomes.
- Optional `pools`: each independently collected `main` / `log` pool has
  `before` and `after` snapshots from the **same** process and actual
  `database/sql.DB.Stats()` handle, with `wait_count`, `wait_duration_ns`,
  `max_open_connections`. Do not pass `log` if it is the same handle as main.
  These are process-wide counter deltas, not per-request waits. Wait duration is
  aggregate and can include overlap; a wait may complete inside the window
  after beginning before it. Capture each replica independently; don't
  average replica percentiles. A reset or pool limit edit invalidates the run.
- Optional `pg_wait_samples`: `successful`, `failed`, `wallet_blocked`,
  `wallet_probe_not_blocked`, `other_or_unknown`; provide one exclusive
  successful label per poll for the intended synthetic target. Confirm a
  blocked probe with PostgreSQL `wait_event_type='Lock'` and blocker PID
  evidence. `wallet_probe_not_blocked` means observed in the verified probe
  without confirmed blocking; absent target/unattributable is unknown.
  Export counts, not SQL, identifiers or `pg_stat_activity` rows. Sample hits
  aren't milliseconds. A row may block between samples even with zero hits.
- Optional `cpu`: `source` (`process_pprof_flat` for a window-matched
  sanitized Go CPU profile; `fixture_process_cpu` for this test's
  process-CPU smoke), `coverage_ms`, `total_cpu_us`, `tokenizer_flat_us` and
  `json_flat_us`. Attribute each CPU sample to a **single leaf class**.
  If you don't have comparable process profiles omit this block; never
  derive these values from relay wall clock, cumulative pprof columns, or
  wall minus SQL. Profile totals may span concurrent requests and goroutines;
  they are not one request's CPU time. Keep profiling endpoint disabled on
  public/production traffic.

The output deliberately doesn't calculate stage residuals, subtract
unpaired percentiles, or pronounce a bottleneck. A missing phase is `n:0`
with null percentiles, not zero latency. A percentile is suppressed below
one nominal tail observation, even though this is not a confidence claim.
Cautions retain mismatched cohorts and missing evidence instead of silently
matching by name. Longer row-probe wall with confirmed lock observations
is evidence to investigate contention; longer row wall by itself is not.
A simultaneous CPU increase can be independent; do not infer cause from
one smoke.

For the next instrumented run, the minimum missing gateway timings are:
1. Sample separate request decode/validation **wall** around
   `helper.GetAndValidateRequest` in `controller/relay.go`.
2. Sample token metadata/estimation **wall** around `GetTokenCountMeta` and
   `service.EstimateRequestToken` in `relay/request_billing.go`; capture
   process CPU attribution independently when needed. Keep the existing
   billing prepare wall and wallet timings, and do not change token counts,
   dispatch, consumption or logging reconciliation.

## Local validation

From `ops/perf`: `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -v test_wallet_cpu_report`.
For the optional tiny SQL smoke use a dedicated, empty PostgreSQL 18.6
database named `wallet_cpu_smoke` on 127.0.0.1, no other workload, and set
`WALLET_CPU_TEST_PG_DSN='postgres://agent@127.0.0.1:<local-port>/wallet_cpu_smoke?sslmode=disable'`
with matching local `psql` on PATH. The exact lowercase URI form, explicit TCP
port, loopback IPv4, synthetic user and database are required. The harness
ignores inherited libpq connection defaults and verifies its effective endpoint
before creating a table. It creates and drops one synthetic table,
checks one observed blocker, and times a small offline JSON loop with the
process CPU clock. Test fixtures intentionally provide synthetic pool
counters; they do not attest actual Go pool wait or New API throughput.
