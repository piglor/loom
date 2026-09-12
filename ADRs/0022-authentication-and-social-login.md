# ADR 0022: Browser authentication and social login

**Status:** Proposed
**Wave:** 1
**Deciders:** core team
**Date:** 2026-09-12

Related: [0020](0020-openbao-integration-secrets.md) · [0021](0021-loom-owned-workflow-spec.md)

---

## Context

The browser console currently asks operators to paste `LOOM_API_TOKEN`. That is
an operator credential, not a user identity: it cannot support account
registration, revocation, per-user sessions, or social sign-in. The first
authentication slice needs a bootstrap administrator, email/password accounts,
and GitHub OAuth registration while preserving the machine-facing bearer token
for existing workers and deployment health checks.

## Decision drivers

- Keep user credentials inside Loom's PostgreSQL control-plane boundary; do not
  put passwords or browser session values in OpenBao (OpenBao remains for
  integration credentials).
- Make browser sessions revocable, opaque, bounded (30-day absolute expiry),
  and inaccessible to JavaScript.
- Use the OAuth authorization-code flow with transaction-specific state and
  PKCE. RFC 9700 requires one-time CSRF protection and recommends PKCE for
  confidential web clients [S1]. GitHub documents the same state, redirect URI,
  and S256 PKCE parameters for its web flow [S2].
- Hash passwords with Argon2id and a unique salt; OWASP recommends Argon2id and
  rejects plaintext or fast hashes for password storage [S3].
- Keep self-hosted setup simple: `LOOM_ADMIN_EMAIL` defaults to
  `admin@example.com`. Loopback installs may omit `LOOM_ADMIN_PASSWORD` and use
  the documented `loom-admin-1234` convenience password; non-loopback installs
  use the random API-token compatibility fallback unless an explicit password
  is provided. Operators should set a private value before exposing the
  console publicly. The stored value is always a password hash.

## Evidence and alternatives

| Option | Functional fit | Security/replay | Operational cost | Decision |
| --- | --- | --- | --- | --- |
| Opaque PostgreSQL sessions + Argon2id + GitHub OAuth code/PKCE | Supports bootstrap, email registration, revocation, and the existing single-organization model. | Server-side revocation; no bearer value in browser JavaScript. OAuth codes are one-use and bound to state/PKCE [S1][S2]. | One migration and a small Go auth module; no new runtime service. | **Selected** |
| JWT access tokens in browser storage | Easy to issue, but revocation and rotation require another stateful mechanism; browser storage exposes bearer tokens to XSS. | Does not meet the revocation and HttpOnly requirements without rebuilding the selected design. | Less database work initially, greater incident and rotation cost. | Rejected |
| Hosted identity provider / full OIDC broker | Offloads account recovery and MFA. | Adds an external availability, privacy, tenant, and callback boundary; more configuration than this first slice. | New service contract and provider billing/operations. | Deferred |
| Continue with one operator API token | No migration and keeps workers working. | Shared credential cannot identify or revoke users and is unsuitable for registration. | Lowest implementation cost, fails the product requirement. | Rejected |

## Proposed decision

Add organization-scoped `users`, `auth_identities`, and `auth_sessions`
tables through Ent schemas and a versioned PostgreSQL migration. A user has an
email, optional display name, role (`admin` or `member`), and an Argon2id
password hash. External identity rows contain the provider, immutable provider
subject, and the verified-email snapshot used for account creation; the
short-lived provider access token is discarded after identity verification.

```text
Browser -> /v1/auth/login or /v1/auth/{provider}/start
             |                         |
             v                         v
       Argon2id verify       Provider code + state + PKCE
             |                         |
             +-------------> users / auth_identities
                                  |
                                  v
                    opaque auth_sessions row
                                  |
                    HttpOnly host-scoped session + CSRF cookie
                                  |
Browser API requests -> Loom authorizer -> existing Goal/Workflow APIs
```

The server sets a host-scoped secure, HttpOnly, SameSite cookie in HTTPS
deployments (HTTP is accepted only for loopback local development). MDN recommends
Secure, HttpOnly, a restrictive Path, and Lax or Strict SameSite for session
identifiers [S4]. Mutating browser requests also
send a double-submit CSRF value; the legacy API token remains accepted only as
an explicit compatibility credential.

