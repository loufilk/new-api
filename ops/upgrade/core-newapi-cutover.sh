#!/usr/bin/env bash
# Deliberately touches the NewAPI service only. Never runs compose down/pull or DB restore.
set -euo pipefail
die() { printf 'CORE cutover: %s\n' "$*" >&2; exit 1; }
[[ $# -ge 1 ]] || die 'use upgrade|rollback [Docker Compose global options]'
phase=$1; shift
case "$phase" in upgrade|rollback) ;; *) die 'use upgrade or rollback';; esac
found_file=no
for arg in "$@"; do
  case "$arg" in -f|--file|--file=*) found_file=yes ;; esac
done
[[ $found_file == yes ]] || die 'pass the existing Compose file(s) explicitly with -f'
: "${CORE_FROM_IMAGE:?expected current NewAPI image, pinned by digest}"
: "${CORE_TO_IMAGE:?target NewAPI image, pinned by digest}"
: "${CORE_TO_REVISION:?exact 40-character target git SHA}"
: "${CORE_CUTOVER_PIN_FILE:?absolute persistent Compose override path}"
[[ $CORE_CUTOVER_PIN_FILE == /* && -d $(dirname -- "$CORE_CUTOVER_PIN_FILE") && ! -L $CORE_CUTOVER_PIN_FILE ]] ||
  die 'the persistent override must be an absolute path in an existing directory, not a symlink'
for arg in "$@"; do
  [[ $arg != "$CORE_CUTOVER_PIN_FILE" && $arg != --file="$CORE_CUTOVER_PIN_FILE" ]] ||
    die 'do not pass the persistent override: this script appends it'
done
svc=${CORE_API_SERVICE:-new-api}
[[ $svc =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ ]] || die 'invalid Compose service'
for ref in "$CORE_FROM_IMAGE" "$CORE_TO_IMAGE"; do
  [[ $ref =~ ^[a-zA-Z0-9._:/-]+@sha256:[0-9a-f]{64}$ || $ref =~ ^sha256:[0-9a-f]{64}$ ]] ||
    die 'image must be a pinned repo digest or local immutable image ID (for rehearsals)'
done
[[ $CORE_TO_REVISION =~ ^[0-9a-f]{40}$ ]] || die 'target revision must be a full git SHA'
command -v docker >/dev/null || die 'Docker/Compose is required'
inspect() { docker image inspect -f "$1" "$2"; }
platform=${CORE_PLATFORM:-linux/amd64}
[[ $platform =~ ^linux/(amd64|arm64)$ ]] || die 'unsupported CORE_PLATFORM'
for ref in "$CORE_FROM_IMAGE" "$CORE_TO_IMAGE"; do
  [[ $(inspect '{{.Os}}/{{.Architecture}}' "$ref") == "$platform" ]] ||
    die "image platform does not match $platform"
done
revision=$(inspect '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$CORE_TO_IMAGE")
[[ $revision == "$CORE_TO_REVISION" ]] || die "target revision mismatch: $revision"
mode=$(inspect '{{index .Config.Labels "io.chuanapi.release.mode"}}' "$CORE_TO_IMAGE")
case "$phase" in
  rollback)
    [[ $mode == im13-schema-preserving-rollback ]] || die 'refusing stock im.13: dedicated rollback image required'
    startup_mode=verify-only
    [[ ${CORE_INVOICES_RECONCILED:-} == yes ]] ||
      die 'first fence new invoice intake and reconcile any in-flight invoice fee callbacks/refunds'
    ;;
  upgrade)
    [[ $mode != im13-schema-preserving-rollback ]] || die 'rollback image is not an upgrade target'
    startup_mode=migrate
    ;;
esac
[[ ${CORE_TRAFFIC_FENCED:-} == yes && ${CORE_OTHER_WRITERS_STOPPED:-} == yes ]] ||
    die 'first fence ingress, drain requests and stop all other writers; confirm explicitly'
[[ ${CORE_BACKUP_VERIFIED:-} == yes ]] || die 'verify the disaster-recovery backup before switching'
if [[ ${CORE_EXECUTE:-} == yes ]]; then
  command -v flock >/dev/null || die 'flock is required for an actual switch'
  [[ ! -L $CORE_CUTOVER_PIN_FILE.lock ]] || die 'existing lock is a symlink'
  exec 9>>"$CORE_CUTOVER_PIN_FILE.lock"
  flock -n 9 || die 'another switch is using this pin file'
fi
from_id=$(inspect '{{.Id}}' "$CORE_FROM_IMAGE")
to_id=$(inspect '{{.Id}}' "$CORE_TO_IMAGE")
[[ $from_id != "$to_id" ]] || die 'current and target are the same image'
export NEW_API_IMAGE="$CORE_FROM_IMAGE"
base=(docker compose "$@")
# Expect the specified project and a single current API instance.
current=$("${base[@]}" ps -a -q "$svc")
[[ $current != *$'\n'* ]] || die 'expected at most one NewAPI container in the chosen project'
if [[ -n $current ]]; then
  [[ $(docker inspect -f '{{.Image}}' "$current") == "$from_id" ]] || die 'current image differs from CORE_FROM_IMAGE'
  current_state=$(docker inspect -f '{{.State.Status}}' "$current")
  case "$current_state" in
    running) ;;
    exited|created|dead)
      [[ $phase == rollback && ${CORE_ACCEPT_FAILED_TARGET:-} == yes ]] ||
        die 'source is not running; explicit failed-target recovery is required'
      ;;
    *) die "unexpected container state: $current_state" ;;
  esac
else
  [[ $phase == rollback && ${CORE_EXPECT_NO_API_CONTAINER:-} == yes ]] ||
    die 'no current container; explicit empty-slot rollback recovery is required'
fi
overlay=$(mktemp "${CORE_CUTOVER_PIN_FILE}.tmp.XXXXXX")
trap 'rm -f -- "$overlay"' EXIT
printf 'services:\n  %s:\n    image: "%s"\n    environment:\n      NODE_TYPE: "master"\n      STARTUP_MIGRATIONS: "%s"\n' "$svc" "$CORE_TO_IMAGE" "$startup_mode" > "$overlay"
export NEW_API_IMAGE="$CORE_TO_IMAGE"
next=(docker compose "$@" -f "$overlay")
"${next[@]}" config --quiet
[[ ${CORE_EXECUTE:-} == yes ]] || {
  printf 'Checked %s, from %s to %s. No change. Set CORE_EXECUTE=yes to switch NewAPI.\n' "$phase" "$from_id" "$to_id"
  exit 0
}
# Do not leave the old container eligible to restart against the new schema.
if [[ -n $current ]]; then
  if [[ $current_state == running ]]; then
    "${base[@]}" stop -t 150 "$svc"
    if [[ $(docker inspect -f '{{.State.ExitCode}}' "$current") != 0 ]]; then
      die 'old gateway did not exit cleanly; keep ingress fenced, inspect drain/reporting and reconcile before continuing'
    fi
  fi
  "${base[@]}" rm -f "$svc"
fi
mv -f -- "$overlay" "$CORE_CUTOVER_PIN_FILE"
next=(docker compose "$@" -f "$CORE_CUTOVER_PIN_FILE")
"${next[@]}" up -d --no-deps --force-recreate --pull never "$svc"
running=$("${next[@]}" ps -q "$svc")
[[ -n $running && $running != *$'\n'* ]] || die 'new gateway is not running; leave ingress fenced'
[[ $(docker inspect -f '{{.Image}}' "$running") == "$to_id" ]] || die 'running target image mismatch; leave ingress fenced'
for (( attempt=0; attempt<90; attempt++ )); do
  if "${next[@]}" exec -T "$svc" wget -q -T 2 -O - http://127.0.0.1:3000/api/readyz 2>/dev/null |
      grep -Eq '"success"[[:space:]]*:[[:space:]]*true'; then
    printf 'NewAPI ready: %s (%s). Keep ingress fenced pending SQL, task and proxy checks.\n' "$to_id" "$revision"
    exit 0
  fi
  sleep 2
done
die 'readiness did not pass; keep ingress fenced, inspect target and select the pinned rollback image if appropriate'
