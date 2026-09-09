# GitHub workflow ingress

Current scope: native Go signed `workflow_run` completion events for explicitly
enrolled Goals, with live GitHub API freshness checks before correlation.

Set a random `LOOM_GITHUB_WEBHOOK_SECRET` of at least 32 characters in the server
secret store and configure a webhook at `/v1/github/webhook`. Until configured,
that endpoint returns 503. Public repositories can be revalidated without an API
token. Private repositories require a short-lived, least-privilege installation
token in `LOOM_GITHUB_API_TOKEN`. Never send the Loom administrator token to
GitHub.

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
finite demo runtimes. Privileged Codex bindings remain disabled until GitHub App
installation authorization and revocation checks are implemented. Finite
bindings are checked against the current PR head and workflow run both when
bound and when delivered, so retained early evidence cannot wake after becoming
stale. Missed-delivery API reconciliation, required-check policy and the full
live Codex round trip remain release gates.

Sources: [GitHub signature validation](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries),
[webhook events](https://docs.github.com/en/webhooks/webhook-events-and-payloads),
[delivery best practices](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks).
