# Validation record

Date: 2026-09-09. This records observed results and does not upgrade unfinished
work into a production claim.

## Current Go-only tree

- No maintained `.py` source, retired packaging manifest, virtual environment,
  package cache, build step, runtime command, test command or container runtime
  remains. `make no-python` and CI reject their reintroduction.
- `go test -race ./...` passes for the control plane, generated Ent persistence,
  native HTTP API, generic integration inbox/bindings, worker/session admission,
  outbox transfer and Hatchet relay behavior.
- Rust formatting, Clippy with warnings denied, workspace tests and six Codex
  protocol conformance tests pass. The deterministic provider fixture is Node.js
  and never invokes a model.
- All 72 browser tests pass across Chromium, Firefox, WebKit and a 320-pixel
  mobile viewport. These are browser tests, not native mobile certification.
- The multi-stage container builds successfully, runs as UID/GID 65532 in a
  distroless final image, serves readiness/health, creates Workers and Goals
  through the native API, and reads the created Goal back from PostgreSQL. The
  image gate also exercises signed GitHub routing and restores a real custom
  PostgreSQL archive into a second database.
- The native Go GitHub adapter passes the official signature vector, tampered
  body rejection, internal-PR identity checks, fork/ambiguous-PR rejection and
  live PR/run freshness tests. Repository hooks remain restricted to finite
  runtimes; privileged Codex requires a GitHub App installation identity.
- The official Hatchet CLI 0.105.16 lists workflows through
  `https://hatchet.piglor.com`. Its advertised gRPC address is intentionally the
  private `hatchet-engine:7070`, reachable only from the deployment network.
- The official Hatchet Go SDK worker registered `loom-dispatch-v1` against the
  real local Hatchet engine and shut down cleanly.
- The generic PostgreSQL proof passes: execution yields, no command is available
  while waiting, a fresh Store represents control-plane restart, an authorized
  external approval is deduplicated/correlated, and the exact Loom Session and
  Worker continue.
- Current production secrets stored in ignored `.loom/production.env` do not
  occur in tracked content.

## Honest release boundary

The live GitHub PR → CI → exact Codex Session continuation has not yet passed in
the Go-only composition. The finite control plane is deployable with privileged
Codex disabled; do not represent that bounded rollout as completion of the
unattended coding-agent release gate. No token or cost avoidance estimate is
claimed.

Historical checkpoints remain in ADRs and `docs/production-rollout.md`; they are
decision/evidence records, not setup instructions for the maintained stack.
