# ADR-0011: Measured execution and suspension, honest cost

**Status:** Proposed
**Date:** 2026-09-08
**Scope:** Loom MVP

## Context and drivers

The product's economic thesis requires evidence. Goal duration, runtime execution and provider inference/billing are different quantities.

## Proposed decision

Persist transition intervals and per-Attempt observations. Show execution, confirmed suspension, scheduling and unknown time separately. Store provider usage with counter scope and dedup identity; unavailable values remain null. Cost estimates require price provenance. Do not ship avoided-token/cost estimates without a measured baseline.

```text
Attempt/transition evidence → measured intervals + usage → labeled derived metrics
```

## Alternatives

Counting all WAITING wall time as savings invents a counterfactual. Counting a missing usage report as zero hides uncertainty. Summing parallel attempt time as Goal elapsed time double counts. Keep measurement definitions explicit.

## Consequences and risks

Actual runtime duration includes tool and network time, not just reasoning. Subscription billing may not yield defensible marginal dollars. Future parallel Sessions need both interval unions and resource-time sums.

## Validation, rollout and reversal

Phase 7 reconciles metrics to audit, deduplicates usage and tests clock gaps/parallel overlap. Preserve raw observations so corrected derivations are reproducible; record pricing/methodology changes without rewriting historical reported facts.

## Evidence

[Cost model](../docs/cost-model.md), [Codex usage evidence and open questions](../docs/research/codex.md). Formulas and presentation rules are Loom recommendations.

All linked moving upstream documentation was accessed on 2026-09-08. Loom recommendations are distinct from upstream guarantees. No runtime test is implied by this ADR.

