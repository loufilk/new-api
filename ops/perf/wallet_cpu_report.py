#!/usr/bin/env python3
"""Offline comparison of orthogonal wallet, pool, PostgreSQL and CPU evidence."""

import argparse
import json
import sys
from pathlib import Path

MAX_BYTES = 8 << 20
MAX_RECORDS = 50_000
MAX_COUNTER = (1 << 63) - 1
OPERATIONS = ("reservation", "settlement", "recovery_settlement")
WALLET_PHASES = (
    "pool_and_begin_us",
    "wallet_lock_query_us",
    "after_lock_query_to_tx_return_us",
    "tx_return_us",
)
COHORT_NUMBERS = (
    "window_ms", "scheduled", "wallets", "keys", "replicas",
    "wallet_sample_permille", "relay_sample_permille",
)
PG_CLASSES = ("wallet_blocked", "wallet_probe_not_blocked", "other_or_unknown")
CPU_SOURCES = ("process_pprof_flat", "fixture_process_cpu")


class InvalidInput(ValueError):
    """A reason from fixed tool vocabulary, never an input string."""


class StrictParser(argparse.ArgumentParser):
    def error(self, _):
        # Reject malformed argv without repeating flags, filenames or values.
        raise InvalidInput("expected baseline and candidate paths")


def obj(value):
    if not isinstance(value, dict):
        raise InvalidInput("expected a JSON object")
    return value


def number(record, field, minimum=0, maximum=MAX_COUNTER):
    v = obj(record).get(field)
    if type(v) is not int or not minimum <= v <= maximum:
        raise InvalidInput(f"invalid {field}")
    return v


def boolean(record, field):
    v = obj(record).get(field)
    if type(v) is not bool:
        raise InvalidInput(f"invalid {field}")
    return v


def records(payload, field):
    value = obj(payload).get(field)
    if not isinstance(value, list) or len(value) > MAX_RECORDS:
        raise InvalidInput(f"invalid {field} list")
    return value


def dist(values):
    xs = sorted(values)
    count = len(xs)
    # Do not report a tail percentile without at least one observation's
    # worth of nominal tail mass. This is not a statistical confidence bound.
    out = {"n": count}
    for p in (50, 95, 99):
        index = (count * p + 99) // 100 - 1
        out[f"p{p}"] = xs[index] if count * (100 - p) >= 100 else None
    return out


def cohort(payload):
    data = obj(payload.get("cohort"))
    result = {}
    for field in COHORT_NUMBERS:
        sampling = field.endswith("_permille")
        result[field] = number(data, field, 0 if sampling else 1, 1000 if sampling else MAX_COUNTER)
    shape = data.get("shape_sha256")
    if not isinstance(shape, str) or len(shape) != 64 or any(x not in "0123456789abcdef" for x in shape):
        raise InvalidInput("invalid shape_sha256")
    result["shape_sha256"] = shape  # compare in memory only; never output
    return result


def wallet_samples(payload, rate):
    result = {op: {s: [] for s in ("acknowledged", "not_acknowledged")} for op in OPERATIONS}
    data = records(payload, "wallet")
    if data and not rate:
        raise InvalidInput("wallet records with sampling disabled")
    for row in data:
        op = obj(row).get("operation")
        if op not in OPERATIONS:
            raise InvalidInput("unknown wallet operation")
        status = "acknowledged" if boolean(row, "commit_acknowledged") else "not_acknowledged"
        total = number(row, "tx_return_us")
        phases = {k: number(row, k, maximum=total) for k in WALLET_PHASES if k in row}
        # Durations do not overlap, but their sum need not equal tx_return:
        # the canonical transaction does other work before the row probe.
        if sum(v for k, v in phases.items() if k != "tx_return_us") > total:
            raise InvalidInput("wallet phases exceed transaction wall time")
        if "after_lock_query_to_tx_return_us" in phases and "wallet_lock_query_us" not in phases:
            raise InvalidInput("wallet lock completion has no row probe")
        result[op][status].append(phases)
    return {
        op: {s: {"reports": len(rows), **{k: dist([r[k] for r in rows if k in r]) for k in WALLET_PHASES}}
             for s, rows in statuses.items()}
        for op, statuses in result.items()
    }


def billing_samples(payload, rate):
    buckets = {key: [] for key in ("http_2xx", "relay_api_error", "other_http")}
    data = records(payload, "relay")
    if data and not rate:
        raise InvalidInput("relay records with sampling disabled")
    for row in data:
        status = number(row, "status", 100, 599)
        key = "relay_api_error" if boolean(row, "relay_api_error") else ("http_2xx" if 200 <= status < 300 else "other_http")
        buckets[key].append(number(row, "billing_prepare_ms") if "billing_prepare_ms" in row else None)
    return {k: {"reports": len(xs), "billing_prepare_wall_ms": dist([v for v in xs if v is not None])} for k, xs in buckets.items()}


