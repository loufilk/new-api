#!/usr/bin/env python3
"""Compare fixed-schema relay and client timing samples from isolated runs."""
import argparse
from collections import Counter, defaultdict
import json
import sys


PATHS = ("direct", "caddy")
OUTCOMES = ("complete_text", "no_text", "stream_error", "http_error", "canceled", "transport_error")
REPORT_FIELDS = (
    "ingress_to_relay_ms",
    "ingress_to_route_selection_ms",
    "billing_prepare_ms",
    "relay_handler_ms",
    "ingress_to_first_upstream_event_ms",
    "ingress_to_first_usable_output_ms",
)
CONTROLLER_WORK_US_FIELDS = ("request_decode_validate_us", "billing_token_metadata_us", "billing_token_estimate_us")
TRANSPORT_FIELDS = (
    "relay_to_dispatch_ms",
    "get_connection_ms",
    "written_to_first_header_byte_ms",
    "dispatch_to_result_ms",
)
CLIENT_FIELDS = ("first_header_ms", "first_visible_text_ms", "duration_ms")
MAX_ROW_BYTES = 8192
MAX_ROWS = 100_000
MAX_DURATION_MS = 3_600_000


class InvalidEvidence(ValueError):
    pass


class StrictParser(argparse.ArgumentParser):
    def error(self, _):
        # Never reprint arguments, filenames, logger prefixes or raw values.
        raise InvalidEvidence("expected --baseline PATH --candidate PATH")


def expect(ok):
    if not ok:
        raise InvalidEvidence("invalid normalized diagnostic row")


def object_pairs(pairs):
    obj = {}
    for key, value in pairs:
        expect(key not in obj)
        obj[key] = value
    return obj


def shape(obj, required, optional=()):
    expect(type(obj) is dict and set(required) <= obj.keys() <= set(required) | set(optional))


def bounded_int(value, lo, hi):
    expect(type(value) is int and lo <= value <= hi)


def milliseconds(obj, fields):
    for field in fields:
        if field in obj:
            bounded_int(obj[field], 0, MAX_DURATION_MS)


def timing_order(obj, earlier, later):
    if earlier in obj and later in obj:
        expect(obj[earlier] <= obj[later])


def normalized_row(row):
    shape(row, ("path", "case", "client"), ("relay",))
    expect(row["path"] in PATHS)
    bounded_int(row["case"], 1, MAX_ROWS)
    client = row["client"]
    shape(client, ("outcome",), (*CLIENT_FIELDS, "status"))
    expect(client["outcome"] in OUTCOMES)
    milliseconds(client, CLIENT_FIELDS)
    timing_order(client, "first_header_ms", "first_visible_text_ms")
    timing_order(client, "first_header_ms", "duration_ms")
    timing_order(client, "first_visible_text_ms", "duration_ms")
    if "status" in client:
        bounded_int(client["status"], 100, 599)
    if client["outcome"] in ("complete_text", "no_text"):
        expect(200 <= client.get("status", 0) < 300 and "first_header_ms" in client and "duration_ms" in client)
        expect(("first_visible_text_ms" in client) == (client["outcome"] == "complete_text"))
    if "first_visible_text_ms" in client:
        expect(200 <= client.get("status", 0) < 300 and "first_header_ms" in client)
    if "relay" not in row:
        return row
    relay = row["relay"]
    shape(relay, ("status", "relay_api_error", "upstream_attempts"), (*REPORT_FIELDS, *CONTROLLER_WORK_US_FIELDS, "upstreams"))
    bounded_int(relay["status"], 100, 599)
    expect(type(relay["relay_api_error"]) is bool)
    bounded_int(relay["upstream_attempts"], 0, MAX_ROWS)
    milliseconds(relay, REPORT_FIELDS)
    for field in CONTROLLER_WORK_US_FIELDS:
        if field in relay:
            bounded_int(relay[field], 0, MAX_DURATION_MS * 1000)
    attempts = relay.get("upstreams", [])
    expect(type(attempts) is list and len(attempts) == min(8, relay["upstream_attempts"]))
    for attempt in attempts:
        shape(attempt, (), (*TRANSPORT_FIELDS, "status", "protocol_major", "connection_reused"))
        milliseconds(attempt, TRANSPORT_FIELDS)
        if "status" in attempt:
            bounded_int(attempt["status"], 100, 599)
        if "protocol_major" in attempt:
            bounded_int(attempt["protocol_major"], 1, 3)
        if "connection_reused" in attempt:
            expect(type(attempt["connection_reused"]) is bool)
    return row


def read_evidence(path, label):
    rows = {}
    number = 0
    try:
        with open(path, "rb") as source:
            while raw := source.readline(MAX_ROW_BYTES + 1):
                number += 1
                expect(number <= MAX_ROWS and len(raw) <= MAX_ROW_BYTES)
                row = normalized_row(json.loads(raw.decode("utf-8"), object_pairs_hook=object_pairs))
                key = (row["path"], row["case"])
                expect(key not in rows)
                rows[key] = row
    except OSError:
        raise InvalidEvidence(f"{label}: cannot read evidence") from None
    except (ValueError, TypeError, RecursionError):
        raise InvalidEvidence(f"{label}: invalid normalized evidence at row {number}") from None
    if not rows:
        raise InvalidEvidence(f"{label}: empty evidence")
    return rows


