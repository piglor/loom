#!/bin/sh

# Re-unseal the convenience-mode instance after a restart. The key is written
# once by bootstrap.sh into a protected Docker volume and is never logged.

set -eu
umask 077

if [ "${LOOM_OPENBAO_AUTO_BOOTSTRAP:-true}" != "true" ]; then
  exit 0
fi

BAO_ADDR="${BAO_ADDR:-http://openbao:8200}"
export BAO_ADDR
STATE_DIR="${LOOM_OPENBAO_BOOTSTRAP_DIR:-/openbao/bootstrap}"
unseal_file="$STATE_DIR/unseal_key"

status_json() {
  bao status -format=json 2>/dev/null || true
}

attempt=0
while [ ! -s "$unseal_file" ] && [ "$attempt" -lt 30 ]; do
  sleep 2
  attempt=$((attempt + 1))
done
[ -s "$unseal_file" ] || exit 0

while :; do
  status=$(status_json)
  if printf '%s' "$status" | grep -Eq '"initialized"[[:space:]]*:[[:space:]]*true' \
    && printf '%s' "$status" | grep -Eq '"sealed"[[:space:]]*:[[:space:]]*true'; then
    unseal_key=$(head -n 1 "$unseal_file")
    bao operator unseal "$unseal_key" >/dev/null 2>&1 || true
  fi
  sleep 10
done
