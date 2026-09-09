# Hatchet integration research

Accessed: 2026-09-08. Scope: current official documentation and upstream source; no running Hatchet experiment was performed for this note. Recommendations below are Loom design judgments, not upstream guarantees. Documentation is moving; Phase 2 must pin and test an engine/SDK pair before relying on these APIs.

## Recommendation

Use the official Python SDK in a central Loom orchestration worker. Hatchet owns durable waits, timers, task scheduling and bounded infrastructure retries. Loom owns goals, authorization, correlation, attempt identity, session affinity and accounting. Python exposes durable tasks, workers, event publishing and scheduling directly. Official documentation lists Python, TypeScript, Go and Ruby; it does not list a supported Rust SDK. [Python client](https://docs.hatchet.run/reference/python/client), [supported languages](https://docs.hatchet.run/v1).

Keep Hatchet credentials on the control plane. A Rust Loom Agent can receive narrowly authorized commands through Loom's outbound HTTPS long-poll mailbox. This adds delivery/acknowledgment work, but preserves a Rust distribution and worker-scoped trust boundary. Do not turn that mailbox into a second workflow scheduler: Hatchet still decides when orchestration proceeds; the mailbox delivers an already-authorized attempt. Long polling in this daemon consumes no model inference.

## Verified capabilities and consequences

| Area | Upstream evidence | Consequence for Loom |
| --- | --- | --- |
| External waits | Durable tasks support event keys, CEL payload matching and replay from an event log after interruption. Scoped lookback can include events arriving before a wait; both publication and wait must carry the scope. [Event waits](https://docs.hatchet.run/v1/durable-event-waits) | Publish a validated internal wake event keyed by wait ID/generation, with matching scope. Test arrival before registration. |
| Timers | Durable sleeps preserve the original duration across interruption; relative and absolute sleep helpers exist. [Durable sleep](https://docs.hatchet.run/v1/durable-sleep) | Use engine timers for deadlines and retry delays. Never use an agent sleep loop. |
| Task eviction | Durable tasks can release slots after a waiting TTL or under capacity pressure, then replay when their condition resolves. Non-durable tasks retain slots while inactive. [Task eviction](https://docs.hatchet.run/v1/task-eviction) | Configure eviction explicitly; stopping Codex is a separate Loom action and must precede confirmed WAITING. |
| Delivery semantics | Execution is at least once; transitions persist transactionally in PostgreSQL. Engine/API restart without losing persisted state. [Architecture and guarantees](https://docs.hatchet.run/v1/architecture-and-guarantees) | Do not claim exactly-once model execution. Deduplicate attempts and fence stale reports. |
| Retry | Task retry count, capped exponential backoff, and non-retryable exceptions are supported. [Retry policies](https://docs.hatchet.run/v1/retry-policies) | Retry safe infrastructure operations; route ambiguous runtime launches to reconciliation before another model call. |
| Timeouts | Documentation gives defaults of 5 minutes scheduling and 60 seconds execution, configurable per task; timeout does not guarantee immediate task termination. [Timeouts](https://docs.hatchet.run/v1/timeouts) | Set explicit timeouts. Confirm how durable waits interact with them on the pinned version. A deadline is not proof the runtime stopped. |
| Idempotency | Beta task/workflow dedup supports TTL and active-status strategies, with CEL keys. Status keys release at terminal state and have a fallback TTL. [Idempotency](https://docs.hatchet.run/v1/idempotency) | These controls supplement durable Loom uniqueness constraints; they cannot permanently deduplicate webhook deliveries or external side effects. |
| Workers | Workers register tasks, maintain slots, and report results. Several workers can register the same task. [Workers](https://docs.hatchet.run/v1/workers) | Use this for central orchestration capacity; explicitly limit slots. Loom machine identity remains independent. |

### Replay and the no-model-waiting invariant

Eviction re-invokes durable task code and replays checkpoints. Upstream warns that ordinary non-idempotent writes and expensive operations inside the durable function may repeat. Therefore a model launch must live behind a separately recorded execution-attempt boundary, not inline before a replayable wait. Configure and test child-task/checkpoint behavior before claiming it prevents repeated launches. [Task resumption](https://docs.hatchet.run/v1/task-eviction#task-resumption).

Go's `DurableContext` documents `WaitForEvent`, `WaitFor`, `SleepFor`, `SleepUntil`, and memoization. Its memo documentation explicitly allows an in-process-only fallback on engines without durable memo support, so a method's existence alone is insufficient evidence of restart safety. [Go context](https://docs.hatchet.run/reference/go/context).

The Loom proof should measure runtime invocation count and process termination across WAITING. A Hatchet durable task may remain resident before eviction, and the engine/database remain active throughout; neither implies model inference. Conversely, releasing a Hatchet slot does not terminate an independently running Codex process.

### Worker affinity and reconnection

Worker labels may be strings or numbers and can change dynamically. A desired label with `required=true` prevents assignment without a match; `required=false` uses weights as preferences. This feature is beta. [Worker affinity](https://docs.hatchet.run/v1/advanced-assignment/worker-affinity).

Sticky assignment is also beta. `SOFT` permits fallback to another worker; `HARD` keeps work pending for its original worker until availability or scheduling timeout. It is documented for workflow/task locality, not as a portable machine-session registry. [Sticky assignment](https://docs.hatchet.run/v1/advanced-assignment/sticky-assignment).

Workers initiate bidirectional gRPC connections and reconnect after network interruption according to the architecture documentation. This already satisfies outbound-only connectivity for supported SDK workers. The documentation does not establish that a fresh process registering the same friendly name recovers the same Hatchet worker UUID, nor promise exactly-once execution during partitions. [Worker connection and reliability guarantees](https://docs.hatchet.run/v1/architecture-and-guarantees).

Loom recommendation: persist its own worker ID and session binding; an offline bound worker yields `WAITING_FOR_WORKER`. If native Hatchet routing is later used on machines, require the stable Loom worker label and verify authenticated identity independently. Labels that a worker can advertise are scheduling metadata, not a security proof.

### Native transport versus Rust mailbox

| Option | Benefit | Cost or unresolved constraint |
| --- | --- | --- |
| Python/Go Hatchet companion beside Rust agent | Reuses registration, gRPC dispatch, heartbeats, retries and reconnection from an official SDK. | Additional machine-side runtime/binary; credential authority and machine binding still need assessment. |
| Rust implementing Hatchet's worker protocol directly | Could preserve a Rust executable. | No documented official Rust SDK found; maintaining listener/replay/version compatibility becomes Loom's responsibility. Not recommended for MVP. |
| Python Hatchet worker centrally, Rust HTTPS mailbox | Hatchet credentials stay central; local policy and worker identity stay in Loom's narrow protocol. | Loom must implement durable command identity, authenticated claims, acknowledgment, reconnect reconciliation and duplicate suppression. Recommended. |

The SDK's existence is strong reason not to invent a transport solely to obtain outbound connectivity. The proposed mailbox is justified by the Rust distribution and security boundary, not because Hatchet lacks outbound workers.

## Authentication and deployment

Workers use `HATCHET_CLIENT_TOKEN`. SDK configuration supports TLS and mTLS with certificate/CA paths; TLS is the documented default. A friendly worker name is a separate setting. [Worker configuration](https://docs.hatchet.run/self-hosting/worker-configuration-options).

Hatchet documents tenant roles and payload visibility controls for members, but API tokens are exempt from payload restrictions. The inspected pages do not establish that a worker token is restricted to one machine, session, or task. Do not distribute tenant credentials to mutually untrusted developer machines based on an assumption that labels enforce authorization. [User roles and payload visibility](https://docs.hatchet.run/v1/user-roles).

Self-hosted Hatchet comprises API, engine, PostgreSQL and dashboard; RabbitMQ is optional. Hatchet Lite bundles engine/API for development and low-volume use, and the upstream Compose guide separates components. [Self-hosting overview](https://docs.hatchet.run/self-hosting), [Lite](https://docs.hatchet.run/self-hosting/hatchet-lite).

The Compose guide explicitly supports PostgreSQL messaging with `SERVER_MSGQUEUE_KIND=postgres` and removal of RabbitMQ references. Its example contains `latest` images, example database credentials and insecure transport flags: it is evidence of topology, not a deployable Loom security configuration. Pin images, generate secrets, use TLS at public endpoints and preserve database/config volumes. Coolify need only host containers and proxy endpoints; direct Hatchet workers additionally require a working gRPC route. [Compose guide](https://docs.hatchet.run/self-hosting/docker-compose).

The upstream LICENSE is MIT. Preserve upstream notices in distributions; this finding is not an audit of every transitive container/package license. [Hatchet LICENSE](https://raw.githubusercontent.com/hatchet-dev/hatchet/main/LICENSE).

Retention defaults to 30 days for old final workflow runs and events, configurable per default tenant retention setting. Keep Loom audit/usage records under its own retention policy and test long-lived waits against event retention. [Data retention](https://docs.hatchet.run/self-hosting/data-retention).

## Phase 2 experiments and acceptance evidence

These are required experiments, not completed tests. Record image digests, Python package lock, configuration, timestamped state, attempt IDs and observed invocation counts.

1. **Restart during wait:** start a fake-runtime goal, record one attempt, yield and confirm its process exits. Stop the Loom server, Hatchet worker and engine while preserving PostgreSQL. Restart, deliver the matching event, and require one continuation with the same run/session binding. Also deliver while the orchestration worker is stopped.
2. **Event registration race:** deliver before, during and after wait registration, including across an outage longer than the chosen lookback window. Use a transactional Loom inbox/outbox with authoritative wait satisfaction so a finite upstream lookback cannot lose a valid wake. Assert one continuation.
3. **Duplicate/stale deliveries:** redeliver the same source event and the same internal wake; deliver an old generation after a new wait. Assert no extra attempt and inspect the recorded rejection reason.
4. **Eviction/replay:** use a short eviction TTL; terminate and restore the orchestration worker several times. Assert code replay never creates another already-claimed model attempt. Verify a completed child is not relaunched; independently test a crash after side effect but before acknowledgment.
5. **Wait versus timeouts:** run beyond scheduling and execution defaults, restart mid-timer, and verify its original deadline. Exercise cancellation during wait and late events after cancellation. Set an explicit supported lifetime policy rather than assuming unlimited waits.
6. **Affinity/disconnection:** with two fake machines, disconnect the bound one and deliver a wake. Require no execution on the second. Reconnect and restart the bound process; preserve Loom identity while observing Hatchet worker-ID behavior if testing native workers.
7. **Partition ambiguity:** disconnect an executing fake runtime before reporting completion. Ensure recovery reconciles the persisted local attempt and refuses overlapping launches. Expired server leases alone must not authorize duplicate side effects.
8. **Authorization:** reject missing/invalid credentials, another worker's claim, wrong session, stale worker incarnation and forged labels. Verify remote workers cannot read unrelated payloads through Loom endpoints.
9. **Long suspension:** maintain WAITING with all runtime processes absent; verify unchanged runtime-call count, active-time accounting and provider-call instrumentation before the wake. Repeat across control-plane restart and a retention boundary in an accelerated test configuration.

Unresolved before implementation: compatible released engine/SDK versions for eviction and scoped lookback; precise retry/reassignment behavior for the selected SDK under a network partition; wait timeout and retention interactions; authentication granularity for any future direct worker integration. The listed experiments turn those unknowns into explicit release gates.
