#!/bin/sh
set -eu

config=$(LOOM_POSTGRES_PASSWORD=deployment-test-password \
  LOOM_API_TOKEN=deployment-test-api-token-0000000000000000 \
  LOOM_PUBLIC_URL=https://loom.example \
  LOOM_OPENBAO_ADDR=https://openbao.example \
  LOOM_OPENBAO_ROLE_ID=deployment-test-role \
  LOOM_OPENBAO_SECRET_ID=deployment-test-secret \
  HATCHET_CLIENT_TOKEN=deployment-test-hatchet-token \
  HATCHET_NETWORK=deployment-test-hatchet-network \
  LOOM_INGRESS_NETWORK=deployment-test-ingress \
  docker compose -f deploy/coolify/compose.yaml config --format json)

printf '%s' "$config" | jq -e '
  .services["loom-server"].labels["traefik.docker.network"] == "deployment-test-ingress"
' >/dev/null

echo "PASS: reverse proxy is pinned to the configured Coolify ingress network"

lite=$(LOOM_POSTGRES_PASSWORD=deployment-test-password \
  LOOM_API_TOKEN=deployment-test-api-token-0000000000000000 \
  LOOM_PUBLIC_URL=https://loom.example \
  LOOM_OPENBAO_ADDR=https://ignored-by-lite.example \
  LOOM_OPENBAO_ROLE_ID=deployment-test-role \
  LOOM_OPENBAO_SECRET_ID=deployment-test-secret \
  HATCHET_CLIENT_TOKEN=deployment-test-hatchet-token \
  HATCHET_NETWORK=deployment-test-hatchet-network \
  LOOM_INGRESS_NETWORK=deployment-test-ingress \
  docker compose -f deploy/coolify/compose.yaml -f deploy/lite/openbao.compose.yaml config --format json)

printf '%s' "$lite" | jq -e '
  .services["loom-server"].environment.LOOM_OPENBAO_ADDR == "http://openbao:8200" and
  (.services.openbao.networks | has("private")) and
  .volumes["loom-openbao-data"] != null
' >/dev/null

echo "PASS: Loom Lite uses private persistent OpenBao storage"
