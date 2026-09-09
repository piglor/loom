# Loom positioning against durable agent frameworks

Research date: 2026-09-09. Sources: current official documentation. This is a
bounded positioning assessment, not an exhaustive feature audit or benchmark.

## Finding

Durable waiting, resumable state, event matching, worker routing, and execution
visibility are established capabilities. Loom's promising differentiation is
their packaging around **the user's existing agent session, machine, and goal**.
That is a product hypothesis requiring adoption and usability evidence; the
sources below do not establish a unique technical moat.

## Comparators

| Comparator | Established capabilities | Implication for Loom |
| --- | --- | --- |
| Temporal | Durable AI workflows recover after failures and multi-day approval waits; its integrations include multiple agent frameworks. Persisted timers require no additional worker resources while waiting. | “Agents can wait and recover” is table stakes, and framework neutrality alone is insufficient. [Durable AI](https://docs.temporal.io/ai), [timers](https://docs.temporal.io/workflow-execution/timers-delays). |
| Inngest / AgentKit | Durable agents memoize completed steps, suspend on events, group runs into sessions, and expose traces with timing. `waitForEvent` supports matching predicates and timeouts. AgentKit documents approval tools using that primitive. | Neither zero model execution while suspended, session grouping, nor filtered event waits is unique. [Durable agents](https://www.inngest.com/docs/learn/durable-agents), [event waits](https://www.inngest.com/docs/features/inngest-functions/steps-workflows/wait-for-event), [AgentKit approvals](https://agentkit.inngest.com/advanced-patterns/human-in-the-loop). |
| LangGraph | Checkpointers persist graph state into threads. Interrupts pause indefinitely and resume through `Command` using the same `thread_id`. The interrupted node restarts from its beginning, requiring care with preceding side effects. | Persistent conversation identity and human input are established. Loom must explain that its external runtime session binding is a different integration contract from resuming graph execution. [Persistence](https://docs.langchain.com/oss/python/langgraph/persistence), [interrupts](https://docs.langchain.com/oss/python/langgraph/interrupts). |

Worker locality is also established: Temporal explicitly documents routing to
specific hosts when subsequent activities need their local files or sessions.
Inngest Connect supports persistent connections from workers and documents local
development. These capabilities preclude claiming that only Loom can execute on
user-controlled machines. [Temporal worker affinity](https://docs.temporal.io/design-patterns/worker-specific-taskqueue),
[Inngest Connect](https://www.inngest.com/docs/setup/connect).

A dashboard alone does not distinguish Loom. Temporal's Web UI includes workflow
state, event history, workers, pending activities, and search; Inngest's durable
agent docs describe structured execution traces and timing. A potentially useful
distinction is organizing that information into an end user's goal and attention
queue. [Temporal Web UI](https://docs.temporal.io/web-ui),
[Inngest observability](https://www.inngest.com/docs/learn/durable-agents#observability--debugging).

## Narrower opportunity and proof required

These are proposed product directions, not claims that competitors cannot build
them or that Loom already implements them:

| Product promise | Concrete acceptance evidence |
| --- | --- |
| Adopt work the user already started | Import an existing supported agent thread with its goal and workspace; transfer ownership safely; survive restart; resume that exact thread without asking the user to reconstruct context. Measure setup steps and time. |
| Keep work on the right machine | Display the bound worker and workspace; surface an offline worker as a distinct state; preserve queued work; prevent silent fallback to another machine or a new thread. Prove exclusive execution ownership. |
| Wake only for an actionable change | Show the wait predicate and event provenance, reject stale/duplicate/untrusted events before model execution, and explain accepted and ignored events. Test races around wait registration. |
| Make waiting understandable | Show current blocker, who or what can resolve it, next timeout, and the exact continuation target. Separate “waiting for external event,” “waiting for worker,” and “needs your decision.” |
| Make efficiency auditable | Derive active, suspended, and worker-unavailable durations from durable transitions; record runtime starts and usage receipts; demonstrate zero model calls during a bounded suspended interval. Do not convert waiting duration into invented token or cost savings. |

Actionability needs more than an event name or resource identifier. For a GitHub
example, an event from an obsolete revision may no longer satisfy the goal;
for a non-GitHub example, an approval may refer to an older document version.
These are proposed Loom semantics and tests, not evidence of missing competitor
functionality. Inngest already supports event predicates, and its documentation
explicitly warns that `waitForEvent` listens from registration rather than
automatically consuming earlier events. [Event-wait semantics](https://www.inngest.com/docs/features/inngest-functions/steps-workflows/wait-for-event).

## Positioning and limits

Candidate wording: “Keep your agent's goal moving across long waits. Loom
remembers what it is waiting for and resumes the right session on your machine
when the right event arrives.” Present this as the intended product promise
until the acceptance tests pass.

The implementation substrate (Hatchet and PostgreSQL) is an engineering choice,
not the user benefit. Keep GitHub as the first integration and Codex as one
runtime adapter; validate the same suspension contract using another event
source without mandatory repository, PR, or SHA fields in generic objects.

The reviewed documentation does not demonstrate turnkey adoption of an already
running third-party coding-agent conversation into these three comparators.
That absence is insufficient to claim they cannot support it. Likewise, this
research does not establish demand, switching willingness, comparative setup
time, pricing advantage, or actual inference savings. Loom's own live-session
adoption remains unproven: see [current-session adoption](current-session-adoption.md).
