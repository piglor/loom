# Piglor production rollout — 2026-09-09

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

Operate only on service UUID `yidv3el41pezd8qp22s6el2w`. Coolify's service Stop
action is the rollback for this initial deployment; preserve its volumes and
secrets for diagnosis/retry. Do not delete volumes or touch the existing Hatchet
service. Pin subsequent deployments to a tested source commit or accessible image
digest and redeploy this same Loom service instead of creating duplicates.

References: [Coolify create service API](https://coolify.io/docs/api-reference/api/services/create-service),
[installed-version service launcher](https://github.com/coollabsio/coolify/blob/v4.1.2/app/Actions/Service/StartService.php),
[installed-version Compose parser](https://github.com/coollabsio/coolify/blob/v4.1.2/bootstrap/helpers/parsers.php).
