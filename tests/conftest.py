import os
from uuid import uuid4

import psycopg
import pytest
from loom.cli import load_env
from loom.config import Settings
from loom.models import Condition, EventCreate, GoalCreate
from loom.store import Store
from psycopg.conninfo import conninfo_to_dict


@pytest.fixture
def store(tmp_path, monkeypatch):
    load_env()
    settings = Settings.from_env()
    test_url = os.environ.get("LOOM_TEST_DATABASE_URL")
    if not test_url or not conninfo_to_dict(test_url).get("dbname", "").endswith(
        "_test"
    ):
        pytest.fail(
            "Set LOOM_TEST_DATABASE_URL to a dedicated database ending in _test"
        )
    params = conninfo_to_dict(test_url)
    database = params.pop("dbname")
    with psycopg.connect(**params, dbname="postgres", autocommit=True) as connection:
        if not connection.execute(
            "SELECT 1 FROM pg_database WHERE datname=%s", (database,)
        ).fetchone():
            connection.execute(
                psycopg.sql.SQL("CREATE DATABASE {}").format(
                    psycopg.sql.Identifier(database)
                )
            )
    monkeypatch.setenv("LOOM_STATE_DIR", str(tmp_path))
    instance = Store(Settings(test_url, settings.api_token, str(uuid4())))
    instance.migrate()
    yield instance
    with instance.connect() as conn:
        org = instance.settings.organization
        for table in (
            "github_bindings",
            "integration_bindings",
            "commands",
            "audit",
            "outbox",
            "wait_history",
            "waits",
            "attempts",
        ):
            conn.execute(
                psycopg.sql.SQL(
                    "DELETE FROM {} WHERE goal_id IN "
                    "(SELECT id FROM goals WHERE organization=%s)"
                ).format(psycopg.sql.Identifier(table)),
                (org,),
            )
        conn.execute(
            "DELETE FROM sessions WHERE run_id IN (SELECT r.id FROM runs r "
            "JOIN goals g ON g.id=r.goal_id WHERE g.organization=%s)",
            (org,),
        )
        conn.execute(
            "DELETE FROM runs WHERE goal_id IN "
            "(SELECT id FROM goals WHERE organization=%s)",
            (org,),
        )
        conn.execute("DELETE FROM goals WHERE organization=%s", (org,))
        conn.execute("DELETE FROM events WHERE organization=%s", (org,))
        conn.execute("DELETE FROM workers WHERE organization=%s", (org,))
        conn.execute("DELETE FROM github_deliveries WHERE organization=%s", (org,))
        conn.execute("DELETE FROM integration_deliveries WHERE organization=%s", (org,))


@pytest.fixture
def goal(store):
    return store.create(
        GoalCreate(
            title="Wait proof",
            objective="Resume only after the matching external event",
            condition=Condition(
                source="test", type="job.completed", resource="job-1", version="B"
            ),
        )
    )


def matching_event(goal, **overrides):
    data = dict(
        goal_id=goal["id"],
        generation=1,
        delivery_id=str(uuid4()),
        **goal["wait"]["condition"],
    )
    data.update(overrides)
    return EventCreate(**data)