Public endpoints:

- `GET /v1/auth/config` — enabled providers and a CSRF seed cookie.
- `GET /v1/auth/session` — current browser identity.
- `POST /v1/auth/login` — email/password sign-in.
- `POST /v1/auth/register` — email registration; the bootstrap account is
  seeded as admin and later registrations are members.
- `GET /v1/auth/{provider}/start` and `/v1/auth/{provider}/callback` — optional
  social-provider OAuth registration/sign-in. Providers are registered by
  plugins; GitHub is the first plugin.
- `POST /v1/auth/logout` — revoke the current session.

`LOOM_ADMIN_EMAIL` and `LOOM_ADMIN_PASSWORD` configure the bootstrap account.
Social-provider protocol and configuration are owned by each installed plugin;
the GitHub plugin currently reads the deployment-injected OAuth App values
`LOOM_AUTH_GITHUB_CLIENT_ID` and `LOOM_AUTH_GITHUB_CLIENT_SECRET`. These are
the plugin's application credentials, distinct from per-account GitHub
integration credentials stored in OpenBao. Callback URIs are derived from the
validated `LOOM_PUBLIC_URL` and are never accepted from a request parameter.
OAuth state details are signed with `LOOM_AUTH_STATE_KEY` when supplied and
one-time state hashes are persisted in PostgreSQL, so callbacks can safely
finish on another replica. An omitted key is generated per process for the
single-instance local default.

## Consequences and risks

Positive: the console no longer requires users to handle a server-wide token;
accounts can be revoked and GitHub registration does not grant GitHub API
authority. Existing worker and health-check integrations remain compatible.

Only administrators can mutate Goals, workflows, workers, or plugin
credentials. Members are read-only until a later role/permission decision.
Bootstrap is idempotent: it never replaces an existing password, but it will
set the configured initial password when the configured email belongs to a
passwordless external-only account.

Risks: email registration without verification can create untrusted member
accounts, and password reset, MFA, invitations, and account recovery are not
implemented in this slice. GitHub OAuth is unavailable until an OAuth App is
configured. Rate limiting is process-local until a shared limiter is needed.
The bootstrap email is returned by the unauthenticated config endpoint only to
make first-login setup easy; deployments that consider this sensitive should
use a neutral email placeholder in a future UI revision.

## Migration and rollout

1. Apply migrations 21–22 and seed the configured administrator idempotently.
2. Deploy the console with cookie authentication; keep the legacy bearer token
   accepted for one compatibility window.
3. Configure `LOOM_AUTH_GITHUB_CLIENT_ID` and
   `LOOM_AUTH_GITHUB_CLIENT_SECRET`, registering the derived callback URI.
4. Add email verification, password reset, invitations, and MFA in a later
   decision before opening registration to an untrusted multi-tenant audience.

Rollback keeps the additive tables and switches the console back to the
legacy bearer path; no existing Goal, Workflow, plugin, or OpenBao data is
rewritten.

## Non-goals

- Storing integration credentials or GitHub installation tokens in the user
  tables.
- Replacing Hatchet, the machine Agent protocol, or the integration-neutral
  Goal/Session/Wait/Event model.
- Acting as a general OAuth provider for other applications.

## Sources

- **[S1]** IETF, *RFC 9700: Best Current Practice for OAuth 2.0 Security*,
  January 2025, https://datatracker.ietf.org/doc/html/rfc9700. Supports
  transaction-specific state, exact redirect handling, and S256 PKCE for web
  authorization-code clients.
- **[S2]** GitHub Docs, *Authorizing OAuth Apps*, accessed 2026-09-12,
  https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps.
  Supports GitHub's web flow, exact callback parameter contract, state, and
  S256 PKCE.
- **[S3]** OWASP, *Password Storage Cheat Sheet*, accessed 2026-09-12,
  https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html.
  Supports Argon2id, unique salts, and rejecting plaintext/fast password
  hashes.
- **[S4]** MDN, *Secure cookie configuration*, accessed 2026-09-12,
  https://developer.mozilla.org/en-US/docs/Web/Security/Practical_implementation_guides/Cookies.
  Supports Secure, HttpOnly, restrictive Path, and SameSite session-cookie
  controls.
