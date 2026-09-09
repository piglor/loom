import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from loom.api import create_app
from loom.runtime import execute_demo
from loom.store import Conflict, NotFound, Store

from tests.conftest import matching_event


def test_wait_is_durable_and_does_not_invoke_runtime(store, goal, monkeypatch):
    execute_demo(store, goal["id"])
    waiting = store.inspect(goal["id"])
    assert waiting["state"] == "WAITING"
    assert waiting["attempts"][0]["state"] == "STOPPED"

    def unexpected(*args, **kwargs):
        pytest.fail("Runtime invoked while waiting")

    with monkeypatch.context() as scoped:
        scoped.setattr("loom.runtime.subprocess.run", unexpected)
        restarted = Store(store.settings)
        restarted.recover_uncertain()
        for _ in range(4):
            execute_demo(restarted, goal["id"])
        time.sleep(0.05)
    later = restarted.inspect(goal["id"])
    assert (
        later["metrics"]["execution_seconds"] == waiting["metrics"]["execution_seconds"]
    )
    assert (
        later["metrics"]["suspended_seconds"] > waiting["metrics"]["suspended_seconds"]
    )
    assert restarted.receive(matching_event(goal))["disposition"] == "accepted"
    execute_demo(restarted, goal["id"])
    completed = restarted.inspect(goal["id"])
    assert completed["state"] == "COMPLETED"
    assert len(completed["attempts"]) == 2
    assert completed["session"]["id"] == waiting["session"]["id"]
    assert completed["metrics"]["tokens"] is None


def test_event_before_wait_and_semantic_duplicate(store, goal):
    assert store.receive(matching_event(goal))["disposition"] == "accepted"
    assert store.receive(matching_event(goal))["disposition"] == "already_satisfied"
    execute_demo(store, goal["id"])
    assert store.inspect(goal["id"])["state"] == "READY"
    execute_demo(store, goal["id"])
    assert store.inspect(goal["id"])["state"] == "COMPLETED"


def test_duplicate_delivery_and_conflicting_body(store, goal):
    event = matching_event(goal)
    store.receive(event)
    assert store.receive(event)["disposition"] == "duplicate"
    with pytest.raises(Conflict):
        store.receive(event.model_copy(update={"version": "C"}))


@pytest.mark.parametrize(
    "change",
    [
        {"version": "A"},
        {"version": "C"},
        {"generation": 2},
        {"resource": "other-pr"},
        {"source": "other-repo"},
        {"type": "wrong"},
    ],
)
def test_mismatched_and_out_of_order_events(store, goal, change):
    execute_demo(store, goal["id"])
    assert store.receive(matching_event(goal, **change))["disposition"] == "mismatch"
    assert store.inspect(goal["id"])["state"] == "WAITING"
    assert len(store.inspect(goal["id"])["attempts"]) == 1


def test_unknown_and_other_organization(store, goal):
    assert (
        store.receive(matching_event(goal, goal_id=uuid4()))["disposition"]
        == "unknown_goal"
    )
    other = Store(replace(store.settings, organization=str(uuid4())))
    with pytest.raises(NotFound):
        other.inspect(goal["id"])
    try:
        assert other.receive(matching_event(goal))["disposition"] == "unknown_goal"
        assert other.list() == []
    finally:
        with other.connect() as conn:
            conn.execute(
                "DELETE FROM events WHERE organization=%s",
                (other.settings.organization,),
            )


def test_concurrent_deliveries_and_claims(store, goal):
    event = matching_event(goal)
    with ThreadPoolExecutor(max_workers=6) as pool:
        outcomes = list(pool.map(lambda _: store.receive(event), range(6)))
        attempts = list(pool.map(lambda _: store.claim(goal["id"]), range(6)))
    assert sum(x["disposition"] == "accepted" for x in outcomes) == 1
    assert sum(x is not None for x in attempts) == 1


@pytest.mark.parametrize(
    "field,value", [("worker_id", "wrong-worker"), ("session_id", uuid4())]
)
def test_wrong_attempt_binding(store, goal, field, value):
    attempt = store.claim(goal["id"])
    with pytest.raises(Conflict):
        store.finish({**attempt, field: value}, 1)
    assert store.inspect(goal["id"])["attempts"][0]["state"] == "RUNNING"


