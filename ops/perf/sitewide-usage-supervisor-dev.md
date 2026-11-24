# Tagged usage projection supervision

**Development only.** This component neither chooses customers nor starts on its own. The ordinary binary, its startup, its DDL and its routes do not change. Requires the existing Stage-7 transactional outbox, native measured settlement and disposable, isolated PostgreSQL schemas.

## Explicit caller

1. An external test harness creates its disposable main/log schemas and installs the **existing** synthetic schemas. The log installer returns its persisted random destination ID. From trusted setup record both full PG endpoints: loopback server address/port, database name+OID, current schema name+OID. Use the result of that setup, not an unauthenticated request or self-discovery by the worker. Main and log can be the same target or separate databases. The worker runs no DDL, discovers no destination and never selects a fallback.
2. Construct `NewLaneUsageSupervisorDev(mainDB, logDB, mainScope, logScope, destinationID, batchSize)`. Both identities are checked in each relevant transaction. Batch size is 1..32. Redis serving and ordinary non-dev schemas are rejected. Treat pool ownership, clones/restores and cross-process release coordination as external responsibilities. This binding is not a production write permit.
3. Either run `w.Run(ctx, interval, report)` (synchronous, one bounded page per tick, 100 ms..1 min, no goroutine created by the component), or explicitly call `w.Window(ctx, maxPages)` (1..8 pages, total 10s deadline). The process owns cancellation/join and closing its pools. Only one invocation per instance is allowed; independent instances use the existing durable lease + exact receipts. Report must be local and nonblocking. Invalid scope/evidence stops Run; transient errors back off, and terminal conflicts stay visible across later ticks.
4. A result includes candidate IDs *counted*, acknowledgements reported by successful main commits, pages, elapsed time and indexed existence flags for due, deferred/leased and conflict states. Every page fetches at most batchSize IDs. Acknowledged can be lower than actual commits on uncertain ACK; never use this counter to bill. Lost destination/main ACKs must reuse the same destination and receipts. Respect the existing database-clock retry lease. A locked selected row may defer progress to another tick.
5. All four conditions must hold before calling the known queue idle: no error, QueueObserved, and all three flags false. It is a *single main snapshot*, not a producer fence, full cross-store audit or completion proof for deleted outbox rows. A conflict is not a success even on a later zero-row, error-free pass. Stop producers, join workers, and reconcile both stores before making any cutover claim.

## Reporting boundary

- One sanitize-and-attribute consume log, keyed on the original request, plus its destination receipt goes to the configured log database first. Main user used quota/request count, channel used quota, and outbox acknowledgement then commit together. Restore never fabricates another log from a missing destination log or receipt.
- Ordinary Key-log lookup by original token ID and ordinary user log lookup by user ID can see the split-database record when `LOG_DB` is that destination. User reads continue to remove admin-only fields. Token/username string-filter parity is not claimed; private names are not copied into receipts. The `quota_data`/hourly export, no-log mode, clickhouse target, cache coherency and all other display endpoints are not certified.
- No wallet/Key balance or `tokens.used_quota` write occurs. Until a real watermark exists, the original finite Key's logical used value is raw used + held + *all* slot spend (one supported generation). It must not be added to a simultaneously updated raw Key or silently reset at retirement. Any future raw-Key/report materializer needs independent evidence keyed by original identity, version/slot/epoch and atomic exactly-once advancement. No default enabled route may rely on this dev worker to assert those guarantees.
- A destination receipt without its matching log, a foreign consume log or disagreeing immutable evidence quarantines rather than backfilling. A terminal settlement without an outbox is outside worker discovery. Cold/offline reconciliation and an operational remediation procedure are separate prerequisites.

## Focused run

With two different disposable loopback PostgreSQL databases supplied in
`TEST_MODEL_POSTGRES_DSN` and `TEST_LANE_USAGE_LOG_POSTGRES_DSN`:

```sh
go test -p 1 -tags lane_billing_integration_dev ./model \
  -run '^(TestLaneUsageSupervisor|TestLaneBillingUsageOutbox)' -count=1 -timeout=150s -v
```

Require no skipped cases. These are correctness tests, not a throughput benchmark.
