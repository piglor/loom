# Repeatable outbound worker contract

Implemented finite-runtime preview, not unattended Codex execution.

Enroll through operator-authenticated `POST /v1/workers` with
`{"workspace_ref":"sandbox","protocol_version":2}`. Include
`"protocol_version":2` in the private Rust worker configuration alongside the
returned worker identity and credential. Existing configurations default to 1.
Use a new journal directory when changing protocol; journals cannot silently
switch protocol or worker identity.

Create a Goal through `POST /v1/goals` with the usual title, objective, initial
`condition`, `runtime: "remote-demo"`, and enrolled `worker_id`. Add
`completion_condition` (the same generic condition shape) and optionally
`max_attempts` (2–1000, default 100). Omitting completion_condition preserves the
legacy two-attempt behavior. Source/type/resource/version are opaque core fields;
GitHub and GitLab authorize their own external-resource bindings separately.

The polling envelope stays version 1. Claims use protocol 2 and return a
`context` object containing Goal identity/objective, lifecycle, attempt budget,
immutable completion condition, current wait generation/condition/satisfaction,
provider Session ID, and separately structured external event data. External
content cannot select the worker, Session, runtime or workspace.

During an admitted continuation, a worker can call:

```text
POST /v1/worker/commands/{command_id}/wait
Authorization: Bearer <worker credential>
```

```json
{
  "protocol_version": 2,
  "claim_id": "<current claim UUID>",
  "session_id": "<bound Loom Session UUID>",
  "expected_generation": 1,
  "condition": {
    "source": "deployment",
    "type": "rollout.completed",
    "resource": "deployment-123",
    "version": "2"
  }
}
```

Preparation archives the previous closed wait and registers the next generation.
Identical retries return the same wait; conflicting claims, Sessions, generations
or conditions are rejected. Initial execution uses the predeclared first wait.
The endpoint does not grant permission to bind arbitrary external resources.
GitHub/GitLab bindings remain operator-authorized integration operations.

Only after stopping the runtime does the worker journal and send its `/stop`
receipt with protocol 2, claim/Session identity, duration, success, and an explicit
`outcome`: `yield`, `complete`, or `blocked`. Receipt retries are idempotent.
Events received before stopping may satisfy the prepared wait, but cannot launch
another attempt while the current execution remains running. The server checks
completion against the immutable final condition; a claim of success is not enough.

The current Rust finite runtime yields once, then completes on the final matching
event or reports blocked if reasoning is needed. Multi-wait HTTP conformance tests
exercise three attempts and two different event sources. The actual Rust restart
proof is `LOOM_PROOF_PROTOCOL=2 make prove-remote`, also required in CI. Neither
test is evidence of the full real Codex + remote CI workflow.
