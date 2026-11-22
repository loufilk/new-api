# Funding Factory and Transaction Capability Contract

## Goal

`BillingSession` remains generic orchestration while concrete Wallet funding
moves to a module. Therefore `NewBillingSession` must stop constructing
`WalletFunding` directly before extraction begins.

Target direction:

```text
Billing orchestrator -> FundingFactory/FundingSource ports <- Wallet module
                                  ^
                                  |
                         composition registration
```

## FundingFactory

The future factory takes a narrow, host-independent request DTO. It must not take
`*gin.Context`, `*relaycommon.RelayInfo`, root model structs, controller/service
types, or an arbitrary `map[string]any` that recreates those dependencies
implicitly.

Candidate request facts are limited to fields proven necessary by current
construction call sites, such as user ID, request ID, model/billing identity,
estimated quota and explicit funding policy metadata. Token fields are included
only if factory construction itself requires them; token mutation remains billing
orchestration responsibility unless a later contract deliberately changes that.

The exact DTO is frozen only after call-site inventory in Phase 3. Adding the
factory is not permission to copy all of `RelayInfo` into a new struct.

Phase 4 implementation freezes the DTO to `UserID`, `RequestID`, `BillingModel`,
`Amount`, and an ordered list of account identifiers. The registry is explicit,
write-once at the host boundary, and frozen before request handling. `NewBillingSession`
no longer constructs a concrete Wallet or Subscription funding struct directly.

## Base funding capability

The base runtime port will own `Source`, `PreConsume`, `Settle` and `Refund`, with
existing optional capabilities for Reserve, outstanding reserve and receipts.
The current `pkg/moneyport` package remains a stdlib-only leaf unless a separate
architecture decision changes that rule.

## Transactional settlement capability

FundingFactory alone is insufficient. Current wallet atomicity depends on a
service-private capability:

```text
settleTx(*gorm.DB, delta) -> *model.LedgerResult
applySettleResult(delta, *model.LedgerResult)
```

That shape is intentionally **not** the target because its private methods and
root model result prevent an external Wallet package from satisfying it cleanly.

The extraction prerequisite is:

1. define a public structural transaction capability whose method signatures use
   only framework/infrastructure types and a host-independent result DTO;
2. keep `pkg/moneyport` free of root `model/service/controller/relay` imports;
3. let `service.BillingSession` own transaction orchestration and type-assert the
   public capability;
4. let the Wallet module satisfy the capability structurally without importing
   `service`;
5. update in-memory receipt/cache state only after the transaction commits;
6. preserve replay behavior: a replayed wallet operation must not reapply token
   quota.

A practical target is an exported service-side structural interface referencing
`*gorm.DB` plus a small DTO defined in a generic port package. Go structural
typing means the Wallet module need not import the service package to implement
that interface. An equivalent adapter design is acceptable if it preserves the
same dependency direction and atomicity tests.

Implemented Phase 4 shape:

```text
SettleInTx(*gorm.DB, delta) -> moneyport.SettlementResult
ApplySettlementResult(delta, moneyport.SettlementResult)
```

`SettlementResult` contains only replay state, post-commit total balance, and
account-share DTOs. It has no root `model` dependency. The structural interface
itself remains service-side because it references GORM; the concrete module can
satisfy it without importing `service`.

## Composition ownership

The application composition root registers concrete factories. Core business
packages consume only the factory/port registry. Final `chuanapi/register.go`
should call module registration functions rather than own Wallet models,
migrations or handlers itself.
