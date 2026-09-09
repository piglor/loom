# ADR-0006: Finite runtime attempts with exact context binding

**Status:** Proposed
**Date:** 2026-09-08
**Scope:** Loom MVP

## Context and drivers

Codex is first, but runtime-independent Goal semantics must outlive it. Loom needs structured control and a verifiable end to every reasoning interval.

## Proposed decision

Use a small adapter around create/resume context, start/observe turn, interrupt and stop confirmation. Implement Codex through private app-server stdio, storing thread.id separately from Loom Session and provider session-tree ID. Use a schema-constrained final outcome for MVP yield.

```text
Loom Attempt → exact provider thread → finite turn → stopped receipt → Wait or policy result
```

## Alternatives

Programmatic codex exec JSONL is a credible simpler alternative with fewer interactive controls. A TypeScript SDK companion adds a second machine-side runtime. Terminal scraping is unnecessary given structured upstream interfaces. A broad plugin framework adds contracts before a first adapter is proven.

## Consequences and risks

Runtime completion is only an outcome proposal; stop evidence and policy determine Loom transitions. Provider schema changes require conformance tests. Mid-turn approvals must become stopped human waits rather than permanently live runtime requests.

## Validation, rollout and reversal

Phase 4 pins a tested binary/schema and demonstrates real context continuity after process termination. Unknown launch outcomes block instead of blindly resending turn/start. New runtime implementations must pass the same conformance suite.

## Evidence

[Official app-server](https://learn.chatgpt.com/docs/app-server), [noninteractive CLI](https://learn.chatgpt.com/docs/non-interactive-mode), [local schema evidence](../docs/research/codex.md).

All linked moving upstream documentation was accessed on 2026-09-08. Loom recommendations are distinct from upstream guarantees. No runtime test is implied by this ADR.

