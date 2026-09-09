# Codex integration research

Accessed 2026-09-08. Official documentation plus local CLI inspection. No model turn, persistence/resume experiment or usage measurement has been run.

## Verified surface

The official app-server page documents private stdio JSON-RPC, connection initialization, `thread/start` returning a thread ID, `thread/resume` by ID, `turn/start`, `turn/interrupt`, terminal `turn/completed` notifications, and `thread/tokenUsage/updated`. A thread's `sessionId` can identify its session-tree root; use `thread.id` as the exact resume target. `outputSchema` supports structured turn output. Dynamic tools remain experimental. Schema generation is version-specific. [OpenAI, Codex App Server](https://learn.chatgpt.com/docs/app-server)

The documented CLI alternative emits JSONL with `thread.started` and turn usage, supports `--output-schema`, and can resume a specific ID with `codex exec resume <SESSION_ID>`. Ephemeral mode omits persisted rollout files. [OpenAI, Non-interactive mode](https://learn.chatgpt.com/docs/non-interactive-mode)

Local read-only inspection on 2026-09-08: installed CLI reports `codex-cli 0.153.4`; `codex app-server --help` lists stdio transport and `generate-json-schema`. This is a compatibility candidate, not a chosen production pin or proof that every moving documentation field exists locally. No credentials were read from the local Codex profile.

Generated the installed stable JSON Schema and verified the needed thread-start, exact-resume, turn-output-schema, completion and usage properties. All five inspected message schemas contained the required adapter fields. See the [validation record](../validation.md); no model inference was used for this check.

## Recommendation and alternatives

Use app-server over a private child-process stdio channel. Loom needs explicit turn state, interrupt and exact thread mapping. Keep model selection configurable under policy; do not select a different model merely because a documentation example does.

Alternative: programmatic `codex exec --json` is a credible simpler proof adapter, especially for finite turns. It is not terminal automation. Adopt it only with tested exact-ID resume, structured output, cancellation and process supervision on the pinned release. Never use `--last` in a multi-Goal worker.

Alternative: the official TypeScript SDK adds a machine-side language runtime alongside Rust. Revisit if its supported semantics materially simplify the adapter. Screen scraping/tmux automation provides no useful advantage over these structured surfaces and is excluded.

## Proposed finite-attempt lifecycle

```text
spawn private app-server
→ initialize / initialized
→ thread/start OR thread/resume(exact thread.id)
→ journal provider binding; server acknowledges
→ turn/start with authorized input and final outcome schema
→ consume item/usage events
→ turn/completed; validate final yield/complete/blocked proposal
→ stop and reap runtime/process tree
→ persist and report stopped outcome
→ no app-server until another attempt is admitted
```

This sequence is Loom's design, not an upstream durability guarantee. Keep rollout storage and workspace affinity on the same Worker. A missing thread is a typed unrecoverable-session condition, never permission to switch to another conversation. Thread creation without a model turn is not itself proof that all desired history is durable.

The output schema should request `yield`, `complete` or `blocked` and a condition bound to authorized resources. Only a successful finite turn with validated output and confirmed process stop may enter WAITING. Do not keep an unanswered dynamic tool/approval request alive through an external wait.

## Required experiments

1. Generate the installed stable protocol schema; verify every method/field used and retain a version/digest record with conformance fixtures.
2. Create a thread, perform a harmless authorized turn in an isolated workspace, record thread and turn IDs, stop app-server, restart it, resume the exact ID and verify continuity using a known artifact/context assertion.
3. Capture complete, failed and interrupted turns. Test approval-needed handling, malformed output, EOF mid-turn and failure after provider acceptance but before acknowledgment.
4. Ensure stop supervision catches surviving children/background activity; demonstrate unchanged runtime/provider counters during a prolonged wait.
5. Test local journal recovery before thread creation, after binding but before turn start, after start but before turn-ID receipt, and after completion but before server acknowledgment.
6. Inspect actual usage notification scope, duplicate behavior and counter reset/compaction. Retain unknown accounting where semantics cannot be established. No assumed dollar savings.
7. Verify local policy against repository-controlled configuration, hooks, tools and credentials. No public app-server listener and no broad inherited environment by default.

The product's acceptance evidence must name the exact tested binary and protocol schema. Official API availability alone cannot prove Loom recovery, zero model waiting or secure execution.
