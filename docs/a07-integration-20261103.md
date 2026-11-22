# A07 integration, 2026-11-03

Status: source integration and selected non-database checks are complete; PostgreSQL runtime and production-version upgrade acceptance are blocked. The owner subsequently authorized committing and pushing this work-in-progress branch with these limitations disclosed. This is not a release approval: do not deploy or describe the branch as accepted before the remaining checks pass.

The imported A07_ACCEPTANCE, A07_PROGRESS and A07_DELIVERY files describe the source branch's earlier work. Their test and deployment statements are not evidence for this integrated branch.

## Fixed baselines

- Production's public `/api/status` reports `v1.0.0-rc.47.chuanapi.noagent.3.routing.1+g7360da3f0080`. The matching available source commit is `7360da3f00800692b55f1519ec48f502c63e0c05`. A public version string is not a verification of the deployed image contents, database contents, or server settings.
- Current custom workline: `loufilk/new-api`, `dot/api-key-smart-routing-upstream-20261102`, `f609ce44f67895a6ac54fc2596844c52805bed2b`.
- Official upstream: `QuantumNous/new-api`, `main`, `ba5f7367797566175a6cd97a748d430af3602a01`.
- A07 source: `jasonhuang1236-commits/wanchuan-agent-a07`, `bench`, `2984b0345eb685b543a57c33ab6057224bd77091`.
- Integration branch: `dot/a07-upstream-integration-20261103`. Upstream-only local merge: `b661da878f696db55cb28f1a4946c4ea95506136`. The A07 integration is being committed as work in progress at the owner’s explicit request; PostgreSQL runtime and upgrade acceptance remain pending.

Production source to the current custom workline is one commit, affecting API-key manual groups and model inventory. There are no changes under `model/` or `modules/chuanapi/` between these two fixed commits. The upgrade baseline must nevertheless be the production source, not a fresh current-version schema.

## Semantic integration

- Retain smart-key routing, Google sign-in, account pools, private support attachments, ordinary administrator redemption management, wallet source accounting and current relay fixes.
- Expose only the simplified A07 routes. Do not restore retired procurement, campaign, legacy budget, or transaction-preparation APIs.
- Retain only model/helper dependencies needed for A07 and historical accounting checks. Startup does not reconstruct retired budgets or automatically promote historical role/profile records.
- Preserve the official Gemini embedding `usageMetadata` priority. Missing or non-positive metadata falls back to the current custom `OriginModelName` billing basis. Both native and OpenAI-compatible embedding handlers are covered by a focused non-database test.
- `PaymentReceipt` and `AgentPaymentReceipt` share the existing physical table and uniqueness constraints. Ordinary and agent payments cannot claim separate identities for the same provider payment.
- New top-ups enqueue A07 referral events. Historical `referral_reward_outboxes/inboxes`, their immutable eligibility snapshots, and the ordinary worker remain. Pending historical events block first migration and must drain on the old version before cutover. No rows are discarded or silently marked done.
- Manual refund registration requires completed external-refund evidence, then invalidates the unused code and accounts for the refund. Manual withdrawal registration requires completed external-payment evidence, then deducts the selected available balance. No automatic refund or payout is introduced.
- New A07 accounts are paused. Reading an account or capabilities cannot activate it. Sales and enhanced referral benefits require an explicitly activated A07 account, not an archived profile alone.
- The first A07 data migration atomically transfers eligible `aff_quota` into paid wallet quota, clears that source balance, and stores matching totals. The migration reuses the existing complete retirement-evidence checks for archived legacy funds; outstanding or unproven obligations remain fail-closed. It must not be tried against production as a smoke test.
- A bounded worker releases expired, unused direct/credit code reservations back to their original sources and disables the codes. Payment codes and legacy codes are excluded.
- Completed A07 migrations distinguish valid live A07 codes from retired legacy redemption markers so ordinary subsequent starts do not reject the new system's own reservations.

## Checks performed in this integration

Tools: official Go 1.27.3 and Bun 1.4.4, installed in task-local directories. PostgreSQL 18.6 was built and initialized locally, but no database service could start.

Passed:

- Frontend frozen-lockfile installation, route generation, typecheck, changed-file lint and formatting, production build.
- 18 selected ordinary frontend test files: 99 tests.
- Root `go build ./...`.
- Independent `GOWORK=off go build ./...` in `relaykit/` and `tokenkit/`.
- Pure test-binary compilation with `go test -c` for controller, middleware, router, chuanapi, agent module, wallet module, model, service and Gemini packages. This does not execute `TestMain` or tests.
- Focused Gemini embedding metadata/fallback test; selected ChuanAPI registration tests; A07 route-registration coverage test. These selected tests do not open databases.
- Exact production-source API-only binary build. This is a baseline build, not a successful service start.
- Git whitespace/conflict-marker inspection.

A process deviation occurred before the PostgreSQL-only constraint reached one build worker: `go test ./model -run '^$'` invoked the package's default SQLite `TestMain`. It was stopped as a validation route and is not counted as production-aligned evidence. Subsequent model checks used compile-only mode. No MySQL matrix or previously blocked security reproduction was executed.

## Blocked acceptance and upgrade plan

PostgreSQL startup failed while creating its Unix-domain socket with `Operation not permitted`, and the server shut down. No alternate socket, TCP, port, or other route was tried. No test database was created. The environment restriction must be resolved through an authorized execution environment before continuing.

Remaining acceptance must use PostgreSQL 18.6 only:

1. Start the exact `7360da3f00800692b55f1519ec48f502c63e0c05` application against a disposable PostgreSQL 18.6 database, with synthetic data and no production credentials or payment callbacks. Seed ordinary wallets, immutable payment receipts, invitation balances, used/unused ordinary codes and representative completed history using that version's schema.
2. Verify the old version's ordinary outbox is drained. Preserve a separate pending-event fixture to prove migration rejects it without deleting events or crediting balances.
3. Stop all old-version writers. Save a disposable database snapshot. Start the integrated version against that same database, not a newly created target schema.
4. Compare balances and accounting provenance, receipt identities, old code state, routing data and referral totals. Verify A07 migration's debit/credit totals and single completion receipt.
5. Exercise normal A07 code issue/redeem/cancel/expiry, manual-refund and manual-withdrawal recording, and ordinary top-up/referral continuity with synthetic data. No actual provider funds are sent.
6. Stop and start the integrated service again after creating valid live A07 codes and events. Check no second migration transfer, no repeated referral credit, no startup rejection of valid A07 rows, and graceful worker shutdown.
7. Separately validate a fresh PostgreSQL database and the configured log-database arrangement. The generic module fixture in this branch does not replace a real old-binary-to-new-binary upgrade test.
8. Review results and record acceptance on the work-in-progress branch before considering release. The owner authorized pushing the branch before these checks finish; deployment remains outside this task.

Do not use broad `go test ./...` as a shortcut: existing packages and fixtures can open SQLite unless explicitly configured. Previously blocked security reproductions remain excluded. No claim of full OWASP ASVS compliance, production compatibility or release readiness is made.
