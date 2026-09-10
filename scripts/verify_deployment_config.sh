#!/bin/sh
set -eu

config=$(LOOM_POSTGRES_PASSWORD=deployment-test-password \
  LOOM_API_TOKEN=deployment-test-api-token-0000000000000000 \
  HATCHET_CLIENT_TOKEN=deployment-test-hatchet-token \
  HATCHET_NETWORK=deployment-test-hatchet-network \
  LOOM_INGRESS_NETWORK=deployment-test-ingress \
  docker compose -f deploy/coolify/compose.yaml config --format json)

printf '%s' "$config" | jq -e '
  .services["loom-server"].labels["traefik.docker.network"] == "deployment-test-ingress"
' >/dev/null

echo "PASS: reverse proxy is pinned to the configured Coolify ingress network"
