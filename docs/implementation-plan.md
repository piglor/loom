# Implementation and proof gates

Status as of 2026-09-09: Phase 1 artifacts and the Phase 2 local durable-core proof exist. The real PostgreSQL/Hatchet restart test passed with a finite subprocess runtime; see [validation](validation.md) and [quickstart](quickstart.md). Phases 3–8 remain unproven, although basic CLI timing and core safety tests are already included. Complete phases incrementally; do not equate a demo runtime with real Codex integration.

Continuation progress: Phase 3 now has a tested outbound Rust finite-runtime
daemon and real two-worker affinity/restart proof; privileged process supervision
and uncertain-attempt reconciliation remain. Phase 4 has a standalone Rust
adapter and a successful real two-turn exact-thread probe, not daemon admission.
Phase 5 has signed, persisted workflow completion ingress for finite Goals and
adversarial tests, not live GitHub App freshness/recovery or privileged execution.
The [production checklist](production-readiness.md) remains open.

| Phase | Deliverable | Required exit evidence |
| --- | --- | --- |
| 1 — Research and architecture | Concepts, architecture, state/data models, protocol/security/cost design, 11 ADRs and upstream research | Reviewable decisions and explicitly recorded unknowns; no runtime claims |
| 2 — Durable core | Python server/Hatchet workflow, PostgreSQL migrations, fake finite runtime, inbox/outbox, audit and timing | Start → stopped wait → restart server/orchestrator/engine with DB preserved → matching event → exactly one logical continuation |
| 3 — Remote worker | Rust daemon, scoped auth, registration, long poll, local journal, session locks and reports | Two workers; bound one disconnects/restarts, other never claims; reconnect resumes pending command without duplicate launch |
| 4 — Codex | Version-pinned private structured adapter and finite yield outcome | Real harmless turn, saved thread ID, complete process stop, delayed exact-ID resume with context continuity |
| 5 — GitHub | App ingress, authorized bindings, freshness checks and missed-delivery reconciliation | Verified real fixture maps repository/PR/SHA/run attempt to exactly the authorized Session/Worker |
| 6 — Reference workflow | Controlled real issue → PR → CI failure → yield/wake/fix | Recorded long suspension with zero runtime starts/provider calls; same Codex thread continues after CI evidence |
| 7 — Efficiency UX | Thin CLI/API inspection, active/suspended/unknown intervals and usage | Counters reconcile to audit; provider usage deduplicates; absent costs remain unknown |
| 8 — Hardening | Recovery, security, retention/upgrade and observability tests | All adversarial/failure gates pass against pinned supported deployment; documented limits |

Idempotency, authentication, correlation and safe stopping begin with their owning phase; Phase 8 broadens the matrix rather than adding basic correctness at the end.

## Immediate Phase 2 slice

Implement one nonterminal Run per Goal and one Session per Run in the proof. Start with one fake runtime that records invocations and immediately returns a structured wait. PostgreSQL stores all domain state. Hatchet performs the durable wait and continuation. A repeat delivery must preserve one continuation identity even if the engine retries.

First verify a compatible engine/SDK pair, TLS endpoint and database access. Then implement migrations, atomic wait/outbox transitions, fake-runtime attempts, Hatchet adapter and restart tests. Only after that introduce the remote machine boundary. Keep live test fixtures in an isolated test namespace and remove only identified test resources, never unrelated tenant workflows.

## Required test matrix

| Cases | Expected evidence / owning phase |
| --- | --- |
| Duplicate event; different deliveries with equivalent condition | One satisfaction and continuation; Phase 2 |
| Stale generation; out-of-order event; event before yield | Current wait preserved or correctly satisfied, no lost wake; Phase 2 |
| Unknown Goal; wrong Session; wrong Worker | Typed rejection, zero unauthorized starts; Phases 2–3 |
| Offline worker; reconnect; worker restart | Pending continuation survives with exact binding and receipt identity; Phase 3 |
| Runtime failure; launch acknowledgment lost | Safe retry or UNKNOWN/reconciliation, no overlapping runtime; Phases 3–4 |
| Control-plane restart during wait | Database wait survives server and engine/orchestration worker restart; Phase 2 |
| Invalid/missing/expired/revoked auth | No read/claim/mutation beyond authority; Phases 2–3 |
| Invalid signature; altered raw bytes | No trusted event and no execution; Phase 5 |
| Wrong repository/PR/SHA/installation | Rejected correlation, no privileged wake; Phase 5 |
| Superseded CI run/attempt; spoofed check producer | Fresh evidence wins, no stale continuation; Phase 5 |
| Fork/untrusted PR; empty or ambiguous PR association | Fail closed or deterministic reconciliation, zero privileged execution; Phase 5 |
| Cancellation versus wake/claim/report | Revoked pending commands; active process stopped before terminal claim; Phases 2–4 |
| Prolonged wait across restart | Absent runtime processes and unchanged request counters until event; Phases 2 and 6 |
| CI green but review pending | Goal remains incomplete without a model wake; Phase 5 |
| Missed webhook; revoked integration; rate limit | Software recovery or inspectable blocked dependency, no model polling; Phase 5 |
| Retention boundary; migration/replay; outbox crash | No lost active waits or duplicate attempts; Phase 8 |
| Repeated usage / uncertain timing | No double counting or invented zero-cost intervals; Phase 7 |

Each owning phase adds its tests to CI. Integration tests require real PostgreSQL/Hatchet; fail the integration job if required services are absent rather than silently skip. Deterministic fake-clock tests complement a real elapsed-time soak. Real Codex/GitHub exercises require controlled credentials and explicit test resources and should produce sanitized evidence artifacts, not secrets/transcripts.

## Deployment readiness

Coolify deployment comes after a tested Compose topology. Use pinned image digests, health checks, nonroot application processes, private databases, secret injection, TLS and backups. Separate application migrations from service startup so multiple replicas cannot race. Keep Hatchet's own migrations under its supported release process.

Do not publish a production-ready claim before crash/partition tests pass. Do not publish a savings claim before a measured comparison exists. A PostgreSQL-backed Goal survives control-plane restart; lost worker disk remains a documented limit on exact local context recovery.
