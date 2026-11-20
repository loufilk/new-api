# CORE im.19 staging gates

Do not use `core-newapi-cutover.sh` for im.19. Its permitted transitions and
schema-preserving im.13 fallback are specific to im.18. The im.18 gateway
does not provide the dedicated fallback's verify-only startup contract. A
valid im.18 image is therefore not evidence it can preserve im.19 writes.

1. Record the exact candidate Git SHA that passed the **im.19 exact candidate**
   CI job on PostgreSQL 18.6 and Redis 8.8.0. Match the tested head SHA in the
   job summary to the candidate, not an older green run or PR merge SHA.
2. On an independently authorized and isolated Linux runner, with the target
   CPU architecture, preload the reviewed im.18 and im.19 candidate images by
   their canonical repository digests. Use an independently reviewed release
   record for the expected digests and full revisions. The following
   command only inspects local Docker images; it never pulls, runs, or
   changes a container:

   ~~~bash
   python3 ops/upgrade/core-rollback-rehearsal/check-im19-image-pins.py \
     --confirm-isolated-runner \
     --previous-image "$APPROVED_IM18_IMAGE" --previous-revision "$APPROVED_IM18_SHA" \
     --candidate-image "$APPROVED_IM19_IMAGE" --candidate-revision "$APPROVED_IM19_SHA"
   ~~~

   A pass proves identity only. Compare each OCI version label to the
   executable's `--version` and the isolated gateway's `GET /api/status`.
3. Confirm a schema-compatible rollback artifact before trying any cutover.
   On isolated copies, independently exercise upgrade, new writes, no-restore
   application rollback, and re-upgrade using the same primary and log
   PostgreSQL 18.6 databases and Redis 8.8.0. Compare schemas, log routing,
   wallet and invoice records, task ownership, and report counts and hashes.
   Test the Caddy routing and both graceful shutdown and private readiness.
   im.19 also freezes payment instructions on new Stripe and Epay orders. The
   previous binary does not interpret those quotes. A safe rollback must
   either preserve that contract in a reviewed fallback or isolate both new
   order creation and settlement on that fallback, then reconcile outstanding
   provider callbacks using the quote-aware version before restoring payments.
   Verify that no accepted order is silently recalculated at the old live rate.
   Keep deployment blocked if those checks are absent or fail. Do not
   restore an old database snapshot as a no-loss application rollback.
