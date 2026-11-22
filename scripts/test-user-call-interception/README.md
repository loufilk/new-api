# Real-binary, isolated PostgreSQL acceptance

Prerequisites: a non-root account able to run `initdb`, PostgreSQL tools on PATH,
Go matching `go.mod`, Python 3, curl, and a fresh NewAPI binary. For the real UI
embedding, first install/build `web/` with its Bun lockfile, then run
`go build -o /absolute/path/new-api .` at the repository root.

```sh
UCI_BINARY=/absolute/path/new-api bash scripts/test-user-call-interception/run.sh

# Also prove additive migration from the separately built exact full baseline:
UCI_BINARY=/absolute/path/new-api \
UCI_BASELINE_BINARY=/absolute/path/new-api-at-7dd0cce \
bash scripts/test-user-call-interception/run.sh
```

The baseline binary must be built from
`7dd0cce50bd2374f3fa7586e3a77286b3c287ac9`, not an arbitrary production binary.
It runs the baseline's original migrations but is deliberately given an invalid
listen address so startup terminates **before any baseline HTTP listener opens**.
That expected bind error must be observed. This is migration evidence, not a
claim that the old baseline serves HTTP successfully in this mode.
The runner does not fetch repositories or change any branch. It creates a new
mode-0700 temporary directory and PostgreSQL cluster; it never accepts a supplied
deployment DSN, drops another database, or connects to a paid upstream. It needs
free loopback ports 55883 (PostgreSQL), 35881/35882 (applications) and 35886 (mock).
New application processes use an empty working directory and clean environment,
with `HTTP_BIND_ADDRESS=127.0.0.1` (the new optional server setting; leaving it
unset preserves the existing wildcard default). Do not substitute a binary that
predates this setting. No repository `.env` is loaded by the runner's applications.
`HTTP_BIND_ADDRESS` accepts a host/IP only (for example `127.0.0.1` or `::1`),
not a port or URL. An invalid resulting address fails startup without serving
HTTP. `PORT` and the existing `--port` fallback otherwise retain their behavior.
All fixture identities, keys, passwords, TOTP secrets and signatures are locally
generated synthetic values. Credentials are not printed; the temporary fixture
JSON is removed on exit. The stopped disposable cluster still contains synthetic
credentials and should be removed when its evidence is no longer needed.

The runner prints its evidence directory and retains startup logs, schema and
data hashes, and a machine-readable `http-results.json`. Success requires:

- Fresh/repeated migration, or baseline upgrade with seeded data, all agent-table
  hashes and existing indexes unchanged; persisted policy/hit/audit survive another
  startup after actual API saves.
- Actual password/TOTP session login, independent profile updates, admin-only
  policy updates and revision-conflict handling.
- Both processes observe enable/edit/disable without restart or policy cache
  invalidation; old, second and newly created valid API keys share the policy.
- Zero/negative user balance and an enabled zero-budget key still get the reply;
  disabled/expired/exhausted/invalid keys, model/IP/group denial and real account
  bans do not become successful assistant responses; forged IDs have no effect.
- Chat, Completions, Responses, Claude, Gemini and both Playground entries produce
  valid local ordinary/SSE terminal replies, with zero usage. A Responses socket
  opened before enablement rejects its next create. Unsupported and media routes
  reject before any upstream call.
- Upstream counter is exactly zero during interception; user/token/channel
  financial values, consumed logs, ledgers and task counts are unchanged.
- Independent hit/audit metadata and identities are queryable without text or
  credentials; forced policy/hit-table failures return 503 without upstream.
- Another user remains unaffected, and disabling restores a normal request to
  the local mock (two deliberately non-intercepted mock calls in total).

The script intentionally does not use Redis or a paid upstream, exercise every
database dialect, load-test, validate a real browser visually, cancel already
running upstream work, or modify/deploy production. Run its focused tests rather
than broad existing database suites when your authorization is PostgreSQL-only.
