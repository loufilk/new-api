#!/usr/bin/env python3
"""Read-only CORE im.21 and im.22 maintenance-window gates. No pull, exec, start or deploy."""
import argparse
from datetime import datetime, timedelta, timezone
import http.client
import json
from pathlib import Path
import re
import socket
import sys
from urllib.parse import parse_qsl, urlsplit


BASE = "v1.0.0-rc.51.chuanapi.im.21"
APPROVED_VERSIONS = (BASE, "v1.0.0-rc.51.chuanapi.im.22")
RAW = "0dff956284eda773db217445724d1d85e8d0b840"
SHA = re.compile(r"[0-9a-f]{40}")
HEX64 = re.compile(r"[0-9a-f]{64}")
PART = r"[a-z0-9]+(?:(?:[.]|_{1,2}|-+)[a-z0-9]+)*"
PIN = re.compile(rf"(?:{PART}(?::[0-9]{{1,5}})?/)+(?:{PART})@sha256:[0-9a-f]{{64}}")
NAME = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}")
HEALTH = ["CMD-SHELL", """wget -q -O - http://localhost:3000/api/readyz | grep -Eq '"success"[[:space:]]*:[[:space:]]*true' || exit 1"""]
LIMIT = 8 * 1024 * 1024


class Blocked(Exception):
    """Only non-secret, fixed error messages may reach the terminal."""


def require(condition, message):
    if not condition:
        raise Blocked(message)


def strict_json(data):
    def unique(pairs):
        result = {}
        for k, v in pairs:
            require(k not in result, "duplicate JSON key")
            result[k] = v
        return result
    return json.loads(data, object_pairs_hook=unique)


def stamp(value):
    # Docker emits RFC3339Nano timestamps; Engine hosts may use local offsets.
    require(isinstance(value, str) and re.fullmatch(
        r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})", value),
        "invalid qualified timestamp")
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("localhost", timeout=10)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


def engine_get(path, resource):
    # No shell, docker contexts, http redirects or user-selected commands.
    conn = UnixConnection(path)
    try:
        conn.request("GET", resource)
        res = conn.getresponse()
        require(res.status == 200, "Docker Engine GET failed")
        data = res.read(LIMIT + 1)
        require(len(data) <= LIMIT, "Docker response exceeded size limit")
        return strict_json(data)
    finally:
        conn.close()


def store_target(value, is_redis=False):
    # Support explicit TCP endpoints only. Query overrides (host, dbname,
    # options/search_path, Redis db, etc.) must not circumvent comparison.
    require(value and not re.search(r"[\s\x00-\x1f\x7f]", value), "invalid store URL")
    u = urlsplit(value)
    allowed = ("redis", "rediss") if is_redis else ("postgres", "postgresql")
    require(u.scheme in allowed and u.hostname and not u.fragment and
            re.fullmatch(r"[a-z0-9][a-z0-9.-]*", u.hostname), "unsupported store endpoint")
    params = parse_qsl(u.query, keep_blank_values=True, strict_parsing=True)
    require(all(not is_redis and k == "sslmode" for k, _ in params) and len(params) <= 1,
            "store URL contains an unsupported target/connection override")
    path = "/0" if is_redis and u.path in ("", "/") else u.path
    require(re.fullmatch(r"/[0-9]+" if is_redis else r"/[a-zA-Z0-9_.-]+", path),
            "an explicit database name/index is required")
    require(u.port != 0, "invalid store port")
    return f"{u.scheme if is_redis else 'postgresql'}://{u.hostname}:{u.port or (6379 if is_redis else 5432)}{path}"


