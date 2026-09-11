# Loom OpenBao module

This is the self-contained OpenBao module for Loom integration credentials. It
uses a pinned OpenBao image, integrated Raft storage and private networking. It
does not expose port 8200 to the host or to the public proxy.

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
bao policy write loom /openbao/config/loom-policy.hcl
bao auth enable approle
bao write auth/approle/role/loom token_policies=loom token_ttl=1h token_max_ttl=4h
bao read auth/approle/role/loom/role-id
bao write -f auth/approle/role/loom/secret-id
bao audit enable file file_path=/openbao/logs/audit.log
```

Put the role ID and secret ID in the Loom deployment secret store, then set:

```text
LOOM_OPENBAO_ADDR=http://openbao:8200
LOOM_OPENBAO_MOUNT=loom
LOOM_OPENBAO_ROLE_ID=<role id>
LOOM_OPENBAO_SECRET_ID=<secret id>
```

Do not put the root token in Compose, `.env`, PostgreSQL or logs. Loom stores
only opaque secret references in PostgreSQL; provider values remain in OpenBao.

For a separate OpenBao host, terminate verified HTTPS (or configure OpenBao
TLS directly) and set `LOOM_OPENBAO_ADDR` to that URL. The application does not
depend on a Docker service named `openbao`; only the all-in-one deployment uses
that internal DNS name.

The production Coolify descriptor expands this service inline because the
installed Coolify 4.1.2 Compose parser reads declared services and does not
resolve Docker Compose's top-level `include`. Keep this module as the canonical
standalone form and keep the expanded service's security settings aligned.
