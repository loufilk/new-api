#!/usr/bin/env python3
"""Start disposable loopback database fixtures without registering services."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import time


def run(arguments, **kwargs):
    return subprocess.run([str(item) for item in arguments], check=True, **kwargs)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=["start", "status", "stop"])
    args = parser.parse_args()
    base = Path(os.environ.get("A07_DB_DIR", Path.home() / "Library/Application Support/WanchuanA07Tests"))
    pg_bin = Path(os.environ.get("A07_PG_BIN", "/opt/homebrew/opt/postgresql@18/bin"))
    mysql_bin = Path(os.environ.get("A07_MYSQL_BIN", "/opt/homebrew/opt/mysql@8.4/bin"))
    pg_port, mysql_port = "55438", "53306"
    base.mkdir(parents=True, exist_ok=True)
    pg_data, mysql_data = base / "postgres", base / "mysql"
    mysql_socket = base / "mysql.sock"

    if args.action == "stop":
        subprocess.run([str(pg_bin / "pg_ctl"), "-D", str(pg_data), "-m", "fast", "-w", "stop"], check=False)
        subprocess.run([str(mysql_bin / "mysqladmin"), "--no-defaults", "--socket=" + str(mysql_socket), "-uroot", "shutdown"], check=False)
        return

    if args.action == "start":
        for executable in [pg_bin / "postgres", mysql_bin / "mysqld"]:
            if not executable.is_file():
                raise SystemExit(f"Database binary missing: {executable}")
        if not (pg_data / "PG_VERSION").exists():
            run([pg_bin / "initdb", "-D", pg_data, "-U", "a07", "--auth-local=trust", "--auth-host=trust", "--encoding=UTF8", "--locale=C"])
        ready = subprocess.run([str(pg_bin / "pg_isready"), "-h", "127.0.0.1", "-p", pg_port], capture_output=True)
        if ready.returncode != 0:
            run([pg_bin / "pg_ctl", "-D", pg_data, "-l", base / "postgres.log", "-o", f"-h 127.0.0.1 -p {pg_port} -k '{base}'", "-w", "-t", "30", "start"])
        if not (mysql_data / "auto.cnf").exists():
            run([mysql_bin / "mysqld", "--no-defaults", "--initialize-insecure", "--datadir=" + str(mysql_data)], stdout=subprocess.DEVNULL)
        mysql_ready = subprocess.run([str(mysql_bin / "mysqladmin"), "--no-defaults", "--socket=" + str(mysql_socket), "-uroot", "ping"], capture_output=True)
        if mysql_ready.returncode != 0:
            log = (base / "mysql-launch.log").open("a")
            subprocess.Popen([str(mysql_bin / "mysqld"), "--no-defaults", "--datadir=" + str(mysql_data), "--bind-address=127.0.0.1", "--port=" + mysql_port, "--socket=" + str(mysql_socket), "--mysqlx=OFF", "--log-error=" + str(base / "mysql.log"), "--pid-file=" + str(base / "mysql.pid")], stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            log.close()
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                ready = subprocess.run([str(mysql_bin / "mysqladmin"), "--no-defaults", "--socket=" + str(mysql_socket), "-uroot", "ping"], capture_output=True)
                if ready.returncode == 0:
                    break
                time.sleep(0.25)
            else:
                raise SystemExit(f"MySQL startup did not finish; inspect {base / 'mysql.log'}")
        for name in ["a07_model", "a07_schema", "a07_compat", "a07_compat_log"]:
            exists = subprocess.check_output([str(pg_bin / "psql"), "-h", "127.0.0.1", "-p", pg_port, "-U", "a07", "-d", "postgres", "-Atc", "SELECT 1 FROM pg_database WHERE datname = '" + name + "'"]).decode().strip()
            if exists != "1":
                run([pg_bin / "createdb", "-h", "127.0.0.1", "-p", pg_port, "-U", "a07", name])
            run([mysql_bin / "mysql", "--no-defaults", "--socket=" + str(mysql_socket), "-uroot", "-e", f"CREATE DATABASE IF NOT EXISTS {name} CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"])
        # These are local disposable fixture addresses, not deployment secrets.
        config = {
            "postgres": f"postgres://a07@127.0.0.1:{pg_port}/a07_model?sslmode=disable",
            "mysql": f"root@tcp(127.0.0.1:{mysql_port})/a07_model?charset=utf8mb4&parseTime=True&loc=Local",
            "directory": str(base),
        }
        (base / "fixture.json").write_text(json.dumps(config, indent=2) + "\n")
    run([pg_bin / "psql", "-h", "127.0.0.1", "-p", pg_port, "-U", "a07", "-d", "postgres", "-Atc", "SHOW server_version"])
    run([mysql_bin / "mysql", "--no-defaults", "--socket=" + str(mysql_socket), "-uroot", "-N", "-e", "SELECT VERSION()"])
    print(f"Fixture directory: {base}")


if __name__ == "__main__":
    main()
