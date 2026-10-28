#!/usr/bin/env bash
# Destructive integration fixtures are restricted to disposable CI service databases.
set -euo pipefail
: "${OUT:?OUT must name an evidence directory}"
: "${SOURCE_SHA:?SOURCE_SHA must identify the checkout}"
test "$(git rev-parse HEAD)" = "$SOURCE_SHA"
mkdir -p "$OUT" web/dist
touch web/dist/index.html
printf '%s\n' "$SOURCE_SHA" > "$OUT/revision.txt"
go version > "$OUT/go-version.txt"
status=0
# Never inherit an operator database or pressure opt-in in unrelated reports.
export TEST_REFUND_OPS_MYSQL_DSN= TEST_REFUND_OPS_POSTGRES_DSN= TEST_REFUND_QUEUE_PRESSURE=
export TEST_FINALIZATION_CACHE_MYSQL_DSN= TEST_FINALIZATION_CACHE_POSTGRES_DSN=
export TEST_SETTLEMENT_DEADLINE_REDIS_ADDR= TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE=
export TEST_TASK_REDIS_ADDR=
export TEST_FINALIZATION_REALTIME_MYSQL_DSN= TEST_FINALIZATION_REALTIME_POSTGRES_DSN=
export TEST_BILLING_RECOVERY_MYSQL_DSN= TEST_BILLING_RECOVERY_POSTGRES_DSN=
export NEW_API_TEST_RECOVERY_RESTART_FILE= NEW_API_TEST_RECOVERY_RESTART_PHASE=
export NEW_API_TEST_RECOVERY_NATIVE_CLAIM=
# Only the callback parent may supply its own temporary SQLite file to its child.
export PAYMENT_STALE_STRIPE_DSN=
export PAYMENT_STALE_FIXTURE_CHILD=
export TEST_BONUS_AUDIT_MYSQL_DSN= TEST_BONUS_AUDIT_POSTGRES_DSN=

report() {
  local label=$1 package=$2 pattern=$3
  shift 3
  local checker_options=()
  case "$label" in
    payment-pending-status|bonus-audit-race|payment-stale-callback|financial-bonus-cache) checker_options+=(--require-run) ;;
  esac
  # Retain compiler stderr separately so it cannot masquerade as Go test JSON.
  # Continue collecting independent suites, but any failed suite fails the job.
  if ! go test -race -p 2 "$package" -run "$pattern" -count=1 -timeout=12m -json > "$OUT/$label.jsonl" 2> "$OUT/$label.stderr.log"; then
    cat "$OUT/$label.stderr.log" >&2
    cat "$OUT/$label.jsonl"
    status=1
  fi
  if ! python3 ops/ci/check_go_test_report.py --reject-skipped-descendants "$OUT/$label.jsonl" "$@" "${checker_options[@]}"; then
    status=1
  fi
}

