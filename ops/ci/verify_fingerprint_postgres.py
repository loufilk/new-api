#!/usr/bin/env python3
"""Run ordinary Go tests and production-version PostgreSQL contracts separately.

Like verify_repair_services.sh, every native report has a required-test gate.
Each invocation owns newly created databases; no existing database is dropped,
emptied, or reused to bypass a fixture's existing-table refusal.
"""
from __future__ import annotations

import argparse
from dataclasses import asdict, dataclass
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import quote

ROOT = Path(__file__).resolve().parents[2]
PHASES = ("unit", "postgres", "fingerprint", "token-migration", "task-financial", "router-task", "redis")
FINGERPRINT = {
    "TestModelTestTargetAuthorizationDatabaseMatrix",
    "TestGroupRenameFingerprintTargetsDatabaseMatrix",
    "TestModelTestTargetCatalogueDatabaseMatrix",
    "TestModelTestTargetRevocationDatabaseMatrix",
    "TestModelFingerprintUnavailableTargetSettingsDatabaseMatrix",
}
# These tests predate the *DatabaseMatrix naming convention. Their first child
# is a database dialect; business-case tests must not receive this selector.
POSTGRES_CHILD = {
    "TestChannelAffinityRetryPriorities",
    "TestDeleteRedemptionBatch",
    "TestIndependentAuditLogStores",
    "TestRouteTaskPluginEndpointUsesSelectedChannel",
    "TestInferencePresetSettingsAndDatabaseRoundTrip",
    "TestMigrationSchemaStability",
    "TestOfficialSubscriptionRefundDatabaseBehavior",
    "TestMoneyColumnsAre64BitOnConfiguredDatabases",
    "TestNonAgentPromotionRetirementMatrix",
    "TestSupportTicketSchemaConfiguredDatabases",
    "TestUserSessionPreviousRefreshHashMigrationConfiguredDatabases",
    "TestPerformanceAggregationAndFlush",
    "TestRuntimeFinalFlushRetainsFailedSamplesAndPersistsCurrentBucket",
    "TestTaskPollingDefersStaleChannelCache",
    "TestBillingRecoveryNativeSessionLeaseHandoff",
}
# These initialize the write-once production registry or replace released
# schemas. Give each its own OS process and disposable PostgreSQL database.
POSTGRES_STANDALONE = {
    "TestDatabaseCompatibilityReal",
    "TestScopedAccessTokenDatabaseCompatibilityReal",
    "TestPaymentOrderBindingDatabaseCompatibilityReal",
    "TestAgentSimpleSchemaFreshUpgradeAndRepeatedMigration",
    "TestAgentSimpleWalletSchemaUpgradePreservesBalancesAndLedgerUniqueness",
    "TestGroupRenamePostgresRedis",
}
SPECIAL = {
    "TestTokenMigrationFromChar48ToVarchar128Postgres": "token-migration",
    "TestTaskSubmissionRateLimitDoesNotBlockTaskReads": "router-task",
}
# These are explicit alternative-engine schema checks, not ordinary tests
# which happen to use an in-memory fixture. Their PostgreSQL counterparts run
# below. No MySQL service or SQLite compatibility profile is started here.
OTHER_DATABASE_COMPATIBILITY = {
    "TestTokenMigrationFromChar48ToVarchar128",
    "TestTokenMigrationFromChar48ToVarchar128MySQL",
    "TestMigratePrefillGroupUniquenessSQLite",
    "TestMigratePrefillGroupUniquenessMySQL",
    "TestMigrateTokenKeyUniquenessSQLite",
    "TestMigrateTokenKeyUniquenessMySQL",
    "TestSupportTicketSchemaSQLite",
    "TestUserSessionPreviousRefreshHashMigrationSQLite",
    "TestAutoMigrateDeclaresMoneyColumnsAs64Bit",
    "TestGroupNamespaceHooksAllowFocusedSQLiteFixtureWithoutOptions",
    "TestSanitizeDBErrorSQLiteDriver",
    "TestBillingRecoveryFileRestart",
    "TestPaymentOrderBindingSQLiteFreshAndRepeatedMigration",
    "TestPaymentOrderBindingSQLiteUpgradePreservesLegacyRows",
}
EMPTY_DATABASE_ENVS = {
    "TestBillingFinalizationDatabaseMatrix": "TEST_FINALIZATION_MODEL_POSTGRES_DSN",
    "TestBillingFinalizationSessionDatabaseMatrix": "TEST_FINALIZATION_SERVICE_POSTGRES_DSN",
    "TestRealtimeFinancialConsistencyDatabaseMatrix": "TEST_FINALIZATION_REALTIME_POSTGRES_DSN",
    "TestBillingRecoveryNativeSessionLeaseHandoff": "TEST_BILLING_RECOVERY_POSTGRES_DSN",
    "TestModelBonusEligibleAuditDatabaseMatrix": "TEST_BONUS_AUDIT_POSTGRES_DSN",
}
# A slash embedded in t.Run's name does not create a parent RUN/PASS event.
MATRIX_REQUIRED_NODES = {
    "TestIndependentAuditLogStores": ["postgres/upgrade=false", "postgres/upgrade=true"],
    "TestAPITokenAuditDatabaseMatrix": ["postgres/separate_log=false", "postgres/separate_log=true"],
}
# These opt-in contracts issue server-wide SCRIPT FLUSH / CLIENT PAUSE. Run
# them serially on the owned Redis service, outside the ordinary unit phase.
REDIS_REQUIRED_NODES = {
    "TestModelRateLimitRealRedis": ["reservation_replay_and_expiry", "script_flush_resumes_request_pipeline"],
    "TestBillingSettlementDeadlineNativeRedisFence": [],
}
FUNCTION = re.compile(r"^func (\w+)\(", re.MULTILINE)
CALL = re.compile(r"(?<![\w.])(\w+)\(")
PG_ENV = re.compile(r'"((?:TEST|AUDIT)_[A-Z_]*POSTGRES[A-Z_]*DSN)"')


