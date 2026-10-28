#!/usr/bin/env python3
"""Synthetic, source-built application upgrade/restart/rollback on disposable PG.

The baseline is the exact archived production source, not the deployed image
or representative production data. The baseline fresh-install failure is retained;
upgrade/rollback uses an explicitly synthetic historical initialized installation. No external DSN, volume, image override, or credentials are
accepted. Only a local Docker daemon is used; runtime has an internal network,
no published ports, and no upstream requests. Both apps use the candidate's
Dockerfile.api (the baseline recipe omits tokenkit/go.mod before go mod download).
"""

import argparse
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import re
import subprocess
import time
import urllib.error
import urllib.request
import uuid


BASELINE_SHA = "093c7197d5eac50787971fd5ff7553a414142fc7"
POSTGRES_IMAGE = "postgres:18.6-alpine@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2"
MODEL = "synthetic-upgrade-model"
TOKEN = "synthetic" + "x" * 39  # No '-' channel-selector suffix in an API key.
PASSWORD = "Synthetic-Only-Upgrade-2026!"
ROLE_ACCOUNTS = (("ci-upgrade", 100), ("ci-admin", 10), ("ci-user", 1))
MONITOR_CONFIG = {"enabled": False, "title": "Synthetic monitor", "description": "Synthetic historical settings",
                  "groups": {"route-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": "Private fixture"},
                  "hidden_groups": {"route-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": True}}

MAIN_SEED = f"""
BEGIN;
INSERT INTO users (username,password,aff_code,role,status,\"group\",quota,quota_paid,quota_bonus,quota_reward,used_quota,auth_version)
VALUES ('synthetic-owner','not-a-login-password-hash','synthetic-upgrade-aff',1,1,'default',5000,4000,1000,0,4294967400,7);
INSERT INTO tokens (user_id,\"key\",name,status,expired_time,remain_quota,used_quota,\"group\",model_limits_enabled,model_limits,unlimited_quota)
SELECT id,'{TOKEN}','synthetic-token',1,-1,700,300,'default',true,'{MODEL}',false FROM users WHERE username='synthetic-owner';
INSERT INTO channels (name,type,\"key\",status,base_url,models,\"group\",channel_info)
VALUES ('synthetic-channel',1,'not-an-upstream-credential',1,'http://127.0.0.1:9','{MODEL}','default','{{}}');
INSERT INTO abilities (\"group\",model,channel_id,enabled)
SELECT 'default','{MODEL}',id,true FROM channels WHERE name='synthetic-channel';
INSERT INTO models (model_name,status,sync_official,context_length,capabilities,bonus_eligible)
VALUES ('{MODEL}',1,0,128000,'["vision"]',false);
INSERT INTO tasks (task_id,platform,user_id,\"group\",quota,status,progress,submit_time,finish_time,properties,private_data,data)
SELECT 'synthetic-completed-task','synthetic',id,'default',123,'SUCCESS','100%',1788600000,1788600001,'{{}}','{{}}','{{"synthetic":true}}'
FROM users WHERE username='synthetic-owner';
-- Completed historical media stays dormant; no provider polling is exercised.
INSERT INTO midjourneys (user_id,mj_id,action,status,progress,quota,token_id,channel_id,billing_channel_id,prompt,submit_time,finish_time)
SELECT u.id,'synthetic-midjourney','IMAGINE','SUCCESS','100%',300,t.id,19,23,'synthetic historical Midjourney',1788600000,1788600001
FROM users u JOIN tokens t ON t.user_id=u.id WHERE u.username='synthetic-owner' AND t.name='synthetic-token';
INSERT INTO quota_data (user_id,username,model_name,created_at,use_group,token_used,count,quota)
SELECT id,username,'{MODEL}',1788600000,'default',321,2,123 FROM users WHERE username='synthetic-owner';
INSERT INTO options (\"key\",value) VALUES
('ModelRatio','{{"{MODEL}":2.5}}'),
('ChuanapiMonitorConfig','{json.dumps(MONITOR_CONFIG)}'),
('monitor_setting.auto_test_channel_enabled','false'),
('PublicModelOwnedBy','synthetic-owner-label'),
('TaskEnabled','false'),('DataExportEnabled','false'),('Notice','synthetic-seed')
ON CONFLICT (\"key\") DO UPDATE SET value=EXCLUDED.value;
COMMIT;
"""

