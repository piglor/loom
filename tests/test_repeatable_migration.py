"""Upgrade a real schema-6 suspended Goal without touching other test schemas."""

from dataclasses import replace
from importlib.resources import files
from uuid import uuid4

from loom.models import EventCreate
from loom.runtime import execute_demo
from loom.store import Store
from psycopg import sql
from psycopg.conninfo import make_conninfo
from psycopg.types.json import Jsonb


def test_schema6_suspended_goal_resumes_after_upgrade(store):
    schema = "upgrade_proof_" + uuid4().hex
    with store.connect() as conn:
        conn.execute(sql.SQL("CREATE SCHEMA {}").format(sql.Identifier(schema)))
    upgraded = Store(
        replace(
            store.settings,
            database_url=make_conninfo(
                store.settings.database_url, options=f"-csearch_path={schema}"
            ),
        )
    )
    goal_id, run_id, session_id, wait_id = (uuid4() for _ in range(4))
    condition = {
        "source": "migration",
        "type": "ready",
        "resource": "proof",
        "version": "1",
    }
    try:
        with upgraded.connect() as conn:
            for filename in (
                "schema.sql",
                "002-workers.sql",
                "003-github.sql",
                "004-github-inbox.sql",
                "005-reconcile.sql",
                "006-provider-binding.sql",
            ):
                conn.execute(files("loom").joinpath(filename).read_text())
            conn.execute(
                "INSERT INTO goals(id,organization,title,objective,state,phase,"
                "completion_criteria) VALUES (%s,%s,'Schema 6 proof',"
                "'Resume original context','WAITING',1,%s)",
                (
                    goal_id,
                    store.settings.organization,
                    Jsonb({"event_matches": condition}),
                ),
            )
            conn.execute(
                "INSERT INTO runs(id,goal_id,policy) VALUES (%s,%s,%s)",
                (run_id, goal_id, Jsonb({"runtime": "demo", "max_attempts": 2})),
            )
            conn.execute(
                "INSERT INTO sessions(id,run_id,worker_id,runtime) "
                "VALUES (%s,%s,'demo-local','demo')",
                (session_id, run_id),
            )
            conn.execute(
                "INSERT INTO waits(id,goal_id,condition,armed_at) "
                "VALUES (%s,%s,%s,clock_timestamp())",
                (wait_id, goal_id, Jsonb(condition)),
            )
            conn.execute(
                "INSERT INTO attempts(id,goal_id,session_id,worker_id,phase,state,"
                "stopped_at,duration_ms,outcome) VALUES "
                "(%s,%s,%s,'demo-local',0,'STOPPED',clock_timestamp(),1,'success')",
                (uuid4(), goal_id, session_id),
            )
        upgraded.migrate()
        upgraded.migrate()
        waiting = upgraded.inspect(goal_id)
        assert waiting["state"] == "WAITING"
        assert waiting["wait"]["id"] == wait_id
        assert waiting["session"]["id"] == session_id
        assert waiting["wait_history"] == []
        assert upgraded.claim(goal_id) is None
        upgraded.receive(
            EventCreate(
                goal_id=goal_id, generation=1, delivery_id=str(uuid4()), **condition
            )
        )
        execute_demo(upgraded, goal_id)
        completed = upgraded.inspect(goal_id)
        assert completed["state"] == "COMPLETED"
        assert len(completed["attempts"]) == 2
        assert completed["session"]["id"] == session_id
    finally:
        # Only the unique schema created by this test; never the shared database.
        with store.connect() as conn:
            conn.execute(
                sql.SQL("DROP SCHEMA {} CASCADE").format(sql.Identifier(schema))
            )