@dataclass
class Package:
    path: str
    tests: list[str]
    functions: dict[str, set[str]]
    function_envs: dict[str, set[str]]
    postgres_envs: set[str]
    native_tests: set[str]


@dataclass
class Suite:
    phase: str
    package: str
    label: str
    tests: list[str]
    pattern: str = ""
    skip: str = ""
    required: list[str] | None = None


def inventory() -> list[Package]:
    """Use Go's build-selected files, without executing any package TestMain."""
    output = subprocess.check_output(["go", "list", "-json", "./..."], cwd=ROOT, text=True)
    decoder = json.JSONDecoder()
    packages = []
    while output.strip():
        output = output.lstrip()
        item, end = decoder.raw_decode(output)
        output = output[end:]
        directory = Path(item["Dir"])
        functions: dict[str, set[str]] = {}
        function_envs: dict[str, set[str]] = {}
        envs: set[str] = set()
        native: set[str] = set()
        for filename in item.get("TestGoFiles", []) + item.get("XTestGoFiles", []):
            source = (directory / filename).read_text()
            envs.update(PG_ENV.findall(source))
            declarations = list(FUNCTION.finditer(source))
            for index, declaration in enumerate(declarations):
                name = declaration.group(1)
                finish = declarations[index + 1].start() if index + 1 < len(declarations) else len(source)
                body = source[declaration.end():finish]
                functions[name] = set(CALL.findall(body))
                function_envs[name] = set(PG_ENV.findall(body))
                if name.startswith("Test") and ("postgres" in filename or "postgres" in name.lower()):
                    native.add(name)
        tests = sorted(name for name in functions if re.match(r"Test[^a-z]", name) and name != "TestMain")
        path = directory.relative_to(ROOT).as_posix()
        packages.append(Package("./" + path if path != "." else ".", tests, functions, function_envs, envs, native))
    return sorted(packages, key=lambda package: package.path)


def uses_task_database(name: str, functions: dict[str, set[str]], seen: set[str] | None = None) -> bool:
    # All callers of the fixed-name task ledger fixture move together, including
    # callers through another test helper. New task tests cannot accidentally
    # return to a shared database merely because their names change.
    if name == "openTaskDialectDatabase":
        return True
    seen = set() if seen is None else seen
    if name in seen:
        return False
    seen.add(name)
    return any(uses_task_database(callee, functions, seen) for callee in functions.get(name, set()))


def referenced_postgres_envs(names: list[str], package: Package) -> set[str]:
    pending = list(names) + ["TestMain"]
    seen: set[str] = set()
    keys: set[str] = set()
    while pending:
        name = pending.pop()
        if name in seen:
            continue
        seen.add(name)
        keys.update(package.function_envs.get(name, set()))
        pending.extend(package.functions.get(name, set()) - seen)
    return keys


