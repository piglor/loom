# ADR-0016: Explicit repeatable worker outcomes

**Status:** Proposed; implementation authorized by continuation instruction
**Date:** 2026-09-09

Protocol 1 remains compatible. Operators may enroll a finite worker with protocol
2 capability and create a Goal with an immutable `completion_condition` and
`max_attempts` budget. Existing Goals default to their original two-attempt policy.
No new transport or Hatchet API is introduced.

```text
worker credential → claim(version 2) → structured execution context
  → prepare next wait(claim + Session + expected generation)
  → runtime stops → fsynced stop receipt(yield / complete / blocked)
  → WAITING or completion-policy evaluation
```

All operations stay on the outbound mailbox. The stable poll envelope is version
1; individual claim/session/wait/stop requests negotiate protocol 2. The server
checks enrolled capability and Run policy before admitting or mutating execution.
An old worker must not claim a repeatable Goal. Protocol 2 can also carry a legacy
Goal without changing its policy or receipt semantics.

Event context is structured external data, separate from the owner objective.
It cannot select a Session, Worker or runtime. Session/provider binding remains
immutable. Preparation and authentication share a transaction. Registration alone
does not mark a runtime stopped. Completion and budgets remain server-authoritative.

The Rust finite adapter supports the protocol but does not invent dependencies:
it yields initially, completes only when the final condition has been satisfied,
and otherwise reports blocked. A reasoning adapter will supply dynamic outcomes
through the same interface. This is not authorization to run Codex or public code.

Alternatives: unversioned additions risk old workers silently ignoring outcomes;
a separate transport duplicates authentication and durable delivery. Explicit
capability plus message version preserves existing receipts and fail-closed admission.

Acceptance: old receipts unchanged, wrong version/worker/session rejected before
mutation, repeated remote waits and early events, exact context delivery, immutable
provider binding, stop replay, restart recovery, budget exhaustion, and actual Rust
outbound conformance. No external-content-derived routing or model polling.
