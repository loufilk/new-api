#!/usr/bin/env bash
# Disposable loopback PostgreSQL and synthetic credentials only. Never accepts a
# deployment DSN. Requires initdb/pg_ctl/psql/createdb, Go, Python 3 and curl.
set -euo pipefail
umask 077
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SCRIPTS="$ROOT/scripts/test-user-call-interception"
: "${UCI_BINARY:?Set UCI_BINARY to the absolute path of a freshly built new-api binary}"
[[ "$UCI_BINARY" == /* && -x "$UCI_BINARY" ]] || exit 1
if [[ -n "${UCI_BASELINE_BINARY:-}" ]]; then
  [[ "$UCI_BASELINE_BINARY" == /* && -x "$UCI_BASELINE_BINARY" ]] || exit 1
fi
RUN=$(mktemp -d "${TMPDIR:-/tmp}/newapi-call-interception.XXXXXXXX")
export UCI_EVIDENCE_DIR="$RUN/evidence" UCI_FIXTURE_FILE="$RUN/accounts.private.json"
mkdir -p "$UCI_EVIDENCE_DIR" "$RUN/home"
export PGPASSFILE="$RUN/no-pgpass" PGSERVICEFILE="$RUN/no-pg-service" PGSYSCONFDIR="$RUN"
unset PGSERVICE PGOPTIONS PGPASSWORD PGHOSTADDR PGHOST PGPORT PGDATABASE PGUSER
pg_command() {
  env -i PATH="$PATH" HOME="$RUN/home" PGPASSFILE="$PGPASSFILE" \
    PGSERVICEFILE="$PGSERVICEFILE" PGSYSCONFDIR="$PGSYSCONFDIR" "$@"
}
cd "$ROOT"
export SQL_DSN='postgres://interception_test@127.0.0.1:55883/interception_runtime?sslmode=disable'
export LOG_SQL_DSN='' NODE_TYPE=master SESSION_COOKIE_SECURE=false REDIS_CONN_STRING='' GIN_MODE=release
export SESSION_SECRET="$(python3 -c 'import secrets; print(secrets.token_hex(32))')"
export CRITICAL_RATE_LIMIT=500 CRITICAL_RATE_LIMIT_DURATION=1200 GLOBAL_API_RATE_LIMIT=6000
export GOMAXPROCS=2 MEMORY_CACHE_ENABLED=false VERSION="$(cat VERSION)" TRUSTED_PROXIES=none
A=0 B=0 MOCK=0 PG_STARTED=0
cleanup() {
  for pid in "$A" "$B" "$MOCK"; do
    if [[ "$pid" != 0 ]]; then kill -TERM "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  done
  if [[ "$PG_STARTED" == 1 ]]; then pg_ctl -D "$RUN/pgdata" -m fast stop >> "$UCI_EVIDENCE_DIR/pg-stop.log" 2>&1 || true; fi
  rm -f "$UCI_FIXTURE_FILE"
  printf 'Synthetic test evidence and stopped disposable cluster: %s\n' "$RUN"
}
trap cleanup EXIT
# Prevent accidentally talking to another test/application occupying these ports.
python3 - <<'PY'
import socket
sockets=[]
for port in (55883,35881,35882,35886):
    sock=socket.socket(); sock.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
    sock.bind(('127.0.0.1',port)); sockets.append(sock)
PY
initdb -D "$RUN/pgdata" -U interception_test --auth-local=trust --auth-host=trust > "$UCI_EVIDENCE_DIR/pg-init.log"
pg_ctl -D "$RUN/pgdata" -l "$UCI_EVIDENCE_DIR/pg-server.log" -o "-p 55883 -k '' -h 127.0.0.1" start
PG_STARTED=1
pg_command createdb -h 127.0.0.1 -p 55883 -U interception_test interception_runtime
pg_command psql "$SQL_DSN" -XAt -v ON_ERROR_STOP=1 -c 'SELECT version()' > "$UCI_EVIDENCE_DIR/pg-version.txt"
ready() {
  local port=$1 pid=$2
  for n in $(seq 1 120); do
    if curl -q --noproxy '*' -fsS "http://127.0.0.1:$port/api/status" > "$UCI_EVIDENCE_DIR/status-$port.json" 2>/dev/null; then return; fi
    kill -0 "$pid" || return 1
    sleep 1
  done
  return 1
}
# A clean cwd prevents .env loading; a clean environment prevents inherited
# diagnostics/proxy/telemetry/secondary database settings from escaping isolation.
start_app() {
    cd "$RUN"
    exec env -i PATH="$PATH" HOME="$RUN/home" TMPDIR="$RUN" \
      SQL_DSN="$SQL_DSN" LOG_SQL_DSN='' NODE_TYPE="$1" PORT="$2" \
      SESSION_COOKIE_SECURE=false REDIS_CONN_STRING='' GIN_MODE=release \
      SESSION_SECRET="$SESSION_SECRET" HTTP_BIND_ADDRESS=127.0.0.1 \
      CRITICAL_RATE_LIMIT=500 CRITICAL_RATE_LIMIT_DURATION=1200 GLOBAL_API_RATE_LIMIT=6000 \
      GOMAXPROCS=2 MEMORY_CACHE_ENABLED=false VERSION="$VERSION" TRUSTED_PROXIES=none \
      "$3" --log-dir "$4"
}
snapshot_baseline_data() {
  # All agent tables, plus populated fixture/accounting tables. Hashes only: no
  # credential, session, reply or payload is copied to the evidence output.
  pg_command psql "$SQL_DSN" -XAt -v ON_ERROR_STOP=1 -c "SELECT tablename FROM pg_tables WHERE schemaname='public' AND (tablename LIKE 'agent_%' OR tablename IN ('users','tokens','channels','abilities','tasks','midjourneys','two_fas','balance_ledger','balance_ledger_operations')) ORDER BY tablename" |
  while read -r table; do
    pg_command psql "$SQL_DSN" -XAt -v ON_ERROR_STOP=1 -c "SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text, '[]')) FROM \"$table\" t" | sed "s/^/$table /"
  done
}
if [[ -n "${UCI_BASELINE_BINARY:-}" ]]; then
  # Exact old binaries have no bind-host option. Let their original startup
  # finish migrations, then fail deterministically BEFORE opening any listener.
  # The invalid port is deliberate, not a successful baseline HTTP startup.
  if (start_app master 'migration-only:disabled' "$UCI_BASELINE_BINARY" "$UCI_EVIDENCE_DIR/baseline-logs") > "$UCI_EVIDENCE_DIR/startup-baseline.log" 2>&1; then
    echo 'Baseline unexpectedly accepted an invalid listen address' >&2; exit 1
  fi
  grep -q 'failed to bind HTTP server' "$UCI_EVIDENCE_DIR/startup-baseline.log"
  go run "$SCRIPTS/seed.go" "$UCI_FIXTURE_FILE" > "$UCI_EVIDENCE_DIR/seed.log" 2>&1
  snapshot_baseline_data > "$UCI_EVIDENCE_DIR/data-before.txt"
  pg_command psql "$SQL_DSN" -XAt -v ON_ERROR_STOP=1 -c "SELECT tablename,indexname,indexdef FROM pg_indexes ORDER BY tablename,indexname" > "$UCI_EVIDENCE_DIR/baseline-indexes.txt"
fi
for round in first repeat; do
  start_app master 35881 "$UCI_BINARY" "$UCI_EVIDENCE_DIR/app-logs" > "$UCI_EVIDENCE_DIR/startup-$round.log" 2>&1 & A=$!
  ready 35881 "$A"
  pg_command psql "$SQL_DSN" -XAt -v ON_ERROR_STOP=1 -c "SELECT tablename,indexname,indexdef FROM pg_indexes WHERE tablename LIKE 'user_call_interception%' ORDER BY tablename,indexname" > "$UCI_EVIDENCE_DIR/schema-$round.txt"
  kill -TERM "$A"; wait "$A"; A=0
done
test -s "$UCI_EVIDENCE_DIR/schema-first.txt"
cmp "$UCI_EVIDENCE_DIR/schema-first.txt" "$UCI_EVIDENCE_DIR/schema-repeat.txt"
if [[ -n "${UCI_BASELINE_BINARY:-}" ]]; then
  snapshot_baseline_data > "$UCI_EVIDENCE_DIR/data-after.txt"
  cmp "$UCI_EVIDENCE_DIR/data-before.txt" "$UCI_EVIDENCE_DIR/data-after.txt"
  pg_command psql "$SQL_DSN" -XAt -v ON_ERROR_STOP=1 -c "SELECT tablename,indexname,indexdef FROM pg_indexes WHERE tablename NOT LIKE 'user_call_interception%' ORDER BY tablename,indexname" > "$UCI_EVIDENCE_DIR/upgraded-old-indexes.txt"
  cmp "$UCI_EVIDENCE_DIR/baseline-indexes.txt" "$UCI_EVIDENCE_DIR/upgraded-old-indexes.txt"
  echo 'BASELINE UPGRADE: SEEDED DATA, AGENT TABLES AND EXISTING INDEXES PRESERVED'
else
  go run "$SCRIPTS/seed.go" "$UCI_FIXTURE_FILE" > "$UCI_EVIDENCE_DIR/seed.log" 2>&1
fi
python3 "$SCRIPTS/mock_upstream.py" > "$UCI_EVIDENCE_DIR/mock.log" 2>&1 & MOCK=$!
start_app master 35881 "$UCI_BINARY" "$UCI_EVIDENCE_DIR/app-a-logs" > "$UCI_EVIDENCE_DIR/startup-a.log" 2>&1 & A=$!
ready 35881 "$A"
start_app slave 35882 "$UCI_BINARY" "$UCI_EVIDENCE_DIR/app-b-logs" > "$UCI_EVIDENCE_DIR/startup-b.log" 2>&1 & B=$!
ready 35882 "$B"
# Linux evidence verifies actual sockets, not just the requested environment.
python3 - <<'PY'
import json, os
from pathlib import Path
ports={35881,35882,35886,55883}; observed={port:[] for port in ports}
if Path('/proc/net/tcp').exists():
    for source in ('/proc/net/tcp','/proc/net/tcp6'):
        if not Path(source).exists(): continue
        for line in Path(source).read_text().splitlines()[1:]:
            parts=line.split(); host,port=parts[1].split(':'); port=int(port,16)
            if port in ports and parts[3]=='0A': observed[port].append(host)
    assert all(hosts==['0100007F'] for hosts in observed.values()), observed
    Path(os.environ['UCI_EVIDENCE_DIR'],'loopback-listeners.json').write_text(json.dumps(observed))
PY
python3 "$SCRIPTS/run_http.py" | tee "$UCI_EVIDENCE_DIR/http-results.log"
# Re-run startup/migrations after saving real policies and logs, not only empty tables.
for table in user_call_interceptions user_call_interception_hits user_call_interception_audits; do
  pg_command psql "$SQL_DSN" -XAt -v ON_ERROR_STOP=1 -c "SELECT '$table',count(*),md5(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text) FROM $table t"
done > "$UCI_EVIDENCE_DIR/policy-before-restart.txt"
kill -TERM "$B"; wait "$B"; B=0
kill -TERM "$A"; wait "$A"; A=0
start_app master 35881 "$UCI_BINARY" "$UCI_EVIDENCE_DIR/restart-logs" > "$UCI_EVIDENCE_DIR/startup-saved-policy.log" 2>&1 & A=$!
ready 35881 "$A"
for table in user_call_interceptions user_call_interception_hits user_call_interception_audits; do
  pg_command psql "$SQL_DSN" -XAt -v ON_ERROR_STOP=1 -c "SELECT '$table',count(*),md5(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text) FROM $table t"
done > "$UCI_EVIDENCE_DIR/policy-after-restart.txt"
cmp "$UCI_EVIDENCE_DIR/policy-before-restart.txt" "$UCI_EVIDENCE_DIR/policy-after-restart.txt"
echo 'SAVED CONFIGURATION AND HIT/AUDIT LOGS SURVIVE REPEAT START'
for pid in "$A" "$B" "$MOCK"; do
  if [[ "$pid" != 0 ]]; then kill -TERM "$pid"; wait "$pid" || true; fi
done
A=0 B=0 MOCK=0
pg_ctl -D "$RUN/pgdata" -m fast stop >> "$UCI_EVIDENCE_DIR/pg-stop.log" 2>&1
PG_STARTED=0
python3 - <<'PY'
import socket
for port in (35881,35882,35886,55883):
    with socket.socket() as sock:
        assert sock.connect_ex(('127.0.0.1',port))!=0, f'test listener survived cleanup: {port}'
PY
echo 'ALL TEST LISTENERS STOPPED'
