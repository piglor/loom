# Loom operator console

The console is a read-only view of actual Goals, wait conditions, session/worker
bindings, audit history and execution timing. It is not Hatchet's dashboard and
does not expose GitHub as a mandatory core concept.

## Local development

Requirements: Node 24, Go as pinned in `services/loom/go.mod`, and the existing
Loom PostgreSQL/API setup. Run `make console-setup` to install locked dependencies
and build assets. Start the existing Python API with `make serve` in one terminal.

In a second terminal, load your locally generated configuration and set the
fixed internal upstream URL, then start Go:

```sh
set -a
. ./.env
set +a
export LOOM_UPSTREAM_URL=http://127.0.0.1:8000
make console-serve
```

Open `http://127.0.0.1:8080`. Sign in with the `LOOM_API_TOKEN` from your local
secret configuration, not a Hatchet token or a worker credential. Keep this
credential private. For a remote deployment use HTTPS. This operator credential
has existing administrative API authority despite the console being read-only.
The UI holds it in memory only; refresh or sign out clears access. Multi-user
SSO and finer-grained browser authorization remain outstanding release gates.

The browser connects only to the same origin. Development on Vite's standalone
port needs an explicit API proxy; the documented Go entry point serves the built
assets and requires no browser CORS exceptions.

## What is implemented

- Goals overview, title/ID search, state filtering and Needs You view.
- Direct links to Goal details, exact wait source/resource/version and event ID.
- Logical session, worker binding, provider-session ID when present.
- Audit transitions, attempt outcomes and recorded timing.
- Explicit demo/unknown-cost labels; no invented savings or worker availability.
- Responsive browser layout, keyboard navigation, loading/error/empty states.

Refresh is manual, not a background model loop. Counts cover the latest 100
Goals returned by the current API, not every Goal in the organization. Execution
duration covers reported completed attempts and is not billed model time.

## Deployment and migration

Build `services/loom/Dockerfile` from the repository root. It produces a non-root
image containing a Go binary and compiled static assets. Merge
`deploy/coolify/console.compose.yaml` with the existing Compose descriptor; route
the public domain to `loom-console:8080`, keeping its upstream API private.

Go implements authenticated organization-scoped read APIs in a read-only database
session. Existing writes, webhooks and worker endpoints are proxied to Python
without changing their authority. Do not stop the Python API or Hatchet worker.
This is an incremental migration, not a completed Go orchestration replacement.

No database schema change is required. Rollback routes to the previous API and
leaves the database, Hatchet workflows and worker session bindings intact.

## Validation

`make console-check` runs Go race/static checks, the TypeScript build, and browser
tests. Install Playwright browser dependencies first with
`npx playwright install --with-deps chromium firefox webkit`. CI runs Chromium,
Firefox, WebKit and a mobile viewport. These are browser tests, not native
iOS/Android application certification.

`make test` additionally runs real PostgreSQL Go/Python read-contract and
organization-isolation tests. Existing durable restart, remote worker,
container and backup proofs remain separate mandatory regression checks.

See [recorded validation and release boundaries](console-validation.md).
