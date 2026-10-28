# Request finalization hotfix: evidence and recovery

## Scope and compatibility

This change starts from production commit `6832178e46dccf02ce6f3ff4fa055c4264205e35`. It changes new request finalization, not prices, multipliers, account eligibility, or historical balances. No historical row is rewritten or deleted. No new table/column/migration or background settlement queue is introduced. `settlement_review_v2` fits the existing varchar(24) `state` column.

Upgrade all request-serving processes together. Older binaries do not understand this state and fail closed; older read-only reports omit it. Do not downgrade and then treat a missing report item as a reconciled request.

## Financial meaning

- A trusted request can hold zero and still owe its full observed usage. Known usage follows the existing pricing and funding calculation; neither HTTP 200 nor cancellation proves a zero charge.
- For a known unused request, the existing refund owner closes even a zero hold. An unmeasured upstream interruption instead keeps `delivery_unknown_v2` and requires review. No elapsed-time threshold authorizes a refund.
- Before changing money, settlement freezes its already calculated `actual_quota` under the original `request_id`, identity and held receipt. `settlement_review_v2` means **not confirmed settled**, not a successful charge. The original three-wallet split, token hold and preconsume remain intact.
- The money transaction still atomically updates wallet/subscription, original token and `settled`. Replays must match the frozen actual and original identity. Both `Refund` and `Reserve` refuse to replace an observed settlement intent.
- Only typed transient database errors receive up to three in-request attempts under the existing five-second operation budget. Metadata uses that same budget where available; after expiry there is at most one one-second evidence-recording tail, plus the existing bounded cache cleanup. Insufficient balance is immediately review-required, with no scheduled automatic retry (`next_retry_at = 0`). An explicit same-ID retry retains original arithmetic and idempotency keys.
- If the intent write or its acknowledgement fails, an in-memory guard prevents fallback refund. This is **not** proof that the actual was durably saved. `billing_settlement_evidence_unconfirmed` logs identify this gap for manual reconciliation; a crash during a database outage may leave an unchanged historical-looking `held_v2` row.

## Read-only reconciliation (no production execution performed)

Use the existing `ReadFinancialReviewReport` function or the SELECT-only queries below with read-only credentials and a consistent snapshot. It now includes `settlement_requires_review`, the frozen actual, original hold, signed settlement delta and an allowlisted failure code. Its pagination scans rows, not findings: continue while `has_more`, even when `items` is empty.

For PostgreSQL, the following statements are review queries only. Execute in a read-only transaction on an operator-authorized snapshot; do not paste database credentials into chat or logs.

```sql
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '10s';
SELECT state, source, count(*) AS requests,
       sum(source_held) AS source_held, sum(token_held) AS token_held,
       sum(CASE WHEN state = 'settlement_review_v2' THEN actual_quota ELSE 0 END) AS known_actual
FROM billing_finalizations
WHERE state IN ('held_v2', 'delivery_unknown_v2', 'settlement_review_v2', 'refund_review_v2')
GROUP BY state, source ORDER BY state, source;

SELECT request_id, user_id, source, state, source_held, token_held,
       pre_consumed, paid, bonus, reward,
       CASE WHEN state = 'settlement_review_v2' THEN actual_quota END AS known_actual,
       CASE WHEN state = 'settlement_review_v2' THEN actual_quota - pre_consumed END AS settlement_delta,
       attempts, first_error_code, last_error_code, last_error_class,
       first_failed_at, last_attempt_at, next_retry_at, created_at, updated_at
FROM billing_finalizations
WHERE state IN ('held_v2', 'delivery_unknown_v2', 'settlement_review_v2', 'refund_review_v2')
ORDER BY request_id LIMIT 200;
-- For subsequent pages use WHERE request_id > :last_request_id as well.
ROLLBACK;
```

Review each request against its original upstream usage/completion evidence, consume/error logs and `balance_ledger` / `balance_ledger_operations` using the same request ID. Do not export token keys/fingerprints or provider request content. Existing `held_v2.actual_quota = 0` is **unknown**, not verified free usage. The reported 1,322 production rows have not been independently inspected by this change.

## Disposition and restart

