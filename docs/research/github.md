# GitHub integration research

Researched: 2026-09-08. Scope: GitHub.com official documentation; no live installation or webhook exercise was performed. Recommendations below are proposed Loom behavior, not guarantees provided by GitHub.

## Findings that affect the MVP

GitHub signs the request body using HMAC-SHA256 in `X-Hub-Signature-256`. Verify the original bytes before JSON parsing using constant-time comparison; reject missing signatures and prevent middleware/proxies from rewriting the body. Signature verification establishes delivery integrity, not authorization to run code on a worker. [Signature validation](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries)

`X-GitHub-Delivery` stays the same on redelivery. GitHub recommends HTTPS, explicit event/action handling, and a 2xx response within ten seconds. Loom should acknowledge only after its durable inbox transaction commits, then process asynchronously. [Webhook best practices](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks)

GitHub does **not** automatically redeliver failed deliveries. Its documentation describes scheduled API inspection and redelivery. A Loom recovery service must reconcile missed notifications without invoking an agent; webhook delivery alone cannot guarantee eventual progress after an outage. [Failed deliveries](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries)

App event subscriptions need Actions read for `workflow_run`, Checks read for `check_run`/`check_suite`, and Pull requests read for PR/review events. Completed workflows may succeed or fail. Check payloads can contain empty `pull_requests` and null `head_branch` for fork pushes. `sender` can be the placeholder `ghost` user. Consequently, neither actor login nor branch name is a safe routing authority. [Webhook payload reference](https://docs.github.com/en/webhooks/webhook-events-and-payloads)

Workflow run records expose `id`, `workflow_id`, `head_sha`, `head_repository`, `pull_requests`, `run_attempt`, status and conclusion. REST supports fetching a particular attempt. The same SHA alone cannot distinguish attempts or different workflows. [Workflow runs API](https://docs.github.com/en/rest/actions/workflow-runs)

PR API responses expose separate base/head repositories and head SHA. Reviews have their own identity, state and `commit_id`. Loom should retain the reviewed commit rather than assuming an approval covers a later push. [Pull requests API](https://docs.github.com/en/rest/pulls/pulls), [Reviews API](https://docs.github.com/en/rest/pulls/reviews)

GitHub Actions' `pull_request` execution context can use the merge commit as `GITHUB_SHA`; `pull_request_target` has different trust/execution semantics. A webhook recipient must not substitute an Actions environment variable for a verified PR head. GitHub also warns about privileged `workflow_run` processing of untrusted content. [Actions event semantics](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows)

## App authentication and permissions

Use a GitHub App, with its private key held centrally. An App JWT is exchanged at `POST /app/installations/{installation_id}/access_tokens`; repository and permission scopes can be narrowed, and installation tokens expire after one hour. Treat tokens as opaque: current documentation describes a 2026 token-format change, so fixed-length validation is inappropriate. [Installation token generation](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app)

Proposed MVP permission set: Metadata read, Actions read, Checks read, Pull requests read. Add Issues read only when importing issue objectives. Grant no write permission merely to observe CI. PR creation and Git publishing are separate execution authorities; explicitly document whether they use the worker's existing user credentials or a separately scoped App credential. Map installation and repository IDs to a Loom organization/project through an authenticated enrollment flow. An installation ID supplied in a request is not self-authorizing. Consult endpoint permissions before enabling each capability. [Choosing App permissions](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app)

## Proposed normalization

Preserve an immutable source envelope and produce one versioned normalized record. Server-owned fields include integration ID, verification result, receive time and payload digest. External timestamps are evidence, not a reliable total ordering.

| Source input | Minimum retained evidence | Loom interpretation |
| --- | --- | --- |
| `workflow_run`, completed | Run/workflow IDs, attempt, status/conclusion, head SHA/repository, PR candidates | `github.workflow.completed` |
| `check_run`, completed | Check ID, producing App ID, suite ID, head SHA, status/conclusion, PR candidates | `github.check.completed` |
| `check_suite`, completed | Suite ID, producing App ID, head SHA, conclusion, PR candidates | CI evidence update; do not duplicate a workflow wake |
| PR change | PR number, base/head repository IDs, head SHA, action | Refresh binding and invalidate superseded version |
| PR review | PR number, review ID, reviewer identity, reviewed commit and review state | Re-evaluate configured review policy |

Check run evidence and producing App information are available through the Checks API. Use provider identity plus check identity, not a check's display name alone. [Check runs API](https://docs.github.com/en/rest/checks/runs)

Keep raw external payload separate from trusted Loom instructions. Restrict payload size, retention and log visibility; retain bounded failure summaries and authoritative URLs rather than injecting entire logs into an agent turn. URLs obtained from payloads are data: build API requests against the configured GitHub API origin using validated identifiers to avoid arbitrary outbound fetches.

## Proposed correlation and wake algorithm

1. Authenticate the integration endpoint, enforce size/content limits, verify HMAC over raw bytes, then parse the event with an allowlisted schema. Ignore unsupported event/action combinations with an auditable reason.
2. Authorize signed installation and numeric repository identity against stored project enrollment. Persist the inbox record under a unique `(integration_id, delivery_id)` key. Store the body digest so a reused delivery ID with a different body becomes an integrity alert. A duplicate already committed receipt returns success without another wake; unfinished inbox processing remains retryable.
3. Maintain authorized bindings before the agent waits: `(project, installation, repository_id, PR, head_repository_id, expected_head_sha, generation)` maps to Goal/Run/Session/worker. Only trusted Goal owners and authenticated attempts may request binding changes. Verify the actual PR through GitHub before accepting a worker's proposed binding. Do not accept worker/session IDs from webhook content.
4. Find candidate waits using exact repository identity and expected version. Resolve a PR using an explicit existing resource binding plus authoritative PR data. Empty or multiple PR candidates are not an invitation to guess: persist an unresolved event and reconcile through the API. A SHA may appear in more than one PR. The first proof should reject ambiguous bindings and restrict privileged execution to same-repository PRs with explicit task authorization.
5. Re-read current PR head/source and the current CI resource before dispatch. Reject an event for a superseded SHA or older run attempt. For workflow runs, require the configured workflow ID and currently bound run ID/attempt; if multiple runs exist for the same SHA, use an explicit selection policy and persist its outcome. Receiving the largest attempt seen locally is insufficient when a newer attempt's webhook was missed. For checks, verify the expected producing App and current check/suite evidence. Retry API failures through orchestration while the model stays stopped.
6. Evaluate whether the event actually changes what is actionable. Record redundant workflow/check/suite evidence without multiple agent wakes. CI success can transition directly to a review wait without invoking a model. Completion requires the full configured criteria, not one successful workflow or an agent's assertion.
7. In one database transaction, lock the current wait, compare its generation, record its satisfaction and audit cause, and insert an outbox wake intent. Uniqueness on wait/generation prevents distinct deliveries representing the same condition from waking twice. The dispatcher retries the same durable command ID; worker execution receipts must also be idempotent.
8. Resolve Goal → Run → Loom Session → bound Worker → provider session from persisted records. If that worker is unavailable, preserve the wake intent and wait for that worker. Recheck cancellation, authorization and generation when the command is claimed. Pass the external evidence in a typed untrusted-data field alongside trusted continuation policy.

A delivery ID is transport deduplication, not semantic deduplication or freshness. Because the HMAC covers the body, do not assume unsigned delivery headers alone make replays impossible. Semantic uniqueness and current resource checks are required even after successful signature validation.

Events can arrive before yield registration. Keep the durable inbox and reconcile its current evidence when arming a wait; do not rely exclusively on future delivery. All reconciliation is ordinary control-plane software, with no model invocation. A race remains between checking GitHub state and executing locally; version fencing and a worker preflight reduce it, but GitHub and Loom do not share an atomic transaction. Limit execution to the authorized version and revalidate before publishing consequential changes.

## Public repository boundary

GitHub warns that untrusted workflows can persistently compromise self-hosted runners and discourages their use for public repositories. Developer machines often carry exactly the credentials and network access that make this dangerous. [Secure use reference](https://docs.github.com/en/actions/reference/security/secure-use)

Proposed Loom rule: normal public CI runs on GitHub-hosted infrastructure. A signed CI notification may resume a previously authorized local development Goal; it does not authorize checking out or executing arbitrary public contributions. For MVP privileged workers, reject fork PRs, missing source repositories, untrusted ownership, unexpected installations and changed source/version bindings. Same-repository status is necessary under this policy but is not sufficient task authorization. Future fork support requires an explicitly isolated execution environment and policy; human workflow approval alone does not make malicious code safe.

## Proof tests and remaining unknowns

Before claiming the real integration works, capture fixtures from a controlled GitHub App installation for same-repository PR, fork PR, rerun, cancelled workflow, concurrent workflows and empty PR association. Confirm which tested SHA each workflow/check actually represents; do not silently broaden matching from head to merge SHA.

Required tests:

- Original raw-byte signature vector passes; changed whitespace, Unicode bytes, missing signature and wrong secret fail.
- Duplicate/redelivered delivery, distinct delivery with equivalent CI evidence, and crash between inbox commit and dispatch produce one logical wake.
- Wrong installation/repository/PR/SHA, unauthorized project, fork, missing head repository, spoofed check name/App and ambiguous PR mapping produce zero privileged executions.
- Old SHA failure after a push, old attempt completion after rerun, later push during dispatch, and event-before-yield preserve the current generation.
- Offline worker, wrong worker/session and worker reconnect preserve exact session affinity; cancellation races cannot launch cancelled work.
- Server restart during a long wait preserves wait and evidence; failed-delivery recovery works; no runtime process or model calls occur throughout waiting.
- Successful CI with pending required review stays incomplete; review dismissal or a newer commit invalidates stale approval under the chosen policy.
- GitHub API timeout/rate-limit/revoked installation leaves the Goal suspended with an inspectable integration failure, not an agent polling loop.

Still to prove: payload association across actual workflow trigger configurations; check rerun identity behavior for the selected CI provider; recovery coverage beyond delivery-history retention; required-check/ruleset evaluation; installation suspension/revocation propagation. These are implementation gates, not reasons to delegate routing decisions to an LLM.
