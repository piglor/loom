#!/bin/sh

# Bootstrap the bundled, single-node OpenBao instance without putting root or
# unseal material in Compose variables, CI output, or the application image.
# The state directory is a protected Docker volume. This convenience path is
# intentionally opt-out; advanced operators should set
# LOOM_OPENBAO_AUTO_BOOTSTRAP=false and manage their own seal/KMS lifecycle.

set -eu
umask 077

if [ "${LOOM_OPENBAO_AUTO_BOOTSTRAP:-true}" != "true" ]; then
  exit 0
fi

BAO_ADDR="${BAO_ADDR:-http://openbao:8200}"
export BAO_ADDR
STATE_DIR="${LOOM_OPENBAO_BOOTSTRAP_DIR:-/openbao/bootstrap}"
CREDENTIALS_DIR="${LOOM_OPENBAO_CREDENTIALS_DIR:-/openbao/credentials}"
MOUNT="${LOOM_OPENBAO_MOUNT:-loom}"
ROLE="${LOOM_OPENBAO_ROLE:-loom}"

mkdir -p "$STATE_DIR" "$CREDENTIALS_DIR"
chmod 700 "$STATE_DIR" "$CREDENTIALS_DIR"
# Loom's server keeps its own non-root UID but joins the OpenBao group when it
# reads this shared volume. Keep the directory private to that group; the
# credential files below are replaced atomically and are group-readable only.
chmod 750 "$CREDENTIALS_DIR"

status_json() {
  bao status -format=json 2>/dev/null || true
}

wait_for_server() {
  attempt=0
  while [ "$attempt" -lt 60 ]; do
    if [ -n "$(status_json)" ]; then
      return 0
    fi
    attempt=$((attempt + 1))
    sleep 2
  done
  echo "OpenBao did not become reachable for automatic setup" >&2
  exit 1
}

is_initialized() {
  status_json | grep -Eq '"initialized"[[:space:]]*:[[:space:]]*true'
}

is_sealed() {
  status_json | grep -Eq '"sealed"[[:space:]]*:[[:space:]]*true'
}

wait_for_server

init_file="$STATE_DIR/init.json"
unseal_file="$STATE_DIR/unseal_key"
root_file="$STATE_DIR/root_token"

if ! is_initialized; then
  if [ ! -s "$init_file" ]; then
    tmp_file="$STATE_DIR/init.json.tmp"
    bao operator init -key-shares=1 -key-threshold=1 -format=json >"$tmp_file" 2>/dev/null
    chmod 600 "$tmp_file"
    mv "$tmp_file" "$init_file"
  fi

  # operator init emits a pretty-printed JSON array. The first string after
  # unseal_keys_b64 is the sole key because this convenience mode uses 1/1.
  unseal_key=$(awk '
    /"unseal_keys_b64"[[:space:]]*:/ { found=1; next }
    found && match($0, /"[^"]+"/) {
      value=substr($0, RSTART + 1, RLENGTH - 2)
      print value
      exit
    }
  ' "$init_file")
  root_token=$(sed -n 's/.*"root_token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$init_file" | head -n 1)
  if [ -z "$unseal_key" ] || [ -z "$root_token" ]; then
    echo "OpenBao initialization did not return the expected credentials" >&2
    exit 1
  fi
  printf '%s\n' "$unseal_key" >"$unseal_file"
  printf '%s\n' "$root_token" >"$root_file"
  chmod 600 "$unseal_file" "$root_file" "$init_file"
fi

# The init response is retained only until the setup below succeeds. A key is
# kept in the protected state volume so the companion unseal monitor can bring
# the service back after a normal restart.
if [ -s "$unseal_file" ] && is_sealed; then
  unseal_key=$(head -n 1 "$unseal_file")
  attempt=0
  while is_sealed && [ "$attempt" -lt 30 ]; do
    bao operator unseal "$unseal_key" >/dev/null 2>&1 || true
    attempt=$((attempt + 1))
    sleep 2
  done
fi

if is_sealed; then
  if [ -s "$unseal_file" ]; then
    echo "OpenBao is initialized but still sealed; automatic setup will retry" >&2
    exit 1
  fi
  # An existing manually managed volume has no convenience-mode key. Do not
  # block Loom startup; the console will report the sealed state instead.
  echo "OpenBao is already initialized and needs operator unseal/configuration" >&2
  exit 0
fi

# A root token exists only for the first successful configuration pass. Every
# command is quiet so neither the token nor generated SecretID can enter logs.
if [ -s "$root_file" ]; then
  root_token=$(head -n 1 "$root_file")
  export BAO_TOKEN="$root_token"

  mounts=$(bao secrets list -format=json 2>/dev/null)
  mount_key="\"$MOUNT/\""
  if ! printf '%s' "$mounts" | grep -Fq "$mount_key"; then
    bao secrets enable -path="$MOUNT" kv-v2 >/dev/null 2>&1
  fi
  bao write "$MOUNT/config" max_versions=10 >/dev/null 2>&1
  bao policy write loom /openbao/config/loom-policy.hcl >/dev/null 2>&1
  bao policy write loom-restore-verify /openbao/config/loom-restore-verify-policy.hcl >/dev/null 2>&1
  auth_methods=$(bao auth list -format=json 2>/dev/null)
  if ! printf '%s' "$auth_methods" | grep -Eq '"approle/"[[:space:]]*:'; then
    bao auth enable approle >/dev/null 2>&1
  fi
  bao write "auth/approle/role/$ROLE" \
    token_policies=loom token_ttl=1h token_max_ttl=4h \
    secret_id_ttl=720h secret_id_num_uses=0 >/dev/null 2>&1

  role_id=$(bao read -field=role_id "auth/approle/role/$ROLE/role-id" 2>/dev/null || true)
  secret_id=$(bao write -field=secret_id -f "auth/approle/role/$ROLE/secret-id" 2>/dev/null || true)
  if [ -z "$role_id" ] || [ -z "$secret_id" ]; then
    echo "OpenBao AppRole setup did not return credentials" >&2
    exit 1
  fi

  role_tmp="$CREDENTIALS_DIR/role_id.tmp"
  secret_tmp="$CREDENTIALS_DIR/secret_id.tmp"
  printf '%s\n' "$role_id" >"$role_tmp"
  printf '%s\n' "$secret_id" >"$secret_tmp"
  chmod 440 "$role_tmp" "$secret_tmp"
  mv "$role_tmp" "$CREDENTIALS_DIR/role_id"
  mv "$secret_tmp" "$CREDENTIALS_DIR/secret_id"

  bao token revoke -self >/dev/null 2>&1
  unset BAO_TOKEN

  # Do not retain the root token or the complete init response after setup.
  rm -f "$root_file" "$init_file"
fi

exit 0
