.PHONY: setup init infra migrate serve worker test check no-python prove image-smoke agent console-setup console-check console-serve go-build

setup:
	cd services/loom && go mod download
	cargo fetch --locked
	npm ci
	npm run build

init:
	@mkdir -p .loom
	@test -f .env || { echo 'Create .env from .env.example with fresh secrets'; exit 1; }

infra:
	docker compose up -d --wait
	HATCHET_CLI_TELEMETRY_ENABLED=false hatchet server start --project-name loom-dev --profile loom-local --dashboard-port 18888 --grpc-port 17077 --tag v0.105.16 --pull-policy missing

migrate:
	cd services/loom && go run . migrate

serve:
	cd services/loom && LOOM_WEB_DIR=../../apps/web/dist go run . serve

worker:
	cd services/loom && go run . worker

test:
	cd services/loom && go test -race ./...
	cargo test --workspace --locked
	npm run test:web

no-python:
	@test -z "$$(rg --files -uu -g '*.py' -g '!target/**' -g '!node_modules/**' -g '!.git/**')"
	@test ! -e pyproject.toml
	@test ! -e requirements.lock

check: no-python
	cd services/loom && test -z "$$(gofmt -l .)" && go generate ./ent && git diff --exit-code -- ent && go vet ./...
	cargo fmt --all --check
	cargo clippy --workspace --all-targets --locked -- -D warnings
	npm run check:web
	npx prettier --check apps/web/src apps/web/tests apps/web/smoke apps/web/playwright.config.ts apps/web/playwright.smoke.config.ts packages/client/src

prove:
	cd services/loom && go test -race ./internal/control -run 'TestOutboundWorkerExactSessionLifecycle|TestIntegrationApprovalRouting|TestEarlyApprovalSurvivesRestartAndBinding' -count=1

image-smoke:
	docker build -f services/loom/Dockerfile -t piglor-loom:local .
	./scripts/verify_image.sh piglor-loom:local

agent:
	cargo build --workspace --locked

console-setup:
	npm ci
	npm run build

console-check: check test

console-serve:
	cd services/loom && LOOM_WEB_DIR=../../apps/web/dist go run . serve

go-build:
	mkdir -p .loom/bin
	cd services/loom && go build -o ../../.loom/bin/loom-server .
