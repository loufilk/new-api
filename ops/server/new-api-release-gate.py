#!/usr/bin/env python3
"""Read-only release evidence validator. This command never deploys."""

import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import sys


UPSTREAM_COMMIT = "eb99ab1b40343c3317bb47981cccdbb2b159a5fa"
UPSTREAM_VERSIONS = {
    UPSTREAM_COMMIT: "v1.0.0-rc.33+",
    "0c76e4dae77a279e015329b7478e6f02d6b62edd": "v1.0.0-rc.34+",
}
REQUIRED_CHECKS = (
    "SOURCE", "GO_TEST", "GO_VET", "GO_RACE", "RELAYKIT_BUILD", "RELAYKIT_TEST",
    "WEB_TYPECHECK", "WEB_TEST", "WEB_BUILD", "WEB_LINT", "WEB_FORMAT", "WEB_AUDIT",
    "SQLITE_MIGRATION_TWICE", "MYSQL_MIGRATION_TWICE", "PG_RESTORE_TWICE",
    "LOG_DATABASE", "RC31_APP_ROLLBACK", "FUNDS_INVARIANTS", "PLUGIN_REGISTRY",
    "TASK_PRICING", "CONTRACTS", "UPSTREAM_REGRESSION_CHECK", "IMAGE_SIGNATURE", "SBOM",
)
MIN_OBSERVATIONS = 800
MIN_SECONDS = 72 * 60 * 60
MAX_GAP_SECONDS = 600


def strict_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate JSON key: " + key)
        value[key] = item
    return value


def reject_constant(value):
    raise ValueError("non-finite JSON value: " + value)


def decode_json(text):
    return json.loads(text, object_pairs_hook=strict_object, parse_constant=reject_constant)


def timestamp(value):
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise ValueError("timestamp must include a timezone")
    return parsed.timestamp()


def evidence_file(root, reference):
    name, expected = reference["file"], reference["sha256"]
    if not re.fullmatch(r"[a-f0-9]{64}", expected):
        raise ValueError("evidence SHA256 is missing or invalid")
    relative = Path(name)
    if relative.is_absolute():
        raise ValueError("evidence path must be relative")
    path = (root / relative).resolve()
    if not path.is_relative_to(root.resolve()) or not path.is_file():
        raise ValueError("evidence file is missing or outside the evidence directory")
    digest = hashlib.sha256()
    size = 0
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
            size += len(chunk)
    if size == 0 or digest.hexdigest() != expected:
        raise ValueError("evidence file is empty or SHA256 does not match")
    return path


def validate_release(document, root, commit, image, now):
    if not re.fullmatch(r"[a-f0-9]{40}", commit):
        raise ValueError("candidate commit must be a full Git SHA")
    if not re.fullmatch(r"[^\s@]+@sha256:[a-f0-9]{64}", image):
        raise ValueError("candidate image must be an immutable repository digest")
    if type(document["schema_version"]) is not int or document["schema_version"] != 1 or document["upstream_commit"] not in UPSTREAM_VERSIONS:
        raise ValueError("unexpected schema or upstream target")
    candidate = document["candidate"]
    if candidate["commit"] != commit or candidate["image"] != image:
        raise ValueError("candidate identity does not match the requested release")
    version = candidate["version"]
    if not version.startswith(UPSTREAM_VERSIONS[document["upstream_commit"]]):
        raise ValueError("candidate version must identify the selected upstream release")
    evidence_file(root, candidate["source_archive"])
    for name in REQUIRED_CHECKS:
        check = document["checks"].get(name, {})
        if check.get("status") != "PASS":
            raise ValueError(name + " is not PASS")
        if check.get("commit") != commit or check.get("image") != image:
            raise ValueError(name + " belongs to another candidate")
        completed = timestamp(check["completed_at"])
        if completed > now or now - completed > 30 * 24 * 60 * 60:
            raise ValueError(name + " has a future or stale completion timestamp")
        if not check.get("evidence"):
            raise ValueError(name + " has no evidence")
        for reference in check["evidence"]:
            evidence_file(root, reference)

    canary = document["canary"]
    started = timestamp(canary["started_at"])
    path = evidence_file(root, canary["observations"])
    if path.stat().st_size > 16 * 1024 * 1024:
        raise ValueError("canary evidence exceeds 16 MiB")
    rows = [decode_json(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    if len(rows) < MIN_OBSERVATIONS:
        raise ValueError("canary needs at least 800 observations")
    first, last, snapshot = None, None, None
    for row in rows:
        observed = timestamp(row["at"])
        if observed < started or observed > now:
            raise ValueError("canary timestamp is outside the observation window")
        if last is not None and (observed <= last or observed - last > MAX_GAP_SECONDS):
            raise ValueError("canary observations are unordered, duplicated, or have a gap")
        if any(row.get(key) != value for key, value in (("commit", commit), ("image", image), ("version", version))):
            raise ValueError("canary candidate changed")
        if any(row.get(key) is not True for key in ("running", "liveness", "ready")) or row.get("oom") is not False:
            raise ValueError("canary health failed")
        for key in ("restarts", "critical_count", "funds_mismatch_count"):
            if type(row.get(key)) is not int or row[key] != 0:
                raise ValueError("canary " + key + " is not zero")
        funds = row["funds_snapshot_sha256"]
        if not re.fullmatch(r"[a-f0-9]{64}", funds) or snapshot is not None and funds != snapshot:
            raise ValueError("idle canary funds snapshot changed or is invalid")
        snapshot = funds
        if first is None:
            first = observed
        last = observed
    if last - first < MIN_SECONDS or now - last > MAX_GAP_SECONDS:
        raise ValueError("canary must span 72 hours with fresh observations")
    return {"status": "PASS", "commit": commit, "image": image, "observations": len(rows)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--image", required=True)
    args = parser.parse_args()
    try:
        document = decode_json(args.manifest.read_text(encoding="utf-8"))
        result = validate_release(document, args.manifest.parent, args.commit, args.image, datetime.now(timezone.utc).timestamp())
    except (OSError, ValueError, TypeError, KeyError, AttributeError) as exc:
        print(json.dumps({"status": "FAIL", "reason": str(exc)}, ensure_ascii=True))
        return 1
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