LOG_SEED = """
BEGIN;
INSERT INTO logs (user_id,created_at,type,content,quota,request_id,other)
VALUES (0,1788600000,2,'synthetic historical consume log',123,'synthetic-upgrade-log','{"cache_tokens":19}');
INSERT INTO audit_logs (event_id,user_id,username,actor_role,created_at,category,request_id,content,success,other)
VALUES ('synthetic-upgrade-event',0,'synthetic-owner',100,1788600000,'operation','synthetic-upgrade-audit',
'synthetic historical audit',false,'{"op":{"action":"model.update","params":{"enabled":false,"limit":0,"large_id":9007199254740993}}}');
COMMIT;
"""

MAIN_ROWS = {
    "users": "username='synthetic-owner'",
    "tokens": "name='synthetic-token'",
    "channels": "name='synthetic-channel'",
    "abilities": f"model='{MODEL}'",
    "models": f"model_name='{MODEL}'",
    "tasks": "task_id='synthetic-completed-task'",
    "midjourneys": "mj_id='synthetic-midjourney'",
    "quota_data": f"model_name='{MODEL}'",
    "options": "\"key\" IN ('ModelRatio','ChuanapiMonitorConfig','PublicModelOwnedBy','TaskEnabled','DataExportEnabled')",
}
LOG_ROWS = {"logs": "request_id='synthetic-upgrade-log'", "audit_logs": "event_id='synthetic-upgrade-event'"}

HISTORICAL_INSTALL_SEED = """
BEGIN;
-- This is a synthetic historical installation, NOT a successful fresh setup.
-- The original 093 version and varchar(50) schema are deliberately unchanged.
INSERT INTO setups (version, initialized_at) VALUES ('synthetic-history-pre093',1788600000);
INSERT INTO users (username,password,aff_code,role,status,"group",quota,quota_paid,quota_bonus,quota_reward,auth_version)
SELECT fixture.username,root.password,fixture.aff_code,fixture.role,1,'default',5000,4000,1000,0,1
FROM users root CROSS JOIN (VALUES ('ci-admin','synthetic-admin-aff',10),('ci-user','synthetic-user-aff',1))
AS fixture(username,aff_code,role) WHERE root.username='ci-upgrade' AND root.role=100;
COMMIT;
"""


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def check_baseline_setup_failure(body, state, version):
    """Only the independently known 093 varchar(50) failure may be continued."""
    require(len(version) == 54, "unexpected archived baseline VERSION length")
    message = json.dumps(body)
    require(body.get("success") is False and ("22001" in message or "character varying(50)" in message),
            "baseline fresh setup failed for a different reason; do not mask it")
    require(state == {"root_count": 1, "setup_count": 0, "username": "ci-upgrade", "quota": 100000000},
            "baseline partial-root state differs from the known setup failure")


def check_fresh_setup_state(state):
    require(state == {"root_count": 1, "setup_count": 1, "quota": 100000000, "quota_paid": 100000000,
                      "quota_bonus": 0, "quota_reward": 0, "ledger_rows": 1, "operation_rows": 1,
                      "linked_root_credits": 1, "credit_delta": 100000000},
            "candidate fresh setup did not create one atomic paid-only root credit")


def check_role_identity(body, username, role):
    data = body.get("data", {})
    require(body.get("success") is True and data.get("username") == username and data.get("role") == role,
            "authenticated account identity or role changed")


def check_root_aff_code_indexes(indexes):
    require(isinstance(indexes, list) and bool(indexes),
            "users.aff_code lacks an actual valid, nonpartial, single-column unique index")


def check_snapshot(before, after, same_schema=False):
    """Existing values and constraints must survive; additive columns may appear."""
    require(set(before) == {"main", "log"} == set(after), "missing primary or separate log snapshot")
    check_root_aff_code_indexes(before["main"].get("root_aff_code_unique_indexes"))
    check_root_aff_code_indexes(after["main"].get("root_aff_code_unique_indexes"))
    for database, rows in (("main", MAIN_ROWS), ("log", LOG_ROWS)):
        for table in rows:
            old = before[database][table]
            new = after[database][table]
            count = 5 if table == "options" else 1
            require(len(old) == len(new) == count, f"{database}.{table}: fixture count changed or missing")
            for previous, current in zip(old, new):
                for column, value in previous.items():
                    # A successful read-only API-token request may touch this timestamp.
                    if table == "tokens" and column == "accessed_time":
                        continue
                    if table == "options" and column == "value" and previous["key"] in {"ModelRatio", "ChuanapiMonitorConfig"}:
                        require(column in current and json.loads(current[column]) == json.loads(value),
                                f"{database}.{table}.{previous['key']}: saved JSON changed")
                        continue
                    require(column in current and current[column] == value,
                            f"{database}.{table}.{column}: pre-existing fixture value changed")
        for kind in ("columns", "indexes", "constraints"):
            old = before[database][kind]
            new = after[database][kind]
            require(bool(old), f"{database}: no {kind} evidence")
            for name, definition in old.items():
                require(new.get(name) == definition, f"{database}: {kind} {name} changed or disappeared")
            if same_schema:
                require(old == new, f"{database}: {kind} changed on repeated candidate startup")


