# Piglor Loom

**Pay for thinking, not waiting.**

Loom is a durable, event-driven control plane for long-running AI agents. It preserves a Goal while its agent stops, then resumes the correct session on the correct worker when useful work becomes possible.

```text
GOAL → WORKFLOW RUN → AGENT STEP → WAIT → VERIFIED EVENT → NEXT WORKFLOW STEP
                                  └── no agent/model execution while waiting ──┘
```

The durable object is the desired outcome, not a permanently running process. Codex + GitHub is the first reference workflow; deployments, research, customer operations and external jobs use the same primitives.

## Status

Status: **Go-only finite control plane release candidate; privileged Codex remains gated.** PostgreSQL
domain persistence, generic external-event correlation, outbound worker
admission, exact Session binding and active/suspended accounting are implemented
in Go. The browser is React/TypeScript and the machine Agent is Rust.

The Go persistence and HTTP tests prove that an outbound execution can yield,
remain stopped through a control-plane restart, accept a correlated external
approval, and resume the same Loom Session on the same Worker. The control plane
uses the official Hatchet Go SDK to transfer durable dispatch intents. A
standalone Codex adapter previously passed exact-thread continuity. Signed
GitHub workflow ingress and live PR/run freshness validation are implemented. A
full GitHub App-to-Codex production proof remains unfinished. No token or cost
savings are claimed.

## Run locally

The [operator console](docs/console.md) adds a React/TypeScript browser interface
and Go API entry point. It shows real Goals, wait contracts, session/worker
bindings, audit history and recorded timing. Operators can also configure
integration plugins; Goal inspection remains read-only. The console uses the
existing operator credential, and multi-user SSO is not implemented.

With Node 24 and the Go version pinned in `services/loom/go.mod`, run
`make console-setup`, then follow the console guide to start it alongside the
existing API. Native iOS/Android clients are planned, not shipped.

With Go 1.26, Rust 1.97, Node 24, Docker Compose v2 and Hatchet CLI 0.105.16:

```bash
make setup
cp .env.example .env
# Fill .env with fresh local values, then:
make init
make infra
make migrate
make serve
```

Run `make worker` in a second terminal after configuring a reachable Hatchet
gRPC endpoint and scoped client token. Run `make check test` for the maintained
gates and `make prove` for the PostgreSQL lifecycle proof.

The local Hatchet stack is development-only. Your production Hatchet deployment is not modified.

The [outbound Rust worker](docs/remote-worker.md) now supports the finite remote
runtime with scoped credentials and restart-safe delivery. Remaining release
requirements are tracked in [production readiness](docs/production-readiness.md).
See the [Codex conformance evidence](docs/codex-adapter.md) and
[GitHub ingress scope](docs/github-ingress.md) before enabling integrations.

GitHub and GitLab are optional integration plugins, not core dependencies. They
authenticate and normalize evidence; a published Loom workflow decides whether
that evidence starts or continues work. Plugins never directly resume an agent.
See [integration plugins](docs/integration-plugins.md) for the contract and
current limitations. Repeatable outbound execution is opt-in through [worker
protocol 2](docs/repeatable-worker.md).

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

The monorepo uses React web/future React Native clients, a Go control plane,
PostgreSQL/Hatchet, and a Rust Loom Agent. No second backend stack is maintained.
Hatchet remains an implementation dependency; users interact with Loom concepts.

## Contributing

Start with the acceptance evidence in the implementation plan. Each phase must produce reproducible tests before claiming its capability. In particular, an in-memory test cannot establish restart durability, a fake runtime cannot establish real Codex resumption, and elapsed waiting time cannot establish tokens or dollars saved.

ADRs are local to this standalone project. Proposed decisions are design recommendations, not claims of user approval. Hatchet, PostgreSQL, outbound workers, generic Goal semantics and the no-model-waiting requirement are requirements supplied in the product brief.

Secrets belong in a deployment secret store or local ignored environment configuration. Never include provider credentials, Hatchet tokens or session transcripts in commits or test fixtures.

## Container images

Successful `main` CI runs publish `ghcr.io/piglor/loom:sha-<commit>` for Linux
amd64. Publishing is gated on the Go/Rust and browser test jobs;
pull requests cannot publish images. Deploy by the immutable digest recorded in
the workflow summary. No moving release tag is published. Images contain the finite
control plane, not a production-enabled Codex daemon.

The first image publication succeeded. GHCR currently requires authentication;
public source availability does not automatically make the container package
public. See the [rollout record](docs/production-rollout.md) for the tested digest
and deployment mode.

## License

Loom is available under the [MIT License](LICENSE).
