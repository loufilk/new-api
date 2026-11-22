"""Exercise CI shell orchestration with offline command stubs, not real services."""
import importlib.util
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "verify_repair_services.sh"
DSN_KEYS = ("TEST_MYSQL_DSN", "TEST_MYSQL_LOG_DSN", "TEST_POSTGRES_DSN", "TEST_POSTGRES_LOG_DSN",
            "TEST_DATABASE_DSN", "TEST_LOG_DATABASE_DSN", "AUDIT_MYSQL_DSN", "AUDIT_POSTGRES_DSN",
            "TEST_PERF_MYSQL_DSN", "TEST_PERF_POSTGRES_DSN", "TEST_FIXED_MYSQL_DSN", "TEST_FIXED_MYSQL_LOG_DSN",
            "TEST_FIXED_POSTGRES_DSN", "TEST_FIXED_POSTGRES_LOG_DSN", "TEST_DATABASE_COMPAT",
            "TEST_DATABASE_DIALECT", "TEST_TASK_DB_DIALECT", "TEST_RUNTIME_REDIS",
            "TEST_REDIS_DSN", "TEST_REDIS_ADDR", "TEST_TASK_REDIS_ADDR",
            "TEST_FINALIZATION_MODEL_MYSQL_DSN", "TEST_FINALIZATION_MODEL_POSTGRES_DSN",
            "TEST_FINALIZATION_SERVICE_MYSQL_DSN", "TEST_FINALIZATION_SERVICE_POSTGRES_DSN",
            "TEST_BILLING_RECOVERY_MYSQL_DSN", "TEST_BILLING_RECOVERY_POSTGRES_DSN",
            "TEST_BONUS_AUDIT_MYSQL_DSN", "TEST_BONUS_AUDIT_POSTGRES_DSN", "PAYMENT_STALE_STRIPE_DSN", "PAYMENT_STALE_FIXTURE_CHILD",
            "NEW_API_TEST_RECOVERY_RESTART_FILE", "NEW_API_TEST_RECOVERY_RESTART_PHASE", "NEW_API_TEST_RECOVERY_NATIVE_CLAIM",
            "TEST_FINALIZATION_REALTIME_MYSQL_DSN", "TEST_FINALIZATION_REALTIME_POSTGRES_DSN",
            "TEST_REFUND_OPS_MYSQL_DSN", "TEST_REFUND_OPS_POSTGRES_DSN", "TEST_REFUND_QUEUE_PRESSURE",
            "TEST_FINALIZATION_CACHE_MYSQL_DSN", "TEST_FINALIZATION_CACHE_POSTGRES_DSN",
            "TEST_SETTLEMENT_DEADLINE_REDIS_ADDR", "TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE")


