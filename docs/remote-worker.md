# Outbound Rust worker

Implemented boundary: a Linux Rust daemon with worker-scoped revocable credentials,
explicit workspace-reference policy, durable SQLite claim/stop journal, and
outbound HTTP(S) delivery. Only the finite `remote-demo` runtime is enabled.
This does not yet execute Codex or arbitrary commands.

```bash
make migrate
make agent
.venv/bin/loom enroll --workspace-ref sandbox \
  --state-dir /absolute/private/loom-worker-state \
  --output .loom/worker.json --allow-insecure-localhost
target/debug/loom-agent --config .loom/worker.json
```

Enrollment is an administrator operation; the generated token only authorizes
that worker's mailbox. The enrollment command prints the worker ID but not its
credential. Configuration is created exclusively with mode 0600; state must be
owned by the daemon user and mode 0700. Never reuse one enrollment across machines.
HTTPS is mandatory except explicitly enabled loopback development. Redirects
are rejected, including same-origin redirects. Do not delete or copy the journal
to bypass an ambiguous execution or change worker identity.

Create a remote Goal using the printed ID:

```bash
.venv/bin/loom goal create --title 'Remote wait' --objective 'Wait then continue' \
  --runtime remote-demo --worker-id WORKER-UUID \
  --source demo --type ready --resource example --version v1
```

The central Hatchet worker queues the command. An offline worker leaves the Goal
WAITING with `waiting_reason=worker`; another worker cannot claim it. Once the
finite runtime stops, the Goal waits for its exact event. Deliver the event using
the [quickstart](quickstart.md); continuation stays bound to the same Session.

Queued work can be cancelled safely. Claimed or uncertain execution cannot be
cancelled without stop evidence. The current finite daemon halts on an ambiguous
RUNNING journal entry rather than repeating it; automated reconciliation and
privileged process supervision remain production gates. A control-plane worker
restart must not mark remote execution stopped or steal its assignment.

`make prove-remote` uses real HTTP, PostgreSQL and two Rust daemons on port 18000.
It stops the bound worker during a wait and restarts it with the same journal.
Mailbox dispatch is invoked directly in this test; the separate `make prove`
validates Hatchet restart behavior. The composed full Codex/GitHub proof remains
required. Proof credentials are ephemeral; its organization/Goal evidence remains
in the local database, not in a production tenant.

Implementation notes: pinned Rust toolchain/Cargo lockfile; reqwest blocks redirects
and bounds response size/time; SQLite WAL uses FULL synchronous commits. See
[reqwest](https://docs.rs/reqwest/0.12.28/reqwest/blocking/struct.ClientBuilder.html)
and [SQLite synchronous](https://www.sqlite.org/pragma.html#pragma_synchronous).
Filesystem guarantees assume a local filesystem and an uncompromised worker user.
