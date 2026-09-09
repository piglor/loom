# ADR-0008: GitHub App for event observation

**Status:** Proposed
**Date:** 2026-09-08
**Scope:** Loom MVP

## Context and drivers

GitHub is the first external integration. Observation authority and privileged development execution must remain distinct.

## Proposed decision

Use an enrolled GitHub App with read-only Actions, Checks and Pull requests permissions for MVP observation. Bind installation and numeric repository IDs to a project. Verify raw-body signatures and persist deliveries before acknowledgment. Reconcile missed events in control-plane software.

```text
GitHub App → verified inbox → current PR/CI evidence → authorized Loom wait
```

## Alternatives

Repository webhooks with separate credentials can prove a narrow prototype but complicate enrollment and permission management. A broad personal token is tied to a person and grants unnecessary observation authority. Public event scraping cannot establish the required trust or delivery contract.

## Consequences and risks

GitHub does not automatically redeliver failed webhooks; recovery needs its own deterministic path. Installation revocation and API rate limits can suspend progress. Git publishing is separately authorized on the worker; merge authority is outside merge-ready MVP.

## Validation, rollout and reversal

Phase 5 uses a controlled App installation and fixtures for reruns/forks/empty PR associations. Add write permissions only with a new explicit execution need; revoke an installation by disabling bindings and rejecting pending claims.

## Evidence

[GitHub failed delivery handling](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries), [App installation authentication](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app), [research](../docs/research/github.md).

All linked moving upstream documentation was accessed on 2026-09-08. Loom recommendations are distinct from upstream guarantees. No runtime test is implied by this ADR.

