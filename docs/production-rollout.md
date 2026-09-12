# Piglor production rollout — 2026-09-09

## Current deployment: Go control plane and native GitHub ingress

The finite event-driven control plane is live at **https://loom.piglor.com**.
Open the console and sign in with the bootstrap administrator configured in
Coolify (`LOOM_ADMIN_EMAIL`, default `admin@example.com`). The existing
`LOOM_API_TOKEN` remains a machine/API compatibility credential; it is not a
Coolify or Hatchet token and should not be shared between people.

Production builds the public repository at the immutable source revision
`e542ef25f062c8401f4b3e0cd6fa8d553ad2fd3f`. The original acceptance revision's
[CI run](https://github.com/piglor/loom/actions/runs/34350521938) passed Go race,
generated persistence, Rust containment, durable lifecycle, packaged-image
backup/restore, and all 72 browser gates before promotion. CI also published:

```text
ghcr.io/piglor/loom@sha256:c488b29575c1d85bad2b012686e7ce9669774f6dead0720937cd210c8bb3fb18
```

The GHCR package still rejects anonymous pulls, so Coolify builds the exact
public Git revision rather than receiving registry credentials. The Go service
is the sole API, persistence, console, GitHub-ingress and Hatchet-dispatch
implementation; all Python source and tooling have been removed. Schema 13 is
installed. The existing Hatchet service and its database were not redeployed.

The rollout preserved the existing PostgreSQL volume and all prior Goal data.
Database backup scheduling is now owned by Coolify and an S3-compatible storage,
not a Compose sidecar. The retired on-host `loom-backups` volume is preserved
until the first platform-managed S3 backup and disposable restore test succeed.

Live production acceptance on 2026-09-09:

- Public health and authenticated database readiness repeatedly returned 200;
  unauthenticated Goal access returned 401, and unsigned GitHub input returned
  401. The old completed Goal and logical Session remained intact across the
  schema-6 to schema-13 migration.
- The Hatchet CLI profile `piglor-loom` observed the new
  `loom-dispatch-v1` workflow after the outbound worker connected.
- Repository webhook `676658375` is scoped to `workflow_run` for `piglor/loom`.
  Its non-actionable queued delivery returned 202 and its signed completion
  delivery returned 200.
- Disposable same-repository PR #1 ran real GitHub Actions workflow
  `353623104`, run `34352340609`, attempt 2, at SHA
  `94e6fbb08980f5b3969cbd55ba684cc9bee4f963`. Loom correlated all five identity
  components plus the SHA; no branch-name or model-based routing was used.
- Goal `e5b09e3e-3493-47b0-b084-67ccf66bfc7f` entered WAITING with Loom Session
  `b4337149-2789-428d-bf09-2159c0a80fd6`, received the signed event, became READY,
  was dispatched by Hatchet, and COMPLETED on that same Session. It recorded two
  STOPPED attempts, one wake-up, 267.133 seconds suspended, and zero finite
  execution seconds. This demo runtime invokes no model, so no token or monetary
  savings are claimed.
- The disposable PR was closed and its exact proof branch was deleted after the
  acceptance run. The earlier Goal whose delivery occurred during the proxy
  cutover was explicitly CANCELLED, leaving its audit trail intact.
- A live console audit passed 72/72 cases in Chromium, Firefox, WebKit and a
  320-pixel mobile viewport, including authentication, deep links, history,
  accessibility, failure recovery, responsive layout and credential clearing.

Coolify 4.1.2 retained the removed `loom-console` container and its old proxy
labels. The rollout therefore replaced that exact legacy resource with a
one-release, no-ingress migration tombstone; the generated descriptor routes the
public hostname only to `loom-server:8080`. Coolify's resource status for the
tombstone is stale, so public probes and the generated label set—not that row—are
the acceptance evidence.

On 2026-09-10, the public route returned gateway timeouts while the container's
local health check remained green. `loom-server` has private, egress and Coolify
ingress networks; generated Traefik labels did not select one deterministically.
Revision `f7189355bc38cf1b4b3e929624686a4a0ee06863` pinned
`traefik.docker.network` to the Coolify ingress network and added a deployment
configuration regression check to `make check`. Revision
`e542ef25f062c8401f4b3e0cd6fa8d553ad2fd3f` removed the redundant Compose backup
job. Repeated public checks and the authenticated readiness probe returned 200,
and the read-only Chromium production smoke passed 3/3 after deployment. The
separate exited `loom-build-check` Coolify application was deleted; GitHub
Actions remains the sole build and test gate.

This is a production deployment of the finite control-plane capability, not the
full privileged Codex promise. GitHub bindings to `codex-container` remain
deliberately rejected until GitHub App installation authorization and revocation
exist and exact Codex-thread continuation passes live acceptance. Operator
alerts, an off-site restore drill, and rotation of credentials previously pasted
into chat remain release gates.

The following section records the initial rollout, not the current revision.

## Initial finite control-plane rollout

Status: **finite control plane live; not a production-ready privileged agent**.

The authorized target was resolved through authenticated Coolify discovery:

- Project: Piglor (`xk0kwgccg4kg88ww40wwcg4s`).
- Environment: production (`io0sw0kg848w0woo0k0wkcok`).
- Server: piglor-production (`fkwc4ogsws4k0coc8wcw0ck0`).
- New service: loom-prod (`yidv3el41pezd8qp22s6el2w`).
- Requested endpoint: `https://loom.piglor.com`, container port 8000.
- Existing Hatchet network: `bvas8r76zkt83qxe67y08i9f`.
- Public source: [piglor/loom](https://github.com/piglor/loom), MIT licensed.
- Deployed source commit: `f2351eeae0d753f1ba1af392c1b70b1c05c547df`.

The initial service reported exited containers and a 503 endpoint. A temporary,
credential-free Dockerfile application on the same server built and ran the same
source successfully, with logs available through the application deployment API.
The service subsequently became healthy. The original failure's root cause was
not established; a missing log endpoint must not be presented as that cause.

The final descriptor builds directly from the public repository at the exact
commit above (remote Git build context), avoiding inline source bundles and
registry credentials. A Loom-only restart was queued and the endpoint went from
503 back to 200 while preserving the test Goal's wait. The temporary build-check
application is stopped; its logs are retained for diagnosis. Existing Hatchet was
not restarted.

GitHub [CI run 34300395655](https://github.com/piglor/loom/actions/runs/34300395655)
passed all tests and published the Linux amd64 image:

```text
ghcr.io/piglor/loom@sha256:36a2c9453787a357be7405baf5a6abbe6afd2126db1ac828bed7a83a7acb92e5
```

The published digest passed a read-only/non-root import smoke test. GHCR initially
made the package private; anonymous pulls returned 401. Production therefore
builds the pinned public source, not this GHCR digest. Anonymous image distribution
still requires changing the package's visibility in GitHub's package settings;
the available REST/GraphQL APIs do not expose that change. No broad GitHub token
was copied to production as a workaround.

Loom has fresh database and administrator secrets stored in Coolify. An owner-only
local copy is at `.loom/production.env` (ignored by Git). It also contains the
previously supplied Hatchet token; that token and credentials pasted into chat
still require rotation through the relevant services. No secrets are in this
report or the generated source snapshot. Do not paste the credential file or
unredacted deployment environments into support logs.

Existing Hatchet containers were not changed, restarted or migrated. Its API
status remains running, with PostgreSQL and RabbitMQ healthy. Loom uses new volume
names and does not reuse Hatchet's database or configuration volumes.

## Live acceptance evidence

- Public and direct-origin HTTPS health: 200; origin certificate verified.
- Unauthenticated administrator request: 401; authenticated schema readiness: 200.
- Goal `843e825d-ff0b-4f76-a2c5-8c7b3ee85b6d`: COMPLETED.
- Logical session `19a69c1a-6a08-4ceb-93c5-4f8edd0259e2` unchanged across both attempts.
- Suspended through the Loom-only redeployment, with one STOPPED attempt and no
  new attempts until the matching event arrived.
- Stale event: `mismatch`; matching event: `accepted`; redelivery: `duplicate`.
- Final metrics: lifetime 254.032 s; suspended 252.962 s; finite execution 0.081 s;
  exactly two STOPPED attempts, one wake-up. No model was invoked in this test.

Deployment-specific backups, operational alerts, exposed credential rotation,
and detailed runtime containment acceptance remain before a production release.

Codex execution, public-repository privileged workloads, and the full unattended
GitHub issue-to-merge-ready workflow remain disabled/unproven. This rollout does
not waive [production release gates](production-readiness.md).

## Safe stop / retry

Operate on Loom service UUID `yidv3el41pezd8qp22s6el2w`. OpenBao is bundled in
that same Coolify Compose resource, with its own persistent volumes. Coolify's
service Stop action is the rollback for the stack; preserve its volumes and
secrets for diagnosis/retry. Do not delete volumes or touch the existing Hatchet
service. Pin subsequent deployments to a tested source commit or accessible image
digest and update this resource instead of creating duplicates.

References: [Coolify create service API](https://coolify.io/docs/api-reference/api/services/create-service),
[installed-version service launcher](https://github.com/coollabsio/coolify/blob/v4.1.2/app/Actions/Service/StartService.php),
[installed-version Compose parser](https://github.com/coollabsio/coolify/blob/v4.1.2/bootstrap/helpers/parsers.php).
