# Generic event persistence (Go migration)

Status: tested persistence slice; not yet the production HTTP/plugin writer.
The existing API and Hatchet orchestration still require their coordinated Go
cutover. These tests do not demonstrate a live external approval resuming Codex.

Loom stores external associations in `integration_bindings` and received
evidence in `integration_deliveries`. Neither table requires a repository, PR,
CI job, runtime or provider-specific column. New providers use these same tables.

For example, a deployment Goal can wait on:

```json
{
  "source": "approval-service",
  "type": "approved",
  "resource": "deployment-123",
  "version": "revision-2"
}
```

An operator-authorized binding associates that condition and its integration
instance with a Goal's current wait generation. A receipt from another tenant,
instance, resource or revision cannot satisfy the binding. Core routing selects
the Goal deterministically; the existing Run/Session/Worker binding remains the
authority for subsequent execution. Receipt processing never invokes a model.

## Authorization contract

`Store.Bind` and `Store.ReceiveIntegration` are internal persistence methods,
not unauthenticated HTTP handlers. HTTP adapters must authenticate callers and
authorize binding creation. Integration adapters must verify signatures and
also actor/task authority, resource identity and provider-specific freshness
before supplying a normalized condition. A nil condition retains evidence but
cannot cause execution. External payload instructions never become core policy.

Attributes hold plugin validation data such as an approved actor group. Opaque
identifiers must be strings. Numeric attributes are restricted to safe integers
within ±(2^53−1), recursively, to avoid lossy JSON-number comparisons. Binding
attributes, instance and condition are immutable within one wait generation.
The current schema permits one binding per generation and one owner of an exact
external resource/version within an organization/instance. Multi-subscriber
fan-out and compound approval policies are not implemented by this slice.

## Receipt ordering and durability

Receipt registration and binding/reconciliation serialize within an integration
instance; other tenants and instances remain independent. Pending receipt locks
are acquired before the Goal lock. Goal locking serializes admission with
cancellation and wait-generation changes. An early authorized receipt remains in
the inbox and is reconciled transactionally when its binding is registered.

Raw-body digests preserve legacy delivery identities. A separate fingerprint
also binds normalized input for new Go receipts. Changed content under the same
delivery identity is rejected. Imported ignored receipts require adapter
revalidation before acquiring a normalized condition. Receipt bytes alone do
not confer authority. Accepted events satisfy a wait and enqueue a durable wake;
they do not imply Goal completion or create an execution attempt.

## Migration and tests

Fresh Go databases create no GitHub-specific tables, even when an adapter later
uses `source=github`. Existing GitHub storage is imported into generic tables
with original tables/bytes retained. Import conflicts abort atomically. Before
production migration: back up, stop legacy writers, reconcile in-flight Runs,
and complete the native API/plugin/orchestration recovery gates. Disabling an
integration must disable its adapter, not delete its history.

Run `go test -race ./...` from `services/loom`. Persistence tests launch their
own disposable PostgreSQL container and create isolated schemas per test. An
explicit `LOOM_GO_TEST_DATABASE_URL` can instead designate a dedicated test
database; never supply a production database. Tests exercise fresh schema,
legacy imports, rollback, early receipts, concurrent binding/receipt delivery,
duplicate events, stale versions, tenant/instance isolation, exact attributes
and cancellation races. CI's Go race/static-check step runs these tests and
checks Ent regeneration. Full Hatchet/worker/Codex end-to-end acceptance remains
a separate release gate.
