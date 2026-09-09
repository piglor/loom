# External-event plugins

GitHub, GitLab, deployment systems and human approval services are optional
adapters. Loom's core Goal, Run, Wait, Session, Worker and Event records contain
no provider-specific repository or pipeline columns.

An adapter must:

1. authenticate the exact integration instance using its documented mechanism;
2. bound and retain the original delivery bytes;
3. normalize only deterministic `source`, `type`, `resource` and `version` keys;
4. verify organization ownership, actor/source trust and freshness before wake;
5. register an immutable generation-specific binding;
6. call the generic integration inbox, which handles early, duplicate, stale and
   out-of-order receipts transactionally;
7. keep free-form external text separate from trusted runtime instructions.

Adding a provider must not add provider fields to core Goal tables or create a
provider-specific core state. Plugin-owned tables are reserved for data that is
genuinely impossible to express through generic binding/evidence records.

The generic binding and receipt persistence is implemented and tested with
approval, deployment, dataset, GitLab and GitHub source names. The native Go
GitHub adapter implements signed `workflow_run` ingress, exact binding,
deduplication, internal-PR/fork checks, and live PR/run freshness validation.
Repository webhooks may wake finite demo runtimes; privileged Codex bindings are
disabled until a GitHub App installation authorization/revocation gate lands.
GitLab HTTP ingress remains a future plugin and is not advertised as active.

For public repositories, a signature alone never authorizes privileged local
execution. The adapter must validate installation, repository, pull request,
source repository, expected immutable version, Goal ownership and current app
authorization. Branch names are not correlation identities.
