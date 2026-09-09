"""Finite-runtime repeatable core + real Go reader/restart proof, not a model proof."""

import json
import os
import subprocess
import tempfile
from pathlib import Path
from uuid import uuid4

import httpx
from loom.cli import load_env
from loom.config import Settings
from loom.models import Condition, EventCreate, GoalCreate
from loom.runtime import execute_demo
from loom.store import Store
from psycopg.conninfo import conninfo_to_dict, make_conninfo

from scripts.prove_recovery import eventually, stop


def local_test_connections(raw):
    params = conninfo_to_dict(raw)
    allowed = {"host", "hostaddr", "port", "dbname", "user", "password", "sslmode"}
    if (
        params.keys() - allowed
        or not params.get("dbname", "").endswith("_test")
        or params.get("host") != "127.0.0.1"
        or params.get("hostaddr") not in (None, "127.0.0.1")
        or not params.get("user")
        or not params.get("port", "5432").isdigit()
    ):
        raise ValueError(
            "Repeatable proof requires a dedicated loopback _test database"
        )
    params.pop("hostaddr", None)
    params.setdefault("port", "5432")
    # libpq and pgx must use the same explicit endpoint, without services or
    # inherited addressing overrides. pgx does not need libpq's hostaddr option.
    return make_conninfo(**params, hostaddr="127.0.0.1"), make_conninfo(**params)


def main():
    load_env()
    test_url, go_url = local_test_connections(
        os.environ.get("LOOM_TEST_DATABASE_URL", "")
    )
    for key in tuple(os.environ):
        if key.startswith("PG"):
            os.environ.pop(key)
    organization = "repeatable-proof-" + str(uuid4())
    token = Settings.from_env().api_token
    store = Store(Settings(test_url, token, organization))
    store.migrate()
    binary = Path(".loom/bin/loom-server").resolve(strict=True)
    conditions = [
        Condition(
            source="deployment",
            type="health.ready",
            resource=str(uuid4()),
            version=str(i),
        )
        for i in range(3)
    ]
    goal = store.create(
        GoalCreate(
            title="Repeatable control-plane proof",
            objective="Three durable waits and four finite attempts",
            condition=conditions[0],
        ),
        completion_condition=conditions[-1],
    )
    environment = {
        **os.environ,
        "LOOM_DATABASE_URL": go_url,
        "LOOM_ORGANIZATION": organization,
        "LOOM_LISTEN_ADDR": "127.0.0.1:18007",
        "LOOM_UPSTREAM_URL": "http://127.0.0.1:1",
        "LOOM_WEB_DIR": str(Path("apps/web/dist").resolve(strict=True)),
    }
    with (
        tempfile.TemporaryDirectory(prefix="loom-repeatable-") as state,
        tempfile.TemporaryFile() as log,
        httpx.Client(
            base_url="http://127.0.0.1:18007",
            timeout=5,
            headers={"Authorization": "Bearer " + token},
        ) as client,
    ):
        previous_state = os.environ.get("LOOM_STATE_DIR")
        os.environ["LOOM_STATE_DIR"] = state
        gateway = None

        def reader_ready():
            if gateway.poll() is not None:
                raise RuntimeError("Proof gateway exited before readiness")
            try:
                # This proof exercises the direct reader, not the write proxy.
                return client.get(f"/v1/goals/{goal['id']}").status_code == 200
            except httpx.TransportError:
                return False

        try:
            execute_demo(store, goal["id"])
            for index in range(3):
                gateway = subprocess.Popen(
                    [str(binary)],
                    env=environment,
                    stdout=log,
                    stderr=log,
                    start_new_session=True,
                )
                eventually(reader_ready)
                assert gateway.poll() is None, "Proof gateway failed to bind"
                waiting = client.get(f"/v1/goals/{goal['id']}").json()
                assert waiting["state"] == "WAITING"
                assert waiting["session"]["id"] == str(goal["session"]["id"])
                assert len(waiting["wait_history"]) == index
                for _ in range(10):
                    execute_demo(store, goal["id"])
                assert store.inspect(goal["id"])["attempts"][-1]["state"] == "STOPPED"
                assert len(store.inspect(goal["id"])["attempts"]) == index + 1
                stop(gateway)
                gateway = None
                store = Store(store.settings)
                store.recover_uncertain()
                incoming = EventCreate(
                    goal_id=goal["id"],
                    delivery_id=str(uuid4()),
                    generation=index + 1,
                    **conditions[index].model_dump(),
                )
                assert store.receive(incoming)["disposition"] == "accepted"
                assert store.receive(incoming)["disposition"] == "duplicate"
                execute_demo(
                    store,
                    goal["id"],
                    next_condition=conditions[index + 1] if index < 2 else None,
                )
            gateway = subprocess.Popen(
                [str(binary)],
                env=environment,
                stdout=log,
                stderr=log,
                start_new_session=True,
            )
            eventually(reader_ready)
            assert gateway.poll() is None
            projected = client.get(f"/v1/goals/{goal['id']}").json()
            domain = store.inspect(goal["id"])
            assert projected["state"] == domain["state"] == "COMPLETED"
            assert len(projected["wait_history"]) == 2
            assert len(projected["attempts"]) == 4
            assert projected["metrics"]["wake_ups"] == 3
            for name in ("lifetime_seconds", "suspended_seconds", "execution_seconds"):
                assert (
                    abs(projected["metrics"][name] - float(domain["metrics"][name]))
                    < 0.000001
                )
            print(
                json.dumps(
                    {
                        "proof": "repeatable-core-go-parity",
                        "attempts": 4,
                        "waits": 3,
                        "gateway_restarts": 3,
                        "metrics": projected["metrics"],
                    }
                )
            )
        finally:
            if gateway is not None:
                stop(gateway)
            if previous_state is None:
                os.environ.pop("LOOM_STATE_DIR", None)
            else:
                os.environ["LOOM_STATE_DIR"] = previous_state


if __name__ == "__main__":
    main()