def pools(payload):
    raw = payload.get("pools")
    if raw is None:
        return None
    raw = obj(raw)
    if set(raw) not in ({"main"}, {"main", "log"}):
        raise InvalidInput("invalid pool set")
    result = {}
    for name in ("main", "log"):
        if name not in raw:
            continue
        pool = obj(raw[name])
        before, after = pool.get("before"), pool.get("after")
        delta_count = number(after, "wait_count") - number(before, "wait_count")
        delta_ns = number(after, "wait_duration_ns") - number(before, "wait_duration_ns")
        limit = number(after, "max_open_connections")
        if delta_count < 0 or delta_ns < 0 or limit != number(before, "max_open_connections"):
            raise InvalidInput("pool counter reset or configuration changed")
        # A wait can start before the first snapshot and finish within the
        # window, so a positive duration with zero new events is possible.
        result[name] = {"max_open_connections": limit, "wait_count_delta": delta_count, "wait_duration_ns_delta": delta_ns}
    return result


def observations(payload):
    item = payload.get("pg_wait_samples")
    if item is None:
        return None
    classes = {k: number(item, k) for k in PG_CLASSES}
    successful, failed = number(item, "successful"), number(item, "failed")
    if sum(classes.values()) != successful:
        raise InvalidInput("PostgreSQL observer category/count mismatch")
    return {**classes, "successful": successful, "failed": failed}


def cpu(payload, window_ms):
    item = payload.get("cpu")
    if item is None:
        return None
    source = obj(item).get("source")
    if source not in CPU_SOURCES:
        raise InvalidInput("unknown CPU source")
    coverage = number(item, "coverage_ms", 1, window_ms)
    names = ("total_cpu_us", "tokenizer_flat_us", "json_flat_us")
    values = {k: number(item, k) for k in names}
    if values[names[1]] + values[names[2]] > values[names[0]]:
        raise InvalidInput("flat CPU classes overlap or exceed total")
    return {"source": source, "coverage_ms": coverage, **values}


def summarize(data):
    number(data, "version", 1, 1)
    run = cohort(data)
    counts = obj(data.get("requests"))
    issued = number(counts, "issued", maximum=run["scheduled"])
    resolved = number(counts, "resolved", maximum=issued)
    failed = number(counts, "failed", maximum=resolved)
    return run, {
        "window_ms": run["window_ms"],
        "requests": {"scheduled": run["scheduled"], "issued": issued, "resolved": resolved, "failed": failed},
        "wallet": wallet_samples(data, run["wallet_sample_permille"]),
        "relay": billing_samples(data, run["relay_sample_permille"]),
        "pools": pools(data),
        "pg_wait_samples": observations(data),
        "cpu": cpu(data, run["window_ms"]),
    }


def compare(baseline, candidate):
    old_cohort, old = summarize(baseline)
    new_cohort, new = summarize(candidate)
    cautions = [k for k in old_cohort if old_cohort[k] != new_cohort[k]]
    if any(w["requests"]["scheduled"] != w["requests"]["resolved"] for w in (old, new)):
        cautions.append("unissued_or_unresolved_requests")
    if any(w["pools"] is None for w in (old, new)):
        cautions.append("pool_counters_missing")
    elif set(old["pools"]) != set(new["pools"]):
        cautions.append("pool_sets_differ")
    if any(w["pg_wait_samples"] is None or w["pg_wait_samples"]["successful"] == 0 for w in (old, new)):
        cautions.append("pg_observation_missing")
    if any(w["cpu"] is None for w in (old, new)):
        cautions.append("cpu_missing")
    elif any(old["cpu"][k] != new["cpu"][k] for k in ("source", "coverage_ms")):
        cautions.append("cpu_sources_or_windows_differ")
    return {
        "version": 1,
        "cohort_matches": old_cohort == new_cohort,
        "comparison_cautions": cautions,
        "baseline": old,
        "candidate": new,
    }


def unique_fields(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise InvalidInput("duplicate JSON field")
        result[key] = value
    return result


def load(path):
    with path.open("rb") as handle:
        raw = handle.read(MAX_BYTES + 1)
    if len(raw) > MAX_BYTES:
        raise InvalidInput("snapshot file too large")
    return obj(json.loads(raw, object_pairs_hook=unique_fields))


def main(argv=None):
    parser = StrictParser(prog="wallet_cpu_report.py", description=__doc__, allow_abbrev=False)
    parser.add_argument("baseline", type=Path)
    parser.add_argument("candidate", type=Path)
    try:
        args = parser.parse_args(argv)
        report = compare(load(args.baseline), load(args.candidate))
    except InvalidInput as e:
        print(f"invalid sanitized evidence: {e}", file=sys.stderr)
        return 2
    except (OSError, ValueError, RecursionError):
        # I/O and parser errors may contain a private path or input strings.
        print("unable to read valid sanitized evidence", file=sys.stderr)
        return 2
    print(json.dumps(report, sort_keys=True, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