def validate_plan(plan, now):
    required = {"revision", "image", "opens", "expires", "personal_pricing", "hosts", "release_id", "stores"}
    require(required <= set(plan) <= required | {"version"}, "invalid release plan fields")
    require(plan.get("version", BASE) in APPROVED_VERSIONS, "unapproved release plan version")
    require(SHA.fullmatch(plan["revision"]) and plan["revision"] not in (RAW, "0" * 40)
            and PIN.fullmatch(plan["image"]), "unapproved target revision or image pin")
    require(re.fullmatch(r"[0-9a-f]{32}", plan["release_id"]), "expected unique window release ID")
    stores = plan["stores"]
    require(set(stores) == {"primary", "log", "redis"} and
            all(store_target(v, k == "redis") == v for k, v in stores.items()),
            "expected canonical non-secret primary, log and Redis targets")
    require(plan["opens"].endswith("Z") and plan["expires"].endswith("Z"), "plan requires UTC window")
    opens, expires = stamp(plan["opens"]), stamp(plan["expires"])
    require(opens <= now < expires and expires - opens <= timedelta(hours=24),
            "maintenance window missing, stale or longer than 24h")
    require(type(plan["personal_pricing"]) is bool, "personal_pricing must select the intended policy")
    require(type(plan["hosts"]) is list and plan["hosts"], "empty engine inventory")
    engines, sockets, migrations, masters = set(), set(), 0, 0
    for host in plan["hosts"]:
        require(set(host) == {"socket", "id", "nodes", "migrator", "infrastructure"},
                "invalid engine inventory fields")
        path, eid = host["socket"], host["id"]
        require(Path(path).is_absolute() and path not in sockets and eid not in engines and
                re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9:-]{8,127}", eid),
                "missing or duplicate engine/socket identity")
        sockets.add(path)
        engines.add(eid)
        nodes, migrator, infra = host["nodes"], host["migrator"], host["infrastructure"]
        require(type(nodes) is dict and all(NAME.fullmatch(k) and v in ("master", "slave")
                                            for k, v in nodes.items()), "invalid node inventory")
        require(migrator is None or NAME.fullmatch(migrator) and migrator not in nodes,
                "invalid migration job inventory")
        require(type(infra) is dict and all(NAME.fullmatch(k) and HEX64.fullmatch(v)
                                            for k, v in infra.items()) and
                not set(infra).intersection(nodes) and migrator not in infra,
                "infrastructure requires independent names and full container IDs")
        migrations += int(migrator is not None)
        masters += list(nodes.values()).count("master")
    require(migrations == 1 and masters >= 1, "expected one migration job and at least one serving/worker master")


