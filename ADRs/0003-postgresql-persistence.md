# ADR-0003: PostgreSQL for central domain state

**Status:** Proposed
**Date:** 2026-09-08
**Scope:** Loom MVP

## Context and drivers

Goals and waits must survive all application processes stopping. Hatchet persistence cannot substitute for Loom's policy, identity and usage records.

## Proposed decision

Use PostgreSQL for Loom domain tables, constraints, inbox/outbox and append-only audit. Separate Loom and Hatchet databases/roles even when sharing one database service. Perform domain state, audit and outbox writes in one transaction.

```text
Event → [Loom state + audit + outbox transaction] → asynchronous engine effect
```

## Alternatives

In-memory state is not durable. SQLite is appropriate for local worker receipts but is not the selected central service. Storing all Loom state in Hatchet task payloads couples retention/schema and prevents coherent domain authorization.

## Consequences and risks

Backups must cover both databases and configuration; local worker disk remains a distinct durability boundary. Cross-service effects are not atomic. Keep external calls outside row-lock transactions and compare versions on commit.

## Validation, rollout and reversal

Apply forward migrations under a dedicated migration role. Prove database restore and concurrency constraints. Prefer additive changes; destructive rollback uses a tested backup and explicit handling of active Runs.

## Evidence

[PostgreSQL constraints](https://www.postgresql.org/docs/current/ddl-constraints.html), [explicit locks](https://www.postgresql.org/docs/current/explicit-locking.html), [data model](../docs/data-model.md).

All linked moving upstream documentation was accessed on 2026-09-08. Loom recommendations are distinct from upstream guarantees. No runtime test is implied by this ADR.

