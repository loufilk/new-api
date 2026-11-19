# CORE: im.13 → im.18 and no-restore rollback

Production baseline: im.13 `5c370275b838d37d0ea4480810a2ebc2d7dd49ca`. The im.18 application binary under test is from `55156418ee436ad1ebba93624f32c30d13480653`. Build the candidate from the reviewed maintenance-branch SHA that contains this runbook and the Caddy fix. Preserve **PostgreSQL 18.6 and Redis 8.8.0**, both the primary and log DSNs, Redis state and the existing persistent mounts. Waffo is out of scope. These instructions are not a cutover approval.

**Never start the stock im.13 master against a database on which im.18 has run.** The old primary and log startup migrations are disabled in the dedicated rollback source branch `fix/im13-rollback-no-migrate-20261119`, not by changing im.18. On this image startup requires `NODE_TYPE=master` and `STARTUP_MIGRATIONS=verify-only`. Keep one rollback master only; stock im.13 replicas are not a workaround. Schema checks fail closed if required state is absent. A new install must use an ordinary installer, not this image.

## Build and pin the two Linux images (qualified Linux Docker runner)

The tags below are **local build targets**, not published registry tags or digests. For CORE, pin the verified Linux platform manifest digest and check its source revision; do not copy a tag or binary SHA into an OCI digest field. Replace the two SHA variables with the exact reviewed revisions, not a moving branch head. Build from clean checked-out sources with their original Dockerfiles; pass no runtime credentials to the build.

~~~bash
# In the im.18 maintenance checkout:
CANDIDATE_SHA=$(git rev-parse HEAD)
test -z "$(git status --porcelain)"
docker build --platform linux/amd64 \
  --build-arg SOURCE_REVISION="$CANDIDATE_SHA" \
  --build-arg RELEASE_VERSION="$(cat VERSION)" \
  -t "chuanapi/core-im18:$CANDIDATE_SHA" .
CANDIDATE_ID=$(docker image inspect -f '{{.Id}}' "chuanapi/core-im18:$CANDIDATE_SHA")

# In the checked-out im.13 rollback-source branch:
ROLLBACK_SHA=$(git rev-parse HEAD)
test "$(git rev-parse HEAD^)" = 5c370275b838d37d0ea4480810a2ebc2d7dd49ca
test -z "$(git status --porcelain)"
docker build --platform linux/amd64 \
  --build-arg SOURCE_REVISION="$ROLLBACK_SHA" \
  --build-arg RELEASE_VERSION="$(cat VERSION)" \
  -t "chuanapi/core-im13-rollback:$ROLLBACK_SHA" .
ROLLBACK_ID=$(docker image inspect -f '{{.Id}}' "chuanapi/core-im13-rollback:$ROLLBACK_SHA")
docker image inspect -f '{{.Os}}/{{.Architecture}} {{index .Config.Labels "org.opencontainers.image.revision"}} {{index .Config.Labels "io.chuanapi.release.mode"}}' "$CANDIDATE_ID" "$ROLLBACK_ID"
~~~

The second line of inspection must be `linux/amd64 <ROLLBACK_SHA> im13-schema-preserving-rollback`. Verify the server architecture before assuming amd64. Run both on the same disposable PG 18.6 and Redis 8.8.0 instances with synthetic data; do not point image CI at CORE. In the approved release channel, record the immutable registry digests of these exact builds and pull both before the window. No registry publication is implied by this runbook.

## Before the window (operator)

