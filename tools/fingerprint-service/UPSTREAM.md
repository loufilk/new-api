# SDK and reference provenance

- SDK project: https://github.com/Ikaleio/lm-detector
- SDK documentation: https://lm.ikale.io/docs/en/sdk
- Supplied source: the user's `sdk.rar`, extracted Go SDK directory.
- Uploaded archive SHA-256:
  `0b6a63df387b62b7606ff68a70142f745b7e0aea140186e56f3fd692aaa947ba`.
- Local compatibility snapshot identifier (`sdk_commit`):
  `f920cfe2ff0bab94989ce01ac061e21425b6e321`.

The archive contains no Git metadata. Eight core SDK files matched the previously
cached source snapshot at the identifier above when trailing whitespace was
ignored. This is a local source snapshot, **not a verified tagged SDK release**.
The identifier documents the compatibility baseline; it is not a claim that the
uploaded archive was authenticated by Git or by an upstream release signature.
All files supplied in the Go SDK directory, including its `go.mod`, `go.sum` and
README, are preserved byte-for-byte in `third_party/lmfpd`. The separately copied
MIT license is in `third_party/lmfpd/LICENSE` (copyright 2026 xqy2006), from the
existing licensed lm-detector snapshot in New API. `SDK_SHA256SUMS` records the
uploaded Go files' exact bytes; the added license is not part of that manifest.

The service's local `replace` directive avoids an SDK fetch. It calls the SDK's
single-local-directory `Load` branch, never its URL/cache branch or model request
functions. SDK code for those other features remains in the unchanged third-party
source but is not used by this service.

The reference pair and saved numeric scoring fixture were copied from the
previous pinned New API fingerprint implementation:

- `data/unified_bank.json`:
  `dc478b8eb023ba5aa14718b7633fda3695bb7c9f5485add0c4c02417a953ebc0`
- `data/shared_detector.bin`:
  `59dffad8128dca8bcf0cfb024f0fcebe7f0b7d7fbae3583b4b5d1418c18e601f`
- `testdata/reference_samples.json`: three saved numeric reference replies whose
  expected leading model is `claude-haiku-4.5`; no live request is needed.

These hardcoded digests check the selected local bytes, independently of any hash
reported by the bank itself. The SDK additionally verifies the bank build,
model IDs and response counts against the detector before scoring.
