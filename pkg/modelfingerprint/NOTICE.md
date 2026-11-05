# Local fingerprint service client

This package is only the protocol client and public result types for the separate
`tools/fingerprint-service` module. NewAPI does not compile or embed the detector,
reference data, or upstream SDK. No third-party HTTP API is used at runtime.

`MODEL_FINGERPRINT_SOCKET` selects an absolute Unix-domain socket path; the default
is `/run/new-api/fingerprint.sock`. Requests cannot use TCP, environment proxies,
redirects or an arbitrary URL. Only synthetic numeric probe responses and bounded
execution metadata are sent. Keys, provider addresses, HTTP headers, user history
and failed/intercepted response bodies have no place in the protocol. Provider
error text is normalized rather than sent to the service.

Start the independent service before enabling tests. A missing service, protocol
mismatch or wrong data hash fails `Ready()` before any paid request. Failures are
not cached, so the next request can recover after the service starts. A failure
while scoring does not imply that preceding model calls were free and must not
trigger another paid probe automatically. Existing NewAPI routing, confirmation,
billing and audit code remains responsible for model calls.

See `tools/fingerprint-service/README.md` for build/run instructions, source and
data provenance, offline behavior and service tests. The fixed `SDKCommit` value
identifies compatible scoring core files; the uploaded archive itself contained
no Git metadata, so its complete Git revision is not independently established.

## Offline regression samples

`testdata/reference_samples.json` contains only three public numeric responses,
challenge IDs/counts and model labels projected from the previously pinned
upstream `data/unified_reference.jsonl`. Full upstream source SHA-256:
`ce430f0de948c3f14da1143c693b4e4d69754169bb2856e766eb223cf5a4bacb`.
The selected rows are the first three responses with expected counts 292–332 and
at least that many in-range values, all labeled `claude-haiku-4.5` upstream.
These are regression fixtures, not independent evidence of real-world accuracy.
The original MIT license is retained as `LICENSE.lm-detector`.

The service performs a closed-set ranking, not model identity authentication.
Unknown models can rank highly, and partial results do not carry probabilities.
