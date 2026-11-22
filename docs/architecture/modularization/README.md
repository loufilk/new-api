# ChuanAPI Modularization Baseline

This directory is the execution baseline for Modularization Plan v2.1. Batch A
is intentionally non-behavioral: it records ownership and dependency facts,
defines contracts required before extraction, and installs guards that prevent
the existing coupling from growing.

## Frozen baseline

- Candidate baseline: `5afacf7da479dcf23b450a0535fbfcd67f4a39fb`
- Official NewAPI baseline: `bdef117505247769268b209665fb3ad7554c3da7`
- Starting fork surface: 620 files, +44,958 / -3,986
- Frontend core concrete ChuanAPI imports: 3
- Production core files containing `chuanapi.com` string literals: 3

The checked-in `fork-manifest.csv` is a snapshot of the candidate baseline, not
the current branch after architecture tooling is added. Re-generate it with:

```text
go run ./cmd/archmanifest -base bdef117505247769268b209665fb3ad7554c3da7 -head 5afacf7da479dcf23b450a0535fbfcd67f4a39fb
```

For a current-branch report, omit `-head` and choose another output path so the
frozen baseline remains immutable.

The manifest preserves Git rename detection so its row count uses the same
logical-file basis as `git diff --shortstat`. Per-file numstat for rename rows is
left at zero instead of fabricating delete/add counts; the authoritative total
insertions/deletions remain the frozen shortstat values in `baseline.csv`.

## Batch A deliverables

1. `baseline.csv` — machine-readable baseline metrics.
2. `import-baseline.csv` and `brand-baseline.csv` — measured coupling allowlists to be driven down in later phases.
3. `ownership-rules.tsv` + `fork-manifest.csv` — deterministic fork ownership map.
4. `dependency-boundaries.md` — allowed dependency directions and composition exceptions.
5. `module-ownership.md` — DB model, migration, handler, task and UI ownership rules.
6. `billing-contract.md` — billing operations, identities, atomicity and failure semantics.
7. `funding-factory-contract.md` — factory injection plus the cross-package transaction capability required before Wallet extraction.
8. `architecture/boundary_test.go` — non-runtime gates that freeze current coupling and prohibit new violations.

`UPSTREAM_CORE` in the initial manifest means the changed file is still located
in upstream-owned surface and has not yet been attributed to a narrower module
or generic enhancement. It is a review bucket, not a claim that the custom diff
should remain forever.

Batch A does not change Turnstile, email, payment, billing, wallet or routing
runtime behavior. Those changes start only in later phases after these contracts
are reviewed.
