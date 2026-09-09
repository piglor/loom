# Production release gates

Status: in progress, not a production release. A passing finite-runtime demo is
not evidence of a safe privileged coding agent.

Implemented since the initial demo: scoped revocable worker credentials; Rust
outbound daemon and private SQLite journal; real remote affinity/restart proof;
standalone Codex exact-thread continuity proof with actual model turns; signed
GitHub workflow ingress, deterministic bindings, retained early-event evidence,
and negative authorization/correlation tests. Those boundaries are tested
individually, not yet as the complete unattended coding workflow.

Local packaging also passed a non-root/read-only API smoke test and a scoped
PostgreSQL restore drill. These do not substitute for a deployment-specific TLS,
secret-rotation, Hatchet recovery, worker containment and operational acceptance run.

The implementation target is initially a single-organization Linux deployment
with administrator-controlled worker enrollment. Production acceptance requires:

- Scoped, revocable worker authentication; outbound connectivity; explicit
  worker/session affinity; durable command and stop-report recovery.
- A real Codex turn, exact provider-session resumption after process termination,
  local workspace policy, bounded execution, and confirmed process-tree shutdown.
- Signed GitHub App webhooks, explicit installation/repository/PR/head identity,
  generation freshness, fork rejection, and a real authorized PR/CI round trip.
- Repeated yields, policy-governed completion, cancellation and safe handling of
  ambiguous execution; timers and human input without active inference.
- Container deployment, TLS, secret rotation, least privilege, health checks,
  backup/restore drill, upgrade checks, operational alerts and release CI.

Live acceptance needs an explicitly authorized disposable GitHub repository,
GitHub App installation and secret-store configuration, and a designated Codex
worker/workspace. These are not inferred from example repository names or the
production Hatchet credential. Production deployment changes require a resolved
target; local implementation and tests may proceed independently.

Previously pasted service passwords and tokens should be rotated before a
production rollout. Keep replacement values in the deployment secret store.
