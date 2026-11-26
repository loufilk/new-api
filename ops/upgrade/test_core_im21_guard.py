"""Release guard tests with Engine API fixtures; no Docker or live stores contacted."""
import copy
import io
from http.server import BaseHTTPRequestHandler
import json
import sys
import socketserver
import tempfile
import threading
import unittest
from contextlib import redirect_stderr
from datetime import datetime, timezone
from unittest.mock import patch

import core_im21_guard as guard


PIN = "example.invalid/core/gateway@sha256:" + "1" * 64
IID = "sha256:" + "2" * 64
REV = "e" * 40
WINDOW = "9e" * 16
A, B = "/run/fleet/a.sock", "/run/fleet/b.sock"
INFRA, MIG, MASTER, SLAVE = (x * 64 for x in "abcd")
PLAN = {
    "revision": REV, "image": PIN, "release_id": WINDOW,
    "opens": "2026-11-23T10:00:00Z", "expires": "2026-11-23T13:00:00Z", "personal_pricing": True,
    "stores": {"primary": "postgresql://pg.example:5432/main", "log": "postgresql://pg.example:5432/log",
               "redis": "redis://redis.example:6379/0"},
    "hosts": [
        {"socket": A, "id": "first-engine", "migrator": "job", "nodes": {"api": "master"},
         "infrastructure": {"pg": INFRA}},
        {"socket": B, "id": "second-engine", "migrator": None, "nodes": {"follower": "slave"},
         "infrastructure": {}}
    ],
}
NOW = datetime(2026, 11, 23, 12, tzinfo=timezone.utc)


def logs_at(value):
    return f"2026-11-23T10:{value:02}:00.123456789Z"


def proc(name, cid, role, active=False):
    job = role == "migrator"
    return {
        "Name": "/" + name, "Id": cid, "Image": IID, "Path": "/new-api", "Args": ["--log-dir", "/app/logs"],
        "Mounts": [{"Destination": "/data"}], "RestartCount": 0,
        "HostConfig": {"NetworkMode": "application", "PortBindings": {}, "PublishAllPorts": False,
                       "RestartPolicy": {"Name": "no" if job else "always"}},
        "State": {"Status": "exited" if job else "running", "Running": not job,
                  "Health": {"Status": "healthy", "Log": [
                      {"ExitCode": 0, "End": "2026-11-23T11:59:35Z"}]}, "Pid": 0 if job else 111,
                  "Restarting": False, "Paused": False, "Dead": False, "OOMKilled": False,
                  "Error": "", "ExitCode": 0, "StartedAt": logs_at(2 if job else 10),
                  "FinishedAt": logs_at(6) if job else "0001-01-01T00:00:00Z"},
        "Config": {"Image": PIN, "Entrypoint": ["/new-api"], "Healthcheck": {"Test": guard.HEALTH[:]},
                   "Env": [
                       "SQL_DSN=postgres://ddl:do-not-log-this@pg.example/main?sslmode=require" if job else
                       "SQL_DSN=postgresql://dml:another-secret@pg.example/main?sslmode=require",
                       "LOG_SQL_DSN=postgres://user:do-not-log-this@pg.example/log",
                       "REDIS_CONN_STRING=redis://:do-not-log-this@redis.example",
                       "CORE_IM21_RELEASE_ID=" + WINDOW,
                       "NODE_TYPE=" + ("master" if job else role), "NODE_NAME=host-" + name,
                       "CORE_IM21_MIGRATION_MODE=" + ("migrate" if job else "serve"),
                       "CORE_IM21_ROLLOUT_MODE=" + ("active" if active and not job else "locked"),
                       "USER_GROUP_PRICING_ENABLED=" + ("true" if active and not job else "false"),
                   ]},
    }