class RepairServiceEnvironmentTests(unittest.TestCase):
    def run_script(self, dialect, reject_label="", creation_fails=False, *, mode="database",
                   redis_version="8.8.0", redis_info="# Server\r\nredis_version:8.8.0\r\n",
                   redis_info_fails=False, finalization_creation_fails="", refund_operations_creation_fails=False,
                   task_submission_creation_fails=False, billing_recovery_creation_fails=False,
                   bonus_audit_creation_fails=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            tools = root / "bin"
            tools.mkdir()
            trace = root / "trace.jsonl"
            stub = tools / "tool.py"
            stub.write_text("#!" + sys.executable + "\n" + '''import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
record = {"tool": name, "args": args}
if name == "go" and args != ["version"]:
    record["env"] = {key: os.environ.get(key, "") for key in ''' + repr(DSN_KEYS) + '''}
with open(os.environ["MOCK_TRACE"], "a") as stream:
    stream.write(json.dumps(record) + "\\n")
if name == "git" and args == ["rev-parse", "HEAD"]:
    print(os.environ["SOURCE_SHA"])
elif name == "go" and args == ["version"]:
    print("offline orchestration stub; no Go tests executed")
elif name == "docker" and os.environ.get("MOCK_CREATE_FAIL") == "1" and any("repair_token_migration" in arg for arg in args):
    raise SystemExit(1)
elif name == "docker" and os.environ.get("MOCK_FINALIZATION_CREATE_FAIL") and any("repair_finalization_" + os.environ["MOCK_FINALIZATION_CREATE_FAIL"] in arg for arg in args):
    raise SystemExit(1)
elif name == "docker" and os.environ.get("MOCK_TASK_SUBMISSION_CREATE_FAIL") == "1" and any("repair_task_submission" in arg for arg in args):
    raise SystemExit(1)
elif name == "docker" and os.environ.get("MOCK_BILLING_RECOVERY_CREATE_FAIL") == "1" and any("repair_billing_recovery" in arg for arg in args):
    raise SystemExit(1)
elif name == "docker" and os.environ.get("MOCK_BONUS_AUDIT_CREATE_FAIL") == "1" and any("repair_bonus_audit" in arg for arg in args):
    raise SystemExit(1)
elif name == "docker" and os.environ.get("MOCK_REFUND_OPERATIONS_CREATE_FAIL") == "1" and any("repair_refund_operations" in arg for arg in args):
    raise SystemExit(1)
elif name == "docker" and args[-3:] == ["redis-cli", "INFO", "server"]:
    if os.environ.get("MOCK_REDIS_INFO_FAIL") == "1":
        raise SystemExit(1)
    sys.stdout.write(os.environ["MOCK_REDIS_INFO"])
elif name == "python3":
    assert args[0] == "ops/ci/check_go_test_report.py"
    assert args[1] == "--reject-skipped-descendants"
    assert len(args) > 3
    if pathlib.Path(args[2]).stem == os.environ.get("MOCK_REJECT_LABEL"):
        raise SystemExit(1)
''')
            stub.chmod(0o755)
            for name in ("git", "go", "docker", "python3"):
                (tools / name).symlink_to(stub)
            env = {"PATH": str(tools) + os.pathsep + os.environ["PATH"], "HOME": str(root),
                   "OUT": str(root / "evidence"), "SOURCE_SHA": "a" * 40, "DIALECT": dialect,
                   "MYSQL_ID": "b" * 64, "MYSQL_PORT": "13306", "PG_ID": "c" * 64, "PG_PORT": "15432",
                   "AUDIT_MYSQL_DSN": "untrusted-ambient-fixture", "AUDIT_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_MODEL_MYSQL_DSN": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_MODEL_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_SERVICE_MYSQL_DSN": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_SERVICE_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "TEST_BILLING_RECOVERY_MYSQL_DSN": "untrusted-ambient-fixture",
                   "TEST_BILLING_RECOVERY_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "TEST_BONUS_AUDIT_MYSQL_DSN": "untrusted-ambient-fixture",
                   "TEST_BONUS_AUDIT_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "PAYMENT_STALE_STRIPE_DSN": "untrusted-ambient-fixture",
                   "PAYMENT_STALE_FIXTURE_CHILD": "untrusted-ambient-child",
                   "NEW_API_TEST_RECOVERY_RESTART_FILE": "untrusted-ambient-fixture",
                   "NEW_API_TEST_RECOVERY_RESTART_PHASE": "resume",
                   "NEW_API_TEST_RECOVERY_NATIVE_CLAIM": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_REALTIME_MYSQL_DSN": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_REALTIME_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "TEST_REFUND_OPS_MYSQL_DSN": "untrusted-ambient-fixture",
                   "TEST_REFUND_OPS_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "TEST_REFUND_QUEUE_PRESSURE": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_CACHE_MYSQL_DSN": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_CACHE_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "TEST_TASK_REDIS_ADDR": "untrusted-ambient-fixture",
                   "TEST_SETTLEMENT_DEADLINE_REDIS_ADDR": "untrusted-ambient-fixture",
                   "TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE": "untrusted-ambient-fixture",
                   "MOCK_REFUND_OPERATIONS_CREATE_FAIL": "1" if refund_operations_creation_fails else "0",
                   "MOCK_FINALIZATION_CREATE_FAIL": finalization_creation_fails,
                   "MOCK_TASK_SUBMISSION_CREATE_FAIL": "1" if task_submission_creation_fails else "0",
                   "MOCK_BILLING_RECOVERY_CREATE_FAIL": "1" if billing_recovery_creation_fails else "0",
                   "MOCK_BONUS_AUDIT_CREATE_FAIL": "1" if bonus_audit_creation_fails else "0",
                   "MOCK_TRACE": str(trace), "MOCK_REJECT_LABEL": reject_label,
                   "MOCK_CREATE_FAIL": "1" if creation_fails else "0",
                   "REDIS_ID": "d" * 64, "REDIS_PORT": "16379", "REDIS_EXPECTED_VERSION": redis_version,
                   "MOCK_REDIS_INFO": redis_info, "MOCK_REDIS_INFO_FAIL": "1" if redis_info_fails else "0"}
            result = subprocess.run(["bash", str(SCRIPT), mode], cwd=root, env=env,
                                    text=True, capture_output=True, timeout=20)
            records = [json.loads(line) for line in trace.read_text().splitlines()]
            return result, records

    def report_contracts(self, rows):
        tests = [row for row in rows if row["tool"] == "go" and row["args"] != ["version"]]
        gates = [row for row in rows if row["tool"] == "python3"]
        self.assertEqual(len(tests), len(gates), "every suite must retain its strict evidence gate")
        contracts = {}
        for test, gate in zip(tests, gates):
            self.assertEqual(["test", "-race", "-p", "2"], test["args"][:4])
            self.assertEqual(["-count=1", "-timeout=12m", "-json"], test["args"][-3:])
            self.assertEqual(["ops/ci/check_go_test_report.py", "--reject-skipped-descendants"], gate["args"][:2])
            label = Path(gate["args"][2]).stem
            require_run = gate["args"][-1] == "--require-run"
            self.assertEqual(label in {"payment-pending-status", "bonus-audit-race", "payment-stale-callback", "financial-bonus-cache"}, require_run)
            # Preserve the raw command trace; normalize only this assertion view.
            normalized = dict(gate, require_run=require_run,
                              args=gate["args"][:-1] if require_run else gate["args"])
            self.assertNotIn(label, contracts, "reports cannot silently overwrite one another")
            contracts[label] = (test, normalized)
            for key in ("NEW_API_TEST_RECOVERY_RESTART_FILE", "NEW_API_TEST_RECOVERY_RESTART_PHASE", "NEW_API_TEST_RECOVERY_NATIVE_CLAIM", "PAYMENT_STALE_STRIPE_DSN", "PAYMENT_STALE_FIXTURE_CHILD"):
                self.assertEqual("", test["env"][key], "only the test parent may authorize a child phase")
            if Path(gate["args"][2]).stem != "request-finalization-service":
                for key in ("TEST_BILLING_RECOVERY_MYSQL_DSN", "TEST_BILLING_RECOVERY_POSTGRES_DSN"):
                    self.assertEqual("", test["env"][key], "native recovery DSNs must not leak to other reports")
            if label != "bonus-audit-race":
                for key in ("TEST_BONUS_AUDIT_MYSQL_DSN", "TEST_BONUS_AUDIT_POSTGRES_DSN"):
                    self.assertEqual("", test["env"][key], "native audit DSNs must not leak to other reports")
        return contracts

    def expected_labels(self, dialect):
        policy = json.loads((SCRIPT.parent / "billing_policy_replacement.json").read_text())
        return policy["report_order"][dialect]

    def test_real_database_selectors_receive_isolated_and_matching_dsns(self):
        for dialect, selected, inactive, port in (("mysql", "MYSQL", "POSTGRES", "13306"),
                                                 ("postgres", "POSTGRES", "MYSQL", "15432")):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                contracts = self.report_contracts(rows)
                self.assertEqual(self.expected_labels(dialect), list(contracts))
                for label, (test, gate) in contracts.items():
                    env = test["env"]
                    self.assertEqual("1", env["TEST_DATABASE_COMPAT"])
                    self.assertEqual(dialect, env["TEST_DATABASE_DIALECT"])
                    self.assertEqual("", env[f"TEST_{inactive}_DSN"])
                    self.assertEqual("", env[f"AUDIT_{inactive}_DSN"])
                    main = env["TEST_DATABASE_DSN"]
                    self.assertIn("127.0.0.1:" + port, main)
                    self.assertIn("/repair_compat?", main)
                    self.assertEqual(main, env[f"AUDIT_{selected}_DSN"])
                    actual = env[f"TEST_{selected}_DSN"]
                    if label == "token-migration":
                        self.assertIn("/repair_token_migration?", actual)
                        self.assertNotEqual(main, actual, "legacy migration may never reuse the startup database")
                    elif label == "immediate-settlement":
                        self.assertIn("/repair_task_submission?", actual)
                        self.assertNotEqual(main, actual, "fixed-name task ledger fixtures must not reuse the startup database")
                    else:
                        self.assertEqual(main, actual, "fresh fixture DSN overrides must not leak into another suite")
                    if label.startswith("performance-"):
                        self.assertEqual(main, env[f"TEST_PERF_{selected}_DSN"])
                    if label in ("native-responses", "converted-responses"):
                        self.assertEqual(main, env[f"TEST_FIXED_{selected}_DSN"])
                        self.assertEqual(env["TEST_LOG_DATABASE_DSN"], env[f"TEST_FIXED_{selected}_LOG_DSN"])
                    if label == "immediate-settlement":
                        self.assertEqual(dialect, env["TEST_TASK_DB_DIALECT"])
                creation = [row for row in rows if row["tool"] == "docker" and any("repair_token_migration" in arg for arg in row["args"])]
                self.assertEqual(1, len(creation))
                self.assertNotIn("IF NOT EXISTS", " ".join(creation[0]["args"]))
                self.assertNotIn("DROP", " ".join(creation[0]["args"]))

    def test_sqlite_cannot_inherit_external_audit_dsns(self):
        result, rows = self.run_script("sqlite")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertFalse(any(row["tool"] == "docker" for row in rows))
        contracts = self.report_contracts(rows)
        self.assertEqual(self.expected_labels("sqlite"), list(contracts))
        for test, _ in contracts.values():
            for key in DSN_KEYS:
                if key.endswith("DSN"):
                    self.assertEqual("", test["env"][key], key)

    def test_required_rejection_fails_job_but_keeps_independent_evidence(self):
        for label in ("token-migration", "token-audit", "official-subscription-refund", "subscription-period-migration",
                      "request-finalization-model", "request-finalization-service", "realtime-financial",
                      "subscription-financial-consistency", "subscription-supplement", "financial-review-read-only"):
            with self.subTest(label=label):
                result, rows = self.run_script("postgres", reject_label=label)
                self.assertNotEqual(0, result.returncode)
                self.assertIn("Required repair contracts failed", result.stderr)
                self.assertEqual(self.expected_labels("postgres"), list(self.report_contracts(rows)))

    def test_existing_migration_database_cannot_be_reused_after_create_failure(self):
        for dialect in ("mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect, creation_fails=True)
                self.assertNotEqual(0, result.returncode)
                self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))

    def test_task_submission_database_is_created_once_and_failure_cannot_reuse_it(self):
        for dialect in ("mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                creation = [row for row in rows if row["tool"] == "docker" and any("repair_task_submission" in arg for arg in row["args"])]
                self.assertEqual(1, len(creation))
                command = " ".join(creation[0]["args"])
                self.assertNotIn("IF NOT EXISTS", command)
                self.assertNotIn("DROP", command)
                tests = [row for row in rows if row["tool"] == "go" and row["args"] != ["version"]]
                self.assertLess(rows.index(creation[0]), rows.index(tests[0]))
                result, rows = self.run_script(dialect, task_submission_creation_fails=True)
                self.assertNotEqual(0, result.returncode)
                self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))

    def test_recovery_native_database_is_created_once_and_failure_cannot_reuse_it(self):
        for dialect in ("mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                creation = [row for row in rows if row["tool"] == "docker" and any("repair_billing_recovery" in arg for arg in row["args"])]
                self.assertEqual(1, len(creation))
                command = " ".join(creation[0]["args"])
                self.assertNotIn("IF NOT EXISTS", command)
                self.assertNotIn("DROP", command)
                tests = [row for row in rows if row["tool"] == "go" and row["args"] != ["version"]]
                self.assertLess(rows.index(creation[0]), rows.index(tests[0]))
                result, rows = self.run_script(dialect, billing_recovery_creation_fails=True)
                self.assertNotEqual(0, result.returncode)
                self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))

    def test_finalization_matrices_use_distinct_fresh_databases_and_all_current_leaves(self):
        inventory = json.loads((SCRIPT.parent / "billing_finalization_required_cases.json").read_text())
        suites = {suite["package"]: suite for suite in inventory["suites"]}
        self.assertEqual({"model", "service"}, set(suites))
        self.assertEqual(["settlement_deadline_row_lock"], suites["service"]["native_required_leaf_suffixes"])
        self.assertEqual({
            "sqlite": ["TestBillingRecoveryFileRestart"],
            "mysql": ["TestBillingRecoveryNativeSessionLeaseHandoff", "TestBillingRecoveryNativeSessionLeaseHandoff/mysql"],
            "postgres": ["TestBillingRecoveryNativeSessionLeaseHandoff", "TestBillingRecoveryNativeSessionLeaseHandoff/postgres"],
        }, suites["service"]["standalone_required_tests"])
        recovery = ["recovery_mixed_legacy_progress", "recovery_cursor_write_resume",
                    "recovery_cursor_write_resume/history_page=true", "recovery_cursor_write_resume/history_page=false",
                    "recovery_scheduler_create_resume", "unknown_delivery_observability"]
        for source in ("wallet", "subscription"):
            recovery.append("unknown_delivery_observability/" + source)
            recovery += ["unknown_delivery_observability/" + source + "/" + leaf for leaf in (
                "write_failure", "reservation_probe", "receipt", "commit_rollback", "commit_ack_lost",
                "success_and_idempotent", "terminal_conflict")]
        recovery += ["unknown_delivery_observability/unavailable_identity_" + kind for kind in ("missing", "control", "oversized")]
        for suffix in recovery:
            self.assertIn(suffix, suites["service"]["required_leaf_suffixes"])
        for suffix in ("legacy_final_more_less/Å", "legacy_final_more_less/ĕ",
                       "commit_boundaries/lost_ack/settlement", "commit_boundaries/rollback/settlement",
                       "real_session_partial_less", "real_session_partial_more", "real_session_trusted",
                       "real_session_playground", "real_session_unlimited",
                       "official_task_independent_steps/source", "official_task_independent_steps/token",
                       "official_task_independent_steps/quota_clear", "historical_pending_read_only/private_only",
                       "official_post_commit_errors/request_token", "official_post_commit_errors/task_quota_clear",
                       "official_refund_before_lost_ack_readback"):
            self.assertIn(suffix, suites["service"]["required_leaf_suffixes"])
        for dialect in ("sqlite", "mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                contracts = self.report_contracts(rows)
                self.assertEqual(self.expected_labels(dialect), list(contracts))
                for label, (invocation, check) in contracts.items():
                    env = invocation["env"]
                    for package, suite in suites.items():
                        active = label == "request-finalization-" + package
                        for engine, key in suite["dsn_env"].items():
                            if active and dialect == engine:
                                self.assertIn("/repair_finalization_" + package + "?", env[key])
                                self.assertNotEqual(env["TEST_DATABASE_DSN"], env[key])
                                self.assertIn("127.0.0.1:", env[key])
                            else:
                                self.assertEqual("", env[key], (label, key))
                        if active:
                            prefix = suite["top"] + "/" + dialect
                            expected = [prefix] + [prefix + "/" + leaf for leaf in suite["required_leaf_suffixes"]]
                            if dialect != "sqlite":
                                expected += [prefix + "/" + leaf for leaf in suite.get("native_required_leaf_suffixes", [])]
                            standalone = suite.get("standalone_required_tests", {}).get(dialect, [])
                            expected += standalone
                            self.assertEqual(expected, check["args"][3:])
                            self.assertEqual("./" + package, invocation["args"][4])
                            pattern = f"^{suite['top']}$/{dialect}$"
                            if standalone:
                                pattern = f"^({suite['top']}|{standalone[0]})$/{dialect}$"
                            self.assertEqual(pattern, invocation["args"][6])
                    for engine in ("mysql", "postgres"):
                        key = "TEST_BILLING_RECOVERY_" + engine.upper() + "_DSN"
                        if label == "request-finalization-service" and dialect == engine:
                            self.assertIn("/repair_billing_recovery?", env[key])
                            self.assertIn("127.0.0.1:", env[key])
                            self.assertNotEqual(env["TEST_DATABASE_DSN"], env[key])
                            self.assertNotEqual(env["TEST_FINALIZATION_SERVICE_" + engine.upper() + "_DSN"], env[key])
                        else:
                            self.assertEqual("", env[key], (label, key))
                if dialect != "sqlite":
                    for package in suites:
                        matching = [row for row in rows if row["tool"] == "docker" and
                                    any("repair_finalization_" + package in arg for arg in row["args"])]
                        self.assertEqual(1, len(matching))
                        command = " ".join(matching[0]["args"])
                        self.assertNotIn("IF NOT EXISTS", command)
                        self.assertNotIn("DROP", command)
                        self.assertLess(rows.index(matching[0]), rows.index(next(iter(contracts.values()))[0]))

    def test_finalization_database_creation_failure_stops_before_tests(self):
        for dialect in ("mysql", "postgres"):
            for package in ("model", "service", "realtime"):
                with self.subTest(dialect=dialect, package=package):
                    result, rows = self.run_script(dialect, finalization_creation_fails=package)
                    self.assertNotEqual(0, result.returncode)
                    self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))

    def test_official_subscription_selects_actual_dialect_and_owned_fixture_contract(self):
        suite = json.loads((SCRIPT.parent / "billing_refund_followup_required_cases.json").read_text())["database_suite"]
        for dialect in ("sqlite", "mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                invocation, check = self.report_contracts(rows)[suite["label"]]
                self.assertEqual([suite["top"], suite["top"] + "/" + dialect], check["args"][3:])
                self.assertEqual("./model", invocation["args"][4])
                self.assertEqual(f"^{suite['top']}$/{dialect}$", invocation["args"][6])
                for engine, key in suite["dsn_env"].items():
                    if dialect == engine:
                        self.assertEqual(invocation["env"]["TEST_DATABASE_DSN"], invocation["env"][key])
                        self.assertIn("/repair_compat?", invocation["env"][key])
                    else:
                        self.assertEqual("", invocation["env"][key])
                self.assertIn("Atomic source and receipt rollback/replay is required", suite["sqlite_result"])

    def test_task_wallet_and_immediate_settlement_require_all_current_outcomes(self):
        inventory = json.loads((SCRIPT.parent / "billing_finalization_required_cases.json").read_text())
        background = "TestServeTaskPluginProtocolBackgroundPreservesConcurrentFinancialState"
        invalid = "TestTaskResponsesBackgroundMetadataRejectsInvalidWrites"
        background_required = [background] + [background + "/" + case for case in (
            "pending", "terminal_intent", "settled", "refunded", "review")]
        background_required += [invalid] + [invalid + "/" + case for case in (
            "id", "user", "zero_id", "zero_user", "public_id", "platform", "empty_public_id",
            "empty_platform", "nil_context", "cancelled_context", "write_failure")]
        self.assertEqual(18, len(background_required))
        self.assertEqual(background_required, inventory["immediate_settlement_suite"]["standalone_required_tests"])
        for dialect in ("sqlite", "mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                contracts = self.report_contracts(rows)
                for key in ("task_wallet_suite", "immediate_settlement_suite"):
                    suite = inventory[key]
                    test, check = contracts[suite["label"]]
                    prefix = suite["top"] + ("/" + dialect if key == "task_wallet_suite" else "")
                    required = [prefix] + [prefix + "/" + leaf for leaf in suite["required_leaf_suffixes"]]
                    required += suite.get("standalone_required_tests", [])
                    self.assertEqual(required, check["args"][3:])
                    self.assertEqual("./" + suite["package"], test["args"][4])
                    selector = suite.get("selector", f"^{suite['top']}$/{dialect}$")
                    self.assertEqual(selector, test["args"][6])
                    if key == "immediate_settlement_suite":
                        self.assertEqual(29, len(required))
                        for top in (suite["top"], background, invalid):
                            self.assertIsNotNone(re.fullmatch(selector, top))
                        self.assertIsNone(re.fullmatch(selector, background + "Unexpected"))
                for table in ("tokens", "tasks"):
                    self.assertIn(f"TestTaskWalletReceiptsDatabaseMatrix/{dialect}/settlement_rolls_back_on_{table}_failure", contracts["wallet"][1]["args"][3:])
                prefix = f"TestSubscriptionPeriodMigrationDatabaseMatrix/{dialect}"
                self.assertEqual([prefix + "/fresh", prefix + "/upgrade"], contracts["subscription-period-migration"][1]["args"][3:])

    def test_retired_policy_reports_databases_and_optins_cannot_be_reactivated_by_ambient_env(self):
        policy = json.loads((SCRIPT.parent / "billing_policy_replacement.json").read_text())
        retired = {item["label"] for item in policy["historical_report_transitions"] if item["status"] in ("retired", "replaced")}
        for dialect in ("sqlite", "mysql", "postgres", "redis"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script("" if dialect == "redis" else dialect,
                                               mode="redis" if dialect == "redis" else "database",
                                               finalization_creation_fails="cache", refund_operations_creation_fails=True)
                self.assertEqual(0, result.returncode, result.stderr)
                contracts = self.report_contracts(rows)
                self.assertEqual(self.expected_labels(dialect), list(contracts))
                self.assertFalse(retired.intersection(contracts))
                for invocation, _ in contracts.values():
                    for key in policy["retired_environment_kept_empty"]:
                        self.assertEqual("", invocation["env"][key], key)
                for row in rows:
                    if row["tool"] == "docker":
                        for database in policy["retired_databases"]:
                            self.assertNotIn(database, " ".join(row["args"]))

    def test_sqlite_only_policy_replacements_require_exact_current_names(self):
        inventory = json.loads((SCRIPT.parent / "billing_refund_followup_required_cases.json").read_text())
        suites = {suite["label"]: suite for suite in inventory["sqlite_only_suites"]}
        self.assertEqual({"billing-error-classifier", "official-token-refund", "official-midjourney-refund", "official-wallet-allocation",
                          "normal-wallet-settlement", "official-task-submission"}, set(suites))
        classifier = "TestBillingErrorClassificationExcludesSecretDetails"
        self.assertEqual([classifier] + [classifier + "/" + case for case in (
            "nil", "unknown_secret", "wrapped_unknown_secret", "timeout", "cancel", "identity", "missing",
            "terminal_owner", "ledger_conflict", "ledger_invalid")], suites["billing-error-classifier"]["required_tests"])
        task_required = suites["official-task-submission"]["required_tests"]
        # Keep all four scenario boundaries under their current v2 semantics,
        # and require rejection of a custom session without atomic handoff.
        for current in (
            "TestExecuteTaskSubmissionPreservesHoldWhenAcceptedInsertFails",
            "TestExecuteTaskSubmissionRollsBackInsertedTaskWhenHandoffFails",
            "TestExecuteTaskSubmissionPreservesHoldOnCancellationAfterUpstreamAcceptance",
            "TestExecuteTaskSubmissionPreservesHoldWhenAcceptedFinalReserveFails",
            "TestExecuteTaskSubmissionRejectsUnsupportedAtomicBilling",
        ):
            self.assertIn(current, task_required)
        for historical in (
            "TestExecuteTaskSubmissionRefundsWhenInsertFails",
            "TestExecuteTaskSubmissionSettlementFailureStaysDurableAndWritesNothing",
            "TestExecuteTaskSubmissionRefundsCancellationBeforeDurableBarrier",
            "TestExecuteTaskSubmissionRefundsWhenFinalReserveFails",
        ):
            self.assertNotIn(historical, task_required)
        for dialect in ("sqlite", "mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                selected = set()
                for label, (invocation, check) in self.report_contracts(rows).items():
                    if label not in suites:
                        continue
                    selected.add(label)
                    suite = suites[label]
                    self.assertEqual(suite["required_tests"], check["args"][3:])
                    self.assertEqual("./" + suite["package"], invocation["args"][4])
                    selector = invocation["args"][6]
                    self.assertEqual(suite["selector"], selector)
                    for top in (test for test in suite["required_tests"] if "/" not in test):
                        self.assertIsNotNone(re.fullmatch(selector, top))
                    self.assertIsNone(re.fullmatch(selector, "TestBillingRefundIntentProcessExitHelper"))
                self.assertEqual(set(suites) if dialect == "sqlite" else set(), selected)
        required = suites["normal-wallet-settlement"]["required_tests"]
        for deletion in ("soft", "hard"):
            for amount in ("trusted", "charge", "refund"):
                self.assertIn(f"TestWalletSettlementSurvivesTokenDeletion/{deletion}/{amount}", required)
        for identity in ("owner", "credential"):
            self.assertIn("TestWalletSettlementRejectsChangedTokenIdentity/" + identity, required)
        for top in ("TestPostConsumeQuotaIdempotencyCoversWalletAndToken",
                    "TestPostConsumeQuotaRefundIdempotencyCoversWalletAndToken",
                    "TestPostConsumeQuotaRefundRequiresDebitReceipt",
                    "TestPostConsumeQuotaRollsBackWalletWhenTokenUpdateFails"):
            self.assertIn(top, required)

    def test_followup_required_rejections_fail_but_retain_later_independent_reports(self):
        inventory = json.loads((SCRIPT.parent / "billing_refund_followup_required_cases.json").read_text())
        labels = [inventory["database_suite"]["label"]] + [suite["label"] for suite in inventory["sqlite_only_suites"]]
        for label in labels:
            with self.subTest(label=label):
                result, rows = self.run_script("sqlite", reject_label=label)
                self.assertNotEqual(0, result.returncode)
                self.assertIn("Required repair contracts failed", result.stderr)
                contracts = self.report_contracts(rows)
                self.assertEqual(self.expected_labels("sqlite"), list(contracts))
                self.assertEqual("sensitive-task-selected-plugin", list(contracts)[-1])

    def test_actual_checker_rejects_each_missing_current_required_path(self):
        policy = json.loads((SCRIPT.parent / "billing_policy_replacement.json").read_text())
        self.assertEqual(policy["checker_sha256"], hashlib.sha256((SCRIPT.parent / "check_go_test_report.py").read_bytes()).hexdigest())
        spec = importlib.util.spec_from_file_location("refund_followup_report_checker", SCRIPT.parent / "check_go_test_report.py")
        checker = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(checker)
        followup = json.loads((SCRIPT.parent / "billing_refund_followup_required_cases.json").read_text())
        labels = {"request-finalization-model", "request-finalization-service", "wallet", "immediate-settlement",
                  followup["database_suite"]["label"]} | {suite["label"] for suite in followup["sqlite_only_suites"]}
        current = json.loads((SCRIPT.parent / "financial_consistency_required_cases.json").read_text())
        labels |= {suite["label"] for suite in current["model_suites"] if suite["label"] != "subscription-correctness"}
        labels |= {"subscription-correctness-sqlite_immediate", "subscription-correctness-sqlite_deferred", "subscription-correctness-mysql", "subscription-correctness-postgres", current["realtime_suite"]["label"]}
        labels |= {suite["label"] for suite in current["sqlite_only_suites"]}
        # Synthetic evidence only: this verifies the exact checker, not product behavior.
        for dialect in ("sqlite", "mysql", "postgres"):
            result, rows = self.run_script(dialect)
            self.assertEqual(0, result.returncode, result.stderr)
            for label, (_, check) in self.report_contracts(rows).items():
                if label not in labels:
                    continue
                required = check["args"][3:]
                self.assertEqual(len(required), len(set(required)), label)
                with tempfile.TemporaryDirectory() as directory:
                    report = Path(directory) / "synthetic.jsonl"
                    events = [{"Action": action, "Package": "offline/fixture", "Test": test}
                              for test in required for action in ("run", "pass")]
                    events.append({"Action": "pass", "Package": "offline/fixture"})
                    report.write_text("".join(json.dumps(event) + "\n" for event in events))
                    self.assertEqual(len(required), len(checker.validate_report(report, required, True, check["require_run"])))
                    for missing in required:
                        with self.subTest(dialect=dialect, label=label, missing=missing):
                            report.write_text("".join(json.dumps(event) + "\n" for event in events if event.get("Test") != missing))
                            with self.assertRaisesRegex(ValueError, "required test did not pass"):
                                checker.validate_report(report, required, True, check["require_run"])
                    report.write_text("".join(json.dumps(event) + "\n" for event in events + [
                        {"Action": "skip", "Package": "offline/fixture", "Test": required[0] + "/unexpected_skipped_contract"}]))
                    with self.assertRaisesRegex(ValueError, "descendant was skipped"):
                        checker.validate_report(report, required, True, check["require_run"])

    def test_current_financial_reports_select_every_required_leaf_and_isolate_realtime(self):
        inventory = json.loads((SCRIPT.parent / "financial_consistency_required_cases.json").read_text())
        realtime = inventory["realtime_suite"]
        for dialect in ("sqlite", "mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                contracts = self.report_contracts(rows)
                for suite in inventory["model_suites"]:
                    modes = suite.get("sqlite_modes", [dialect]) if dialect == "sqlite" else [dialect]
                    for selected in modes:
                        label = suite["label"] + ("-" + selected if suite["label"] == "subscription-correctness" else "")
                        invocation, check = contracts[label]
                        prefix = suite["top"] + "/" + selected
                        leaves = [prefix + "/" + leaf for leaf in suite["required_leaf_suffixes"]]
                        self.assertEqual(leaves if suite.get("existing_report") or suite["label"] == "subscription-correctness" else [prefix] + leaves,
                                         check["args"][3:])
                        self.assertEqual("./" + suite["package"], invocation["args"][4])
                        self.assertEqual(f"^{suite['top']}$/{selected}$", invocation["args"][6])
                for label, (invocation, check) in contracts.items():
                    for engine, key in realtime["dsn_env"].items():
                        if label == realtime["label"] and dialect == engine:
                            self.assertIn("/repair_finalization_realtime?", invocation["env"][key])
                            self.assertIn("127.0.0.1:", invocation["env"][key])
                            self.assertNotEqual(invocation["env"]["TEST_DATABASE_DSN"], invocation["env"][key])
                        else:
                            self.assertEqual("", invocation["env"][key], (label, key))
                invocation, check = contracts[realtime["label"]]
                prefix = realtime["top"] + "/" + dialect
                self.assertEqual([prefix] + [prefix + "/" + leaf for leaf in realtime["required_leaf_suffixes"]], check["args"][3:])
                self.assertEqual("./" + realtime["package"], invocation["args"][4])
                self.assertEqual(f"^{realtime['top']}$/{dialect}$", invocation["args"][6])
                if dialect != "sqlite":
                    creation = [row for row in rows if row["tool"] == "docker" and any("repair_finalization_realtime" in arg for arg in row["args"])]
                    self.assertEqual(1, len(creation))
                    self.assertNotIn("IF NOT EXISTS", " ".join(creation[0]["args"]))
                    self.assertNotIn("DROP", " ".join(creation[0]["args"]))
                    self.assertLess(rows.index(creation[0]), rows.index(next(iter(contracts.values()))[0]))
                for suite in inventory["sqlite_only_suites"]:
                    if dialect != "sqlite":
                        self.assertNotIn(suite["label"], contracts)
                        continue
                    invocation, check = contracts[suite["label"]]
                    self.assertEqual(suite["required_tests"], check["args"][3:])
                    self.assertEqual("./" + suite["package"], invocation["args"][4])
                    for leaf in suite["required_tests"]:
                        self.assertIsNotNone(re.fullmatch(invocation["args"][6], leaf.split("/")[0]))

    def new_financial_suites(self, dialect):
        payment = json.loads((SCRIPT.parent / "payment_stale_order_required_cases.json").read_text())
        bonus = json.loads((SCRIPT.parent / "bonus_audit_required_cases.json").read_text())
        self.assertEqual({"sqlite": 41, "mysql": 45, "postgres": 45}, payment["database_suite"]["required_leaf_counts"])
        self.assertEqual({"sqlite": 13, "mysql": 24, "postgres": 16}, bonus["database_suite"]["required_leaf_counts"])
        suites = []
        for inventory in (payment, bonus):
            self.assertEqual({"require_run": True, "run_count": 1, "pass_count": 1,
                              "reject_skipped_descendants": True}, inventory["evidence_gate"])
            suite = inventory["database_suite"]
            required = list(suite["required_tests"][dialect])
            retained = suite.get("retained_sqlite_required_tests", []) if dialect == "sqlite" else []
            selector = f"^{suite['top']}$/{dialect}$"
            if retained:
                selector = "^(" + "|".join([suite["top"]] + retained) + f")$/{dialect}$"
            suites.append((suite, required + retained, selector))
            leaves = [name for name in required if not any(other.startswith(name + "/") for other in required)]
            self.assertEqual(suite["required_leaf_counts"][dialect], len(leaves))
            self.assertEqual(len(required), len(set(required)))
            for leaf in leaves:
                parts = leaf.split("/")
                for depth in range(1, len(parts)):
                    self.assertIn("/".join(parts[:depth]), required, "matrix ancestors must execute too")
        if dialect == "sqlite":
            suite = payment["sqlite_only_suite"]
            suites.append((suite, suite["required_tests"], suite["selector"]))
            leaves = [name for name in suite["required_tests"]
                      if not any(other.startswith(name + "/") for other in suite["required_tests"])]
            self.assertEqual(suite["required_leaf_count"], len(leaves))
            self.assertEqual(10, len(leaves))
            self.assertNotIn("TestPancakeCreationFailurePreservesSuccessfulWebhook/topup", suite["required_tests"])
            self.assertNotIn("TestPancakeCreationFailurePreservesSuccessfulWebhook/subscription", suite["required_tests"])
            self.assertIsNone(re.fullmatch(suite["selector"], suite["excluded_worker"]))
        return suites

    def test_payment_and_bonus_profiles_match_exact_inventory_and_keep_cache_regressions(self):
        policy = json.loads((SCRIPT.parent / "billing_policy_replacement.json").read_text())
        self.assertEqual({"payment-pending-status", "bonus-audit-race", "payment-stale-callback", "financial-bonus-cache"},
                         set(policy["strict_run_reports"]))
        for dialect in ("sqlite", "mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                contracts = self.report_contracts(rows)
                # Count from actual named invocations and current policy, never historical totals.
                self.assertEqual(policy["report_order"][dialect], list(contracts))
                self.assertEqual(len(policy["report_order"][dialect]), len(contracts))
                for suite, required, selector in self.new_financial_suites(dialect):
                    invocation, gate = contracts[suite["label"]]
                    self.assertEqual(required, gate["args"][3:])
                    self.assertTrue(gate["require_run"])
                    self.assertEqual("./" + suite["package"], invocation["args"][4])
                    self.assertEqual(selector, invocation["args"][6])
                if dialect != "sqlite":
                    self.assertNotIn("payment-stale-callback", contracts)
                else:
                    cache = contracts["financial-bonus-cache"][1]
                    self.assertTrue(cache["require_run"])
                    for parent in ("TestBonusEligibilityCommittedMetadataRefreshControls", "TestBonusEligibilityMutationCommitBoundaries"):
                        self.assertIn(parent, cache["args"][3:])
                    for suffix in ("batch_commit", "batch_lost_ack", "delete_lost_ack", "delete_pricing_lost_ack",
                                   "sync_bonus_lost_ack", "sync_name_rule_lost_ack"):
                        self.assertIn("TestBonusEligibilityMutationCommitBoundaries/" + suffix, cache["args"][3:])

    def test_bonus_owned_database_is_exactly_scoped_and_creation_failure_never_reuses_it(self):
        for dialect, expected in (
            ("mysql", "root:compat@tcp(127.0.0.1:13306)/repair_bonus_audit?charset=utf8mb4&parseTime=true&loc=UTC"),
            ("postgres", "postgres://postgres:compat@127.0.0.1:15432/repair_bonus_audit?sslmode=disable"),
        ):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                contracts = self.report_contracts(rows)
                for label, (invocation, _) in contracts.items():
                    for engine in ("mysql", "postgres"):
                        key = "TEST_BONUS_AUDIT_" + engine.upper() + "_DSN"
                        self.assertEqual(expected if label == "bonus-audit-race" and engine == dialect else "",
                                         invocation["env"][key], (label, key))
                creation = [row for row in rows if row["tool"] == "docker" and any("repair_bonus_audit" in arg for arg in row["args"])]
                self.assertEqual(1, len(creation))
                args = creation[0]["args"]
                if dialect == "mysql":
                    self.assertEqual(["exec", "b" * 64, "mysql", "-uroot", "-pcompat", "-e",
                                      "CREATE DATABASE repair_bonus_audit CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"], args)
                else:
                    self.assertEqual(["exec", "c" * 64, "createdb", "-U", "postgres", "repair_bonus_audit"], args)
                self.assertNotIn("IF NOT EXISTS", " ".join(args))
                self.assertNotIn("DROP", " ".join(args))
                self.assertLess(rows.index(creation[0]), rows.index(next(iter(contracts.values()))[0]))
                result, rows = self.run_script(dialect, bonus_audit_creation_fails=True)
                self.assertNotEqual(0, result.returncode)
                self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))

    def test_new_strict_reports_reject_each_missing_or_duplicate_run_and_pass(self):
        spec = importlib.util.spec_from_file_location("financial_strict_report_checker", SCRIPT.parent / "check_go_test_report.py")
        checker = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(checker)
        current = json.loads((SCRIPT.parent / "financial_consistency_required_cases.json").read_text())
        cache = next(suite for suite in current["sqlite_only_suites"] if suite["label"] == "financial-bonus-cache")
        for dialect in ("sqlite", "mysql", "postgres"):
            suites = [(suite["label"], required) for suite, required, _ in self.new_financial_suites(dialect)]
            if dialect == "sqlite":
                suites.append((cache["label"], cache["required_tests"]))
            for label, required in suites:
                with tempfile.TemporaryDirectory() as directory:
                    report = Path(directory) / "synthetic.jsonl"
                    # Synthetic checker contracts only, never product execution evidence.
                    events = [{"Action": action, "Package": "offline/fixture", "Test": test}
                              for test in required for action in ("run", "pass")]
                    events.append({"Action": "pass", "Package": "offline/fixture"})
                    def write(values):
                        report.write_text("".join(json.dumps(value) + "\n" for value in values))
                    write(events)
                    self.assertEqual(len(required), len(checker.validate_report(report, required, True, True)))
                    for name in required:
                        for action in ("run", "pass"):
                            event = {"Action": action, "Package": "offline/fixture", "Test": name}
                            with self.subTest(dialect=dialect, label=label, name=name, action=action, defect="missing"):
                                write([item for item in events if item != event])
                                with self.assertRaises(ValueError):
                                    checker.validate_report(report, required, True, True)
                            with self.subTest(dialect=dialect, label=label, name=name, action=action, defect="duplicate"):
                                write(events + [event])
                                with self.assertRaisesRegex(ValueError, "exactly one RUN and PASS"):
                                    checker.validate_report(report, required, True, True)
                    write(events + [{"Action": "skip", "Package": "offline/fixture", "Test": required[0] + "/skipped"}])
                    with self.assertRaisesRegex(ValueError, "descendant was skipped"):
                        checker.validate_report(report, required, True, True)

    def test_new_required_rejection_fails_but_preserves_later_independent_reports(self):
        for dialect in ("sqlite", "mysql", "postgres"):
            labels = [suite["label"] for suite, _, _ in self.new_financial_suites(dialect)]
            if dialect == "sqlite":
                labels.append("financial-bonus-cache")
            for label in labels:
                with self.subTest(dialect=dialect, label=label):
                    result, rows = self.run_script(dialect, reject_label=label)
                    self.assertNotEqual(0, result.returncode)
                    self.assertIn("Required repair contracts failed", result.stderr)
                    self.assertEqual(self.expected_labels(dialect), list(self.report_contracts(rows)))

    def test_normal_settlement_deadline_inventory_remains_required(self):
        inventory = json.loads((SCRIPT.parent / "billing_settlement_cache_required_cases.json").read_text())
        for dialect in ("sqlite", "mysql", "postgres"):
            result, rows = self.run_script(dialect)
            self.assertEqual(0, result.returncode, result.stderr)
            contracts = self.report_contracts(rows)
            suite = inventory["normal_database_suite"]
            required = contracts[suite["label"]][1]["args"][3:]
            prefix = "TestBillingFinalizationSessionDatabaseMatrix/" + dialect
            leaves = suite["required_leaf_suffixes"] + (suite["native_required_leaf_suffixes"] if dialect != "sqlite" else [])
            for leaf in leaves:
                self.assertIn(prefix + "/" + leaf, required)
            for test, _ in contracts.values():
                self.assertEqual("", test["env"]["TEST_SETTLEMENT_DEADLINE_REDIS_ADDR"])
                self.assertEqual("", test["env"]["TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE"])
            if dialect == "sqlite":
                suite = inventory["normal_token_suite"]
                for name in suite["required_tests"]:
                    self.assertIn(name, contracts[suite["label"]][1]["args"][3:])

    def test_both_pinned_redis_versions_keep_remaining_real_contracts_and_loopback_dsns(self):
        expected = {
            "model-script-flush": ["TestModelRateLimitRealRedis/reservation_replay_and_expiry",
                                   "TestModelRateLimitRealRedis/script_flush_resumes_request_pipeline"],
            "token-bucket-script-flush": ["TestAllowFallsBackToEvalAfterScriptFlush/real_redis"],
            "settlement-deadline-redis": ["TestBillingSettlementDeadlineNativeRedisFence"],
        }
        for version in ("7.2.16", "8.8.0"):
            with self.subTest(version=version):
                result, rows = self.run_script("", mode="redis", redis_version=version,
                                               redis_info=f"# Server\r\nredis_version:{version}\r\nredis_mode:standalone\r\n")
                self.assertEqual(0, result.returncode, result.stderr)
                contracts = self.report_contracts(rows)
                self.assertEqual(self.expected_labels("redis"), list(contracts))
                self.assertEqual(list(expected), list(contracts))
                for label, (test, gate) in contracts.items():
                    self.assertEqual(expected[label], gate["args"][3:])
                    self.assertEqual("1", test["env"]["TEST_RUNTIME_REDIS"])
                    self.assertEqual("127.0.0.1:16379" if label == "settlement-deadline-redis" else "",
                                     test["env"]["TEST_SETTLEMENT_DEADLINE_REDIS_ADDR"])
                    self.assertEqual("1" if label == "settlement-deadline-redis" else "",
                                     test["env"]["TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE"])
                    self.assertEqual("redis://127.0.0.1:16379/0", test["env"]["TEST_REDIS_DSN"])
                    self.assertEqual("127.0.0.1:16379", test["env"]["TEST_REDIS_ADDR"])

    def test_wrong_missing_ambiguous_and_malformed_redis_versions_fail_before_tests(self):
        cases = [("8.8.0", "redis_version:7.2.16\r\n"), ("8.8.0", ""),
                 ("8.8.0", "redis_version:8.8.0\nredis_version:8.8.0\n"),
                 ("8.8.0", "redis_version:8.8.0\nredis_version:\n"),
                 ("8.8.0", "redis_version:8.8.0:extra\n"), ("8.8.0", "redis_version:8.8.0-rc1\n"),
                 ("8.8.0", " redis_version:8.8.0\n"), ("", "redis_version:8.8.0\n"),
                 ("8.8", "redis_version:8.8\n")]
        for expected, info in cases:
            with self.subTest(expected=expected, info=info):
                result, rows = self.run_script("", mode="redis", redis_version=expected, redis_info=info)
                self.assertNotEqual(0, result.returncode)
                self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))

    def test_native_redis_deadline_inventory_and_actual_strict_checker(self):
        suite = json.loads((SCRIPT.parent / "billing_settlement_cache_required_cases.json").read_text())["redis_suite"]
        result, rows = self.run_script("", mode="redis")
        self.assertEqual(0, result.returncode, result.stderr)
        test, check = self.report_contracts(rows)[suite["label"]]
        self.assertEqual(suite["required_tests"], check["args"][3:])
        self.assertEqual("./" + suite["package"], test["args"][4])
        self.assertEqual(f"^{suite['top']}$", test["args"][6])
        self.assertEqual("127.0.0.1:16379", test["env"][suite["address_env"]])
        self.assertEqual("1", test["env"][suite["disposable_env"]])
        spec = importlib.util.spec_from_file_location("settlement_redis_report_checker", SCRIPT.parent / "check_go_test_report.py")
        checker = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(checker)
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "synthetic.jsonl"
            events = [{"Action": "pass", "Package": "offline/fixture", "Test": suite["top"]},
                      {"Action": "pass", "Package": "offline/fixture"}]
            report.write_text("".join(json.dumps(event) + "\n" for event in events))
            self.assertEqual(1, len(checker.validate_report(report, suite["required_tests"], True)))
            report.write_text(json.dumps(events[1]) + "\n")
            with self.assertRaisesRegex(ValueError, "required test did not pass"):
                checker.validate_report(report, suite["required_tests"], True)
            events[0]["Action"] = "skip"
            report.write_text("".join(json.dumps(event) + "\n" for event in events))
            with self.assertRaises(ValueError):
                checker.validate_report(report, suite["required_tests"], True)

    def test_failed_redis_info_query_cannot_start_contracts(self):
        result, rows = self.run_script("", mode="redis", redis_info_fails=True)
        self.assertNotEqual(0, result.returncode)
        self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))

    def test_redis_required_rejection_keeps_later_evidence_and_fails_job(self):
        for label in self.expected_labels("redis"):
            with self.subTest(label=label):
                result, rows = self.run_script("", mode="redis", reject_label=label)
                self.assertNotEqual(0, result.returncode)
                self.assertIn("Required repair contracts failed", result.stderr)
                self.assertEqual(self.expected_labels("redis"), list(self.report_contracts(rows)))


if __name__ == "__main__":
    unittest.main()
