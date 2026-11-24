"""Offline unit and CLI cases for the isolated relay transport comparison."""
import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import relay_transport_compare as compare


def sample(case=1, path="direct", reused=False, proto=1, text=78):
    return {
        "path": path, "case": case,
        "client": {"outcome": "complete_text", "status": 200, "first_header_ms": 7,
                   "first_visible_text_ms": text, "duration_ms": 150},
        "relay": {"status": 200, "relay_api_error": False, "upstream_attempts": 1,
                  "ingress_to_first_upstream_event_ms": 12,
                  "ingress_to_first_usable_output_ms": 26,
                  "upstreams": [{"protocol_major": proto, "connection_reused": reused, "status": 200,
                                 "get_connection_ms": 0 if reused else 16,
                                 "written_to_first_header_byte_ms": 5}]},
    }


class TransportCompareTest(unittest.TestCase):
    def invoke(self, baseline, candidate):
        with tempfile.TemporaryDirectory() as scratch:
            a, b = (Path(scratch) / name for name in ("baseline.ndjson", "candidate.ndjson"))
            a.write_text(baseline if isinstance(baseline, str) else "".join(json.dumps(r) + "\n" for r in baseline),
                         encoding="utf-8")
            b.write_text(candidate if isinstance(candidate, str) else "".join(json.dumps(r) + "\n" for r in candidate),
                         encoding="utf-8")
            return subprocess.run([sys.executable, "-B", str(Path(compare.__file__)), "--baseline", str(a),
                                   "--candidate", str(b)], capture_output=True, text=True, check=False)

    def test_microsecond_work_stages_include_failure_without_inventing_missed_phases(self):
        old = sample()
        failed = {
            "path": "direct", "case": 1,
            "client": {"outcome": "http_error", "status": 400, "duration_ms": 10, "first_header_ms": 9},
            "relay": {"status": 400, "relay_api_error": True, "upstream_attempts": 0,
                      "request_decode_validate_us": 1140, "billing_token_metadata_us": 0},
        }
        proc = self.invoke([old], [failed])
        self.assertEqual(proc.returncode, 0, proc.stderr)
        direct = json.loads(proc.stdout)["paths"]["direct"]
        before = direct["baseline"]["controller_work_all_sampled_us"]
        after = direct["candidate"]["controller_work_all_sampled_us"]
        self.assertEqual(0, before["request_decode_validate_us"]["n"])
        self.assertEqual(1, after["request_decode_validate_us"]["n"])
        self.assertEqual(1140, after["request_decode_validate_us"]["p50"])
        self.assertEqual(0, after["billing_token_metadata_us"]["p50"])
        self.assertEqual(0, after["billing_token_estimate_us"]["n"])
        self.assertEqual(0, direct["candidate"]["selection_coverage"]["selected_single_successful_http"])
        failed["relay"]["billing_token_metadata_us"] = True
        self.assertEqual(2, self.invoke([old], [failed]).returncode)

    def test_fresh_and_reuse_do_not_merge_protocol_or_caddy(self):
        a = [sample(1), sample(2, reused=True, proto=2), sample(3, path="caddy", reused=True, proto=1)]
        b = copy.deepcopy(a)
        b[0]["client"]["first_visible_text_ms"] += 15
        b[1]["client"]["first_visible_text_ms"] -= 10
        b[2]["client"]["first_visible_text_ms"] += 5
        proc = self.invoke(a, b)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(proc.stdout)
        self.assertFalse(result["production_or_capacity_qualified"])
        direct = result["paths"]["direct"]
        first = direct["baseline"]["transport_single_successful_http_by_protocol_and_reuse"]
        self.assertEqual(set(first), {"http1:fresh", "http2:reused"})
        self.assertEqual(first["http2:reused"]["stages_ms"]["get_connection_ms"]["p95"], 0)
        self.assertEqual(direct["paired_client_visible_text_change_candidate_minus_baseline_ms"],
                         {"n": 2, "p50": -10, "p95": 15, "p99": 15})
        self.assertEqual(result["paths"]["caddy"]["paired_client_visible_text_change_candidate_minus_baseline_ms"]["p95"], 5)

    def test_http_200_error_control_only_and_trace_cap_are_counted(self):
        a = [sample(i) for i in range(1, 7)]
        a[0]["client"] = {"outcome": "stream_error", "status": 200, "first_header_ms": 6}
        a[0]["relay"]["relay_api_error"] = True
        a[1]["client"] = {"outcome": "no_text", "status": 200, "first_header_ms": 6, "duration_ms": 100}
        a[1]["relay"]["upstream_attempts"] = 10
        a[1]["relay"]["upstreams"] += [{} for _ in range(7)]
        a[2].pop("relay")
        a[3]["relay"]["upstreams"] = [{}]
        a[4]["relay"]["upstreams"][0]["status"] = 429
        a[5]["relay"]["upstreams"], a[5]["relay"]["upstream_attempts"] = [], 0
        result = self.invoke(a, a)
        self.assertEqual(result.returncode, 0, result.stderr)
        direct = json.loads(result.stdout)["paths"]["direct"]
        coverage = direct["baseline"]["selection_coverage"]
        self.assertEqual(coverage["relay_single_http_gateway_error_with_2xx"], 1)
        self.assertEqual(coverage["relay_retried_http"], 1)
        self.assertEqual(coverage["attempts_omitted_at_trace_cap"], 2)
        self.assertEqual(coverage["relay_unsampled"], 1)
        self.assertEqual(coverage["relay_single_http_missing_status"], 1)
        self.assertEqual(coverage["relay_single_http_non_2xx"], 1)
        self.assertEqual(coverage["relay_no_http_attempt"], 1)
        self.assertEqual(direct["matching_cases_without_two_complete_text_results"], 2)
        self.assertEqual(direct["baseline"]["client_outcomes"]["no_text"], 1)
        self.assertEqual(direct["baseline"]["client_outcomes"]["stream_error"], 1)

    def test_missing_sample_and_unmatched_cases_not_zeros(self):
        a, b = sample(1), sample(2)
        del a["relay"]["upstreams"][0]["connection_reused"]
        del a["relay"]["upstreams"][0]["written_to_first_header_byte_ms"]
        result = self.invoke([a], [b])
        self.assertEqual(result.returncode, 0, result.stderr)
        direct = json.loads(result.stdout)["paths"]["direct"]
        self.assertEqual(direct["baseline_only_cases"], 1)
        self.assertEqual(direct["candidate_only_cases"], 1)
        self.assertEqual(direct["paired_client_visible_text_change_candidate_minus_baseline_ms"], {"n": 0})
        stages = direct["baseline"]["transport_single_successful_http_by_protocol_and_reuse"]["http1:unknown"]["stages_ms"]
        self.assertEqual(stages["written_to_first_header_byte_ms"], {"n": 0})

    def test_protected_fields_or_log_prefix_never_echo(self):
        sample_with_extra = sample()
        sample_with_extra["relay"]["upstreams"][0]["url"] = "https://secret.invalid/?key=synthetic-sentinel"
        raw_prefixed = "id=synthetic-sentinel relay_timing " + json.dumps(sample()) + "\n"
        for evidence in ([sample_with_extra], raw_prefixed):
            with self.subTest(evidence_type=type(evidence).__name__):
                result = self.invoke(evidence, [sample()])
                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stdout, "")
                self.assertIn("baseline: invalid normalized evidence at row 1", result.stderr)
                self.assertNotIn("synthetic-sentinel", result.stderr)
                self.assertNotIn("invalid/?", result.stderr)

    def test_duplicate_json_keys_and_duplicate_fixture_case_are_refused(self):
        duplicate = json.dumps(sample()).replace('"case": 1', '"case": 2, "case": 1', 1)
        for result, line in ((self.invoke(duplicate, [sample()]), 1), (self.invoke([sample(), sample()], [sample()]), 2)):
            self.assertEqual(result.returncode, 2)
            self.assertIn(f"row {line}", result.stderr)

    def test_bool_not_duration_and_impossible_client_order_fail(self):
        for change in ({"first_visible_text_ms": True}, {"first_visible_text_ms": 1},
                       {"first_visible_text_ms": None}, {"duration_ms": 70}):
            a = sample()
            a["client"].update(change)
            self.assertEqual(self.invoke([a], [sample()]).returncode, 2)

    def test_partial_invalid_candidate_produces_no_aggregate(self):
        result = self.invoke([sample()], json.dumps(sample()) + "\n" + "x" * 8193)
        self.assertEqual(result.returncode, 2)
        self.assertEqual(result.stdout, "")
        self.assertIn("candidate: invalid normalized evidence at row 2", result.stderr)

    def test_control_and_usable_event_are_not_client_visible_text(self):
        a = sample()
        a["client"] = {"outcome": "no_text", "status": 200, "first_header_ms": 7, "duration_ms": 80}
        result = self.invoke([a], [sample()])
        self.assertEqual(result.returncode, 0, result.stderr)
        direct = json.loads(result.stdout)["paths"]["direct"]
        metrics = direct["baseline"]["gateway_event_and_usable_flush_single_successful_http_ms"]
        self.assertEqual(metrics["ingress_to_first_usable_output_ms"]["p50"], 26)
        self.assertEqual(direct["baseline"]["client_completed_text_ms"]["first_visible_text_ms"], {"n": 0})
        self.assertEqual(direct["matching_cases_without_two_complete_text_results"], 1)


if __name__ == "__main__":
    unittest.main()
