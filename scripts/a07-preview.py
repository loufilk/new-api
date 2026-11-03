#!/usr/bin/env python3
"""Serve the built app with synthetic users on loopback port 3107."""

import argparse
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["start", "status", "stop"])
    parser.add_argument("--binary", type=Path, default=Path("/tmp/wanchuan-a07-preview-server"))
    args = parser.parse_args()
    base = Path.home() / "Library/Application Support/WanchuanA07Preview"
    base.mkdir(mode=0o700, parents=True, exist_ok=True)
    pidfile = base / "server.pid"
    url = "http://127.0.0.1:3107"
    if args.action == "stop":
        if pidfile.exists():
            pid = int(pidfile.read_text())
            name = subprocess.run(["ps", "-p", str(pid), "-o", "comm="], capture_output=True, text=True).stdout.strip()
            if name == str(args.binary):
                os.kill(pid, signal.SIGTERM)
                deadline = time.monotonic() + 20
                while time.monotonic() < deadline:
                    with socket.socket() as probe:
                        if probe.connect_ex(("127.0.0.1", 3107)) != 0:
                            break
                    time.sleep(.25)
                else:
                    raise SystemExit("Preview has not released its port; inspect server.log.")
                pidfile.unlink()
                print("Stopped the isolated A07 preview.")
            elif name:
                raise SystemExit("Saved PID belongs to another process; left it untouched.")
        return
    if args.action == "status":
        with urllib.request.urlopen(url + "/api/status", timeout=5) as response:
            data = json.load(response)
        print(f"{'Ready' if data.get('success') else 'Not ready'}: {url}")
        return
    if not args.binary.is_file():
        raise SystemExit("Build the frontend, then go build -o /tmp/wanchuan-a07-preview-server .")
    with socket.socket() as probe:
        probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        probe.bind(("127.0.0.1", 3107))
    private = base / "fixture.private.json"
    if private.exists():
        fixture = json.loads(private.read_text())
    else:
        fixture = {"password": "A07!" + secrets.token_hex(8), "session_secret": secrets.token_hex(32)}
        descriptor = os.open(private, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as stream:
            stream.write(json.dumps(fixture, indent=2) + "\n")
    env = {key: os.environ[key] for key in ["PATH", "HOME", "TMPDIR", "LANG"] if key in os.environ}
    env.update({"GOWORK": "off", "SQL_DSN": "local", "SQLITE_PATH": str(base / "preview.db"), "HTTP_BIND_ADDRESS": "127.0.0.1", "PORT": "3107", "NODE_TYPE": "master", "GIN_MODE": "release", "SESSION_COOKIE_SECURE": "false", "SESSION_SECRET": fixture["session_secret"], "REDIS_CONN_STRING": "", "TRUSTED_PROXIES": "none", "DEFAULT_LANGUAGE": "zh-CN", "VERSION": "A07-local-preview"})

    def launch():
        with (base / "server.log").open("a") as log:
            process = subprocess.Popen([str(args.binary), "--log-dir", str(base / "logs")], cwd=base, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        pidfile.write_text(str(process.pid))
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise SystemExit(f"Preview exited; inspect {base / 'server.log'}")
            try:
                with urllib.request.urlopen(url + "/api/status", timeout=1) as response:
                    if json.load(response).get("success"):
                        return process
            except (OSError, ValueError):
                pass
            time.sleep(.25)
        process.terminate()
        raise SystemExit(f"Preview startup timed out; inspect {base / 'server.log'}")

    process = launch()
    try:
        seed_env = env | {"A07_PREVIEW_PASSWORD": fixture["password"]}
        subprocess.run(["go", "run", "scripts/a07-preview-seed.go"], cwd=Path(__file__).resolve().parent.parent, env=seed_env, check=True)
    finally:
        process.terminate()
        process.wait(timeout=20)
    launch()
    accounts = base / "ACCOUNTS.txt"
    accounts.write_text(f"A07 local synthetic preview only\nURL: {url}/sign-in\n\nAgent: a07_andy\nRoot: a07_root\nUser: a07_user\nPassword for these three accounts: {fixture['password']}\n\nNo real payment provider or production data.\n")
    accounts.chmod(0o600)
    print(f"Preview ready: {url}/agent-platform")
    print(f"Synthetic login details: {accounts}")


if __name__ == "__main__":
    main()
