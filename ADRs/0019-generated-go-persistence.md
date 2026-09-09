# ADR-0019: Ent-generated persistence

Status: Accepted by user direction, 2026-09-09.

Use Ent v0.14.6 schemas written in Go and generated typed CRUD/query builders.
Use PostgreSQL for Loom and Hatchet in both deployment modes, with separate
databases and credentials. Lite packages a smaller single-host installation;
Server can scale API, orchestration and machine workers independently.

Do not create a separate scheduler or maintain a SQLite implementation now.
Preserve the ability to change persistence behind the domain interface, without
pretending PostgreSQL locking semantics are automatically portable.

Existing IDs, tables, foreign keys and event/receipt identities remain stable.
Historical versioned SQL migrations stay explicit; do not run Ent's automatic
schema creation against production. Ordinary CRUD and predicates are generated.
Reviewed database coordination operations may use narrowly scoped SQL.

Ent schema definitions are authoritative for generated application access; the
versioned migration history is authoritative for deployment. CI checks generation
is reproducible and tests the generated builders against the migrated database.
Ordinary integration bindings and receipt inboxes use the same generic tables
for every provider. Core fields describe organization, source, instance, event
type, resource, version and wait generation, not repository, PR or commit.
Plugin-specific validation data lives in binding attributes; signed receipt
bytes can be retained separately from normalized, authorized event conditions.
Adding an approval system does not require a table or Goal-state migration.
Only genuinely provider-specific storage may be owned by a plugin.

The Go cutover does not create historical GitHub tables on a fresh database.
On existing databases, it upgrades and imports legacy bindings/receipts into
the generic tables without deleting the originals. Imported ignored receipts
remain unnormalized until the plugin revalidates them; migration is not event
authorization. Conflicting binding ownership aborts rather than overwrites.
Stop legacy writers and take a backup before migration: the old writer does
not dual-write the new generic representation. Runtime/API cutover and recovery
tests are separate release gates, not implied by successful schema migration.

Sources reviewed 2026-09-09:

- [Ent fields](https://entgo.io/docs/schema-fields/): custom IDs, storage keys and JSON fields.
- [Ent feature flags](https://entgo.io/docs/feature-flags/): generated upsert and row-locking support.
- [Hatchet Lite](https://docs.hatchet.run/self-hosting/hatchet-lite): development and low-volume deployment using PostgreSQL.

Compared with sqlc, Ent generates CRUD from Go schema definitions instead of
requiring hand-authored CRUD SQL. Compared with a reflection-first ORM, generated
builders make available fields and predicates explicit to the compiler. This is
the requested maintenance trade-off, not a claim that code generation implements
Loom's authorization, transactions, idempotency or workflow policy for us.
