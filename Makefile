.PHONY: setup init infra migrate serve worker test check prove

setup:
	python3 -m venv .venv
	.venv/bin/pip install -r requirements.lock
	.venv/bin/pip install --no-deps -e .

init:
	.venv/bin/loom init

infra:
	docker compose up -d --wait
	HATCHET_CLI_TELEMETRY_ENABLED=false hatchet server start --project-name loom-dev --profile loom-local --dashboard-port 18888 --grpc-port 17077 --tag v0.105.16 --pull-policy missing

migrate:
	.venv/bin/loom migrate

serve:
	.venv/bin/loom serve

worker:
	HATCHET_CLI_TELEMETRY_ENABLED=false hatchet worker dev -p loom-local --no-reload

test:
	.venv/bin/pytest -q

check:
	.venv/bin/ruff check server tests scripts deploy/coolify
	.venv/bin/ruff format --check server tests scripts deploy/coolify

prove:
	.venv/bin/python scripts/prove_recovery.py --restart-container loom-dev-hatchet-1

agent:
	cargo build --workspace --locked

prove-remote: agent
	.venv/bin/python -m scripts.prove_remote

prove-container:
	docker build -t piglor-loom:local .
	.venv/bin/python -m scripts.prove_container

prove-backup:
	.venv/bin/python -m scripts.prove_backup
