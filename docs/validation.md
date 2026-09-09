# Validation record

Date: 2026-09-08. This record distinguishes actual checks from planned acceptance tests.

## Performed

- Initialized a standalone local Git repository on `main`; no remote repository was created or published.
- Researched primary Hatchet, GitHub and OpenAI documentation; findings and uncertainty are in `docs/research/`.
- Ran `codex --version`: `codex-cli 0.153.4`.
- Ran `codex app-server generate-json-schema` into a temporary directory without starting a model turn.
- Inspected generated stable schema properties: ThreadStartParams has cwd/sandbox/approvalPolicy; ThreadResumeParams has threadId; TurnStartParams has threadId/input/outputSchema; TurnCompletedNotification has threadId/turn; ThreadTokenUsageUpdatedNotification has threadId/turnId/tokenUsage.
- Checked repository Markdown relative links and fenced-code balance: no errors across the initial 25 Markdown documents. A final check covers this record as well.

These schema checks establish field presence, not runtime execution or compatibility across other Codex releases.

## Supplied Hatchet instance

The user supplied `https://hatchet.piglor.com` and a client credential, then supplied a replacement. The replacement was tested with official Hatchet CLI 0.105.16, installed through the upstream installer with successful checksum verification. No worker registration, workflow creation or tenant mutation was performed.

Correction: the earlier HTTP 403 and CLI missing-server_url error used an incorrectly transcribed copy of the first token. Those results do not establish a fault in the supplied token or deployment.

Observed from this workspace:

| Check | Result | Interpretation |
| --- | --- | --- |
| HTTPS dashboard HEAD request | HTTP 200 | Dashboard route reachable; does not prove SDK access |
| CLI profile creation with replacement token | API check passed; profile created after explicitly retaining TLS | Authenticated API access established |
| `hatchet runs list -o json -p piglor-loom --since 1h --limit 1` | Exit 0, valid JSON, `rows: []` | Official CLI REST connectivity check passed |
| DNS lookup for advertised `hatchet-engine` | No resolution | Token-advertised internal gRPC address unavailable from this workspace |

The advertised `hatchet-engine:7070` can be valid inside the deployment's private container network. An orchestration worker here needs a reachable gRPC address, or must run on that internal network. Verify TLS and `HATCHET_CLIENT_HOST_PORT` using the supported SDK configuration. The API and gRPC routes are separate. [Hatchet worker configuration](https://docs.hatchet.run/self-hosting/worker-configuration-options)

The CLI's gRPC auto-probe reported DNS failure. TLS was retained as a conservative profile setting, not detected or verified on that endpoint. Worker connectivity still needs a reachable gRPC address or execution inside the deployment network; API access is confirmed.

The `piglor-loom` profile stores the replacement token outside the repository in `/root/.hatchet/config.yaml` (mode 0600; directory mode 0700). No token is in repository files. CLI telemetry was disabled for these checks. The official `hatchet skills install` command installed `skills/hatchet-cli/` and added its references to the repository `AGENTS.md`; the setup reference was used for the connection check.

## Not performed at the Phase 1 checkpoint

No application code, database migration, Hatchet durable workflow, remote-worker execution, Codex model turn, real GitHub webhook, restart test, measured suspension interval or savings experiment has been run. No live proof is implied by the documentation. Follow the [implementation gates](implementation-plan.md).

## Phase 2 — 2026-09-09

Implemented and tested locally with Hatchet CLI/engine 0.105.16, Python SDK 1.40.0 and Loom PostgreSQL 17.6. The worker is started through the official Hatchet CLI. Production infrastructure was not changed.

`make prove` passed against the actual local engine and database: the finite subprocess stopped, the Goal remained WAITING without another attempt, and API/worker/engine restarts preserved that wait. Stale, matching and duplicate events were delivered while the worker was absent. Reconnecting produced one continuation using the same Session and Worker, then COMPLETED.

Recorded first proof: Goal `fc20d944-df82-4435-a4b6-f1ed6b32274d`; lifetime 96.294 seconds; suspended 89.220 seconds; finite execution 0.059 seconds; two attempts; one wake-up. These are observed demo timings, not model usage or estimated savings. Token and provider-cost values are unknown.

Final rerun after recovery fixes also passed: Goal `09454d1a-3dc2-4db2-9693-01644d79302c`; lifetime 90.300 seconds; suspended 85.684 seconds; finite execution 0.028 seconds; two attempts and one wake-up.

All 22 regression tests passed. They cover authenticated API access, isolation, correlation, duplicate/concurrent deliveries, out-of-order events, early events, wrong attempt bindings, runtime failure, cancellation, outbox recovery, unknown execution on restart, and durable stop receipts including journal write failure. Ruff lint/format checks and `pip check` passed. Two upstream test-client deprecation warnings remain. The quickstart documents remaining limits; real Codex resumption and privileged remote-worker/GitHub security gates are not established by this proof.

## Continued implementation — 2026-09-09

- 44 Python tests and 8 Rust tests pass. Ruff, rustfmt, Clippy with warnings denied,
  and Python dependency checks pass. Two upstream test-client deprecations remain.
- `make prove` passed again with schema version 5: Goal
  `cee6a2e0-1268-42ef-b181-df5d47915513`; 98.281 seconds lifetime,
  86.322 seconds suspended, 0.036 seconds finite execution, two attempts, one wake.
- `make prove-remote` passed with real HTTP/PostgreSQL and two Rust daemons:
  fixed worker/session affinity, stopped wait, disconnect, journal restart and
  one continuation; the other worker never received the bound command.
- The standalone Codex probe passed two actual turns with installed 0.153.4:
  exact provider thread retained context after terminating the first process.
  This is not yet a mailbox-admitted Codex execution. See the tool/supervision
  limitations in [Codex adapter](codex-adapter.md).
- Signed workflow ingress passes raw-byte signature, replay, fork, stale run/SHA,
  wrong repository/installation/PR, early-unbound delivery and interrupted binding
  reconciliation tests. No live GitHub App fixture or privileged wake is claimed.
- The digest-pinned Python container built successfully; its API readiness passed
  under UID 10001 with a read-only filesystem, no capabilities and no-new-privileges.
  Coolify Compose syntax validated. No production resource was deployed.
- The local PostgreSQL dump/restore drill passed: restored WAITING state and exact
  Session, then accepted a matching event and completed the finite continuation.
  It verified the inspected local container/DSN binding and removed only its fresh
  restore database, archive and fixture rows. Hatchet disaster recovery and worker
  filesystem restoration remain separate deployment gates.
- Two-axis code reviews drove fixes for queued cancellation, early worker events,
  wake accounting, local-runtime admission, journal file safety, retry categories,
  provider notification ordering, inbox recovery and test-target confinement.

See [production readiness](production-readiness.md) for uncompleted release gates.
The repository remains unpublished; hosted CI has not run. Local counterparts of
the configured CI checks ran successfully. API and central CLI worker were
restarted on the local development stack after migration.
