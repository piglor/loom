# Security model

Status: proposed, not a completed security review. A developer worker can be highly privileged; access to its command stream is consequential authority.

## Trust boundaries

| Boundary | Required control |
| --- | --- |
| User → Loom | Authenticated principal, project ownership and explicit Goal policy |
| External system → ingress | HTTPS, signature verification over original bytes, size/schema limits, integration enrollment |
| Ingress → domain | Deterministic authorization/correlation; signature is not code-execution permission |
| Loom → Hatchet | Central service credential only; minimal payload references, private network or verified TLS |
| Loom → worker | Worker-scoped credential, command claim authorization, session binding, incarnation/fence checks |
| Worker → runtime | Local workspace/runtime allowlist, least privilege sandbox, environment filtering, process supervision |
| External content → model | Separate evidence fields, bounded data, no promotion into trusted instructions |

## Authentication and authorization

Self-hosted MVP can use randomly generated opaque bearer credentials over TLS with server-side hashed verifiers, explicit principal/scopes, expiry and revocation. Use standard library/maintained authentication primitives; no signed homegrown capability format. Enrollment credentials are separate from worker credentials. A worker token cannot create Goals, grant capabilities to itself, read another worker's payloads or mutate arbitrary Sessions.

Use short-lived execution claims with current database authorization. Polling authentication does not make a stale queued command executable. Check revocation at claim and heartbeat. Local execution policy can narrow server authority but never expand it. Credential loss pauses execution/reconciliation rather than falling back to anonymous access.

Keep Hatchet tokens centrally. Hatchet documents API-token exemptions from member payload restrictions; do not infer machine-level isolation from labels. [Hatchet roles](https://docs.hatchet.run/v1/user-roles)

Loom Goal metadata and Hatchet inputs contain references and bounded summaries by default. Raw provider credentials, private keys and local environment dumps must not enter prompts, logs, database audit payloads or Hatchet task history. Store integration secrets by secret-store reference. Rotate/revoke without rewriting immutable audit history.

## GitHub ingress and privileged execution

Verify HMAC-SHA256 with constant-time comparison before parsing, persist the inbox before acknowledging, then authenticate the installation/repository mapping. Delivery IDs deduplicate transport; semantic wait-generation checks also reject replay. [Signature verification](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries), [delivery guidance](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks)

Before a CI event can wake a privileged worker, require all of:

- Authorized Loom owner/project and still-active Goal/Run.
- Enrolled GitHub App installation and numeric base repository ID.
- Explicit PR binding and current head repository/SHA.
- Same-repository trusted source for MVP, plus explicit task authorization; fork/unknown source rejected.
- Expected workflow/check producer and current run/attempt or check identity.
- Current binding generation, exact Loom Session and bound Worker.

Branch names, actor names and a successful signature are insufficient. Empty/ambiguous PR associations fail closed and invoke software reconciliation. Workflow completion for a previous rerun of the same SHA is stale. [GitHub research and API sources](research/github.md)

Normal public-repository CI runs on GitHub-hosted runners. A webhook resumes already-authorized work; it does not authorize running arbitrary public PR code on a developer machine. GitHub warns about persistent compromise of self-hosted runners by untrusted workflows. [GitHub secure use](https://docs.github.com/en/actions/reference/security/secure-use)

The GitHub App observes with read-only permissions initially. Git publishing uses separately enrolled worker authority; merge/deployment rights must be explicit and are unnecessary for the merge-ready proof. Check current version again before any consequential publishing action. External systems and Loom do not share atomic transactions, so preflight reduces but cannot eliminate time-of-check/time-of-use races.

## Prompt injection and data handling

Repository files, CI logs, comments, issue text and API payloads remain untrusted even when delivered authentically. Pass them as structured evidence alongside trusted policy. Do not interpolate event text into shell commands, URLs, runtime configuration or instruction files. Construct GitHub API URLs from validated IDs against an allowlisted origin; bound log downloads and redact secrets. Prompt boundaries help the model but are not a security sandbox.

Worker runtime policy restricts workspace roots, network access and inherited environment. Repository-controlled configuration/hooks/plugins can execute code; the Codex proof must inspect effective configuration and demonstrate enforcement rather than assuming a clean profile. Never expose app-server on a public socket. Worker execution of trusted development code still has risk; scope credentials and filesystem access to that task.

## Failure and operational controls

Reject cross-organization links at database and API boundaries. Rate-limit webhook/command/report endpoints; enforce bounded payload and output sizes. Redact tokens and authorization headers from exceptions and proxy logs. TLS certificate verification remains enabled; local private traffic exceptions must be explicit deployment configuration.

An expired lease fences future reports but cannot revoke an in-flight external side effect. Use local watchdogs, exclusive Session locks and stopped-process evidence. If a machine is unreachable, report uncertainty rather than claiming zero inference. No reassigning its Session to recover availability.

Audit rejected wakes with reasons and nonsecret identifiers. Retain security evidence under an explicit policy, restrict raw payload access and test backup restore. Public release requires the adversarial cases in the implementation plan; this document alone is not evidence those controls work.
