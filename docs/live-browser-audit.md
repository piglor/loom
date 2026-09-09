# Live browser audit — 2026-09-09

Target: https://loom.piglor.com, deployed application revision
`428596a369d3e9b30fc9a7850297aa3ec4a13c81`.

This was an actual browser pass against the deployed UI and authenticated API,
not just source inspection or mocked happy-path tests. No production configuration,
credentials, Goals or other data were changed during this audit. Network failure
and expired-authority responses were simulated only inside the testing browser.

## Results

72 scenario/profile checks: **64 passed, 8 failed**. The failures repeat two
findings across Chromium (1440px), Firefox (1366px), WebKit (1280px) and a narrow
Chromium viewport (320px). Viewport emulation is not physical iOS/Android testing.

Passed: invalid credentials, exact Goal deep links after login, expanded policy,
history/back/forward navigation, search and state filters, responsive layout,
failed-refresh stale-data suppression and recovery, rejection of expired authority,
unknown/malformed Goal recovery, sign-out/back isolation, credential clearing on
reload, no credential in local/session storage or cookies, and no uncaught page
JavaScript errors. Automated WCAG A/AA scans found no violations on login,
overview or detail in the tested profiles. This is not a complete accessibility
certification, load test or security audit.

### 1. Unknown URLs lack recovery navigation

Open `/not-a-loom-page` directly. The server responds with plain `404 page not
found`; there is no Loom navigation or link home. The client has a friendly
wildcard route, but the Go handler returns before loading the application for
this path (`services/loom/main.go`, route allowlist near line 158).

Impact: a mistyped/bookmarked route strands the visitor outside the console.
The HTTP 404 itself is correct; the finding is missing recovery UX. A fix should
preserve correct API/asset errors rather than returning application HTML for
every unknown resource.

### 2. Skip navigation does not focus its destination

Sign in using Tab, typing and Enter. Tab to “Skip to content”, then Enter.
The URL gains `#content`, but the focused element is `BODY`, not the main region.
An isolated Chromium reproduction confirms this; subsequent Tab does move into
content controls. Keyboard sign-in and sequential navigation are not completely
broken, but explicit destination focus is missing.

The target is `<main id="content">` without a focusable target contract in
`apps/web/src/main.tsx`, near line 488. Automated axe scans did not flag this
interaction. Validate focus behavior across browsers and assistive technology
when addressing it; do not equate a passing automated scan with full access.

## Product gaps visible while using the UI

- Read-only console: no browser action to create, cancel, approve or provide input
  to a Goal. The user must leave the UI for the CLI/API.
- No Worker, Session or Integration management screens; detail only displays a
  Goal's existing binding and explicitly does not establish worker availability.
- Refresh is manual, and the API/list is capped at the latest 100 Goals without
  pagination. Counts describe that returned subset, not an unlimited organization.
- Raw operator-token sign-in and logout on reload are documented preview behavior,
  not a finished user-account/session experience.
- Available live evidence remains finite-runtime execution, not an unattended
  Codex/GitHub workflow or a claim of model-cost savings.

## Evidence and scope

Owner-local audit harness, sanitized result JSON and screenshots are retained in
ignored `.loom/` files (`live-browser-audit.cjs`, `live-browser-audit.json`,
`audit-*.png`, `browser-repro.cjs`, `keyboard-repro.cjs`). Credentials are loaded
privately and are not recorded in the report or screenshots. Failure simulations
did not revoke a real token or interrupt a service.

The initial filter test used an overly strict label locator and failed before
interacting with the select. The corrected role-based locator passed in all four
profiles; that initial harness failure is not counted as a product defect.

This report records findings, not deployed fixes. Production was unchanged.

## Regression coverage added after the audit

The follow-up change gives unknown extensionless browser navigations the console
shell with HTTP 404, while preserving API, missing-asset and file-like resource
errors. The main region is now programmatically focusable for the skip link.

`npm run test:web` runs 18 scenarios across four profiles (72 checks) against
production-built assets served by the Go executable. Its GitHub `ci / console`
job runs on pushes and pull requests and gates image publication. API responses
are deterministic browser fixtures: this CI suite does not need or use production
credentials. Go API/database/recovery tests remain separate gates. Browser failure
traces use fixture credentials only.

The CI suite covers the audit's interactions, combining the three accessibility
scans into one scenario and retaining additional external-HTML rendering coverage.
It includes WCAG 2.1 AA tags, per-test uncaught-JavaScript monitoring, explicit
1440/1366/1280 desktop widths and a 320px Chromium mobile profile. It is not a claim
that the original production audit harness itself runs against CI fixtures.

The separate `production-smoke / public-browser-smoke` workflow is manually
triggered after deployment. It checks real public browser assets/sign-in,
health/auth boundaries and unknown-route recovery. It is read-only, uses no
operator token, stores no traces and performs no failure injection. Run it with
`gh workflow run production-smoke.yml --ref main`. Production smoke results and
the full authenticated live audit must be verified after deploying the fix;
local CI results alone do not establish that production has changed.