def check_candidate_legacy_defaults(snapshot):
    main = snapshot["main"]
    token, task, mj, quota = (main[name][0] for name in ("tokens", "tasks", "midjourneys", "quota_data"))
    require("allow_user_quota_query" in token and token["allow_user_quota_query"] in (None, False),
            "historical token gained user-quota permission")
    require("wallet_debit" in mj and mj["wallet_debit"] is None,
            "historical media lost its legacy NULL wallet receipt")
    for column in ("settlement_pending", "settlement_attempted_at"):
        require(column in task and task[column] is None, "historical task manufactured pending settlement state")
    for column in ("cache_read_tokens", "cache_creation_tokens"):
        require(column in quota and quota[column] == 0, "historical cache counters did not default to zero")
    require("idx_audit_time_event" in snapshot["log"]["indexes"], "audit ordering index was not migrated")


def check_models(body, candidate):
    require(body.get("object") == "list" and isinstance(body.get("data"), list), "model list contract failed")
    require([row.get("id") for row in body["data"]] == [MODEL], "synthetic token model visibility changed")
    if candidate:
        require(body["data"][0].get("owned_by") == "synthetic-owner-label", "persisted candidate public owner was not loaded")


def verify_checkout(path, sha):
    require(re.fullmatch(r"[0-9a-f]{40}", sha) is not None, "source SHA must be immutable and complete")
    actual = subprocess.check_output(["git", "-C", str(path), "rev-parse", "HEAD"], text=True).strip()
    require(actual == sha, f"unexpected checkout revision: {path}")
    status = subprocess.check_output(["git", "-C", str(path), "status", "--porcelain", "--untracked-files=all"], text=True)
    require(not status, f"source checkout must be clean: {path}")
    ignored = subprocess.check_output(["git", "-C", str(path), "ls-files", "--others", "--ignored", "--exclude-standard"], text=True)
    require(not ignored, f"source checkout must not contain ignored local files or credentials: {path}")
    return (path / "VERSION").read_text().strip()


class NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, new_url):
        return None


def owned_container_http_origin(state, network, app, network_name, network_id):
    require(network_id and network.get("Id") == network_id, "runtime network identity changed")
    require(network.get("Name") == network_name and network.get("Internal") is True and network.get("Driver") == "bridge",
            "runtime network must be the owned internal bridge")
    require(state.get("Name") == "/" + app and not state["HostConfig"]["PortBindings"],
            "runtime container identity or unpublished-port contract changed")
    attached = state["NetworkSettings"]["Networks"]
    require(set(attached) == {network_name} and attached[network_name].get("NetworkID") == network_id,
            "runtime attached to unexpected network")
    member = network.get("Containers", {}).get(state["Id"], {})
    require(member.get("Name") == app, "runtime container is not a member of the owned network")
    address = ipaddress.ip_address(attached[network_name]["IPAddress"])
    require(address.version == 4 and address.is_private and not address.is_loopback and not address.is_unspecified and not address.is_multicast,
            "runtime HTTP target must be a private Docker IPv4 address")
    require(ipaddress.ip_interface(member["IPv4Address"]).ip == address,
            "runtime container and network disagree on the HTTP address")
    subnets = [ipaddress.ip_network(item["Subnet"]) for item in network["IPAM"]["Config"] if ":" not in item["Subnet"]]
    require(any(address in subnet for subnet in subnets), "runtime HTTP address is outside the owned network")
    return f"http://{address}:3000"


