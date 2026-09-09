# ADR-0009: Explicit execution authority across trust boundaries

**Status:** Proposed
**Date:** 2026-09-08
**Scope:** Loom MVP

## Context and drivers

Workers may hold credentials and private network access. Authenticated external content can still be malicious, and a compromised worker must not gain another worker's authority.

## Proposed decision

Enforce owner/project policy, worker-scoped HTTPS authentication, exact Session binding, per-attempt claims/fences, local runtime/workspace restrictions and signed ingress. Keep Hatchet credentials central. Treat external payloads as untrusted evidence, never configuration or new instructions.

```text
External data → verification → authorization → fenced command → local policy → runtime
```

## Alternatives

A shared fleet-wide token cannot isolate workers. Signature-only admission confuses source integrity with execution authorization. Prompt-only restrictions cannot protect privileged files or network resources. Enforce software and OS boundaries.

## Consequences and risks

Same-repository PRs still require explicit task authorization. MVP rejects forks on privileged workers. Leases cannot undo side effects; unknown runtime state blocks reassignment. Secrets need deployment storage, rotation and redacted observability.

## Validation, rollout and reversal

Security tests start with each boundary, not only Phase 8. Revocation prevents new claims and initiates interruption; completion waits for stopped evidence. Expanding public/fork execution requires an explicit isolation decision and proof.

## Evidence

[Security design](../docs/security.md), [GitHub secure use](https://docs.github.com/en/actions/reference/security/secure-use), [Hatchet token visibility](https://docs.hatchet.run/v1/user-roles).

All linked moving upstream documentation was accessed on 2026-09-08. Loom recommendations are distinct from upstream guarantees. No runtime test is implied by this ADR.

