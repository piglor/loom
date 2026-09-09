# ADR-0002: Hatchet beneath Loom

**Status:** Proposed
**Date:** 2026-09-08
**Scope:** Loom MVP

## Context and drivers

Hatchet is a required initial dependency. Loom must reuse durable orchestration while remaining a product with independent authorization and domain history.

## Proposed decision

Use an official Python SDK worker centrally. Hatchet owns durable waits, timers, scheduling and safe infrastructure retries. Loom owns attempt admission, policy and evidence. Transfer intents with a transactional outbox and never call a model inline in replayable orchestration code.

```text
Loom transaction → outbox → Hatchet workflow → idempotent Loom attempt admission
```

## Alternatives

A native Hatchet model exposed to users loses Loom's product boundary. Rebuilding orchestration over PostgreSQL duplicates the selected engine. A central supported SDK meets the requirement while isolating engine-specific APIs.

## Consequences and risks

At-least-once execution and replay require Loom idempotency at every side-effect boundary. Finite event lookback cannot be the only source of wake truth. Engine/SDK pins remain an implementation gate, not an assumed compatible latest pair.

## Validation, rollout and reversal

Phase 2 must prove engine/orchestration restart, eviction/replay, event-before-wait and duplicate launch prevention. Version workflow definitions; retain old definitions until active Runs drain. Replacing Hatchet requires a new migration decision.

## Evidence

[Hatchet guarantees](https://docs.hatchet.run/v1/architecture-and-guarantees), [durable event waits](https://docs.hatchet.run/v1/durable-event-waits), [research and alternatives](../docs/research/hatchet.md).

All linked moving upstream documentation was accessed on 2026-09-08. Loom recommendations are distinct from upstream guarantees. No runtime test is implied by this ADR.

