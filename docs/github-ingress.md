# GitHub workflow ingress

Current scope: signed `workflow_run` completion events for explicitly enrolled
finite-runtime Goals. It is not a production privileged-agent integration yet.

Set a random `LOOM_GITHUB_WEBHOOK_SECRET` of at least 32 characters in the server
secret store and configure a GitHub App webhook at `/v1/github/webhook`. Until
configured, that endpoint returns 503. The App needs Actions read and relevant
repository access. Never send the Loom administrator bearer token to GitHub.

An administrator creates a Goal whose condition is:

```json
{
  "source": "github",
  "type": "workflow.completed",
  "resource": "REPOSITORY_ID/PR_NUMBER/RUN_ID/RUN_ATTEMPT",
  "version": "40-character-lowercase-head-sha"
}
```

Then `POST /v1/github/bindings` with administrator authorization and:
`goal_id`, `installation_id`, `repository_id`, `pull_request`, `head_sha`,
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

Privileged runtime bindings are explicitly rejected. Before enabling Codex, add
GitHub App installation authentication, current PR/run revalidation, missed-event
reconciliation, revocation handling, and captured live payload tests. A correctly
signed historical event is not proof that its PR head is still current. Review
events, check-run providers, required-check policies and approval freshness are
also outstanding. See [research](research/github.md) and [release gates](production-readiness.md).

Sources: [GitHub signature validation](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries),
[webhook events](https://docs.github.com/en/webhooks/webhook-events-and-payloads),
[delivery best practices](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks).
