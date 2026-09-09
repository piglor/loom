from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from loom.api import create_app
from loom.mailbox import Mailbox, Unauthorized
from loom.models import ClaimRequest, Condition, GoalCreate, StopReport, WorkerEnroll
from loom.store import Conflict, NotFound

from tests.conftest import matching_event


@pytest.fixture
def remote(store):
    box = Mailbox(store)
    worker = box.enroll(WorkerEnroll(workspace_ref="sandbox"))
    goal = store.create(
        GoalCreate(
            title="Remote proof",
            objective="Wait then resume",
            runtime="remote-demo",
            worker_id=worker["worker_id"],
            condition=Condition(source="test", type="ready", resource="1", version="1"),
        )
    )
    box.dispatch(goal["id"])
    return box, worker, goal


def test_offline_and_reconnect_exact_affinity(store, remote):
    box, worker, goal = remote
    other = box.enroll(WorkerEnroll(workspace_ref="sandbox"))
    assert not box.poll(other["token"])["commands"]
    assert store.inspect(goal["id"])["waiting_reason"] == "worker"
    for phase in (0, 1):
        command = box.poll(worker["token"])["commands"][0]
        claim = ClaimRequest(claim_id=uuid4())
        result = box.claim(worker["token"], command["id"], claim)
        assert result["phase"] == phase
        assert result["session_id"] == goal["session"]["id"]
        assert box.claim(worker["token"], command["id"], claim) == result
        report = StopReport(
            claim_id=claim.claim_id,
            session_id=result["session_id"],
            duration_ms=1,
            success=True,
        )
        assert (
            box.report(worker["token"], command["id"], report)["status"] == "accepted"
        )
        assert (
            box.report(worker["token"], command["id"], report)["status"] == "duplicate"
        )
        if phase == 0:
            assert store.inspect(goal["id"])["state"] == "WAITING"
            assert not box.poll(worker["token"])["commands"]
            store.receive(matching_event(goal))
            box.dispatch(goal["id"])
    assert store.inspect(goal["id"])["state"] == "COMPLETED"


def test_claim_fences_and_stop_integrity(remote):
    box, worker, goal = remote
    command = box.poll(worker["token"])["commands"][0]
    other = box.enroll(WorkerEnroll(workspace_ref="sandbox"))
    with pytest.raises(NotFound):
        box.claim(other["token"], command["id"], ClaimRequest(claim_id=uuid4()))
    claim = ClaimRequest(claim_id=uuid4())
    box.claim(worker["token"], command["id"], claim)
    with pytest.raises(Conflict):
        box.claim(worker["token"], command["id"], ClaimRequest(claim_id=uuid4()))
    report = StopReport(
        claim_id=claim.claim_id, session_id=uuid4(), duration_ms=1, success=True
    )
    with pytest.raises(Conflict):
        box.report(worker["token"], command["id"], report)
    report.session_id = goal["session"]["id"]
    box.report(worker["token"], command["id"], report)
    report.success = False
    with pytest.raises(Conflict):
        box.report(worker["token"], command["id"], report)


def test_worker_token_is_not_admin_and_revocation(store, remote):
    box, worker, _ = remote
    client = TestClient(create_app(store.settings))
    headers = {"Authorization": "Bearer " + worker["token"]}
    assert client.get("/v1/goals", headers=headers).status_code == 401
    assert client.get("/v1/worker/commands", headers=headers).status_code == 200
    box.revoke(worker["worker_id"])
    with pytest.raises(Unauthorized):
        box.poll(worker["token"])
    assert client.get("/v1/worker/commands", headers=headers).status_code == 401


def test_remote_attempt_not_marked_unknown_by_orchestrator_restart(store, remote):
    box, worker, goal = remote
    command = box.poll(worker["token"])["commands"][0]
    box.claim(worker["token"], command["id"], ClaimRequest(claim_id=uuid4()))
    store.recover_uncertain()
    assert store.inspect(goal["id"])["state"] == "RUNNING"


def test_early_event_preserves_worker_wait_and_cancellation(store, remote):
    box, worker, goal = remote
    command = box.poll(worker["token"])["commands"][0]
    store.receive(matching_event(goal))
    box.dispatch(goal["id"])
    current = store.inspect(goal["id"])
    assert current["state"] == "WAITING"
    assert current["waiting_reason"] == "worker"
    store.cancel(goal["id"])
    assert not box.poll(worker["token"])["commands"]
    with pytest.raises(Conflict):
        box.claim(worker["token"], command["id"], ClaimRequest(claim_id=uuid4()))


def test_local_admission_rejects_remote_binding(store, remote):
    _, _, goal = remote
    with store.connect() as conn:
        conn.execute("UPDATE goals SET state='READY' WHERE id=%s", (goal["id"],))
    with pytest.raises(Conflict):
        store.claim(goal["id"])
