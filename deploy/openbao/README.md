# Loom OpenBao module

This is the self-contained OpenBao module for Loom integration credentials. It
uses a pinned OpenBao image, integrated Raft storage and private networking. It
does not expose port 8200 to the host or to the public proxy. The default Loom
installation includes the same service in
[`deploy/coolify/compose.yaml`](../coolify/compose.yaml), so a first-time
Coolify or Lite setup is one Compose project. Use this file directly for the
advanced topology where OpenBao runs as a separate project or host.

Easy bundled setup (Coolify or a local Compose project):

```sh
docker compose -f deploy/coolify/compose.yaml up -d
docker compose -f deploy/coolify/compose.yaml exec openbao bao operator init
docker compose -f deploy/coolify/compose.yaml exec openbao bao operator unseal
```

The bundled service is private at `http://openbao:8200`; it has persistent Raft
and audit volumes and starts sealed. Loom does not hard-depend on OpenBao health,
so the console remains available while an operator completes bootstrap.

Advanced standalone setup:

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
bao policy write loom-restore-verify /openbao/config/loom-restore-verify-policy.hcl
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

Immediately before the snapshot, write a non-sensitive canary and create an
orphaned, non-root token with the dedicated read-only policy. Keep the canary
value and token only in the protected backup record. The token is independent
of the operator token that created it, lasts 30 days, and is the restore-read
check; it must never be written to Compose, `.env`, PostgreSQL or logs:

```sh
RESTORE_MARKER="$(date -u +%Y%m%dT%H%M%SZ)-<random>"
bao kv put -mount=loom restore-check value="$RESTORE_MARKER"
SOURCE_VERIFY_TOKEN=$(bao token create -orphan -policy=loom-restore-verify -ttl=720h -format=json \
  | jq -er '.auth.client_token')
```

Run the drill before this token expires (within 30 days of the snapshot), or
create a fresh canary/token and snapshot. Revoke the token after verification.

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
  -e BAO_TOKEN="$SOURCE_VERIFY_TOKEN" openbao bao kv get -mount=loom \
  -field=value restore-check
```

The restored cluster uses the source barrier and unseal key, while the
orphaned, read-only canary token captured with the snapshot proves that the
snapshot is readable without retaining a source root token. Compare the output
to `$RESTORE_MARKER`, then revoke the
temporary target token after the drill and destroy the disposable resource
without touching production volumes. Let the verification token expire or
revoke it on the source after the drill.

Keep the audit volume in the deployment backup plan as well. Rotate
`/openbao/logs/audit.log` at least monthly (disable the file audit device,
archive the file to protected storage, then re-enable it) and alert on disk
usage; never truncate it while the audit device is active.

For a separate OpenBao host, terminate verified HTTPS (or configure OpenBao TLS
directly) and set `LOOM_OPENBAO_ADDR` to that URL. The application has no hard
dependency on a Docker service named `openbao`; the bundled Compose simply makes
that name available for the easy path. For a separate Coolify resource, use
this standalone descriptor (or a managed OpenBao), keep its API private to the
Loom network where possible, and set `LOOM_OPENBAO_CA_CERT` when needed.