class Lifecycle:
    def __init__(self, out, log_database_layout="separate"):
        require(log_database_layout in {"separate", "shared"}, "unsupported log database layout")
        self.out = out
        self.log_database_layout = log_database_layout
        self.log_database = "synthetic_main" if log_database_layout == "shared" else "synthetic_log"
        self.prefix = "new-api-synthetic-" + uuid.uuid4().hex[:12]
        self.network = self.prefix + "-net"
        self.network_id = None
        self.pg = self.prefix + "-pg"
        self.app = None
        self.http_origin = None
        self.sessions = {}
        self.containers = []
        self.images = []
        self.env = os.environ.copy()
        self.env.pop("DOCKER_CONTEXT", None)
        self.env.pop("DOCKER_HOST", None)

    def docker(self, *args, input=None, check=True, timeout=180, evidence=None):
        command = ["docker", "--host", "unix:///var/run/docker.sock", *args]
        if evidence and args[0] in {"build", "pull", "logs", "version"}:
            # Stream build/diagnostic output directly to disk, retaining partial
            # evidence even when a subprocess or the job reaches its deadline.
            with (self.out / evidence).open("w") as output:
                result = subprocess.run(command, input=input, text=True, stdout=output,
                                        stderr=subprocess.STDOUT, timeout=timeout, env=self.env)
            result.stdout, result.stderr = (self.out / evidence).read_text(), ""
        else:
            result = subprocess.run(command, input=input, text=True, capture_output=True,
                                    timeout=timeout, env=self.env)
            if evidence:
                (self.out / evidence).write_text(result.stdout + result.stderr)
        require(not check or result.returncode == 0, f"docker {args[0]} failed; see retained evidence" if evidence else f"docker {args[0]} failed: {result.stderr}")
        return result

    def inspect(self, kind, name, evidence=None):
        return json.loads(self.docker(kind, "inspect", name, evidence=evidence).stdout)[0]

    def sql(self, database, query):
        return self.docker("exec", "-i", self.pg, "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", database, input=query).stdout.strip()

    def snapshot(self, phase):
        snapshot = {}
        for key, database, tables in (("main", "synthetic_main", MAIN_ROWS), ("log", self.log_database, LOG_ROWS)):
            result = {}
            for table, predicate in tables.items():
                order = '"key"' if table == "options" else "1"
                raw = self.sql(database, f"SELECT COALESCE(jsonb_agg(to_jsonb(t)), '[]') FROM (SELECT * FROM {table} WHERE {predicate} ORDER BY {order}) t;")
                result[table] = json.loads(raw)
            result["columns"] = json.loads(self.sql(database, f"SELECT COALESCE(jsonb_object_agg(table_name||'.'||column_name,jsonb_build_object('type',udt_name,'nullable',is_nullable,'default',column_default,'length',character_maximum_length,'precision',numeric_precision,'scale',numeric_scale)),'{{}}') FROM information_schema.columns WHERE table_schema='public';"))
            result["indexes"] = json.loads(self.sql(database, f"SELECT COALESCE(jsonb_object_agg(indexname,indexdef),'{{}}') FROM pg_indexes WHERE schemaname='public';"))
            result["constraints"] = json.loads(self.sql(database, f"SELECT COALESCE(jsonb_object_agg(c.relname||'.'||con.conname,pg_get_constraintdef(con.oid)),'{{}}') FROM pg_constraint con JOIN pg_class c ON c.oid=con.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public';"))
            if key == "main":
                result["root_aff_code_unique_indexes"] = self.root_aff_code_indexes(database)
            snapshot[key] = result
        (self.out / f"{phase}-snapshot.json").write_text(json.dumps(snapshot, indent=2, sort_keys=True) + "\n")
        return snapshot

    def root_aff_code_indexes(self, database):
        indexes = json.loads(self.sql(database, """
SELECT COALESCE(jsonb_agg(ci.relname ORDER BY ci.relname),'[]')
FROM pg_index i JOIN pg_class t ON t.oid=i.indrelid
JOIN pg_namespace n ON n.oid=t.relnamespace JOIN pg_class ci ON ci.oid=i.indexrelid
JOIN pg_attribute a ON a.attrelid=t.oid AND a.attnum=i.indkey[0]
WHERE n.nspname='public' AND t.relname='users' AND a.attname='aff_code'
AND i.indisunique AND i.indisvalid AND i.indnkeyatts=1 AND i.indnatts=1
AND i.indexprs IS NULL AND i.indpred IS NULL;
"""))
        check_root_aff_code_indexes(indexes)
        return indexes

    def request(self, path, method="GET", payload=None, bearer=None, expected_status=200):
        require(self.http_origin is not None, "runtime HTTP endpoint has not been verified")
        require(path.startswith("/") and not path.startswith("//") and not any(ord(char) < 32 for char in path),
                "runtime HTTP request must use a local absolute path")
        headers = {}
        if payload is not None:
            headers["Content-Type"] = "application/json"
        if bearer:
            headers["Authorization"] = "Bearer " + bearer
        request = urllib.request.Request(self.http_origin + path, method=method, headers=headers,
                                         data=None if payload is None else json.dumps(payload).encode())
        # Linux Docker bridge addresses are reachable from the local daemon host
        # without publishing ports. Never send test sessions via host proxies or
        # follow redirects; GNU wget drops HTTP 401 bodies and exits with code 6.
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirectHandler())
        try:
            try:
                response = opener.open(request, timeout=5)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                require(response.status == expected_status, f"{method} {path} did not return expected HTTP {expected_status}")
                body = response.read(1024 * 1024 + 1)
                require(len(body) <= 1024 * 1024, "runtime HTTP response exceeded the evidence size limit")
                # Headers and response bodies may contain test sessions. Retain
                # only the presence bit in memory, never raw authentication data.
                self.response_has_cookie = response.headers.get("Set-Cookie") is not None
        except (urllib.error.URLError, OSError, http.client.HTTPException):
            raise RuntimeError(f"{method} {path} runtime HTTP transport failed") from None
        try:
            parsed = json.loads(body)
        except (ValueError, UnicodeError):
            raise RuntimeError(f"{method} {path} did not return valid JSON") from None
        require(isinstance(parsed, dict), f"{method} {path} did not return a JSON object")
        return parsed

    def start(self, image, phase, version, main_database="synthetic_main", log_database=None):
        if log_database is None:
            log_database = self.log_database
        self.app = self.prefix + "-" + phase
        self.containers.append(self.app)
        self.docker("run", "--detach", "--pull=never", "--name", self.app, "--network", self.network,
                    "--cap-drop=ALL", "--security-opt=no-new-privileges", "--tmpfs", "/tmp", "--tmpfs", "/data",
                    "--env", f"SQL_DSN=postgres://postgres:synthetic@{self.pg}:5432/{main_database}?sslmode=disable",
                    "--env", f"LOG_SQL_DSN=postgres://postgres:synthetic@{self.pg}:5432/{log_database}?sslmode=disable",
                    "--env", "REDIS_CONN_STRING=", "--env", "SESSION_SECRET=synthetic-upgrade-only-session-secret",
                    "--env", "CRYPTO_SECRET=synthetic-upgrade-only-crypto-secret", "--env", "DEFAULT_LANGUAGE=en",
                    "--env", "CHANNEL_TEST_ENABLED=false", "--env", "UPDATE_TASK=false", "--env", "MODEL_MONITOR_ENABLED=false", "--env", "GIN_MODE=release", image)
        state = self.inspect("container", self.app, evidence=f"{phase}-container.json")
        require(state["Image"] == image, "running app image ID does not match the built source image")
        require(not state["HostConfig"]["PortBindings"], "runtime must not publish host ports")
        require(set(state["NetworkSettings"]["Networks"]) == {self.network}, "runtime attached to unexpected network")
        self.http_origin = owned_container_http_origin(state, self.inspect("network", self.network), self.app, self.network, self.network_id)
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            require(self.inspect("container", self.app)["State"]["Running"], f"{phase} exited during startup")
            probe = self.docker("exec", self.app, "wget", "--quiet", "--timeout=3", "--tries=1", "--output-document=-", "http://127.0.0.1:3000/api/readyz", check=False)
            if probe.returncode == 0:
                require(json.loads(probe.stdout) == {"success": True}, "invalid readiness response")
                break
            time.sleep(1)
        else:
            raise RuntimeError(f"{phase}: readiness deadline exceeded")
        health = self.request("/api/healthz")
        status = self.request("/api/status")
        require(health == {"success": True}, "health probe failed")
        require(status.get("success") is True and status.get("data", {}).get("version") == version, "running binary version mismatch")
        (self.out / f"{phase}-health.json").write_text(json.dumps({"ready": True, "health": health, "version": version}) + "\n")

    def stop(self, phase):
        self.docker("stop", "--time", "30", self.app)
        self.docker("logs", self.app, evidence=f"{phase}-application.log")
        state = self.inspect("container", self.app)["State"]
        (self.out / f"{phase}-exit.json").write_text(json.dumps(state) + "\n")
        require(state["ExitCode"] == 0 and not state["OOMKilled"], f"{phase} did not shut down cleanly")

    def exercise(self, phase, previous_notice, candidate):
        roles = []
        # Exercise root, administrator, and ordinary-user paths independently.
        # Old session tokens cross upgrade/restart/rollback; none enter evidence.
        for username, role in ROLE_ACCOUNTS:
            previous_session = self.sessions.get(username)
            if previous_session:
                check_role_identity(self.request("/api/user/self", bearer=previous_session), username, role)
            login = self.request("/api/user/login", "POST", {"username": username, "password": PASSWORD})
            require(login.get("success") is True, f"synthetic role {role} login failed")
            bearer = login.get("data", {}).get("access_token")
            require(isinstance(bearer, str) and bool(bearer), "login did not issue a session token")
            check_role_identity(self.request("/api/user/self", bearer=bearer), username, role)
            self.sessions[username] = bearer
            end = int(time.time())
            query = f"?start_timestamp={end - 86400}&end_timestamp={end}"
            for path in ("/api/data/self", "/api/data/flow/self"):
                require(self.request(path + query, bearer=bearer).get("success") is True,
                        f"role {role} self-dashboard API failed")
            for path in ("/api/data/", "/api/data/users", "/api/data/flow"):
                expected = 200 if role >= 10 else 403
                body = self.request(path + query, bearer=bearer, expected_status=expected)
                require(body.get("success") is (role >= 10), f"role {role} admin-dashboard authorization changed")
            if role < 100:
                require(self.request("/api/option/", bearer=bearer, expected_status=403).get("success") is False,
                        "non-root account could read root-only settings")
            roles.append({"role": role, "new_login": True, "prior_session": previous_session is not None,
                          "self_dashboard": True, "admin_dashboard_allowed": role >= 10})
        require(self.request("/api/user/self", expected_status=401).get("success") is False,
                "anonymous caller could read protected identity")
        bearer = self.sessions["ci-upgrade"]
        notice = self.request("/api/notice")
        require(notice.get("success") is True and notice.get("data") == previous_notice, "application did not read the prior version's write")
        saved = self.request("/api/option/", "PUT", {"key": "Notice", "value": phase}, bearer)
        require(saved.get("success") is True, "application option write failed")
        require(self.request("/api/notice").get("data") == phase, "application option write could not be read back")
        monitor = self.request("/api/option/model-monitor", bearer=bearer)
        require(monitor.get("success") is True and monitor.get("data", {}).get("config") == MONITOR_CONFIG, "saved monitor visibility did not load")
        omitted = {key: value for key, value in MONITOR_CONFIG.items() if key != "hidden_groups"}
        monitor = self.request("/api/option/model-monitor", "PUT", omitted, bearer)
        require(monitor.get("success") is True and monitor.get("data") == MONITOR_CONFIG, "legacy omission-save lost hidden groups")
        usage = self.request("/api/usage/token/", bearer="sk-" + TOKEN)
        require(usage.get("code") is True and "user_quota" not in usage.get("data", {}),
                "historical API token exposed user-level quota after upgrade")
        models = self.request("/v1/models", bearer="sk-" + TOKEN)
        check_models(models, candidate)
        (self.out / f"{phase}-contracts.json").write_text(json.dumps({"roles": roles, "root_read_write": True, "previous_notice": previous_notice,
            "saved_notice": phase, "monitor_omission_save": True, "models": models}, indent=2) + "\n")

    def fresh_setup_state(self):
        return json.loads(self.sql("candidate_fresh_main", """
SELECT jsonb_build_object(
 'root_count',(SELECT count(*) FROM users WHERE role=100),
 'setup_count',(SELECT count(*) FROM setups),
 'quota',quota,'quota_paid',quota_paid,'quota_bonus',quota_bonus,'quota_reward',quota_reward,
 'ledger_rows',(SELECT count(*) FROM balance_ledger),
 'operation_rows',(SELECT count(*) FROM balance_ledger_operations),
 'linked_root_credits',(SELECT count(*) FROM balance_ledger l JOIN balance_ledger_operations o ON o.id=l.operation_id
   WHERE l.user_id=u.id AND o.user_id=u.id AND l.reference_type='root_init' AND l.account='paid'
     AND l.idempotency_key=o.idempotency_key AND l.idempotency_key='credit:root_init:'||u.id::text),
 'credit_delta',(SELECT sum(delta) FROM balance_ledger WHERE user_id=u.id))
FROM users u WHERE username='ci-fresh';
"""))

    def fresh_candidate(self, image, version):
        fresh_log = "candidate_fresh_main" if self.log_database_layout == "shared" else "candidate_fresh_log"
        for database in sorted({"candidate_fresh_main", fresh_log}):
            self.docker("exec", self.pg, "createdb", "-U", "postgres", database)
        self.start(image, "candidate-fresh-setup", version, "candidate_fresh_main", fresh_log)
        payload = {"username": "ci-fresh", "password": PASSWORD, "confirmPassword": PASSWORD}
        body = self.request("/api/setup", "POST", payload)
        require(body.get("success") is True, "candidate fresh HTTP setup failed")
        require(not self.response_has_cookie and "access_token" not in json.dumps(body), "setup must not issue a login session")
        unique_indexes = self.root_aff_code_indexes("candidate_fresh_main")
        initial = self.fresh_setup_state()
        check_fresh_setup_state(initial)
        require(self.request("/api/setup", "POST", payload).get("success") is False, "setup replay unexpectedly succeeded")
        require(self.fresh_setup_state() == initial, "setup replay changed the root grant")
        self.stop("candidate-fresh-setup")
        self.start(image, "candidate-fresh-restart", version, "candidate_fresh_main", fresh_log)
        require(self.request("/api/setup").get("data", {}).get("status") is True, "setup status did not survive restart")
        require(self.fresh_setup_state() == initial, "candidate restart changed the root grant")
        self.stop("candidate-fresh-restart")
        (self.out / "candidate-fresh-setup-contracts.json").write_text(json.dumps({"setup_state": initial, "root_aff_code_unique_indexes": unique_indexes}, indent=2) + "\n")

    def cleanup(self):
        # Names are freshly generated here, never supplied by a caller.
        for container in reversed(self.containers):
            self.docker("logs", container, check=False, evidence=container + "-cleanup.log")
            self.docker("rm", "--force", "--volumes", container, check=False)
        self.docker("network", "rm", self.network, check=False)
        for image in self.images:
            self.docker("image", "rm", image, check=False)
        # A successful subprocess wrapper is not proof that --check=False
        # removals succeeded. Verify that our uniquely named resources are gone.
        require(not self.docker("container", "ls", "--all", "--filter", "name=" + self.prefix,
                                "--format", "{{.Names}}").stdout.strip(), "owned synthetic containers remain after cleanup")
        require(not self.docker("network", "ls", "--filter", "name=" + self.network,
                                "--format", "{{.Name}}").stdout.strip(), "owned synthetic network remains after cleanup")
        require(not self.docker("image", "ls", "--filter", "reference=new-api:" + self.prefix + "-*",
                                "--format", "{{.Repository}}:{{.Tag}}").stdout.strip(), "owned synthetic image tags remain after cleanup")


