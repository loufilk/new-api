import copy
import json
import os
import re
import selectors
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

import wallet_cpu_report as report


def manifest():
    return {
        "version": 1,
        "cohort": {
            "window_ms": 500, "scheduled": 2, "wallets": 1, "keys": 2, "replicas": 1,
            "wallet_sample_permille": 1000, "relay_sample_permille": 1000,
            "shape_sha256": "a" * 64,
        },
        "requests": {"issued": 2, "resolved": 2, "failed": 0},
        "wallet": [
            {"operation": "reservation", "commit_acknowledged": True, "pool_and_begin_us": 3,
             "wallet_lock_query_us": 40, "after_lock_query_to_tx_return_us": 10, "tx_return_us": 62},
            {"operation": "reservation", "commit_acknowledged": True, "pool_and_begin_us": 4,
             "wallet_lock_query_us": 60, "after_lock_query_to_tx_return_us": 10, "tx_return_us": 82},
        ],
        "relay": [
            {"status": 200, "relay_api_error": False, "billing_prepare_ms": 10},
            {"status": 200, "relay_api_error": True, "ingress_to_first_upstream_event_ms": 4},
        ],
        "pools": {"main": {
            "before": {"wait_count": 100, "wait_duration_ns": 80_000, "max_open_connections": 20},
            "after": {"wait_count": 102, "wait_duration_ns": 140_000, "max_open_connections": 20},
        }},
        "pg_wait_samples": {
            "successful": 3, "failed": 0, "wallet_blocked": 1, "wallet_probe_not_blocked": 0, "other_or_unknown": 2,
        },
        "cpu": {"source": "process_pprof_flat", "coverage_ms": 400,
                "total_cpu_us": 2_000, "tokenizer_flat_us": 500, "json_flat_us": 300},
    }


