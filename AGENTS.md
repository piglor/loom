## Loom product boundary

Treat this repository as a monorepo for Loom's backend, browser console,
machine agent/CLI, shared contracts, and future iOS/Android applications.
Evaluate UI choices against native mobile requirements, not browser rendering
alone. Rust rendering was an initial preference, not a settled requirement to
use Rust on every platform. Backend language and UI language are separate
decisions; recommendations must not be presented as completed migrations.

Accepted client direction: React + TypeScript + Vite on web, future React Native
on iOS/Android. The control plane and all maintained tooling are Go with
PostgreSQL and Hatchet; the machine Agent is Rust. The retired implementation
was removed on 2026-09-09. Do not restore it, add a service bridge to it, or use
another language as a permanent proof-tool dependency.
Use Ent schemas and generated typed persistence builders rather than handwritten
CRUD SQL. PostgreSQL remains the initial database for both Lite and Server;
Hatchet Lite is the low-volume deployment, not a different orchestration engine.
Keep domain policy, generated persistence, integrations and runtime adapters
separate. SQL exceptions are limited to reviewed migrations, database-level
coordination and genuinely specialized operations behind the persistence seam.

Keep the durable control plane integration-agnostic. GitHub is one external
integration plugin, and Codex is one agent-runtime adapter; neither defines the
core Goal/Session/Wait/Event model. Use `piglor/loom` as the first GitHub testing
repository, with bounded test resources and existing trust/authorization checks.
Also test the same core suspension/resumption contract with a non-GitHub event
source. Do not make repository, PR or SHA mandatory on generic core objects.
Core persistence must express generic external resource bindings, event receipts
and versioned wait conditions, including external-system approvals. Adding an
integration must not require a new core table or a provider-specific Goal state.
Prefer the generic binding/inbox model over one binding table per provider;
plugin-owned storage is justified only for genuinely provider-specific needs.
Verify approvals deterministically against tenant, integration instance,
resource, version and authorization before waking the bound Session/Worker.
An event satisfies a wait; it does not authorize unrelated execution or imply
Goal completion. Preserve existing provider data during migration, and test
non-code workflows as well as CI before claiming integration independence.

The Loom-building session is a reference acceptance case, not proof that the
running conversation has been handed over to Loom. See
`docs/session-case-study.md` and `docs/research/current-session-adoption.md`.

<!-- hatchet-skills:start -->
## Hatchet Agent Skills

Hatchet agent skills are installed in `skills/hatchet-cli/`. When working with this project's Hatchet workflows, read the relevant reference:

- **Setup CLI**: `skills/hatchet-cli/references/setup-cli.md`
- **Start worker**: `skills/hatchet-cli/references/start-worker.md`
- **Trigger & watch**: `skills/hatchet-cli/references/trigger-and-watch.md`
- **Debug a run**: `skills/hatchet-cli/references/debug-run.md`
- **Replay a run**: `skills/hatchet-cli/references/replay-run.md`

Full skill: `skills/hatchet-cli/SKILL.md`
<!-- hatchet-skills:end -->
