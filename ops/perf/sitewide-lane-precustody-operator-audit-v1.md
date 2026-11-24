# Pre-custody operator observation, version 1

This standalone verifier reports **observed quiesced, no custody**. Its
receipt is not admission, permission to open wallets, a persistent fence, or
proof that the supplied inventory covers the fleet. No app, migration, liveness
probe or worker calls it. The App's runtime capability remains false. It never
changes a role, cancels a backend, resolves a prepared natively, creates a row,
or alters a setting outside its own short read-only transaction.

## Before anyone runs it on a real environment

Have the responsible database and fleet operators independently identify and
sign a change record covering the exact release/epoch and:

- Every writable endpoint and route to it (network/sidecar/transaction pool,
  failover and restored copies, MAIN/LOG). Version 1 accepts only **one direct
  writable PostgreSQL 18.6 instance**, a dedicated cluster with one shared
  MAIN/LOG database/schema, no split log or alternative writer. Never omit an
  endpoint to get a positive result. This verifier neither discovers these
  endpoints nor qualifies a pool or an HA failover topology.
- Every app, admin, payment, job, worker, scheduler, direct SQL client, replica
  that could be promoted, operator and credential distributor. Maintain an
  externally enforced freeze from before the first sample until a separately
  reviewed handover or explicit rollback. Check old deployments, secrets
  and alternate credentials cannot restart, reconnect, or assume another role.
  Protect the DBA/control plane, backups and migration roles. Never copy the
  staged-next credential to old binaries. Check active network/HBA policy
  and actual old-auth denial independently, including proxy paths. Removing
  an old login grant does not terminate existing transactions.
- Explicitly drain or stop old sessions and deal with any 2PC transactions
  under the responsible operator's durable commit decision. **Do not** use
  this tool to classify a transaction as safe to roll back. Check the entire
  dedicated PostgreSQL cluster, not only the application database. A zero
  snapshot isn't proof if a writer can reconnect or the DB can be replaced.
- Derive the expected identifiers from the controlled migration and artifact
  receipts and independently authenticated database/cluster inventory before
  sampling. Include server IP, port, system ID, timeline, database/schema OIDs,
  three physical manifest relation OIDs, role names and OIDs, expected limit
  for prepared transactions (explicit zero or positive), candidate and base
  release IDs and hashes, and the common MAIN/LOG migration run ID. Keep the
  offline candidate artifact on a read-only/trusted local file store. Record
  the SHA-256 of the signed operator change record; the verifier copies this
  digest, **does not** verify the external signature or completeness.

Do not use production credentials to run the included rehearsal. Do not rely
on this utility for a live activation: its V1 supported shape is deliberately
much narrower than a normal running service.

## Invoking from a clean source checkout

Build the verifier from the same exact clean Git commit as the offline New API
candidate executable:

```sh
go build -buildvcs=true -trimpath -o /trusted/bin/lane-precustody-audit ./cmd/lane-precustody-audit
/trusted/bin/lane-precustody-audit -inventory /trusted/release/store-inventory.json \
  -candidate /trusted/release/new-api > /trusted/release/pre-custody-observation.json
```

Inject the auditor's PostgreSQL URL into the process via the isolated
`LANE_PRECUSTODY_AUDITOR_DSN` secret channel, **not** a command-line argument,
inventory file, shell-history literal, terminal transcript, or shared log. The
URL requires the registered auditor user, nonempty password, target DB and
explicit port, a certificate hostname, and just `sslmode=verify-full` plus an
explicit trusted `sslrootcert` path. Do not set `PG*` environment overrides.
The verifier pins the TCP destination to the independently inventoried server
address, checks the actual SSL session/SQL server address and does not use a
pool/proxy or fallback DSN. Cluster, endpoint and source data are independently
checked against inputs, not read back and promoted to expected values. Both
tool and app clean VCS revision and the app's actual SHA-256 must match release
inputs; this doesn't establish what binary is deployed or prove build provenance.

The V1 inventory has this shape. Angle-bracket values are placeholders; JSON
number fields are numeric and all OIDs must be independently observed actual
values. The file has no credential fields. Keys must use exactly the spelling
shown, with every field present and without duplicates; spacing and order are
not significant.

