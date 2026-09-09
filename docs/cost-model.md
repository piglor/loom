# Usage and cost model

Status: proposed accounting contract. No measured savings are available yet.

## What Loom can measure

| Measure | Definition / evidence |
| --- | --- |
| Goal wall time | First authorized start to terminal time, or observation time while active; creation delay reported separately |
| Active agent execution | Union of confirmed runtime-attempt intervals for elapsed time; sum of intervals separately for machine/runtime resource consumption |
| Confirmed suspended time | Intervals with an armed wait and all runtime execution confirmed stopped |
| Blocked / uncertain time | Missing runtime evidence, unresolved launch, policy intervention or other blockage; never counted as proven suspension |
| Scheduling time | Ready/dispatch delay before runtime starts |
| Wake-ups | Unique satisfied conditions resulting in admitted continuation; distinguish evidence-only transitions |
| Execution attempts | Durable attempts, separated into admitted, actually started and failed-before-start |
| Tokens | Provider-reported observations with scope and dedup identity; absent is null |
| Provider cost | Reported monetary usage or a separately labeled estimate with applicable price version and token categories |
| Runtime/tool cost | Separately metered infrastructure/tool usage; not inferred from model tokens |

An execution interval includes local tool activity and provider/network latency. It is **not** a measurement of GPU inference duration or useful reasoning. Do not label it “active inference time” without instrumentation that establishes that narrower quantity.

For MVP's single active Session, nonoverlapping phase intervals partition Run wall time into execution, confirmed suspension, scheduling and blocked/unknown time. Future parallel Sessions need union-based wall-time metrics; summed attempt duration can exceed wall time. Goal metrics aggregate Runs without counting terminal gaps as active pursuit.

## Time and token integrity

Use server UTC for state transitions; record worker monotonic elapsed duration plus observed timestamps and receipt time. Delayed reports must not silently extend active duration to their receive time. Keep raw observations and derived estimates separate. Across a worker crash or clock discontinuity, record bounds/unknowns instead of a false precise interval.

Provider notifications can repeat or contain cumulative counters. Store source observation ID, thread/turn, counter scope, raw values and collection time. Derive deltas only within a known counter epoch; do not sum cumulative totals or subtract across resets/compaction without validating semantics. Missing usage is null, not zero. Preserve provider/model changes per Attempt and price schedule/currency for cost estimates.

Codex exposes usage notifications, while noninteractive JSON output documents token categories. Whether the pinned app-server supplies complete per-turn accounting and whether a subscription maps to a marginal dollar charge remain validation questions. [Codex research](research/codex.md)

## Honest presentation

An illustrative display, **not an observed result**:

```text
Goal lifetime       2h 46m
Agent execution        31m
Confirmed suspended 2h 15m
Wake-ups                 4
81.3% of lifetime had confirmed suspended execution.
```

Use `confirmed_suspended / goal_wall_time` for the suspended share, clearly including scheduling/unknown time in the denominator. A small active share is not automatically good: a blocked Goal can also have low execution time. Show completion, retry and human-intervention outcomes alongside timing.

## No fabricated counterfactual

Do not calculate “tokens avoided” as waiting seconds multiplied by an arbitrary token rate. An idle process may consume no model tokens even without Loom; the comparison must establish actual polling/resume behavior.

Any later savings study needs a disclosed baseline workflow, model/version, polling cadence, identical task/input conditions, observed requests and token/cost categories, sample size, uncertainty and completion-quality comparison. Separate observed spend from modeled avoided spend and account for Loom infrastructure costs. Subscription billing may make dollars avoided unquantifiable even when token calls were avoided.

## No-model waiting proof

At confirmed WAITING, record stop receipt and verify the runtime/process group is absent. Instrument runtime-start and provider-request boundaries, then keep the Goal waiting, restart the control plane, and show counters remain unchanged until the qualifying event. The same proof must resume the exact provider thread and worker and show a new Attempt only after admission. Missing telemetry during an outage does not prove zero activity.

The honest initial claim is “Loom started no runtime and made no model request during this observed suspended interval.” Provider-side billing behavior and activity from unrelated processes are outside that claim.