1. Verify the actual Compose project, API service, existing ports, mounts, main/log DBs, and Redis from the owner’s runbook. Use the same named volumes or binds and secrets. Do not rely on writable container layers or anonymous volumes. Preserve credentials, session/crypto secrets, Passkey RP/Origin, Redis revocation state and audit evidence. Capture both PG and log backups (and Redis recovery state as applicable); **backups are disaster recovery only, not this no-loss rollback**.
2. Ensure all existing operator/boot automation consistently includes the persistent override (`core-newapi.pin.yml` below). Do not subsequently run generic `docker compose up` without the pin file, or restart a stock im.13 container. Ensure there is no other orchestrator, supervisor or replica able to restart an old writer during the window. Keep traffic fenced until checks pass. Use existing maintenance controls to drain SSE/WS; stop all other workers and gateway writers and wait for leases to expire or be released, not SQL manipulation of ownership.
3. For a rollback, first stop admitting new invoices and fee checkouts (CORE defaults off today; verify). Keep the old signer/notification configuration and reconcile in-flight Epay CNY fee callbacks, applications and refunds while im.18 is available: im.13 has no invoice callback endpoint even though it preserves verified receipts and ledger history. Do not rename, void, delete, or replay receipts to pass startup. If pending callbacks cannot be resolved safely, do not cut back yet.
4. Before stopping im.18, verify a successful final statistics flush after draining, keep the flush cursor rows, and capture a release barrier timestamp plus post-cutover ledger/report fingerprints. No process can preserve a report bucket that was never flushed across a crash. If there is an abnormal drain, hold ingress and investigate rather than toggling the recovery flag blindly.
5. With the authorized restricted backup in an isolated, access-controlled environment, measure the actual DDL lock window before approving the change. Sample blockers and elapsed time during the im.18 master startup (query below). Define an agreed lock/maintenance budget before attempting production; if exceeded, stop and diagnose under maintenance rather than editing schema manually.

## Select and switch only NewAPI

Run these commands on the approved Linux host with the Docker user/service account. Replace the path, project, service and existing digest variables with the values from CORE. Leave the current host, bind mounts and database variables unchanged. For a disposable rehearsal the local immutable image IDs shown above are accepted; production uses registry `repo@sha256:…` pins.

~~~bash
# Set once for this session (example paths; use actual CORE paths/project).
cd /srv/core
BASE=(-p core --env-file /srv/core/.env -f /srv/core/docker-compose.yml)
SWITCH=/path/to/reviewed-im18/ops/upgrade/core-newapi-cutover.sh
export CORE_API_SERVICE=new-api CORE_CUTOVER_PIN_FILE=/srv/core/core-newapi.pin.yml
# Record immutable image refs and full source SHAs before the window.
: "$STOCK_IMAGE" "$NEW_IMAGE" "$FALLBACK_IMAGE" "$CANDIDATE_SHA" "$ROLLBACK_SHA"

# Say yes only after independently confirming ingress fence and OTHER writers stopped.
export CORE_TRAFFIC_FENCED=yes CORE_OTHER_WRITERS_STOPPED=yes CORE_BACKUP_VERIFIED=yes

# im.13 -> im.18. First command only validates; second performs.
CORE_FROM_IMAGE="$STOCK_IMAGE" CORE_TO_IMAGE="$NEW_IMAGE" CORE_TO_REVISION="$CANDIDATE_SHA" \
  CORE_EXECUTE=no "$SWITCH" upgrade "${BASE[@]}"
CORE_FROM_IMAGE="$STOCK_IMAGE" CORE_TO_IMAGE="$NEW_IMAGE" CORE_TO_REVISION="$CANDIDATE_SHA" \
  CORE_EXECUTE=yes "$SWITCH" upgrade "${BASE[@]}"

# im.18 -> compatibility im.13, preserving CURRENT PostgreSQL and Redis.
# Only set this after verifying invoice callbacks/refunds are drained or reconciled.
export CORE_INVOICES_RECONCILED=yes
CORE_FROM_IMAGE="$NEW_IMAGE" CORE_TO_IMAGE="$FALLBACK_IMAGE" CORE_TO_REVISION="$ROLLBACK_SHA" \
  CORE_EXECUTE=no "$SWITCH" rollback "${BASE[@]}"
CORE_FROM_IMAGE="$NEW_IMAGE" CORE_TO_IMAGE="$FALLBACK_IMAGE" CORE_TO_REVISION="$ROLLBACK_SHA" \
  CORE_EXECUTE=yes "$SWITCH" rollback "${BASE[@]}"

# compatibility im.13 -> im.18, after fencing/draining again.
CORE_FROM_IMAGE="$FALLBACK_IMAGE" CORE_TO_IMAGE="$NEW_IMAGE" CORE_TO_REVISION="$CANDIDATE_SHA" \
  CORE_EXECUTE=yes "$SWITCH" upgrade "${BASE[@]}"
