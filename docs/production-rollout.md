# Piglor production rollout — 2026-09-09

Status: **deployment submitted; not live or production-ready**.

The authorized target was resolved through authenticated Coolify discovery:

- Project: Piglor (`xk0kwgccg4kg88ww40wwcg4s`).
- Environment: production (`io0sw0kg848w0woo0k0wkcok`).
- Server: piglor-production (`fkwc4ogsws4k0coc8wcw0ck0`).
- New service: loom-prod (`yidv3el41pezd8qp22s6el2w`).
- Requested endpoint: `https://loom.piglor.com`, container port 8000.
- Existing Hatchet network: `bvas8r76zkt83qxe67y08i9f`.
- Source archive SHA-256:
  `e8ac3f34ad0455ee1127c1a3f124766002160dc22f0fee3fa5f23a52f21d1989`.

Coolify accepted service creation, environment configuration and startup requests.
The API last reported all four Loom containers as `exited`; the public health
endpoint returned 503 (a later request timed out). This is not evidence of a
working deployment. The same compressed inline image built successfully locally.
No root cause is claimed without the remote deployment log. Coolify 4.1.2 exposes
application deployment logs through its API, but not custom service build logs.
The next required artifact is the latest loom-prod deployment error from its UI.

Loom has fresh database and administrator secrets stored in Coolify. An owner-only
local copy is at `.loom/production.env` (ignored by Git). It also contains the
previously supplied Hatchet token; that token and credentials pasted into chat
still require rotation through the relevant services. No secrets are in this
report or the generated source snapshot. Do not paste the credential file or
unredacted deployment environments into support logs.

Existing Hatchet containers were not changed, restarted or migrated. Its API
status remains running, with PostgreSQL and RabbitMQ healthy. Loom uses new volume
names and does not reuse Hatchet's database or configuration volumes.

## Acceptance still required

1. Resolve the remote deployment error; inspect Coolify's generated Compose and
   verify the exact image, non-root/read-only settings, ports and trust boundary.
2. Public HTTPS health 200, unauthenticated administrator request 401, authenticated
   schema readiness 200. Verify origin TLS as well as Cloudflare edge TLS.
3. A finite Goal reaches WAITING with a stopped attempt; no additional attempts
   occur while suspended; a correlated event resumes the same session and ends
   with exactly two stopped attempts. Test duplicate/stale events on this Goal.
4. Restart only Loom during a wait and verify recovery; establish tested backups
   and alerts before treating this as an operational production release.

Codex execution, public-repository privileged workloads, and the full unattended
GitHub issue-to-merge-ready workflow remain disabled/unproven. This rollout does
not waive [production release gates](production-readiness.md).

## Safe stop / retry

Operate only on service UUID `yidv3el41pezd8qp22s6el2w`. Coolify's service Stop
action is the rollback for this initial deployment; preserve its volumes and
secrets for diagnosis/retry. Do not delete volumes or touch the existing Hatchet
service. Once a build log establishes the failure, correct the descriptor or
environment and redeploy this same Loom service instead of creating duplicates.

References: [Coolify create service API](https://coolify.io/docs/api-reference/api/services/create-service),
[installed-version service launcher](https://github.com/coollabsio/coolify/blob/v4.1.2/app/Actions/Service/StartService.php),
[installed-version Compose parser](https://github.com/coollabsio/coolify/blob/v4.1.2/bootstrap/helpers/parsers.php).
