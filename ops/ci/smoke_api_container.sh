#!/usr/bin/env bash
# Build only a local image. Runtime has no network, host ports or upstream credentials.
set -euo pipefail
: "${OUT:?OUT must name an evidence directory}"
: "${SOURCE_SHA:?SOURCE_SHA must identify the checkout}"
test "$(git rev-parse HEAD)" = "$SOURCE_SHA"
mkdir -p "$OUT"
printf '%s\n' "$SOURCE_SHA" > "$OUT/revision.txt"
docker version > "$OUT/docker-version.txt"
version=$(tr -d '\r\n' < VERSION)
image="new-api:ci-api-only-${SOURCE_SHA:0:12}"
container="new-api-ci-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-$$"
volume="$container-data"

cleanup() {
  local status=$?
  if docker container inspect "$container" >/dev/null 2>&1; then
    docker logs "$container" > "$OUT/container.log" 2>&1 || true
    docker inspect --format '{{json .State}}' "$container" > "$OUT/container-state.json" || true
    docker rm --force "$container" >/dev/null || true
  fi
  docker volume rm "$volume" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT

docker build --progress=plain -f Dockerfile.api \
  --build-arg "SOURCE_REVISION=$SOURCE_SHA" --build-arg "RELEASE_VERSION=$version" \
  --tag "$image" . 2>&1 | tee "$OUT/build.log"
docker image inspect "$image" > "$OUT/image.json"
test "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")" = "$SOURCE_SHA"
test "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$image")" = "$version"
docker volume create "$volume" >/dev/null
docker run --detach --name "$container" --network none \
  --cap-drop ALL --security-opt no-new-privileges --tmpfs /tmp \
  --mount "type=volume,source=$volume,target=/data" \
  --env SQL_DSN=local --env SQLITE_PATH=/data/new-api.db \
  --env LOG_SQL_DSN= --env REDIS_CONN_STRING= \
  --env SESSION_SECRET=disposable-api-only-ci-session-secret \
  --env DEFAULT_LANGUAGE=en --env GIN_MODE=release "$image" > "$OUT/container-id.txt"

for startup in fresh repeated; do
  if [ "$startup" = repeated ]; then
    docker start "$container" >/dev/null
  fi
  deadline=$((SECONDS + 120))
  ready=false
  while [ "$SECONDS" -lt "$deadline" ]; do
    test "$(docker inspect --format '{{.State.Running}}' "$container")" = true
    if docker exec "$container" wget --quiet --timeout=3 --output-document=- http://127.0.0.1:3000/api/readyz > "$OUT/$startup-ready.json"; then
      ready=true
      break
    fi
    sleep 1
  done
  test "$ready" = true
  docker exec "$container" wget --quiet --timeout=3 --output-document=- http://127.0.0.1:3000/api/healthz > "$OUT/$startup-health.json"
  docker exec "$container" wget --quiet --timeout=3 --output-document=- http://127.0.0.1:3000/api/status > "$OUT/$startup-status.json"
  # Wget returns 8 for an HTTP error. Only the expected API-only JSON 404 is valid.
  dashboard_status=0
  docker exec "$container" wget --server-response --content-on-error --max-redirect=0 --timeout=3 --output-document=- http://127.0.0.1:3000/dashboard > "$OUT/$startup-dashboard.json" 2> "$OUT/$startup-dashboard.headers" || dashboard_status=$?
  test "$dashboard_status" -eq 8
  python3 - "$OUT" "$startup" "$version" <<'PY'
import json
from pathlib import Path
import re
import sys

root, startup, version = Path(sys.argv[1]), sys.argv[2], sys.argv[3]
for endpoint in ("ready", "health"):
    body = json.loads((root / f"{startup}-{endpoint}.json").read_text())
    if body != {"success": True}:
        raise SystemExit(f"{startup} {endpoint} probe failed: {body!r}")
status = json.loads((root / f"{startup}-status.json").read_text())
if status.get("success") is not True or status.get("data", {}).get("version") != version:
    raise SystemExit("running binary version does not match the checked-out source")
headers = (root / f"{startup}-dashboard.headers").read_text()
if not re.search(r"HTTP/\S+ 404\b", headers) or not re.search(r"Content-Type: application/json", headers, re.I):
    raise SystemExit("API-only dashboard must return an HTTP 404 JSON response")
if re.search(r"^\s*Location:", headers, re.M | re.I):
    raise SystemExit("API-only dashboard must not redirect")
dashboard = json.loads((root / f"{startup}-dashboard.json").read_text())
if "Invalid URL" not in dashboard.get("error", {}).get("message", ""):
    raise SystemExit("API-only dashboard returned an unexpected error contract")
print(f"{startup}: ready, healthy, source version {version}, API-only JSON 404")
PY
  docker stop --time 30 "$container" >/dev/null
  test "$(docker inspect --format '{{.State.ExitCode}}' "$container")" = 0
done
printf 'API-only image built and passed fresh/repeated startup for %s\n' "$SOURCE_SHA" | tee "$OUT/result.txt"