case "${1:-}" in
database)
  : "${DIALECT:?DIALECT is required}"
  export TEST_DATABASE_COMPAT=1 TEST_DATABASE_DIALECT="$DIALECT"
  export TEST_DATABASE_DSN= TEST_LOG_DATABASE_DSN=
  export TEST_MYSQL_DSN= TEST_MYSQL_LOG_DSN= TEST_POSTGRES_DSN= TEST_POSTGRES_LOG_DSN=
  export AUDIT_MYSQL_DSN= AUDIT_POSTGRES_DSN=
  token_migration_mysql_dsn= token_migration_postgres_dsn=
  task_submission_mysql_dsn= task_submission_postgres_dsn=
  billing_recovery_mysql_dsn= billing_recovery_postgres_dsn=
  bonus_audit_mysql_dsn= bonus_audit_postgres_dsn=
  # Retained model/service matrices still refuse nonempty fixture databases.
  # Never share those fixtures or inherit an ambient database DSN.
  export TEST_FINALIZATION_MODEL_MYSQL_DSN= TEST_FINALIZATION_MODEL_POSTGRES_DSN=
  export TEST_FINALIZATION_SERVICE_MYSQL_DSN= TEST_FINALIZATION_SERVICE_POSTGRES_DSN=
  finalization_model_mysql_dsn= finalization_model_postgres_dsn=
  finalization_service_mysql_dsn= finalization_service_postgres_dsn=
  finalization_realtime_mysql_dsn= finalization_realtime_postgres_dsn=
  case "$DIALECT" in
  mysql)
    : "${MYSQL_ID:?a real MySQL service is required}" "${MYSQL_PORT:?a real MySQL port is required}"
    docker inspect --format '{{.Config.Image}} {{.Image}}' "$MYSQL_ID" > "$OUT/database-image.txt"
    docker exec "$MYSQL_ID" mysql -uroot -pcompat -e 'SELECT VERSION(); ALTER DATABASE repair_compat CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_compat_log CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_token_migration CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;' > "$OUT/database-version.txt"
    docker exec "$MYSQL_ID" mysql -uroot -pcompat -e 'CREATE DATABASE repair_finalization_model CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_finalization_service CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_finalization_realtime CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_task_submission CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE DATABASE repair_billing_recovery CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'
    docker exec "$MYSQL_ID" mysql -uroot -pcompat -e 'CREATE DATABASE repair_bonus_audit CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'
    finalization_model_mysql_dsn="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_finalization_model?charset=utf8mb4&parseTime=true&loc=UTC"
    finalization_service_mysql_dsn="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_finalization_service?charset=utf8mb4&parseTime=true&loc=UTC"
    finalization_realtime_mysql_dsn="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_finalization_realtime?charset=utf8mb4&parseTime=true&loc=UTC"
    export TEST_MYSQL_DSN="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_compat?charset=utf8mb4&parseTime=true&loc=UTC"
    export TEST_MYSQL_LOG_DSN="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_compat_log?charset=utf8mb4&parseTime=true&loc=UTC"
    export TEST_DATABASE_DSN="$TEST_MYSQL_DSN" TEST_LOG_DATABASE_DSN="$TEST_MYSQL_LOG_DSN"
    token_migration_mysql_dsn="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_token_migration?charset=utf8mb4&parseTime=true&loc=UTC"
    task_submission_mysql_dsn="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_task_submission?charset=utf8mb4&parseTime=true&loc=UTC"
    billing_recovery_mysql_dsn="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_billing_recovery?charset=utf8mb4&parseTime=true&loc=UTC"
    bonus_audit_mysql_dsn="root:compat@tcp(127.0.0.1:$MYSQL_PORT)/repair_bonus_audit?charset=utf8mb4&parseTime=true&loc=UTC"
    ;;
  postgres)
    : "${PG_ID:?a real PostgreSQL service is required}" "${PG_PORT:?a real PostgreSQL port is required}"
    docker inspect --format '{{.Config.Image}} {{.Image}}' "$PG_ID" > "$OUT/database-image.txt"
    docker exec "$PG_ID" psql -U postgres -d repair_compat -Atc 'SHOW server_version;' > "$OUT/database-version.txt"
    docker exec "$PG_ID" createdb -U postgres repair_compat_log
    docker exec "$PG_ID" createdb -U postgres repair_token_migration
    docker exec "$PG_ID" createdb -U postgres repair_finalization_model
    docker exec "$PG_ID" createdb -U postgres repair_finalization_service
    docker exec "$PG_ID" createdb -U postgres repair_finalization_realtime
    docker exec "$PG_ID" createdb -U postgres repair_task_submission
    docker exec "$PG_ID" createdb -U postgres repair_billing_recovery
    docker exec "$PG_ID" createdb -U postgres repair_bonus_audit
    finalization_model_postgres_dsn="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_finalization_model?sslmode=disable"
    finalization_service_postgres_dsn="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_finalization_service?sslmode=disable"
    finalization_realtime_postgres_dsn="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_finalization_realtime?sslmode=disable"
    export TEST_POSTGRES_DSN="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_compat?sslmode=disable"
    export TEST_POSTGRES_LOG_DSN="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_compat_log?sslmode=disable"
    export TEST_DATABASE_DSN="$TEST_POSTGRES_DSN" TEST_LOG_DATABASE_DSN="$TEST_POSTGRES_LOG_DSN"
    token_migration_postgres_dsn="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_token_migration?sslmode=disable"
    task_submission_postgres_dsn="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_task_submission?sslmode=disable"
    billing_recovery_postgres_dsn="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_billing_recovery?sslmode=disable"
    bonus_audit_postgres_dsn="postgres://postgres:compat@127.0.0.1:$PG_PORT/repair_bonus_audit?sslmode=disable"
    ;;
  sqlite) ;;
  *) echo "Unsupported database dialect: $DIALECT" >&2; exit 1 ;;
  esac
  export AUDIT_MYSQL_DSN="$TEST_MYSQL_DSN" AUDIT_POSTGRES_DSN="$TEST_POSTGRES_DSN"

  # Production startup installs an immutable registry; keep it in its own process.
  report startup ./model '^TestDatabaseCompatibilityReal$' TestDatabaseCompatibilityReal
  # New admin/config ports must not hide unconfigured database skips.
  prefix="TestChannelPriorityDatabaseMatrix/$DIALECT"
  report channel-priority ./model "^TestChannelPriorityDatabaseMatrix$/$DIALECT$" "$prefix"
  prefix="TestAuditLogSortIndexDatabaseMatrix/$DIALECT"
  report audit-ordered-index ./model "^TestAuditLogSortIndexDatabaseMatrix$/$DIALECT$" "$prefix/upgrade=false" "$prefix/upgrade=true"
  prefix="TestRootAffCodeUniqueIndexDatabaseMatrix/$DIALECT"
  report setup-aff-code-index ./model "^TestRootAffCodeUniqueIndexDatabaseMatrix$/$DIALECT$" "$prefix/fresh" "$prefix/repeated_startup"
  case "$DIALECT" in
    sqlite) migration=TestTokenMigrationFromChar48ToVarchar128 ;;
    mysql) migration=TestTokenMigrationFromChar48ToVarchar128MySQL ;;
    postgres) migration=TestTokenMigrationFromChar48ToVarchar128Postgres ;;
  esac
  # This safety-sensitive legacy-schema test refuses an existing tokens table.
  # Use a fresh database owned by this disposable service, never the startup DB;
  # service teardown removes it without dropping any externally supplied data.
  TEST_MYSQL_DSN="$token_migration_mysql_dsn" TEST_POSTGRES_DSN="$token_migration_postgres_dsn" \
    report token-migration ./controller "^$migration$" "$migration"
  prefix="TestAPITokenAuditDatabaseMatrix/$DIALECT"
  report token-audit ./controller "^TestAPITokenAuditDatabaseMatrix$/$DIALECT$" "$prefix/separate_log=false" "$prefix/separate_log=true"
  export TEST_PERF_MYSQL_DSN="$TEST_MYSQL_DSN" TEST_PERF_POSTGRES_DSN="$TEST_POSTGRES_DSN"
  prefix="TestPerformanceAggregationAndFlush/$DIALECT"
  report performance-aggregation ./pkg/perf_metrics "^TestPerformanceAggregationAndFlush$/$DIALECT$" "$prefix" "$prefix/recent_models_are_summarized_over_their_status_sample_hours" "$prefix/recent_history_stops_at_the_7_day_boundary_even_with_fewer_than_24_samples"
  prefix="TestRuntimeFinalFlushRetainsFailedSamplesAndPersistsCurrentBucket/$DIALECT"
  report performance-runtime-flush ./pkg/perf_metrics "^TestRuntimeFinalFlushRetainsFailedSamplesAndPersistsCurrentBucket$/$DIALECT$" "$prefix"
  prefix="TestSetupWalletDatabaseMatrix/$DIALECT"
  required=("$prefix/fresh_root_and_replay" "$prefix/concurrent_fresh_setup_single_credit" "$prefix/rollback_balance_ledger_operations" "$prefix/rollback_balance_ledger" "$prefix/setup_record_failure_retry" "$prefix/paid_only_preconsume" "$prefix/invalid_credentials_no_account_or_grant")
  for account in empty legacy bonus_only split; do
    required+=("$prefix/existing_root_$account")
  done
  report setup-wallet ./controller "^TestSetupWalletDatabaseMatrix$/$DIALECT$" "${required[@]}"
  prefix="TestTaskSettlementMigrationDatabaseMatrix/$DIALECT"
  report task-migration ./model "^TestTaskSettlementMigrationDatabaseMatrix$/$DIALECT$" "$prefix/fresh" "$prefix/upgrade"
  prefix="TestQuotaDataCacheUsageDatabaseMatrix/$DIALECT"
  report dashboard-migration ./model "^TestQuotaDataCacheUsageDatabaseMatrix$/$DIALECT$" "$prefix/fresh" "$prefix/upgrade"
  prefix="TestMidjourneyWalletReceiptMigrationDatabaseMatrix/$DIALECT"
  report midjourney-migration ./model "^TestMidjourneyWalletReceiptMigrationDatabaseMatrix$/$DIALECT$" "$prefix/upgrade=false" "$prefix/upgrade=true"
  prefix="TestTaskWalletReceiptsDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in \
    'legacy_missing_receipt_fails_closed' \
    'midjourney_charge_rolls_back_on_midjourneys_failure' \
    'midjourney_charge_rolls_back_on_tokens_failure' \
    'midjourney_failure_before_charge_never_debits' \
    'midjourney_restores_original_accounts_from_stale_observer' \
    'midjourney_refund_atomic_midjourneys_failure' \
    'midjourney_refund_atomic_tokens_failure' \
    'refund_atomic_tasks_failure' \
    'refund_atomic_tokens_failure' \
    'subscription_settlement_rolls_back_with_token_failure' \
    'partial_refund_persists_remaining_accounts' \
    'settlement_falls_back_and_persists_cumulative_receipt' \
    'settlement_keeps_existing_bonus_exclusion' \
    'settlement_rolls_back_on_tasks_failure' \
    'settlement_rolls_back_on_tokens_failure'; do
    required+=("$prefix/$contract")
  done
  report wallet ./service "^TestTaskWalletReceiptsDatabaseMatrix$/$DIALECT$" "${required[@]}"
  # Current atomic subscription requirements reuse uniquely owned table/schema helpers.
  # The old nested-transaction parity evidence is historical, never a GREEN oracle.
  prefix="TestSubscriptionFinancialConsistencyDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in 'absolute_reserve_and_terminal_identity' 'real_commit_lost_ack_replay' 'receipt_write_failure_rolls_back_reserve_and_settle' 'caller_transaction_rollback_preserves_reservation' 'retired_period_rejects_growth_and_closes_old_settlement' 'task_adjustment_requires_exact_prior_evidence' 'unknown_legacy_period_requires_review' 'overflow_cannot_credit_or_publish_a_receipt' 'concurrent_request_claims_validate_the_winner/same_identity' 'concurrent_request_claims_validate_the_winner/different_user' 'concurrent_request_claims_validate_the_winner/different_amount' 'concurrent_settle_and_refund_have_one_terminal_owner'; do required+=("$prefix/$contract"); done
  report subscription-financial-consistency ./model "^TestSubscriptionFinancialConsistencyDatabaseMatrix$/$DIALECT$" "${required[@]}"
  prefix="TestFinancialReviewReportDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in 'pending_identity_redaction_and_no_writes' 'undeployed_and_partial_schemas_are_not_clean_history'; do required+=("$prefix/$contract"); done
  report financial-review-read-only ./model "^TestFinancialReviewReportDatabaseMatrix$/$DIALECT$" "${required[@]}"
  financial_modes=("$DIALECT")
  if [ "$DIALECT" = sqlite ]; then financial_modes=(sqlite_immediate sqlite_deferred); fi
  for mode in "${financial_modes[@]}"; do
    prefix="TestFinancialConsistencySubscriptionDatabaseMatrix/$mode"
    report "subscription-correctness-$mode" ./model "^TestFinancialConsistencySubscriptionDatabaseMatrix$/$mode$" "$prefix/request_identity" "$prefix/refund_receipt_write_failure" "$prefix/old_period_refund"
  done

  prefix="TestSubscriptionSupplementDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in 'same_operation_and_independent_segments' 'concurrent_same_operation_has_one_owner' 'amount_and_full_identity_conflicts' 'token_sql_error' 'terminal_receipt_sql_error' 'real_commit_lost_ack_replay' 'retired_period_rejects_new_charge_but_keeps_prior_replay' 'invalid_input_and_replacement_token_fail_closed'; do required+=("$prefix/$contract"); done
  report subscription-supplement ./model "^TestSubscriptionSupplementDatabaseMatrix$/$DIALECT$" "${required[@]}"

  prefix="TestOfficialSubscriptionRefundDatabaseBehavior/$DIALECT"
  report official-subscription-refund ./model "^TestOfficialSubscriptionRefundDatabaseBehavior$/$DIALECT$" TestOfficialSubscriptionRefundDatabaseBehavior "$prefix"
  prefix="TestSubscriptionPeriodMigrationDatabaseMatrix/$DIALECT"
  report subscription-period-migration ./model "^TestSubscriptionPeriodMigrationDatabaseMatrix$/$DIALECT$" "$prefix/fresh" "$prefix/upgrade"
  report owner ./model "^TestPublicModelOwnerDatabaseMatrix$/$DIALECT$" "TestPublicModelOwnerDatabaseMatrix/$DIALECT"
  report owner-group-settings ./model "^TestRequestPolicyDatabaseMatrix$/$DIALECT$" "TestRequestPolicyDatabaseMatrix/$DIALECT"
  prefix="TestMonitorHiddenGroupsDatabaseMatrix/$DIALECT"
  required=("$prefix/production_json_reload" "$prefix/fresh_default_insert" "$prefix/corrupt_saved_visibility_fails_closed" "$prefix/failed_write_preserves_saved_visibility" "$prefix/concurrent_legacy_write_preserves_visibility" "$prefix/connection_reopen_preserves_visibility")
  for contract in omitted_preserves null_preserves empty_clears false_clears explicit_replaces invalid_key_rejected invalid_false_key_rejected invalid_type_rejected too_many_rejected; do
    required+=("$prefix/save_contract/$contract")
  done
  report monitor-visibility ./modules/chuanapi/console/monitor "^TestMonitorHiddenGroupsDatabaseMatrix$/$DIALECT$" "${required[@]}"
  prefix="TestSensitiveExemptionGroupRenameDatabaseMatrix/$DIALECT"
  required=("$prefix/rename_preserves_policy_not_old_name" "$prefix/collision_and_coordinated_removal" "$prefix/explicit_options_normalize_and_revoke" "$prefix/invalid_or_stale_explicit_options_rollback" "$prefix/stale_policy_writes_and_no_group_definition" "$prefix/explicit_reintroduction_remains_possible" "$prefix/policy_save_precedes_rename" "$prefix/concurrent_rename_policy_save/rollback=false" "$prefix/concurrent_rename_policy_save/rollback=true")
  report sensitive-group-rename ./model "^TestSensitiveExemptionGroupRenameDatabaseMatrix$/$DIALECT$" "${required[@]}"
  export TEST_FIXED_MYSQL_DSN="$TEST_MYSQL_DSN" TEST_FIXED_MYSQL_LOG_DSN="$TEST_MYSQL_LOG_DSN"
  export TEST_FIXED_POSTGRES_DSN="$TEST_POSTGRES_DSN" TEST_FIXED_POSTGRES_LOG_DSN="$TEST_POSTGRES_LOG_DSN"
  prefix="TestConvertedResponsesFailureAccountingDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for format in openai claude gemini; do
    for outcome in failed bare_error cancelled failed_status eof done_without_terminal incomplete success empty_failure empty_eof fixed_partial fixed_empty fixed_success_missing_usage; do
      required+=("$prefix/buffered/$format/$outcome")
    done
  done
  report converted-responses ./relay "^TestConvertedResponsesFailureAccountingDatabaseMatrix$/$DIALECT/" "${required[@]}"
  prefix="TestNativeResponsesFailureFixedAccountingDatabaseMatrix/$DIALECT"
  report native-responses ./relay "^TestNativeResponsesFailureFixedAccountingDatabaseMatrix$/$DIALECT/" "$prefix" "$prefix/http_nonstream/failed/empty" "$prefix/http_nonstream/failed/partial_usage" "$prefix/http_request/failed_partial_usage" "$prefix/zero_settlement_transaction_failure_recovers_reserve"
  export TEST_TASK_DB_DIALECT="$DIALECT"
  prefix=TestImmediateTaskSettlementDatabase
  required=("$prefix")
  for contract in partial zero larger invalid_usage expression_failure negative_result missing_usage failed official_settlement_failure_after_insert official_upward_shortage_before_insert; do
    required+=("$prefix/$contract")
  done
  # Keep the background writer race and identity guards mandatory on all dialects.
  required+=(
    'TestServeTaskPluginProtocolBackgroundPreservesConcurrentFinancialState'
    'TestServeTaskPluginProtocolBackgroundPreservesConcurrentFinancialState/pending'
    'TestServeTaskPluginProtocolBackgroundPreservesConcurrentFinancialState/terminal_intent'
    'TestServeTaskPluginProtocolBackgroundPreservesConcurrentFinancialState/settled'
    'TestServeTaskPluginProtocolBackgroundPreservesConcurrentFinancialState/refunded'
    'TestServeTaskPluginProtocolBackgroundPreservesConcurrentFinancialState/review'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites/id'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites/user'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites/zero_id'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites/zero_user'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites/public_id'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites/platform'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites/empty_public_id'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites/empty_platform'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites/nil_context'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites/cancelled_context'
    'TestTaskResponsesBackgroundMetadataRejectsInvalidWrites/write_failure'
  )
  # Fixed-name wallet/owner tables bypass this fixture's normal table prefix.
  # Keep its existing-table refusal: only use a newly created disposable DB,
  # never delete or reuse tables left by the startup and earlier suites.
  TEST_MYSQL_DSN="$task_submission_mysql_dsn" TEST_POSTGRES_DSN="$task_submission_postgres_dsn" \
    report immediate-settlement ./controller '^(TestImmediateTaskSettlementDatabase|TestServeTaskPluginProtocolBackgroundPreservesConcurrentFinancialState|TestTaskResponsesBackgroundMetadataRejectsInvalidWrites)$' "${required[@]}"


  # Required current-policy leaves are mirrored by offline orchestration tests.
  prefix="TestBillingFinalizationDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in \
    'migration_preserves_pending_and_tombstones' \
    'near_miss_realtime_key_rejected/wss:+1:20' \
    'near_miss_realtime_key_rejected/wss:01:20' \
    'near_miss_realtime_key_rejected/wss:0:20' \
    'near_miss_realtime_key_rejected/wss:18446744073709551616:20' \
    'near_miss_realtime_key_rejected/wss:1:21' \
    'receipt_mismatch_and_legacy_replay' \
    'rollback_probe_no_permanent_owner' \
    'wallet_refund_sequence/partial_remaining_replay' \
    'wallet_refund_sequence/rollback_then_retry' \
    'wallet_refund_sequence/legacy_overcredit_rejected' \
    'token_counter_bounds/remaining_overflow' \
    'token_counter_bounds/remaining_underflow' \
    'token_counter_bounds/used_overflow' \
    'token_counter_bounds/used_underflow' \
    'token_counter_bounds/exact_integer_boundary' \
    'token_counter_bounds/unscoped_debit_active' \
    'token_counter_bounds/unscoped_debit_soft_deleted'; do
    required+=("$prefix/$contract")
  done
  if [ "$DIALECT" != sqlite ]; then
    required+=("$prefix/subscription_wallet_lock_order/supplement_vs_wallet_same_user_token")
  fi
  TEST_FINALIZATION_MODEL_MYSQL_DSN="$finalization_model_mysql_dsn" TEST_FINALIZATION_MODEL_POSTGRES_DSN="$finalization_model_postgres_dsn" \
    report request-finalization-model ./model "^TestBillingFinalizationDatabaseMatrix$/$DIALECT$" "${required[@]}"

  prefix="TestBillingFinalizationSessionDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in \
    'commit_boundaries/lost_ack/settlement' \
    'commit_boundaries/rollback/settlement' \
    'extension_capability/custom_billing_settler_insert' \
    'extension_capability/custom_non_wallet' \
    'extension_capability/custom_subscription_delta' \
    'extension_capability/custom_wallet_atomic_replay' \
    'failed_settle_reserve_refund' \
    'independent_violation_fee/fee_before_refund' \
    'independent_violation_fee/refund_before_fee' \
    'legacy_final_more_less/a' \
    'legacy_final_more_less/Å' \
    'legacy_final_more_less/ĕ' \
    'probe_failure' \
    'real_session_partial_less' \
    'real_session_partial_more' \
    'real_session_playground' \
    'real_session_subscription_zero_delta' \
    'real_session_subscription_zero_delta_replaced_token' \
    'real_session_trusted' \
    'real_session_unlimited' \
    'realtime_increment_terminal/failed_final_then_refund' \
    'realtime_increment_terminal/final_settle' \
    'realtime_increment_terminal/refund_initial_hold' \
    'separate_held_amounts' \
    'zero_delta_replacement/hold_0/key' \
    'zero_delta_replacement/hold_0/owner' \
    'zero_delta_replacement/hold_100/key' \
    'zero_delta_replacement/hold_100/owner' \
    'settlement_deadline_driver_pool/driver' \
    'settlement_deadline_driver_pool/connection' \
    'settlement_deadline_commit_evidence/wallet/rolled_back' \
    'settlement_deadline_commit_evidence/wallet/committed_lost_ack' \
    'settlement_deadline_redis/precommit_fence' \
    'settlement_deadline_redis/postcommit_cache' \
    'settlement_deadline_redis/zero_delta_cache' \
    'settlement_deadline_redis/terminal_replay_cache' \
    'settlement_deadline_redis/live_redis_error' \
    'settlement_cancelled_client' \
    'official_request_source_failure' \
    'official_request_token_failure' \
    'official_subscription_three_attempts' \
    'official_task_independent_steps/source' \
    'official_task_independent_steps/token' \
    'official_task_independent_steps/quota_clear' \
    'official_post_commit_errors/request_token' \
    'official_post_commit_errors/task_quota_clear' \
    'historical_pending_read_only/flag' \
    'historical_pending_read_only/private_only' \
    'historical_pending_read_only/both' \
    'inherited_093_allocation_limits/small_three_account_remainder' \
    'inherited_093_allocation_limits/submit_before_task_refund' \
    'inherited_093_allocation_limits/task_refund_before_submit' \
    'official_refund_before_lost_ack_readback' \
    'financial_lost_ack/readable' \
    'financial_lost_ack/read_failure' \
    'financial_task_refund/batch_false/tokens' \
    'financial_task_refund/batch_false/tasks' \
    'financial_task_refund/batch_true/tokens' \
    'financial_task_refund/batch_true/tasks' \
    'financial_mj_refund' \
    'financial_stale_success' \
    'financial_metadata/both_ids' \
    'financial_metadata/channel' \
    'financial_submit/trusted_zero' \
    'financial_submit/preconsume_more' \
    'financial_submit/preconsume_less' \
    'financial_submit/reserved' \
    'financial_submit/submit_rollback' \
    'financial_submit/submit_lost_ack' \
    'financial_task_identity/hard_delete' \
    'financial_task_identity/same_id_new_key' \
    'task_atomic/refund_rollback_source' \
    'task_atomic/refund_rollback_token' \
    'task_atomic/refund_rollback_user_usage' \
    'task_atomic/refund_rollback_channel_usage' \
    'task_atomic/refund_rollback_task_receipt' \
    'task_atomic/refund_rollback_main_log' \
    'task_atomic/commit_boundary_refund_false_mode_1' \
    'task_atomic/commit_boundary_refund_false_mode_2' \
    'task_atomic/commit_boundary_refund_true_mode_1' \
    'task_atomic/commit_boundary_refund_true_mode_2' \
    'task_atomic/settlement_more_retains_exact_net_receipt' \
    'task_atomic/concurrent_fresh_snapshots' \
    'task_atomic/fail_closed_wallet_receipt' \
    'task_atomic/fail_closed_token_fingerprint' \
    'task_atomic/fail_closed_source_only_refund' \
    'task_atomic/fail_closed_legacy_no_token' \
    'task_atomic/soft_deleted_original_token' \
    'mj_atomic/charge_quota_0_commit_mode_1' \
    'mj_atomic/charge_quota_0_commit_mode_2' \
    'mj_atomic/charge_quota_300_commit_mode_1' \
    'mj_atomic/charge_quota_300_commit_mode_2' \
    'mj_atomic/refund_tokens' \
    'mj_atomic/refund_channels' \
    'mj_atomic/refund_midjourneys' \
    'mj_atomic/refund_logs' \
    'mj_atomic/refund_lost_ack' \
    'mj_atomic/concurrent_refund_fresh_snapshots' \
    'mj_atomic/fail_closed_missing_identity' \
    'mj_atomic/fail_closed_missing_receipt' \
    'mj_atomic/fail_closed_source_only_refund' \
    'mj_atomic/fail_closed_source_only_charge' \
    'mj_atomic/uncharged_failure_never_refunds' \
    'mj_atomic/no_token_uses_explicit_identity' \
    'separate_log/midjourney_false' \
    'separate_log/midjourney_true' \
    'lifecycle_initial/wallet/source_receipt' \
    'lifecycle_initial/wallet/token' \
    'lifecycle_initial/wallet/held' \
    'lifecycle_initial/subscription/source_receipt' \
    'lifecycle_initial/subscription/token' \
    'lifecycle_initial/subscription/held' \
    'lifecycle_reserve/wallet/source_receipt' \
    'lifecycle_reserve/wallet/token' \
    'lifecycle_reserve/wallet/held' \
    'lifecycle_reserve/subscription/source_receipt' \
    'lifecycle_reserve/subscription/token' \
    'lifecycle_reserve/subscription/held' \
    'lifecycle_admission_failure/wallet' \
    'lifecycle_admission_failure/subscription' \
    'lifecycle_ready_recovery/wallet' \
    'lifecycle_ready_recovery/subscription' \
    'lifecycle_unknown_delivery/wallet' \
    'lifecycle_unknown_delivery/subscription' \
    'unknown_delivery_observability' \
    'unknown_delivery_observability/wallet' \
    'unknown_delivery_observability/wallet/write_failure' \
    'unknown_delivery_observability/wallet/reservation_probe' \
    'unknown_delivery_observability/wallet/receipt' \
    'unknown_delivery_observability/wallet/commit_rollback' \
    'unknown_delivery_observability/wallet/commit_ack_lost' \
    'unknown_delivery_observability/wallet/success_and_idempotent' \
    'unknown_delivery_observability/wallet/terminal_conflict' \
    'unknown_delivery_observability/subscription' \
    'unknown_delivery_observability/subscription/write_failure' \
    'unknown_delivery_observability/subscription/reservation_probe' \
    'unknown_delivery_observability/subscription/receipt' \
    'unknown_delivery_observability/subscription/commit_rollback' \
    'unknown_delivery_observability/subscription/commit_ack_lost' \
    'unknown_delivery_observability/subscription/success_and_idempotent' \
    'unknown_delivery_observability/subscription/terminal_conflict' \
    'unknown_delivery_observability/unavailable_identity_missing' \
    'unknown_delivery_observability/unavailable_identity_control' \
    'unknown_delivery_observability/unavailable_identity_oversized' \
    'recovery_mixed_legacy_progress' \
    'recovery_cursor_write_resume' \
    'recovery_cursor_write_resume/history_page=true' \
    'recovery_cursor_write_resume/history_page=false' \
    'recovery_scheduler_create_resume' \
    'lifecycle_trusted_zero' \
    'lifecycle_reservation_ack/wallet/initial' \
    'lifecycle_reservation_ack/wallet/reserve' \
    'lifecycle_reservation_ack/subscription/initial' \
    'lifecycle_reservation_ack/subscription/reserve' \
    'mj_request_owner/same_request_same_amount' \
    'mj_request_owner/same_request_different_amount' \
    'mj_request_owner/same_request_other_user' \
    'mj_request_owner/zero_request_duplicate' \
    'mj_request_owner/canceled_request_cannot_recharge' \
    'mj_request_owner/source_only_different_amount_fails_closed' \
    'mj_zero_refund_identity' \
    'financial_contention/wallet_settle_refund' \
    'financial_contention/task_refund' \
    'financial_contention/midjourney_refund' \
    'review_failure_usage/token_write_fault=false' \
    'review_failure_usage/token_write_fault=true' \
    'review_legacy_terminal/SUCCESS' \
    'review_legacy_terminal/FAILURE' \
    'review_zero_price_usage' \
    'review_task_recovery_fairness' \
    'review_mj_recovery_fairness' \
    'review_quota_export/separate_log=false' \
    'review_quota_export/separate_log=true' \
    'realtime_lifecycle/wallet/aggregate' \
    'realtime_lifecycle/wallet/segment_rollback' \
    'realtime_lifecycle/wallet/segment_lost_ack' \
    'realtime_lifecycle/wallet/final_lost_ack' \
    'realtime_lifecycle/wallet/partial_refund' \
    'realtime_lifecycle/wallet/zero_usage' \
    'realtime_lifecycle/wallet/zero_price' \
    'realtime_lifecycle/wallet/fixed_price' \
    'realtime_lifecycle/wallet/missing_total' \
    'realtime_lifecycle/subscription/aggregate' \
    'realtime_lifecycle/subscription/segment_rollback' \
    'realtime_lifecycle/subscription/segment_lost_ack' \
    'realtime_lifecycle/subscription/final_lost_ack' \
    'realtime_lifecycle/subscription/partial_refund' \
    'realtime_lifecycle/subscription/zero_usage' \
    'realtime_lifecycle/subscription/zero_price' \
    'realtime_lifecycle/subscription/fixed_price' \
    'realtime_lifecycle/subscription/missing_total' \
    'realtime_trusted' \
    'subscription_snapshot_unknown/fresh_replay' \
    'subscription_snapshot_unknown/reserve_lost_ack' \
    'unowned_hold/refund' \
    'unowned_hold/settle_zero' \
    'unowned_hold/settle_more' \
    'unowned_hold/handoff_zero' \
    'unowned_hold/handoff_more' \
    'unowned_hold/free_task_zero' \
    'audio_fixed_price/fixed_price' \
    'audio_fixed_price/token_ratios'; do
    required+=("$prefix/$contract")
  done
  if [ "$DIALECT" = sqlite ]; then
    # This standalone parent joins two sequential OS children using one SQLite file.
    standalone=TestBillingRecoveryFileRestart
    required+=("$standalone")
  else
    required+=("$prefix/settlement_deadline_row_lock")
    # Native handoff owns a separate empty DB, not the matrix's fixed-name tables.
    standalone=TestBillingRecoveryNativeSessionLeaseHandoff
    required+=("$standalone" "$standalone/$DIALECT")
  fi
  # Go runs the matching standalone parent even with a dialect subtest selector;
  # native subtests still select only this dialect, never an unconfigured skip.
  TEST_FINALIZATION_SERVICE_MYSQL_DSN="$finalization_service_mysql_dsn" TEST_FINALIZATION_SERVICE_POSTGRES_DSN="$finalization_service_postgres_dsn" \
    TEST_BILLING_RECOVERY_MYSQL_DSN="$billing_recovery_mysql_dsn" TEST_BILLING_RECOVERY_POSTGRES_DSN="$billing_recovery_postgres_dsn" \
    report request-finalization-service ./service "^(TestBillingFinalizationSessionDatabaseMatrix|$standalone)$/$DIALECT$" "${required[@]}"

  # Dedicated empty database: real WS fixtures may never share another matrix's tables.
  prefix="TestRealtimeFinancialConsistencyDatabaseMatrix/$DIALECT"
  required=("$prefix")
  for contract in 'financial/multiple_response_done' 'financial/segment_sql_failure_observed_once' 'financial/tiered_audio_and_cache_facts' 'financial/tiered_image_cache_presence' 'financial/tiered_mixed_cache_presence/known_first' 'financial/tiered_mixed_cache_presence/missing_first' 'termination/request_cancel' 'termination/upstream_close' 'termination/client_close'; do required+=("$prefix/$contract"); done
  TEST_FINALIZATION_REALTIME_MYSQL_DSN="$finalization_realtime_mysql_dsn" TEST_FINALIZATION_REALTIME_POSTGRES_DSN="$finalization_realtime_postgres_dsn" \
    report realtime-financial ./relay/channel/openai "^TestRealtimeFinancialConsistencyDatabaseMatrix$/$DIALECT$" "${required[@]}"

  # Native profiles must never turn a missing fixture DSN into skipped evidence.
  case "$DIALECT" in
    mysql) : "${TEST_MYSQL_DSN:?payment model requires its owned native DSN}" "${bonus_audit_mysql_dsn:?bonus audit requires its new empty database}" ;;
    postgres) : "${TEST_POSTGRES_DSN:?payment model requires its owned native DSN}" "${bonus_audit_postgres_dsn:?bonus audit requires its new empty database}" ;;
  esac
  top=TestPaymentPendingStatusDatabaseMatrix
  prefix="$top/$DIALECT"
  required=("$top" "$prefix")
  for provider in waffo waffo_pancake stripe subscription; do
    required+=("$prefix/$provider")
    for contract in pending_changes_only_status preserve_success preserve_failed preserve_expired identity_guards read write zero_rows commit_rollback commit_lost_ack; do
      required+=("$prefix/$provider/$contract")
    done
    if [ "$provider" = stripe ]; then required+=("$prefix/$provider/existing_expiry_contract"); fi
    if [ "$DIALECT" != sqlite ]; then
      # Controlled commit ordering is not an observed native database lock wait.
      required+=("$prefix/$provider/native_controlled_success_commit_before_failure_result")
    fi
  done
  report payment-pending-status ./model "^$top$/$DIALECT$" "${required[@]}"

  top=TestModelBonusEligibleAuditDatabaseMatrix
  prefix="$top/$DIALECT"
  required=("$top" "$prefix")
  for contract in no_op_and_missing_model_remain_unclaimed mixed_replay_applies_every_unclaimed_item audit_model_identity_must_match later_conflict_rolls_back_prior_item_even_if_current_matches later_audit_insert_rolls_back_entire_batch later_actual_duplicate_rolls_back_entire_batch later_model_update_rolls_back_entire_batch real_commit_lost_ack_preserves_audit_and_invalidates; do
    required+=("$prefix/$contract")
  done
  case "$DIALECT" in
    sqlite) isolations=(Default) ;;
    mysql) isolations=(Read_Committed Repeatable_Read) ;;
    postgres) isolations=(Read_Committed) ;;
  esac
  for isolation in "${isolations[@]}"; do
    required+=("$prefix/$isolation")
    for contract in false_then_true_conflict true_then_false_conflict true_then_null_conflict same_target_replay; do
      required+=("$prefix/$isolation/$contract")
    done
    if [ "$DIALECT" != sqlite ]; then
      required+=("$prefix/$isolation/overlapping_batch_replay_applies_unclaimed_item" "$prefix/$isolation/overlapping_batch_conflict_rolls_back_unclaimed_item")
    fi
    required+=("$prefix/$isolation/different_keys_preserve_actual_before")
    if [ "$DIALECT" != sqlite ]; then required+=("$prefix/$isolation/reverse_input_batches"); fi
  done
  selector="^$top$/$DIALECT$"
  if [ "$DIALECT" = sqlite ]; then
    # This pre-existing serial regression owns SQLite; never count it as native.
    standalone=TestBatchUpdateModelBonusEligibleWithAudit
    required+=("$standalone")
    selector="^($top|$standalone)$/$DIALECT$"
  fi
  # The fixed audit TableName bypasses table prefixes. Only this report receives
  # the freshly created empty DB; disposable service teardown owns its removal.
  TEST_BONUS_AUDIT_MYSQL_DSN="$bonus_audit_mysql_dsn" TEST_BONUS_AUDIT_POSTGRES_DSN="$bonus_audit_postgres_dsn" \
    report bonus-audit-race ./model "$selector" "${required[@]}"

  if [ "$DIALECT" = sqlite ]; then
    # Actual signed callbacks and the cross-process Stripe child are SQLite-only.
    # Pancake's slash-bearing t.Run name has no separate topup/subscription node.
    required=(
      'TestWaffoCreationFailurePreservesSuccessfulWebhook'
      'TestWaffoCreationFailurePreservesSuccessfulWebhook/transport'
      'TestWaffoCreationFailurePreservesSuccessfulWebhook/rejected_response'
      'TestWaffoCreationFailurePreservesSuccessfulWebhook/response_identity'
      'TestWaffoCreationFailurePreservesSuccessfulWebhook/provider_order_binding'
      'TestPancakeCreationFailurePreservesSuccessfulWebhook'
      'TestPancakeCreationFailurePreservesSuccessfulWebhook/topup/transport'
      'TestPancakeCreationFailurePreservesSuccessfulWebhook/topup/provider_session_binding'
      'TestPancakeCreationFailurePreservesSuccessfulWebhook/subscription/transport'
      'TestPancakeCreationFailurePreservesSuccessfulWebhook/subscription/provider_session_binding'
      'TestWaffoSDKFailureBeforeRequestMarksPendingFailed'
      'TestStripeDelayedFailurePreservesSuccessAcrossProcesses'
    )
    report payment-stale-callback ./controller '^(TestWaffoCreationFailurePreservesSuccessfulWebhook|TestWaffoSDKFailureBeforeRequestMarksPendingFailed|TestPancakeCreationFailurePreservesSuccessfulWebhook|TestStripeDelayedFailurePreservesSuccessAcrossProcesses)$' "${required[@]}"
    required=('TestPrepareFreeRealtimeBilling/wallet_only/model' 'TestPrepareFreeRealtimeBilling/wallet_only/group' 'TestPrepareFreeRealtimeBilling/subscription_only/model' 'TestPrepareFreeRealtimeBilling/subscription_only/group')
    report realtime-free-entry ./relay '^(TestPrepareFreeRealtimeBilling)$' "${required[@]}"
    required=('TestBonusEligibilityExpiresPreviouslyTrueCache' 'TestBonusEligibilityDoesNotRepublishInvalidatedSnapshot' 'TestBonusEligibilityCommittedMetadataRefreshControls' 'TestBonusEligibilityCommittedMetadataRefreshControls/update' 'TestBonusEligibilityCommittedMetadataRefreshControls/sync' 'TestBonusEligibilityCommittedMetadataRefreshControls/delete' 'TestBonusEligibilityDoesNotExtendPricingSnapshotLifetime' 'TestBonusEligibilityModelUpdateInvalidatesAfterCommit' 'TestBonusEligibilityRejectedMutationKeepsEligibility' 'TestBonusEligibilityCommitACKLossInvalidates' 'TestBonusEligibilityMutationCommitBoundaries' 'TestBonusEligibilityMutationCommitBoundaries/batch_commit' 'TestBonusEligibilityMutationCommitBoundaries/batch_lost_ack' 'TestBonusEligibilityMutationCommitBoundaries/delete_lost_ack' 'TestBonusEligibilityMutationCommitBoundaries/delete_pricing_lost_ack' 'TestBonusEligibilityMutationCommitBoundaries/sync_bonus_lost_ack' 'TestBonusEligibilityMutationCommitBoundaries/sync_name_rule_lost_ack')
    report financial-bonus-cache ./model '^(TestBonusEligibilityExpiresPreviouslyTrueCache|TestBonusEligibilityDoesNotRepublishInvalidatedSnapshot|TestBonusEligibilityCommittedMetadataRefreshControls|TestBonusEligibilityDoesNotExtendPricingSnapshotLifetime|TestBonusEligibilityModelUpdateInvalidatesAfterCommit|TestBonusEligibilityRejectedMutationKeepsEligibility|TestBonusEligibilityCommitACKLossInvalidates|TestBonusEligibilityMutationCommitBoundaries)$' "${required[@]}"
    report financial-bonus-cache-status ./controller '^TestModelBonusCacheStatusOnlyCommitACKLoss$' TestModelBonusCacheStatusOnlyCommitACKLoss
    required=('TestManageUserQuotaInputPresenceAndType/missing_override' 'TestManageUserQuotaInputPresenceAndType/null_override' 'TestManageUserQuotaInputPresenceAndType/explicit_zero_override' 'TestManageUserQuotaInputPresenceAndType/fractional_override' 'TestManageUserQuotaInputPresenceAndType/nonfinite_exponent_override' 'TestManageUserQuotaInputPresenceAndType/unsafe_integer_override' 'TestManageUserQuotaInputPresenceAndType/numeric_string_override' 'TestManageUserQuotaInputPresenceAndType/missing_nonquota' 'TestManageUserQuotaInputPresenceAndType/null_nonquota')
    report financial-admin-input ./controller '^TestManageUserQuotaInputPresenceAndType$' "${required[@]}"
  fi

  # Canceled refund dispatcher/cache/period/pending-recovery reports and their
  # databases are retired in billing_policy_replacement.json, never relabeled.
  if [ "$DIALECT" = sqlite ]; then
    # Retained sanitized error codes are not a refund retry/worker contract.
    required=(
      'TestBillingErrorClassificationExcludesSecretDetails'
      'TestBillingErrorClassificationExcludesSecretDetails/nil'
      'TestBillingErrorClassificationExcludesSecretDetails/unknown_secret'
      'TestBillingErrorClassificationExcludesSecretDetails/wrapped_unknown_secret'
      'TestBillingErrorClassificationExcludesSecretDetails/timeout'
      'TestBillingErrorClassificationExcludesSecretDetails/cancel'
      'TestBillingErrorClassificationExcludesSecretDetails/identity'
      'TestBillingErrorClassificationExcludesSecretDetails/missing'
      'TestBillingErrorClassificationExcludesSecretDetails/terminal_owner'
      'TestBillingErrorClassificationExcludesSecretDetails/ledger_conflict'
      'TestBillingErrorClassificationExcludesSecretDetails/ledger_invalid'
    )
    report billing-error-classifier ./model '^TestBillingErrorClassificationExcludesSecretDetails$' "${required[@]}"
    required=(
      'TestOfficialTokenRefundUsesBatchAndIndependentSQL'
      'TestOfficialTokenRefundUsesAsyncCacheWithoutPrivateFence'
      'TestBillingTokenContextPreservesFence'
      'TestBillingTokenRedisErrorRetainsLegacyLiveContextPolicy'
      'TestBillingTokenExpiredDeadlineBeforeNotificationRollsBackSource'
    )
    report official-token-refund ./model '^(TestOfficialTokenRefundUsesBatchAndIndependentSQL|TestOfficialTokenRefundUsesAsyncCacheWithoutPrivateFence|TestBillingTokenContextPreservesFence|TestBillingTokenRedisErrorRetainsLegacyLiveContextPolicy|TestBillingTokenExpiredDeadlineBeforeNotificationRollsBackSource)$' "${required[@]}"
    required=(
      'TestMidjourneyMissingChannelRequiresReviewWithoutRefund'
      'TestMidjourneyTransientChannelLookupCannotAuthorizeRefund'
      'TestMidjourneyEmptyUpstreamIDsHaveIndependentRefundReceipts'
      'TestMidjourneyRefundPreservesLegacyAndUnchargedReceipts'
      'TestMidjourneyRefundPreservesLegacyAndUnchargedReceipts/legacy_null'
      'TestMidjourneyRefundPreservesLegacyAndUnchargedReceipts/legacy_empty'
      'TestMidjourneyRefundPreservesLegacyAndUnchargedReceipts/prepared_uncharged'
      'TestMidjourneyCacheMissRequiresAuthoritativeAbsence'
    )
    report official-midjourney-refund ./controller '^(TestMidjourneyMissingChannelRequiresReviewWithoutRefund|TestMidjourneyTransientChannelLookupCannotAuthorizeRefund|TestMidjourneyEmptyUpstreamIDsHaveIndependentRefundReceipts|TestMidjourneyRefundPreservesLegacyAndUnchargedReceipts|TestMidjourneyCacheMissRequiresAuthoritativeAbsence)$' "${required[@]}"
    required=(
      'TestOfficialCompatibilityKeeps093RefundRemainder'
    )
    report official-wallet-allocation ./modules/chuanapi/wallet '^(TestOfficialCompatibilityKeeps093RefundRemainder)$' "${required[@]}"
    required=(
      'TestWalletSettlementSurvivesTokenDeletion'
      'TestWalletSettlementSurvivesTokenDeletion/soft/trusted'
      'TestWalletSettlementSurvivesTokenDeletion/soft/charge'
      'TestWalletSettlementSurvivesTokenDeletion/soft/refund'
      'TestWalletSettlementSurvivesTokenDeletion/hard/trusted'
      'TestWalletSettlementSurvivesTokenDeletion/hard/charge'
      'TestWalletSettlementSurvivesTokenDeletion/hard/refund'
      'TestWalletSettlementRejectsChangedTokenIdentity'
      'TestWalletSettlementRejectsChangedTokenIdentity/owner'
      'TestWalletSettlementRejectsChangedTokenIdentity/credential'
      'TestPostConsumeQuotaIdempotencyCoversWalletAndToken'
      'TestPostConsumeQuotaRefundIdempotencyCoversWalletAndToken'
      'TestPostConsumeQuotaRefundRequiresDebitReceipt'
      'TestPostConsumeQuotaRollsBackWalletWhenTokenUpdateFails'
      'TestBillingSessionWalletSettleRollsBackFundingWhenTokenUpdateFails'
      'TestBillingSessionWalletRefundSettleRollsBackFundingWhenTokenUpdateFails'
      'TestWalletSettleFailsClosedWithoutTransactionalSettlementCapability'
      'TestWalletReserveLedgerSequenceUnchanged'
      'TestWalletReceiptOrderValuesAndDefensiveCopy'
      'TestReceiptNilForFundingWithoutReceiptCapability'
      'TestSubscriptionReserveUsesTargetDeltaWithoutDoubleCount'
      'TestReserveFundingFailureRollsBackTokenAndRetryDebitsFresh'
      'TestNeedsRefundUsesOutstandingReserveCapability'
      'TestReserveFailsClosedWithoutReservableCapability'
      'TestStreamFailurePartialUsageSettlesOnceAndNeverRefunds'
      'TestStreamFailureWithoutUsageRefundsReservationOnce'
    )
    report normal-wallet-settlement ./service '^(TestWalletSettlementSurvivesTokenDeletion|TestWalletSettlementRejectsChangedTokenIdentity|TestPostConsumeQuotaIdempotencyCoversWalletAndToken|TestPostConsumeQuotaRefundIdempotencyCoversWalletAndToken|TestPostConsumeQuotaRefundRequiresDebitReceipt|TestPostConsumeQuotaRollsBackWalletWhenTokenUpdateFails|TestBillingSessionWalletSettleRollsBackFundingWhenTokenUpdateFails|TestBillingSessionWalletRefundSettleRollsBackFundingWhenTokenUpdateFails|TestWalletSettleFailsClosedWithoutTransactionalSettlementCapability|TestWalletReserveLedgerSequenceUnchanged|TestWalletReceiptOrderValuesAndDefensiveCopy|TestReceiptNilForFundingWithoutReceiptCapability|TestSubscriptionReserveUsesTargetDeltaWithoutDoubleCount|TestReserveFundingFailureRollsBackTokenAndRetryDebitsFresh|TestNeedsRefundUsesOutstandingReserveCapability|TestReserveFailsClosedWithoutReservableCapability|TestStreamFailurePartialUsageSettlesOnceAndNeverRefunds|TestStreamFailureWithoutUsageRefundsReservationOnce)$' "${required[@]}"
    required=(
      'TestPresentTaskSubmissionUsesNativePresenterAfterPersistence'
      'TestPresentTaskSubmissionFallbackUsesPersistedPublicID'
      'TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt'
      'TestExecuteTaskSubmissionPreservesHoldWhenAcceptedInsertFails'
      'TestExecuteTaskSubmissionRollsBackInsertedTaskWhenHandoffFails'
      'TestExecuteTaskSubmissionRejectsUnsupportedAtomicBilling'
      'TestExecuteTaskSubmissionPersistsPinnedPluginProvenance'
      'TestExecuteTaskSubmissionPersistsExecutionOnlyRequestID'
      'TestExecuteTaskSubmissionHonorsRouteRetainResult'
      'TestExecuteTaskSubmissionHonorsRouteRetainResult/immediate_success_is_discarded'
      'TestExecuteTaskSubmissionHonorsRouteRetainResult/immediate_failure_is_discarded'
      'TestExecuteTaskSubmissionHonorsRouteRetainResult/asynchronous_result_is_retained'
      'TestExecuteTaskSubmissionPreservesHoldOnCancellationAfterUpstreamAcceptance'
      'TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds'
      'TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier'
      'TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund'
      'TestAcceptedSubmitStreamNeverRetries'
      'TestRespondTaskSubmissionErrorWithoutCause'
      'TestExecuteTaskSubmissionPreservesHoldWhenAcceptedFinalReserveFails'
    )
    report official-task-submission ./controller '^(TestPresentTaskSubmissionUsesNativePresenterAfterPersistence|TestPresentTaskSubmissionFallbackUsesPersistedPublicID|TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt|TestExecuteTaskSubmissionPreservesHoldWhenAcceptedInsertFails|TestExecuteTaskSubmissionRollsBackInsertedTaskWhenHandoffFails|TestExecuteTaskSubmissionRejectsUnsupportedAtomicBilling|TestExecuteTaskSubmissionPersistsPinnedPluginProvenance|TestExecuteTaskSubmissionPersistsExecutionOnlyRequestID|TestExecuteTaskSubmissionHonorsRouteRetainResult|TestExecuteTaskSubmissionPreservesHoldOnCancellationAfterUpstreamAcceptance|TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds|TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier|TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund|TestAcceptedSubmitStreamNeverRetries|TestRespondTaskSubmissionErrorWithoutCause|TestExecuteTaskSubmissionPreservesHoldWhenAcceptedFinalReserveFails)$' "${required[@]}"
    report reminder-option-validation ./model '^TestQuotaReminderThresholdOptionsRejectInvalidWithoutMutation$' TestQuotaReminderThresholdOptionsRejectInvalidWithoutMutation
    report reminder-request-validation ./controller '^(TestQuotaReminderProfileThresholdValidationPreservesLegacyFractions|TestQuotaReminderSiteOptionRejectsMalformedWireWithoutWrites)$' TestQuotaReminderProfileThresholdValidationPreservesLegacyFractions TestQuotaReminderSiteOptionRejectsMalformedWireWithoutWrites
    report frontend-embedded-assets ./common '^TestEmbeddedFrontendOnlyExposesRegularAssets$' TestEmbeddedFrontendOnlyExposesRegularAssets
    report frontend-static-cache ./middleware '^(TestFrontendAssetCacheSecurityContract|TestCacheControlByPath|TestServeFrontendFilesCompressesEachFileOnce)$' TestFrontendAssetCacheSecurityContract TestCacheControlByPath TestServeFrontendFilesCompressesEachFileOnce
    report frontend-static-routing ./router '^(TestWebFallbackResponseSecurityContract|TestWebFallbackDoesNotCacheMissingAPIOrAssets|TestDisabledWebAssetsServeJSONNotFoundAndKeepPluginDispatcher|TestDisabledWebAssetsTakePrecedenceOverFrontendBaseUrlRedirect|TestDisabledWebAssetsKeepRegisteredAPIRoutes)$' TestWebFallbackResponseSecurityContract TestWebFallbackDoesNotCacheMissingAPIOrAssets TestDisabledWebAssetsServeJSONNotFoundAndKeepPluginDispatcher TestDisabledWebAssetsTakePrecedenceOverFrontendBaseUrlRedirect TestDisabledWebAssetsKeepRegisteredAPIRoutes
    report token-auth-route-expiry ./middleware '^TestUserAuthAppliesAccessTokenRouteRules$' TestUserAuthAppliesAccessTokenRouteRules
    report newapi-balance-finite ./controller '^TestUpdateChannelNewAPIBalanceRejectsNonFiniteConversion$' TestUpdateChannelNewAPIBalanceRejectsNonFiniteConversion
    report token-permissions ./controller '^(TestTokenAllowUserQuotaQueryOnCreateAndUpdate|TestGetTokenUsageFollowsTheUserQuotaSwitchAndIPWhitelist|TestGetTokenRoutableModelsUsesOwnedTokenScopeWithoutCLIApp)$' TestTokenAllowUserQuotaQueryOnCreateAndUpdate TestGetTokenUsageFollowsTheUserQuotaSwitchAndIPWhitelist TestGetTokenRoutableModelsUsesOwnedTokenScopeWithoutCLIApp
    report token-cache-permissions ./model '^(TestTokenAllowUserQuotaQueryRoundTripsThroughRedisHashCache|TestTokenUpdateRevokesCachedUserQuotaQueryPermission|TestRootAffCodeFixtureRejectsRemoteDatabases)$' TestTokenAllowUserQuotaQueryRoundTripsThroughRedisHashCache TestTokenUpdateRevokesCachedUserQuotaQueryPermission TestRootAffCodeFixtureRejectsRemoteDatabases
    report admin-route-authorization ./router '^(TestTokenRoutableModelsRouteRequiresOwnedTokenAndReadScope|TestAccessTokenRouteRulesCoverEveryDashboardRoute|TestChannelStatusRoutesUseExpectedPermissions)$' TestTokenRoutableModelsRouteRequiresOwnedTokenAndReadScope TestAccessTokenRouteRulesCoverEveryDashboardRoute TestChannelStatusRoutesUseExpectedPermissions
    report notification-preferences ./controller '^TestUpdateUserSettingKeepsWhatTheRequestLeavesOut$' TestUpdateUserSettingKeepsWhatTheRequestLeavesOut
    report notification-delivery ./service '^(TestQuotaReminderFiresOncePerDrop|TestChannelNoticesFollowEachAdministratorsSwitch|TestCheckNotificationLimitWindowSpansClockHours|TestCheckNotificationLimitRedisWindowSpansClockHours)$' TestQuotaReminderFiresOncePerDrop TestChannelNoticesFollowEachAdministratorsSwitch TestCheckNotificationLimitWindowSpansClockHours TestCheckNotificationLimitRedisWindowSpansClockHours
    report transactional-email ./controller '^(TestResend|TestSMTPAndLegacyConfigurationNeverUseResend|TestEmailOptionsNeverReturnResendCredentials|TestEmailProvider|TestEmailKeyStatusIsDerivedAndReadOnly|TestSecurityAccountMailsFollowTheOwnersLanguage)' TestResendDeliversThroughSelectedProvider TestResendFailuresDoNotRetryFallbackOrExposeSensitiveContent TestResendRejectsIncompleteOrInvalidConfigurationBeforeSending TestResendRejectsMalformedSuccessAndSanitizesTransportErrors TestSMTPAndLegacyConfigurationNeverUseResend TestEmailOptionsNeverReturnResendCredentials TestEmailProviderOptionRejectsInvalidValues TestEmailProviderCannotActivateIncompleteResend TestResendSettingsChangesKeepActiveDeliveryWorking TestEmailKeyStatusIsDerivedAndReadOnly TestResendPreservesExplicitPlainTextForTransactionalEmails TestSecurityAccountMailsFollowTheOwnersLanguage
    report runtime-resources ./common '^(TestManagedSystemMonitorClearsDisabledStatusAndStopsOnDrain|TestContainer.*|TestSystemOverloadRequiresConsecutiveSamples|TestCheckSystemOverload|TestResendFailuresKeepCredentialsAndEmailContentsOutOfLogs)$' TestManagedSystemMonitorClearsDisabledStatusAndStopsOnDrain TestContainerCPUSaturation TestContainerCPUSaturationFallsBackWithoutQuota TestContainerMemoryUsageUsesWorkingSet TestContainerMemoryUsageFallsBackWhenUnavailable TestSystemOverloadRequiresConsecutiveSamples TestCheckSystemOverload TestResendFailuresKeepCredentialsAndEmailContentsOutOfLogs
    report runtime-drain ./pkg/lifecycle '^(TestForceCancellationIsSeparateFromGracefulDrain|TestWorkerPanicIsReportedAndDoesNotLeakTheJoin|TestWorkerFailureDuringDrainRemainsObservable|TestCompletedDrainWinsOverCancelledWait|TestCompletedWorkerFailureWinsOverCancelledWait)$' TestForceCancellationIsSeparateFromGracefulDrain TestWorkerPanicIsReportedAndDoesNotLeakTheJoin TestWorkerFailureDuringDrainRemainsObservable TestCompletedDrainWinsOverCancelledWait TestCompletedWorkerFailureWinsOverCancelledWait
    prefix=TestTaskSettlementClampPersistence
    report settlement-clamp ./model '^TestTaskSettlementClampPersistence$' "$prefix/positive_infinity" "$prefix/negative_infinity" "$prefix/nan"
    prefix=TestConsumeLogExportsNormalizedCacheUsage
    report dashboard-consume-cache ./model '^TestConsumeLogExportsNormalizedCacheUsage$' "$prefix/normalized_cache_totals" "$prefix/missing_cache_usage" "$prefix/negative_cache_usage"
    report dashboard-separate-log-cache ./model '^TestConsumeLogExportsCacheUsageWithSeparateLogDatabase$' TestConsumeLogExportsCacheUsageWithSeparateLogDatabase
    prefix=TestRelayTaskSubmitRetryWalletDatabase
    report retry ./relay '^TestRelayTaskSubmitRetryWalletDatabase$' "$prefix" "$prefix/higher_quote_reserved_before_paid_request" "$prefix/wallet_shortage_never_calls_retry_upstream" "$prefix/token_shortage_never_calls_retry_upstream" "$prefix/bonus_ineligible_retry_keeps_bonus_untouched" "$prefix/per_call_retry_clears_expression_and_refunds_all"
    prefix=TestRelayTaskSensitiveRejectionPrecedesPluginAndBilling
    report task-sensitive ./relay '^TestRelayTaskSensitiveRejectionPrecedesPluginAndBilling$' "$prefix/json" "$prefix/vendor_json" "$prefix/normalized_vendor_prompt"
    report owner-visibility ./controller '^(TestPublicModelsShareOwnerAndVisibility|TestListModelsPublicOwnerOverridesWithoutLeakingOtherChannels|TestPublicModelDetailPreservesNativeSchemasAndBillingVisibility|TestBuildOpenAIModelUsesGlobalOwnerForStaticAndCustomModels)$' TestPublicModelsShareOwnerAndVisibility TestListModelsPublicOwnerOverridesWithoutLeakingOtherChannels TestPublicModelDetailPreservesNativeSchemasAndBillingVisibility TestBuildOpenAIModelUsesGlobalOwnerForStaticAndCustomModels
    report group-limits ./middleware '^TestModelRateLimitCountsConfiguredGroupsSeparately$' TestModelRateLimitCountsConfiguredGroupsSeparately
    report sensitive-groups ./service '^(TestRequestSensitiveExemptionRequiresAuthorizedActualRoute|TestRequestSensitiveRechecksAutoCrossGroupRetry|TestTaskSensitivePolicyChecksExtractedTextAndPreservesBody|TestRequestSensitiveDisabledSwitchesRemainDisabled)$' TestRequestSensitiveExemptionRequiresAuthorizedActualRoute TestRequestSensitiveRechecksAutoCrossGroupRetry TestTaskSensitivePolicyChecksExtractedTextAndPreservesBody TestRequestSensitiveDisabledSwitchesRemainDisabled
    prefix=TestRequestSensitiveHTTPAndWebSocketBeforeBilling
    required=("$prefix" "$prefix/http/fixed_channel_mismatch")
    for transport in http websocket; do
      for scenario in default_cannot_spoof_group authorized_exemption auto_cross-group_retry auto_token_scope unauthorized_group; do
        required+=("$prefix/$transport/$scenario")
      done
    done
    report sensitive-relay ./controller '^TestRequestSensitiveHTTPAndWebSocketBeforeBilling$' "${required[@]}"
    prefix=TestTaskSensitivePolicyChecksSelectedPluginNormalizationBeforeDriver
    required=()
    for format in json multipart; do
      for scenario in selected_provider_prompt_is_blocked discarded_provider_prompt_does_not_block disabled_policy_still_permits_the_driver; do
        required+=("$prefix/$format/$scenario")
      done
    done
    report sensitive-task-selected-plugin ./controller '^TestTaskSensitivePolicyChecksSelectedPluginNormalizationBeforeDriver$' "${required[@]}"
  fi
  ;;