def verify_candidate(c, img, plan, stage, role, now):
    cfg, hc, state = c["Config"], c["HostConfig"], c["State"]
    iid, rev = c["Image"], plan["revision"]
    labels = img["Config"]["Labels"]
    require(re.fullmatch(r"sha256:[0-9a-f]{64}", iid) and img["Id"] == iid and
            cfg["Image"] == plan["image"] and plan["image"] in img["RepoDigests"] and
            labels.get("org.opencontainers.image.revision") == rev and
            labels.get("org.opencontainers.image.version") == plan.get("version", BASE) + "+g" + rev[:12] and
            img["Os"] == "linux", "candidate image does not match approved digest and stamped full revision")
    require(c["Path"] == "/new-api" and c["Args"] in ([], ["--log-dir", "/app/logs"]) and
            cfg["Entrypoint"] == img["Config"]["Entrypoint"] == ["/new-api"],
            "unexpected executable/arguments (including early-exit commands)")
    require(not any(m["Destination"] in ("/", "/new-api") for m in c["Mounts"]),
            "executable is shadowed by a mount")
    require(state["OOMKilled"] is False and state["Dead"] is False and
            state["Restarting"] is False and state["Paused"] is False and
            not state["Error"] and c["RestartCount"] == 0, "container is unstable")
    env = {}
    for item in cfg["Env"]:
        key, sep, value = item.partition("=")
        require(sep and key not in env, "invalid/duplicate effective environment variable")
        env[key] = value
    require(not env.get("VERSION"), "runtime version override not permitted")
    require(env.get("CORE_IM21_RELEASE_ID") == plan["release_id"], "incorrect migration window identity")
    primary = store_target(env.get("SQL_DSN"))
    log = store_target(env["LOG_SQL_DSN"]) if env.get("LOG_SQL_DSN") else primary
    redis = store_target(env.get("REDIS_CONN_STRING"), True)
    require({"primary": primary, "log": log, "redis": redis} == plan["stores"],
            "primary/log/Redis targets do not match the approved release plan")
    require(env.get("CORE_IM21_MIGRATION_MODE") == ("migrate" if role == "migrator" else "serve"),
            "missing or wrong managed migration mode")
    rollout = env.get("CORE_IM21_ROLLOUT_MODE", "")
    pricing = env.get("USER_GROUP_PRICING_ENABLED", "")
    active = stage == "active" and role != "migrator"
    require(rollout == "active" if active else rollout in ("", "locked"), "unexpected rollout mode")
    require(pricing == "true" if active and plan["personal_pricing"] else pricing in ("", "false"),
            "personal pricing flag is inconsistent with the release stage")
    node = env.get("NODE_TYPE", "")
    require(node == "slave" if role == "slave" else node in ("", "master"), "unexpected master/follower role")
    started = stamp(state["StartedAt"])
    require(started >= stamp(plan["opens"]), "container predates current release window")
    if role == "migrator":
        require(state["Status"] == "exited" and state["Running"] is False and state["ExitCode"] == 0 and
                hc["RestartPolicy"]["Name"] in ("no", "") and not hc["PortBindings"] and
                not hc["PublishAllPorts"] and hc["NetworkMode"] != "host",
                "migration job did not complete once, offline, with exit status 0")
        finished = stamp(state["FinishedAt"])
        require(finished >= started, "invalid migration completion time")
        return finished, None
    require(state["Status"] == "running" and state["Running"] is True and state["Pid"] > 0 and
            cfg["Healthcheck"]["Test"] == HEALTH and state["Health"]["Status"] == "healthy",
            "serving/worker node is not running and healthy on its private readyz check")
    health = state["Health"]["Log"][-1] if state["Health"]["Log"] else None
    require(health and health["ExitCode"] == 0 and
            now - timedelta(seconds=90) <= stamp(health["End"]) <= now and stamp(health["End"]) >= started,
            "missing, failed or stale latest readyz health check")
    node_name = env.get("NODE_NAME", "")
    require(NAME.fullmatch(node_name), "serving nodes need an explicit stable NODE_NAME")
    return started, node_name


def observe(plan, stage, get, clock):
    evidence, job_finished, node_starts, node_names = [], None, [], set()
    for host in plan["hosts"]:
        path, eid = host["socket"], host["id"]
        require(get(path, "/info")["ID"] == eid, "socket does not identify the approved engine")
        registered = host["infrastructure"]
        roles = dict(host["nodes"]) if stage in ("locked", "active") else {}
        if host["migrator"] and stage != "drained":
            roles[host["migrator"]] = "migrator"
        remaining = set(registered) | set(roles)
        images = {}
        # Include stopped containers: an old restartable replica is not drained.
        for row in get(path, "/containers/json?all=1"):
            cid = row["Id"]
            require(HEX64.fullmatch(cid), "invalid engine container ID")
            c = get(path, "/containers/" + cid + "/json")
            name = c["Name"].removeprefix("/")
            require(c["Id"] == cid and name in remaining, "unlisted, duplicated or residual container")
            remaining.remove(name)
            if name in registered:
                require(registered[name] == cid and c["Path"] != "/new-api" and
                        c["Config"]["Image"] != plan["image"], "infrastructure exemption does not match")
                evidence.append((eid, name, cid, c["State"]["StartedAt"], c["State"]["Status"]))
                continue
            iid = c["Image"]
            require(isinstance(iid, str) and re.fullmatch(r"sha256:[a-f0-9]{64}", iid),
                    "invalid content image ID")
            if iid not in images:
                images[iid] = get(path, "/images/" + iid + "/json")
            now = clock()
            moment, node_name = verify_candidate(c, images[iid], plan, stage, roles[name], now)
            require(moment <= now, "Docker clock is ahead of the trusted release clock")
            # Neither a replaced root-layer binary nor a one-off --version exit is migration evidence.
            changes = get(path, "/containers/" + cid + "/changes")
            require(changes is None or isinstance(changes, list) and
                    all(v["Path"] != "/new-api" for v in changes), "executable changed in the writable layer")
            if roles[name] == "migrator":
                job_finished = moment
            else:
                require(node_name not in node_names, "duplicate NODE_NAME across serving/worker nodes")
                node_names.add(node_name)
                node_starts.append(moment)
            evidence.append((eid, name, cid, c["State"]["StartedAt"], c["State"]["Status"]))
        require(not remaining, "expected containers missing")
    require(stage == "drained" or (job_finished is not None and
            all(start >= job_finished for start in node_starts)),
            "candidate process started before the successful migration")
    return sorted(evidence)


