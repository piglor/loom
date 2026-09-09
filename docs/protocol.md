# Worker and runtime protocol

Status: proposed v1 contract; exact JSON schemas will be implemented and locked with Phase 3. No custom cryptography or general runtime plugin system.

The tables below describe the target protocol, not all currently available routes.
For the implemented finite worker see [remote worker](remote-worker.md); for the
implemented provider-binding endpoint and receipt extension see
[provider session admission](session-binding.md).
The implemented opt-in protocol 2 is documented separately in
[repeatable worker](repeatable-worker.md); the target tables below are not its wire schema.

## Worker transport

Agent initiates authenticated HTTPS requests. A long poll delivers already-authorized commands, with bounded response time, jittered reconnect and a durable cursor. The connection can be idle while all model processes are absent. Heartbeats and software recovery queries are not model inference.

| Operation | Proposed endpoint | Semantics |
| --- | --- | --- |
| RegisterWorker | `POST /v1/workers/register` | Exchange owner-scoped enrollment authority for stable worker identity; self-advertised owner/labels cannot grant permission |
| Reconnect | `POST /v1/workers/{id}/connections` | Authenticate machine, reconcile local journal, acquire new incarnation subject to no overlapping execution |
| Heartbeat / capabilities | `POST /v1/workers/{id}/heartbeat` | Update availability, bounded metadata and active attempt observations |
| Receive | `GET /v1/workers/{id}/commands?after=...` | Long poll returns only this worker's commands; cursor is not sole delivery authority |
| Claim | `POST /v1/commands/{id}/claim` | Revalidate current state, identity, generation, policy and cancellation before launch |
| Bind provider context | `POST /v1/attempts/{id}/session` | Persist exact provider thread before first model turn; reject reassignment |
| Report | `POST /v1/attempts/{id}/reports` | Sequenced started/progress/stopped/failure reports; duplicate sequence and digest handled idempotently |

All mutations carry idempotency key, protocol version and worker incarnation; Attempt mutations also carry session ID and fence. Identity comes from the credential, not the URL or payload. Reject mismatches without disclosing another worker's command. Unknown major versions fail closed. Retrying the same report returns the committed result; same identity with conflicting content is an integrity error.

Commands are `Execute`, `ResumeSession` and `Interrupt` initially. `SendInput` for active turns is deferred; MVP continuation input always begins a new authorized Attempt after the previous one stopped. Human action can resolve a blocked Run through the control-plane API.

```json
{
  "protocol_version": 1,
  "command_id": "opaque-command-id",
  "kind": "ResumeSession",
  "attempt_id": "opaque-attempt-id",
  "session_id": "loom-session-id",
  "worker_id": "bound-worker-id",
  "worker_incarnation": 4,
  "fence": 12,
  "generation": 3,
  "runtime": {"type": "codex", "provider_thread_id": "exact-thread-id"},
  "workspace_ref": "pre-enrolled-workspace",
  "input": {
    "trusted_instruction": "Continue the authorized Goal. Classify the CI failure before changing code. Yield when nothing remains actionable. Do not poll CI.",
    "external_evidence": [{"trust": "untrusted_content", "event_id": "event-id", "summary": "Bounded failure evidence"}]
  }
}
```

Strings identifying objects above are illustrative, not production UUID fixtures. Lease expiry and cancellation state are obtained at claim. A command cannot supply arbitrary executable paths, unrestricted environment variables, shell command strings or new workspace roots. Runtime and workspace must match local enrollment policy.

## Local persistence and supervision

Persist a small local journal (proposed SQLite) outside the worktree with restrictive filesystem permissions. Store stable worker identity, last accepted incarnation, command digest, attempt phase, provider thread/turn IDs, stop receipt and unsent reports. Credentials remain in OS-protected storage or an owner-readable credential file, not the journal payload.

Acquire an OS lock for the worker and each bound Session. Journal before launch; re-send receipts until acknowledged. On restart, reconcile any `STARTING`/`RUNNING`/`UNKNOWN` entry and surviving process before accepting new work. A retry does not create a second process. Phase 3 must prove atomic journal writes, restart identity and lock behavior on the supported OS; Linux is the first proof platform, macOS/Windows claims require their own supervision tests.

Start each Codex app-server within an owned process group/supervision boundary. Stop and reap it and runtime-created children before reporting suspended execution. External CI is an independently owned operation and continues. Cancellation/lease expiry first requests interrupt, then enforces a bounded process shutdown. If shutdown cannot be confirmed, report `UNKNOWN`, not stopped.

## Runtime adapter

The first interface is intentionally small:

```text
create_context(workspace, policy) → provider binding
resume_context(exact binding, workspace, policy) → loaded context
start_turn(context, trusted instruction, external evidence, outcome schema)
observe() → started | progress | usage | approval_needed | finished | failed
interrupt(thread ID, turn ID) → cancellation requested
stop_and_confirm() → durable stop receipt | unknown
```

Runtime status and Goal state are distinct. The adapter reports capabilities such as exact resume and usage support. Unsupported operations return typed failures; they do not silently degrade to a new conversation.

For Codex use private stdio JSON-RPC, capture `thread.id`, later call `thread/resume` then `turn/start`, and interpret `turn/completed` status. A successful interrupt response is not the final completion notification. [Codex evidence](research/codex.md)

## Yield outcome

MVP uses schema-constrained final output, not an experimental tool framework. The finite turn proposes one of `yield`, `complete` or `blocked`, with a bounded summary and typed condition referring to authorized bindings. Example:

```json
{
  "status": "yield",
  "summary": "Published the authorized change; remote CI is pending.",
  "wait": {"kind": "external_event", "binding_id": "binding-id", "condition": "ci_actionable", "generation": 3}
}
```

The server resolves filters from the binding and policy. The model cannot broaden a repository/PR/SHA filter or choose a new worker by writing JSON. An empty or unauthorized condition is rejected. Invalid output produces a typed adapter failure and bounded recovery decision, not an infinite correction loop.

Approvals that arise mid-turn are persisted as human-action requests, then the adapter interrupts/stops. A later approval authorizes a fresh continuation scoped to the recorded action and version; it is not a promise that an in-memory provider approval handle survives shutdown. MVP must never leave a model/runtime alive for an indefinite approval wait.
