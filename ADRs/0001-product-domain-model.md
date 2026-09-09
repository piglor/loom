# ADR-0001: Goal as the durable product object

**Status:** Proposed
**Date:** 2026-09-08
**Scope:** Loom MVP

## Context and drivers

The product must preserve a desired outcome across many periods of execution and suspension, and later permit multiple agent roles.

## Proposed decision

Own Goal, Run, Loom Session, Worker, Event, Wait and Execution Attempt in Loom. Keep Goal state generic and treat provider context as a Session binding. MVP permits one active Run and one active Attempt per Session; retain one-to-many history.

```text
Goal → Run → Session → Attempts; Run → Waits; Session → Worker + provider thread
```

## Alternatives

Process-centered ownership makes process death look like goal loss. Conversation-centered ownership binds the product to one runtime and obscures completion policy. Goal-centered ownership preserves the requested semantics with explicit projections.

## Consequences and risks

More identities require referential constraints and clear inspection tools. A terminal turn is not terminal Goal authority. Preserve immutable Run policy and audit evidence.

## Validation, rollout and reversal

Phase 2 exercises repeated waits and attempts without changing Goal identity. Additive role/session fields support future multi-agent work; do not migrate existing provider context implicitly.

## Evidence

[Concepts](../docs/concepts.md) and [state machine](../docs/state-machine.md) are the normative proposed Loom semantics, derived from the product brief.

All linked moving upstream documentation was accessed on 2026-09-08. Loom recommendations are distinct from upstream guarantees. No runtime test is implied by this ADR.

