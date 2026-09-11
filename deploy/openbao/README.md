# Loom OpenBao module

This is the self-contained OpenBao module for Loom integration credentials. It
uses a pinned OpenBao image, integrated Raft storage and private networking. It
does not expose port 8200 to the host or to the public proxy. The production
Coolify resource is [a separate descriptor](../coolify/openbao.compose.yaml),
so Coolify does not copy Loom's application environment into the secret server.

```sh
docker compose -f deploy/openbao/compose.yaml up -d
docker compose -f deploy/openbao/compose.yaml exec openbao bao operator init
docker compose -f deploy/openbao/compose.yaml exec openbao bao operator unseal
```

Store every unseal and recovery key outside the host and outside source
control. OpenBao is sealed after initialization and after a restart until an
operator unseals it. This module is a single-node deployment, not an HA
topology.

After unsealing, authenticate the CLI with the temporary root token and run:

```sh
bao secrets enable -path=loom kv-v2
bao write loom/config max_versions=10
bao policy write loom /openbao/config/loom-policy.hcl
bao auth enable approle
bao write auth/approle/role/loom token_policies=loom token_ttl=1h token_max_ttl=4h secret_id_ttl=720h secret_id_num_uses=0
bao read auth/approle/role/loom/role-id
bao write -f auth/approle/role/loom/secret-id
bao audit enable file file_path=/openbao/logs/audit.log
```

The mount-wide retention limit is a safety net for rotations. A soft delete
keeps the current version recoverable; only an operator may permanently
destroy metadata or individual versions. The SecretID is reusable so Loom can
re-authenticate after a restart or token expiry, but it expires after 30 days;
rotate it at least monthly (or immediately after an operator change).

Put the role ID and one active SecretID in the Loom deployment secret store,
then set:

```text
LOOM_OPENBAO_ADDR=http://openbao:8200
LOOM_OPENBAO_MOUNT=loom
LOOM_OPENBAO_ROLE_ID=<role id>
LOOM_OPENBAO_SECRET_ID=<secret id>
```

After bootstrap, revoke the temporary root token (`bao token revoke -self`)
from that session or replace it with a short-lived operator token. Do not put
any root or operator token in Compose, `.env`, PostgreSQL or logs. Loom stores
only opaque secret references in PostgreSQL; provider values remain in OpenBao.

## Back up and restore

Take a Raft snapshot at least daily and before upgrades. Use a short-lived
operator token in `BAO_TOKEN` for this command; never place that token in a
Compose file or committed environment. Copy the snapshot off the host before
removing the temporary file; a snapshot is encrypted by OpenBao's barrier and
still requires the source cluster's unseal/recovery material to restore.

```sh
stamp=$(date -u +%Y%m%dT%H%M%SZ)
docker compose -f deploy/openbao/compose.yaml exec -T -e BAO_TOKEN="$OPENBAO_OPERATOR_TOKEN" openbao \
  bao operator raft snapshot save "/openbao/logs/raft-${stamp}.snap"
docker compose -f deploy/openbao/compose.yaml cp \
  "openbao:/openbao/logs/raft-${stamp}.snap" "./raft-${stamp}.snap"
sha256sum "./raft-${stamp}.snap"
```

Upload the snapshot and checksum to the same protected backup location as the
Loom database, with a defined retention window. Restore only into a disposable
OpenBao service first, using the matching image version and both the temporary
target and source unseal material. The target must be initialized and unsealed
before the force restore; the force restore then seals it and replaces its
barrier with the source cluster's state.

```sh
docker compose -p loom-openbao-restore -f deploy/openbao/compose.yaml up -d
docker compose -p loom-openbao-restore -f deploy/openbao/compose.yaml exec -T openbao \
  bao operator init -key-shares=1 -key-threshold=1
# Save the target key and temporary target root token, then unseal with that key.
docker compose -p loom-openbao-restore -f deploy/openbao/compose.yaml exec -T openbao \
  bao operator unseal "$TARGET_UNSEAL_KEY"
docker compose -p loom-openbao-restore -f deploy/openbao/compose.yaml exec -T openbao \
  sh -c 'cat > /tmp/restore.snap' < "./raft-${stamp}.snap"
docker compose -p loom-openbao-restore -f deploy/openbao/compose.yaml exec -T \
  -e BAO_TOKEN="$TARGET_ROOT_TOKEN" openbao \
  bao operator raft snapshot restore -force /tmp/restore.snap
docker compose -p loom-openbao-restore -f deploy/openbao/compose.yaml restart openbao
docker compose -p loom-openbao-restore -f deploy/openbao/compose.yaml exec -T openbao \
  bao operator unseal "$SOURCE_UNSEAL_KEY"
docker compose -p loom-openbao-restore -f deploy/openbao/compose.yaml exec -T \
  -e BAO_TOKEN="$SOURCE_ROOT_TOKEN" openbao bao kv get -mount=loom \
  organizations/<hash>/plugins/github/credentials/<id>
```

The restored cluster uses the source root token and unseal key; revoke the
temporary target token after the drill and destroy the disposable resource
without touching production volumes.

Keep the audit volume in the deployment backup plan as well. Rotate
`/openbao/logs/audit.log` at least monthly (disable the file audit device,
archive the file to protected storage, then re-enable it) and alert on disk
usage; never truncate it while the audit device is active.

For a separate OpenBao host, terminate verified HTTPS (or configure OpenBao
TLS directly) and set `LOOM_OPENBAO_ADDR` to that URL. The application does not
depend on a Docker service named `openbao`; only the local Lite overlay uses
that internal DNS name. The Coolify descriptor intentionally publishes the
OpenBao API through a dedicated HTTPS domain and has no host port.

The production Coolify descriptor is a separate resource because the installed
Coolify 4.1.2 Compose parser reads declared services and injects each resource's
environment file into every service. Create/update that resource from
`deploy/coolify/openbao.compose.yaml`; set its HTTPS domain to the same value
used by `OPENBAO_API_ADDR`, and set Loom's `LOOM_OPENBAO_ADDR` to that URL.
