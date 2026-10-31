# 用户级调用拦截与自定义回复 / User-level call interception and custom reply

An administrator can independently enable a local reply for one authenticated
user. It is **not an account ban**, a login restriction, a balance adjustment, or
a token edit. No user is enabled by migration. Configuration has no environment
variable, user-ID constant, provider domain, or built-in notice text.

## Administration

Open **Users → Edit user → User-level call interception and custom reply**.
The section has its own save/confirmation; saving profile fields does not save
the policy. The reply is plain multiline Unicode text (maximum 16,000 UTF-8
bytes). Enabling requires visible content; NUL and invalid UTF-8 are rejected.
Disabling may retain a draft. A stale revision returns 409, preserving the
administrator's unsaved draft until an explicit reload.

Routes under `/api/user/:id/call-interception`:

| Method / suffix | Purpose | Permission |
| --- | --- | --- |
| GET | Read configuration (missing row: disabled, revision 0) | `user_call_interception:read` |
| PUT | Save `{enabled, reply, expected_revision}` | `user_call_interception:update` |
| GET `/hits` | Paginated independent hit log | `user_call_interception:read` |
| GET `/audits` | Paginated configuration audit | `user_call_interception:read` |

Existing administrator authentication, permission checks and role hierarchy
apply. The controller rechecks the persisted actor; the save transaction locks
and rechecks actor and target. Non-administrators cannot use these endpoints,
even when targeting themselves. The permissions also provide matching scoped
administrator PAT access through the existing permission-route registry; this
does **not** enable PATs for Playground or agent-platform business APIs.
The latter remain browser-session-only, including legacy PAT rejection and
existing transaction MFA. Personal-profile updates use an explicit field
allowlist and cannot change this separate policy.

Lists return `data: {items, total, page, page_size}`. Page size is capped at 100.
Common filters: `request_id`, `start_time`, `end_time` (Unix seconds).
Hits additionally support `token_id`, `actor_user_id`, `identity_kind`, `protocol`,
`status`; audits support `actor_id`. `p` or `page` selects the page.

## Request boundary and protocols

`TokenAuth` separates **identity validity** from **remaining quota**. It still
validates the real server-side token/user, token status and expiry, IP limits,
account status and group/channel permissions before a successful local reply.
Model limits and Playground group authorization are also retained. Client user
IDs, headers and flags cannot select or suppress a policy.

The policy is checked before token balance, model request quota, distribution,
channel selection, precharge, settlement or an upstream request. A valid,
enabled key with zero remaining budget and a user with zero/negative balance
can receive the local reply. A key already disabled, expired or marked exhausted
does not become valid. All effective keys, including keys created later, resolve
the same user policy. True disabled accounts still fail authentication.

| Entry | Local behavior when enabled |
| --- | --- |
| `POST /v1/chat/completions`, `/v1/completions` | Ordinary assistant/text response or SSE ending in `[DONE]` |
| `POST /v1/responses` | Completed assistant `output_text`; SSE ends in `response.completed`, consistent item IDs and sequence numbers |
| `POST /v1/messages` | Claude message; SSE ends with `message_delta` (`end_turn`) and `message_stop` |
| `/v1/models/*:generateContent`, `/v1beta/models/*:generateContent` | Gemini model text, `STOP` |
| Corresponding `*:streamGenerateContent` | SSE for `alt=sse`; JSON response array otherwise |
| `/pg/chat/completions`, `/api/user/playground/chat` | Session-authenticated Chat ordinary/SSE; PATs still rejected |
| Images, audio, embeddings, rerank, compact/search/tools, models, tasks, video, MJ, new WebSocket handshakes | HTTP 403, fixed `user_call_intercepted` error; no upstream |
| Dashboard/signed task artifacts, video content, legacy public MJ image | Check database resource owner before remote content fetch; fixed 403, no custom reply disclosed to anonymous viewers |

All existing model-key relay groups (including dynamically registered task
plugin native/host routes) use `TokenAuth`, so unrecognized future routes default
to rejection rather than accidentally reaching an upstream. New session-only
model routes must install `UserCallInterception()` **after authentication and
before** distribution/billing. Resource proxies reuse `InterceptUserArtifact`.
The four legacy `/dashboard/billing/{subscription,usage}` and
`/v1/dashboard/billing/{subscription,usage}` introspection routes are deliberately
excluded: these are account reads, not model invocations. Management, login and
agent business APIs are not transformed into chat replies.

All local successful replies explicitly report zero usage. They do not create
consume logs, balance ledger entries, tasks or upstream requests. Reply strings
are JSON encoded (including SSE data), not interpolated as event framing.

### Long-lived requests

