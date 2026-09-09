"""Real PostgreSQL contract parity for the incremental Go read API."""

import os
import socket
import subprocess
import time
from datetime import datetime
from pathlib import Path
from uuid import uuid4

import httpx
import pytest
from fastapi.encoders import jsonable_encoder
from loom.config import Settings
from loom.runtime import execute_demo
from loom.store import Store

from tests.conftest import matching_event

ROOT = Path(__file__).resolve().parents[1]


@pytest.fixture(scope="module")
def go_binary(tmp_path_factory):
    binary = tmp_path_factory.mktemp("go-build") / "loom-server"
    subprocess.run(
        ["go", "build", "-o", str(binary), "."],
        cwd=ROOT / "services/loom",
        check=True,
        timeout=180,
    )
    return binary


@pytest.fixture
def go_api(go_binary, store, tmp_path):
    (tmp_path / "index.html").write_text("<!doctype html><title>Loom</title>")
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    env = {
        **os.environ,
        "LOOM_DATABASE_URL": store.settings.database_url,
        "LOOM_API_TOKEN": store.settings.api_token,
        "LOOM_ORGANIZATION": store.settings.organization,
        "LOOM_UPSTREAM_URL": "http://127.0.0.1:1",
        "LOOM_WEB_DIR": str(tmp_path),
        "LOOM_LISTEN_ADDR": f"127.0.0.1:{port}",
    }
    process = subprocess.Popen(
        [str(go_binary)],
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    try:
        with httpx.Client(
            base_url=f"http://127.0.0.1:{port}",
            headers={"Authorization": f"Bearer {store.settings.api_token}"},
            timeout=5,
        ) as client:
            for _ in range(100):
                assert process.poll() is None, "Go server exited during startup"
                try:
                    if client.get("/healthz").status_code == 200:
                        break
                except httpx.ConnectError:
                    pass
                time.sleep(0.05)
            else:
                pytest.fail("Go server did not become healthy")
            yield client
    finally:
        process.terminate()
        process.wait(timeout=15)


def test_go_goal_read_contract(go_api, store, goal):
    # Force the cross-language representation edge instead of waiting for a
    # randomly generated microsecond value ending in zero.
    with store.connect() as conn:
        conn.execute(
            "UPDATE runs SET created_at='2026-09-09T07:38:10.239980Z' WHERE goal_id=%s",
            (goal["id"],),
        )
    response = go_api.get(f"/v1/goals/{goal['id']}")
    assert response.status_code == 200
    actual = response.json()
    expected = jsonable_encoder(store.inspect(goal["id"]))
    for key in ("id", "state", "objective", "completion_criteria", "run", "session"):
        assert normalize(actual[key]) == normalize(expected[key])
    assert actual["wait"]["condition"] == expected["wait"]["condition"]
    assert actual["metrics"]["tokens"] is None
    assert actual["metrics"]["provider_cost"] is None
    assert actual["metrics"]["execution_seconds"] == 0
    assert actual["attempts"] == []
    assert actual["audit"][0]["action"] == "goal_created"
    assert go_api.get("/v1/goals").json()[0]["id"] == str(goal["id"])


def test_go_organization_isolation(go_api, store, goal):
    stranger = Store(
        Settings(store.settings.database_url, store.settings.api_token, str(uuid4()))
    )
    with stranger.connect() as conn:
        conn.execute(
            "UPDATE goals SET organization=%s WHERE id=%s",
            (stranger.settings.organization, goal["id"]),
        )
    try:
        assert go_api.get("/v1/goals").json() == []
        assert go_api.get(f"/v1/goals/{goal['id']}").status_code == 404
    finally:
        with store.connect() as conn:
            conn.execute(
                "UPDATE goals SET organization=%s WHERE id=%s",
                (store.settings.organization, goal["id"]),
            )


def test_go_authentication_and_unknown_goal(go_api):
    assert (
        go_api.get("/v1/goals", headers={"Authorization": "Bearer bad"}).status_code
        == 401
    )
    assert go_api.get(f"/v1/goals/{uuid4()}").status_code == 404
    assert go_api.get("/v1/goals/invalid").status_code == 422
    assert go_api.get("/v1/goals").json() == []


def assert_contract(go_api, store, goal_id):
    actual = go_api.get(f"/v1/goals/{goal_id}").json()
    expected = jsonable_encoder(store.inspect(goal_id))
    for field in ("lifetime_seconds", "suspended_seconds"):
        assert (
            abs(float(actual["metrics"][field]) - float(expected["metrics"][field])) < 2
        )
        actual["metrics"].pop(field)
        expected["metrics"].pop(field)

    # ISO timestamps may differ only in insignificant trailing fractional zeros.
    assert normalize(actual) == normalize(expected)


def normalize(value):
    if isinstance(value, list):
        return [normalize(item) for item in value]
    if isinstance(value, dict):
        return {
            key: datetime.fromisoformat(item)
            if key.endswith("_at") and isinstance(item, str)
            else normalize(item)
            for key, item in value.items()
        }
    return value


@pytest.mark.parametrize("outcome", ["complete", "cancel", "uncertain", "failure"])
def test_go_full_lifecycle_contract(go_api, store, goal, outcome):
    if outcome == "uncertain":
        store.claim(goal["id"])
        store.recover_uncertain()
    elif outcome == "failure":
        attempt = store.claim(goal["id"])
        store.finish(attempt, 20, success=False)
    else:
        execute_demo(store, goal["id"])
        assert_contract(go_api, store, goal["id"])
        before = store.inspect(goal["id"])["attempts"]
        for _ in range(3):
            go_api.get(f"/v1/goals/{goal['id']}")
        assert store.inspect(goal["id"])["attempts"] == before
        if outcome == "cancel":
            store.cancel(goal["id"])
        else:
            store.receive(matching_event(goal))
            execute_demo(store, goal["id"])
    assert_contract(go_api, store, goal["id"])
