#!/bin/sh
set -eu

config=$(LOOM_POSTGRES_PASSWORD=deployment-test-password \
  LOOM_API_TOKEN=deployment-test-api-token-0000000000000000 \
  LOOM_PUBLIC_URL=https://loom.example \
  LOOM_OPENBAO_ADDR= \
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

grep -Fq 'select(.key == "LOOM_OPENBAO_ADDR") | (.value // .real_value // "")' .github/workflows/ci.yml
echo "PASS: Coolify fills an empty bundled OpenBao address"

printf '%s' "$config" | jq -e '
  (.services.openbao.image | startswith("piglor-loom-openbao:local")) and
  (.services.openbao.build.dockerfile == "deploy/openbao/Dockerfile") and
  (.services["openbao-bootstrap"].build.dockerfile == "deploy/openbao/Dockerfile") and
  (.services["openbao-unseal"].build.dockerfile == "deploy/openbao/Dockerfile") and
  (.services.openbao.networks | has("openbao")) and
  (.services.openbao.expose | index("8200") != null) and
  (.services.openbao.ports == null) and
  (.services.openbao.labels["traefik.enable"] == "false") and
  (.services.openbao.healthcheck.test[0] == "CMD-SHELL") and
  (.services.openbao.healthcheck.test[1] | contains("503")) and
  .volumes["openbao-data"] != null and
  .volumes["openbao-audit"] != null and
  .volumes["openbao-bootstrap"] != null and
  .volumes["loom-openbao-credentials"] != null and
  .services["openbao-bootstrap"].entrypoint[0] == "/openbao/bootstrap.sh" and
  .services["openbao-bootstrap"].depends_on.openbao.condition == "service_healthy" and
  .services["openbao-unseal"].entrypoint[0] == "/openbao/unseal-monitor.sh" and
  .services["openbao-unseal"].depends_on["openbao-bootstrap"].condition == "service_completed_successfully" and
  (.services["loom-server"].networks | has("openbao")) and
  .services["loom-server"].user == "65532:1000" and
  (.services["loom-server"].depends_on.openbao == null) and
  (.services["loom-server"].depends_on["openbao-bootstrap"].condition == "service_completed_successfully") and
  (.services["loom-server"].volumes[] | select(.target == "/run/secrets/loom-openbao") | .read_only == true) and
  .services["loom-server"].environment.LOOM_OPENBAO_ROLE_ID_FILE == "/run/secrets/loom-openbao/role_id" and
  .services["loom-server"].environment.LOOM_OPENBAO_SECRET_ID_FILE == "/run/secrets/loom-openbao/secret_id" and
  (.services["loom-server"].environment.LOOM_OPENBAO_ADDR == "http://openbao:8200")
' >/dev/null

echo "PASS: Coolify Compose bundles private persistent OpenBao with the Loom server"

external=$(LOOM_POSTGRES_PASSWORD=deployment-test-password \
  LOOM_API_TOKEN=deployment-test-api-token-0000000000000000 \
  LOOM_PUBLIC_URL=https://loom.example \
  LOOM_OPENBAO_ADDR=https://openbao.example \
  HATCHET_CLIENT_TOKEN=deployment-test-hatchet-token \
  HATCHET_NETWORK=deployment-test-hatchet-network \
  docker compose -f deploy/coolify/compose.yaml config --format json)

printf '%s' "$external" | jq -e '
  .services["loom-server"].environment.LOOM_OPENBAO_ADDR == "https://openbao.example" and
  .services.openbao != null
' >/dev/null

echo "PASS: external OpenBao URL remains an explicit advanced override"

grep -Fq 'path "loom/data/organizations/*"' deploy/openbao/config/loom-policy.hcl
grep -Fq 'capabilities = ["create", "read", "update", "delete"]' deploy/openbao/config/loom-policy.hcl
! grep -Fq 'loom/metadata/' deploy/openbao/config/loom-policy.hcl
grep -Fq 'path "loom/data/restore-check"' deploy/openbao/config/loom-restore-verify-policy.hcl
grep -Fq 'capabilities = ["read"]' deploy/openbao/config/loom-restore-verify-policy.hcl
grep -Fq 'audit "file" "loom"' deploy/openbao/config/openbao.hcl
grep -Fq 'file_path = "/openbao/logs/audit.log"' deploy/openbao/config/openbao.hcl

echo "PASS: OpenBao policies and declarative audit device are constrained"

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
