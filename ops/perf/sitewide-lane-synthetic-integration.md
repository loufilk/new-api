# Sitewide lane integration: synthetic development stage

This branch joins the paid treasury, canonical request owner, original finite
Key enrollment, one-way HTTP dispatch, and strict usage settlement. It is a
development stage toward a default sitewide 32-shard / 96-admission /
8-per-shard release. It does not install, enable or size such an engine.

The explicit `lane_billing_integration_dev` fixture uses a fresh namespaced
PostgreSQL 18.6 schema and one loopback synthetic upstream. It grants paid and
original finite-Key quota through the actual cold transfer APIs, prices
through normal `ModelPriceHelper`, and sends the exact prepared bytes through
normal `DoApiRequest`. It then exercises the owned branch of
`PostTextConsumeQuota`. Strict terminal usage produces a sanitized projection
receipt with a ready bit committed atomically with dual spend. Missing terminal
proof preserves both durable holds and produces no projection. In both cases
the same dispatch cannot be sent twice, ordinary history is preserved, and
only idle funds are returned.

The fixture does not install a public controller or upstream adapter. Its
closed-world verifier and strict synthetic SSE envelope are not provider
attestations; the ready bit is not consumption logs or a working projector.
The database installer is explicitly dev-only. The ordinary application has
no new lane caller, schema migration, or automatic enrollment.

Remaining gates before enabling a production default:

- Resolve eligibility before either billing path owns the request. Verify
  paid-only funding using current wallet reward/promo/subscription policy
  under authoritative locks; keep unsupported requests on ordinary billing
  with coherent logical spendable funds.
- Bind to final provider bytes and a bounded real tokenizer policy, with
  strict terminal usage provenance and one-attempt transport evidence for
  each supported route. Prove an explicit no-ordinary-fallback rule after
  binding or uncertain dispatch, including process loss and mixed versions.
- Recover durable observed/unknown deliveries, consume exactly one projection,
  conserve wallet and Key balances including held/spent amounts in ordinary
  reads, and safely fence, reconcile and unwind existing generations.
- Implement and exercise the 32 / 96 / 8 admission scheduler only after
  those safety gates. Do not infer throughput or deployment readiness from
  the synthetic test.

Run the tagged synthetic test with `TEST_LANE_INTEGRATION_POSTGRES_DSN` set to
an isolated PostgreSQL 18.6 loopback database named `owner_treasury_child_test`:

```sh
go test -tags lane_billing_integration_dev ./relay/channel -run '^TestLaneNativeChatSyntheticNormalTransportPostgres$' -count=1 -v
```