def finish_evidence(run, manifest, out):
    error_to_raise = None
    try:
        run.cleanup()
        manifest["cleanup"] = "completed"
    except (OSError, subprocess.SubprocessError, RuntimeError) as error:
        (out / "cleanup-error.txt").write_text(str(error) + "\n")
        manifest["cleanup"] = "failed"
        if manifest["result"] == "passed":
            manifest["result"] = "failed"
            manifest["error"] = "cleanup failed; inspect cleanup-error.txt"
            error_to_raise = error
    (out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    if error_to_raise is not None:
        raise error_to_raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline-dir", required=True, type=Path)
    parser.add_argument("--candidate-dir", required=True, type=Path)
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--log-database-layout", choices=("separate", "shared"), default="separate")
    args = parser.parse_args()
    baseline, candidate, out = args.baseline_dir.resolve(), args.candidate_dir.resolve(), args.out.resolve()
    require(baseline != candidate, "baseline and candidate must use separate checkouts")
    require(not out.is_relative_to(baseline) and not out.is_relative_to(candidate), "evidence must be outside source checkouts")
    out.mkdir(parents=True, exist_ok=True)
    require(not any(out.iterdir()), "evidence directory must be empty; stale results cannot be reused")
    versions = {"baseline": verify_checkout(baseline, BASELINE_SHA), "candidate": verify_checkout(candidate, args.candidate_sha)}
    require(0 < len(versions["candidate"]) <= 50, "candidate VERSION exceeds setups.version varchar(50); do not widen historical schema to hide it")
    recipe = candidate / "Dockerfile.api"
    manifest = {"scope": "exact archived 093 source with synthetic historical initialization; NOT deployed production artifact or representative production data",
                "baseline_sha": BASELINE_SHA, "candidate_sha": args.candidate_sha, "postgres_image": POSTGRES_IMAGE,
                "build_recipe_source_sha": args.candidate_sha, "build_recipe_sha256": hashlib.sha256(recipe.read_bytes()).hexdigest(),
                "baseline_recipe_difference": "candidate Dockerfile.api includes tokenkit/go.mod before go mod download; baseline source is unchanged",
                "versions": versions, "result": "running", "completed_phases": [],
                "baseline_fresh_setup": "not_run", "browser_old_tabs": "not_run_api_only_image",
                "production_schema_clone": False, "new_upstream_requests": False,
                "log_database_layout": args.log_database_layout}
    run = Lifecycle(out, args.log_database_layout)
    try:
        run.docker("version", evidence="docker-version.txt")
        images = {}
        for label, path, sha in (("baseline", baseline, BASELINE_SHA), ("candidate", candidate, args.candidate_sha)):
            tag = "new-api:" + run.prefix + "-" + label
            run.images.append(tag)
            run.docker("build", "--progress=plain", "-f", str(recipe), "--build-arg", "SOURCE_REVISION=" + sha,
                       "--build-arg", "RELEASE_VERSION=" + versions[label], "--tag", tag, str(path), timeout=1800, evidence=label + "-build.log")
            inspected = run.inspect("image", tag, evidence=label + "-image.json")
            labels = inspected["Config"]["Labels"]
            require(labels.get("org.opencontainers.image.revision") == sha and labels.get("org.opencontainers.image.version") == versions[label], "built image provenance labels mismatch")
            images[label] = inspected["Id"]
        manifest["application_image_ids"] = images
        run.docker("pull", POSTGRES_IMAGE, timeout=300, evidence="postgres-pull.log")
        pg_image = run.inspect("image", POSTGRES_IMAGE, evidence="postgres-image.json")["Id"]
        manifest["postgres_image_id"] = pg_image
        run.network_id = run.docker("network", "create", "--internal", run.network).stdout.strip()
        require(run.inspect("network", run.network, evidence="network.json")["Internal"] is True, "runtime network is not internal")
        run.containers.append(run.pg)
        run.docker("run", "--detach", "--pull=never", "--name", run.pg, "--network", run.network,
                   "--env", "POSTGRES_PASSWORD=synthetic", "--env", "POSTGRES_DB=synthetic_main", pg_image)
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            require(run.inspect("container", run.pg)["State"]["Running"], "PostgreSQL exited during startup")
            if run.docker("exec", run.pg, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "synthetic_main", check=False).returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError("PostgreSQL readiness deadline exceeded")
        db_version = run.sql("synthetic_main", "SHOW server_version;")
        require(re.match(r"18\.6(?:\D|$)", db_version) is not None, "expected PostgreSQL 18.6")
        manifest["postgres_server_version"] = db_version
        if args.log_database_layout == "separate":
            run.docker("exec", run.pg, "createdb", "-U", "postgres", "synthetic_log")
        run.start(images["baseline"], "baseline-fresh", versions["baseline"])
        setup = run.request("/api/setup", "POST", {"username": "ci-upgrade", "password": PASSWORD, "confirmPassword": PASSWORD})
        state = json.loads(run.sql("synthetic_main", """
SELECT jsonb_build_object('root_count',(SELECT count(*) FROM users WHERE role=100),
 'setup_count',(SELECT count(*) FROM setups),'username',username,'quota',quota)
FROM users WHERE username='ci-upgrade';
"""))
        check_baseline_setup_failure(setup, state, versions["baseline"])
        manifest["baseline_fresh_setup"] = {"result": "known_failure_observed", "cause": "54-character VERSION exceeds varchar(50)",
                                             "state": state, "not_a_passing_fresh_install": True}
        (out / "baseline-fresh-setup-known-failure.json").write_text(json.dumps(manifest["baseline_fresh_setup"], indent=2) + "\n")
        run.stop("baseline-fresh")
        manifest["completed_phases"].append("baseline-fresh-known-failure")
        run.sql("synthetic_main", HISTORICAL_INSTALL_SEED)
        run.sql("synthetic_main", MAIN_SEED)
        run.sql(run.log_database, LOG_SEED)
        initial = run.snapshot("seed")
        check_snapshot(initial, initial)
        previous_notice = "synthetic-seed"
        upgraded = None
        for phase, label in (("baseline-seeded", "baseline"), ("candidate-upgrade", "candidate"),
                             ("candidate-restart", "candidate"), ("baseline-rollback", "baseline"),
                             ("candidate-reupgrade", "candidate")):
            run.start(images[label], phase, versions[label])
            run.exercise(phase, previous_notice, label == "candidate")
            run.stop(phase)
            current = run.snapshot(phase)
            check_snapshot(initial, current)
            if label == "candidate" or upgraded is not None:
                check_candidate_legacy_defaults(current)
            if upgraded is not None:
                check_snapshot(upgraded, current, same_schema=phase in {"candidate-restart", "candidate-reupgrade"})
            if phase == "candidate-upgrade":
                upgraded = current
            require(run.sql("synthetic_main", "SELECT value FROM options WHERE \"key\"='Notice';") == phase, "application write was not persisted after shutdown")
            previous_notice = phase
            manifest["completed_phases"].append(phase)
        run.fresh_candidate(images["candidate"], versions["candidate"])
        manifest["completed_phases"].extend(["candidate-fresh-setup", "candidate-fresh-restart"])
        verify_checkout(baseline, BASELINE_SHA)
        verify_checkout(candidate, args.candidate_sha)
        manifest["result"] = "passed"

    except BaseException as error:
        manifest["result"] = "failed"
        manifest["error"] = str(error)
        raise
    finally:
        finish_evidence(run, manifest, out)
    print(f"PASS: exact 093 source synthetic historical install → candidate → restart → application rollback → re-upgrade; candidate fresh setup/replay/restart; root/admin/user API and retained sessions; {args.log_database_layout} PG 18.6 main/log layout. Baseline fresh setup remains a known failure; browser old tabs and production equivalence NOT tested.")


if __name__ == "__main__":
    main()
