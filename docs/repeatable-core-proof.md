# Repeatable execution core: implementation boundary

The new internal policy removes the two-attempt limit without changing deployed
v1 Goal semantics. It is not yet exposed by Goal creation HTTP/CLI or remote
worker admission. See [ADR-0014](../ADRs/0014-repeatable-execution-core.md).

## Implemented

- An immutable final event condition and bounded attempt budget.
- Dynamic next-wait preparation by the exact admitted attempt, before an
  external operation starts. One replacement per attempt; retries are idempotent.
- Atomic historical wait preservation and generation advancement.
- Explicit yield/complete/blocked stop outcomes. Completion without matching
  authoritative event evidence blocks; it does not discard runtime-stop evidence.
- Early events can satisfy prepared waits but cannot start overlapping execution.
- Stop-receipt recovery, unknown-execution blocking, and cumulative wait metrics.
- Outbox acknowledgments cannot clear a newer publication of the same row.
- Schema-6 suspended Goals preserve identity and their original two-attempt policy
  when upgraded to schema 7. Go and Python readers expose the same history.

## Reproduce

Use the isolated local development stack, never production credentials:

```sh
make setup init infra migrate
make test
npm ci
npm run build
make prove-repeatable
```

`make test` includes repeated cycles, generation mismatch, early event admission,
receipt conflict/recovery, completion rejection, attempt exhaustion, wrong
attempt/session/worker bindings, failed preparation without execution, unknown
recovery, and a real schema-6 upgrade fixture.

`make prove-repeatable` requires a loopback `_test` PostgreSQL database. It runs
four finite, non-model subprocess attempts, three waits, and three Go-reader
restarts, then compares cumulative metrics with Python. Calls made while waiting
do not launch subprocesses. It uses a dedicated organization and receipt directory.
The proof does not invoke Codex, GitHub, or a Hatchet workflow. It retains scoped
proof Goals in the test database. The ordinary `make prove-go` separately tests
legacy v1 orchestration recovery through real Hatchet and the Go gateway.

Both the unit/integration tests and the new repeatable proof are mandatory in the
`durable-core` GitHub Actions job; the existing 72-case browser suite remains a
separate required job. A green browser suite is not an autonomous-agent proof.

## Still required before public admission

1. Extend the worker contract with pre-registered waits and explicit outcomes;
   negotiate capabilities so old workers cannot misinterpret new commands.
2. Carry those outcomes in the Codex adapter's fsynced stop receipts and prove
   exact provider-session binding under isolated runtime execution.
3. Make GitHub bindings generation-aware and revalidate source authorization and
   current CI evidence before privileged execution. Keep other event sources valid.
4. Prove repeated cycles through Hatchet, then a real isolated PR/CI webhook path.
5. Provide authenticated create/cancel/approve/input UX and safe onboarding.

The finite demo executor accepts an internal next-condition argument for these
tests; it does not choose a workflow or invent dependencies. Public APIs still
create only legacy demo Goals. No production deployment or billable runtime
admission is implied by this change.