def gate(plan, stage, get=engine_get, clock=lambda: datetime.now(timezone.utc)):
    require(stage in ("drained", "migrated", "locked", "active"),
            "no qualified in-place rollback, including raw 0dff956; maintain freeze and fix forward")
    validate_plan(plan, clock())
    first = observe(plan, stage, get, clock)
    require(first == observe(plan, stage, get, clock), "fleet changed while inspecting")
    validate_plan(plan, clock())
    return first


def release_window(plan, wait, report, get=engine_get, clock=lambda: datetime.now(timezone.utc)):
    # Never load phase success from caller-provided stdout/JSON. A restarted
    # process has no trusted phase history and must not authorize admission.
    retained = None
    for stage in ("drained", "migrated", "locked", "active"):
        wait(stage)
        snap = gate(plan, stage, get, clock)
        if stage != "drained":
            [(eid, name)] = [(h["id"], h["migrator"]) for h in plan["hosts"] if h["migrator"]]
            [job] = [item for item in snap if item[:2] == (eid, name)]
            require(retained is None or retained == job, "migration evidence replaced during window")
            retained = job
        report(stage, len(snap))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("stage", choices=("window", "drained", "migrated", "locked", "active", "rollback"))
    p.add_argument("--plan", type=Path, help="independently approved, window-specific inventory")
    args = p.parse_args()
    try:
        require(args.stage != "rollback", "no qualified in-place rollback, including raw 0dff956; fix forward")
        require(args.plan is not None, "an approved plan is required")
        with args.plan.open("rb") as f:
            data = f.read(LIMIT + 1)
        require(len(data) <= LIMIT, "inventory exceeds size limit")
        plan = strict_json(data)
        if args.stage == "window":
            def wait(stage):
                print(f"Awaiting {stage}: complete the runbook step, then press Enter.", file=sys.stderr, flush=True)
                input()  # timing signal only; input text never attests that a step passed
            def report(stage, count):
                print(f"Verified {stage}: {count} containers", file=sys.stderr, flush=True)
            release_window(plan, wait, report)
            print(json.dumps({"window": "PASS", "revision": plan["revision"], "image": plan["image"]}))
        else:
            snap = gate(plan, args.stage)
            print(json.dumps({"diagnostic_stage": args.stage, "inspection": "PASS",
                              "containers": len(snap), "admission": "NOT_AUTHORIZED"}))
        return 0
    except Blocked as exc:
        print(f"BLOCKED: {exc}", file=sys.stderr)
    except (EOFError, OSError, ValueError, TypeError, KeyError, AttributeError, http.client.HTTPException):
        # Daemon/JSON input may contain credentials. Do not print exception data.
        print("BLOCKED: cannot read valid trusted release inventory or Docker Engine state", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
