#!/usr/bin/env bash
# Runs only against disposable CI services. No deployment or reference writes.
set -euo pipefail
report() {
  local label=$1 package=$2 pattern=$3
  shift 3
  # Module-download diagnostics are stderr, not Go test events. Keep both
  # streams intact and retain the command's exit status before checking JSON.
  go test -race -p 2 "$package" -run "$pattern" -count=1 -timeout=12m -json > "$OUT/$label.jsonl" 2> "$OUT/$label.stderr.log"
  python3 ops/ci/check_go_test_report.py "$OUT/$label.jsonl" "$@"
}
case "$KIND" in
redis)
  export TEST_RUNTIME_REDIS=1
  export TEST_REDIS_DSN="redis://127.0.0.1:$REDIS_PORT/0"
  docker exec "$REDIS_ID" redis-cli INFO server > "$OUT/redis-version.txt"
  report redis ./middleware '^TestModelRateLimitRealRedis$' TestModelRateLimitRealRedis
  ;;
clickhouse)
  export AUDIT_CLICKHOUSE_DSN="clickhouse://compat:compat@127.0.0.1:$CH_PORT/runtime_compat"
  docker exec "$CH_ID" clickhouse-client --user compat --password compat --query 'SELECT version()' > "$OUT/clickhouse-version.txt"
  report audit ./controller '^TestIndependentAuditLogStores$/clickhouse/' 'TestIndependentAuditLogStores/clickhouse/upgrade=false' 'TestIndependentAuditLogStores/clickhouse/upgrade=true'
  ;;
