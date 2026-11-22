# Dependency Boundaries

## Backend

The target direction is `core -> port <- module`. Core production code must not
import `chuanapi` or `modules/chuanapi`. The one production exception is
`main.go`, which is the application composition root and may invoke
`chuanapi.Register`.

`chuanapi/register.go` is a temporary composition adapter. It may depend on host
and module packages while the migration is in progress, but its final state must
only compose independently owned modules. Root `model`, `service`, `controller`,
`router` and `relay` packages must never import it.

### Host-owned HTTP scopes

Existing host API surfaces are exposed to route contributions through
`extension.RouteScopes`. `router.SetApiRouter` creates the `/api` base group and
its authenticated/admin child groups, applies host middleware once, then passes
those exact group handles to extension registration. ChuanAPI modules attach
endpoint-specific routes to these handles; they must not reconstruct the global
gzip, audit, body-cleanup, or API rate-limit chain themselves. This keeps future
host middleware changes automatically inherited by module routes.

### Payment provider registry

Optional payment catalogues register independently in `pkg/paymentport.Registry`
with a stable provider id and a declared set of payment-method types and response
field keys. Composition order is the deterministic render order. Duplicate
provider ids, method types, or field keys are rejected before the registry is
frozen; runtime contributions are also rejected if they emit undeclared
surfaces. The process installs one frozen registry, not one concrete provider
slot. Host-configured legacy `PayMethods` retain their existing precedence when
the top-up response is assembled.

## Extension kernel versus runtime ports

These are deliberately separate mechanisms:

- `extension/` is startup contribution infrastructure. Its current external
  dependency whitelist remains **stdlib + gin + gorm only**. Batch A does not
  permit a generic DTO dependency and does not relax `extension/boundary_test.go`.
- `pkg/moneyport/` and future `pkg/*port` packages define runtime capability
  contracts and their own host-independent DTOs. They do not become extension
  descriptors or service locators.

If a future extension descriptor genuinely needs a shared DTO package, that is
an architecture change requiring a separate ADR and an explicit boundary-test
update. It must not happen as incidental refactoring.

## Frontend

`web/src/extensions/bootstrap.ts` is the frontend composition root and is
loaded explicitly by `web/src/main.tsx`. `web/src/extensions/index.ts` is a
generic accessor surface only and must not import the concrete implementation.
Concrete ChuanAPI implementation lives under `web/src/extensions/chuanapi/`.

At the Batch A baseline, three route shells directly imported
`@/extensions/chuanapi/help`. Phase 2 replaced them with generic help/page
contributions and the core concrete-import gate is now zero. Later Wallet and
payment-settings extraction follows the same rule: core imports generic accessors
from `@/extensions`, while `bootstrap.ts` is the single concrete wiring point.

## Brand literals

Batch A freezes the three current production-core files containing
`chuanapi.com` string literals. Phase 1 will move those values behind trusted
configuration/composition. New production-core literals are forbidden now.
