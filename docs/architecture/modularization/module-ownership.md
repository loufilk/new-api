# Module Ownership Contract

## Ownership is behavioral, not a directory rename

Moving a file under `modules/` is not considered extraction if the new package
still imports root business implementations or root business packages import it
directly. Extraction succeeds only when the dependency direction follows the
port/composition rules.

## DB model ownership

- NewAPI-owned tables remain in root `model/`: `User`, `Token`, `Channel`,
  `Subscription` and other host identity/runtime records.
- ChuanAPI-owned tables move with their domain module. Initial Wallet candidates
  include balance-ledger operation/rows and bonus-policy audit records. Affiliate
  reward/reconcile records belong to the Affiliate module.
- Cross-boundary relations use stable IDs and narrow contracts rather than
  module models embedding or importing root services/controllers.

The first Wallet extraction does **not** move `users.quota_paid` or
`users.quota_bonus` out of the `users` table. Code ownership is separated first
through a `UserBalanceStore`-style port/adapter. A later, independent migration
may evaluate dedicated wallet-account tables after SQLite/MySQL/PostgreSQL
compatibility is proven.

Phase 5 completes **runtime funding ownership**, not the later physical data
ownership split. `modules/chuanapi/wallet` owns the concrete Funding implementation
and factory registration, while `model/balance_ledger.go` and the ledger GORM
structs remain a compatibility boundary because they still atomically update the
root-owned `users.quota*` columns. Moving that engine is deferred until a dedicated
ledger-store/UserBalanceStore boundary can be proven across all three databases;
this transitional state must not be reported as full DB-model extraction.

## Migration ownership

Each module owns the implementation of its schema/data migrations. The host owns
phase ordering and supplies the exact `*gorm.DB` being migrated. Module migration
callbacks must use that supplied handle and must not reach for package-global
`model.DB`.

`runLegacyQuotaBackfill(db *gorm.DB)` already demonstrates the desired database
handle boundary; its implementation should move into the Wallet module without
changing the host-controlled migration phase.

## HTTP handler and route ownership

A ChuanAPI module should own handlers for ChuanAPI-owned routes and register them
through a generic route contribution. A module must not import root
`controller` merely to reuse a concrete handler. If a route truly represents a
host capability, composition may inject a narrow handler capability instead.

The current `/llms.txt` registration in `chuanapi/register.go` is therefore
temporary: either the ChuanAPI console/help module owns the handlers, or a future
host-handler capability is explicitly injected. `chuanapi/register.go` must not
remain the implementation owner.

### Payment provider ownership

Concrete Waffo and Waffo-Pancake HTTP handlers, webhook verification, checkout
SDK calls and provider catalogue metadata are owned by
`modules/chuanapi/payment`. Generic host concerns such as payment-compliance
gating, wallet-capacity validation, callback/return-address resolution, payload
correlation summaries and order serialization live in provider-neutral
`pkg/paymentcore` / `pkg/paymentport` contracts.

The first provider extraction intentionally retains two compatibility boundaries:

- `setting/payment_waffo*.go` plus the matching option-map wiring remain in the
  host settings layer so existing persisted option keys and operator configuration
  continue to load without a migration;
- the host `TopUp` / `SubscriptionPlan` schema and provider-specific completion
  helpers in `model/` remain the transaction/data compatibility boundary. Moving
  those fields requires a separate persisted-schema/settlement migration and is
  not implied by handler/service modularization.

These boundaries must be reported explicitly; Phase 10 removes concrete provider
knowledge from root router/controller/service orchestration, not every historical
provider column or option key from the host database/settings model.

## System task and frontend ownership

Affiliate maintenance and similar module-specific scheduled jobs belong to the
module and will require a lifecycle/system-task contribution contract before
extraction. Concrete pages, i18n, flags and help content belong with the frontend
module; file-route shells consume generic accessors after Phase 2.
