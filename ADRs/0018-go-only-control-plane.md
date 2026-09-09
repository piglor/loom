# ADR-0018: Go-only control plane and tooling

Status: Accepted by explicit user direction, 2026-09-09.

Replace the Python writer, Hatchet coordinator, integration adapters and proof
tooling with Go. Keep Rust for the machine agent/runtime adapter, and TypeScript
for the browser. Supersedes ADR-0013's interim reverse-proxy deployment once the
replacement passes its gates. The user does not want a maintained Python stack.

```text
HTTP / worker / webhook → Go domain transactions → PostgreSQL
                           ↕ durable outbox
                        Hatchet Go worker
                           ↕ outbound commands
                         Rust Agent
```

Preserve schema, IDs, worker credentials, Session affinity, event digests and
receipt compatibility. Port the existing tests to Go, initially comparing against
the old implementation on an isolated database. Remove Python source, packaging,
dependency lockfiles and CI setup after equivalent Go proofs pass. Historical
commits remain a recovery reference, not a second maintained backend.

Use the official Hatchet Go SDK, pinned against the tested engine. Give the Go
workflow a new name: Python durable replay history must not be interpreted as Go
history. During cutover fence old writers, inventory nonterminal Goals, reconcile
active Attempts without relaunch, then explicitly attach a Go workflow to each
remaining Goal. Back up before migration. Do not delete live workflows or data.

Evidence: official [Go context](https://docs.hatchet.run/reference/go/context),
[client](https://docs.hatchet.run/reference/go/client) and
[runnables](https://docs.hatchet.run/reference/go/runnables) document durable event
waits, retries, event publishing and workers. Inspect the pinned source before
implementation and prove compatibility rather than assuming replay portability.

Release gates: duplicate/stale/early events, wrong identity and invalid auth,
worker/API/orchestrator restart, repeatable waits, both integration plugins,
database upgrade, backup/restore, all browser regressions and no Python in the
runtime image or required development toolchain. Unfinished Codex containment
findings remain independent release gates, not reasons to retain Python.
