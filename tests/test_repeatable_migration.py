"""Upgrade a real schema-6 suspended Goal without touching other test schemas."""

import hashlib
import json
from dataclasses import replace
from importlib.resources import files
from uuid import uuid4

import pytest
from loom.github import GitHubIngress
from loom.models import EventCreate
from loom.runtime import execute_demo
from loom.store import Store
from psycopg import sql
from psycopg.conninfo import make_conninfo
from psycopg.types.json import Jsonb

from tests.test_plugin_cycles import external


@pytest.mark.parametrize("with_github", [False, True])
def test_schema6_suspended_goal_resumes_after_upgrade(store, with_github):
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
    if with_github:
        binding, payload = external("github", 1, goal_id)
        condition = binding.condition().model_dump()
        raw = json.dumps(payload).encode()
        delivery_id = uuid4()
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
            if with_github:
                conn.execute(
                    "INSERT INTO github_bindings(goal_id,organization,installation_id,"
                    "repository_id,pull_request,head_sha,run_id,run_attempt,"
                    "workflow_id) "
                    "VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s)",
                    (
                        goal_id,
                        store.settings.organization,
                        binding.installation_id,
                        binding.repository_id,
                        binding.pull_request,
                        binding.head_sha,
                        binding.run_id,
                        binding.run_attempt,
                        binding.workflow_id,
                    ),
                )
                conn.execute(
                    "INSERT INTO github_deliveries(organization,delivery_id,digest,"
                    "disposition,event_type,raw_body,payload) "
                    "VALUES (%s,%s,%s,'ignored','workflow_run',%s,%s)",
                    (
                        store.settings.organization,
                        delivery_id,
                        hashlib.sha256(raw).hexdigest(),
                        raw,
                        Jsonb(payload),
                    ),
                )
        upgraded.migrate()
        upgraded.migrate()
        waiting = upgraded.inspect(goal_id)
        assert waiting["state"] == "WAITING"
        assert waiting["wait"]["id"] == wait_id
        assert waiting["session"]["id"] == session_id
        assert waiting["wait_history"] == []
        assert upgraded.claim(goal_id) is None
        if with_github:
            # Existing pre-generation binding and receipt reconcile after upgrade.
            GitHubIngress(upgraded).reconcile_pending()
            assert (
                GitHubIngress(upgraded).receive(raw, delivery_id, "workflow_run")[
                    "disposition"
                ]
                == "duplicate"
            )
        else:
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
