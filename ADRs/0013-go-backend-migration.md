# ADR-0013: Incremental Go backend migration

**Status:** Accepted
**Date:** 2026-09-09
**Decision owner:** User; authorized implementation of the recommended stack.

## Decision

Target a Go modular monolith, independently runnable API and orchestration roles,
PostgreSQL persistence, and the official Hatchet Go SDK. Preserve the existing
Python workflow implementation until compatibility and recovery gates pass.

First introduce a Go console/API entry point with native PostgreSQL read APIs.
Forward existing mutation, webhook and worker contracts to the Python service
without changing authentication headers, payloads or retry behavior. There is
one authoritative writer during this transition; Go must not duplicate admission.

```text
Client → Go entry point → PostgreSQL (read-only transactions)
                       → Python API → existing Hatchet workflows (writes)
```

## Alternatives and evidence

Retaining Python is the lowest migration risk and remains the rollback path.
A Rust API is viable, but adds an official-SDK orchestration bridge or reliance
on an unofficial SDK. PocketBase introduces SQLite and a single-server scaling
model instead of the required PostgreSQL foundation.

[Go HTTP routing](https://go.dev/blog/routing-enhancements),
[Hatchet Go durable context](https://docs.hatchet.run/reference/go/context),
[PocketBase limitations](https://pocketbase.io/faq/), and
[Rust SDK status](https://docs.rs/hatchet-sdk/latest/hatchet_sdk/) were reviewed
2026-09-09. See [assessment](../docs/research/platform-stack.md).

## Compatibility and security

Use the existing organization-scoped JSON contracts and constant-time operator
token validation. Worker credentials remain independently validated by the
existing API. No retries of mutation requests in the gateway. Restrict upstream
configuration to an operator-specified fixed origin; never accept a client
destination. Serve only built frontend assets, not the repository.

## Rollout and rollback

Validate reads against the existing API on an isolated test database. Run race,
browser, container, durable restart, worker reconnect and backup/restore tests.
Route traffic through Go only after these pass. Rollback routes directly to
Python without changing persisted workflow identities or database writes.

Later prove a pinned Go SDK against the deployed Hatchet engine. Drain or
explicitly migrate existing Python workflows before removing their worker.
A gateway is not a completed Go orchestration migration. Credential rotation,
OIDC, multi-user authorization and full real-agent acceptance remain explicit
production gates rather than implied benefits of the language change.
