# Provider session admission

This implemented boundary is a prerequisite for the remote Codex runner, not a
claim that unattended Codex execution is enabled. The machine daemon still only
admits `remote-demo`; its privileged-runtime supervision gate remains closed.

The control plane owns the Goal/Run/Session/Worker relationship. The runtime owns
its provider thread ID. A worker may attach that ID only to its current claimed
execution. Binding does not constitute execution or stop evidence.

```text
Claim command → open exact/new provider thread → persist binding → acknowledge
             → one model turn → confirm runtime stopped → matching stop receipt
```

## Implemented HTTP contract

`POST /v1/worker/commands/{command_id}/session`, authenticated with the worker's
credential (not the operator token):

```json
{
  "protocol_version": 1,
  "claim_id": "<current claim UUID>",
  "session_id": "<bound Loom Session UUID>",
  "provider_session_id": "<exact runtime thread ID>"
}
```

The response echoes protocol version, Loom Session, Worker, runtime type and
provider ID from the validated binding. Callers must verify those identities.
Only a CLAIMED command with a RUNNING Goal/Attempt can bind. A matching duplicate
is idempotent and does not add another audit entry; a conflicting claim/session
or provider reassignment returns 409. A different worker sees 404 and invalid or
revoked credentials receive 401. A provider ID is opaque, contains no whitespace
or Unicode control characters, and is at most 256 Unicode characters; it is not a
filesystem path or an executable instruction.

An initial phase can attach a new provider context; a continuation can only
confirm the existing context. The same provider context cannot belong to two
Loom Sessions on the same worker/runtime. PostgreSQL enforces a partial unique
index, and worker row locking serializes competing binding requests.

Once bound, every new stop receipt must include the matching
`provider_session_id`, including failure receipts. Without it the Goal remains
RUNNING; a binding acknowledgment never changes it to WAITING. Existing unbound
finite-runtime receipts remain compatible, including their persisted hashes.

## Rust adapter boundary

`Codex::open_bound_thread` opens/resumes a thread, then invokes a required
persistence callback. The callback must commit the local binding and any required
remote acknowledgment before returning success. Only then can `turn` run.
The callback is a caller contract, not cryptographic proof of database state.
Persistence failure disables inference on that process; stop it and reconcile
the saved binding rather than silently creating another context.

Each adapter process admits at most one `turn/start`, marked before its first
write. A failed acknowledgment, failed turn or unsupported approval cannot lead
to a hidden second model turn. A later authorized attempt starts a fresh app-server
and resumes the exact persisted thread. The standalone probe now uses this gate
around its fsynced binding receipt.

These methods follow Codex's documented separation of `thread/start`,
`thread/resume` and `turn/start`. See the
[official app-server lifecycle](https://learn.chatgpt.com/docs/app-server).

## Migration and validation

Run `make migrate` for schema version 6 before starting the new API/gateway.
The migration adds an index; it does not change existing Goal states or erase
in-flight work. If historical provider IDs conflict, migration fails rather than
choosing an owner. Resolve those records explicitly. Do not roll back to a writer
that lacks this binding contract while bound attempts are in flight; old writers
cannot validate their new receipts. No production migration is implied here.

Tests use synthetic provider IDs and deterministic Codex protocol fixtures, not
model inference. They cover persistence across API reconstruction, duplicate and
concurrent requests, wrong claim/session/worker, revocation, continuation identity,
legacy receipt compatibility, and refusal to infer before persistence. Existing
Hatchet/worker/container restart proofs remain separate regression gates.

Current validation includes Go PostgreSQL/race tests, Rust unit and Codex
protocol conformance tests, static checks and the 72-browser matrix. These
results do not constitute a composed remote Codex or production deployment proof.

The first CI run exposed intermittent Linux `ETXTBSY` in parallel tests that wrote
and immediately executed temporary fixtures. A local stress loop reproduced it.
The tests now execute one immutable checked-in fixture, avoiding transient
inherited writable descriptors during concurrent process startup. All 100
subsequent six-test runs passed without retries. The 32-browser-check matrix and
both non-root/read-only container proofs also passed locally.

Still required before enabling the remote Codex adapter: isolated runtime
configuration, enforced descendant-process containment, revocation/lease watchdog,
durable machine-side provider bindings and stop receipts integrated into delivery,
explicit ambiguous-execution reconciliation, and a composed real-provider proof.
