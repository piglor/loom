import argparse
import json
import os
import secrets
import sys
import urllib.error
import urllib.request
from pathlib import Path

from loom.config import Settings
from loom.store import Store


def load_env():
    # Development config written by `loom init`; do not execute shell content.
    path = Path(".env")
    if path.exists():
        for line in path.read_text().splitlines():
            if line and not line.startswith("#") and "=" in line:
                key, value = line.split("=", 1)
                os.environ.setdefault(key, value)


def request(method, path, body=None):
    url = os.environ.get("LOOM_URL", "http://127.0.0.1:8000").rstrip("/")
    token = os.environ.get("LOOM_API_TOKEN", "")
    req = urllib.request.Request(
        url + path,
        data=json.dumps(body).encode() if body is not None else None,
        headers={
            "Authorization": "Bearer " + token,
            "Content-Type": "application/json",
        },
        method=method,
    )
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)


def main():
    parser = argparse.ArgumentParser(
        description="Piglor Loom — pay for thinking, not waiting"
    )
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("init", help="Create ignored local development credentials")
    commands.add_parser("migrate", help="Apply Loom's PostgreSQL schema")
    commands.add_parser("serve", help="Serve the local Goal API on 127.0.0.1:8000")
    commands.add_parser("worker", help="Run the central Hatchet demo worker")
    enroll = commands.add_parser("enroll", help="Enroll a scoped finite-runtime worker")
    enroll.add_argument("--workspace-ref", required=True)
    enroll.add_argument(
        "--output", required=True, help="New owner-only agent config file"
    )
    enroll.add_argument("--state-dir", required=True)
    enroll.add_argument("--allow-insecure-localhost", action="store_true")
    goals = commands.add_parser("goal").add_subparsers(dest="action", required=True)
    create = goals.add_parser("create")
    create.add_argument("--title", required=True)
    create.add_argument("--objective", required=True)
    create.add_argument("--runtime", choices=["demo", "remote-demo"], default="demo")
    create.add_argument("--worker-id")
    for field in ("source", "type", "resource", "version"):
        create.add_argument("--" + field, required=True)
    goals.add_parser("list")
    for name in ("inspect", "cancel"):
        goals.add_parser(name).add_argument("id")
    event = commands.add_parser("event")
    event.add_argument("file", help="JSON event file")
    args = parser.parse_args()
    load_env()
    try:
        if args.command == "init":
            password = secrets.token_hex(24)
            data = (
                f"LOOM_POSTGRES_PASSWORD={password}\n"
                f"LOOM_DATABASE_URL=postgresql://loom:{password}@127.0.0.1:15432/loom\n"
                f"LOOM_TEST_DATABASE_URL=postgresql://loom:{password}@127.0.0.1:15432/loom_test\n"
                f"LOOM_API_TOKEN={secrets.token_hex(32)}\n"
                "LOOM_URL=http://127.0.0.1:8000\n"
            )
            fd = os.open(".env", os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as output:
                output.write(data)
            print("Created .env (owner-readable only). Keep it out of version control.")
        elif args.command == "migrate":
            Store(Settings.from_env()).migrate()
            print("Schema version 6 ready")
        elif args.command == "serve":
            import uvicorn

            uvicorn.run(
                "loom.api:create_app",
                factory=True,
                host=os.environ.get("LOOM_BIND_HOST", "127.0.0.1"),
                port=int(os.environ.get("LOOM_PORT", "8000")),
            )
        elif args.command == "worker":
            from loom.worker import main as run_worker

            run_worker()
        elif args.command == "event":
            print(
                json.dumps(
                    request(
                        "POST", "/v1/events", json.loads(Path(args.file).read_text())
                    ),
                    indent=2,
                )
            )
        elif args.command == "enroll":
            # Reserve the output before enrollment; never overwrite an identity.
            fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as output:
                worker = request(
                    "POST", "/v1/workers", {"workspace_ref": args.workspace_ref}
                )
                config = {k: worker[k] for k in ("worker_id", "token", "workspace_ref")}
                config.update(
                    server_url=os.environ.get("LOOM_URL", "http://127.0.0.1:8000"),
                    state_dir=str(Path(args.state_dir).resolve()),
                    allow_insecure_localhost=args.allow_insecure_localhost,
                )
                json.dump(config, output, indent=2)
                output.flush()
                os.fsync(output.fileno())
            print("Worker enrolled: " + worker["worker_id"])
            print("Credential saved in owner-only configuration; keep it private.")
        elif args.action == "create":
            body = {
                "title": args.title,
                "objective": args.objective,
                "runtime": args.runtime,
                "worker_id": args.worker_id,
                "condition": {
                    k: getattr(args, k)
                    for k in ("source", "type", "resource", "version")
                },
            }
            print(json.dumps(request("POST", "/v1/goals", body), indent=2))
        elif args.action == "list":
            print(json.dumps(request("GET", "/v1/goals"), indent=2))
        else:
            path = "/v1/goals/" + args.id
            if args.action == "cancel":
                print(json.dumps(request("POST", path + "/cancel"), indent=2))
            else:
                print(json.dumps(request("GET", path), indent=2))
    except urllib.error.HTTPError as error:
        print(f"HTTP {error.code}: {error.read().decode()}", file=sys.stderr)
        raise SystemExit(1)
    except (OSError, ValueError) as error:
        print(
            f"{type(error).__name__}: check configuration and service availability",
            file=sys.stderr,
        )
        raise SystemExit(1)