class Engine:
    def __init__(self):
        self.stage("drained")

    def stage(self, name):
        self.image = {"Id": IID, "RepoDigests": [PIN], "Os": "linux", "Config": {"Labels": {
            "org.opencontainers.image.revision": REV, "org.opencontainers.image.version": guard.BASE + "+g" + REV[:12]},
            "Entrypoint": ["/new-api"]}}
        self.changes = []
        self.rows = {A: {INFRA: {"Id": INFRA, "Name": "/pg", "Path": "docker-entrypoint.sh",
                                 "State": {"Status": "running", "StartedAt": logs_at(0)},
                                 "Config": {"Image": "postgres@other"}}}, B: {}}
        if name != "drained":
            self.rows[A][MIG] = proc("job", MIG, "migrator")
        if name in ("locked", "active"):
            self.rows[A][MASTER] = proc("api", MASTER, "master", name == "active")
            self.rows[B][SLAVE] = proc("follower", SLAVE, "slave", name == "active")

    def get(self, path, resource):
        if resource == "/info":
            return {"ID": "first-engine" if path == A else "second-engine"}
        if resource == "/containers/json?all=1":
            return [{"Id": k} for k in self.rows[path]]
        if resource == "/images/" + IID + "/json":
            return self.image
        if resource.endswith("/changes"):
            return self.changes
        if resource.endswith("/json"):
            return self.rows[path][resource.split("/")[2]]
        raise AssertionError("not a supported, read-only Engine inspection")


