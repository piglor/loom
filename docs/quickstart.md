# Run the durable core locally

This walkthrough uses the **development release's finite demo runtime**. It does not implement unattended coding or production multi-user hosting. Separate guides cover the [Rust worker](remote-worker.md), [signed GitHub ingress](github-ingress.md) and [standalone Codex probe](codex-adapter.md). The demo proves the orchestration boundary without a model: one subprocess exits, the Goal waits, an authorized event arrives, and a second finite subprocess completes the Goal under its event criterion.

## Requirements

Linux, Python 3.12 with `venv`/pip support, Docker Engine with the Compose v2 plugin, Make and Hatchet CLI 0.105.16. Local ports 8000, 15432, 17077 and 18888 must be available. On Ubuntu, missing venv support is provided by `python3.12-venv`.

Use a development machine: Hatchet's CLI development server uses demo administrator credentials and publishes its ports. Do not expose that stack publicly or use it as the production deployment. Loom's API and PostgreSQL port bind to loopback.

## Start

```bash
make setup
make init
make infra
make migrate
```

`make init` creates an owner-readable, ignored `.env` with random local database and API credentials. It refuses to overwrite an existing file. Keep that file to reconnect to the persistent database volume. The new local Hatchet profile is `loom-local`; commands name it explicitly and do not use your production profile.

In one terminal:

```bash
make serve
```

In a second terminal:

```bash
make worker
```

The worker runs through `hatchet worker dev`. No published Loom image is required. Inspect the authenticated API schema at `http://127.0.0.1:8000/docs` (API calls require the bearer token from `.env`) and Hatchet at `http://localhost:18888`.

## Create a Goal

```bash
.venv/bin/loom goal create \
  --title 'Wait for dataset' \
  --objective 'Demonstrate a finite attempt followed by durable suspension' \
  --source demo \
  --type dataset.ready \
  --resource dataset-123 \
  --version v1

.venv/bin/loom goal list
.venv/bin/loom goal inspect <goal-id>
```

After the first attempt, status is `WAITING`. Inspection includes the Session/Worker binding, expected condition, stopped attempt, audit history and execution/suspension timing. The subprocess is absent while waiting. The central worker and engine remain alive as inexpensive software; no model is loaded or invoked.

Create an `event.json` file containing the ID returned above:

```json
{
  "delivery_id": "dataset-123-ready-v1",
  "goal_id": "REPLACE-WITH-GOAL-UUID",
  "generation": 1,
  "source": "demo",
  "type": "dataset.ready",
  "resource": "dataset-123",
  "version": "v1"
}
```

Then deliver it:

```bash
.venv/bin/loom event event.json
.venv/bin/loom goal inspect <goal-id>
```

The event is persisted before dispatch. The Goal completes after its second finite demo attempt. Repeating the same event returns `duplicate`; a changed version/resource does not satisfy the wait. Reusing a delivery ID with changed content returns HTTP 409. Events may arrive before the first attempt yields without being lost.

The event endpoint is an administrator-authenticated generic ingress. It is **not** a GitHub webhook endpoint: it does not validate GitHub signatures, installation trust, forks or PR state. Do not send public webhooks to it.

## Cancel and recover

```bash
.venv/bin/loom goal cancel <goal-id>
```

Cancellation works when execution is confirmed stopped. An active/unknown attempt returns a conflict; automatic runtime interruption is deferred to the real agent adapter. If the demo worker crashes between admission and persisted stop evidence, the Goal becomes `BLOCKED` with an `UNKNOWN` attempt on restart, rather than rerunning possibly completed work. Preserve `.loom/` for stop-receipt recovery. There is no operator resolution API for UNKNOWN in this release; inspect the audit and create a fresh demo Goal when appropriate.

If journal storage and PostgreSQL both fail while reporting a stopped attempt, restart/reconciliation is required; automatic recovery from simultaneous loss of both evidence stores is not promised.

## Verify durability

Stop your manual API and worker terminals first; the proof starts and owns these processes on port 8000.

```bash
make check
make test
make prove
```

Tests use `LOOM_TEST_DATABASE_URL`, require its database name to end in `_test`, create it through the local database role if absent, and remove their own fixture rows. They do not fall back to the application database. The proof uses a fresh organization in the local application database and retains its Goal/audit as evidence.

`make prove` checks stopped waiting, restarts the API, worker and explicitly identified `loom-dev` Hatchet container while retaining PostgreSQL, delivers stale/duplicate/matching events while the worker is absent, and requires one continuation on the same Session. It refuses to restart a container from another Docker project. Use `--suspend-seconds 120` with `python -m scripts.prove_recovery --restart-container loom-dev-hatchet-1` for a longer observation.

## Release limits

- One local demo executor per organization, enforced by PostgreSQL and a local lock. Linux process/file-lock semantics are required.
- One Run, Session, wait generation and two finite demo attempts per Goal. The design documents describe later broader semantics.
- Durable workflow timeout is explicitly 30 days; arbitrary-duration production waits and timer/human workflows are not yet claimed.
- The outbox may publish duplicate engine messages; PostgreSQL admission prevents duplicate demo attempts. No exactly-once arbitrary external side-effect guarantee.
- Tokens/cost are unknown (`null`); elapsed suspension is not a savings estimate.
- Dependencies are locked for the tested Python environment. Local container tags are versioned; a production release still needs image-digest pinning, upgrades, deployment hardening and backup drills.
