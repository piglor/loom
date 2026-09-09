# ADR-0005: Hard affinity for local Sessions

**Status:** Proposed
**Date:** 2026-09-08
**Scope:** Loom MVP

## Context and drivers

Worktrees, uncommitted changes, credentials and provider history can live only on the original machine. Routing to another capable machine can corrupt continuity or authority.

## Proposed decision

Persist a stable Loom worker identity on the Session. Reconnect with an authenticated incarnation and reconcile a local journal. An offline bound worker produces WAITING/worker. Uncertain active execution produces BLOCKED/runtime_status_unknown. Lost local context blocks exact resume.

```text
Session → fixed Worker; offline → wait; reconnect + reconcile → same Session
```

## Alternatives

Capability-only routing is useful for unbound Sessions but insufficient once local context exists. Soft sticky routing can fall back and violates the requirement. Hatchet hard affinity can assist supported workers but does not replace authenticated Loom machine identity.

## Consequences and risks

Availability is lower when the bound machine is absent. Fences stop stale reports but cannot stop external side effects; local locks and process supervision are required. Portability is an explicit future feature.

## Validation, rollout and reversal

Test two workers, stale incarnations and surviving child processes. Migration must be an owner-authorized operation with copied/validated state and no overlapping attempt; no automatic fallback.

## Evidence

[Hatchet worker affinity](https://docs.hatchet.run/v1/advanced-assignment/worker-affinity), [sticky assignment](https://docs.hatchet.run/v1/advanced-assignment/sticky-assignment), [protocol](../docs/protocol.md).

All linked moving upstream documentation was accessed on 2026-09-08. Loom recommendations are distinct from upstream guarantees. No runtime test is implied by this ADR.

