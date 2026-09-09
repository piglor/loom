# Console and Go entry-point validation

Date: 2026-09-09. Scope: incremental read-only operator console, not the complete
production agent product.

## Local evidence

- 59 Python/PostgreSQL tests passed, including seven real Go read/API contract
  cases. Full nested lifecycle data is compared for waiting, completion,
  cancellation, runtime failure and uncertain execution. Timestamp representations
  are normalized by instant, not silently ignored.
- Go tests passed with the race detector; `go vet` passed. Coverage includes
  authentication, organization-scoped reads (in the PostgreSQL suite), readiness,
  invalid IDs, unavailable upstreams, signed payload forwarding and dropped
  mutation connections without a retry.
- 32 built-asset browser checks passed across Chromium, Firefox, WebKit and an
  iPhone-sized browser viewport. These cover login/logout, in-memory-only tokens,
  deep links, navigation/filter state, error handling, injection-as-text, layout,
  and automated WCAG A/AA scans of three screens. Screenshots were inspected.
  Automated scans are not a complete accessibility certification or native app
  validation. Browser API fixtures complement, rather than replace, real database
  and gateway tests.
- The Go container passed non-root/read-only execution, built-asset/CSP serving,
  authentication, real Python mutation forwarding, wait preservation through
  container restart, duplicate-event handling and exact logical-session continuity.
- Real Hatchet/API/worker restart and Rust worker reconnect proofs passed through
  the Go gateway. PostgreSQL dump/restore preserved a stopped wait and Session.
- Rust formatting, Clippy, eight tests and the coverage command passed. Existing
  Rust line coverage measured 68.54%; this is not a high-coverage claim. No new
  real Codex/model turn was used for these console tests.
- Dependency scans: npm audit reported no vulnerabilities; Go vulnerability
  analysis reported none after upgrading to Go 1.26.8, pgx 5.11.0 and x/text 0.42.0.
  Base container images and dependency manifests are pinned.

## Defects found and corrected

Review/tests caught route-registration conflict, readiness missing the Go database,
possible HTTP transport mutation retries, stale filter state across navigation,
misleading loading counters, an invalid default asset directory and a direct-script
import regression. Expanded tests also exposed shared proof ports/receipt locks:
recovery now uses dedicated ports, a private temporary receipt directory and a
unique Hatchet namespace.

Initial cross-engine accessibility runs exceeded the ordinary interaction timeout
under concurrent build load. Browser workers are now bounded to one; the three-scan
case has its own 90-second budget. The complete matrix subsequently passed, with
retries disabled. Browser startup runs a prebuilt Go binary rather than timing
toolchain download/compilation as server startup.

Fresh GitHub runners exposed intermittent initial dispatch stalls: the Goal was
READY with an assigned Hatchet workflow but no execution attempt. Three runs
failed; a diagnostics-only subsequent run passed all checks and image publication.
Local repetition, a fresh engine and a single-CPU test did not reproduce the stall.
Investigation also found that the outbox relay started before workflow registration.
A regression test failed against that ordering; the relay now starts through the
SDK lifespan after registration, and tests cover registration failure and thread
teardown. This corrects a startup ordering defect, but does not by itself establish
the cause of the CI stalls. Safe diagnostics now retain domain/engine progress
without printing payloads or credentials, including when the database is unavailable.

## Reproduce and CI

See [console guide](console.md). CI's `console` job builds assets and runs the
browser matrix. `durable-core` runs Go race/vulnerability checks, PostgreSQL tests,
existing proofs, `prove-go`, `prove-go-remote`, and the Go container proof. Both
jobs gate publication of the Python and Go-console images.

## Remaining release boundaries

The Go orchestration migration, OIDC/per-user authorization, native mobile apps,
repeated real-agent yields and the full unattended GitHub reference workflow
remain unfinished. The current console does not create or control Goals.

Production was not overwritten to install this console. The available Coolify
token can list the existing service but omits its current Compose configuration
under the installed API's sensitive-read filtering. A reviewed current descriptor
or the appropriate read permission is needed before safely merging the console
service and changing the public route. This does not block local development,
testing or publishing the source/images.