~~~

The script stops/removes only the existing API container without removing volumes, atomically saves the target pin, recreates the service with `--no-deps --pull never`, and checks origin-local readiness. It does not un-fence traffic. All future Compose invocations for CORE must include `-f "$CORE_CUTOVER_PIN_FILE"` in addition to the same base args. Keep the Caddy and DB/Redis lifecycle independent. If the new container is already stopped following a failed launch, first reconcile any partial schema/writes and inspect logs; then set `CORE_ACCEPT_FAILED_TARGET=yes` for a rollback. If it was never created, likewise verify the slot is empty and set `CORE_EXPECT_NO_API_CONTAINER=yes`. Neither flag clears leases, force-starts an unknown source or restores data. The verify-only image deliberately refuses a partial/incompatible schema rather than repairing it. If required, recover forward to a fully migrated im.18 on the same database under maintenance before trying that rollback; never use stock im.13 to fix the partial database.

## Operator acceptance before re-enabling ingress

From an existing authorized, read-only psql session on the primary, check shape and compare known post-upgrade rows before/after the rollback and re-upgrade. The query below reads PostgreSQL’s actual catalog, not the old application's schema defaults. Use exact IDs recorded at the barrier; for a large production table do not scan every row while traffic is live.

~~~sql
SELECT a.attname, format_type(a.atttypid,a.atttypmod) AS actual_type
FROM pg_attribute a
WHERE a.attrelid='public.quota_data'::regclass AND a.attnum>0 AND NOT a.attisdropped
  AND a.attname IN ('username','model_name','use_group','node_name')
ORDER BY a.attname;
-- Each must be text after all three switches. In a controlled rehearsal, capture
-- hashes/lengths for the same four rows and compare to captured full fixture text.
-- In psql, set verify_ids to a safe comma-separated list of captured integer IDs.
SELECT id, char_length(username), char_length(model_name),
       char_length(use_group), char_length(node_name),
       md5(jsonb_build_array(username,model_name,use_group,node_name)::text) AS label_hash
FROM public.quota_data WHERE id IN (:verify_ids) ORDER BY id;

SELECT node_name, to_timestamp(started_at), to_timestamp(last_seen_at), info::jsonb
FROM public.system_instances ORDER BY last_seen_at DESC LIMIT 5;
SELECT type, task_id, locked_by, to_timestamp(locked_until) AS lease_until
FROM public.system_task_locks ORDER BY type;
SELECT type, status, locked_by, to_timestamp(updated_at)
FROM public.system_tasks ORDER BY id DESC LIMIT 20;

-- Run during upgrade from a separate monitor on the target DB.
SELECT pid, now()-query_start AS elapsed, wait_event_type, wait_event, pg_blocking_pids(pid)
FROM pg_stat_activity
WHERE datname=current_database() AND pid<>pg_backend_pid()
  AND query ILIKE '%alter table%quota_data%';
~~~

Verify the correct revision/image and new master heartbeat, one owner per unexpired lease, and a harmless authorized scheduled job (in the isolated rehearsal, explicit succeeded task history under the new master). There are additional referral/agent accounting, wallet maturity, support email, plugin and reporting loops outside master-only schedules; readiness alone cannot prove they are idle or healthy. Do not deliberately trigger destructive log cleanup on production as a smoke; use existing task transitions or an approved harmless probe. Reconcile a canary's request ID, new logs, account and wallet component totals, ledger operation count, and invoice rows/receipts/documents across each switch. Test both invoice paths via CORE’s reverse proxy from an internal test browser; do not enable invoice intake just for a production smoke. If health is green but data, lease ownership, or proxy checks fail, keep ingress fenced.

An actual DB restore forfeits writes after its backup barrier unless they are separately reconciled and replayed. It is not a substitute for the same-database application rollback above.

## Rehearsal evidence (isolated Linux; no OCI result yet)

