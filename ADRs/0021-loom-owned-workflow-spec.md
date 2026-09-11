# ADR-0021: Loom-owned workflow specification above orchestration adapters

**Status:** Accepted | **Wave:** workflow foundation | **Deciders:** core team | **Date:** 2026-09-12

Related: [ADR-0002](0002-hatchet-workflow-runtime.md) · [ADR-0007](0007-event-normalization-correlation.md) · [ADR-0015](0015-external-event-plugins.md)

---

## Context

The current GitHub adapter resolves a verified event directly into a Goal-level
`wake`, and the Hatchet worker exposes one hard-coded dispatch task. That makes
an integration appear to control execution and leaves no Loom-owned definition
that operators, clients, or AI tools can inspect and author.

The decision is whether Loom owns a portable workflow contract or exposes the
selected scheduler's workflow model as its product API.

## Decision drivers

- Plugins authenticate, normalize, and deliver evidence; they do not schedule agents.
- Workflow relationships must be inspectable on web and future native clients.
- Historical runs must replay against the immutable definition that started them.
- Hatchet must be replaceable without migrating Loom's public API or durable policy.
- Tenant authorization and event correlation remain authoritative in PostgreSQL.

## Evidence and alternatives

| Option                                   | Evidence                                                                                                                                                                                                              | Assessment                                                                                                                                                             |
| ---------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Loom-owned versioned DAG                 | JSON Schema defines a portable vocabulary for validating JSON documents [S1]. Hatchet's Go SDK supports parent-linked tasks [S2] and durable event waits [S3], so an adapter can compile the Loom relationship model. | Selected. Loom owns intent and audit; the adapter owns execution mechanics.                                                                                            |
| Expose Hatchet workflows directly        | Hatchet provides workflows, tasks, and run metadata through its SDK [S2].                                                                                                                                             | Rejected. It couples public identifiers, authoring, and migration to the current engine and makes mobile clients depend on scheduler concepts.                         |
| Adopt CNCF Serverless Workflow wholesale | The CNCF specification defines a vendor-neutral workflow DSL and schemas [S4].                                                                                                                                        | Rejected for v1. Its broad function/event model exceeds Loom's narrow agent/wait/subflow safety contract; compatibility can be considered later through import/export. |

## Proposed decision

Loom owns organization-scoped drafts and immutable workflow versions. Schema
version 1 publishes typed `agent`, `wait_event`, `condition`, `subflow`, and
`complete` steps joined by explicit success/failure edges. A condition evaluates
an allow-listed path in the run context (trigger and verified event details).
A subflow pins the latest published child version, creates a persisted child
run, and resumes its parent only after that child completes. A published
workflow is a finite DAG with exactly one entry step and at least one reachable
completion step.

```text
Plugin adapter -> verified IntegrationReceipt -> Loom trigger/wait matcher
                                                |
                                                v
                                  versioned WorkflowRun + outbox intent
                                                |
                                                v
                                  Orchestrator port -> Hatchet adapter
```

An integration event may start a workflow through an explicit trigger binding
or satisfy an exact workflow wait. Only an `agent` step creates or resumes an
agent attempt. Raw webhook bodies and credentials never cross the orchestration
boundary.

The public JSON contract contains no Hatchet types. Dispatches give Hatchet the
compiled Loom topology plus immutable version and step identifiers, and return
opaque orchestration references for diagnostics. Operators and tools retrieve
the canonical version from Loom's API. Loom persists identity, relationships,
authorization decisions, and audit; Hatchet supplies execution telemetry.

## Consequences and risks

- Publication compiles and pins an immutable topology. The Hatchet adapter
  consumes that topology on each outbox dispatch while one stable worker task
  remains registered; Loom, not Hatchet, evaluates relationships and advances
  the next step.
- Loom must validate cycles, reachability, typed configuration, and tenant-scoped
  integration bindings before publication.
- PostgreSQL and Hatchet cannot commit atomically; a transactional outbox and
  idempotent run keys provide retryable transfer while Loom remains authoritative.
- The initial compiler intentionally keeps the condition language small and
  treats child runs as sequential, bounded subflows; parallel fan-out remains
  out of scope.

## Migration and rollout

Add workflow tables and nullable run metadata. Migration 020 backfills missing
legacy root Run/Session rows before compatibility dispatch. Existing runs remain
`legacy` and retain `loom-dispatch-v1` until drained. New plugin UI and APIs use
workflow trigger bindings. Remove Goal-level integration bindings only after
compatibility traffic reaches zero.

## Non-goals

- A general-purpose arbitrary-code workflow language.
- A Hatchet graph embedded in the Loom console.
- Built-in AI workflow generation in v1; external agents use the same schema/API.

## Sources

- **[S1]** JSON Schema, “Draft 2020-12,” accessed 2026-09-12: https://json-schema.org/draft/2020-12 — JSON vocabulary and validation contract.
- **[S2]** Hatchet, “Go SDK Runnables,” accessed 2026-09-12: https://docs.hatchet.run/reference/go/runnables — workflows, tasks, parents, and run metadata.
- **[S3]** Hatchet, “Go SDK Context,” accessed 2026-09-12: https://docs.hatchet.run/reference/go/context — durable task context and event waiting.
- **[S4]** CNCF Serverless Workflow, specification repository, accessed 2026-09-12: https://github.com/serverlessworkflow/specification — portable workflow DSL and schemas.
