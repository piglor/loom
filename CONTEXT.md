# Loom domain language

Loom is an integration-neutral control plane for durable, finite agent work.
These terms are product contracts and must not be renamed after the current
orchestration engine or first integration.

- **Goal** — the durable outcome requested by an operator or workflow trigger.
- **Workflow Definition** — an organization-owned editable description of how
  work moves. A published definition creates an immutable Workflow Version.
- **Workflow Run** — one execution of a pinned Workflow Version under a Goal.
  Schema version 1 has one active root run; child runs are reserved for a later
  compiler version and are not silently emulated.
- **Workflow Step** — a typed unit connected by explicit success/failure
  relationships. Schema version 1 publishes `agent`, `wait_event`, and
  `complete`; `condition` and `subflow` remain reserved vocabulary.
- **Plugin** — an integration adapter that authenticates an external system,
  stores its credentials through the secret-store port, and delivers verified
  normalized events. A plugin never starts or resumes an agent.
- **Trigger Binding** — explicit authorization for a normalized plugin event to
  start a published Workflow Version.
- **Wait** — a versioned condition owned by a Workflow Run. A matching event
  satisfies the wait; the workflow decides what follows.
- **Agent Step** — the only workflow step allowed to request an agent attempt.
- **Session** — the runtime conversation/context identity reused by agent steps
  within one Workflow Run.
- **Orchestration Adapter** — a replaceable implementation of Loom's scheduling
  port. Hatchet is the current adapter and receives compiled relationships and
  opaque Loom identities; it is not the public workflow schema.
- **OpenBao** — the current secret-store adapter. It stores integration
  credential values but grants no workflow or execution authority.

Preferred lifecycle wording:

```text
verified event -> workflow start/signal -> workflow decision -> agent step ready
```

Avoid “the plugin wakes the agent.” Legacy database and protocol names may keep
`wake` only while existing runs drain.
