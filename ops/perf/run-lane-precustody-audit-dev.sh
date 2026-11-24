#!/usr/bin/env bash
# Fresh TLS loopback fixture only; never consumes a supplied database URL.
set -Eeuo pipefail
target="${1:?provide the exact local source commit}"
cd "$(git rev-parse --show-toplevel)"
[[ "$(id -u)" != 0 && "$(git rev-parse HEAD)" == "$target" && -z "$(git status --porcelain)" && ! -e web/dist ]]
git check-ignore -q web/dist/index.html
git diff --check 3ffba33 HEAD
git show -s --format='commit=%H%ntree=%T%nparent=%P'
pg="${PG_BIN:-$(pg_config --bindir)}"
share="${PG_SHARE:-$(pg_config --sharedir)}"
"$pg/postgres" --version | grep -E '^postgres \(PostgreSQL\) 18[.]6([ ]|$)'
go version
port="${LANE_PRECUSTODY_TEST_PORT:-28838}"
[[ "$port" =~ ^[0-9]{4,5}$ && "$port" -gt 1023 && "$port" -lt 65536 ]]
umask 077
d="$(mktemp -d /tmp/lane-precustody-audit.XXXXXXXX)"
printf 'local_fixture=%s\n' "$d"
mkdir "$d/go"
random_hex() { od -An -N16 -tx1 /dev/urandom | tr -d '[:space:]'; }
password="$(random_hex)"
nonce="$(random_hex)"
printf '%s\n' "$password" > "$d/password"
"$pg/initdb" -D "$d/pg" -L "$share" -U agent --no-locale --encoding=UTF8 --auth-local=trust \
  --auth-host=scram-sha-256 --pwfile="$d/password" > "$d/initdb.log"
rm -f "$d/password"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=lane-audit.test \
  -addext 'subjectAltName=DNS:lane-audit.test,IP:127.0.0.1' -keyout "$d/server.key" -out "$d/server.crt" > "$d/tls.log" 2>&1
cat >> "$d/pg/postgresql.conf" <<CONF
listen_addresses='127.0.0.1'
port=$port
unix_socket_directories=''
ssl=on
ssl_cert_file='$d/server.crt'
ssl_key_file='$d/server.key'
max_connections=20
shared_buffers='32MB'
track_activities=on
autovacuum=off
max_prepared_transactions=4
lane_audit.fixture_nonce='$nonce'
CONF
printf 'hostssl all all 127.0.0.1/32 scram-sha-256\n' > "$d/pg/pg_hba.conf"
while IFS='=' read -r name _; do
  [[ "$name" == PG* && "$name" != PG_BIN && "$name" != PG_SHARE ]] && unset "$name" || true
done < <(env)
unset SQL_DSN LOG_SQL_DSN REDIS_CONN_STRING LANE_PRECUSTODY_AUDITOR_DSN
"$pg/pg_ctl" -D "$d/pg" -l "$d/postgres.log" -w start
trap '"$pg/pg_ctl" -D "$d/pg" -m fast -w stop; rm -f "$d/server.key" web/dist/index.html; rmdir web/dist 2>/dev/null || true' EXIT
PGPASSWORD="$password" PGSSLMODE=verify-full PGSSLROOTCERT="$d/server.crt" \
  "$pg/createdb" -h 127.0.0.1 -p "$port" -U agent lane_precustody_audit_test
export TEST_LANE_PRECUSTODY_PGDATA="$d/pg" TEST_LANE_PRECUSTODY_PORT="$port" \
  TEST_LANE_PRECUSTODY_NONCE="$nonce" TEST_LANE_PRECUSTODY_PASSWORD="$password"
export GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOTELEMETRY=off CGO_ENABLED=0 GOMAXPROCS=2 TMPDIR="$d/go" GOTMPDIR="$d/go"
# Same root application source and VCS metadata, with only an ignored inert
# frontend fixture as in Dockerfile.dev. The candidate must never be started.
mkdir web/dist
printf '<!DOCTYPE html><title>isolated verifier artifact</title>\n' > web/dist/index.html
export TEST_LANE_PRECUSTODY_AUDIT_BIN="$d/lane-precustody-audit" TEST_LANE_PRECUSTODY_CANDIDATE="$d/new-api"
echo 'building exact verifier and offline synthetic application artifact'
go build -p 1 -buildvcs=true -trimpath -o "$TEST_LANE_PRECUSTODY_AUDIT_BIN" ./cmd/lane-precustody-audit
go build -p 1 -buildvcs=true -trimpath -o "$TEST_LANE_PRECUSTODY_CANDIDATE" .
for binary in "$TEST_LANE_PRECUSTODY_AUDIT_BIN" "$TEST_LANE_PRECUSTODY_CANDIDATE"; do
  go version -m "$binary" | awk '/vcs.revision|vcs.modified/{print}'
done
go test -p 1 -ldflags=-w -timeout=110s -count=1 -v ./cmd/lane-precustody-audit > "$d/test.log" 2>&1 ||
  { awk '/^=== RUN|^--- (FAIL|SKIP):|^FAIL|^[[:space:]]+Error Trace:/' "$d/test.log"; exit 1; }
! grep -qE -- '--- (FAIL|SKIP):|no tests to run' "$d/test.log"
printf 'printed_pass=%s\n' "$(grep -c -- '--- PASS:' "$d/test.log")"
awk '/^[[:space:]]*--- PASS:/ || /^PASS$/ || /^ok[[:space:]]/{print}' "$d/test.log"
echo "PASS exact isolated read-only operator rehearsal $target"
