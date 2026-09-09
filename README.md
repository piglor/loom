# Piglor Loom

**Pay for thinking, not waiting.**

Loom is a durable, event-driven control plane for long-running AI agents. It preserves a Goal while its agent stops, then resumes the correct session on the correct worker when useful work becomes possible.

```text
RUN → YIELD → STOP → WAIT → EVENT → WAKE → RESUME
                     └── no agent/model execution ──┘
```

The durable object is the desired outcome, not a permanently running process. Codex + GitHub is the first reference workflow; deployments, research, customer operations and external jobs use the same primitives.

## Status

Phase 2: **runnable local durable-core development release**. Includes an authenticated API and CLI, PostgreSQL domain state, Hatchet durable waits, deterministic event correlation, audit history and active/suspended timing. The finite demo runtime invokes no model.

The live restart proof preserves a stopped wait through API, worker and Hatchet engine restarts, then resumes the same logical Session exactly once. An outbound Rust finite-runtime worker and signed GitHub workflow ingress are now implemented; a standalone Codex adapter passed exact-thread continuity with real model turns. The finite control plane is [deployed and acceptance-tested](docs/production-rollout.md); **unattended Codex execution and the full production release are not ready.** No token or cost savings are claimed. Implementation proceeds through [eight proof gates](docs/implementation-plan.md).

## Run locally

With Linux, Python 3.12, Docker Compose v2 and Hatchet CLI 0.105.16 installed:

```bash
make setup
make init
make infra
make migrate
make serve
```

Run `make worker` in another terminal. Follow the [quickstart](docs/quickstart.md) to create a Goal, observe suspension and send its matching event. Run `make check test` for checks and `make prove` for the live restart proof (stop manual API/worker processes first).

The local Hatchet stack is development-only. Your production Hatchet deployment is not modified.

The [outbound Rust worker](docs/remote-worker.md) now supports the finite remote
runtime with scoped credentials and restart-safe delivery. Remaining release
requirements are tracked in [production readiness](docs/production-readiness.md).
See the [Codex conformance evidence](docs/codex-adapter.md) and
[GitHub ingress scope](docs/github-ingress.md) before enabling integrations.

## Design

- [Concepts and invariants](docs/concepts.md)
- [Architecture and recovery](docs/architecture.md)
- [State machine](docs/state-machine.md)
- [Data model](docs/data-model.md)
- [Worker and runtime protocol](docs/protocol.md)
- [Security](docs/security.md)
- [Usage and cost](docs/cost-model.md)
- [Reference workflows](docs/workflows.md)
- [Architecture decisions](ADRs/README.md)
- [Validation and live connection status](docs/validation.md)
- Upstream research: [Hatchet](docs/research/hatchet.md), [Codex](docs/research/codex.md), [GitHub](docs/research/github.md)

The proposed stack is a Python control plane using Hatchet's official SDK, central PostgreSQL, and a Rust Loom Agent. Hatchet remains an implementation dependency; users interact with Goals, Runs, Sessions, Workers, Events, Waits and Execution Attempts.

## Contributing

Start with the acceptance evidence in the implementation plan. Each phase must produce reproducible tests before claiming its capability. In particular, an in-memory test cannot establish restart durability, a fake runtime cannot establish real Codex resumption, and elapsed waiting time cannot establish tokens or dollars saved.

ADRs are local to this standalone project. Proposed decisions are design recommendations, not claims of user approval. Hatchet, PostgreSQL, outbound workers, generic Goal semantics and the no-model-waiting requirement are requirements supplied in the product brief.

Secrets belong in a deployment secret store or local ignored environment configuration. Never include provider credentials, Hatchet tokens or session transcripts in commits or test fixtures.

## Container images

Successful `main` CI runs publish `ghcr.io/piglor/loom:sha-<commit>`
for Linux amd64. Publishing is gated on the durable-core test and recovery job;
pull requests cannot publish images. Deploy by the immutable digest recorded in
the workflow summary. No moving release tag is published. Images contain the finite
control plane, not a production-enabled Codex daemon.

The first image publication succeeded. GHCR currently requires authentication;
public source availability does not automatically make the container package
public. See the [rollout record](docs/production-rollout.md) for the tested digest
and deployment mode.

## License

Loom is available under the [MIT License](LICENSE).