class WalletCPUReport(unittest.TestCase):
    def test_distinct_phases_missing_not_zero_and_safe_tails(self):
        old = manifest()
        new = copy.deepcopy(old)
        new["wallet"][0]["wallet_lock_query_us"] = 32
        new["wallet"][1]["wallet_lock_query_us"] = 34
        out = report.compare(old, new)
        self.assertTrue(out["cohort_matches"])
        self.assertEqual([], out["comparison_cautions"])
        for key, expected in (("baseline", 40), ("candidate", 32)):
            run = out[key]
            durations = run["wallet"]["reservation"]["acknowledged"]
            self.assertEqual({"n": 2, "p50": expected, "p95": None, "p99": None}, durations["wallet_lock_query_us"])
            self.assertEqual(3, durations["pool_and_begin_us"]["p50"])
            self.assertEqual(60_000, run["pools"]["main"]["wait_duration_ns_delta"])
            self.assertEqual(1, run["pg_wait_samples"]["wallet_blocked"])
            self.assertEqual(500, run["cpu"]["tokenizer_flat_us"])
            self.assertEqual(0, run["relay"]["relay_api_error"]["billing_prepare_wall_ms"]["n"])
        self.assertEqual(94, report.dist(range(100))["p95"])
        self.assertEqual(98, report.dist(range(100))["p99"])

    def test_incomparable_and_missing_data_remain_visible(self):
        old, new = manifest(), manifest()
        new["cohort"]["shape_sha256"] = "b" * 64
        new["requests"] = {"issued": 1, "resolved": 0, "failed": 0}
        new["pools"] = new["pg_wait_samples"] = new["cpu"] = None
        out = report.compare(old, new)
        self.assertFalse(out["cohort_matches"])
        self.assertEqual(
            ["shape_sha256", "unissued_or_unresolved_requests", "pool_counters_missing", "pg_observation_missing", "cpu_missing"],
            out["comparison_cautions"],
        )
        self.assertIsNone(out["candidate"]["cpu"])

    def test_reject_mixed_invalid_math(self):
        corruptions = [
            lambda r: r["wallet"][0].update(tx_return_us=2),
            lambda r: r["wallet"][0].update(commit_acknowledged=1),
            lambda r: r["wallet"][0].update(wallet_lock_query_us=True),
            lambda r: r["wallet"][0].pop("wallet_lock_query_us"),
            lambda r: r["cohort"].update(wallet_sample_permille=0),
            lambda r: r["pools"]["main"]["after"].update(wait_count=99),
            lambda r: r["pools"]["main"]["after"].update(max_open_connections=19),
            lambda r: r["cpu"].update(json_flat_us=2000),
            lambda r: r["pg_wait_samples"].update(wallet_blocked=5),
            lambda r: r["requests"].update(resolved=3),
        ]
        for i, break_record in enumerate(corruptions):
            with self.subTest(i=i):
                candidate = manifest()
                break_record(candidate)
                with self.assertRaises(report.InvalidInput):
                    report.compare(manifest(), candidate)

    def test_pool_wait_can_cross_window_without_new_wait(self):
        r = manifest()
        r["pools"]["main"]["after"]["wait_count"] = 100
        summary = report.compare(r, r)["candidate"]
        self.assertEqual(0, summary["pools"]["main"]["wait_count_delta"])
        self.assertEqual(60_000, summary["pools"]["main"]["wait_duration_ns_delta"])

    def test_privacy_stdout_stderr_and_duplicate_fields(self):
        r = manifest()
        marker = "not-a-real-key-PRIVATE-should-not-escape"
        r["wallet"][0].update(request_id=marker, error=marker, sql=marker, headers=[marker])
        r["relay"][0].update(key=marker, upstream_error=marker, path=marker)
        r["cohort"]["retained_user"] = marker
        code = str(Path(report.__file__).resolve())
        with tempfile.TemporaryDirectory() as directory:
            baseline, candidate = [Path(directory) / file for file in ("base.json", marker + ".json")]
            baseline.write_text(json.dumps(r), encoding="UTF-8")
            candidate.write_text(json.dumps(r), encoding="UTF-8")
            result = subprocess.run([sys.executable, code, str(baseline), str(candidate)], capture_output=True, timeout=5)
            self.assertEqual(0, result.returncode)
            self.assertNotIn(marker.encode(), result.stdout + result.stderr)
            candidate.write_text('{"version":1,"version":2,"private":"' + marker + '"}', encoding="UTF-8")
            result = subprocess.run([sys.executable, code, str(baseline), str(candidate)], capture_output=True, timeout=5)
            self.assertEqual(2, result.returncode)
            self.assertIn(b"duplicate JSON field", result.stderr)
            self.assertNotIn(marker.encode(), result.stdout + result.stderr)

    def test_malformed_cli_arguments_do_not_echo(self):
        marker = "not-a-real-key-PRIVATE-argument"
        code = str(Path(report.__file__).resolve())
        for args in ([], ["baseline", "candidate", marker], ["--" + marker]):
            result = subprocess.run([sys.executable, "-B", code, *args], capture_output=True, timeout=5)
            self.assertEqual(2, result.returncode)
            self.assertEqual(b"", result.stdout)
            self.assertEqual(b"invalid sanitized evidence: expected baseline and candidate paths\n", result.stderr)

    def test_fixture_process_cpu_is_not_pprof_comparable(self):
        old, new = manifest(), manifest()
        new["cpu"]["source"] = "fixture_process_cpu"
        self.assertIn("cpu_sources_or_windows_differ", report.compare(old, new)["comparison_cautions"])


PG_SMOKE_DSN = re.compile(r"postgres://agent@127\.0\.0\.1:([1-9][0-9]{0,4})/wallet_cpu_smoke\?sslmode=disable", re.ASCII)
PG_SMOKE_ERROR = "wallet CPU PostgreSQL fixture failed"
PG_SMOKE_SWITCH = "--isolated-wallet-cpu-pg-smoke"


class PGSmokeFailure(ValueError):
    pass


def psql_config(dsn, environ):
    match = PG_SMOKE_DSN.fullmatch(dsn)
    if match is None or int(match[1]) > 65_535:
        raise PGSmokeFailure
    port = match[1]
    # Do not give libpq a conninfo string to interpret. PGHOSTADDR overrides
    # hostname resolution; pin it explicitly as well. Only the runner's
    # binary/library search paths survive, not PG*, HOME, PSQLRC or proxies.
    env = {k: environ[k] for k in ("PATH", "LD_LIBRARY_PATH") if k in environ}
    env.update(PGHOSTADDR="127.0.0.1", PGSSLMODE="disable", PGGSSENCMODE="disable",
               PGCONNECT_TIMEOUT="3", PGPASSFILE=os.devnull, PGSERVICEFILE=os.devnull)
    argv = ["psql", "--no-psqlrc", "--no-password", "--no-align", "--tuples-only", "--quiet",
            "--host", "127.0.0.1", "--port", port, "--username", "agent", "--dbname", "wallet_cpu_smoke",
            "-v", "ON_ERROR_STOP=1"]
    return argv, env, port


