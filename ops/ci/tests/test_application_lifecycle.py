import copy
import importlib.util
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("lifecycle", Path(__file__).parents[1] / "verify_application_lifecycle.py")
lifecycle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lifecycle)


def snapshot_fixture():
    fixture = {}
    for database, tables in (("main", lifecycle.MAIN_ROWS), ("log", lifecycle.LOG_ROWS)):
        fixture[database] = {table: [{"id": 1, "value": "preserved"}] for table in tables}
        fixture[database]["columns"] = {"fixture.id": {"type": "int8", "nullable": "NO"}}
        fixture[database]["indexes"] = {"fixture_unique": "CREATE UNIQUE INDEX fixture_unique ON fixture (id)"}
        fixture[database]["constraints"] = {"fixture.fixture_pkey": "PRIMARY KEY (id)"}
    fixture["main"]["root_aff_code_unique_indexes"] = ["idx_users_aff_code"]
    fixture["main"]["options"] = [
        {"key": "ChuanapiMonitorConfig", "value": json.dumps(lifecycle.MONITOR_CONFIG)},
        {"key": "DataExportEnabled", "value": "false"},
        {"key": "ModelRatio", "value": '{"synthetic-upgrade-model":2.5}'},
        {"key": "PublicModelOwnedBy", "value": "synthetic-owner-label"},
        {"key": "TaskEnabled", "value": "false"},
    ]
    fixture["main"]["users"] = [{"id": 1, "quota": 5000, "quota_paid": 4000, "quota_bonus": 1000, "used_quota": 4294967400}]
    fixture["main"]["tokens"] = [{"id": 1, "remain_quota": 700, "used_quota": 300, "accessed_time": 0}]
    fixture["log"]["audit_logs"] = [{"id": 1, "other": {"enabled": False, "limit": 0, "large_id": 9007199254740993}}]
    return fixture


