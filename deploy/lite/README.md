# Loom Lite secret storage

The standard Compose deployment already contains a persistent single-node
OpenBao service and an automatic bootstrap/unseal companion. This Lite overlay
only makes Loom's internal address explicit; it is not OpenBao development mode
and it is not an HA Server topology.

```sh
docker compose -f ../coolify/compose.yaml -f openbao.compose.yaml up -d
```

The bundled convenience mode initializes the store, configures Loom's AppRole,
and re-unseals after restart without operator copy/paste. It retains a generated
single-key seal in a protected Docker volume; use the advanced manual procedure
in [`../openbao/README.md`](../openbao/README.md) when you need off-host KMS/HSM
key custody.

For the advanced/manual path, unseal the service, set `BAO_TOKEN` to the
temporary root token inside the OpenBao container, then create Loom's KV v2
mount and scoped AppRole:

```sh
bao secrets enable -path=loom kv-v2
bao write loom/config max_versions=10
bao policy write loom /openbao/config/loom-policy.hcl
bao auth enable approle
bao write auth/approle/role/loom token_policies=loom token_ttl=1h token_max_ttl=4h secret_id_ttl=720h secret_id_num_uses=0
bao read auth/approle/role/loom/role-id
bao write -f auth/approle/role/loom/secret-id
```

The file audit device is declared in `openbao.hcl` and is enabled automatically
on startup.

Verify the declarative audit device is writing to the protected audit volume
before accepting credentials.
Put the resulting role ID and secret ID in the deployment secret store as
`LOOM_OPENBAO_ROLE_ID` and `LOOM_OPENBAO_SECRET_ID`, unset the temporary root
token, revoke it with `bao token revoke -self`, and start the remaining services
with both Compose files. Do not place the root token in Compose or `.env`.

Take and restore-test a Raft snapshot and retain the audit volume using the
procedure in [`../openbao/README.md`](../openbao/README.md) before accepting
production credentials.

Server deployments do not use this overlay. They connect to an externally
operated HA OpenBao over verified TLS and set `LOOM_OPENBAO_CA_CERT` when the CA
is not in the system trust store.
