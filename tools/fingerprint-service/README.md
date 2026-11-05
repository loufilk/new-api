# Independent model fingerprint service

This is an optional, independently built process for New API. The New API
application does **not** import the scoring SDK or embed the reference data. It
uses a thin Unix-socket client. This service generates the three numeric
challenges and scores numeric replies with the supplied official Go SDK.
New API retains upstream routing, group selection and billing. API keys,
provider endpoints, request headers and user conversations must never be sent to
this service.

There is no TCP listener, public HTTP API, model request, download, refresh or
SDK cache. The only SDK operations used are local `Load`, `GenerateChallenges`,
`ParseNumbers` and `Analyze`. The upstream source is copied unchanged under
`third_party/lmfpd` and used by a local module replacement. Its generic README
also documents online SDK features; those features are not used here.
See [UPSTREAM.md](UPSTREAM.md) for the snapshot and data provenance.

## Build and test offline

Use Go 1.27.3 and a dependency cache already containing the modules listed in
`go.mod` and `go.sum`. An offline build deliberately fails if they are missing;
it must not automatically fetch a different SDK or reference bank.

From this directory:

```sh
GOPROXY=off GOSUMDB=off GOWORK=off go test -race ./...
GOPROXY=off GOSUMDB=off GOWORK=off go vet ./...
GOPROXY=off GOSUMDB=off GOWORK=off go build -o bin/fingerprint-service .
```

The tests use the checked-in numeric reference fixture and a temporary private
Unix socket. They do not call a live model or incur model charges. An environment
that prohibits binding Unix sockets needs permission to run the socket tests;
no TCP fallback is provided.

The independently selected `github.com/klauspost/compress v1.18.7` is already
cached in the development environment and satisfies the SDK's `v1.18.4` minimum
through Go module version selection. The SDK's own module files are unchanged.

## Start the service before enabling probes

The defaults are:

- `-socket /run/new-api/fingerprint.sock`
- `-data-dir ./data`, relative to the service's working directory

Use explicit absolute paths in a process supervisor. The socket parent directory
must already exist, be owned by the service UID and grant no permissions to group
or other users (normally mode `0700`). The service creates the socket with mode
`0600`. Run New API under the **same UID**. Do not make the socket world-readable
or expose it through a reverse proxy. For a typical installation, an operator
creates `/run/new-api` with owner `newapi` and mode `0700` and supplies the
appropriate runtime-directory setting in their process supervisor.

Example after building, using a private directory owned by your current user:

```sh
install -d -m 0700 "$HOME/.local/run/new-api"
./bin/fingerprint-service \
  -socket "$HOME/.local/run/new-api/fingerprint.sock" \
  -data-dir "$PWD/data"
```

In the separately started New API process, configure the matching path:

```sh
export MODEL_FINGERPRINT_SOCKET="$HOME/.local/run/new-api/fingerprint.sock"
```

With the default service socket, set
`MODEL_FINGERPRINT_SOCKET=/run/new-api/fingerprint.sock` instead. A missing,
unready or incompatible service makes readiness fail before paid probes start.
Only a successfully verified and loaded bank is reported as ready.

The service refuses to start if the socket path already exists, including a stale
socket. It never deletes an existing path to take over. After an unclean stop,
an operator must verify that no process still uses the path before removing the
stale socket. Normal shutdown removes only the socket created by that process.
No deployment or process-supervisor changes are made by this module.

## Protocol v1

HTTP is carried only over the Unix socket:

- `GET /v1/ready`: `protocol_version`, `sdk_commit`, `data_version` (bank
  `built_at`) and `data_sha256` (the pinned detector digest).
- `GET /v1/challenges`: `{ "challenges": [...] }`, always exactly three distinct
  challenges with `id`, `prompt` and `expected_count` (292–332).
- `POST /v1/analyze`: `Content-Type: application/json` and
  `{ "samples": [...], "strict": true }`. Exactly three samples are required.
  A sample has `challenge_id`, `response`, `expected_count`, optional `error`,
  `non_model` and `truncated`. New API sends only fixed public execution errors
  such as `request_failed`, never provider error bodies.

Responses retain New API's result shape: `status`, `reason`, `top_candidates`,
`sample_quality`, `data_version`, `data_sha256`, `sdk_commit`, `probability_note`,
`probability_status`, `used_outputs` and `strict`. At most three ranked candidates
are returned. Candidate fields are `model`, `display_name`, `family`,
`family_name`, `score` and nullable `probability`. Quality fields contain only
bounded challenge metadata, counts and fixed status reasons; no sample text or
upstream error content is returned or logged.

Strict mode requires three sufficiently long, nontruncated samples. The SDK's
minimum length is `max(80, ceil(expected_count * 0.55))`; `short` separately
indicates fewer numbers than requested. A partial ranking has no probabilities.
A calibrated probability ranks candidates only within the pinned reference bank
and is not proof of a model's identity. Models absent from that bank may still
rank highly.

Unknown JSON fields, unsupported content encodings, extra JSON values and query
parameters are rejected. Each numeric response is capped at 64 KiB, request
bodies at 256 KiB, response JSON at 512 KiB, error markers at 128 bytes, and
challenge IDs at 128 ASCII letters/digits or `_.:-`. Scoring is limited to four
concurrent operations. HTTP header/read/write/idle deadlines are bounded. Error
responses are fixed messages and contain no raw input. The service does not keep
an access log.

## Reference data lifecycle

`data/unified_bank.json` and `data/shared_detector.bin` are required regular
files; symlinks and special files are rejected. Both are checked against pinned
SHA-256 digests before the SDK sees them. Loading uses a private, temporary copy
of the verified bytes to avoid a check/load race, then deletes that temporary
copy. The loaded detector remains immutable in memory. The service does not
reload data in the background.

If either file is missing, oversized or corrupted, startup fails before opening
a listener. Restore the exact pinned files and restart. A deliberate data update
requires reviewing the new bank/detector pair and updating both pins and the
compatible client metadata together. There is no automatic fallback to SDK
online sources, stale cache or caller-supplied digests.

## Verification recorded for this change

On 2026-11-05 with Go 1.27.3, the offline SDK, reference-integrity and in-memory
HTTP protocol suite passed with the race detector:

```sh
GOPROXY=off GOSUMDB=off GOWORK=off go test -race -count=1 -v \
  -run 'Test(Official|Partial|Threshold|NonModel|Invalid|Quality|Generate|Load|Pinned|Reference|HTTP)' ./...
```

Result: PASS (`11.362s` for the complete selected suite, including repeated data
loads). The real reference fixture ranked `claude-haiku-4.5` first. The same run
covered partial/strict/truncated samples, invalid input, safe errors, data
tampering, no network/cache fallback and HTTP request limits. `go vet ./...`,
`go build` and `sha256sum -c SDK_SHA256SUMS` also passed.

The complete `go test -race ./...` was attempted, including one normal permission
request. This execution environment still prohibited Unix socket creation. The
three socket-dependent tests could not pass here: real Unix-socket HTTP exchange,
existing-socket preservation, and socket cleanup/replacement. They remain in the
suite and must run on a host that permits local Unix sockets. This is **not** a
claim that transport integration or deployment was verified. No live-model calls,
push or deployment were performed.
