# Codex adapter conformance

The Rust `loom_agent::codex::Codex` adapter uses private stdio app-server JSON-RPC,
not terminal automation. It initializes, starts a provider thread, resumes an
exact thread, starts a schema-constrained turn, observes its completion and stops
the owned process group. Unexpected approval/tool requests fail closed. Frames,
requests, notification buffering and runtime reads/writes are bounded.

Official [app-server documentation](https://learn.chatgpt.com/docs/app-server)
and locally generated Codex 0.153.4 schemas informed the implementation. The
provider binding must be persisted before a model turn. The standalone probe
syncs both receipt and parent directory before starting that turn.

The adapter now enforces an explicit persistence callback through
`open_bound_thread` and admits only one model turn per process. The server also
implements immutable, worker/claim-scoped provider binding and validates bound
stop receipts. See [session admission](session-binding.md) for the tested contract
and remaining daemon integration/supervision gates.

## Live proof

On 2026-09-09, two actual model turns passed the continuity check with installed
Codex 0.153.4: a newly generated marker was recalled after stopping the first
app-server and resuming the exact saved thread in a second process. The marker
was not repeated in the continuation input. Provider-reported usage was retained
in the ignored local proof receipt, with no invented price/savings estimate.

To run this explicitly billable check with a local authenticated Codex install:

```bash
cargo build --workspace --locked
target/debug/loom-codex-probe /absolute/path/to/codex \
  /absolute/empty/test-workspace /absolute/new/private-receipt.jsonl
```

The probe asks Codex not to use tools and requests a read-only filesystem sandbox.
That sandbox does **not** establish containment of configured MCP tools or remote
side effects. Use an isolated Codex configuration without privileged integrations
for production acceptance. The adapter currently inherits the invoking user's
Codex configuration; it is deliberately not wired into the remote daemon.

Process-group termination is not sufficient containment for arbitrary subprocesses
that escape that group. A production worker must have an enforced supervision
boundary (for example a correctly configured cgroup/container), local workspace
policy, a lease watchdog, and an explicit ambiguous-execution resolution path.
Do not enable unattended privileged coding based on this probe alone.

CI runs a deterministic protocol fixture, including notifications arriving before
the turn-start acknowledgment and unexpected approval requests. CI does not claim
to run the live provider test without configured credentials. Real CI-driven
agent continuation remains a separate end-to-end release gate.
