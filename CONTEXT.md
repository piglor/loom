# Loom domain language

Loom is an integration-neutral control plane for durable, finite agent work.
These terms are product contracts and must not be renamed after the current
orchestration engine or first integration.

- **Goal** — the durable outcome requested by an operator or workflow trigger.
- **Workflow Definition** — an organization-owned editable description of how
  work moves. A published definition creates an immutable Workflow Version.
- **Workflow Run** — one execution of a pinned Workflow Version under a Goal.
  A run may invoke a separately pinned child run; parent and child identity and
  step progress remain visible in the read model.
- **Workflow Step** — a typed unit connected by explicit success/failure
  relationships. Schema version 1 publishes `agent`, `wait_event`, `condition`,
  `subflow`, and `complete`. Conditions evaluate a pinned run context; a
  subflow invokes a published child version and resumes its parent on success.
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
  port. Hatchet is the current adapter and validates/records compiled
  relationships with opaque Loom identities; Loom remains the relationship
  authority and it is not the public workflow schema.
- **OpenBao** — the current secret-store adapter. It stores integration
  credential values but grants no workflow or execution authority.

Preferred lifecycle wording:

```text
verified event -> workflow start/signal -> workflow decision -> agent step ready
```

Avoid “the plugin wakes the agent.” Legacy database and protocol names may keep
`wake` only while existing runs drain.
