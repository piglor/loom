# ADR-0010: Rust machine-side daemon

**Status:** Proposed
**Date:** 2026-09-08
**Scope:** Loom MVP

## Context and drivers

The agent should have a small idle footprint, run on user-owned machines and manage process/journal correctness. Rust is the preferred language in the product brief.

## Proposed decision

Implement Loom Agent and protocol/domain values in Rust; introduce a thin CLI when functional APIs exist. Keep the initial Codex adapter in the agent. Use maintained HTTP/async/serialization dependencies, a local SQLite receipt journal and OS process/session locks, with versions selected during Phase 3.

```text
Rust daemon → local journal + policy + process supervisor → Codex adapter
```

## Alternatives

An all-Python product simplifies the first server but compromises the requested machine-side distribution. A Go daemon is a credible single-binary alternative but departs from the requested preference without a demonstrated need. Rust throughout the server would add unsupported Hatchet SDK work.

## Consequences and risks

Rust does not automatically make process supervision or secret handling correct. Two languages require wire fixtures and compatibility tests. Cross-platform behavior must be tested per OS; Linux is the initial proof target.

## Validation, rollout and reversal

Phase 3 measures idle process behavior and tests crash/reconnect. Phase 4 proves runtime shutdown. Extract crates only when independent ownership/testing warrants it. Revisit journal dependency or packaging via a focused implementation decision.

## Evidence

This is a recommendation from the product requirements and [protocol design](../docs/protocol.md); [Hatchet SDK research](../docs/research/hatchet.md) supports separating the central SDK language.

All linked moving upstream documentation was accessed on 2026-09-08. Loom recommendations are distinct from upstream guarantees. No runtime test is implied by this ADR.