redis)
  : "${REDIS_ID:?a real Redis service is required}" "${REDIS_PORT:?a real Redis port is required}"
  : "${REDIS_EXPECTED_VERSION:?an exact Redis version is required}"
  if [[ ! "$REDIS_EXPECTED_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo 'An exact numeric Redis release is required.' >&2
    exit 1
  fi
  printf '%s\n' "$REDIS_EXPECTED_VERSION" > "$OUT/redis-expected-version.txt"
  docker inspect --format '{{.Config.Image}} {{.Image}}' "$REDIS_ID" > "$OUT/redis-image.txt"
  docker exec "$REDIS_ID" redis-cli INFO server > "$OUT/redis-version.txt"
  # A requested image tag is not evidence of the server actually exercised.
  redis_version="$(awk '/^redis_version:/ { sub(/\r$/, ""); print }' "$OUT/redis-version.txt")"
  if [ "$redis_version" != "redis_version:$REDIS_EXPECTED_VERSION" ]; then
    echo 'Redis server version is missing, ambiguous, or differs from the required release.' >&2
    exit 1
  fi
  export TEST_RUNTIME_REDIS=1 TEST_REDIS_DSN="redis://127.0.0.1:$REDIS_PORT/0" TEST_REDIS_ADDR="127.0.0.1:$REDIS_PORT"
  report model-script-flush ./middleware '^TestModelRateLimitRealRedis$' TestModelRateLimitRealRedis/reservation_replay_and_expiry TestModelRateLimitRealRedis/script_flush_resumes_request_pipeline
  report token-bucket-script-flush ./common/limiter '^TestAllowFallsBackToEvalAfterScriptFlush$/real_redis$' TestAllowFallsBackToEvalAfterScriptFlush/real_redis
  # The deleted task-batch restart/recovery fixture is retired explicitly.
  # Normal native Redis rate-limit and settlement deadline fixtures remain.
  # The CI-owned Redis service was version-checked above. These opt-ins are
  # scoped to the pause/unpause fixture, never inherited by other reports.
  TEST_SETTLEMENT_DEADLINE_REDIS_ADDR="127.0.0.1:$REDIS_PORT" TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE=1 \
    report settlement-deadline-redis ./service '^TestBillingSettlementDeadlineNativeRedisFence$' TestBillingSettlementDeadlineNativeRedisFence
  ;;
*) echo 'Usage: verify_repair_services.sh database|redis' >&2; exit 1 ;;
esac

if ! git diff --exit-code --ignore-space-at-eol; then status=1; fi
if [ "$status" -ne 0 ]; then
  echo 'Required repair contracts failed; inspect the retained exact-SHA evidence.' >&2
  exit "$status"
fi
printf 'All required %s contracts passed on %s\n' "$1" "$SOURCE_SHA"
