#!/usr/bin/env python3
"""Run A07 accounting, schema-upgrade and startup tests on local fixtures."""

import argparse
from datetime import datetime
import json
import os
from pathlib import Path
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--engine", choices=["all", "sqlite", "mysql", "postgres"], default="all")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    output = args.output or Path(tempfile.mkdtemp(prefix="wanchuan-a07-matrix-"))
    output.mkdir(parents=True, exist_ok=True)
    engines = ["sqlite", "mysql", "postgres"] if args.engine == "all" else [args.engine]
    fixture_dir = Path(os.environ.get("A07_DB_DIR", Path.home() / "Library/Application Support/WanchuanA07Tests"))
    fixture = {}
    if any(engine != "sqlite" for engine in engines):
        fixture = json.loads((fixture_dir / "fixture.json").read_text())
    baseline = os.environ.copy()
    # Never inherit addresses for another database or a deployment environment.
    for key in list(baseline):
        if key.startswith("TEST_") or key in ["SQL_DSN", "LOG_SQL_DSN"]:
            del baseline[key]
    baseline["GOWORK"] = "off"
    results = []
    for engine in engines:
        env = baseline.copy()
        if engine == "postgres":
            env["TEST_MODEL_POSTGRES_DSN"] = fixture[engine]
        elif engine == "mysql":
            env["TEST_MODEL_MYSQL_DSN"] = fixture[engine]
        suites = [
            ("accounting", "TestAgentSimple|TestReferralA07|TestRecordReferral|TestTransferAffQuota|TestAgentInvitation"),
            ("schema", "^TestAgentSimpleSchemaFreshUpgradeAndRepeatedMigration$"),
            ("startup", "^TestDatabaseCompatibilityReal$"),
        ]
        for name, pattern in suites:
            case_env = env.copy()
            if name == "schema":
                case_env["TEST_A07_SCHEMA_COMPAT"] = "1"
                for key in ["TEST_MODEL_POSTGRES_DSN", "TEST_MODEL_MYSQL_DSN"]:
                    if key in case_env:
                        case_env[key] = case_env[key].replace("/a07_model", "/a07_schema")
            elif name == "startup":
                case_env["TEST_DATABASE_COMPAT"] = "1"
                case_env["TEST_DATABASE_DIALECT"] = engine
                if engine != "sqlite":
                    case_env["TEST_DATABASE_DSN"] = fixture[engine].replace("/a07_model", "/a07_compat")
                    case_env["TEST_LOG_DATABASE_DSN"] = fixture[engine].replace("/a07_model", "/a07_compat_log")
            command = ["go", "test", "./model", "-run", pattern, "-count=1", "-v", "-timeout=10m"]
            logfile = output / f"{engine}-{name}.log"
            print(f"Running {engine}/{name}: {logfile}", flush=True)
            with logfile.open("w") as stream:
                result = subprocess.run(command, cwd=repo, env=case_env, stdout=stream, stderr=subprocess.STDOUT)
            results.append({"engine": engine, "suite": name, "exit_code": result.returncode, "log": str(logfile), "command": command})
            print(f"{'PASS' if result.returncode == 0 else 'FAIL'} {engine}/{name}", flush=True)
    report = {"created_at": datetime.now().astimezone().isoformat(), "results": results}
    (output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    print(f"Matrix report: {output / 'results.json'}")
    raise SystemExit(0 if all(result["exit_code"] == 0 for result in results) else 1)


if __name__ == "__main__":
    main()
