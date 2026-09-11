# Coolify pre-production packaging

This Compose file packages the currently implemented finite-runtime control plane.
It is **not** a production-ready privileged Codex deployment. See the release gates
before using developer credentials or public-repository workloads.

Use one Coolify Compose resource from this repository with the root build
context and `deploy/coolify/compose.yaml`. The resource contains the database,
migration job, Loom server, worker, and a persistent single-node OpenBao. A
first-time installation is therefore one resource and one deploy; OpenBao has
no host port or public proxy route and Loom reaches it at the internal default
`http://openbao:8200`.

OpenBao is initialized and configured automatically by the bundled
`openbao-bootstrap` service on the first deploy. Its companion monitor
re-unseals the convenience-mode instance after restarts, and Loom reads the
generated AppRole files from a private volume. No terminal visit or AppRole
copy/paste is required for the easy path. See
[`deploy/openbao/README.md`](../openbao/README.md) for the advanced manual/KMS,
backup and restore procedures.

Coolify 4.1.2 injects a resource's `.env` file into every service in that
resource. The bundled path is optimized for simple setup, so OpenBao will see
the resource environment (it ignores variables it does not use). If your
threat model requires strict environment isolation, use the advanced
standalone OpenBao deployment described below instead.

Supply these in Coolify's secret/environment settings, not in the Compose file:

- `LOOM_POSTGRES_PASSWORD`: fresh random URL-safe password (hex is simplest).
- `LOOM_API_TOKEN`: fresh random administrator credential, at least 32 characters.
- `LOOM_PUBLIC_URL`: canonical HTTPS origin used for GitHub setup callbacks.
- `LOOM_OPENBAO_MOUNT`: optional mount name (defaults to `loom`). The bundled
  address and AppRole files are already configured; no address or credential
  values are required for the easy path. For the advanced topology, set
  `LOOM_OPENBAO_AUTO_BOOTSTRAP=false`, `LOOM_OPENBAO_ADDR` to a verified HTTPS
  URL, and `LOOM_OPENBAO_CA_CERT` when the private CA is not in the system trust
  store, then provide AppRole credentials through the deployment secret store.
  The bundled default `LOOM_OPENBAO_AUTO_BOOTSTRAP=true` should only be used
  when the Docker host is trusted with the generated convenience-mode seal key.
- `LOOM_GITHUB_APP_INSTALL_URL`: HTTPS installation page for the GitHub App shown
  in the console plugin store.
- `LOOM_GITHUB_WEBHOOK_SECRET`: GitHub App webhook secret, at least 32 characters.
  This is a one-release legacy fallback; new plugin-store connections keep the
  value in OpenBao.
- `LOOM_GITHUB_API_TOKEN`: short-lived, least-privilege installation token when
  private repository validation is required.
- `HATCHET_CLIENT_TOKEN`: a scoped Hatchet worker/client credential.
- `HATCHET_NETWORK`: the exact existing Docker network shared with Hatchet.
- `LOOM_INGRESS_NETWORK`: the Docker network used by Coolify's reverse proxy
  for this resource (the resource UUID network on Coolify 4.1.2).
- `HATCHET_CLIENT_HOST_PORT`: normally `hatchet-engine:7070` on that network.
- `HATCHET_CLIENT_TLS_STRATEGY`: `none` only for the private in-network gRPC hop.

The previously supplied Hatchet network was `bvas8r76zkt83qxe67y08i9f`; verify it
still exists on the same Docker host with `docker network inspect` before using
that value. `external: true` attaches the application to it without recreating or
deleting it. Hatchet's own PostgreSQL and RabbitMQ services are not redeployed.

Set the Loom server's Coolify HTTPS domain to container port 8080. The server is
attached to multiple networks, so `LOOM_INGRESS_NETWORK` must match the network
that Coolify attaches to its proxy; the explicit `traefik.docker.network` label
prevents Traefik from selecting a private network and timing out. No database or
worker inbound port should be published. Restrict administrator routes at the
proxy where appropriate. Remote workers connect only to Loom HTTPS.

Configure PostgreSQL backups in Coolify against the `loom-db` component and
upload them to a validated S3-compatible storage. Keep a small local retention
window as a fallback, configure independent S3 retention, trigger a backup
immediately after setup, and restore that artifact into a disposable database.
Back up OpenBao separately with the Raft snapshot and audit-volume procedure in
[`deploy/openbao/README.md`](../openbao/README.md); database backups alone do
not recover integration credentials.
Do not add a backup sidecar to this Compose stack: backup scheduling, credentials
and retention belong to the deployment control plane.

The default internal Hatchet transport is `hatchet-engine:7070` without TLS,
matching the supplied private-engine topology. This is only for a trusted Docker
network. Override the host and TLS strategy for any other topology; never disable
TLS on an Internet-facing worker connection. The finite control plane has passed
live yield/restart/wake acceptance on Piglor production using a commit-pinned
public Git build context. See [rollout status](../../docs/production-rollout.md)
for the exact deployed revision, image visibility and remaining release gates.

The easy Coolify deployment uses convenience mode: the bootstrap service keeps
only the generated unseal key in its protected volume, revokes the temporary
root token, and never writes root/unseal material to Compose, GitHub Actions or
application logs. This trades the barrier against Docker-host theft for a
zero-touch first run; use the advanced external/KMS topology when that tradeoff
is not acceptable. Do not delete the OpenBao volumes during upgrades.

Coolify 4.1.2 uses `docker compose up ... --build` for custom services. Its parser
adds declared top-level networks to services and injects the service environment
file into containers. The bundled OpenBao service is intentionally private and
shares the Compose lifecycle with Loom. No host database or OpenBao port is
published by this descriptor. For strict isolation or an HA cluster, deploy
[`deploy/openbao/compose.yaml`](../openbao/compose.yaml) (or an equivalent
managed OpenBao) separately and set `LOOM_OPENBAO_ADDR` to its HTTPS endpoint;
the Loom image and plugin configuration do not otherwise change.

Before rollout: rotate credentials pasted into chat; take and restore-test Loom,
Hatchet and OpenBao backups; preserve Hatchet config/secrets and worker journals;
test migration against a copy; verify TLS and revocation; establish audit-log
retention, database/disk alerts and an operator recovery procedure. Do not remove
volumes as a migration or password-reset workaround.
