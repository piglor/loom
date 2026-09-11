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

printf '%s' "$config" | jq -e '
  (.services.openbao == null) and
  (.services["loom-server"].depends_on.openbao == null) and
  (.services["loom-server"].environment.LOOM_OPENBAO_ADDR == "https://openbao.example")
' >/dev/null

echo "PASS: Coolify application is decoupled from the dedicated OpenBao resource"

openbao=$(LOOM_SOURCE_REF=deployment-test-ref \
  OPENBAO_API_ADDR=https://openbao.example \
  OPENBAO_DOMAIN=openbao.example \
  docker compose -f deploy/coolify/openbao.compose.yaml config --format json)

printf '%s' "$openbao" | jq -e '
  (.services.openbao.image | startswith("piglor-openbao:local")) and
  (.services.openbao.build.dockerfile == "deploy/openbao/Dockerfile") and
  (.services.openbao.networks | has("openbao")) and
  (.services.openbao.expose | index("8200") != null) and
  (.services.openbao.ports == null) and
  (.services.openbao.labels["traefik.enable"] == "true") and
  (.services.openbao.labels["traefik.http.services.openbao.loadbalancer.server.port"] == "8200") and
  (.services["openbao-provenance"].image | startswith("busybox:1.36.1@sha256:")) and
  (.services["openbao-provenance"].labels["traefik.http.routers.openbao-provenance.rule"] == "Host(`openbao.example`) && PathPrefix(`/_loom/`)" ) and
  (.services["openbao-provenance"].labels["traefik.http.routers.openbao-provenance.entrypoints"] == "https") and
  (.services["openbao-provenance"].labels["traefik.http.routers.openbao-provenance.tls"] == "true") and
  (.services["openbao-provenance"].labels["traefik.http.services.openbao-provenance.loadbalancer.server.port"] == "8080") and
  (.services.openbao.environment.BAO_API_ADDR == "https://openbao.example") and
  (.services.openbao.healthcheck.test[0] == "CMD-SHELL") and
  (.services.openbao.healthcheck.test[1] | contains("503")) and
  .volumes["openbao-data"] != null and
  .volumes["openbao-audit"] != null
' >/dev/null

echo "PASS: Dedicated Coolify OpenBao resource uses HTTPS ingress, provenance proof and persistent storage"

grep -Fq 'path "loom/data/organizations/*"' deploy/openbao/config/loom-policy.hcl
grep -Fq 'capabilities = ["create", "read", "update", "delete"]' deploy/openbao/config/loom-policy.hcl
! grep -Fq 'loom/metadata/' deploy/openbao/config/loom-policy.hcl
grep -Fq 'path "loom/data/restore-check"' deploy/openbao/config/loom-restore-verify-policy.hcl
grep -Fq 'capabilities = ["read"]' deploy/openbao/config/loom-restore-verify-policy.hcl

echo "PASS: OpenBao AppRole policy permits soft delete but not metadata destruction"

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
  (.services.openbao.networks | has("openbao")) and
  .volumes["openbao-data"] != null and
  (.services["loom-server"].depends_on.openbao == null)
' >/dev/null

echo "PASS: Loom Lite uses private persistent OpenBao storage"
