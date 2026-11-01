#!/usr/bin/env bash
# Invoke via test-no-agent-postgres.sh: owned synthetic cluster, no paid upstream.
set -euo pipefail
[[ ${NO_AGENT_FIXTURE_OWNED:-} == 1 && ${PGHOST:-} == 127.0.0.1 ]] || exit 2
root=$(cd "$(dirname "$0")/.." && pwd)
evidence=${NO_AGENT_EVIDENCE_DIR:?Set an evidence directory}
base="postgres://postgres@127.0.0.1:$PGPORT"
"$PG_BIN/createdb" no_agent_fixed
"$PG_BIN/createdb" no_agent_fixed_logs
export TEST_FIXED_POSTGRES_DSN="$base/no_agent_fixed?sslmode=disable"
export TEST_FIXED_POSTGRES_LOG_DSN="$base/no_agent_fixed_logs?sslmode=disable"
cd "$root"
go test -p 2 -count=1 -timeout=15m -json ./service -run '^TestFixedPriceBillingDatabaseMatrix$/^postgres$' > "$evidence/fixed-price.log" 2>&1
go test -p 2 -count=1 -timeout=15m -json ./service -run '^TestBillingFinalizationSessionDatabaseMatrix$/^postgres$' > "$evidence/finalization.log" 2>&1
printf 'PostgreSQL 18.6 ordinary consumption and recovery matrices passed\n'
