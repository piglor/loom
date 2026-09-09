"""Repeatable outbound protocol; no provider-specific fields or inference."""

from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from loom.api import create_app
from loom.mailbox import Mailbox
from loom.models import ClaimRequest, Condition, GoalCreate, StopReport, WorkerEnroll
from loom.store import Conflict

from tests.conftest import matching_event


@pytest.mark.parametrize("budget", [2, 3])
def test_repeatable_http_worker_cycles(store, budget):
    box = Mailbox(store)
    worker = box.enroll(WorkerEnroll(workspace_ref="proof", protocol_version=2))
    client = TestClient(create_app(store.settings))
    headers = {"Authorization": "Bearer " + worker["token"]}
    initial = Condition(source="deployment", type="ready", resource="1", version="1")
    final = initial.model_copy(update={"source": "human", "type": "approved"})
    response = client.post(
        "/v1/goals",
        headers={"Authorization": "Bearer " + store.settings.api_token},
        json={
            "title": "Repeated outbound waits",
            "objective": "Wait twice",
            "runtime": "remote-demo",
            "worker_id": worker["worker_id"],
            "condition": initial.model_dump(),
            "completion_condition": final.model_dump(),
            "max_attempts": budget,
        },
    )
    assert response.status_code == 201, response.text
    goal = response.json()
    for phase in range(3):
        box.dispatch(goal["id"])
        if phase == budget:
            current = store.inspect(goal["id"])
            assert current["state"] == "BLOCKED"
            assert len(current["attempts"]) == budget
            assert not box.poll(worker["token"])["commands"]
            assert current["audit"][-1]["action"] == "attempt_budget_exhausted"
            return
        offered = box.poll(worker["token"])["commands"][0]
        path = f"/v1/worker/commands/{offered['id']}"
        claim = {"protocol_version": 2, "claim_id": str(uuid4())}
        assert (
            client.post(
                path + "/claim",
                headers=headers,
                json={
                    **claim,
                    "protocol_version": 1,
                },
            ).status_code
            == 409
        )
        execution = client.post(path + "/claim", headers=headers, json=claim)
        assert execution.status_code == 200, execution.text
        execution = execution.json()
        assert execution["session_id"] == goal["session"]["id"]
        assert execution["phase"] == phase
        assert execution["context"]["lifecycle"] == "event-driven-v1"
        assert execution["context"]["completion_condition"] == final.model_dump()
        if phase == 1:
            preparation = {
                **claim,
                "session_id": execution["session_id"],
                "expected_generation": 1,
                "condition": final.model_dump(),
            }
            other = box.enroll(WorkerEnroll(workspace_ref="other", protocol_version=2))
            foreign_headers = {"Authorization": "Bearer " + other["token"]}
            assert (
                client.post(
                    path + "/wait", headers=foreign_headers, json=preparation
                ).status_code
                == 404
            )
            box.revoke(other["worker_id"])
            assert (
                client.post(
                    path + "/wait", headers=foreign_headers, json=preparation
                ).status_code
                == 401
            )
            for override in (
                {"session_id": str(uuid4())},
                {"claim_id": str(uuid4())},
                {"expected_generation": 9},
            ):
                assert (
                    client.post(
                        path + "/wait",
                        headers=headers,
                        json={**preparation, **override},
                    ).status_code
                    == 409
                )
            prepared = client.post(path + "/wait", headers=headers, json=preparation)
            assert prepared.status_code == 200, prepared.text
            assert prepared.json()["generation"] == 2
            assert (
                client.post(path + "/wait", headers=headers, json=preparation).json()
                == prepared.json()
            )
            # A fast external result cannot cause concurrent execution.
            store.receive(matching_event(goal, generation=2, **final.model_dump()))
            assert store.inspect(goal["id"])["state"] == "RUNNING"
        report = {
            **claim,
            "session_id": execution["session_id"],
            "duration_ms": 1,
            "success": True,
            "outcome": "complete" if phase == 2 else "yield",
        }
        stopped = client.post(path + "/stop", headers=headers, json=report)
        assert stopped.status_code == 200, stopped.text
        assert (
            client.post(path + "/stop", headers=headers, json=report).json()["status"]
            == "duplicate"
        )
        assert (
            client.post(
                path + "/stop", headers=headers, json={**report, "outcome": "blocked"}
            ).status_code
            == 409
        )
        if phase == 0:
            assert store.inspect(goal["id"])["state"] == "WAITING"
            assert not box.poll(worker["token"])["commands"]
            store.receive(matching_event(goal))
    current = store.inspect(goal["id"])
    assert current["state"] == "COMPLETED"
    assert len(current["attempts"]) == 3
    assert all(attempt["state"] == "STOPPED" for attempt in current["attempts"])


def test_repeatable_requires_explicit_worker_capability(store):
    box = Mailbox(store)
    worker = box.enroll(WorkerEnroll(workspace_ref="legacy"))
    condition = Condition(source="test", type="ready", resource="1", version="1")
    with pytest.raises(Conflict, match="protocol 2"):
        store.create(
            GoalCreate(
                title="No downgrade",
                objective="Stay safe",
                condition=condition,
                completion_condition=condition,
                runtime="remote-demo",
                worker_id=worker["worker_id"],
            )
        )
    goal = store.create(
        GoalCreate(
            title="Legacy",
            objective="Stay safe",
            condition=condition,
            runtime="remote-demo",
            worker_id=worker["worker_id"],
        )
    )
    box.dispatch(goal["id"])
    command = box.poll(worker["token"])["commands"][0]
    with pytest.raises(Conflict, match="not enrolled"):
        box.claim(
            worker["token"],
            command["id"],
            ClaimRequest(protocol_version=2, claim_id=uuid4()),
        )
    assert store.inspect(goal["id"])["attempts"][0]["state"] == "QUEUED"


def test_protocol_two_preserves_legacy_policy_and_receipts(store):
    box = Mailbox(store)
    worker = box.enroll(WorkerEnroll(workspace_ref="legacy", protocol_version=2))
    goal = store.create(
        GoalCreate(
            title="Legacy policy",
            objective="Two attempts",
            runtime="remote-demo",
            worker_id=worker["worker_id"],
            condition=Condition(source="test", type="ready", resource="1", version="1"),
        )
    )
    for phase in (0, 1):
        box.dispatch(goal["id"])
        command = box.poll(worker["token"])["commands"][0]
        claim = ClaimRequest(protocol_version=2, claim_id=uuid4())
        execution = box.claim(worker["token"], command["id"], claim)
        assert execution["context"]["lifecycle"] == "legacy"
        report = StopReport(
            **claim.model_dump(),
            session_id=execution["session_id"],
            duration_ms=1,
            success=True,
        )
        box.report(worker["token"], command["id"], report)
        assert (
            box.report(worker["token"], command["id"], report)["status"] == "duplicate"
        )
        if phase == 0:
            store.receive(matching_event(goal))
    assert store.inspect(goal["id"])["state"] == "COMPLETED"
