"""Reject missing, skipped, failed, or incomplete required Go integration tests."""
from __future__ import annotations

import argparse
import json
from pathlib import Path


def validate_report(path: Path, required: list[str], reject_skipped_descendants: bool = False,
                    require_run: bool = False) -> dict[str, str]:
    outcomes: dict[str, str] = {}
    packages: dict[str, str] = {}
    failures: list[str] = []
    skipped_tests: set[str] = set()
    runs: dict[str, int] = {}
    passes: dict[str, int] = {}
    with path.open(encoding="utf-8") as stream:
        for number, line in enumerate(stream, 1):
            if not line.strip():
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError(f"{path.name}:{number}: invalid Go test JSON") from exc
            if not isinstance(event, dict):
                raise ValueError(f"{path.name}:{number}: expected a Go test event")
            action = event.get("Action")
            if action in {"build-output", "build-fail"}:
                import_path = event.get("ImportPath")
                if not isinstance(import_path, str) or not import_path:
                    raise ValueError(f"{path.name}:{number}: missing build import path")
                if action == "build-fail":
                    failures.append(import_path)
                continue
            package = event.get("Package")
            test = event.get("Test")
            if not isinstance(package, str) or not package:
                raise ValueError(f"{path.name}:{number}: missing package")
            if test is not None and (not isinstance(test, str) or not test):
                raise ValueError(f"{path.name}:{number}: invalid test name")
            if action == "fail" or action == "build-fail":
                failures.append(test or package)
            if test:
                if action == "run":
                    runs[test] = runs.get(test, 0) + 1
                if action == "pass":
                    passes[test] = passes.get(test, 0) + 1
                if action in {"pass", "skip", "fail"}:
                    outcomes[test] = action
                if action == "skip":
                    skipped_tests.add(test)
            elif action in {"start", "pass", "skip", "fail", "build-fail"}:
                packages[package] = action
    if failures:
        raise ValueError("failed test or package: " + ", ".join(failures))
    if not packages or any(state != "pass" for state in packages.values()):
        raise ValueError("Go test package did not finish successfully")
    missing = [name for name in required if outcomes.get(name) != "pass"]
    if missing:
        raise ValueError("required test did not pass: " + ", ".join(missing))
    if require_run:
        invalid = [name for name in required if runs.get(name, 0) != 1 or passes.get(name, 0) != 1]
        if invalid:
            raise ValueError("required test must have exactly one RUN and PASS: " + ", ".join(invalid))
    if reject_skipped_descendants:
        skipped = sorted(
            test for test in skipped_tests
            if any(test == name or test.startswith(name + "/") for name in required)
        )
        if skipped:
            raise ValueError("required test descendant was skipped: " + ", ".join(skipped))
    if not required:
        raise ValueError("at least one required test must be specified")
    return {name: outcomes[name] for name in required}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reject-skipped-descendants", action="store_true")
    parser.add_argument("--require-run", action="store_true",
                        help="require exactly one RUN and PASS event for every required node")
    parser.add_argument("report", type=Path)
    parser.add_argument("required", nargs="+")
    args = parser.parse_args()
    try:
        outcomes = validate_report(args.report, args.required, args.reject_skipped_descendants, args.require_run)
    except (OSError, ValueError) as exc:
        parser.exit(1, f"integration evidence rejected: {exc}\n")
    print(json.dumps(outcomes, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
