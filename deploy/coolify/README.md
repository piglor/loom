# Coolify pre-production packaging

This Compose file packages the currently implemented finite-runtime control plane.
It is **not** a production-ready privileged Codex deployment. See the release gates
before using developer credentials or public-repository workloads.

Use a new Coolify Compose resource from this repository with the root build context
and `deploy/coolify/compose.yaml`. The Python base image is digest-pinned and the
runtime dependencies are constrained by the tested lockfile. Application services
run as UID 10001, with read-only root filesystems and no Linux capabilities. The
database and worker receipt volumes must persist across redeployments.

Supply these in Coolify's secret/environment settings, not in the Compose file:

- `LOOM_POSTGRES_PASSWORD`: fresh random URL-safe password (hex is simplest).
- `LOOM_API_TOKEN`: fresh random administrator credential, at least 32 characters.
- `HATCHET_CLIENT_TOKEN`: rotated Hatchet credential for this deployment.
- `HATCHET_NETWORK`: the exact existing Docker network shared with the engine.
- `LOOM_GITHUB_WEBHOOK_SECRET`: optional fresh GitHub App webhook secret.

The previously supplied Hatchet network was `bvas8r76zkt83qxe67y08i9f`; verify it
still exists on the same Docker host with `docker network inspect` before using
that value. `external: true` attaches the application to it without recreating or
deleting it. Hatchet's own PostgreSQL and RabbitMQ services are not redeployed.

Set the Loom server's Coolify HTTPS domain to container port 8000. No database or
worker inbound port should be published. Restrict administrator routes at the
proxy where appropriate. Remote workers connect only to Loom HTTPS.

The default internal Hatchet transport is `hatchet-engine:7070` without TLS,
matching the supplied private-engine topology. This is only for a trusted Docker
network. Override the host and TLS strategy for any other topology; never disable
TLS on an Internet-facing worker connection. The finite control plane has passed
live yield/restart/wake acceptance on Piglor production using a commit-pinned
public Git build context. See [rollout status](../../docs/production-rollout.md)
for the exact deployed revision, image visibility and remaining release gates.

## Deploying without a source repository

`python3 deploy/coolify/render_snapshot.py` emits Compose with a compressed,
allowlisted source snapshot in an inline Dockerfile. It requires no third-party
Python modules. Only `pyproject.toml`, `requirements.lock`, and direct Python/SQL
files in `server/loom/` enter the snapshot; `.env`, `.loom`, credentials, Rust
workspaces and unrelated files do not. Source symlinks are rejected. Review the
Dockerfile and generated descriptor before sending it to Coolify's create/update
service API. Secrets remain environment references, not embedded values.

Coolify 4.1.2 uses `docker compose up ... --build` for custom services. Its parser
adds declared top-level networks to services and injects the service environment
file into containers. Do not assume the per-container network/environment lists
in the source descriptor enforce isolation after Coolify transforms it. Use a
dedicated trusted stack; inspect the generated Compose in Coolify before adding
privileged workloads. No host database port is published by this descriptor.

Before rollout: rotate credentials pasted into chat; take and restore-test both
Loom and Hatchet database backups; preserve Hatchet config/secrets and worker
journals; test migration against a copy; verify TLS and revocation; establish log
retention, database/disk alerts and an operator recovery procedure. Do not remove
volumes as a migration or password-reset workaround.
