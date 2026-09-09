# Outbound Rust Worker

The Rust Loom Agent connects to Loom over outbound HTTP(S), claims commands bound
to its Worker identity, journals claims and stop receipts locally, and preserves
Loom Session/provider Session affinity. Developer machines require no inbound
port, SSH access or router configuration.

Build it with `make agent`. Enroll a protocol-2 Worker through the administrator
API, writing the returned credential directly to an owner-only configuration:

```sh
umask 077
curl --fail --silent --show-error \
  -H "Authorization: Bearer $LOOM_API_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"workspace_ref":"sandbox","protocol_version":2}' \
  "$LOOM_URL/v1/workers" |
jq --arg server "$LOOM_URL" \
   --arg state "/absolute/private/loom-worker-state" \
   '. + {server_url:$server,state_dir:$state,allow_insecure_localhost:true}' \
   > .loom/worker.json
target/debug/loom-agent --config .loom/worker.json
```

Use `allow_insecure_localhost` only for loopback development. Production requires
HTTPS. Never copy an enrollment between machines, expose its token, delete its
journal to bypass an ambiguous attempt, or silently move an affine Session to a
different Worker.

The control-plane API and Rust tests cover wrong/revoked Workers, command claims,
exact Session binding, reconnect journals, redirects, workspace policy and
finite outcomes. A claimed or uncertain execution cannot be cancelled until Loom
has stop evidence. Privileged Codex use additionally requires the contained
runtime policy and the live provider acceptance gate.
