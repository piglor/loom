# ADR-0014: Repeatable execution with pre-registered waits

**Status:** Proposed — core-only implementation experiment
**Date:** 2026-09-09

## Context

The accepted product direction requires repeated yield/resume cycles, not a
two-attempt demonstration. Existing deployed Goals and Rust v1 receipts must
retain their meaning. This is not permission to enable privileged Codex workers.

## Decision under test

Opt in internally to an immutable event-driven Run policy with an explicit final
completion condition and a bounded attempt budget. Keep the public creation API
and remote-worker v1 contract unchanged until adapter conformance is proven.

While RUNNING, the admitted attempt may prepare one next wait, using the expected
previous generation as a compare-and-swap. Persist the closed previous wait in
history and replace the current projection atomically. A matching early event
may satisfy the new wait but cannot admit another attempt while RUNNING. Only a
confirmed stop receipt arms it and transitions the Goal. Replay cannot replace a
wait, change context, or spend inference twice.

Explicit outcomes are yield, complete, and blocked. Completion requires the
current, satisfied, closed wait to match the immutable completion condition.
Invalid completion or a yield without a prepared dependency records the runtime
as stopped and blocks the Goal; it must not discard stop evidence. Attempt
exhaustion blocks before admitting more execution. No automatic reasoning retry.

## Alternatives and consequences

Removing phase bounds alone would retain automatic completion and lose history.
A new transport or workflow engine does not solve domain admission. Replacing
the current wait projection with a many-row join would break the deployed Go
reader; a separate immutable history table keeps that projection compatible.

The internal `phase` column becomes an attempt sequence for opt-in Goals; v1
retains phase 0/1 semantics. This temporary naming is retained for compatibility.
Go metrics must include history before this policy is exposed publicly. Generic
event sources work first; GitHub binding generations and worker receipts still
need their own conformance work. History is not an event-sourcing framework.

## Validation and release gate

Test repeated cycles, early/duplicate/stale events, stop replay, foreign attempts,
uncertain recovery, completion refusal, budget exhaustion, and v1 regression.
Do not deploy or expose the new policy until the integration and migration proofs
pass. An old writer must not handle new-policy Goals: roll forward, or first stop
and reconcile those Goals. This experiment is not an autonomous Codex proof.

## Basis

This is a Loom domain decision derived from the product brief, not an upstream
API guarantee. See ADRs [0001](0001-product-domain-model.md),
[0006](0006-agent-runtime-interface.md), [0007](0007-event-normalization-correlation.md)
and [0013](0013-go-backend-migration.md). No Hatchet or Codex API changes are needed
for this core-only experiment.
