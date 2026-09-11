# Architecture decisions

These records belong to standalone Piglor Loom. Initial records remain Proposed;
the client stack and incremental Go migration were accepted by the user on
2026-09-09. Technology constraints explicitly supplied in the product brief
(including Hatchet and PostgreSQL) are requirements. No external tracker
publication is implied.

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](0001-product-domain-model.md) | Goal as the durable product object | Proposed |
| [0002](0002-hatchet-workflow-runtime.md) | Hatchet beneath Loom | Proposed |
| [0003](0003-postgresql-persistence.md) | PostgreSQL for central domain state | Proposed |
| [0004](0004-worker-outbound-connectivity.md) | Outbound worker connectivity | Proposed |
| [0005](0005-worker-session-affinity.md) | Hard affinity for local Sessions | Proposed |
| [0006](0006-agent-runtime-interface.md) | Finite runtime attempts with exact context binding | Proposed |
| [0007](0007-event-normalization-correlation.md) | Deterministic event correlation | Proposed |
| [0008](0008-github-app-strategy.md) | GitHub App for event observation | Proposed |
| [0009](0009-security-model.md) | Explicit execution authority across trust boundaries | Proposed |
| [0010](0010-rust-loom-agent.md) | Rust machine-side daemon | Proposed |
| [0011](0011-usage-cost-accounting.md) | Measured execution and suspension, honest cost | Proposed |
| [0012](0012-monorepo-clients.md) | React clients and shared contracts in the monorepo | Accepted |
| [0013](0013-go-backend-migration.md) | Incremental Go backend with existing Python writer | Accepted |
| [0014](0014-repeatable-execution-core.md) | Pre-registered repeatable waits (internal experiment) | Proposed |
| [0015](0015-external-event-plugins.md) | Integration-neutral external-event plugins | Proposed |
| [0016](0016-repeatable-worker-protocol.md) | Explicit repeatable outbound worker outcomes | Proposed |
| [0017](0017-contained-codex-attempts.md) | Per-attempt Codex container containment | Proposed |
| [0018](0018-go-only-control-plane.md) | Go-only control-plane and tooling cutover | Accepted |
| [0019](0019-generated-go-persistence.md) | Ent-generated generic persistence, PostgreSQL in both modes | Accepted |
| [0020](0020-openbao-integration-secrets.md) | OpenBao for integration credentials | Accepted |
| [0021](0021-loom-owned-workflow-spec.md) | Loom-owned workflow specification above orchestration adapters | Proposed (implementation authorized) |

Use these alongside the [architecture](../docs/architecture.md) and [implementation gates](../docs/implementation-plan.md). Implement one proof phase at a time. Record acceptance or amendments explicitly; keep superseded decisions for history. Dependency pins and tested operational behavior are evidence produced by implementation, not guesses made in an architecture record.
