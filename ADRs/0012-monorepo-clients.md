# ADR-0012: React clients in the Loom monorepo

**Status:** Accepted
**Date:** 2026-09-09
**Decision owner:** User; authorized implementation of the recommended stack.

## Decision

Use React, TypeScript and Vite for the browser console. Plan React Native mobile
clients with shared language-neutral API contracts and TypeScript client logic.
Keep Rust for machine-side execution. GitHub remains an external integration.

## Alternatives and evidence

Next.js adds server rendering that the initial authenticated console does not
require. Svelte/Capacitor is viable for a web-rendered mobile product; Dioxus is
viable for Rust-first clients. Native mobile controls and shared TypeScript
client logic drove the React choice, not an assertion that Rust cannot build UI.
See [platform assessment](../docs/research/platform-stack.md) and its primary
sources. [Vite](https://vite.dev/guide/) builds static browser assets;
[React Router](https://reactrouter.com/start/declarative/installation) supplies
navigation. Both were accessed 2026-09-09.

## Boundaries and rollout

```text
Browser / future mobile → Loom API → domain policy → durable orchestration
```

No database, Hatchet, worker or provider credentials are embedded in assets.
The first console is a single-organization operator tool using the existing
operator bearer credential, held only in browser memory and cleared on logout.
This is not end-user SSO. OIDC and per-user authorization remain release gates
before broad multi-user access. Do not add localStorage credential persistence.

The console must show actual API state, unavailable metrics as unavailable,
finite runtimes as demos, and visible refresh/error states. Browser refresh
activity is deterministic HTTP activity, never agent/model execution.

## Risks and validation

Test unauthorized access, rendering of untrusted content as text, deep links,
mobile-width layout, logout, errors and API contract drift. Shared logic does
not imply complete UI reuse or validated iOS/Android builds. No native app is
claimed until device-specific tests and packaging pass.