| Run | Observed |
|---|---|
| im.13 baseline | Exact source 5c370275, Linux API-only executable; PostgreSQL 18.6 and Redis 8.8.0. All four existing 64-character reporting labels intact. |
| im.18 | Executable built from 5515641 (subsequent candidate changes are release/proxy tooling only). Actual four `text` columns, original values intact. Four separate synthetic valid UTF-8 reporting values of 76, 87, 98 and 109 characters were written after upgrade. |
| Dedicated im.13 rollback master | Source 6f7f6fe, executable version `v1.0.0-rc.51.chuanapi.im.13.rollback.1`. Same main DB, log DB, and Redis process; all labels matched full original text including suffixes. Both schema fingerprints unchanged, no rollback DDL detected. |
| im.18 again | Same data stores, schemas, receipt and prior writes intact. One charged streaming request and one succeeded master-owned job in each of the four stages. Four usage logs and wallet/limited-key/ledger deltas all reconcile to 4 × 50 units; previous full log/ledger row hashes retained. A synthetic linked Epay invoice fee record and its receipt were unchanged. |
| DDL lock | 261 reporting rows, 216 KiB with indexes before widening. Deliberately held an isolated blocking lock: first widening statement 303.261 ms including about 250 ms injected hold, then 0.819 / 0.820 / 0.493 ms for the other three. Production size and wait are not inferred from this. No authorized restricted CORE backup was available in this run. |
| Linux reverse proxy | Real loopback HTTPS using the exact Caddy path matcher in a synthetic proxy fixture; both invoice path families return the embedded application shell, authentication and role checks pass. No actual Caddy/container/browser/CDN test on Linux; the earlier Windows browser evidence is separate. |

Linux executable SHA-256 (not image digests):

- Stock im.13: `de42823cf45b189f075daee4d8fb24c5b04b8f4ed37950ff742e4ea11a198fe4`
- im.18: `089e8fd019fdfd2ef797e53134c9522d4a3cb02b7317a238adbcbed0ed4813cb`
- Dedicated im.13 rollback: `1ae0fb3dfe132ddb50ed29e64e8d1a0c51dec926897494b20eab1e76a81605ac`

The rollback executable in this rehearsal was built with Go 1.27.3 on Linux, embedded frontend, and dynamic glibc linkage; the OCI recipe rebuilds it with the repository's own Go flags and base images. Those resulting image IDs are not implied by the executable hashes. Until an approved runner executes the OCI and Linux browser/proxy checks, keep the rollout pending.

## Reproduce the synthetic executable rehearsal

Use PostgreSQL 18.6 client/server tools and Redis 8.8.0 binaries on an isolated Linux runner, with the exact app executables built from the recorded sources. The script refuses inherited datastore DSNs and starts its own loopback-only main/log databases and Redis; it does not accept live DSNs.

~~~bash
env -u SQL_DSN -u LOG_SQL_DSN -u REDIS_CONN_STRING \
  python3 ops/upgrade/core-rollback-rehearsal/rehearse.py \
  --im13 /qa/bin/im13-stock --im18 /qa/bin/im18 \
  --rollback /qa/bin/im13-rollback --pg-bin /qa/pg18.6/bin \
  --redis-bin /qa/redis8.8.0/bin --output /qa/receipts
~~~

For the **actual OCI gate**, use the companion adapter on an approved dedicated Linux test host (local Docker socket, no production services on that host). Preload all three build images and set their immutable pins. It refuses moving tags and mismatched source labels, uses the local native platform, starts fresh PG and Redis fixtures, and validates that four gateway containers stop gracefully. It uses host networking solely so the containers can reach the fixture's loopback-only servers; do not run this harness on CORE. Only its CLI stub tests were run here.

~~~bash
python3 ops/upgrade/core-rollback-rehearsal/rehearse-oci.py \
  --confirm-dedicated-test-host \
  --im13 "$STOCK_IMAGE" --im13-revision 5c370275b838d37d0ea4480810a2ebc2d7dd49ca \
  --im18 "$NEW_IMAGE" --im18-revision "$CANDIDATE_SHA" \
  --rollback "$FALLBACK_IMAGE" --rollback-revision "$ROLLBACK_SHA" \
  --pg-bin /qa/pg18.6/bin --redis-bin /qa/redis8.8.0/bin --output /qa/receipts
~~~
