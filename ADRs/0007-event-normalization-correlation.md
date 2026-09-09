# ADR-0007: Deterministic event correlation

**Status:** Proposed
**Date:** 2026-09-08
**Scope:** Loom MVP

## Context and drivers

External deliveries may arrive early, late, repeatedly or out of order. A signed event can still describe the wrong version or unauthorized work.

## Proposed decision

Persist a versioned normalized Event and authorized external binding. Correlate integration/resource/version/generation to Run, Session and Worker in software. Use unique inbox receipts, semantic Wait satisfaction, audit dispositions and outbox continuation identities.

```text
Verified receipt → authorized binding → current Wait generation → one continuation
```

## Alternatives

LLM routing is nondeterministic and consumes inference on untrusted data. Branch-name routing is ambiguous. Delivery-ID-only deduplication does not prevent distinct deliveries of equivalent evidence or stale run attempts. Use explicit versioned bindings.

## Consequences and risks

Some payloads cannot identify a unique PR; keep them unresolved and reconcile through the source API. Arming a Wait checks existing evidence. The external API and Loom transaction cannot be atomic, so revalidate before consequential execution.

## Validation, rollout and reversal

Phase 2 tests generic duplicates and early events; Phase 5 adds repository/PR/SHA/run-attempt fixtures. Envelope versions are additive; do not reinterpret historical evidence under a changed policy without an audited re-evaluation.

## Evidence

[GitHub research](../docs/research/github.md), [architecture race protocol](../docs/architecture.md), [data model](../docs/data-model.md).

All linked moving upstream documentation was accessed on 2026-09-08. Loom recommendations are distinct from upstream guarantees. No runtime test is implied by this ADR.

