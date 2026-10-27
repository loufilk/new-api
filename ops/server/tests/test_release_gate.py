import copy
from datetime import datetime, timedelta, timezone
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


SPEC = importlib.util.spec_from_file_location("release_gate", Path(__file__).parents[1] / "new-api-release-gate.py")
GATE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GATE)


class ReleaseGateTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.commit = "a" * 40
        self.image = "wanchuan/new-api@sha256:" + "b" * 64
        self.start = datetime(2026, 9, 6, tzinfo=timezone.utc)
        self.now = (self.start + timedelta(hours=72)).timestamp()
        self.version = "v1.0.0-rc.33+fixture"
        source = self.artifact("source.tar.gz", b"fixture source archive")
        self.document = {
            "schema_version": 1, "upstream_commit": GATE.UPSTREAM_COMMIT,
            "candidate": {"commit": self.commit, "image": self.image, "version": self.version, "source_archive": source},
            "checks": {}, "canary": {"started_at": self.start.isoformat()},
        }
        for name in GATE.REQUIRED_CHECKS:
            self.document["checks"][name] = {
                "status": "PASS", "commit": self.commit, "image": self.image,
                "completed_at": self.start.isoformat(),
                "evidence": [self.artifact(name + ".log", (name + " fixture evidence").encode())],
            }
        # Five-minute records are the smallest full 72-hour observation series.
        self.rows = [{
            "at": (self.start + timedelta(minutes=5 * index)).isoformat(),
            "commit": self.commit, "image": self.image, "version": self.version,
            "running": True, "liveness": True, "ready": True, "oom": False,
            "restarts": 0, "critical_count": 0, "funds_mismatch_count": 0,
            "funds_snapshot_sha256": "c" * 64,
        } for index in range(865)]
        self.observations(self.rows)

    def artifact(self, name, content):
        (self.root / name).write_bytes(content)
        return {"file": name, "sha256": hashlib.sha256(content).hexdigest()}

    def observations(self, rows):
        data = "\n".join(json.dumps(row) for row in rows).encode()
        self.document["canary"]["observations"] = self.artifact("observations.jsonl", data)

    def validate(self):
        return GATE.validate_release(self.document, self.root, self.commit, self.image, self.now)

    def test_complete_candidate_evidence_passes_without_mutating_files(self):
        before = {p.name: p.read_bytes() for p in self.root.iterdir()}
        self.assertEqual(self.validate()["status"], "PASS")
        self.assertEqual(before, {p.name: p.read_bytes() for p in self.root.iterdir()})

    def test_rc34_requires_matching_version_and_full_observation_window(self):
        self.document["upstream_commit"] = "0c76e4dae77a279e015329b7478e6f02d6b62edd"
        with self.assertRaisesRegex(ValueError, "version"):
            self.validate()
        self.document["candidate"]["version"] = "v1.0.0-rc.34+fixture"
        for row in self.rows:
            row["version"] = "v1.0.0-rc.34+fixture"
        self.observations(self.rows)
        self.assertEqual(self.validate()["status"], "PASS")
        self.observations(self.rows[1:])
        with self.assertRaises(ValueError):
            self.validate()

    def test_missing_pending_and_wrong_candidate_checks_fail(self):
        original = copy.deepcopy(self.document)
        for name in GATE.REQUIRED_CHECKS:
            for change in ("missing", "pending", "identity", "proof"):
                with self.subTest(name=name, change=change):
                    self.document = copy.deepcopy(original)
                    item = self.document["checks"][name]
                    if change == "missing":
                        del self.document["checks"][name]
                    elif change == "pending":
                        item["status"] = "PENDING"
                    elif change == "identity":
                        item["commit"] = "d" * 40
                    else:
                        item["evidence"] = []
                    with self.assertRaises(ValueError):
                        self.validate()

    def test_artifact_tampering_and_path_traversal_fail(self):
        (self.root / "GO_TEST.log").write_text("changed")
        with self.assertRaisesRegex(ValueError, "SHA256"):
            self.validate()
        ref = {"file": "../outside.log", "sha256": "a" * 64}
        with self.assertRaises(ValueError):
            GATE.evidence_file(self.root, ref)

    def test_mutable_images_and_other_releases_fail(self):
        with self.assertRaisesRegex(ValueError, "immutable"):
            GATE.validate_release(self.document, self.root, self.commit, "wanchuan/new-api:latest", self.now)
        self.document["upstream_commit"] = "0" * 40
        with self.assertRaisesRegex(ValueError, "upstream"):
            self.validate()

    def test_canary_health_identity_and_funds_errors_fail(self):
        for key, value in (
            ("ready", False), ("running", "true"), ("liveness", False), ("oom", True),
            ("restarts", 1), ("critical_count", -1), ("funds_mismatch_count", False),
            ("image", "wanchuan/new-api:changed"), ("funds_snapshot_sha256", "e" * 64),
        ):
            with self.subTest(key=key):
                rows = copy.deepcopy(self.rows)
                rows[400][key] = value
                self.observations(rows)
                with self.assertRaises(ValueError):
                    self.validate()

    def test_short_sparse_stale_future_and_duplicate_observations_fail(self):
        future = copy.deepcopy(self.rows)
        future[-1]["at"] = (self.start + timedelta(hours=73)).isoformat()
        duplicate = copy.deepcopy(self.rows)
        duplicate[1]["at"] = duplicate[0]["at"]
        for rows in (self.rows[:799], self.rows[1:], self.rows[:400] + self.rows[403:], future, duplicate):
            with self.subTest(records=len(rows)):
                self.observations(rows)
                with self.assertRaises(ValueError):
                    self.validate()
        self.observations(self.rows)
        self.now += 601
        with self.assertRaisesRegex(ValueError, "fresh"):
            self.validate()

    def test_ambiguous_json_is_rejected(self):
        for content in ('{"status":"FAIL","status":"PASS"}', '{"value":NaN}'):
            with self.assertRaises(ValueError):
                GATE.decode_json(content)


if __name__ == "__main__":
    unittest.main()
