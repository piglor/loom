# ADR-0004: Outbound worker connectivity

**Status:** Proposed
**Date:** 2026-09-08
**Scope:** Loom MVP

## Context and drivers

Developer machines require no public port, SSH reachability or fixed IP. The preferred worker implementation is Rust with narrowly scoped credentials.

## Proposed decision

Use a Rust daemon with authenticated HTTPS long polling, claims and durable reports to Loom. Keep the supported Hatchet worker centrally. The Loom mailbox delivers admitted commands; it does not implement workflow scheduling or timers.

```text
Rust Agent → Loom HTTPS mailbox ← central Hatchet orchestration worker
```

## Alternatives

Native Python/Go Hatchet workers already satisfy outbound connectivity and reduce transport code, but require a companion runtime and assessment of token authority. A Rust implementation of Hatchet's internal protocol creates unsupported compatibility work. Inbound SSH violates the topology requirement.

## Consequences and risks

Loom must own command identity, acknowledgments and reconnect reconciliation. Polling occurs only in inexpensive transport software. Assess a maintained standard HTTP client/server stack during implementation; no new networking framework.

## Validation, rollout and reversal

Phase 3 proves disconnected/restarted worker delivery, idempotent reports and zero foreign claims. A future native SDK transport must preserve worker-scoped authorization and existing command identities.

## Evidence

[Hatchet worker connectivity and SDK research](../docs/research/hatchet.md), [protocol](../docs/protocol.md). This choice is a Loom security/distribution inference, not a limitation of Hatchet outbound transport.

All linked moving upstream documentation was accessed on 2026-09-08. Loom recommendations are distinct from upstream guarantees. No runtime test is implied by this ADR.

