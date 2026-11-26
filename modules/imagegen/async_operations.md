# Dashboard async image receipts

* Existing clients and existing result tickets keep their original endpoints.
  New dashboard builds mint an operation via session-authenticated GET
  /v1/images/tasks/operation-ticket, use POST
  /v1/images/tasks/operations/:id/{generations,edits}, and query results via
  session-authenticated GET /v1/images/tasks/operations/:id. Deploy the
  backend to **all** image-serving nodes before activating the new frontend, or
  route the operations prefix and ticket endpoint exclusively to updated nodes; do not rewrite it
  to the legacy upload route or retry an upload with a fresh ID. On rollback
  keep an updated read backend available until outstanding image results expire.
* The dashboard records the opaque operation ID in owner-scoped browser storage
  **before** sending the paid request. If persistence is unavailable it stops.
  Closing, reloading or pressing Retrieve only polls; it never resends the prompt.
  No bearer token, uploaded file or result is stored in the receipt. Browser
  storage for another account, browser or device is not a recovery mechanism.
* The operation ID is minted by the server using Redis TIME and signed for
  the current owner using the deployment's existing shared CRYPTO_SECRET
  (or SESSION_SECRET fallback). Confirm every image-serving node has the same
  configured value before rollout. Issuing a ticket does not reserve quota or
  write Redis. Redis enforces a two-minute admission window; changing the
  browser clock or inventing a timestamp cannot create valid consent.
  Identical owner, Key ID, operation type and serialized
  input get one worker. Changed input or Key ID conflicts. JSON is compared
  byte-for-byte; MIME multipart boundaries alone may differ. Result retention
  remains 15 minutes and the worker deadline remains 5 minutes. A small hashed
  binding is retained for 24 hours; even deleting an expired tombstone does not
  make a stale ID valid. An early lookup may see awaiting_admission rather than
  claiming a concurrent POST failed. A busy or disconnected submit response
  is not proof that an in-flight copy was rejected; check the original ID.
  An ambiguous ACK does not authorize a retry.
* This guarantee assumes a consistent Redis primary retaining receipt keys for
  their TTL. Use persistence, non-evicting capacity and a tested failover policy.
  A restore or failover that loses acknowledged writes is **not** an exactly-once
  financial ledger. Do not replay unknown paid writes after disaster recovery;
  reconcile against usage/billing records. No billing or historical data is migrated.
* A lost Redis admission reply or a worker crash before completion can leave
  the result unavailable even though replay is disallowed. A failed or missing
  result is not proof of a refund. Check the existing usage records before
  authorizing a genuinely new generation.