def build_plan(packages: list[Package]) -> list[Suite]:
    suites = []
    discovered: set[str] = set()
    for package in packages:
        excluded = []
        for name in package.tests:
            discovered.add(name)
            if name in REDIS_REQUIRED_NODES:
                suites.append(Suite("redis", package.path, name, [name], pattern="^" + name + "$",
                                    required=[name, *(name + "/" + child for child in REDIS_REQUIRED_NODES[name])]))
                # Keep the ordinary selector intact. Its opt-in skip remains
                # visible even though this separate phase executes the contract.
            phase = SPECIAL.get(name)
            if name in OTHER_DATABASE_COMPATIBILITY:
                excluded.append(name)
                continue
            matrix = name.endswith("DatabaseMatrix") or name in POSTGRES_CHILD
            if name in FINGERPRINT:
                phase = "fingerprint"
            elif matrix or name in POSTGRES_STANDALONE:
                phase = "postgres"
            elif package.path == "./controller" and uses_task_database(name, package.functions):
                phase = "task-financial"
            if not phase:
                continue
            excluded.append(name)
            pattern = "^" + name + "$"
            required = [name]
            if matrix:
                pattern += "/^postgres$"
                required.extend(name + "/" + child for child in MATRIX_REQUIRED_NODES.get(name, ["postgres"]))
            suites.append(Suite(phase, package.path, name, [name], pattern=pattern, required=required))
        kept = sorted(set(package.tests) - set(excluded))
        skip = "^(" + "|".join(excluded) + ")$" if excluded else ""
        # Build packages with no tests too, retaining the whole root-module gate.
        required = sorted((set(kept) & package.native_tests) - {"TestGroupConfigSecondProcessHelper"})
        label = "root" if package.path == "." else package.path.removeprefix("./").replace("/", "-")
        suites.append(Suite("unit", package.path, label,
                            kept, skip=skip, required=required))
    mandatory = (FINGERPRINT | set(SPECIAL) | set(EMPTY_DATABASE_ENVS) | POSTGRES_CHILD
                 | POSTGRES_STANDALONE | set(MATRIX_REQUIRED_NODES) | set(REDIS_REQUIRED_NODES))
    missing = mandatory - discovered
    if missing:
        raise ValueError("configured required tests disappeared: " + ", ".join(sorted(missing)))
    return suites


def clean_environment(source: dict[str, str]) -> dict[str, str]:
    env = dict(source)
    # Every database used below must come from this owned container. Never
    # inherit an operator DSN, alternate-engine opt-in, or child-process phase.
    for key in list(env):
        if ((key.startswith(("TEST_", "AUDIT_")) and ("DSN" in key or "MYSQL" in key))
                or key.startswith(("NEW_API_TEST_RECOVERY_", "PAYMENT_STALE_"))):
            env.pop(key)
    for key in ("SQL_DSN", "LOG_SQL_DSN", "REDIS_CONN_STRING", "TEST_DATABASE_COMPAT", "TEST_DATABASE_DIALECT",
                "TEST_RELEASE_UPGRADE_PHASE", "TEST_RELEASE_SQLITE_PATH", "TEST_A07_SCHEMA_COMPAT",
                "TEST_RUNTIME_REDIS", "TEST_SETTLEMENT_DEADLINE_REDIS_ADDR", "TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE"):
        env[key] = ""
    env.update(NO_AGENT_FIXTURE_OWNED="1", TEST_SECURITY_DIALECT="postgres", TEST_TASK_DB_DIALECT="postgres",
               TEST_FINGERPRINT_DIALECT="postgres", GOWORK="off", GOTOOLCHAIN="local")
    # Redis is still the independently pinned disposable service in the workflow.
    for key in ("TEST_REDIS_DSN", "TEST_AUTH_REDIS_DSN"):
        if source.get(key):
            env[key] = source[key]
    return env


