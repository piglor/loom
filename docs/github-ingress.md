# GitHub workflow ingress

Current scope: native Go signed `workflow_run` completion events for explicitly
enrolled Goals, with live GitHub API freshness checks before correlation.

Use **Plugins → GitHub → Connect with GitHub** in the operator console. The App
Manifest flow configures `/v1/github/webhook`, creates a read-only GitHub App,
stores its credential bundle in OpenBao and verifies the installation against
the authorizing GitHub user. Webhooks resolve the secret by installation and
private-repository freshness checks mint a short-lived installation token from
the App credential. `LOOM_GITHUB_WEBHOOK_SECRET`,
`LOOM_GITHUB_APP_INSTALL_URL`, and `LOOM_GITHUB_API_TOKEN` remain one-release
read-only fallbacks for existing deployments. Never send the Loom administrator
token to GitHub.

An administrator creates a Goal whose condition is:

```json
{
  "source": "github",
  "type": "workflow.completed",
  "resource": "REPOSITORY_ID/PR_NUMBER/WORKFLOW_ID/RUN_ID/RUN_ATTEMPT",
  "version": "40-character-lowercase-head-sha"
}
```

Then `POST /v1/github/bindings` with administrator authorization and:
`goal_id`, `installation_id`, `repository_id`, `repository_full_name`, `pull_request`, `head_sha`,
`run_id`, `run_attempt`, `workflow_id`. The binding must match the Goal condition;
it cannot replace the Goal's worker/session or expand its scope. Previously
received unbound deliveries are retained and reconciled when the binding is
registered. A durable pending marker lets the central relay recover an interrupted
binding-to-reconciliation handoff. Recovery of deliveries
that never reached Loom still requires GitHub API reconciliation.

The ingress verifies HMAC-SHA256 over original bytes, limits body size to 1 MiB,
deduplicates delivery UUIDs with payload digests, retains signed payload bytes and
CI outcome evidence separately from trusted instructions, and atomically persists event
satisfaction plus its dispatch intent. It requires the exact same-repository PR,
SHA, installation, workflow/run/attempt and `pull_request` trigger. Missing or
ambiguous PR associations, forks and `pull_request_target` are rejected. Tests
include GitHub's official signature vector and tampered bytes.

Repository webhook bindings use `installation_id: 0` and are restricted to
finite demo runtimes. Privileged Codex bindings require an active, verified
GitHub App installation. Disabling an installation prevents new privileged
bindings and managed secret resolution. Bindings are checked against the current PR head and workflow run both when
bound and when delivered, so retained early evidence cannot wake after becoming
stale. Missed-delivery API reconciliation, required-check policy and the full
live Codex round trip remain release gates.

Sources: [GitHub signature validation](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries),
[webhook events](https://docs.github.com/en/webhooks/webhook-events-and-payloads),
[delivery best practices](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks).
