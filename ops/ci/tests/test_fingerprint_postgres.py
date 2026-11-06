"""Offline checks for PG selection, database ownership, and rejected skip evidence."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

SCRIPT = Path(__file__).resolve().parents[1] / "verify_fingerprint_postgres.py"
SPEC = importlib.util.spec_from_file_location("fingerprint_postgres", SCRIPT)
profile = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = profile
SPEC.loader.exec_module(profile)


def package(path="./model", extra=(), functions=None, envs=(), function_envs=None):
    names = (profile.FINGERPRINT | set(profile.SPECIAL) | set(profile.EMPTY_DATABASE_ENVS)
             | profile.POSTGRES_CHILD | profile.POSTGRES_STANDALONE | set(profile.MATRIX_REQUIRED_NODES) | set(extra))
    return profile.Package(path, sorted(names), functions or {}, function_envs or {}, set(envs), set())


def runner_for_test(directory, pkg, phase="postgres"):
    runner = profile.Runner.__new__(profile.Runner)
    runner.phase = phase
    runner.out = Path(directory)
    runner.packages = {pkg.path: pkg}
    runner.env = profile.clean_environment({})
    runner.container = "a" * 64
    runner.port = 5432
    runner.password = "synthetic-test-password"
    return runner


class FingerprintPostgresProfileTests(unittest.TestCase):
    def test_all_required_fingerprint_tests_select_only_postgres(self):
        suites = profile.build_plan([package()])
        selected = [suite for suite in suites if suite.phase == "fingerprint"]
        self.assertEqual(profile.FINGERPRINT, {suite.label for suite in selected})
        for suite in selected:
            self.assertEqual("^" + suite.label + "$/^postgres$", suite.pattern)
            self.assertEqual([suite.label, suite.label + "/postgres"], suite.required)
        unit = next(suite for suite in suites if suite.phase == "unit")
        self.assertTrue(all(name in unit.skip for name in profile.FINGERPRINT))

    def test_embedded_slashes_require_real_nodes_not_invented_parents(self):
        suites = {suite.label: suite for suite in profile.build_plan([package()])}
        for top, children in profile.MATRIX_REQUIRED_NODES.items():
            self.assertEqual([top, *(top + "/" + child for child in children)], suites[top].required)
            self.assertNotIn(top + "/postgres", suites[top].required)

    def test_task_fixture_callers_are_separated_transitively(self):
        functions = {"TestTaskFinancialContract": {"taskHelper"}, "taskHelper": {"openTaskDialectDatabase"},
                     "TestOrdinaryContract": {"ordinaryHelper"}}
        suites = profile.build_plan([package("./controller", functions, functions)])
        task = next(s for s in suites if s.label == "TestTaskFinancialContract")
        self.assertEqual("task-financial", task.phase)
        self.assertEqual(["TestTaskFinancialContract"], task.required)
        unit = next(s for s in suites if s.phase == "unit")
        self.assertIn("TestOrdinaryContract", unit.tests)
        self.assertNotIn("TestTaskFinancialContract", unit.tests)

    def test_disappearing_required_test_rejects_the_plan(self):
        pkg = package()
        pkg.tests.remove("TestModelTestTargetAuthorizationDatabaseMatrix")
        with self.assertRaisesRegex(ValueError, "required tests disappeared"):
            profile.build_plan([pkg])

    def test_environment_cannot_inherit_an_operator_or_other_engine_database(self):
        source = {"SQL_DSN": "production", "LOG_SQL_DSN": "production-log",
                  "TEST_MYSQL_DSN": "ambient-mysql", "TEST_MODEL_POSTGRES_DSN": "ambient-postgres",
                  "AUDIT_POSTGRES_DSN": "ambient-audit", "TEST_DATABASE_COMPAT": "1",
                  "NEW_API_TEST_RECOVERY_RESTART_PHASE": "resume", "PAYMENT_STALE_FIXTURE_CHILD": "1",
                  "TEST_REDIS_DSN": "redis://127.0.0.1:6379/2", "TEST_AUTH_REDIS_DSN": "redis://127.0.0.1:6379/1"}
        env = profile.clean_environment(source)
        self.assertEqual("", env["SQL_DSN"])
        self.assertEqual("", env["LOG_SQL_DSN"])
        for key in ("TEST_MYSQL_DSN", "TEST_MODEL_POSTGRES_DSN", "AUDIT_POSTGRES_DSN",
                    "NEW_API_TEST_RECOVERY_RESTART_PHASE", "PAYMENT_STALE_FIXTURE_CHILD"):
            self.assertNotIn(key, env)
        self.assertEqual("postgres", env["TEST_FINGERPRINT_DIALECT"])
        self.assertEqual(source["TEST_REDIS_DSN"], env["TEST_REDIS_DSN"])
        self.assertEqual(source["TEST_AUTH_REDIS_DSN"], env["TEST_AUTH_REDIS_DSN"])

    def test_financial_empty_databases_are_distinct_from_testmain_and_other_suites(self):
        all_keys = set(profile.EMPTY_DATABASE_ENVS.values()) | {"TEST_POSTGRES_DSN", "TEST_MODEL_POSTGRES_DSN"}
        functions = {top: all_keys for top in profile.EMPTY_DATABASE_ENVS}
        pkg = package(envs=all_keys, function_envs=functions)
        with tempfile.TemporaryDirectory() as directory:
            runner = runner_for_test(directory, pkg)
            created = []
            runner.database = lambda name: created.append(name) or "postgres://127.0.0.1/" + name
            for index, (top, expected_key) in enumerate(profile.EMPTY_DATABASE_ENVS.items(), 1):
                suite = profile.Suite("postgres", pkg.path, top, [top])
                env, owned = runner.suite_environment(suite, index)
                self.assertNotEqual(env[expected_key], env["TEST_MODEL_POSTGRES_DSN"])
                self.assertNotEqual(env[expected_key], env["TEST_POSTGRES_DSN"])
                for other in set(profile.EMPTY_DATABASE_ENVS.values()) - {expected_key}:
                    self.assertNotIn(other, env)
                self.assertEqual(len(owned), len(set(owned)))
            self.assertEqual(len(created), len(set(created)))

    def test_a07_wallet_and_schema_upgrade_both_get_the_required_opt_in(self):
        tops = {"TestAgentSimpleSchemaFreshUpgradeAndRepeatedMigration",
                "TestAgentSimpleWalletSchemaUpgradePreservesBalancesAndLedgerUniqueness"}
        pkg = package()
        suites = profile.build_plan([pkg])
        with tempfile.TemporaryDirectory() as directory:
            runner = runner_for_test(directory, pkg)
            runner.database = lambda name: "postgres://127.0.0.1/" + name
            for index, suite in enumerate((s for s in suites if s.label in tops), 1):
                self.assertEqual("postgres", suite.phase)
                self.assertNotIn("/", suite.pattern)
                env, _ = runner.suite_environment(suite, index)
                self.assertEqual("1", env["TEST_A07_SCHEMA_COMPAT"])

    def test_no_test_or_postgres_fixture_package_creates_no_database(self):
        pkg = profile.Package("./empty", [], {}, {}, set(), set())
        with tempfile.TemporaryDirectory() as directory:
            runner = runner_for_test(directory, pkg, "unit")
            runner.database = mock.Mock(side_effect=AssertionError("unneeded database"))
            env, owned = runner.suite_environment(profile.Suite("unit", pkg.path, "empty", []), 1)
            self.assertEqual([], owned)
            self.assertNotIn("TEST_POSTGRES_DSN", env)

    def test_existing_database_creation_failure_never_reuses_or_drops_it(self):
        with tempfile.TemporaryDirectory() as directory:
            runner = runner_for_test(directory, package())
            with mock.patch.object(profile.subprocess, "run", side_effect=subprocess.CalledProcessError(1, "createdb")) as command:
                with self.assertRaises(subprocess.CalledProcessError):
                    runner.database("fp_existing")
            arguments = command.call_args.args[0]
            self.assertIn("createdb", arguments)
            self.assertNotIn("DROP", " ".join(arguments))
            self.assertNotIn("IF NOT EXISTS", " ".join(arguments))

    def test_wrong_postgresql_version_is_rejected_before_database_creation(self):
        env = {"PG_ID": "a" * 64, "PG_TEST_PASSWORD": "synthetic", "NO_AGENT_FIXTURE_OWNED": "1"}
        with mock.patch.dict(os.environ, env, clear=True), mock.patch.object(profile.subprocess, "check_output", return_value="180005\n"):
            with self.assertRaisesRegex(ValueError, "production PostgreSQL 18.6"):
                profile.Runner("postgres", Path("unused"), [])

    def test_required_skip_rejects_successful_go_exit_without_starting_any_database(self):
        real_run = subprocess.run
        top = "TestRequiredPostgres"
        pkg = package()
        with tempfile.TemporaryDirectory() as directory:
            runner = runner_for_test(directory, pkg)
            runner.suite_environment = lambda suite, index: ({}, [])
            suite = profile.Suite("postgres", pkg.path, top, [top], pattern="^" + top + "$", required=[top])

            def run(command, **kwargs):
                if command[0] != "go":
                    return real_run(command, **kwargs)
                events = [{"Action": "start", "Package": "fixture"},
                          {"Action": "run", "Package": "fixture", "Test": top},
                          {"Action": "skip", "Package": "fixture", "Test": top},
                          {"Action": "pass", "Package": "fixture"}]
                kwargs["stdout"].write("".join(json.dumps(event) + "\n" for event in events))
                return subprocess.CompletedProcess(command, 0)

            with mock.patch.object(profile.subprocess, "run", side_effect=run):
                self.assertFalse(runner.run(suite, 1))
            summary = json.loads((Path(directory) / ("001-" + top + ".summary.json")).read_text())
            self.assertEqual([top], summary["skipped_tests"])
            self.assertFalse(summary["success"])


if __name__ == "__main__":
    unittest.main()
