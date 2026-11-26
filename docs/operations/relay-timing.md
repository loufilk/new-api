# Sampled HTTP relay stage timing

The gateway emits no `relay_timing` logs by default. Set
`RELAY_TIMING_SAMPLE_PERMILLE=50` in an isolated gateway process to sample
approximately 5% of relays that reach the HTTP controller. Restart that process
to change the value. Use `0` to turn it off. Invalid values also disable it.
No new endpoint, database table, client header, or user ID/destination/model/
payload is added. The regular server log prefix carries the gateway request ID.

## What to measure

- `ingress_to_route_selection_ms` is when the distributor selected the initial
  route, relative to gateway ingress. It includes authentication, rate limiting
  and routing; it is not a separate timing for each middleware. Requests
  rejected by those middleware do not reach the relay controller and are not
  sampled here. Use HTTP response rates alongside these samples.
- `ingress_to_relay_ms` additionally includes binding the selected channel.
  `billing_prepare_ms` measures request estimation, quote and precharge, not
  later settlement. `relay_handler_ms` is the controller's end-to-end duration
  including streaming and its cleanup.
- The first eight HTTP dispatches have independent timings. Connection
  acquisition (`get_connection_ms`) includes dialing for a fresh connection;
  compare reused and fresh samples separately. `written_to_first_header_byte_ms`
  is not model latency: it can include network delay and upstream queuing, and
  can see an interim HTTP response before final headers.
  `dispatch_to_result_ms` ends when the HTTP call returns headers or an error;
  it does not wait for the response body. A transport-level reconnect uses the
  latest connection acquisition. Missing stages are omitted, not reported
  as zero. Direct SDK and upstream WebSocket dispatches do not have these HTTP
  sub-stages.
- `ingress_to_first_upstream_event_ms` is the relay's first recorded upstream
  stream event; it may be a control event rather than model output.
  `ingress_to_first_usable_output_ms` currently covers native Chat's first
  successfully flushed content, reasoning, refusal, audio, or tool delta. It
  is not necessarily visible text or the time the client receives it. For
  customer TTFT, measure first visible text at the test client separately.
- `status` is the HTTP status already set by the controller. An error in an
  opened SSE stream can still have HTTP 200; `relay_api_error` means the relay controller had an API error.
  It does not cover all stream failures/cancellations or errors outside the
  relay controller. Use the load generator's stream outcomes as well.

## Local A/B

Run the same sanitized payload distribution, one synthetic user and a fixed,
loopback fixture provider, holding all other settings constant. Compare the
baseline and candidate at the same sample rate, after the transports are warmed.
Keep sampling modest: output uses the normal synchronous server logger, so 100%
sampling can perturb latency. Collect capacity headline results with timing
disabled as a separate run.
Export all lines containing `relay_timing `, remove the logger prefix and
calculate p50/p95/p99 for fields that are present. Correlate with test-client
TTFT, achieved arrival rate, status/stream failures, DB wait deltas and
Redis pool timeouts. Exclude canary startup, retries or failed dispatches only
when reporting a separately labeled slice; do not silently discard them.
