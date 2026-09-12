# Loom operator console

The React/TypeScript console is a Goal-centered view of durable control-plane
state. It shows Goals, waits, Session/Worker affinity, attempts, audit history and
active-versus-suspended timing. It is not Hatchet's dashboard and does not make
GitHub a required core concept.

## Local development

Build the client and Go server from the monorepo root:

```sh
make console-setup
set -a; . ./.env; set +a
make migrate
make console-serve
```

Open `http://127.0.0.1:8080` and sign in with the bootstrap administrator.
`LOOM_ADMIN_EMAIL` defaults to `admin@example.com`; set
`LOOM_ADMIN_PASSWORD` to choose a separate password. For this loopback setup,
if it is omitted, a fresh install uses `loom-admin-1234`. Set a private value
before exposing the console publicly. Loom stores only an Argon2id password
hash and uses a revocable HttpOnly browser session.
Set `LOOM_AUTH_EMAIL_REGISTRATION=false` to require social-provider sign-in.
GitHub registration appears when `LOOM_AUTH_GITHUB_CLIENT_ID` and
`LOOM_AUTH_GITHUB_CLIENT_SECRET` are configured.
For multiple server replicas, set the same random 32+ character
`LOOM_AUTH_STATE_KEY` on each instance so an OAuth callback can complete on a
different replica.

After sign-in, the guided home points operators to the next useful setup action.
The plugin store exposes GitHub with persisted `not configured`, `ready to
connect`, `connected`, `needs attention`, and `disabled` states. The recommended
flow uses GitHub's App Manifest handshake so GitHub returns the generated
private key and webhook secret directly to the Go server. An advanced form can
enroll an existing App. Secret fields are write-only: the server stores them in
OpenBao KV v2 and PostgreSQL retains only an opaque reference. Installation
callbacks verify the App, the authorizing GitHub user, repository selection and
permissions before the connection becomes active.

The guided flow opens GitHub in a separate window. A short-lived HttpOnly,
SameSite setup cookie binds the callback, while the browser account remains a
revocable server-side session. Signing out revokes that session.

The sidebar displays the image's source build SHA in published deployments, so
an operator can confirm that a rollout is serving the expected console bundle.

The console also includes Goal search/state filtering, Needs You, Goal details,
wait correlation, Session/Worker/provider binding, audit history, attempt timing,
accessible navigation, responsive layouts and explicit unknown usage values. It
never fabricates savings and refresh does not run a model.

The current Go server supports the console, authenticated reads, Goal/event
mutations and the outbound Worker protocol. Hatchet dispatch is native Go.
Provider plugin ingress and the live GitHub-to-Codex acceptance case remain, so
the UI is not an end-to-end production console yet.

## Validation and packaging

```sh
make console-check
docker build -f services/loom/Dockerfile -t piglor-loom:local .
```

The multi-stage image compiles the browser assets and a static Go binary, then
runs as a distroless non-root user. CI exercises Chromium, Firefox, WebKit and a
mobile viewport. These browser tests are not native iOS/Android certification.