A new process can enumerate review rows without a `BillingSession`; the regression suite proves this using a separate OS process and PostgreSQL `READ ONLY`. It deliberately does not auto-charge them. Current wallet debit order/bonus eligibility and current pricing may differ from the original request. Without trustworthy original funding policy and actual usage, disposition remains **manual review**.

After an operator verifies the original policy, original identity, actual amount and account evidence, separately authorize an explicit same-ID settlement through the original transactional billing API. Replenishing an account alone does not authorize or schedule collection. Do not generate a new request ID, override `actual_quota`, change `state` by SQL, grant a replacement token credit, blanket-refund, bulk-delete, or use a five-minute age as fee evidence. There is no new public/admin mutation endpoint in this patch.

## Verification

Use the dedicated PostgreSQL 18.6 / Redis 8.8.0 fixture and select `TestBillingFinalizationSessionDatabaseMatrix/postgres/production_finalization`. This includes trusted zero holds, measured partial output across three wallets/token/ledger, insufficient balance and explicit replay, separate-process review, both intent and money COMMIT rollback/lost ACK, intent write failure, bounded transient retry, concurrent settlement/refund, and real Redis invalidation. Other fault/compatibility tests are selected separately. The service test executable retains its old SQLite bootstrap; these selected money scenarios execute only on PostgreSQL. No MySQL or production database verification is claimed.

### Recovered broad regression evidence

The earlier `billing-hotfix-related-20261024` run has a terminal **exit 1**, not an unknown outcome; its original logs and stopped fixture data remain preserved. Three cases failed for two assertion-contract errors:

- `production_finalization/concurrent_replay_and_refund` treated a nil error as refund authority even when `RequestBillingRefund` had returned the already-settled owner. That existing API returns the winning terminal owner without changing money. The repaired test requires either rejection while settlement is pending or a settled owner with the original actual, and independently requires `ApplyBillingRefund` to refuse the request. Exact final three-wallet, token and ledger balances remain asserted.
- `unowned_hold/settle_zero` and `unowned_hold/settle_more` expected `NeedsRefund` after observed settlement had failed with `owner_missing`. Review-required accounting is not refund eligibility. The tests now additionally attempt a late refund, reject reserve and a changed actual, and prove a same-actual replay still returns `owner_missing`. All original balance, token, ledger, absent-owner and absent-task assertions remain.

Only tests changed in commit `7d79d4985e4c10ad65065891c5d7f4312df2f118` (tree `10750c17687d045b12169e0909518e8cef3adf88`); no production logic was relaxed to make those assertions pass. The following command completed on that clean commit:

```bash
source ../production-upgrade-env.sh
python ../run-production-upgrade-targeted.py "$PWD" billing-hotfix-recovered-related-20261024 ./service --run '^TestBillingFinalizationSessionDatabaseMatrix$/^postgres$/^(production_finalization|financial_lost_ack|financial_contention|lifecycle_trusted_zero|lifecycle_unknown_delivery|commit_boundaries|settlement_deadline_driver_pool|settlement_deadline_row_lock|settlement_deadline_commit_evidence|official_refund_before_lost_ack_readback|settlement_deadline_redis|settlement_cancelled_client|zero_delta_replacement|probe_failure|realtime_increment_terminal|realtime_lifecycle|realtime_trusted|failed_settle_reserve_refund|legacy_final_more_less|real_session_.*|unowned_hold)$'
```

Result: **exit 0**, 84 real child leaf cases passed, including all 18 production-finalization cases; **0 skipped and 0 failed**. Parent fixture proxy nodes are not counted as a second business execution. The runner recorded Go **1.27.3**, PostgreSQL **18.6**, Redis **8.8.0**, identical before/after source snapshots with an empty patch, and owned PostgreSQL/Redis cleanup both exiting 0. Evidence is retained under `evidence/production-media-upgrade/native/billing-hotfix-recovered-related-20261024/`, including `tests.process.json`, `result.json`, `source-before.json`, `source-after.json`, `cleanup.json` and `verified-summary.json`.

This is a focused PostgreSQL regression result, not a full-suite, race, build, production, MySQL, SQLite-business-matrix or Go 1.27.2 verification. No archived fixture directory or cache was deleted to obtain it. No deployment, historical correction or automatic collection was performed.
