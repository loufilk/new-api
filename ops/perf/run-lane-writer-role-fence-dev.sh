#!/usr/bin/env bash
# Local destructive test fixture only: always creates a fresh cluster and
# never accepts a DSN or changes an existing cluster.
set -Eeuo pipefail

target="${1:?provide the exact local source commit}"
repo="$(git rev-parse --show-toplevel)"
cd "$repo"
[[ "$(id -u)" != 0 && "$(git rev-parse HEAD)" == "$target" && -z "$(git status --porcelain)" ]]
git diff --check 0a10b95 HEAD
git show -s --format='commit=%H%ntree=%T%nparent=%P'

pg="${PG_BIN:-$(pg_config --bindir)}"
pg_share="${PG_SHARE:-$(pg_config --sharedir)}"
"$pg/postgres" --version
"$pg/postgres" --version | grep -Eq '^postgres \(PostgreSQL\) 18[.]6([ ]|$)'
go version
port="${LANE_WRITER_FENCE_TEST_PORT:-28830}"
[[ "$port" =~ ^[0-9]{4,5}$ && "$port" -gt 1023 && "$port" -lt 65536 ]]
d="$(mktemp -d /tmp/lane-role-fence.XXXXXX)"
printf 'runtime=%s\n' "$d"
umask 077
mkdir -m 700 "$d/go"
random_hex() { od -An -N16 -tx1 /dev/urandom | tr -d '[:space:]'; }
secret="$(random_hex)"
nonce="$(random_hex)"
printf '%s\n' "$secret" > "$d/pw"
"$pg/initdb" -D "$d/pg" -L "$pg_share" -U agent --no-locale --encoding=UTF8 --auth-local=trust \
    --auth-host=scram-sha-256 --pwfile="$d/pw" > "$d/initdb.log"
rm -f "$d/pw"
printf "listen_addresses='127.0.0.1'\nport=%s\nunix_socket_directories=''\nmax_connections=22\nshared_buffers='32MB'\nfsync=on\nsynchronous_commit=on\nfull_page_writes=on\nautovacuum=off\ntrack_activities=on\npassword_encryption='scram-sha-256'\nmax_prepared_transactions=4\nlane_fence.fixture_nonce='%s'\n" \
    "$port" "$nonce" >> "$d/pg/postgresql.conf"
cat > "$d/pg/pg_hba.conf" <<'HBA'
host all all 127.0.0.1/32 scram-sha-256
HBA
unset PGHOSTADDR PGSERVICE PGSERVICEFILE PGOPTIONS PGPASSWORD SQL_DSN LOG_SQL_DSN REDIS_CONN_STRING TEST_MODEL_MYSQL_DSN
"$pg/pg_ctl" -D "$d/pg" -l "$d/postgres.log" -w start
trap '"$pg/pg_ctl" -D "$d/pg" -m fast -w stop' EXIT
PGPASSWORD="$secret" "$pg/createdb" -h 127.0.0.1 -p "$port" -U agent lane_writer_fence_test
PGPASSWORD="$secret" "$pg/createdb" -h 127.0.0.1 -p "$port" -U agent lane_writer_fence_model_test

export TEST_LANE_WRITER_FENCE_POSTGRES_DSN="postgres://agent:$secret@127.0.0.1:$port/lane_writer_fence_test?sslmode=disable"
export TEST_MODEL_POSTGRES_DSN="postgres://agent:$secret@127.0.0.1:$port/lane_writer_fence_model_test?sslmode=disable"
export TEST_LANE_WRITER_FENCE_PGDATA="$d/pg" TEST_LANE_WRITER_FENCE_NONCE="$nonce"
export GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOTELEMETRY=off CGO_ENABLED=0 GOMAXPROCS=2 TMPDIR="$d/go" GOTMPDIR="$d/go"
run() {
  local name="$1"; shift
  echo "start $name"
  if ! go test -p 1 -ldflags=-w -count=1 -timeout=110s -v "$@" > "$d/$name.log" 2>&1; then
    tail -110 "$d/$name.log"; return 1
  fi
  if grep -Eq -- '--- (SKIP|FAIL):|no tests to run' "$d/$name.log"; then
    grep -E -- '--- (SKIP|FAIL):|no tests to run' "$d/$name.log"; return 1
  fi
  printf 'printed_pass=%s\n' "$(grep -c -- '--- PASS:' "$d/$name.log")"
  awk '/^[[:space:]]*--- PASS:/ || /^PASS$/ || /^ok[[:space:]]/ {print}' "$d/$name.log" | tee "$d/$name.pass.log"
}
run tagged-fence -tags lane_billing_integration_dev ./model -run '^TestLaneWriterRoleFenceDevPostgres$'
run ordinary-gate ./model -run '^(TestLaneCustodyTransitionBlocksServingWithUnfencedOldWriterPostgres|TestLaneStoreAuthorityEpochRevocationFencesInFlightPostgres)$'
echo "PASS exact isolated writer-fence rehearsal $target"
