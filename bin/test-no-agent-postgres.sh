#!/usr/bin/env bash
# Own a fresh loopback-only PostgreSQL cluster. Never accept a supplied database.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
pg_bin=${PG_BIN:?Set PG_BIN to PostgreSQL 18.6 bin directory}
"$pg_bin/postgres" --version | grep -E ' 18\.6$'
state=$(mktemp -d "${TMPDIR:-/tmp}/new-api-no-agent-pg.XXXXXX")
evidence=${NO_AGENT_EVIDENCE_DIR:-$state/evidence}
mkdir -p "$evidence" "$state/socket"
cleanup() { "$pg_bin/pg_ctl" -D "$state/data" -m fast -w stop >"$evidence/cleanup.log" 2>&1 || true; }
trap cleanup EXIT
unset SQL_DSN LOG_SQL_DSN REDIS_CONN_STRING TEST_MYSQL_DSN TEST_AGENT_POSTGRES_DSN
"$pg_bin/initdb" -D "$state/data" -U postgres --auth-local=trust --auth-host=trust --encoding=UTF8 --locale=C >"$evidence/initdb.log" 2>&1
# TCP loopback only also works in sandboxes that prohibit Unix sockets.
printf "unix_socket_directories = ''\n" >> "$state/data/postgresql.conf"
port=${NO_AGENT_PG_PORT:-55439}
"$pg_bin/pg_ctl" -D "$state/data" -l "$evidence/postgres.log" -o "-h 127.0.0.1 -p $port" -w start >"$evidence/start.log" 2>&1
export PGHOST=127.0.0.1 PGPORT=$port PGUSER=postgres
"$pg_bin/psql" -d postgres -Atc 'SHOW server_version_num' | grep '^180006$' | tee "$evidence/postgres-version.txt"
for name in model service auth preflight wallet referral normalization finalization fresh upgrade retirement; do "$pg_bin/createdb" "no_agent_$name"; done
base="postgres://postgres@127.0.0.1:$port"
export TEST_RETIREMENT_POSTGRES_DSN="$base/no_agent_retirement?sslmode=disable"
export TEST_MODEL_POSTGRES_DSN="$base/no_agent_model?sslmode=disable"
export TEST_SERVICE_POSTGRES_DSN="$base/no_agent_service?sslmode=disable"
export TEST_POSTGRES_DSN="$base/no_agent_auth?sslmode=disable"
export TEST_NON_AGENT_POSTGRES_DSN="$base/no_agent_preflight?sslmode=disable"
export TEST_WALLET_POSTGRES_DSN="$base/no_agent_wallet?sslmode=disable"
export TEST_REFERRAL_POSTGRES_DSN="$base/no_agent_referral?sslmode=disable"
export TEST_NORMALIZATION_POSTGRES_DSN="$base/no_agent_normalization?sslmode=disable"
export TEST_FINALIZATION_SERVICE_POSTGRES_DSN="$base/no_agent_finalization?sslmode=disable"
export TEST_SECURITY_DIALECT=postgres TEST_MANAGE_USER_DIALECT=postgres TEST_TASK_DB_DIALECT=postgres
export GOWORK=off GOTOOLCHAIN=local NO_AGENT_FIXTURE_OWNED=1
cd "$root"
if (($#)); then "$@"; exit; fi
run() { local label=$1; shift; "$@" 2>&1 | tee "$evidence/$label.log"; }
run model go test -p 2 -count=1 -timeout=10m -json ./model -run '^(TestOrdinary|TestPaymentReceipt|TestBonusSource|TestRedeem|TestRedemptionQuota|TestSearchRedemptions|TestRecordReferralCommission|TestNonAgentPreflight|TestMainAutoMigrate|TestUserCall|TestInsertPersistsValidatedInviterRelationship|TestInsertWithTxRejectsDisabledAndSelfInviter)'
run promotion-retirement go test -p 2 -count=1 -timeout=10m -json ./model -run '^TestNonAgentPromotionRetirementMatrix$/^postgres$'
run service go test -p 2 -count=1 -timeout=10m -json ./service -run '^(TestReferralPostgreSQL|TestReferralCommission|TestUserCall)'
run permissions go test -p 2 -count=1 -timeout=10m -json ./middleware ./controller -run '^(TestLegacyConsumerRoleAuthorizationPostgres|TestRetiredQualification|TestSelfDTO|TestUserCall|TestAdminUser|TestWaffoPancakeNormalizationPostgreSQL)'
run redemption go test -p 2 -count=1 -timeout=10m -json ./controller -run '^(TestRedemptionLifecycleDatabaseMatrix|TestDeleteRedemptionBatch)$/^postgres$'
run wallet go test -p 2 -count=1 -timeout=10m -json ./modules/chuanapi/wallet -run '^(TestGetWalletSummary|TestGetReferralRewards|TestTransferAffQuota|TestFunding|TestWalletRefund)'
run routes go test -p 2 -count=1 -timeout=10m -json ./router ./chuanapi -run '^(TestNonAgent|TestAccessToken|TestRegister|TestWalletModels|TestSupportRoute|TestLlmsRoute)'
run preserved-relay go test -p 2 -count=1 -timeout=10m -json ./relay ./relay/common -run '^(TestFunctionDescription|TestChatToResponsesDescription|TestEnsureFunctionDescriptions|TestExistingDescriptionOverrideLimits|TestImage|TestUserCall)'
SQL_DSN="$base/no_agent_fresh?sslmode=disable" run migration-first go run ./bin/no-agent-fixture
SQL_DSN="$base/no_agent_fresh?sslmode=disable" run migration-repeat go run ./bin/no-agent-fixture
