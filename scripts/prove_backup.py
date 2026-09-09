"""Restore drill confined to disposable development/test databases."""

import json
import os
import subprocess
import tempfile
from dataclasses import replace
from pathlib import Path
from uuid import uuid4

import psycopg
from loom.cli import load_env
from loom.config import Settings
from loom.models import Condition, EventCreate, GoalCreate
from loom.runtime import execute_demo
from loom.store import Store
from psycopg import sql
from psycopg.conninfo import conninfo_to_dict, make_conninfo


def main():
    load_env()
    test_url = os.environ["LOOM_TEST_DATABASE_URL"]
    params = conninfo_to_dict(test_url)
    source_name = params.pop("dbname")
    if source_name != "loom_test":
        raise SystemExit("This local drill requires the dedicated loom_test database")
    container = "loom-loom-postgres-1"
    project = subprocess.check_output(
        [
            "docker",
            "inspect",
            container,
            "--format",
            '{{index .Config.Labels "com.docker.compose.project"}}',
        ],
        text=True,
    ).strip()
    if project != "loom":
        raise SystemExit("Refusing to use a database outside the local loom project")
    bindings = json.loads(
        subprocess.check_output(
            [
                "docker",
                "inspect",
                container,
                "--format",
                "{{json .NetworkSettings.Ports}}",
            ],
            text=True,
        )
    )
    expected = {"HostIp": "127.0.0.1", "HostPort": params.get("port", "5432")}
    if (
        params.get("host") != "127.0.0.1"
        or params.get("user") != "loom"
        or params.get("hostaddr") not in (None, "127.0.0.1")
        or params.get("service")
        or expected not in (bindings.get("5432/tcp") or [])
    ):
        raise SystemExit(
            "Test DSN must target the inspected local container's loopback port"
        )
    params["hostaddr"] = "127.0.0.1"
    test_url = make_conninfo(**params, dbname=source_name)
    target = "loom_restore_" + uuid4().hex + "_test"
    org = "backup-proof-" + str(uuid4())
    settings = replace(Settings.from_env(), database_url=test_url, organization=org)
    source = Store(settings)
    source.migrate()
    goal = None
    created = False
    try:
        goal = source.create(
            GoalCreate(
                title="Restore proof",
                objective="Preserve stopped wait through restore",
                condition=Condition(
                    source="proof", type="ready", resource=str(uuid4()), version="1"
                ),
            )
        )
        execute_demo(source, goal["id"])
        with psycopg.connect(**params, dbname="postgres", autocommit=True) as admin:
            admin.execute(sql.SQL("CREATE DATABASE {}").format(sql.Identifier(target)))
            created = True
        with tempfile.TemporaryDirectory(prefix="loom-backup-drill-") as directory:
            archive = Path(directory) / "backup.dump"
            with archive.open("xb") as output:
                archive.chmod(0o600)
                subprocess.run(
                    [
                        "docker",
                        "exec",
                        container,
                        "pg_dump",
                        "-U",
                        "loom",
                        "-Fc",
                        "--no-owner",
                        "--no-privileges",
                        source_name,
                    ],
                    stdout=output,
                    check=True,
                )
            with archive.open("rb") as input_file:
                subprocess.run(
                    [
                        "docker",
                        "exec",
                        "-i",
                        container,
                        "pg_restore",
                        "-U",
                        "loom",
                        "--exit-on-error",
                        "--no-owner",
                        "--no-privileges",
                        "-d",
                        target,
                    ],
                    stdin=input_file,
                    check=True,
                )
            restored = Store(
                replace(settings, database_url=make_conninfo(**params, dbname=target))
            )
            restored.migrate()  # Idempotent upgrade after restoring existing state.
            current = restored.inspect(goal["id"])
            assert current["state"] == "WAITING"
            assert current["session"]["id"] == goal["session"]["id"]
            assert (
                len(current["attempts"]) == 1
                and current["attempts"][0]["state"] == "STOPPED"
            )
            restored.receive(
                EventCreate(
                    goal_id=goal["id"],
                    delivery_id=str(uuid4()),
                    generation=1,
                    **goal["wait"]["condition"],
                )
            )
            execute_demo(restored, goal["id"])
            assert restored.inspect(goal["id"])["state"] == "COMPLETED"
            print(
                "PASS: PostgreSQL dump/restore retained the stopped wait and "
                "exact Session; continuation completed"
            )
    finally:
        if created:
            # Exact UUID-named database created by this invocation only.
            with psycopg.connect(**params, dbname="postgres", autocommit=True) as admin:
                admin.execute(
                    sql.SQL("DROP DATABASE {}").format(sql.Identifier(target))
                )
        with source.connect() as conn:
            # This organization was created solely by this invocation. Query it
            # even if create's response was lost after its transaction committed.
            owned = conn.execute(
                "SELECT g.id,r.id AS run_id FROM goals g JOIN runs r "
                "ON r.goal_id=g.id WHERE g.organization=%s",
                (org,),
            ).fetchall()
            for fixture in owned:
                cleanup_fixture(conn, fixture, org)


def cleanup_fixture(conn, goal, org):
    for table in ("outbox", "audit", "attempts", "waits"):
        conn.execute(
            sql.SQL("DELETE FROM {} WHERE goal_id=%s").format(sql.Identifier(table)),
            (goal["id"],),
        )
    conn.execute("DELETE FROM sessions WHERE run_id=%s", (goal["run_id"],))
    conn.execute("DELETE FROM runs WHERE id=%s", (goal["run_id"],))
    conn.execute("DELETE FROM goals WHERE id=%s AND organization=%s", (goal["id"], org))


if __name__ == "__main__":
    main()
