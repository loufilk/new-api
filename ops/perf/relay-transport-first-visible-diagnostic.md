# Isolated relay transport and first visible text comparison

This offline comparator is for the existing sampled HTTP relay evidence on
baseline 0a10b9565c789e0554db5c838b1c73fcaab1b932. It adds no service hook,
configuration, authentication, database read, listener, request, or load. Do
not point a test at a paid upstream or use this output as capacity, image, or
deployment qualification.

## Produce normalized NDJSON on the isolated runner

Use one sanitized fixture payload distribution, stable case ordinals, the same
sampling policy, and deliberately separate direct gateway and local Caddy paths.
Keep each request outcome, including cancellations or no text. If matching the
gateway log to the client trace, privately join by the known fixture request ID
and discard the ID and logger prefix before saving the normalized row. Do not
send raw log lines, SSE bodies, payloads, request/client IDs, channel names,
hosts, URLs, credentials or arbitrary strings to this tool. It rejects
additional fields at every level and does not echo invalid input.

A row is one object whose required fields are:

- `path`: `direct` or `caddy`.
- `case`: generated integer fixture ordinal (one row per path and ordinal).
  Across baseline and candidate it must denote the same fixture case, not a
  real request ID.
- `client.outcome`: `complete_text`, `no_text`, `stream_error`, `http_error`,
  `canceled`, or `transport_error`. Integer client `status`, `first_header_ms`,
  `first_visible_text_ms` and `duration_ms` are otherwise optional; omit
  unavailable values, rather than writing null or zero. A complete 2xx stream
  needs status, header and end-of-stream duration. A complete stream with
  visible text also needs the corresponding first text timestamp. Measure all
  client values from the same monotonic client request start.
- Optional `relay`: the exact numeric/boolean object after the existing
  `relay_timing ` marker (without any prefix), on the pinned source. Omit relay
  evidence if it was not sampled or never reached that controller.

Minimal example, all values synthetic:

    {"path":"caddy","case":1,"client":{"outcome":"complete_text","status":200,"first_header_ms":7,"first_visible_text_ms":51,"duration_ms":120},"relay":{"status":200,"relay_api_error":false,"upstream_attempts":1,"ingress_to_first_upstream_event_ms":11,"ingress_to_first_usable_output_ms":24,"upstreams":[{"protocol_major":2,"status":200,"connection_reused":true,"get_connection_ms":0,"written_to_first_header_byte_ms":4}]}}

Categorize **first visible text** with an endpoint-aware SSE decoder at the
client: for Chat, a non-empty text content delta; for Responses, a non-empty
`response.output_text.delta`; for Gemini, a non-thought text part. Never count
heartbeats, role/control frames, reasoning/thought, tool arguments, usage,
finish markers, response headers, or a successful HTTP status as text. The
client collector or existing Caddy latch is responsible for decoding and
checking the stream; this program never reads or retains its contents. A
stream error after text should still be reported as `stream_error`, not
`complete_text`. Keep unsampled/no HTTP trace cases in the cohort.

Run:

    python3 -B ops/perf/relay_transport_compare.py --baseline baseline.ndjson --candidate candidate.ndjson
    python3 -B ops/perf/test_relay_transport_compare.py

## Interpretation

Transport distributions use exactly one successful HTTP dispatch and a 2xx
gateway result without a relay API error, split by negotiated protocol and
fresh/reused/unknown acquisition. The report also counts excluded retries,
errors, missing dispatches, and the eight-attempt trace cap. Connection reuse
describes the last acquisition for that one dispatch; a transparent transport
retry can still occur. Header byte timing can include HTTP interim headers.
Successful HTTP headers do not prove the SSE stream completed.

Gateway event/usable-flush distributions remain separate from client timing.
Gateway first usable flush is native Chat and includes reasoning, refusal,
audio and tool deltas; it is **not** first visible client text. Paired client
deltas are candidate minus baseline on identical path and case and only when
both completed with text. The signed delta is conditional on those surviving
cases and can still exhibit survivor bias. Always read the complete, failed,
partially matched, and unmatched counts alongside it. The p50/p95/p99 use
nearest rank and show their sample size; a small check is a semantic diagnostic,
not a tail-latency claim. Do not use
different clock origins to subtract out proxy or upstream time.

Baseline Caddy source excludes stream routes from gzip and uses
`flush_interval -1`. A real client-facing Caddy latch, and a comparison that
keeps gateway, fixture provider, payload, and concurrency fixed, are distinct
evidence; the baseline configuration or this offline analyzer alone does not
prove proxy behavior or identify a production bottleneck.