class ApplicationLifecycleEvidenceTests(unittest.TestCase):
    def test_root_setup_requires_actual_unique_index_evidence(self):
        lifecycle.check_root_aff_code_indexes(["idx_users_aff_code"])
        for invalid in (None, [], {}, "gorm uniqueIndex tag"):
            with self.subTest(evidence=invalid), self.assertRaises(RuntimeError):
                lifecycle.check_root_aff_code_indexes(invalid)
        before = snapshot_fixture()
        after = copy.deepcopy(before)
        after["main"]["root_aff_code_unique_indexes"] = []
        with self.assertRaises(RuntimeError):
            lifecycle.check_snapshot(before, after)

    def test_cleanup_requires_owned_resources_to_be_absent(self):
        with tempfile.TemporaryDirectory() as directory:
            runner = lifecycle.Lifecycle(Path(directory))
            for leftover in ("container", "network", "image"):
                def response(*args, **kwargs):
                    output = "synthetic-leftover" if args[:2] == (leftover, "ls") else ""
                    return subprocess.CompletedProcess(args, 0, output, "")
                with self.subTest(kind=leftover), patch.object(runner, "docker", side_effect=response):
                    with self.assertRaises(RuntimeError):
                        runner.cleanup()
            with patch.object(runner, "docker", return_value=subprocess.CompletedProcess([], 0, "", "")):
                runner.cleanup()

    def test_cleanup_failure_cannot_leave_a_passed_manifest(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            runner = lifecycle.Lifecycle(out)
            manifest = {"result": "passed"}
            with patch.object(runner, "cleanup", side_effect=RuntimeError("synthetic cleanup failure")):
                with self.assertRaises(RuntimeError):
                    lifecycle.finish_evidence(runner, manifest, out)
            saved = json.loads((out / "manifest.json").read_text())
            self.assertEqual("failed", saved["result"])
            self.assertEqual("failed", saved["cleanup"])
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            runner = lifecycle.Lifecycle(out)
            manifest = {"result": "failed", "error": "original failure"}
            with patch.object(runner, "cleanup", side_effect=RuntimeError("synthetic cleanup failure")):
                lifecycle.finish_evidence(runner, manifest, out)
            self.assertEqual("original failure", json.loads((out / "manifest.json").read_text())["error"])

    def test_candidate_preserves_legacy_null_and_permission_defaults(self):
        snapshot = snapshot_fixture()
        snapshot["main"]["tokens"][0]["allow_user_quota_query"] = None
        snapshot["main"]["tasks"][0].update(settlement_pending=None, settlement_attempted_at=None)
        snapshot["main"]["midjourneys"][0]["wallet_debit"] = None
        snapshot["main"]["quota_data"][0].update(cache_read_tokens=0, cache_creation_tokens=0)
        snapshot["log"]["indexes"]["idx_audit_time_event"] = "CREATE INDEX idx_audit_time_event ON audit_logs (created_at, event_id)"
        lifecycle.check_candidate_legacy_defaults(snapshot)
        for table, column, value in (("tokens", "allow_user_quota_query", True), ("midjourneys", "wallet_debit", "[]"),
                                     ("tasks", "settlement_pending", True), ("tasks", "settlement_attempted_at", 0),
                                     ("quota_data", "cache_read_tokens", 1), ("quota_data", "cache_creation_tokens", None)):
            changed = copy.deepcopy(snapshot)
            changed["main"][table][0][column] = value
            with self.subTest(table=table, column=column), self.assertRaises(RuntimeError):
                lifecycle.check_candidate_legacy_defaults(changed)
            del changed["main"][table][0][column]
            with self.assertRaises(RuntimeError):
                lifecycle.check_candidate_legacy_defaults(changed)

    def test_exact_archived_baseline_is_not_the_prior_f25_fixture(self):
        self.assertEqual("093c7197d5eac50787971fd5ff7553a414142fc7", lifecycle.BASELINE_SHA)

    def test_known_baseline_setup_failure_cannot_hide_another_failure(self):
        body = {"success": False, "message": "value too long for type character varying(50) (SQLSTATE 22001)"}
        state = {"root_count": 1, "setup_count": 0, "username": "ci-upgrade", "quota": 100000000}
        version = "v1.0.0-rc.44.chuanapi.model-owner.20261019+gf25c536764"
        lifecycle.check_baseline_setup_failure(body, state, version)
        for invalid in ({"success": True}, {"success": False, "message": "database unavailable"}):
            with self.subTest(body=invalid), self.assertRaises(RuntimeError):
                lifecycle.check_baseline_setup_failure(invalid, state, version)
        for key, value in (("root_count", 0), ("root_count", 2), ("setup_count", 1), ("quota", 0)):
            changed = dict(state, **{key: value})
            with self.subTest(state=changed), self.assertRaises(RuntimeError):
                lifecycle.check_baseline_setup_failure(body, changed, version)
        with self.assertRaises(RuntimeError):
            lifecycle.check_baseline_setup_failure(body, state, "shortened-version")

    def test_candidate_setup_requires_single_linked_paid_credit(self):
        state = {"root_count": 1, "setup_count": 1, "quota": 100000000, "quota_paid": 100000000,
                 "quota_bonus": 0, "quota_reward": 0, "ledger_rows": 1, "operation_rows": 1,
                 "linked_root_credits": 1, "credit_delta": 100000000}
        lifecycle.check_fresh_setup_state(state)
        for key in state:
            with self.subTest(field=key), self.assertRaises(RuntimeError):
                lifecycle.check_fresh_setup_state(dict(state, **{key: state[key] + 1}))

    def test_each_role_must_keep_its_identity(self):
        for username, role in lifecycle.ROLE_ACCOUNTS:
            body = {"success": True, "data": {"username": username, "role": role}}
            lifecycle.check_role_identity(body, username, role)
            for changed in ({"success": False}, {"success": True, "data": {"username": "another", "role": role}},
                            {"success": True, "data": {"username": username, "role": role + 1}}):
                with self.subTest(role=role, body=changed), self.assertRaises(RuntimeError):
                    lifecycle.check_role_identity(changed, username, role)

    def test_http_transport_preserves_error_bodies_and_rejects_invalid_evidence(self):
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                self.server.seen.append((self.command, self.path, self.headers.get("Cookie")))
                status, body, headers = self.server.responses[self.path]
                if self.path == "/write":
                    request_body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                    body = json.dumps({"success": request_body == {"key": "Notice", "value": "synthetic"}
                                       and self.headers.get("Authorization") == "Bearer synthetic-only"}).encode()
                self.send_response(status)
                for key, value in headers.items():
                    self.send_header(key, value)
                self.end_headers()
                self.wfile.write(body)

            do_POST = do_GET
            do_PUT = do_GET

            def log_message(self, *args):
                pass

        server = HTTPServer(("127.0.0.1", 0), Handler)
        server.seen = []
        server.responses = {f"/status/{status}": (status, b'{"success":false}', {}) for status in (200, 401, 403, 500)}
        server.responses.update({
            "/redirect": (302, b'{"success":true}', {"Location": f"http://127.0.0.1:{server.server_port}/status/200"}),
            "/empty": (200, b"", {}), "/malformed": (200, b"not-json", {}),
            "/array": (200, b"[]", {}), "/null": (200, b"null", {}),
            "/oversized": (200, b"x" * (1024 * 1024 + 1), {}),
            "/cookie": (200, b"{}", {"Set-Cookie": "synthetic-not-exported"}),
            "/write": (200, b"{}", {}),
        })
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as directory:
                runner = lifecycle.Lifecycle(Path(directory))
                with self.assertRaisesRegex(RuntimeError, "endpoint has not been verified"):
                    runner.request("/status/200")
                runner.http_origin = f"http://127.0.0.1:{server.server_port}"
                # An explicitly unusable proxy proves that ambient proxy settings
                # cannot receive test sessions or alter our local HTTP evidence.
                with patch.dict(lifecycle.os.environ, {"http_proxy": "http://127.0.0.1:1", "HTTP_PROXY": "http://127.0.0.1:1",
                                                       "ALL_PROXY": "http://127.0.0.1:1", "NO_PROXY": "", "no_proxy": ""}):
                    for status in (200, 401, 403, 500):
                        with self.subTest(status=status):
                            self.assertEqual({"success": False}, runner.request(f"/status/{status}", expected_status=status))
                            self.assertFalse(runner.response_has_cookie)
                    for status in (401, 403, 500):
                        with self.subTest(unexpected_status=status), self.assertRaisesRegex(RuntimeError, "expected HTTP 200"):
                            runner.request(f"/status/{status}")
                    seen_before = list(server.seen)
                    with self.assertRaisesRegex(RuntimeError, "expected HTTP 200"):
                        runner.request("/redirect")
                    self.assertEqual(seen_before + [("GET", "/redirect", None)], server.seen, "redirects must not be followed")
                    for path in ("/empty", "/malformed", "/array", "/null", "/oversized"):
                        with self.subTest(path=path), self.assertRaises(RuntimeError):
                            runner.request(path)
                    for path in ("https://outside.invalid/", "//outside.invalid/", "/line\nbreak"):
                        with self.subTest(path=path), self.assertRaises(RuntimeError):
                            runner.request(path)
                    self.assertEqual({"success": True}, runner.request("/write", "PUT", {"key": "Notice", "value": "synthetic"}, "synthetic-only"))
                    runner.request("/cookie")
                    self.assertTrue(runner.response_has_cookie)
                    runner.request("/status/200")
                    self.assertEqual(("GET", "/status/200", None), server.seen[-1], "response cookies must not be replayed")
                self.assertEqual([], list(Path(directory).iterdir()), "raw authentication headers and bodies must not enter artifacts")
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_http_target_must_match_the_owned_internal_bridge_membership(self):
        app, name, network_id, container_id = "synthetic-app", "synthetic-net", "a" * 64, "b" * 64
        state = {"Name": "/" + app, "Id": container_id, "HostConfig": {"PortBindings": {}},
                 "NetworkSettings": {"Networks": {name: {"NetworkID": network_id, "IPAddress": "172.28.0.3"}}}}
        network = {"Name": name, "Id": network_id, "Internal": True, "Driver": "bridge",
                   "IPAM": {"Config": [{"Subnet": "172.28.0.0/16"}]},
                   "Containers": {container_id: {"Name": app, "IPv4Address": "172.28.0.3/16"}}}
        self.assertEqual("http://172.28.0.3:3000", lifecycle.owned_container_http_origin(state, network, app, name, network_id))
        for field, value in (("Id", "c" * 64), ("Name", "other-network"), ("Internal", False), ("Driver", "host"), ("Containers", {})):
            with self.subTest(field=field), self.assertRaises(RuntimeError):
                lifecycle.owned_container_http_origin(state, dict(network, **{field: value}), app, name, network_id)
        for kind in ("name", "published", "foreign-network", "extra-network", "public-ip", "membership-ip", "outside-subnet"):
            changed, changed_network = copy.deepcopy(state), copy.deepcopy(network)
            if kind == "name":
                changed["Name"] = "/not-owned"
            elif kind == "published":
                changed["HostConfig"]["PortBindings"] = {"3000/tcp": [{"HostIp": "0.0.0.0", "HostPort": "3000"}]}
            elif kind == "foreign-network":
                changed["NetworkSettings"]["Networks"][name]["NetworkID"] = "c" * 64
            elif kind == "extra-network":
                changed["NetworkSettings"]["Networks"]["other"] = {}
            elif kind == "public-ip":
                changed["NetworkSettings"]["Networks"][name]["IPAddress"] = "8.8.8.8"
            elif kind == "membership-ip":
                changed_network["Containers"][container_id]["IPv4Address"] = "172.28.0.4/16"
            else:
                changed_network["IPAM"]["Config"] = [{"Subnet": "172.29.0.0/16"}]
            with self.subTest(kind=kind), self.assertRaises(RuntimeError):
                lifecycle.owned_container_http_origin(changed, changed_network, app, name, network_id)
        with self.assertRaises(RuntimeError):
            lifecycle.owned_container_http_origin(state, network, app, name, None)

    def test_database_layout_is_explicit_and_cannot_target_an_external_database(self):
        with tempfile.TemporaryDirectory() as directory:
            shared = lifecycle.Lifecycle(Path(directory), "shared")
            separate = lifecycle.Lifecycle(Path(directory), "separate")
            self.assertEqual("synthetic_main", shared.log_database)
            self.assertEqual("synthetic_log", separate.log_database)
            with self.assertRaises(RuntimeError):
                lifecycle.Lifecycle(Path(directory), "postgres://production.invalid/database")

    def test_fixture_token_has_no_channel_selector_suffix(self):
        # TokenAuth strips sk- and splits channel selectors on '-'. A malformed
        # fixture would fail authentication before testing version compatibility.
        credential = ("sk-" + lifecycle.TOKEN).removeprefix("sk-").split("-")[0]
        self.assertEqual(lifecycle.TOKEN, credential)
        self.assertRegex(credential, r"^[A-Za-z0-9]{48}$")

    def test_additive_upgrade_and_request_timestamp_preserve_old_rows(self):
        old = snapshot_fixture()
        new = copy.deepcopy(old)
        new["main"]["tasks"][0]["settlement_pending"] = False
        new["main"]["quota_data"][0]["cache_read_tokens"] = 0
        new["main"]["tokens"][0]["accessed_time"] = 1790000000
        new["main"]["options"][0]["value"] = json.dumps(lifecycle.MONITOR_CONFIG, sort_keys=True, separators=(",", ":"))
        lifecycle.check_snapshot(old, new)

    def test_wallet_identity_and_log_corruption_fail_closed(self):
        cases = (("main", "users", "quota", 4999), ("main", "tokens", "remain_quota", 699),
                 ("main", "models", "value", "lost"), ("log", "logs", "value", "lost"),
                 ("log", "audit_logs", "other", {"enabled": False, "limit": 0, "large_id": 9007199254740992}))
        for database, table, column, changed in cases:
            with self.subTest(table=table, column=column):
                old = snapshot_fixture()
                new = copy.deepcopy(old)
                new[database][table][0][column] = changed
                with self.assertRaises(RuntimeError):
                    lifecycle.check_snapshot(old, new)

    def test_clearing_hidden_groups_is_not_accepted_as_preservation(self):
        old = snapshot_fixture()
        new = copy.deepcopy(old)
        changed = copy.deepcopy(lifecycle.MONITOR_CONFIG)
        changed["hidden_groups"] = {}
        new["main"]["options"][0]["value"] = json.dumps(changed)
        with self.assertRaisesRegex(RuntimeError, "ChuanapiMonitorConfig"):
            lifecycle.check_snapshot(old, new)

    def test_missing_empty_or_duplicate_fixtures_cannot_pass(self):
        for database, table in (("main", "users"), ("main", "options"), ("log", "audit_logs")):
            for count in (0, 2):
                with self.subTest(database=database, table=table, count=count):
                    old = snapshot_fixture()
                    old[database][table] = old[database][table][:1] * count
                    with self.assertRaises(RuntimeError):
                        lifecycle.check_snapshot(old, old)
        with self.assertRaises(RuntimeError):
            lifecycle.check_snapshot({"main": {}}, {"main": {}})

    def test_index_or_constraint_loss_cannot_pass(self):
        for database in ("main", "log"):
            for kind in ("columns", "indexes", "constraints"):
                with self.subTest(database=database, kind=kind):
                    old = snapshot_fixture()
                    new = copy.deepcopy(old)
                    new[database][kind] = {}
                    with self.assertRaises(RuntimeError):
                        lifecycle.check_snapshot(old, new)
                    with self.assertRaises(RuntimeError):
                        lifecycle.check_snapshot(new, new)

    def test_restart_must_preserve_candidate_schema_not_only_baseline(self):
        baseline = snapshot_fixture()
        upgraded = copy.deepcopy(baseline)
        upgraded["main"]["columns"]["tasks.settlement_pending"] = {"type": "bool", "nullable": "YES"}
        upgraded["main"]["indexes"]["idx_tasks_settlement_pending"] = "CREATE INDEX idx_tasks_settlement_pending ON tasks (settlement_pending)"
        lifecycle.check_snapshot(baseline, upgraded)
        lifecycle.check_snapshot(upgraded, upgraded, same_schema=True)
        with self.assertRaises(RuntimeError):
            lifecycle.check_snapshot(upgraded, baseline, same_schema=True)
        repeated = copy.deepcopy(upgraded)
        repeated["main"]["columns"]["tasks.unexpected"] = {"type": "int8", "nullable": "YES"}
        with self.assertRaises(RuntimeError):
            lifecycle.check_snapshot(upgraded, repeated, same_schema=True)

    def test_model_visibility_and_candidate_owner_contract(self):
        baseline = {"object": "list", "data": [{"id": lifecycle.MODEL, "owned_by": "baseline-provider"}]}
        lifecycle.check_models(baseline, candidate=False)
        with self.assertRaises(RuntimeError):
            lifecycle.check_models(baseline, candidate=True)
        candidate = copy.deepcopy(baseline)
        candidate["data"][0]["owned_by"] = "synthetic-owner-label"
        lifecycle.check_models(candidate, candidate=True)
        for body in ({"success": False}, {"object": "list", "data": []},
                     {"object": "list", "data": candidate["data"] + [{"id": "unrelated-private-model"}]}):
            with self.subTest(body=body), self.assertRaises(RuntimeError):
                lifecycle.check_models(body, candidate=True)

    def test_provenance_rejects_short_wrong_and_dirty_source(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            (path / "VERSION").write_text("synthetic-version\n")
            with self.assertRaises(RuntimeError):
                lifecycle.verify_checkout(path, "f25c536764")
            with patch.object(subprocess, "check_output", return_value="0" * 40 + "\n"):
                with self.assertRaises(RuntimeError):
                    lifecycle.verify_checkout(path, lifecycle.BASELINE_SHA)
            with patch.object(subprocess, "check_output", side_effect=[lifecycle.BASELINE_SHA + "\n", " M go.mod\n"]):
                with self.assertRaises(RuntimeError):
                    lifecycle.verify_checkout(path, lifecycle.BASELINE_SHA)
            with patch.object(subprocess, "check_output", side_effect=[lifecycle.BASELINE_SHA + "\n", "", ".env\n"]):
                with self.assertRaises(RuntimeError):
                    lifecycle.verify_checkout(path, lifecycle.BASELINE_SHA)
            with patch.object(subprocess, "check_output", side_effect=[lifecycle.BASELINE_SHA + "\n", "", ""]):
                self.assertEqual("synthetic-version", lifecycle.verify_checkout(path, lifecycle.BASELINE_SHA))

    def test_remote_docker_environment_is_never_used(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict("os.environ", {"DOCKER_HOST": "tcp://production.invalid:2375", "DOCKER_CONTEXT": "production"}):
            runner = lifecycle.Lifecycle(Path(directory))
            with patch.object(subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "ok", "")) as execute:
                runner.docker("version")
            arguments, options = execute.call_args
            self.assertEqual(["docker", "--host", "unix:///var/run/docker.sock", "version"], arguments[0])
            self.assertNotIn("DOCKER_HOST", options["env"])
            self.assertNotIn("DOCKER_CONTEXT", options["env"])


if __name__ == "__main__":
    unittest.main()
