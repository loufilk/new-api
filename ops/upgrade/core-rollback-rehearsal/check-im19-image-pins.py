#!/usr/bin/env python3
"""Read-only OCI identity preflight on an isolated Linux runner. Never deploys."""
import argparse
import json
import os
import platform
from pathlib import Path
import re
import shutil
import subprocess

from release_identity import ID, ROLLBACK_REVISION, STOCK_REVISION, VERSIONS, inspect_image_identity, pinned_ref

IM19_VERSION = "v1.0.0-rc.51.chuanapi.im.19"


def verify_staged_pins(previous, candidate, architecture, inspect):
    approved = (("previous", *previous, VERSIONS["target"]), ("candidate", *candidate, IM19_VERSION))
    if architecture not in ("amd64", "arm64"):
        raise ValueError("unsupported Linux architecture")
    if previous[0] == candidate[0] or previous[1] == candidate[1]:
        raise ValueError("distinct immutable image and source pins are required")
    for role, pin, revision, _ in approved:
        if not pinned_ref(pin) or ID.fullmatch(pin):
            raise ValueError(role + ": a canonical registry repository digest is required")
        if (not re.fullmatch(r"[0-9a-f]{40}", revision)
                or revision in ("0" * 40, STOCK_REVISION, ROLLBACK_REVISION)):
            raise ValueError(role + ": an approved full source SHA for im.18 or im.19 is required")
    artifacts = {
        role: inspect_image_identity(role, pin, inspect(pin), architecture, revision, "",
                                     version, require_stamp=role == "candidate")
        for role, pin, revision, version in approved
    }
    if artifacts["previous"]["id"] == artifacts["candidate"]["id"]:
        raise ValueError("approved images must resolve to distinct image IDs")
    return artifacts


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for role in ("previous", "candidate"):
        parser.add_argument(f"--{role}-image", required=True)
        parser.add_argument(f"--{role}-revision", required=True)
    parser.add_argument("--confirm-isolated-runner", required=True, action="store_true")
    parser.add_argument("--docker-socket", type=Path, default=Path("/var/run/docker.sock"))
    args = parser.parse_args()
    dock = shutil.which("docker")
    if not dock or platform.system() != "Linux":
        parser.error("use a Linux runner with an existing Docker CLI; no installation will be attempted")
    if not args.docker_socket.is_absolute() or not args.docker_socket.is_socket():
        parser.error("an existing local Docker Unix socket is required")
    arch = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine())
    env = {"PATH": os.environ["PATH"]}

    def inspect(pin):
        result = subprocess.run(
            [dock, "--host", f"unix://{args.docker_socket}", "image", "inspect", pin],
            env=env, capture_output=True, text=True, timeout=30, check=True,
        )
        img, = json.loads(result.stdout)
        return img

    try:
        artifacts = verify_staged_pins(
            (args.previous_image, args.previous_revision), (args.candidate_image, args.candidate_revision),
            arch, inspect,
        )
    except (ValueError, KeyError, TypeError, AttributeError, subprocess.SubprocessError, OSError) as exc:
        parser.exit(1, f"im.19 image identity preflight failed: {exc}\n")
    print(json.dumps({"identity_preflight": "PASS", "schema_preserving_rollback": "NOT_PROVEN",
                      "deployment": "NOT_AUTHORIZED", "artifacts": artifacts}, indent=2))


if __name__ == "__main__":
    main()