def wait_or_kill(process):
    try:
        return process.wait(timeout=4)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=4)
        raise PGSmokeFailure from None


def pg_smoke(dsn):
    command, env, port = psql_config(dsn, os.environ)

    def query(statement):
        result = subprocess.run(command + ["-c", statement], capture_output=True, timeout=4, text=True, env=env)
        if result.returncode:
            raise PGSmokeFailure
        return result.stdout.strip()

    # This SELECT is read-only; do not create a table before checking the
    # actual server and local database, instead of trusting only the argv.
    expected = f"127.0.0.1|{port}|wallet_cpu_smoke|agent|180006"
    if query("SELECT host(inet_server_addr()), inet_server_port(), current_database(), "
             "current_user, current_setting('server_version_num')") != expected:
        raise PGSmokeFailure
    query("CREATE TABLE synthetic_wallet_probe(id int primary key); INSERT INTO synthetic_wallet_probe VALUES(1)")
    try:
        start = time.perf_counter_ns()
        query("SELECT id FROM synthetic_wallet_probe WHERE id=1 FOR UPDATE")
        unblocked_us = (time.perf_counter_ns() - start) // 1_000

        owner = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                 env=env, text=True)
        try:
            owner.stdin.write("BEGIN; SET LOCAL statement_timeout='3s'; SELECT id FROM synthetic_wallet_probe WHERE id=1 FOR UPDATE;\n")
            owner.stdin.flush()
            with selectors.DefaultSelector() as ready:
                ready.register(owner.stdout, selectors.EVENT_READ)
                if not ready.select(4) or owner.stdout.readline().strip() != "1":
                    raise PGSmokeFailure
            start = time.perf_counter_ns()
            waiter = subprocess.Popen(
                command + ["-c", "SET statement_timeout='3s'; SELECT id FROM synthetic_wallet_probe WHERE id=1 FOR UPDATE"],
                env={**env, "PGAPPNAME": "wallet_cpu_smoke_waiter"}, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            )
            try:
                seen_blocker = False
                for _ in range(20):
                    seen_blocker = query(
                        "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name='wallet_cpu_smoke_waiter' "
                        "AND wait_event_type='Lock' AND cardinality(pg_blocking_pids(pid))>0)"
                    ) == "t"
                    if seen_blocker:
                        break
                if not seen_blocker:
                    raise PGSmokeFailure
                owner.stdin.write("COMMIT;\n")
                owner.stdin.flush()
                if wait_or_kill(waiter):
                    raise PGSmokeFailure
                blocked_us = (time.perf_counter_ns() - start) // 1_000
            finally:
                if waiter.poll() is None:
                    waiter.kill()
                waiter.wait(timeout=4)
        finally:
            try:
                owner.stdin.close()
            finally:
                try:
                    if wait_or_kill(owner):
                        raise PGSmokeFailure
                finally:
                    owner.stdout.close()
    finally:
        query("DROP TABLE synthetic_wallet_probe")

    # Actual Python process CPU; not gateway CPU or tokenization.
    wall_start, cpu_start = time.perf_counter_ns(), time.process_time_ns()
    for _ in range(500):
        json.dumps({"synthetic": ["test"] * 30, "n": 1})
    json_us = (time.process_time_ns() - cpu_start) // 1_000
    coverage_ms = max(1, (time.perf_counter_ns() - wall_start + 999_999) // 1_000_000)
    return {"unblocked_us": unblocked_us, "blocked_us": blocked_us, "json_cpu_us": json_us, "coverage_ms": coverage_ms}


def pg_smoke_main():
    try:
        result = pg_smoke(os.environ.get("WALLET_CPU_TEST_PG_DSN", ""))
    except (ValueError, OSError, subprocess.SubprocessError):
        # stderr may contain connection strings, usernames or arbitrary
        # server output. No raw child stdout/stderr or exception is printed.
        print(PG_SMOKE_ERROR, file=sys.stderr)
        return 2
    print(json.dumps(result, sort_keys=True))
    return 0


def smoke_subprocess(dsn, **environ):
    try:
        return subprocess.run(
            [sys.executable, "-B", str(Path(__file__).resolve()), PG_SMOKE_SWITCH],
            env={**os.environ, **environ, "WALLET_CPU_TEST_PG_DSN": dsn},
            capture_output=True, timeout=20, text=True,
        )
    except (OSError, subprocess.SubprocessError):
        return subprocess.CompletedProcess([], 2, "", PG_SMOKE_ERROR + "\n")


class PGSmokeSafety(unittest.TestCase):
    def test_reject_alternate_endpoints_and_credentials(self):
        good = "postgres://agent@127.0.0.1:28797/wallet_cpu_smoke?sslmode=disable"
        invalid = (
            good.replace(":28797", ":28797,203.0.113.4:5432"),
            good.replace(":28797", ":28797,localhost:5432"),
            good.replace(":28797", ""),
            good.replace(":28797", ":65536"),
            good.replace(":28797", ":0"),
            good.replace("agent@", "agent:synthetic-private@"),
            good.replace("agent@", "other@"),
            good.replace("127.0.0.1", "localhost"),
            good.replace("127.0.0.1", "%2Ftmp%2Fpg"),
            good + "&hostaddr=203.0.113.4",
            good + "&service=alternate",
            good + " ",
        )
        for number, dsn in enumerate(invalid):
            with self.subTest(case=number), self.assertRaises(PGSmokeFailure):
                psql_config(dsn, {})

    def test_env_and_argv_are_explicit_no_inherited_libpq_defaults(self):
        dsn = "postgres://agent@127.0.0.1:12345/wallet_cpu_smoke?sslmode=disable"
        poison = {
            "PATH": "/tmp/trusted-tools", "LD_LIBRARY_PATH": "/tmp/trusted-libs", "PGHOST": "example.invalid",
            "PGHOSTADDR": "203.0.113.4", "PGPORT": "4040", "PGDATABASE": "foreign", "PGUSER": "foreign",
            "PGPASSWORD": "synthetic-private", "PGSERVICE": "foreign", "PGSERVICEFILE": "/tmp/alternate.conf",
            "PGOPTIONS": "-c search_path=foreign", "PGAPPNAME": "wrong", "PSQLRC": "/tmp/untrusted",
            "HOME": "/tmp/user-with-service-config", "LD_PRELOAD": "/tmp/untrusted-lib",
        }
        argv, env, port = psql_config(dsn, poison)
        self.assertEqual("12345", port)
        self.assertEqual(
            {"PATH": poison["PATH"], "LD_LIBRARY_PATH": poison["LD_LIBRARY_PATH"], "PGHOSTADDR": "127.0.0.1",
             "PGSSLMODE": "disable", "PGGSSENCMODE": "disable", "PGCONNECT_TIMEOUT": "3",
             "PGPASSFILE": os.devnull, "PGSERVICEFILE": os.devnull},
            env,
        )
        self.assertIn("--no-password", argv)
        self.assertIn("--no-psqlrc", argv)
        self.assertEqual(["--host", "127.0.0.1", "--port", port, "--username", "agent", "--dbname", "wallet_cpu_smoke"],
                         argv[6:14])
        self.assertNotIn(dsn, argv)

    def test_effective_endpoint_mismatch_fails_before_ddl(self):
        dsn = "postgres://agent@127.0.0.1:28797/wallet_cpu_smoke?sslmode=disable"
        with tempfile.TemporaryDirectory() as directory:
            folder = Path(directory)
            stub = folder / "psql"
            calls = folder / "calls"
            stub.write_text(f"#!/bin/sh\nprintf '%s\n' \"$*\" >> '{calls}'\n"
                            "printf '127.0.0.1|9999|wallet_cpu_smoke|agent|180006\n'\n", encoding="UTF-8")
            stub.chmod(0o700)
            result = smoke_subprocess(dsn, PATH=directory)
            self.assertEqual(2, result.returncode)
            self.assertEqual(PG_SMOKE_ERROR + "\n", result.stderr)
            self.assertEqual("", result.stdout)
            statements = calls.read_text(encoding="UTF-8").splitlines()
            self.assertEqual(1, len(statements))
            self.assertIn("SELECT host(inet_server_addr())", statements[0])
            self.assertNotIn("CREATE TABLE", statements[0])

    def test_no_subprocess_for_invalid_dsn_and_no_child_diagnostics(self):
        marker = "not-actual-private-sentinel"
        good = "postgres://agent@127.0.0.1:28797/wallet_cpu_smoke?sslmode=disable"
        with tempfile.TemporaryDirectory() as directory:
            folder = Path(directory)
            stub = folder / "psql"
            calls = folder / "calls"
            # Fake child sends arbitrary output and cannot open any socket.
            stub.write_text(f"#!/bin/sh\nprintf 'called\n' >> '{calls}'\n"
                            f"printf '{marker} bad SQL and DSN\n' >&2\nprintf '{marker}\n'\nexit 17\n", encoding="UTF-8")
            stub.chmod(0o700)
            bad = good.replace(":28797", ":28797,203.0.113.4:5432") + f"&password={marker}"
            rejected = smoke_subprocess(bad, PATH=directory)
            self.assertEqual(2, rejected.returncode)
            self.assertFalse(calls.exists())
            failed = smoke_subprocess(good, PATH=directory, PGHOSTADDR="203.0.113.4", PGSERVICE=marker)
            self.assertEqual("called\n", calls.read_text(encoding="UTF-8"))
            self.assertEqual(2, failed.returncode)
            for result in (rejected, failed):
                self.assertEqual("", result.stdout)
                self.assertEqual(PG_SMOKE_ERROR + "\n", result.stderr)


class DisposablePGSmoke(unittest.TestCase):
    """One row, one contention event, no app sessions or sustained pressure."""

    def test_confirmed_postgres_row_wait_is_separate_from_probe_and_process_cpu(self):
        dsn = os.getenv("WALLET_CPU_TEST_PG_DSN")
        if not dsn:
            self.skipTest("requires an explicitly designated disposable PostgreSQL")
        result = smoke_subprocess(dsn)
        self.assertEqual(0, result.returncode, PG_SMOKE_ERROR)
        times = json.loads(result.stdout)
        json_us, coverage_ms = times["json_cpu_us"], times["coverage_ms"]
        self.assertGreater(json_us, 0)
        old, new = manifest(), manifest()
        for run, us, blocked in ((old, times["unblocked_us"], 0), (new, times["blocked_us"], 1)):
            run["cohort"].update(scheduled=1, relay_sample_permille=0, window_ms=max(500, coverage_ms))
            run["requests"] = {"issued": 1, "resolved": 1, "failed": 0}
            run["relay"] = []
            run["wallet"] = [
                {"operation": "reservation", "commit_acknowledged": True,
                 "wallet_lock_query_us": us, "tx_return_us": us}
            ]
            run["pools"] = None  # psql is not Go's database/sql pool
            run["cpu"] = {"source": "fixture_process_cpu", "coverage_ms": coverage_ms, "total_cpu_us": json_us,
                          "tokenizer_flat_us": 0, "json_flat_us": json_us}
            run["pg_wait_samples"] = {"successful": 1, "failed": 0, "wallet_blocked": blocked,
                                      "wallet_probe_not_blocked": 0, "other_or_unknown": 1 - blocked}
        out = report.compare(old, new)
        self.assertIn("pool_counters_missing", out["comparison_cautions"])
        self.assertEqual(1, out["candidate"]["pg_wait_samples"]["wallet_blocked"])
        self.assertEqual(0, out["baseline"]["pg_wait_samples"]["wallet_blocked"])
        self.assertEqual(json_us, out["candidate"]["cpu"]["json_flat_us"])
        self.assertEqual({"n": 1, "p50": None, "p95": None, "p99": None},
                         out["candidate"]["wallet"]["reservation"]["acknowledged"]["wallet_lock_query_us"])


if __name__ == "__main__":
    if sys.argv[1:] == [PG_SMOKE_SWITCH]:
        sys.exit(pg_smoke_main())
    unittest.main()
