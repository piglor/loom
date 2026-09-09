# Case study: Loom should orchestrate its own development

Assessment date: 2026-09-09. This is an evidence-backed self-review and proposed
next acceptance case, not a claim that Loom currently owns this Codex session.
No production settings, webhooks or agent sessions were changed for this review.

## Scope constraint: GitHub is one external plugin

The user explicitly reaffirmed this constraint during the review: GitHub is one
external integration plugin, and `piglor/loom` is the first testing repository.
It is not the product's mandatory workflow or the core domain model.

The generic core owns Goals, Runs, Sessions, Workers, execution admission,
versioned Waits, normalized Events, deterministic matching and completion policy.
GitHub-specific authentication, delivery IDs, repository/PR/SHA/run interpretation,
API reconciliation and failure-context extraction belong to the GitHub integration.
Codex is a separate agent-runtime adapter. Neither GitHub nor Codex should be
required for core wait/recovery tests. Do not build a large plugin SDK for this
first integration; establish and test the boundary with a small adapter.

Use the same core contract for a second non-GitHub fixture, such as a deployment
completion event, alongside GitHub's CI event. Repository, PR and SHA must never
be required fields on every core Goal, Wait or Session. Changes to this repository
for future live tests must be bounded and must not enable privileged production
execution merely because the repository belongs to the project.

## The actual Goal

Build, publish and operate a usable Loom release on the authorized Piglor server.
The conversation supplied the objective, then deployment credentials, target
domain and explicit public GitHub/image-publication authorization. These are
distinct policy/input updates, not reasons to lose the original Goal.

The agent performed useful research, coding, debugging and deployment work, but
also repeatedly checked unchanged CI/deployment state and reported non-actionable
updates. The user had to say to continue and to find another diagnostic route.
This is a direct example of the human orchestration burden Loom is meant to remove.

## What this session reveals

| Observed step | What should own it | When reasoning is needed |
| --- | --- | --- |
| Research, implementation, interpreting a deployment failure | Codex execution attempt | New problem or evidence requires a decision |
| CI running with no actionable result | Durable Loom wait | Only a relevant failure, changed requirement or decision gap |
| All release checks pass and deployment is already authorized | Deterministic release policy/action | Only if an unexpected condition needs judgment |
| Deployment queued, stale service status, endpoint recovering | Bounded non-model connector reconciliation/timer | A terminal failure or unresolved inconsistency, not every status read |
| Missing service-log API | Active diagnostic work | Exhaust safe alternate evidence sources before requesting human work |
| User authorizes publishing or selects a target | Authenticated human input/policy update | Resume if that makes the next action possible |
| GHCR package requires a web-only visibility change | Specific human prerequisite, while independent work continues | After verified completion, and only if reasoning remains necessary |
| Rerun passes after a startup timeout | Preserve unresolved reliability evidence | Passing once is not a diagnosis of the earlier failure |

Sleeping inside a tool does not by itself establish model inference throughout
that interval. The avoidable behavior was repeatedly bringing the model back to
inspect unchanged state and compose status messages. The current conversation
was not durably yielded to Loom, and no measured model-cost baseline exists.
Do not present the finite runtime's 0.081 seconds as this session's model usage.

## What is actually proven

The [production record](production-rollout.md) shows a finite Goal remaining
WAITING through a Loom redeployment, retaining the same logical Session, rejecting
a stale event and deduplicating redelivery. It completed with two stopped
attempts and one wake-up. The API health check returned 200 again during this
assessment.

