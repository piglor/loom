# ADR-0017: Container-contained Codex attempts

Status: Proposed; implementation authorized by continuation instruction.
Date: 2026-09-09.

Use the existing private app-server adapter inside one Docker container per
execution attempt. An explicitly enrolled `codex-container` worker has local,
immutable configuration for its image ID, disposable workspace and session store.
Remote requests cannot supply paths, images, commands, credentials or mounts.

```text
claim → local RUNNING journal → create bounded container → open exact thread
  → local + server binding persisted → one turn → validate outcome
  → kill/remove owned container → journal STOPPED → report → durable wait
```

Docker isolates the runtime from the worker's credential and Docker socket. Only
the dedicated workspace and dedicated provider context directory are mounted;
never a developer home or production repository with credentials. The root
filesystem is read-only, capabilities dropped, privilege escalation disabled,
and CPU/memory/PID limits applied. A fixed in-container deadline stops PID 1 even
if the worker disconnects. The initial adapter uses a read-only workspace.
Network egress remains enabled for provider access: this is a controlled preview,
not hardened execution of arbitrary untrusted code on a private network.

The worker owns the container name and label derived from the command UUID.
Uncertain local execution is never automatically relaunched. A stop receipt is
not written unless container removal succeeds. Crashes before receipt persistence
leave an inspectable unresolved attempt rather than claiming zero active runtime.
Session files persist on the same worker, and an existing provider binding cannot
be replaced by a new thread. Only protocol 2 and repeatable Goals are admitted.

Alternatives: a host process group cannot contain descendants that create new
sessions; requiring the whole worker to be disposable loses easy control over
per-attempt stop evidence. MicroVMs offer stronger isolation but add a separate
operator/deployment boundary; revisit before arbitrary public code is admitted.

Schema 10 expands the Session runtime constraint. No existing runtime is renamed;
old workers retain their protocol. Roll back only after draining or accounting for
new-runtime Runs. GitHub/GitLab privileged binding restrictions remain in place
until their independent authorization/freshness gates are met.

Acceptance: exact-thread resume through the actual outbound daemon; stopped
container during waits; malformed output, lost binding acknowledgment, runtime
timeout, wrong provider and receipt replay tests; credentials never in images,
logs or repository. A live model proof is explicit and bounded; CI uses a fake
app-server in the same container/worker transport plus real Docker and PostgreSQL.

Evidence reviewed 2026-09-09:

- [OpenAI app-server](https://learn.chatgpt.com/docs/app-server): exact `thread.id`
  resume, one `turn/start`, output schema and completion notifications. Installed
  Codex 0.153.4 remains the compatibility pin; experimental APIs are not needed.
- [Docker run](https://docs.docker.com/engine/containers/run/): process, mounts,
  resource limits, capability and read-only controls. The combination and
  acceptance policy above are Loom design decisions, not a security guarantee.
