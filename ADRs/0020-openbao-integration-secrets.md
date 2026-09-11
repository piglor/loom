# ADR-0020: OpenBao for integration credentials

**Status:** Accepted by user direction
**Date:** 2026-09-11
**Scope:** Loom Lite and Server integration credentials

## Context

The operator console must let an operator connect external plugins without
putting provider credentials in browser storage, PostgreSQL, logs, Hatchet
payloads, or committed deployment files. Integration credentials are mutable
security material, while Loom's durable Goal, Session, Wait, Event, and binding
records must remain provider-neutral and replayable without secret values.

The decision question is: **where does Loom store and retrieve integration
credential bundles, and how does the control plane authenticate to that store?**

## Decision drivers

- Write-only browser setup and independently rotatable provider credentials.
- Organization and plugin isolation without provider-specific core tables.
- A usable single-host Lite deployment and an operator-managed Server topology.
- Auditable access, fail-closed behavior, versioning, and recoverable migration
  from existing environment configuration.
- A maintained open-source implementation with a narrow Go adapter boundary.

## Evidence and alternatives

| Option | Evidence | Assessment |
| --- | --- | --- |
| OpenBao KV v2 | KV v2 versions values, supports check-and-set, and separates read/write/delete ACL capabilities. [S1] AppRole is intended for machine workflows and can issue constrained renewable tokens. [S2] OpenBao records API requests through audit devices, although operators must configure them and protect their output. [S3] | Selected. It separates secret values from Loom persistence and supplies rotation and audit primitives. Loom still owns availability handling, bootstrap, and references. |
| Encrypted PostgreSQL columns | PostgreSQL provides `pgcrypto`, but its documentation states that data and passwords move between the functions and client in clear text and require trusting database administrators. [S4] | Rejected. It leaves the encryption key and ciphertext lifecycle in Loom's database boundary and weakens separation from normal persistence access. |
| Deployment environment or container secrets only | Deployment injection works for static service configuration, but cannot provide an in-product, per-organization connection lifecycle or independent rotation without restarting/redeploying Loom. | Retained only as a one-release read-only migration source and for OpenBao bootstrap credentials. |

## Decision

Use OpenBao KV v2 as the source of truth for integration credential bundles.
Loom stores only an opaque secret reference and non-secret connection metadata
in generic Ent-managed PostgreSQL tables.

```text
Operator browser -> Go integration setup API -> OpenBao KV v2
                          |                       (secret values)
                          v
                    PostgreSQL
              (reference, state, external IDs)
```

The Go control plane owns a small secret-store port. Its OpenBao adapter uses
AppRole by default, caches only the issued token until shortly before expiry,
and limits access to the configured Loom KV mount. Lite and Server deployments
must never give the application a root token; the root token in the local dev
Compose profile is explicitly disposable. Secret responses are never exposed
through browser APIs.

Lite includes a pinned OpenBao service with persistent integrated storage on a
private network. It is initialized and unsealed explicitly; development mode is
not an acceptable Lite or production configuration. Server connects to an
externally operated OpenBao deployment. Integrated storage supports persistent
and HA operation without another storage product. [S5]

Credential identity is generic: organization, plugin ID, credential-set ID,
version, and status. Provider installations are separate generic integration
instances. GitHub-specific fields exist only inside the encrypted bundle and
the GitHub adapter.

## Consequences and risks

- OpenBao availability becomes a prerequisite for new plugin setup, webhook
  verification, and provider API calls. These operations fail closed; unrelated
  Goal reads and durable waits remain available.
- Lite operators must back up the OpenBao volume and retain unseal/recovery
  material separately. Server operators own HA, TLS, unseal, and backup policy.
- KV v2 retains old versions by default; rotation policy must bound retained
  versions and permanent destruction remains an explicit administrative action.
- PostgreSQL and OpenBao cannot commit atomically. Loom writes the secret first,
  compensates on a failed metadata insert, and exposes incomplete cleanup as an
  auditable error rather than silently accepting a connection.
- OpenBao API and image versions are pinned and upgraded deliberately. A new ADR
  is required to replace OpenBao or persist plaintext/decryptable credentials
  in Loom's PostgreSQL database.

## Migration and rollback

During one compatibility release, existing webhook and API environment values
may be read when no OpenBao-backed installation exists; browser writes always
target OpenBao. Operators migrate by enrolling the existing GitHub App through
the write-only advanced setup form, verifying its installation, and only then
removing the legacy values. The older environment variables do not contain a
complete GitHub App credential bundle, so Loom does not claim that they can be
automatically imported. Rollback disables new credential writes and keeps the
opaque metadata; it never exports secrets into PostgreSQL.

## Non-goals

- OpenBao does not authorize Goals, bindings, workers, or external events.
- This decision does not add arbitrary downloadable plugin code.
- User/organization RBAC, cloud KMS selection, and native mobile UI are separate
  decisions.

## Sources

- **[S1]** OpenBao, "KV secrets engine - version 2," documentation 2.6.x,
  accessed 2026-09-11: https://openbao.org/docs/secrets/kv/kv-v2/
- **[S2]** OpenBao, "AppRole auth method," documentation 2.6.x, accessed
  2026-09-11: https://openbao.org/docs/auth/approle/
- **[S3]** OpenBao, "Audit devices," documentation 2.6.x, accessed 2026-09-11:
  https://openbao.org/docs/audit/
- **[S4]** PostgreSQL Global Development Group, "pgcrypto," PostgreSQL 17,
  accessed 2026-09-11: https://www.postgresql.org/docs/17/pgcrypto.html
- **[S5]** OpenBao, "Integrated Storage," documentation 2.6.x, accessed
  2026-09-11: https://openbao.org/docs/concepts/integrated-storage/