def test_runtime_failure(store, goal, monkeypatch):
    def fail(*args, **kwargs):
        raise OSError("runtime unavailable")

    monkeypatch.setattr("loom.runtime.subprocess.run", fail)
    execute_demo(store, goal["id"])
    result = store.inspect(goal["id"])
    assert result["state"] == "FAILED"
    assert result["attempts"][0]["outcome"] == "runtime_failure"


def test_worker_crash_blocks_ambiguous_attempt(store, goal):
    store.claim(goal["id"])
    Store(store.settings).recover_uncertain()
    result = store.inspect(goal["id"])
    assert result["state"] == "BLOCKED"
    assert result["attempts"][0]["state"] == "UNKNOWN"
    assert store.claim(goal["id"]) is None
    with pytest.raises(Conflict):
        store.cancel(goal["id"])


def test_cancel_and_late_event(store, goal):
    execute_demo(store, goal["id"])
    store.cancel(goal["id"])
    assert store.receive(matching_event(goal))["disposition"] == "inactive"
    execute_demo(store, goal["id"])
    result = store.inspect(goal["id"])
    assert result["state"] == "CANCELLED"
    assert len(result["attempts"]) == 1


def test_outbox_survives_restart_and_resignals(store, goal):
    item = store.outbox_batch()[0]
    assert item["kind"] == "start"
    store.delivery_failed(item)
    with store.connect() as conn:
        conn.execute(
            "UPDATE outbox SET available_at=clock_timestamp() WHERE id=%s",
            (item["id"],),
        )
    assert Store(store.settings).outbox_batch()[0]["id"] == item["id"]
    store.delivered(item)
    assert store.outbox_batch() == []


def test_api_auth_validation_and_inspection(store, goal):
    client = TestClient(create_app(store.settings))
    assert client.get("/v1/goals").status_code == 401
    assert (
        client.get("/v1/goals", headers={"Authorization": "Bearer wrong"}).status_code
        == 401
    )
    headers = {"Authorization": "Bearer " + store.settings.api_token}
    assert client.get("/v1/goals", headers=headers).status_code == 200
    assert (
        client.get(f"/v1/goals/{goal['id']}", headers=headers).json()["state"]
        == "READY"
    )
    assert (
        client.post(
            "/v1/events", headers=headers, json={"trust": {"verified": True}}
        ).status_code
        == 422
    )
    assert client.get(f"/v1/goals/{uuid4()}", headers=headers).status_code == 404


def test_stop_receipt_retries_without_runtime_restart(store, goal, monkeypatch):
    import psycopg
    from loom.runtime import flush_receipts

    original = store.finish
    with monkeypatch.context() as scoped:

        def unavailable(*args, **kwargs):
            raise psycopg.OperationalError("temporary outage")

        scoped.setattr(store, "finish", unavailable)
        with pytest.raises(psycopg.OperationalError):
            execute_demo(store, goal["id"])
    monkeypatch.setattr(store, "finish", original)

    def unexpected(*args, **kwargs):
        pytest.fail("Stop reporting must not relaunch runtime")

    monkeypatch.setattr("loom.runtime.subprocess.run", unexpected)
    flush_receipts(store)
    execute_demo(store, goal["id"])
    result = store.inspect(goal["id"])
    assert result["state"] == "WAITING"
    assert len(result["attempts"]) == 1


def test_failed_continuation_still_counts_wake(store, goal):
    execute_demo(store, goal["id"])
    store.receive(matching_event(goal))
    attempt = store.claim(goal["id"])
    assert store.inspect(goal["id"])["metrics"]["wake_ups"] == 1
    store.finish(attempt, 1, success=False)
    assert store.inspect(goal["id"])["metrics"]["wake_ups"] == 1


@pytest.mark.parametrize("operation", ["replace", "fsync"])
def test_journal_disk_failure_reports_stopped(store, goal, monkeypatch, operation):
    def unavailable(*args, **kwargs):
        raise OSError("journal unavailable")

    monkeypatch.setattr(f"loom.runtime.os.{operation}", unavailable)
    execute_demo(store, goal["id"])
    result = store.inspect(goal["id"])
    assert result["state"] == "WAITING"
    assert result["attempts"][0]["state"] == "STOPPED"
