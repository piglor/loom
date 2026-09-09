# Can Loom adopt the session building Loom?

Research date: 2026-09-09. Scope: read-only local metadata and official protocol
documentation; no model thread was launched, resumed, forked, or interrupted.

## Conclusion

This conversation is a useful product acceptance case, but Loom cannot yet claim
to manage its lifecycle. Local identifiers and matching persistence filenames
are available. That establishes discoverability, not safe ownership transfer.
Do not resume a second writer against this live conversation as a demonstration.

## Observed evidence

- Installed executable reports `codex-cli 0.153.4`; its app-server supports
  private stdio. The CLI also accepts an explicit ID for interactive resume.
- A check restricted to the presence of `CODEX_THREAD_ID`, `CODEX_SESSION_ID`,
  `CODEX_ROLLOUT_PATH`, and `CODEX_HOME` found both ID variables present. The
  latter two variables were absent. A filename-only lookup found a local
  persisted filename matching each ID. No transcript bodies or authentication
  files were opened, and no actual IDs or persistence paths are recorded here.
- The research ran in a child agent: its thread ID and session ID differ. An
  identifier inherited in one execution context must not be guessed to identify
  the user-facing root thread. The main agent independently performed the same
  restricted check: its two IDs are equal, with two matching local filenames.
  Neither check validates history completeness, uniqueness of a usable rollout,
  supported history format, or actual resumability.
- [`Codex::open_thread`](../../crates/loom-agent/src/codex.rs) uses `thread/resume`
  with the supplied thread ID and rejects a different returned ID. It has no
  import/adoption operation or handshake with this conversation's current owner.
- [`GoalCreate`](../../server/loom/models.py) accepts only `demo` and
  `remote-demo`; the [Rust daemon](../../crates/loom-agent/src/main.rs) accepts
  only `remote-demo`. The separate [adapter probe](../codex-adapter.md) proved
  newly created thread continuity, not takeover of an existing harness session.

## Official interface and its limits

App-server resumes by `thread.id`; `thread.sessionId` identifies the live session
tree root and can differ for forked threads. `thread/read` can omit turns and
does not resume the thread. `turn/start` initiates execution; `turn/completed`
reports completed, interrupted, or failed status. Resuming can restore persisted
dynamic tools, and configuration can include required MCP servers. These are
important compatibility and authority concerns for an adopted conversation.
The current reference also says paginated-history resume is not yet supported.
[Official app-server reference](https://learn.chatgpt.com/docs/app-server).

The documentation reviewed does not establish an atomic ownership-transfer API
between this harness and an independent Loom worker. Nor does it establish that
an idle/not-loaded observation in one process excludes a different writer.
This is a limit of the evidence, not a claim that Codex cannot support handoff.

## Missing safe handoff

Loom must bind the exact provider thread, logical Loom session, worker, workspace,
and allowed tools before execution. The incumbent must acknowledge its final
turn has ended and relinquish execution authority. The new owner needs a
durable exclusive ownership epoch and a supervision boundary that actually
prevents the old owner from writing. A database lease alone cannot fence an
uncooperative independent Codex process.

After ownership transfer, Loom must durably record a structured wait, terminate
the owned execution, and resume only on its authorized event. Recovered history
does not recreate all harness tools or grant permission to use credentials and
production integrations. The current adapter inherits local configuration and
rejects unsupported tool/approval requests; a read-only filesystem sandbox is
not containment for remote tools.

## Next acceptance test

Use a disposable, isolated Codex configuration and workspace before this live,
privileged session:

1. An incumbent starts a thread, produces a harmless random continuity marker,
   and records a deployment/CI wait. Persist its exact binding before any turn.
2. Attempt adoption while the incumbent remains active: reject it without a
   model call. Release must be explicit and auditable, not a timeout guess.
3. Incumbent finishes and exits its enforced supervision boundary. Transfer the
   ownership epoch; attempts using the old epoch must fail before runtime launch.
4. Restart Loom and its worker while waiting. Assert no runtime process or
   `turn/start` occurs during the suspended interval.
5. Deliver wrong-version and duplicate events, then one authorized matching
   event. Exactly one continuation resumes the same thread on the same worker.
6. Recall the marker without putting it in the wake input. Assert two useful
   turns, one wake, no overlapping writers, and preserved usage/stop receipts.
7. Missing history, unknown execution ownership, unsupported tools, and worker
   mismatch must fail closed without silently starting a replacement session.

Only after this test and a compatible release mechanism for the actual harness
should the live Loom-building conversation become an adoption target. Until
then, it is a workflow case study, not an inference-savings claim.
