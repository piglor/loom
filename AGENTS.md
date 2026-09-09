## Loom product boundary

Treat this repository as a monorepo for Loom's backend, browser console,
machine agent/CLI, shared contracts, and future iOS/Android applications.
Evaluate UI choices against native mobile requirements, not browser rendering
alone. Rust rendering was an initial preference, not a settled requirement to
use Rust on every platform. Backend language and UI language are separate
decisions; recommendations must not be presented as completed migrations.

Accepted client direction: React + TypeScript + Vite on web, future React Native
on iOS/Android. Accepted backend target: Go with PostgreSQL and Hatchet; keep Rust
for the Agent. Follow ADRs 0012/0013 for incremental migration and proof gates.
Do not replace the existing Python writer/orchestrator until its replacement
passes recovery/compatibility tests and in-flight Runs are accounted for.

Keep the durable control plane integration-agnostic. GitHub is one external
integration plugin, and Codex is one agent-runtime adapter; neither defines the
core Goal/Session/Wait/Event model. Use `piglor/loom` as the first GitHub testing
repository, with bounded test resources and existing trust/authorization checks.
Also test the same core suspension/resumption contract with a non-GitHub event
source. Do not make repository, PR or SHA mandatory on generic core objects.

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