The GitHub history is also material: [first full run](https://github.com/piglor/loom/actions/runs/34300395655)
passed, a [documentation-only rerun](https://github.com/piglor/loom/actions/runs/34301100784)
timed out before the initial WAITING state, and the [diagnostic-enabled run](https://github.com/piglor/loom/actions/runs/34301587137)
passed. That sequence supports an intermittent setup/recovery-test problem, not
a proven root cause or a claim that adding diagnostics fixed it.

## Concrete blockers in today's implementation

1. **The worker cannot run this session.** `GoalCreate.runtime` accepts only
   `demo` and `remote-demo`; the Rust daemon also rejects other runtimes. The
   real Codex adapter is a standalone probe, not an admitted daemon runtime.
   Sources: `server/loom/models.py`, `crates/loom-agent/src/main.rs`,
   [Codex adapter evidence](codex-adapter.md).
2. **The lifecycle is still a two-attempt demonstration.** One wait per Goal,
   phase-limited attempts and automatic completion after the second successful
   attempt cannot represent CI → fix → CI → deployment → verification. A stopped
   turn must not automatically mean the Goal is complete. Sources:
   `server/loom/schema.sql`, `server/loom/store.py` (`create`, `finish`).
3. **This session's CI trigger is unsupported.** The real run reports
   `event=push`, whereas `GitHubBinding` requires a PR and `matches` requires
   `event=pull_request`. Add an explicit trusted push-workflow binding rather
   than pretending a push has a PR or weakening existing fork protections.
   Sources: `server/loom/github.py`; GitHub Actions run API for run 34301587137.
4. **No live event-to-Codex route is established.** Repository webhook enumeration
   returned an empty list. That does not enumerate every possible GitHub App
   installation webhook, so it is not proof that no App exists. The implementation
   itself still rejects privileged runtime bindings and lacks live freshness and
   missed-delivery reconciliation. Source: [GitHub ingress scope](github-ingress.md).
5. **Thread identity is not execution ownership.** The root environment exposes
   current thread/session identifiers, and matching local session filenames exist.
   That permits further read-only eligibility checks; it does not establish safe
   concurrent resumption, compatibility, or ownership transfer. Do not start a
   second executor against this active thread. See
   [session-adoption research](research/current-session-adoption.md).
6. **Completion and actionability need explicit criteria.** Source public,
   image built, image anonymously pullable, API healthy, acceptance proof passed
   and privileged-agent safety approved are different facts. Likewise, a CI run
   for documentation changes need not wake a release agent whose deployed source
   is unchanged. Audit these criteria instead of treating every completion event
   as an instruction to invoke a model.

## Recommended next vertical slice

The next product milestone should be **one real agent session, repeated durable
yields, and a plugin-delivered correlated resumption**, not a larger UI or another
finite demo. Codex is the first runtime adapter; GitHub is the first external
integration under test, not a special case embedded into the core lifecycle.

1. Replace phase-count completion with explicit execution outcomes and multiple
   versioned wait/attempt cycles. Preserve the existing fake-runtime regression
   suite and prove three or more yields without adding model calls.
2. Establish a controlled session handoff: bind the exact provider thread to a
   worker/workspace, persist a checkpoint and permissions, confirm the old owner
   is idle/released, then admit one new owner with stale-owner rejection and
   supervised process shutdown. Never serialize this transcript's credentials.
3. Connect the existing Codex adapter to that admission/stop-report boundary,
   initially in an isolated, nonprivileged workspace with no inherited production
   MCP integrations. Use an authenticated timer/test event first to isolate
   session continuity from GitHub integration failures.
4. In the GitHub plugin, add explicit push-workflow correlation for the actual
   Loom CI: repository ID,
   workflow ID, expected commit, run ID/attempt and authorized trigger context.
   Validate source authenticity and current evidence before admitting execution;
   retain PR/fork restrictions for their existing binding type.
5. Run the real exercise using `piglor/loom` as the controlled GitHub test
   repository: perform a useful change, yield,
   stop the runtime, restart/disconnect the worker while waiting, deliver unrelated
   and stale events, then deliver a matching CI result. Resume the exact thread
   once. Repeat the cycle before asking completion policy to close the Goal.
6. Run the same core suspension/recovery contract with a non-GitHub event fixture.
   A passing GitHub-specific path alone is not evidence of a generic control plane.

The current session can supply the goal, artifacts, constraints and acceptance
story now. Adopt its exact thread only after the current owner can safely hand it
off and compatibility is verified. If that cannot be proven, use a separately
enrolled test thread with a sanitized checkpoint and explicitly call it a new
session—not exact-session continuity.

## Acceptance and measurement

- No admitted runtime/model requests during the suspended interval; corroborate
  attempt records with worker process/request instrumentation, not just Goal state.
- One active owner; same provider thread, workspace and worker on continuation.
- Wrong SHA, old run attempt, duplicate delivery, stale lease and unrelated event
  cause no extra execution. Reconnect never silently moves a local session.
- Multiple waits survive restart; a second yield does not complete the Goal.
- Deterministic success can advance policy without a model wake; human attention
  is requested only for a named remaining input/authority gap.
- Count attempts, wake reasons and provider usage when available. Separate
  deployment-only waits from unrelated productive work and from unknown intervals.
  Do not extrapolate tokens or money saved from wall-clock time alone.

This review creates local findings only. It does not enable Codex in production,
register a webhook, fork/resume a conversation, or publish further changes.
