# Browser, mobile, and backend stack assessment

Date: 2026-09-09. Status: recommendation, not an accepted migration decision.

## Requirements

Loom is a monorepo. Its browser console must leave a practical path to native
iOS and Android applications. GitHub remains an integration plugin. The control
plane must operate independently of whether any user interface is connected.

## Recommendation

- Browser: React and TypeScript, with accessible HTML and responsive layouts.
- Mobile: React Native with Expo and TypeScript; platform-specific screens where
  appropriate. Share API clients, validation, presentation logic, and design
  tokens rather than promising complete screen reuse.
- Backend target: Go modular monolith plus a separately runnable Hatchet worker
  from the same codebase, subject to a pinned-engine compatibility proof.
- Machine agent and CLI: retain Rust.
- Persistence/orchestration: retain PostgreSQL and Hatchet.
- Contracts: versioned OpenAPI/JSON schemas, generated clients and cross-language
  conformance tests. Authorization and state transitions remain server-owned.

The earlier Rust server-rendered console suggestion was reasonable for a
browser-only interface, but is not the preferred direction given native mobile
requirements. Rust-generated HTML is not a shared native mobile UI.

## Evidence and alternatives

React Native supports TypeScript and platform-backed Android/iOS components.
Expo documents monorepo support. This supports sharing client logic, not an
assumption that browser DOM components work unchanged on phones.
[TypeScript](https://reactnative.dev/docs/typescript),
[native components](https://reactnative.dev/docs/intro-react-native-components),
[Expo monorepos](https://docs.expo.dev/guides/monorepos/).

Tauri is a viable alternative for packaging a web UI with Rust and native
integrations, including mobile. It uses system WebViews, so native packaging
must not be confused with native UI controls. Prefer it if web-view reuse is
the priority; prefer React Native for this proposed native mobile experience.
[Tauri architecture overview](https://v2.tauri.app/start/).

The current Python implementation was selected for an official Hatchet SDK and
initial proof velocity, not because Go or Rust cannot serve Loom. Hatchet's
official Go SDK documents workers, labels, durable tasks, event waits, timers,
and persisted memoization. Go is therefore a credible direct integration path.
Documentation does not establish compatibility with Loom's deployed engine:
verify the exact SDK/engine pair before migration.
[Go client](https://docs.hatchet.run/reference/go/client),
[durable context](https://docs.hatchet.run/reference/go/context).

Rust is also viable for the HTTP API. The Rust Hatchet SDK found in this review
explicitly describes itself as unofficial. A Rust API with an official-SDK
orchestration bridge is another option, but introduces a service/language
boundary; a custom Rust Hatchet SDK adds ongoing protocol maintenance. Neither
is necessary solely to achieve zero-model waiting.
[Rust SDK's own documentation](https://docs.rs/hatchet-sdk/latest/hatchet_sdk/).

## Monorepo direction

Suggested eventual locations, not directories to create speculatively:

```text
apps/web/                 browser console
apps/mobile/              iOS and Android application
services/loom/            Go API and orchestration worker entry points
crates/                   Rust agent, CLI, protocol types
packages/client/          generated TypeScript client and shared client logic
packages/design-tokens/   shared visual primitives, not forced screen reuse
contracts/                versioned language-neutral API/event schemas
integrations/             external plugin contracts and conformance fixtures
migrations/               Loom-owned database migrations
deploy/                   container and deployment configuration
```

Mobile is a control surface: inspect Goals, receive relevant attention alerts,
and submit authorized approvals/input. It is not assumed to be an always-on
agent worker. Reconnect must fetch authoritative state; a push notification or
open socket is never the durable record. Credentials for Hatchet and workers
must not ship to browser or mobile clients.

## Migration and scalability gates

Do not rewrite production immediately. First retain the existing API contract
and recovery suite, prove Go event-before-wait/restart/duplicate/reconnect
behavior without an LLM, then plan versioned workflow migration. In-flight
Python workflows must drain or be explicitly migrated; changing language does
not automatically preserve orchestration history.

Scale through bounded database pools, indexed correlation, transactional
admission/outbox processing, backpressure, pagination and recoverable event
streams. More API replicas must not create additional attempts. Language choice
does not establish either throughput or correctness without load/recovery tests.

These recommendations do not create a console, migrate the server, or establish
production readiness. A migration decision and implementation proof remain.