```json
{
  "version": 1,
  "topology": "one_direct_shared_main_log_primary",
  "operator_evidence_sha256": "<64 lowercase hex>",
  "source_revision": "<40 lowercase hex>",
  "writable_endpoints": [{
    "label": "dedicated-shared-primary",
    "store": {
      "system_id": "<unsigned PostgreSQL system identifier as decimal text>",
      "timeline": 1,
      "server_addr": "192.0.2.10",
      "server_port": 5432,
      "server_version_num": 180006,
      "database": "newapi",
      "schema": "managed_newapi",
      "database_oid": 10001,
      "schema_oid": 10002,
      "authority_oid": 10003,
      "catalog_oid": 10004,
      "migration_oid": 10005,
      "max_prepared_transactions": 0,
      "auditor": {"name": "audit_reader", "oid": 10006},
      "owner": {"name": "protected_owner", "oid": 10007},
      "retired_writers": [{"name": "legacy_writer", "oid": 10008}],
      "next_writers": [{"name": "staged_writer", "oid": 10009}],
      "control_admins": [{"name": "protected_dba", "oid": 10010}],
      "transition": {
        "Release": {"StoreID": "<32 lowercase hex>", "ReleaseID": "<32 lowercase hex>",
                    "BinarySHA256": "<64 lowercase hex>", "Epoch": 7},
        "BaseReleaseID": "<32 lowercase hex>",
        "BaseBinarySHA256": "<64 lowercase hex>",
        "MigrationRunID": "<same BaseReleaseID>:<exact run id suffix>"
      }
    }
  }]
}
```

V1 requires named DBA admission as the trusted external control plane;
unaccounted elevated roles reject the sample. The DB/schema/three metadata
tables must have the protected NOLOGIN owner (no role memberships). Both the
retired **and the not-yet-enabled next** writer sets must be NOLOGIN with no
CONNECT, no memberships, no CREATE/TEMP/no application relation writes, no
sequencing writes, and no implicit app-table ownership. PUBLIC cannot CONNECT,
CREATE or TEMP on the target DB. The audited observer can CONNECT without
grant option, has a narrow `pg_read_all_stats` membership with INHERIT true,
SET/ADMIN false, only the required schema/table/catalog/function reads and no
application or DDL access. It must be able to read only the harmless `pg_subscription.oid` column, not
`subconninfo` (which can hold credentials), and use the
`pg_control_system` / `pg_control_checkpoint` functions. Authentication must
be configured by the database operator; the CLI installs nothing. Explicit `ALTER SYSTEM` grants to the auditor or embargoed writers are denied.
The built-in session-only `pg_settings` rules are verified by SHA-256. Unsupported
extension, replication subscription, foreign-server, event-trigger, user
SECURITY DEFINER, historic enterprise/lane tables or same-name metadata shadow
fail closed. Normal traffic in *any* database, including other authenticated
readers or DB-affiliated background jobs, blocks observation.

Two distinct short, sequential physical sessions must see the exact immutable
no-custody catalogue, dormant release and both matching migration receipts.
Counts must show only the audit client beyond the known core launchers, and
zero prepared transactions. A new connection and fresh stats sample do not
mathematically prove continuous quiescence; they help identify drift or stale
pool sessions. A physical backup can copy system ID/OIDs, and a timeline does
not prove topology. No producer trusts this output. Local credentials and
backend error details do not enter it. Store the receipt alongside the signed
operator record; any policy, manifest, version, database, postmaster,
deployment, or topology change invalidates its use as evidence.

## Synthetic proof

`ops/perf/run-lane-precustody-audit-dev.sh <exact-source-sha>` initializes
only a new loopback PostgreSQL 18.6 cluster, pins a generated TLS certificate,
and exercises fresh sessions, an active client in another database, real
prepared work surviving disconnect in another database, residual writer
privileges, unknown CONNECT, role membership, shadow/migration and physical
identity drift, mismatched prepared mode, invalid TLS and unsupported input.
It executes the built standalone command against an offline build of the actual
root Go app source with an inert ignored frontend fixture (not a deployable
artifact); it never starts the app or touches another server.

PostgreSQL references: [activity visibility](https://www.postgresql.org/docs/18/monitoring-stats.html),
[role edge options](https://www.postgresql.org/docs/18/catalog-pg-auth-members.html),
[prepared work](https://www.postgresql.org/docs/18/view-pg-prepared-xacts.html),
[control functions](https://www.postgresql.org/docs/18/functions-info.html).
