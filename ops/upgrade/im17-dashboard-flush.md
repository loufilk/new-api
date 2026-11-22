# Dashboard statistics flush

`quota_data` is an hourly **report**, not a financial ledger. The gateway still
writes consumption logs and performs settlement through their existing paths.

On a flush the process swaps its active reporting buffer under a short lock.
Arrivals while the detached snapshot is saved go into a new buffer. Each
transaction saves at most 64 distinct dimension buckets and has a five-second
timeout. PostgreSQL applies the deltas with one set-based write. The reporting
writers use a dedicated gate row to coordinate creating new buckets; this
does not lock account balances. Existing duplicate buckets are not deleted
or consolidated: only the lowest-ID exact match receives the new delta. An
old row with a NULL dimension is not silently relabelled as a default group,
channel, key or node.

The migration widens the four PostgreSQL reporting labels (username, model,
group and node) from `varchar(64)` to `text`, preserving index names and
existing values without an application-level backfill. Model names up to 128
characters and longer node names no longer block an entire batch. Allow a
DDL lock window at migration time. Do not run the old primary's AutoMigrate
against the widened table during a rolling upgrade or rollback: it may try
to narrow labels back to 64. Back up first and use a migration-disabled
rollback binary or a coordinated database restore if rollback is necessary.

The additive cursor migration creates `quota_data_flush_cursors` **in the main
database**, containing a gate row and one cursor per process that has flushed.
It does not require a unique index on the historical `quota_data` dimensions,
a historical backfill, or a log-database migration. Run the new master's migration
and verify readiness before upgrading reporting replicas. Drain old writers and
do not restart an old master with migrations enabled against the widened schema.
A batch's statistics and
its cursor advance in the same transaction. If the connection fails during
commit, the same process retries the original batch with the same sequence.
Do not truncate this small table or reset a live writer's cursor: the
acknowledgment of its last successful commit may have been lost.

If a flush fails, the service logs the number of saved and pending buckets.
The failed batch and subsequent buckets remain in memory; subsequent passes
retry before advancing. A persistent write error delays that process's reports
and can grow its memory use. Resolve the database error and allow a successful
retry **before** restarting or rolling back. Uncommitted buffers are not a
durable queue and cannot survive a process crash. Do not replay them manually
into the ledger. The final drain reports failure rather than claiming a
successful flush. In a mixed-version rollout, old writers retain their
pre-upgrade behavior; drain them promptly, especially if historical duplicate
dimensions are present.