def percentile_summary(values):
    v = sorted(values)
    if not v:
        return {"n": 0}
    # Nearest-rank tails; with few rows p99 may be the maximum. Always show n.
    return {"n": len(v), **{f"p{p}": v[(len(v) * p + 99) // 100 - 1] for p in (50, 95, 99)}}


def stages(rows, keys):
    return {key: percentile_summary(r[key] for r in rows if key in r) for key in keys}


def first_transport_bucket(attempt):
    reuse = {None: "unknown", True: "reused", False: "fresh"}[attempt.get("connection_reused")]
    protocol = attempt.get("protocol_major")
    return f"http{protocol if protocol is not None else '_unknown'}:{reuse}"


def summarize(rows):
    count = Counter({outcome: 0 for outcome in OUTCOMES})
    coverage = Counter({key: 0 for key in (
        "relay_sampled", "relay_unsampled", "relay_api_errors", "relay_no_http_attempt",
        "relay_single_http_missing_status", "relay_single_http_non_2xx",
        "relay_single_http_gateway_error_with_2xx", "relay_retried_http",
        "observed_http_attempts", "attempts_omitted_at_trace_cap", "selected_single_successful_http",
    )})
    cohorts = defaultdict(list)
    selected = []
    sampled = []
    for row in rows:
        client, relay = row["client"], row.get("relay")
        count[client["outcome"]] += 1
        if relay is None:
            coverage["relay_unsampled"] += 1
            continue
        coverage["relay_sampled"] += 1
        sampled.append(relay)
        coverage["relay_api_errors"] += relay["relay_api_error"]
        n = relay["upstream_attempts"]
        coverage["observed_http_attempts"] += n
        coverage["attempts_omitted_at_trace_cap"] += max(0, n - 8)
        if n == 0:
            coverage["relay_no_http_attempt"] += 1
            continue
        if n > 1:
            coverage["relay_retried_http"] += 1
            continue
        first = relay["upstreams"][0]
        status = first.get("status")
        if status is None:
            coverage["relay_single_http_missing_status"] += 1
        elif not 200 <= status < 300:
            coverage["relay_single_http_non_2xx"] += 1
        elif relay["relay_api_error"] or not 200 <= relay["status"] < 300:
            coverage["relay_single_http_gateway_error_with_2xx"] += 1
        else:
            coverage["selected_single_successful_http"] += 1
            cohorts[first_transport_bucket(first)].append(first)
            selected.append(relay)
    clients = [r["client"] for r in rows if r["client"]["outcome"] == "complete_text"]
    return {
        "requests": len(rows),
        "client_outcomes": dict(count),
        "selection_coverage": dict(coverage),
        "transport_single_successful_http_by_protocol_and_reuse": {
            key: {"requests": len(group), "stages_ms": stages(group, TRANSPORT_FIELDS)}
            for key, group in sorted(cohorts.items())
        },
        "controller_work_all_sampled_us": stages(sampled, CONTROLLER_WORK_US_FIELDS),
        "gateway_event_and_usable_flush_single_successful_http_ms": stages(selected, REPORT_FIELDS[-2:]),
        "client_completed_text_ms": stages(clients, CLIENT_FIELDS),
    }


def compare(baseline, candidate):
    paths = {}
    for path in PATHS:
        a = {case: r for (p, case), r in baseline.items() if p == path}
        b = {case: r for (p, case), r in candidate.items() if p == path}
        common = a.keys() & b.keys()
        paired = [case for case in common if
                  a[case]["client"]["outcome"] == b[case]["client"]["outcome"] == "complete_text"]
        paths[path] = {
            "baseline": summarize(list(a.values())),
            "candidate": summarize(list(b.values())),
            "matching_cases": len(common),
            "baseline_only_cases": len(a.keys() - b.keys()),
            "candidate_only_cases": len(b.keys() - a.keys()),
            "matching_cases_without_two_complete_text_results": len(common) - len(paired),
            "paired_client_visible_text_change_candidate_minus_baseline_ms": percentile_summary(
                b[k]["client"]["first_visible_text_ms"] - a[k]["client"]["first_visible_text_ms"] for k in paired
            ),
        }
    return {
        "diagnostic": "isolated_trace_comparison_only",
        "production_or_capacity_qualified": False,
        "paths": paths,
    }


def main(argv=None):
    parser = StrictParser(description=__doc__)
    parser.add_argument("--baseline", required=True)
    parser.add_argument("--candidate", required=True)
    try:
        args = parser.parse_args(argv)
        result = compare(read_evidence(args.baseline, "baseline"), read_evidence(args.candidate, "candidate"))
    except InvalidEvidence as exc:
        print(f"relay transport comparator: {exc}", file=sys.stderr)
        return 2
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
