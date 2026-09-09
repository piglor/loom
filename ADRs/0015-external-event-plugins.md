# ADR-0015: External-event plugins behind a small registration interface

**Status:** Proposed; implementation authorized by the user's integration-neutral direction
**Date:** 2026-09-09

GitHub and GitLab are external-event adapters, not workflow engines or runtime
authorities. A built-in plugin exposes HTTP routes and durable inbox reconciliation.
The application composition registry selects trusted, reviewed plugins; there is
no dynamic code download or arbitrary module-name loading from requests.

```text
GitHub / GitLab / other adapter
  verify → normalize → authorized binding → generic Event → Wait → Session
```

The Goal, runtime and worker protocols retain source/type/resource/version fields.
Plugins cannot select a worker or provider session from webhook content. GitLab
uses reusable binding/inbox storage; existing GitHub storage and URLs remain
compatible. A plugin can own specialized storage when needed. New plugins need
not alter the Goal schema. Reconciliation failures are isolated per plugin.

An operator-created binding must match the current wait and generation. One
source/instance/type/resource/version is bound to one Goal in an organization.
Bindings are immutable and historical; new versions use new generations. Inbox
evidence may precede binding and is reconciled durably. The new GitLab/generic
inbox stores only normalized correlation and bounded outcome evidence, not
pipeline variables or entire logs. The legacy GitHub inbox still retains raw
bodies and parsed payloads for compatibility; this is not a global retention guarantee.

## Evidence and alternatives

- GitHub specifies HMAC validation over raw body bytes:
  [GitHub webhook verification](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries).
- GitLab documents signing tokens, timestamped HMAC and stable delivery IDs:
  [GitLab webhooks](https://docs.gitlab.com/user/project/integrations/webhooks/).
- GitLab's pipeline payload identifies the pipeline, project, MR source/target
  project and SHA: [event schema](https://docs.gitlab.com/user/project/integrations/webhook_events/#pipeline-events).

Sources accessed 2026-09-09. These are upstream formats; Loom authorization and
the plugin interface are project decisions. A single GitHub-shaped adapter would
leak repository-specific assumptions into the core. A subprocess marketplace
would add distribution and sandbox contracts before they are needed. Trusted
in-process plugins are the smallest interface supporting two actual providers.

## Security and rollout

GitLab requires configured instance identity and signing token. Legacy secret
header support, multi-instance administration UI, live API freshness checks and
privileged runtime admission are not enabled by this initial adapter. Accept only
same-project MR pipelines for explicitly authorized project/MR/pipeline/SHA tuples.
Successful delivery is not proof of merge readiness. Keep finite runtimes until
the composed runtime/security acceptance gate passes. TLS remains required.

Migration 8 adds plugin-owned storage without replacing existing tables. Migration
9 preserves existing GitHub bindings as generation 1 and changes their key to
Goal plus generation. Old GitHub writers must not run once multigeneration
bindings are admitted: roll forward, or stop/reconcile those Goals before rollback.
Existing
URLs remain available. Disabling a plugin removes its routes/reconciler, not its
stored bindings or Goals. Re-enable to resume reconciliation. No live GitLab
deployment is implied by fixture tests.