class ReleaseGateTest(unittest.TestCase):
    def setUp(self):
        self.engine = Engine()
        self.plan = copy.deepcopy(PLAN)

    def inspect(self, stage):
        return guard.gate(self.plan, stage, self.engine.get, lambda: NOW)

    def window(self, wait):
        steps = []
        guard.release_window(self.plan, wait, lambda s, n: steps.append((s, n)), self.engine.get, lambda: NOW)
        return steps

    def test_forward_requires_all_four_live_stages(self):
        self.assertEqual(self.window(self.engine.stage), [("drained", 1), ("migrated", 2),
                                                          ("locked", 4), ("active", 4)])
        self.engine.stage("active")
        with self.assertRaises(guard.Blocked):
            self.window(lambda _: None)

    def test_im22_requires_explicit_plan_and_exact_stamped_version(self):
        version = "v1.0.0-rc.51.chuanapi.im.22"

        def stage_im22(stage):
            self.engine.stage(stage)
            self.engine.image["Config"]["Labels"]["org.opencontainers.image.version"] = version + "+g" + REV[:12]

        stage_im22("locked")
        with self.assertRaisesRegex(guard.Blocked, "candidate image"):
            self.inspect("locked")
        self.plan["version"] = version
        self.assertEqual(self.window(stage_im22), [("drained", 1), ("migrated", 2),
                                                   ("locked", 4), ("active", 4)])

        for stamp in (guard.BASE + "+g" + REV[:12], version, version + "+g" + "0" * 12):
            with self.subTest(stamp=stamp):
                self.engine.image["Config"]["Labels"]["org.opencontainers.image.version"] = stamp
                with self.assertRaisesRegex(guard.Blocked, "candidate image"):
                    self.inspect("locked")

    def test_refuse_unapproved_explicit_release_versions_before_engine_inspection(self):
        for bad in ("v1.0.0-rc.51.chuanapi.im.23", "v1.0.0-rc.51.chuanapi.im.22+g" + REV[:12], None):
            with self.subTest(bad=bad):
                self.plan["version"] = bad
                with self.assertRaisesRegex(guard.Blocked, "unapproved release plan version"):
                    guard.gate(self.plan, "drained", lambda *_: self.fail("bad plan must not inspect the engine"),
                               lambda: NOW)

    def test_engine_speaks_http_get_no_shell_and_refuses_redirect(self):
        with patch("socket.socket") as sock:
            mock = sock.return_value
            body = b'{"ID":"engine-fixture"}'
            mock.makefile.return_value = io.BytesIO(
                b"HTTP/1.1 200 OK\r\nContent-Length: " + str(len(body)).encode() + b"\r\n\r\n" + body)
            self.assertEqual(guard.engine_get(A, "/info"), {"ID": "engine-fixture"})
            mock.connect.assert_called_once_with(A)
            self.assertTrue(mock.sendall.call_args.args[0].startswith(b"GET /info HTTP/1.1\r\n"))
            mock.makefile.return_value = io.BytesIO(b"HTTP/1.1 302 Found\r\nLocation: /other\r\n\r\n")
            with self.assertRaises(guard.Blocked):
                guard.engine_get(A, "/info")

    def test_real_unix_http_transport_accepts_only_live_engine_responses(self):
        engine = self.engine
        engine.stage("locked")
        # One fixture host runs its one-shot and combined serving/worker master.
        self.plan["hosts"] = self.plan["hosts"][:1]
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                data = json.dumps(engine.get(A, self.path)).encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)
            def log_message(self, *args):
                pass
        with tempfile.TemporaryDirectory() as folder:
            path = folder + "/engine.sock"
            self.plan["hosts"][0]["socket"] = path
            try:
                local_server = socketserver.UnixStreamServer(path, Handler)
            except PermissionError:
                self.skipTest("local AF_UNIX socket creation prohibited on this runner")
            with local_server as server:
                thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01})
                thread.start()
                try:
                    self.assertEqual(len(guard.gate(self.plan, "locked", clock=lambda: NOW)), 3)
                finally:
                    server.shutdown()
                    thread.join()

    def test_extra_old_worker_missing_expected_worker_and_unreachable_host(self):
        self.engine.rows[B]["1" * 64] = proc("old-worker", "1" * 64, "master")
        with self.assertRaisesRegex(guard.Blocked, "unlisted"):
            self.inspect("drained")
        self.engine.stage("locked")
        del self.engine.rows[B][SLAVE]
        with self.assertRaisesRegex(guard.Blocked, "missing"):
            self.inspect("locked")
        self.plan["hosts"][1]["id"] = "wrong-host"
        with self.assertRaisesRegex(guard.Blocked, "engine"):
            self.inspect("locked")

    def test_refuse_unidentified_source_artifact(self):
        for alter in ("tag", "raw", "stale-stamp", "tag-only", "pin-absent", "changed-binary", "mount"):
            with self.subTest(alter=alter):
                self.engine.stage("locked")
                self.plan = copy.deepcopy(PLAN)
                if alter == "tag":
                    self.plan["image"] = "example.invalid/core/gateway:im21"
                if alter == "raw":
                    self.plan["revision"] = guard.RAW
                if alter == "stale-stamp":
                    self.engine.image["Config"]["Labels"]["org.opencontainers.image.version"] = guard.BASE
                if alter == "tag-only":
                    self.engine.rows[A][MASTER]["Config"]["Image"] = "latest"
                if alter == "pin-absent":
                    self.engine.image["RepoDigests"] = []
                if alter == "changed-binary":
                    self.engine.changes = [{"Path": "/new-api", "Kind": 0}]
                if alter == "mount":
                    self.engine.rows[B][SLAVE]["Mounts"].append({"Destination": "/new-api"})
                with self.assertRaises(guard.Blocked):
                    self.inspect("locked")

    def test_deny_inconsistent_mode_readiness_store_or_window(self):
        for key, bad in (("CORE_IM21_ROLLOUT_MODE", "locked"), ("USER_GROUP_PRICING_ENABLED", "false"),
                         ("CORE_IM21_MIGRATION_MODE", ""), ("NODE_TYPE", "master"),
                         ("CORE_IM21_RELEASE_ID", "1" * 32), ("NODE_NAME", "host-api"),
                         ("SQL_DSN", "postgresql://pg.example/other"),
                         ("LOG_SQL_DSN", "postgresql://pg.example/log?host=otherhost"),
                         ("REDIS_CONN_STRING", "redis://redis.example/1")):
            with self.subTest(key=key):
                self.engine.stage("active")
                cfg = self.engine.rows[B][SLAVE]["Config"]
                cfg["Env"] = [v if not v.startswith(key + "=") else key + "=" + bad for v in cfg["Env"]]
                with self.assertRaises(guard.Blocked):
                    self.inspect("active")
        for key, bad in (("Status", "starting"), ("Log", [{"ExitCode": 0, "End": logs_at(12)}]),
                         ("Test", ["CMD", "true"])):
            with self.subTest(key=key):
                self.engine.stage("active")
                node = self.engine.rows[A][MASTER]
                target = node["Config"]["Healthcheck"] if key == "Test" else node["State"]["Health"]
                target[key] = bad
                with self.assertRaises(guard.Blocked):
                    self.inspect("active")

    def test_refuse_stale_failed_online_or_short_circuited_migrator(self):
        for key, bad in (("FinishedAt", logs_at(55)), ("StartedAt", "2026-11-21T10:00:00Z"),
                         ("ExitCode", 1), ("Args", ["--version"]), ("NetworkMode", "host"),
                         ("OOMKilled", True), ("PortBindings", {"3000/tcp": [{}]}), ("RestartCount", 1)):
            with self.subTest(key=key):
                self.engine.stage("active")
                c = self.engine.rows[A][MIG]
                target = c if key in c else c["HostConfig"] if key in c["HostConfig"] else c["State"]
                target[key] = bad
                with self.assertRaises(guard.Blocked):
                    self.inspect("active")

    def test_abort_if_evidence_is_swapped_between_phases(self):
        def swap(stage):
            self.engine.stage(stage)
            if stage == "locked":
                self.engine.rows[A]["1" * 64] = self.engine.rows[A].pop(MIG)
                self.engine.rows[A]["1" * 64]["Id"] = "1" * 64
        with self.assertRaisesRegex(guard.Blocked, "replaced"):
            self.window(swap)

    def test_double_snapshot_rejects_replaced_replica(self):
        self.engine.stage("locked")
        calls = 0
        def get(path, resource):
            nonlocal calls
            if resource == "/info" and path == A:
                calls += 1
                if calls == 2:
                    row = self.engine.rows[A].pop(MASTER)
                    row["Id"] = "1" * 64
                    self.engine.rows[A][row["Id"]] = row
            return self.engine.get(path, resource)
        with self.assertRaisesRegex(guard.Blocked, "changed"):
            guard.gate(self.plan, "locked", get, lambda: NOW)

    def test_stale_plan_or_duplicate_json_fail_closed(self):
        self.plan["expires"] = "2026-11-23T11:00:00Z"
        with self.assertRaisesRegex(guard.Blocked, "stale"):
            self.inspect("drained")
        with self.assertRaises(guard.Blocked):
            guard.strict_json('{"hosts":[],"hosts":[]}')

    def test_rollback_unconditionally_denied_prior_to_inspection(self):
        with patch.object(sys, "argv", ["guard", "rollback"]), redirect_stderr(io.StringIO()) as err:
            self.assertEqual(guard.main(), 1)
        self.assertIn("0dff956", err.getvalue())
        with self.assertRaises(guard.Blocked):
            guard.gate({}, "rollback", lambda *_: self.fail("rollback must never access the engine"))

    def test_daemon_errors_do_not_print_config_values(self):
        with patch.object(sys, "argv", ["guard", "window", "--plan", "/test/inventory"]), \
             patch("pathlib.Path.open", return_value=io.BytesIO(json.dumps(self.plan).encode())), \
             patch.object(guard, "release_window", side_effect=ValueError("password=simulated-secret")), \
             redirect_stderr(io.StringIO()) as err:
            self.assertEqual(guard.main(), 1)
        self.assertNotIn("simulated-secret", err.getvalue())


if __name__ == "__main__":
    unittest.main()