database)
  export TEST_DATABASE_COMPAT=1 TEST_DATABASE_DIALECT="$DIALECT"
  case "$DIALECT" in
  mysql)
    docker exec "$MYSQL_ID" mysql -uroot -pcompat -e 'ALTER DATABASE runtime_compat CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE runtime_compat_log CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; SELECT VERSION();' > "$OUT/database-version.txt"
    export TEST_DATABASE_DSN="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/runtime_compat?charset=utf8mb4&parseTime=true&loc=UTC"
    export TEST_MYSQL_DSN="$TEST_DATABASE_DSN"
    export TEST_LOG_DATABASE_DSN="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/runtime_compat_log?charset=utf8mb4&parseTime=true&loc=UTC"
    ;;
  postgres)
    docker exec "$PG_ID" createdb -U postgres runtime_compat_log
    docker exec "$PG_ID" psql -U postgres -d runtime_compat -c 'SHOW server_version;' > "$OUT/database-version.txt"
    export TEST_DATABASE_DSN="postgres://postgres:compat@127.0.0.1:$PG_PORT/runtime_compat?sslmode=disable"
    export TEST_POSTGRES_DSN="$TEST_DATABASE_DSN"
    export TEST_LOG_DATABASE_DSN="postgres://postgres:compat@127.0.0.1:$PG_PORT/runtime_compat_log?sslmode=disable"
    ;;
  sqlite) ;;
  *) echo "Unsupported dialect" >&2; exit 1 ;;
  esac
  # Each startup suite has its own process because the extension registry is immutable.
  report migration ./model '^TestDatabaseCompatibilityReal$' TestDatabaseCompatibilityReal
  report scoped-token ./model '^TestScopedAccessTokenDatabaseCompatibilityReal$' TestScopedAccessTokenDatabaseCompatibilityReal
  if [ "$DIALECT" = sqlite ]; then
    report schema ./model '^TestSupportTicketSchemaSQLite$' TestSupportTicketSchemaSQLite
  else
    report schema ./model "^(TestMoneyColumnsAre64BitOnConfiguredDatabases|TestSupportTicketSchemaConfiguredDatabases)$/$DIALECT$" "TestMoneyColumnsAre64BitOnConfiguredDatabases/$DIALECT" "TestSupportTicketSchemaConfiguredDatabases/$DIALECT"
  fi
  report payment ./pkg/paymentcore '^TestPaymentOrderBindingDatabaseCompatibilityReal$' TestPaymentOrderBindingDatabaseCompatibilityReal
  report preconsume ./controller "^TestPreConsumePolicyDatabaseMatrix$/$DIALECT$" "TestPreConsumePolicyDatabaseMatrix/$DIALECT"
  prefix="TestTaskPollingDefersStaleChannelCache/$DIALECT"
  report task-cache ./service "^TestTaskPollingDefersStaleChannelCache$/$DIALECT$" "$prefix/batch=false/wallet" "$prefix/batch=false/subscription" "$prefix/batch=true/wallet" "$prefix/batch=true/subscription"
  export TEST_TASK_DB_DIALECT="$DIALECT"
  report midjourney-cache ./controller '^TestMidjourneyPollingDefersStaleChannelCache$' TestMidjourneyPollingDefersStaleChannelCache
  export TEST_PERF_MYSQL_DSN="$TEST_MYSQL_DSN" TEST_PERF_POSTGRES_DSN="$TEST_POSTGRES_DSN"
  export AUDIT_MYSQL_DSN="$TEST_MYSQL_DSN" AUDIT_POSTGRES_DSN="$TEST_POSTGRES_DSN"
  report performance ./pkg/perf_metrics "^(TestPerformanceAggregationAndFlush|TestRuntimeFinalFlushRetainsFailedSamplesAndPersistsCurrentBucket)$/$DIALECT$" "TestPerformanceAggregationAndFlush/$DIALECT" "TestRuntimeFinalFlushRetainsFailedSamplesAndPersistsCurrentBucket/$DIALECT"
  if [ "$DIALECT" != sqlite ]; then
    report audit ./controller "^TestIndependentAuditLogStores$/$DIALECT/" "TestIndependentAuditLogStores/$DIALECT/upgrade=false" "TestIndependentAuditLogStores/$DIALECT/upgrade=true"
  fi
  report token-audit ./controller "^TestAPITokenAuditDatabaseMatrix$/$DIALECT/" "TestAPITokenAuditDatabaseMatrix/$DIALECT/separate_log=false" "TestAPITokenAuditDatabaseMatrix/$DIALECT/separate_log=true"
  if [ "$LABEL" = postgres18 ]; then
    for database in runtime_groups runtime_identity runtime_websocket; do
      docker exec "$PG_ID" createdb -U postgres "$database"
    done
    export TEST_GROUP_RENAME_POSTGRES_DSN="postgres://postgres:compat@127.0.0.1:$PG_PORT/runtime_groups?sslmode=disable"
    export TEST_RESPONSES_WS_POSTGRES_DSN="postgres://postgres:compat@127.0.0.1:$PG_PORT/runtime_websocket?sslmode=disable"
    export TEST_REDIS_ADDR="127.0.0.1:$REDIS_PORT"
    report group-contracts ./model '^TestGroupRenamePostgresRedis$' TestGroupRenamePostgresRedis TestGroupRenamePostgresRedis/two_process_group_runtime TestGroupRenamePostgresRedis/warm_caches
    TEST_SECURITY_DIALECT=postgres TEST_POSTGRES_DSN="postgres://postgres:compat@127.0.0.1:$PG_PORT/runtime_identity?sslmode=disable" report identity-contracts ./controller '^TestGitHubBindingConcurrentPostgres$' TestGitHubBindingConcurrentPostgres
    report websocket ./controller '^TestResponses(WSRequestRunner|WebSocket|InterruptedStreamHealth|StreamOutcomesPreserveAccounting|HTTPHealthCountsFinalResult)' TestResponsesWebSocketDisconnectSettlesDeliveredOutputOnce TestResponsesWebSocketInitialUpstreamRejectionRefundsReservation TestResponsesStreamOutcomesPreserveAccounting TestResponsesWebSocketPreRoutingRejectionsFollowHealthClassification
  fi
  ;;
*) echo 'Unsupported service kind' >&2; exit 1 ;;
esac
git diff --exit-code --ignore-space-at-eol
printf 'All required %s contracts passed on %s\n' "$LABEL" "$(git rev-parse HEAD)"
