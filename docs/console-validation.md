# Console validation

The current console gate builds the React/TypeScript client and runs 72 tests
with retries disabled across Chromium, Firefox, WebKit and a constrained mobile
viewport. Coverage includes authentication, credential clearing, deep links,
history navigation, filters, malformed routes, backend failures, injection as
text, accessible navigation and responsive overflow.

The browser is served by the same native Go binary used in the container. Go
tests cover read authentication, organization-scoped PostgreSQL views, readiness,
invalid identifiers, missing assets, and the native mutation/worker API. The
final image is non-root and distroless.

Automated accessibility scans and browser emulation do not certify native iOS or
Android clients. The console does not prove the unfinished live GitHub/Codex
workflow; see [validation](validation.md).
