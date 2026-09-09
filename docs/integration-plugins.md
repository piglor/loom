# External-event plugins

GitHub is optional. Loom's Goal/Run/Wait/Session/Worker model does not contain
GitHub repository or PR fields. Built-in adapters mount their own authenticated
routes and reconcile their own durable inboxes; the core accepts normalized
source/type/resource/version/generation evidence and chooses the existing Session.

Select plugins on both the server and orchestration worker:

```dotenv
LOOM_INTEGRATIONS=github,gitlab
```

Use `gitlab` alone to disable GitHub, `github` alone for GitHub, or an empty value
to disable both. Unknown or repeated names are configuration errors. No packages
are downloaded at runtime. Third-party code is not a security sandbox: additions
must be reviewed and registered in `server/loom/plugins.py` before deployment.
The small interface is `name`, `routes(authenticate)`, and `reconcile_pending()`.

## GitHub

Existing `/v1/github/bindings` and `/v1/github/webhook` URLs and signature
configuration remain compatible. Bindings now accept `generation` (default 1),
matching the current wait. A later SHA/run requires a new generation, not mutation
of an old binding. Historical deliveries cannot wake a later wait.

## GitLab pipeline preview

The adapter uses [GitLab's documented signed webhook format](https://docs.gitlab.com/user/project/integrations/webhooks/#signing-tokens)
and [pipeline payload](https://docs.gitlab.com/user/project/integrations/webhook_events/#pipeline-events).
Configure a signing token from GitLab, not the legacy plaintext secret header.
This requires a GitLab version with signing-token support. Keep TLS verification
enabled. Store configuration as private deployment secrets, never in Git:

```dotenv
LOOM_INTEGRATIONS=gitlab
LOOM_GITLAB_INSTANCE=gitlab.com
LOOM_GITLAB_SIGNING_TOKEN=<private signing token from GitLab>
```

For self-managed GitLab, use its hostname as the instance identifier. This initial
configuration supports one GitLab instance per Loom deployment. The configured
token establishes instance authority; untrusted instance headers cannot select
another credential or namespace. Do not reuse signing keys across instances.

Enable **Pipeline events** on a project webhook targeting
`https://<loom-host>/v1/gitlab/webhook`. The receiver validates the raw-body HMAC,
signed delivery identity and a five-minute timestamp window; keep clocks aligned.
Missing signing configuration returns 503, failed authentication returns 401,
oversized bodies return 413, and disabled plugin routes return 404.

The operator creates a Goal with a matching condition, then posts an authenticated
binding to `/v1/gitlab/bindings`:

```json
{
  "goal_id": "<existing Loom Goal UUID>",
  "generation": 1,
  "instance": "gitlab.com",
  "project_id": 11,
  "merge_request": 12,
  "pipeline_id": 81,
  "head_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
```

Its generic wait condition is:

```json
{
  "source": "gitlab",
  "type": "pipeline.completed",
  "resource": "gitlab.com/11/12/81",
  "version": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
```

Only explicitly bound same-project MR pipelines match. Wrong project/MR/pipeline,
SHA, forks, missing identities, and nonterminal states do not wake the Goal.
The completed event includes success/failure/cancellation/skipping as bounded
audit evidence: **pipeline completed does not mean pipeline passed or merge-ready**.
No free-form pipeline variables, logs or instructions enter the new inbox.
Legacy GitHub raw-payload retention is unchanged.

Verified early deliveries are stored before binding and reconciled after binding,
including after an interruption. A source/instance/type/resource/version tuple
has one authorized Goal owner per organization. Reusing a pipeline ID and SHA
for a new generation is rejected; this preview does not model GitLab job retries
within the same pipeline. Create a new pipeline or await a future retry-aware adapter.

## Evidence and limitations

Tests exercise GitHub alone, GitLab alone, and a single Session alternating between
both providers, plus signatures, stale generations, concurrent binding/delivery,
organization/instance separation, and disable/re-enable recovery. They run in the
mandatory backend CI job against PostgreSQL. These are realistic signed fixtures,
not a live GitLab-hosted pipeline demonstration.

Runtime admission remains limited to finite demos. Installing either adapter does
not authorize arbitrary public code execution on a developer machine. Codex
worker integration, live freshness checks, and the full real PR/CI/resume proof
remain separate acceptance gates. Public Goal creation still uses legacy demo
semantics until the repeatable worker contract is exposed and tested.