class Runner:
    def __init__(self, phase: str, out: Path, packages: list[Package]):
        self.phase = phase
        self.out = out
        self.packages = {package.path: package for package in packages}
        self.env = clean_environment(dict(os.environ))
        self.container = os.environ["PG_ID"]
        self.port = int(os.environ.get("PG_PORT", "5432"))
        self.password = os.environ["PG_TEST_PASSWORD"]
        if os.environ.get("NO_AGENT_FIXTURE_OWNED") != "1" or not re.fullmatch(r"[a-f0-9]{12,64}", self.container):
            raise ValueError("an explicitly owned PostgreSQL CI service container is required")
        if not 1 <= self.port <= 65535:
            raise ValueError("invalid PostgreSQL fixture port")
        version = subprocess.check_output(["docker", "exec", self.container, "psql", "-U", "postgres", "-d", "postgres",
                                           "-Atc", "SHOW server_version_num;"], text=True).strip()
        if version != "180006":
            raise ValueError("this verification requires production PostgreSQL 18.6, got " + version)
        self.redis_port = None
        redis_evidence = None
        if phase == "redis":
            redis_container = os.environ["REDIS_ID"]
            redis_port = int(os.environ["REDIS_PORT"])
            if not re.fullmatch(r"[a-f0-9]{12,64}", redis_container) or not 1 <= redis_port <= 65535:
                raise ValueError("an explicitly owned Redis CI service container and valid port are required")
            bindings = subprocess.check_output(["docker", "port", redis_container, "6379/tcp"], text=True).splitlines()
            if bindings != [f"127.0.0.1:{redis_port}"]:
                raise ValueError("owned Redis must publish only the specified IPv4 loopback port")
            info = subprocess.check_output(["docker", "exec", redis_container, "redis-cli", "--raw", "INFO", "server"], text=True)
            fields = dict(line.split(":", 1) for line in info.splitlines() if ":" in line)
            if fields.get("redis_version") != "8.8.0" or fields.get("tcp_port") != "6379":
                raise ValueError("this verification requires owned Redis 8.8.0 listening on container port 6379")
            self.redis_port = redis_port
            redis_evidence = {"container": redis_container, "version": fields["redis_version"], "bindings": bindings}
        self.out.mkdir(parents=True, exist_ok=True)
        if redis_evidence:
            (self.out / "redis-runtime.json").write_text(json.dumps(redis_evidence, indent=2) + "\n")

    def database(self, name: str) -> str:
        if not re.fullmatch(r"[a-z][a-z0-9_]{0,62}", name):
            raise ValueError("invalid owned fixture database name")
        # CREATE must fail on collision. No IF NOT EXISTS, DROP, or TRUNCATE.
        subprocess.run(["docker", "exec", self.container, "createdb", "-U", "postgres", "--template=template0", name], check=True)
        return f"postgres://postgres:{quote(self.password, safe='')}@127.0.0.1:{self.port}/{name}?sslmode=disable"

    def suite_environment(self, suite: Suite, index: int) -> tuple[dict[str, str], list[str]]:
        env = dict(self.env)
        prefix = f"fp_{self.phase.replace('-', '_')}_{index:03d}"
        package = self.packages[suite.package]
        keys = set(package.postgres_envs) if suite.phase == "unit" else referenced_postgres_envs(suite.tests, package)
        if keys or suite.phase != "unit":
            keys.add("TEST_POSTGRES_DSN")
        # Main TestMain fixtures must never consume an empty migration/matrix DB.
        if suite.package == "./model":
            keys.add("TEST_MODEL_POSTGRES_DSN")
        if suite.package == "./service":
            keys.add("TEST_SERVICE_POSTGRES_DSN")
        dedicated = EMPTY_DATABASE_ENVS.get(suite.label)
        keys.difference_update(EMPTY_DATABASE_ENVS.values())
        if dedicated:
            keys.add(dedicated)
        keys.discard("TEST_RETIREMENT_POSTGRES_DSN")
        databases = []
        for number, key in enumerate(sorted(keys)):
            name = f"{prefix}_{number:02d}"
            env[key] = self.database(name)
            databases.append(name)
        if suite.label == "TestNonAgentPromotionRetirementMatrix":
            # This fixture additionally verifies the exact owned DB name.
            env["TEST_RETIREMENT_POSTGRES_DSN"] = self.database("no_agent_retirement")
            databases.append("no_agent_retirement")
        if suite.label in {"TestDatabaseCompatibilityReal", "TestScopedAccessTokenDatabaseCompatibilityReal",
                           "TestPaymentOrderBindingDatabaseCompatibilityReal"}:
            env.update(TEST_DATABASE_COMPAT="1", TEST_DATABASE_DIALECT="postgres",
                       TEST_DATABASE_DSN=env["TEST_POSTGRES_DSN"])
            env["TEST_LOG_DATABASE_DSN"] = self.database(prefix + "_log")
            databases.append(prefix + "_log")
        if suite.label in {"TestAgentSimpleSchemaFreshUpgradeAndRepeatedMigration",
                           "TestAgentSimpleWalletSchemaUpgradePreservesBalancesAndLedgerUniqueness"}:
            env["TEST_A07_SCHEMA_COMPAT"] = "1"
        if suite.phase == "redis":
            if not self.redis_port:
                raise ValueError("Redis opt-ins require the verified owned service")
            if suite.label == "TestModelRateLimitRealRedis":
                env.update(TEST_RUNTIME_REDIS="1", TEST_REDIS_DSN=f"redis://127.0.0.1:{self.redis_port}/3")
            elif suite.label == "TestBillingSettlementDeadlineNativeRedisFence":
                env.update(TEST_SETTLEMENT_DEADLINE_REDIS_ADDR=f"127.0.0.1:{self.redis_port}",
                           TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE="1")
            else:
                raise ValueError("unknown required Redis contract")
        return env, databases

    def run(self, suite: Suite, index: int) -> bool:
        env, databases = self.suite_environment(suite, index)
        label = f"{index:03d}-{suite.label}"
        report = self.out / (label + ".jsonl")
        errors = self.out / (label + ".stderr.log")
        command = ["go", "test", "-p", "1", "-count=1", "-timeout=20m", "-json"]
        if suite.phase != "unit":
            command.append("-race")
        command.append(suite.package)
        if suite.pattern:
            command += ["-run", suite.pattern]
        if suite.skip:
            command += ["-skip", suite.skip]
        (self.out / (label + ".plan.json")).write_text(json.dumps(
            dict(asdict(suite), command=command, databases=databases), indent=2) + "\n")
        print(f"[{self.phase}] {suite.package}: {suite.label}", flush=True)
        with report.open("w") as stdout, errors.open("w") as stderr:
            completed = subprocess.run(command, cwd=ROOT, env=env, stdout=stdout, stderr=stderr)
        success = completed.returncode == 0
        if not success:
            print(errors.read_text(), file=sys.stderr)
            print("\n".join(report.read_text().splitlines()[-100:]), file=sys.stderr)
        if suite.required:
            checker = [sys.executable, "ops/ci/check_go_test_report.py", "--require-run", "--reject-skipped-descendants",
                       str(report), *suite.required]
            success = subprocess.run(checker, cwd=ROOT).returncode == 0 and success
        outcomes = {"pass": 0, "fail": 0, "skip": 0}
        skipped = []
        for line in report.read_text().splitlines():
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue  # The Go exit status / required-report checker still fail malformed runs.
            action, test = event.get("Action"), event.get("Test", "")
            if action in outcomes and test and "/" not in test:
                outcomes[action] += 1
            if action == "skip" and test:
                skipped.append(test)
        (self.out / (label + ".summary.json")).write_text(json.dumps(
            {"success": success, "top_level_tests": outcomes, "skipped_tests": skipped}, indent=2) + "\n")
        if skipped:
            print(f"[{self.phase}] {len(skipped)} skipped test nodes recorded in {label}.summary.json", flush=True)
        return success


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=("plan", *PHASES))
    parser.add_argument("--out", type=Path, default=Path("verification"))
    args = parser.parse_args()
    packages = inventory()
    suites = build_plan(packages)
    args.out.mkdir(parents=True, exist_ok=True)
    manifest = {"postgresql_version": "18.6", "suites": [asdict(suite) for suite in suites],
                "excluded_alternative_database_compatibility": sorted(OTHER_DATABASE_COMPATIBILITY)}
    (args.out / "postgres-plan.json").write_text(json.dumps(manifest, indent=2) + "\n")
    if args.phase == "plan":
        print(json.dumps({phase: sum(s.phase == phase for s in suites) for phase in PHASES}, sort_keys=True))
        return 0
    selected = [suite for suite in suites if suite.phase == args.phase]
    if not selected:
        raise ValueError("requested PostgreSQL verification phase has no tests")
    runner = Runner(args.phase, args.out / args.phase, packages)
    results = [runner.run(suite, index) for index, suite in enumerate(selected, 1)]
    return 0 if all(results) else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (KeyError, OSError, ValueError, subprocess.CalledProcessError) as error:
        print("PostgreSQL verification rejected: " + str(error), file=sys.stderr)
        raise SystemExit(1)
