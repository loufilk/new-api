# Core Attribution Closeout

This report closes the classification lane of Modularization Plan v2.1. Its
purpose is to distinguish successful module isolation from the separate amount
of customization that still lives on upstream-owned NewAPI surfaces.

## Measurement point

- Implementation head measured: `7a9ae7025aae51b6767f0dfe3681233a0477155a`
- Official comparison base: `bdef117505247769268b209665fb3ad7554c3da7`
- Logical changed files at that point: 707
- Default `UPSTREAM_CORE` / unclassified rows after the final rule set: **0**

The absence of unclassified rows is an attribution result, not a claim that the
core fork is small.

## Ownership distribution

| Ownership | Files | Production files |
| --- | ---: | ---: |
| ChuanAPI module | 118 | 105 |
| Generic module | 58 | 27 |
| Generic extension kernel | 31 | 16 |
| Composition root | 6 | 3 |
| Generic core enhancement | 93 | 85 |
| Product-core customization | 248 | 247 |
| Data compatibility | 12 | 10 |
| Test / documentation compatibility | 125 | 1 |
| Ops / deployment | 16 | 7 |

Production files that still belong to an upstream-owned core surface are
counted separately as:

```text
generic core enhancement       85
product-core customization    247
data compatibility             10
---------------------------------
core production surface       342
```

Therefore the earlier aspirational `<120–150` core-file target is **not met**
by this refactor. The dominant residual is the intentionally customized frontend
product surface, not concrete ChuanAPI module imports. Future upstream-conflict
reduction work should treat this 342-file number as an independent backlog.

## What was actually isolated

The current rules find **zero** ChuanAPI-module-owned paths outside
`modules/chuanapi/` and `web/src/extensions/chuanapi/`. Runtime ownership for
Wallet, Affiliate maintenance/policy, Waffo/Waffo-Pancake handlers and SDK
integration, public console/help, and the customized Wallet/payment-settings UI
is now module/extension-owned.

The generic host consumes those capabilities through extension/port boundaries;
this is separate from the remaining product-core changes above.

## Deferred data compatibility

The following host files deliberately remain compatibility boundaries rather
than being physically moved during this refactor:

- `model/balance_ledger.go`
- `model/model_bonus_eligible.go`
- `model/model_bonus_eligible_admin.go`
- `model/affiliate.go`
- `model/affiliate_reconcile.go`
- `model/option.go`
- `model/topup.go`
- `model/subscription.go`
- `web/src/features/system-settings/types.ts`
- `web/src/features/system-settings/billing/index.tsx`

Associated tests are classified with the same data boundary. The backend list
exists because current ledgers and provider records still mutate/read host
`users.quota*`, TopUp, Subscription and shared OptionMap schema. The frontend
pair retains the persisted option DTO/default shape while concrete Wallet and
Waffo/Pancake settings UI is extension-owned.

Splitting these schemas is a separate data migration and is intentionally not
mixed into the runtime modularization change.

## Generic upstream-review lane

Generic backend changes remain visible as `GENERIC_CORE_ENHANCEMENT`. The rule
set marks reusable billing, security, relay, task, payment-core and persistence
changes for upstream review rather than pretending they are ChuanAPI modules.
At this measurement point 80 production files are explicitly marked
`UPSTREAM_REVIEW` or `UPSTREAM_CANDIDATE`.

## Interpretation

The modularization goal achieved by this phase is dependency ownership:

```text
NewAPI core -> generic extension / port contracts <- concrete modules
```

It does **not** mean the entire customized product is now a thin 100-file fork.
The remaining product-core surface must continue to be tracked whenever upstream
NewAPI is updated.
