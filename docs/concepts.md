# Concepts

Status: proposed design, 2026-09-08.

## Domain vocabulary

| Concept | Meaning | Lifetime / ownership |
| --- | --- | --- |
| Goal | Desired outcome, objective and explicit completion criteria | Owned by a principal within a project and organization; survives execution failures |
| Run | A durable pursuit of one Goal under a versioned policy | May span many attempts and waits; a new Run records an explicit retry/restart of the pursuit |
| Loom Session | Logical context for one agent role | Belongs to a Run; binds runtime type, provider thread and worker |
| Worker | Registered execution environment | Stable server-issued identity; process restarts use a new incarnation, not a new machine identity |
| Agent Runtime | Adapter to a reasoning/execution provider | Creates/resumes context, drives finite execution and reports its outcome |
| Event | Validated occurrence from an integration, timer, human or dependency | Immutable receipt plus normalized evidence; never an implicit execution permission |
| Wait | Durable condition preventing useful work | Belongs to a Run and generation; satisfied, cancelled or superseded explicitly |
| Execution Attempt | One authorized active runtime interval | Has a unique command, fencing epoch, cause, outcome and usage observations |
| Binding | Authorized mapping from an external resource/version to a Run | Versioned; determines routing without a model |
| Policy | Explicit rules for authority, completion, retries and budgets | Snapshotted on a Run, changed only by an authorized recorded action |

MVP allows one nonterminal Run per Goal and one nonterminal Attempt per Session. These are implementation limits, not a claim that a Goal can only ever involve one agent. Session roles allow later implementation/review/integration agents without changing Goal identity.

## Invariants

1. A Goal can remain under pursuit while all its executions are stopped. `ACTIVE` is a derived user-facing grouping, not an additional persisted state.
2. Confirmed `WAITING` requires no live or unaccounted-for runtime execution for the Run. A yield proposal or expired lease alone is insufficient.
3. The next attempt must be justified by initial authorization, actionable evidence, an authorized human action, or a bounded recovery decision. A timer to recheck unchanged CI is handled by software, not an LLM.
4. A worker-bound Session never moves implicitly. A missing machine causes `WAITING` with reason `worker`; missing provider history causes `BLOCKED` with reason `session_unrecoverable`.
5. Events cannot nominate a worker, provider thread or new authority. Those come from persisted authorized mappings.
6. At-least-once delivery is expected. One wait generation has at most one effective satisfaction and one resulting logical continuation.
7. An agent proposes completion; deterministic policy decides whether criteria are met using current evidence.
8. Usage unknowns remain unknown. No invented baseline or inference-savings counter.

## Logical versus provider session

```text
Goal → Run → Loom Session(role=implementer)
                    ├── Worker ID + workspace binding
                    └── runtime=codex + provider thread ID
```

The provider's session-tree identifier, turn ID and Loom Session ID are separate fields. Never resume `--last`. A runtime that cannot resume exactly must advertise that limitation; the MVP blocks instead of silently creating new context.

## Structured waiting

```json
{
  "state": "WAITING",
  "reason": "external_event",
  "wait_id": "wait-example",
  "generation": 3,
  "condition": {
    "source": "github",
    "type": "workflow.completed",
    "binding_id": "binding-example",
    "expected_version": "abc123"
  },
  "execution_state": "stopped"
}
```

`WAITING_CI` and `WAITING_REVIEW` are optional workflow labels derived from these fields. Deployment and customer-reply waits need no new core states. A human approval wait is still suspended execution; the attention flag can independently be `needs_human`.