Already-open Responses WebSockets re-run authentication and the policy for each
new `response.create`; a blocked message produces the fixed error event rather
than a fake successful response. Its hit records internal `POST /v1/responses`,
`protocol=websocket`, selected status 403, not the earlier handshake's 101.
An already-admitted create/HTTP stream, an already-established realtime stream,
or a previously accepted background task is **not retroactively cancelled**;
existing polling and settlement may continue. This feature is not a revocation
mechanism for work already in flight.

## Persistence, concurrency and failure

The existing migration registry adds three primary-database tables:
`user_call_interceptions`, `user_call_interception_hits`, and
`user_call_interception_audits`. There is no `User` field or policy cache.
Configuration is read directly from the primary database on every authenticated
call. A lookup beginning after a successful save commits sees that revision on
any application node. A request already past its lookup keeps its snapshot.
Do not route these reads to a lagging replica. Existing account/key caches and
authorization refresh windows remain unchanged; this guarantee concerns the
new policy, not all pre-existing authentication caches.

Saving configuration and its minimal audit is atomic; `expected_revision`
prevents overwriting another administrator's edit. An audit write failure rolls
back the configuration. Hits are written synchronously **before** any local
response bytes. A policy lookup failure, invalid enabled reply on the text-call
path, or hit write failure returns fixed 503
`user_call_interception_unavailable`, never upstream. Artifact routes always
reject an enabled policy without disclosing its text.
If the database cannot persist the hit, the failed hit cannot be recovered from
that same table; a non-sensitive system error signals the failure. A migration
must therefore complete before serving traffic on an upgraded node.

Hits/audits are separate from `LOG_DB` and consumption statistics. Hit indexes
support user/time/ID and request-ID lookups. They contain time, policy user ID,
actual authenticated actor ID, identity kind, key **ID**, server request ID,
route template, protocol, selected status and revision. They never contain key
material, authentication tokens, query strings, request body or custom reply.
Configuration audit records actor, target, revision, enabled transition and
whether text changed, not the text or its hash. Database diagnostics in this
path suppress interpolated SQL values.

Identity kinds are `api_key`, `dashboard_session`, `artifact_capability`, and
`public_artifact`. Anonymous/capability access has actor/key zero; a dashboard
administrator viewing someone else's artifact retains their real actor ID.
**Resource ownership is not proof that the owner personally made a request.**
Hits record interception decisions, not guaranteed delivery of an entire stream
or a complete access log. Invalid credentials and earlier authorization/body
failures may have no hit; absence of hits does not prove absence of attempts.

Each intercepted request incurs primary-DB read/write work. Existing ingress
limits apply, but model request quota is deliberately not consumed. Assess
database capacity and establish an authorized retention/archival policy; no
automatic deletion or new retention schedule is installed by this feature.

## Reproducible verification

Focused model/controller/router tests accompany the implementation. Use a
disposable **PostgreSQL** database and their documented test DSN variables; do
not run broad database fixtures against a deployment. The standalone runner is
`scripts/test-user-call-interception/run.sh` (see its README): two real application
processes, password/TOTP sessions, a local mock upstream, ordinary/SSE/WS calls,
financial snapshots, fresh/repeat migrations and optional baseline upgrade.

Validation of this feature used PostgreSQL 18.6 and the exact full integration
baseline `7dd0cce50bd2374f3fa7586e3a77286b3c287ac9`. This is a source baseline,
not a claim about a deployment's current revision. MySQL/SQLite and minimum
supported database versions require a separate compatibility run. Frontend
DOM tests, type checking and production build do not constitute visual browser
acceptance or a real-client compatibility matrix.

Authentication review references: [OWASP ASVS 5.0.0](https://asvs.owasp.org/),
[Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html),
[Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html),
and [Mass Assignment](https://cheatsheetseries.owasp.org/cheatsheets/Mass_Assignment_Cheat_Sheet.html).
This is not a claim of full ASVS certification.

## Coexistence and later cutover

Existing temporary notification routing is not changed by installing this
code. Under a separately authorized deployment plan: verify the actual running
baseline/routes, back up the database/routing, migrate all intended nodes, copy
the approved notice through the administrator API, and verify the native origin
using the same real authenticated identity (including zero balance and SSE).
Check native hit records, zero upstream/billing effects, other users, and all
route aliases **before** removing the temporary routing. Keep its configuration
available until the observation period completes.

If native verification fails, leave or restore the temporary route first. Do
not disable the only effective interception while rolling application code
back. The additive tables may remain during an application rollback; retain
audit evidence rather than dropping tables. A rollback to a binary without the
feature requires the temporary enforcement path to remain active. No deployment,
automatic user configuration, temporary-route removal or production migration is
performed by the test runner or feature defaults.
