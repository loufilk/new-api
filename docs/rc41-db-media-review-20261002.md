# rc41 database-foundation incident candidate

- Verified cancellation base: `27a20d391044bc169b18bd1e0a8b6adc9668b30d`.
- Database upgrade base: `62d951fab129f4df1a921735835e5e9c7b8db031`.
- Latest maintenance reference reviewed: `ec13ba777f9a0a521a815722525b55929a361996`. Its six commits after the database base touch CI and frontend formatting, not database dependencies, models or Redis.
- rc41 tag: `2035a82aeb5414253a728bd937d4b8f97aa99b9b`, already an ancestor of the verified base.
- Additional upstream fix: `1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5` (Responses custom tools and delayed function names).
- Source version: `v1.0.0-rc.41.chuanapi.20261002.db-media`. Manual branch builds preserve this version and append the immutable commit, instead of replacing it with a branch-only label. Existing image tag naming is unchanged.

## Review of the incident advice

Keep the restored production image running. Do not redeploy the failed candidate as a test. Client cancellation must terminate retry and must not disable accounts or write channel-error table rows; request/termination diagnostics remain available. Real provider 429/503, server deadlines and semantic failures are not globally hidden.

Video validation remains fail-closed: Cline is not a video-native channel. This candidate does **not** silently change a requested DeepSeek model into another model or bypass group, token, pinned-channel or session restrictions. Automatic video routing needs verified, explicit model/channel capabilities; it is not implemented by merely allowing every non-Cline channel. Unsupported video input continues to return a local 400 before an upstream call. Image and tool conversions remain covered by the existing tests.

## Release gate

The assembly job checks ancestry and unchanged database/Redis dependencies and models, then creates one exact merge commit. The same commit is passed by a Git bundle to backend, relaykit and real SQLite/MySQL/PostgreSQL checks. Only successful gates permit a non-forced push to the isolated candidate branch. No production deployment or Docker push is part of that workflow.

Acceptance requires the cancellation/media/accounting regressions, no-error-table cancellation check, source version stamping check, full Go tests and race tests. Existing production request replay is still required before rollout; HTTP 200 totals or a CI badge alone are not evidence of complete SSE delivery.
