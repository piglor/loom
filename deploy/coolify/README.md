# Coolify pre-production packaging

This Compose file packages the currently implemented finite-runtime control plane.
It is **not** a production-ready privileged Codex deployment. See the release gates
before using developer credentials or public-repository workloads.

Use a new Coolify Compose resource from this repository with the root build
context and `deploy/coolify/compose.yaml`. The multi-stage image builds the React
console and static Go binary, then runs as the distroless non-root user with a
read-only root filesystem and no Linux capabilities.

This stack includes a private, single-node OpenBao service. It has persistent
Raft data and audit volumes, no host port, and an explicit `traefik.enable=false`
label. It starts sealed; Loom intentionally does not depend on its health or
require OpenBao credentials at process startup, so unsealing and AppRole setup
can be completed safely before enabling plugin writes. See
[`deploy/openbao/README.md`](../openbao/README.md) for the bootstrap commands.

Supply these in Coolify's secret/environment settings, not in the Compose file:

- `LOOM_POSTGRES_PASSWORD`: fresh random URL-safe password (hex is simplest).
- `LOOM_API_TOKEN`: fresh random administrator credential, at least 32 characters.
- `LOOM_PUBLIC_URL`: canonical HTTPS origin used for GitHub setup callbacks.
- `LOOM_OPENBAO_ADDR`, `LOOM_OPENBAO_MOUNT`, `LOOM_OPENBAO_ROLE_ID` and
  `LOOM_OPENBAO_SECRET_ID`: the private stack uses `http://openbao:8200` after
  the embedded service is initialized, unsealed and given the least-privilege
  AppRole. For an independently hosted OpenBao, use its verified HTTPS URL and
  set `LOOM_OPENBAO_CA_CERT` for a private CA.
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
Do not add a backup sidecar to this Compose stack: backup scheduling, credentials
and retention belong to the deployment control plane.

The default internal Hatchet transport is `hatchet-engine:7070` without TLS,
matching the supplied private-engine topology. This is only for a trusted Docker
network. Override the host and TLS strategy for any other topology; never disable
TLS on an Internet-facing worker connection. The finite control plane has passed
live yield/restart/wake acceptance on Piglor production using a commit-pinned
public Git build context. See [rollout status](../../docs/production-rollout.md)
for the exact deployed revision, image visibility and remaining release gates.

The first Coolify deployment of OpenBao requires an operator bootstrap from the
Coolify service terminal: initialize once, retain the unseal/recovery material
off-host, unseal, enable the `loom` KV v2 mount, write the policy and AppRole,
enable the file audit device, then copy only the AppRole ID/secret into Loom's
Coolify environment variables. Redeploy Loom after setting those values. Never
put the temporary root token in the Compose file or GitHub Actions.

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
