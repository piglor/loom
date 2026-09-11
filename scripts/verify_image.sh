#!/bin/sh
set -eu

image=${1:-piglor-loom:local}
suffix="$$"
network="loom-image-check-${suffix}"
database=""
server=""
backup_volume="loom-image-backup-${suffix}"

cleanup() {
  if [ -n "$server" ]; then docker stop "$server" >/dev/null 2>&1 || true; fi
  if [ -n "$database" ]; then docker stop "$database" >/dev/null 2>&1 || true; fi
  docker volume rm "$backup_volume" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker network create "$network" >/dev/null
docker volume create "$backup_volume" >/dev/null
database=$(docker run --detach --rm --network "$network" --network-alias loom-db \
  -e POSTGRES_PASSWORD=loom-test-only -e POSTGRES_USER=loom -e POSTGRES_DB=loom postgres:17.6)

attempt=0
until docker run --rm --network "$network" postgres:17.6 pg_isready -h loom-db -U loom -d loom >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  [ "$attempt" -lt 60 ] || { echo "PostgreSQL did not become ready" >&2; exit 1; }
  sleep 1
done

database_url='postgresql://loom:loom-test-only@loom-db:5432/loom'
api_token='image-test-administrator-token-0000000000000000'
github_secret='image-test-github-secret-000000000000000000'
docker run --rm --network "$network" -e LOOM_DATABASE_URL="$database_url" \
  -e LOOM_ORGANIZATION=image-test "$image" migrate

server=$(docker run --detach --rm --network "$network" --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true --tmpfs /tmp -p 127.0.0.1::8080 \
  -e LOOM_DATABASE_URL="$database_url" -e LOOM_ORGANIZATION=image-test \
  -e LOOM_API_TOKEN="$api_token" -e LOOM_GITHUB_WEBHOOK_SECRET="$github_secret" \
  "$image" serve)
port=$(docker port "$server" 8080/tcp | sed 's/.*://')
attempt=0
until curl -fsS "http://127.0.0.1:${port}/healthz" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  [ "$attempt" -lt 30 ] || { docker logs "$server"; exit 1; }
  sleep 1
done
health=$(curl -fsS "http://127.0.0.1:${port}/healthz")
if [ -n "${EXPECTED_BUILD_SHA:-}" ]; then
  printf '%s' "$health" | grep -Fq "\"build_sha\":\"${EXPECTED_BUILD_SHA}\"" || {
    echo "Unexpected image build revision: $health" >&2
    exit 1
  }
fi
if [ -n "${EXPECTED_SOURCE_SHA:-}" ]; then
  printf '%s' "$health" | grep -Fq "\"source_sha\":\"${EXPECTED_SOURCE_SHA}\"" || {
    echo "Unexpected image source fingerprint: $health" >&2
    exit 1
  }
fi

[ "$(docker inspect "$server" --format '{{.Config.User}}')" = '65532:65532' ]
[ "$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${port}/readyz")" = 401 ]
[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer ${api_token}" "http://127.0.0.1:${port}/readyz")" = 200 ]
goal=$(curl -fsS -H "Authorization: Bearer ${api_token}" -H 'Content-Type: application/json' \
  --data '{"title":"Image acceptance","objective":"Persist through the native API","condition":{"source":"human","type":"approved","resource":"image-check","version":"1"}}' \
  "http://127.0.0.1:${port}/v1/goals")
goal_id=$(printf '%s' "$goal" | jq -er .id)
curl -fsS -H "Authorization: Bearer ${api_token}" "http://127.0.0.1:${port}/v1/goals/${goal_id}" | jq -e '.state == "READY"' >/dev/null

ping='{}'
signature=$(printf '%s' "$ping" | openssl dgst -sha256 -hmac "$github_secret" -hex | sed 's/^.* /sha256=/')
[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "X-Hub-Signature-256: ${signature}" -H "X-GitHub-Delivery: 00000000-0000-4000-8000-000000000001" -H 'X-GitHub-Event: ping' --data-binary "$ping" "http://127.0.0.1:${port}/v1/github/webhook")" = 202 ]

docker run --rm --network "$network" -v "$backup_volume:/backups" -e PGHOST=loom-db \
  -e PGUSER=loom -e PGPASSWORD=loom-test-only -e PGDATABASE=loom postgres:17.6 \
  sh -ec 'pg_dump --format=custom --file=/backups/loom.dump; pg_restore --list /backups/loom.dump >/dev/null; createdb loom_restore; pg_restore --exit-on-error --no-owner --no-privileges -d loom_restore /backups/loom.dump; psql -d loom_restore -Atc "SELECT count(*) FROM goals" | grep -qx 1'

echo "PASS: non-root read-only image, native API, signed GitHub route, and PostgreSQL restore"
