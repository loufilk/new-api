"""Regression coverage for the CI integration-test evidence gate."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location(
    "go_test_report", Path(__file__).resolve().parents[1] / "check_go_test_report.py"
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def event(action, test=None):
    value = {"Action": action, "Package": "example/integration"}
    if test is not None:
        value["Test"] = test
    return value


class GoTestReportTests(unittest.TestCase):
    def check(self, events, required=None, reject_skipped_descendants=False, require_run=False):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "go.jsonl"
            path.write_text("".join(json.dumps(item) + "\n" for item in events), encoding="utf-8")
            return MODULE.validate_report(path, required or ["TestRealDatabase"], reject_skipped_descendants, require_run)

    def test_accepts_executed_required_test_and_completed_package(self):
        self.assertEqual(
            self.check([event("start"), event("run", "TestRealDatabase"), event("pass", "TestRealDatabase"), event("pass")]),
            {"TestRealDatabase": "pass"},
        )

    def test_rejects_empty_report(self):
        with self.assertRaises(ValueError):
            self.check([])

    def test_run_mode_requires_exactly_one_run_and_pass_for_each_node(self):
        required = ["TestMatrix", "TestMatrix/sqlite", "TestMatrix/sqlite/leaf"]
        events = [event(action, name) for name in required for action in ("run", "pass")]
        events.append(event("pass"))
        self.assertEqual(dict.fromkeys(required, "pass"), self.check(events, required, True, True))
        for name in required:
            for action in ("run", "pass"):
                with self.subTest(name=name, action=action, defect="duplicate"):
                    with self.assertRaisesRegex(ValueError, "exactly one RUN and PASS"):
                        self.check(events + [event(action, name)], required, True, True)
                with self.subTest(name=name, action=action, defect="missing"):
                    filtered = [item for item in events if item != event(action, name)]
                    with self.assertRaises(ValueError):
                        self.check(filtered, required, True, True)

    def test_default_mode_preserves_historical_pass_only_semantics(self):
        self.assertEqual({"TestRealDatabase": "pass"}, self.check([
            event("pass", "TestRealDatabase"), event("pass", "TestRealDatabase"), event("pass")]))

    def test_run_mode_keeps_failed_and_skipped_descendants_rejected(self):
        complete = [event("run", "TestRealDatabase"), event("pass", "TestRealDatabase"), event("pass")]
        for action in ("fail", "skip"):
            with self.subTest(action=action):
                with self.assertRaises(ValueError):
                    self.check(complete + [event(action, "TestRealDatabase/descendant")],
                               reject_skipped_descendants=True, require_run=True)

    def test_rejects_successful_package_without_required_test(self):
        with self.assertRaises(ValueError):
            self.check([event("start"), event("pass")])

    def test_rejects_skipped_required_test(self):
        with self.assertRaises(ValueError):
            self.check([event("start"), event("skip", "TestRealDatabase"), event("pass")])

    def test_rejects_failure_even_if_later_event_reports_pass(self):
        with self.assertRaises(ValueError):
            self.check([event("start"), event("fail", "TestRealDatabase"), event("pass", "TestRealDatabase"), event("pass")])

    def test_rejects_truncated_package_after_test_pass(self):
        with self.assertRaises(ValueError):
            self.check([event("start"), event("pass", "TestRealDatabase")])

    def test_requires_exact_database_subtest(self):
        with self.assertRaises(ValueError):
            self.check([event("start"), event("pass", "TestMatrix"), event("skip", "TestMatrix/postgres"), event("pass")], ["TestMatrix/postgres"])

    def test_strict_mode_rejects_skipped_case_inside_passing_required_database(self):
        with self.assertRaisesRegex(ValueError, "descendant was skipped"):
            self.check([
                event("start"), event("skip", "TestMatrix/postgres/legacy_upgrade"),
                event("pass", "TestMatrix/postgres"), event("pass", "TestMatrix"), event("pass"),
            ], ["TestMatrix/postgres"], reject_skipped_descendants=True)

    def test_strict_mode_allows_unselected_database_skip(self):
        self.assertEqual(self.check([
            event("start"), event("skip", "TestMatrix/mysql"),
            event("pass", "TestMatrix/postgres/migration"), event("pass", "TestMatrix/postgres"),
            event("pass", "TestMatrix"), event("pass"),
        ], ["TestMatrix/postgres"], reject_skipped_descendants=True), {"TestMatrix/postgres": "pass"})

    def test_strict_mode_rejects_skip_even_if_later_reported_as_pass(self):
        with self.assertRaisesRegex(ValueError, "descendant was skipped"):
            self.check([
                event("start"), event("skip", "TestRealDatabase/legacy_upgrade"),
                event("pass", "TestRealDatabase/legacy_upgrade"), event("pass", "TestRealDatabase"), event("pass"),
            ], reject_skipped_descendants=True)

    def test_accepts_build_diagnostic_with_import_path(self):
        result = self.check([
            {"Action": "build-output", "ImportPath": "example/helper", "Output": "compiler warning"},
            event("start"), event("pass", "TestRealDatabase"), event("pass"),
        ])
        self.assertEqual(result, {"TestRealDatabase": "pass"})

    def test_rejects_build_failure_with_import_path(self):
        with self.assertRaises(ValueError):
            self.check([
                {"Action": "build-fail", "ImportPath": "example/helper"},
                event("start"), event("pass", "TestRealDatabase"), event("pass"),
            ])

    def test_rejects_malformed_json(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "go.jsonl"
            path.write_text('{"Action":', encoding="utf-8")
            with self.assertRaises(ValueError):
                MODULE.validate_report(path, ["TestRealDatabase"])


if __name__ == "__main__":
    unittest.main()
