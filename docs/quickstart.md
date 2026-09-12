# Development quickstart

Requirements: Go 1.26, Rust 1.97, Node 24, Docker Compose v2, Make, `rg`, and the
Hatchet CLI version pinned by CI.

Create an ignored `.env` from `.env.example` and provide fresh local values:

```sh
cp .env.example .env
chmod 600 .env
```

At minimum set:

```text
LOOM_POSTGRES_PASSWORD=<random local password>
LOOM_DATABASE_URL=postgresql://loom:<same password>@127.0.0.1:15432/loom
LOOM_TEST_DATABASE_URL=postgresql://loom:<same password>@127.0.0.1:15432/loom
LOOM_API_TOKEN=<random value of at least 32 characters>
LOOM_ADMIN_EMAIL=admin@example.com
LOOM_ADMIN_PASSWORD=<optional; defaults to loom-admin-1234>
# Used by remote workers/CLI; the browser callback origin is LOOM_PUBLIC_URL.
LOOM_URL=http://127.0.0.1:8080
# Optional: shared 32+ character OAuth signing key for multi-replica deployments
LOOM_AUTH_STATE_KEY=
# Optional Google sign-in (register the callback in Google Cloud first)
LOOM_AUTH_GOOGLE_CLIENT_ID=
LOOM_AUTH_GOOGLE_CLIENT_SECRET=
```

Then install locked dependencies, start PostgreSQL, migrate, and serve the Go
binary:

```sh
make setup
docker compose up -d --wait
set -a; . ./.env; set +a
make migrate
make serve
```

Open `http://127.0.0.1:8080` and sign in with the bootstrap administrator. The
default email is `admin@example.com`; when no separate admin password is
configured, use `loom-admin-1234` for the first sign-in. Set a private
`LOOM_ADMIN_PASSWORD` before exposing the console publicly. The console and
native read endpoints are present.

To enable “Continue with Google”, create a Google OAuth web client and add
`<LOOM_PUBLIC_URL>/v1/auth/google/callback` as an authorized redirect URI. Put
the client ID and secret in the two `LOOM_AUTH_GOOGLE_*` variables, restart
Loom, and the Google button will appear automatically on the sign-in screen.
Native Goal/event and outbound Worker endpoints are implemented. With a valid
Hatchet client token and reachable gRPC endpoint, start `make worker` in a second
terminal. Signed GitHub workflow ingress is available when its secret is set;
the live GitHub-to-Codex acceptance case remains a release gate.

Run the maintained gates with:

```sh
make check
make test
make prove
docker build -f services/loom/Dockerfile -t piglor-loom:local .
```

`make check` includes a guard that rejects any reintroduction of retired source
or packaging manifests. `make prove` exercises the generic wait, external
approval, restart and exact Session/Worker continuation against PostgreSQL.

Use only disposable credentials for local development. Never put Hatchet,
GitHub, Coolify, runtime or Loom tokens in source files or shell history. Rotate
any credential pasted into a conversation.
