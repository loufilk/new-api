# Offline lm-detector integration

The `sdk_*.go` files and `templates.json` are adapted from the MIT-licensed
[Ikaleio/lm-detector](https://github.com/Ikaleio/lm-detector/tree/f920cfe2ff0bab94989ce01ac061e21425b6e321/sdk/go)
at immutable commit `f920cfe2ff0bab94989ce01ac061e21425b6e321`.
Copyright (c) 2026 xqy2006. Full license is in `LICENSE.lm-detector`.
Upstream acknowledges xqy2006/ModelTrace for algorithm ideas and some original data.

Only offline challenge generation, numeric parsing, feature extraction, binary
codec, ranking and calibration are included. Transport, request construction,
credentials, online downloads, cache refresh and SDK live detection are omitted.
JSON decoding uses the host's `common.Unmarshal`. Public wrapper adds three-sample
validation, strict completeness checks, input size bounds, top-three projection,
and immutable embedded-data verification.

## Reference provenance

Data comes from that same pinned commit's `data/` directory. It was retrieved
once during development, not at runtime. The trained library contains 66 model
entries and reports build time `2026-11-03T12:06:39.864352+00:00`.

- `reference/detector.bin` is byte-for-byte `data/shared_detector.bin`.
  Git blob SHA-1: `3386798a7465e30a5138d7ef7b412d79e5ef0382`.
  SHA-256: `59dffad8128dca8bcf0cfb024f0fcebe7f0b7d7fbae3583b4b5d1418c18e601f`.
- Upstream full `data/unified_bank.json` Git blob SHA-1:
  `6f71ea7dc1d2fa1dfbafce83a06ba053622d5bac`. SHA-256:
  `5460e6d79e0245aa695d9e63063c767712dee8faec52c9ab1b215aae473454ce`.
- `reference/bank.json` is a lossless projection of fields used by the SDK:
  `built_at` and each model's `id`, `display_name`, `family`, `family_name`,
  `response_count`, preserving upstream model order. Serialized as UTF-8 JSON
  with no whitespace (`ensure_ascii=False`, separators `(',', ':')`).
  SHA-256: `dc478b8eb023ba5aa14718b7633fda3695bb7c9f5485add0c4c02417a953ebc0`.

The loader hashes both embedded files against code-pinned SHA-256 values before
any parsing. It additionally verifies schema, build time, model order and response
counts match the detector. Updating data requires a reviewed source change and
new digests; there is no online or background update mechanism.

Strict mode requires three successfully completed, nontruncated transport
responses, each with at least `max(80, ceil(expected_count * 0.55))` parsed
numbers. It does not require every requested number. A shorter complete
response is reported with `short=true`; provider truncation is a separate flag.
This preserves the pinned SDK's strict threshold semantics.

A result is a closed-set comparison against this reference library, not proof of
identity. Unknown models may rank highly. Partial results have no probability.

## Offline regression samples

`testdata/reference_samples.json` contains only three existing numeric responses,
challenge IDs/counts and model labels projected from the pinned commit's
`data/unified_reference.jsonl`. Full upstream source SHA-256:
`ce430f0de948c3f14da1143c693b4e4d69754169bb2856e766eb223cf5a4bacb`.
The selected rows are the first three responses with expected counts 292–332 and
at least that many in-range values, all labeled `claude-haiku-4.5` upstream.
No live model calls were used to create the fixtures. These are regression
fixtures, not independent validation of the detector's real-world accuracy.
