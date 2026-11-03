# A07 implementation progress A01

Date: 2026-11-02
Branch: bench
Status: in progress; not ready for acceptance; not committed or pushed.

## Scope

Implement the user-approved A07 agent system in this repository and push the
verified result to origin/bench. Do not deploy to production or run production
migrations. Preserve existing work and exclude the unexpected web/pnpm-lock.yaml.

## Completed in this stage

- Website top-up referrals use uncapped 3% ordinary / 5% active agent rates,
  captured at payment time, with a five-day hold.
- Paused/cancelled agents do not receive new 5% rewards or new-customer grants.
- Active-agent referrals grant new users a fixed $3 bonus once; existing signup
  bonus stacking and compliance gates remain covered.
- Released referral rewards transfer to the paid wallet with idempotency and
  live browser-session validation; frozen rewards cannot transfer.
- Direct code issuance automatically spends owned sources before credit and
  records mixed source allocations. Customer redemption creates credit debt.
- Agent recharge uses actual payment to repay debt first, then credits the
  remainder at the fixed 0.90 cost ratio.
- Payment-code maturity includes redeemed codes. Refunds reverse their own
  pending profit or available profit/debt without spending another order's hold.
- Removed the worker that repeatedly erased refund debt without consuming value.
- Expired codes cannot release credit a second time. Verified late payment
  callbacks remain deliverable after local payment-link expiry.

## Observed validation

- Go 1.27.3 darwin/arm64 is available in PATH; compile/run check succeeded.
- `GOWORK=off go test ./model -run 'TestAgentSimple|TestReferral|TestTransferAff|TestRecordReferral|TestAgentInvitation' -count=1`: PASS (9.273s).
- `GOWORK=off go test ./modules/chuanapi/wallet -count=1`: PASS (1.270s).
- Focused Vitest for `use-affiliate` and `simple-code-form`: 2 files / 4 tests PASS.
- Regression cases were observed failing before the relevant corrections.
- Detailed transient logs: /tmp/wanchuan-a07-financial-current.log,
  /tmp/wanchuan-a07-wallet-after.log, /tmp/wanchuan-a07-frontend-current.log.

## Remaining acceptance work

- Unified root withdrawal registration for referral and payment-code profits,
  minimum $20 combined, one calendar month, 2% fee, debt offset first.
- Weekly bills, ordered income offset, repayments, and daily overdue reminders.
- Agent exit balance transfer and cash-only refunds with audit evidence.
- Simple root management and audited full-code inspection; close old workflows.
- Shared wallet/agent referral panel, support entry, records and translations.
- Frontend extension boundaries, typecheck/lint/build and runtime UI validation.
- Idempotent legacy invitation-balance migration and reconciliation.
- Real SQLite/MySQL/PostgreSQL fresh/upgrade/restart validation. This Mac currently
  has no detected MySQL/PostgreSQL server or Docker/Podman; do not claim cross-DB
  verification without running it.
- Final full requirement audit, commit, push origin/bench, remote hash check.

## Decision

Continue implementation. Do not claim A07 complete or release-ready from the
focused checks recorded here. No production actions are authorized by this file.
