from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from loom.api import create_app
from loom.mailbox import Mailbox
from loom.models import ClaimRequest, Condition, GoalCreate, StopReport, WorkerEnroll

from tests.conftest import matching_event


@pytest.fixture(params=[1, 2])
def binding_case(store, request):
    box = Mailbox(store)
    worker = box.enroll(
        WorkerEnroll(workspace_ref="isolated", protocol_version=request.param)
    )
    goal = store.create(
        GoalCreate(
            title="Provider binding",
            objective="Persist context before inference",
            runtime="remote-demo",
            worker_id=worker["worker_id"],
            condition=Condition(
                source="dataset", type="ready", resource="data-1", version="B"
            ),
        )
    )
    box.dispatch(goal["id"])
    command = box.poll(worker["token"])["commands"][0]
    claim = ClaimRequest(claim_id=uuid4(), protocol_version=request.param)
    box.claim(worker["token"], command["id"], claim)
    client = TestClient(create_app(store.settings))
    headers = {"Authorization": "Bearer " + worker["token"]}
    body = {
        "protocol_version": request.param,
        "claim_id": str(claim.claim_id),
        "session_id": str(goal["session"]["id"]),
        "provider_session_id": "thread-exact",
    }
    path = f"/v1/worker/commands/{command['id']}/session"
    return box, worker, goal, command, claim, client, headers, body, path


def test_binding_is_durable_immutable_and_audited_once(store, binding_case):
    *_, client, headers, body, path = binding_case
    for _ in range(2):
        response = client.post(path, headers=headers, json=body)
        assert response.status_code == 200
        assert response.json()["provider_session_id"] == "thread-exact"
    # New API instance, same database. No provider or runtime process is needed.
    restarted = TestClient(create_app(store.settings))
    assert restarted.post(path, headers=headers, json=body).status_code == 200
    assert (
        restarted.post(
            path, headers=headers, json={**body, "provider_session_id": "other"}
        ).status_code
        == 409
    )
    goal = store.inspect(binding_case[2]["id"])
    assert goal["session"]["provider_session_id"] == "thread-exact"
    assert [a["action"] for a in goal["audit"]].count("provider_session_bound") == 1
    assert goal["state"] == "RUNNING"  # Binding is not stop evidence.


@pytest.mark.parametrize("field", ["claim_id", "session_id"])
def test_binding_rejects_wrong_execution(field, binding_case):
    *_, client, headers, body, path = binding_case
    assert (
        client.post(
            path, headers=headers, json={**body, field: str(uuid4())}
        ).status_code
        == 409
    )


def test_binding_rejects_wrong_worker_auth_and_revocation(store, binding_case):
    box, worker, _, _, _, client, headers, body, path = binding_case
    other = box.enroll(WorkerEnroll(workspace_ref="isolated"))
    assert client.post(path, json=body).status_code == 401
    assert (
        client.post(
            path,
            headers={"Authorization": "Bearer " + store.settings.api_token},
            json=body,
        ).status_code
        == 401
    )
    assert (
        client.post(
            path, headers={"Authorization": "Bearer " + other["token"]}, json=body
        ).status_code
        == 404
    )
    box.revoke(worker["worker_id"])
    assert client.post(path, headers=headers, json=body).status_code == 401


def test_bound_session_requires_exact_provider_in_stop_receipt(store, binding_case):
    box, worker, goal, command, claim, client, headers, body, path = binding_case
    assert client.post(path, headers=headers, json=body).status_code == 200
    report = StopReport(
        protocol_version=claim.protocol_version,
        claim_id=claim.claim_id,
        session_id=goal["session"]["id"],
        duration_ms=1,
        success=True,
    )
    stop_path = path.removesuffix("session") + "stop"
    for provider in (None, "other"):
        data = report.model_dump(mode="json")
        if provider:
            data["provider_session_id"] = provider
        assert client.post(stop_path, headers=headers, json=data).status_code == 409
    data["provider_session_id"] = "thread-exact"
    assert (
        client.post(stop_path, headers=headers, json=data).json()["status"]
        == "accepted"
    )
    assert (
        client.post(stop_path, headers=headers, json=data).json()["status"]
        == "duplicate"
    )
    assert store.inspect(goal["id"])["state"] == "WAITING"
    assert client.post(path, headers=headers, json=body).status_code == 409

    # Resume belongs to the next admitted command, but the provider context is
    # immutable across the external wait and control-plane/worker reconnection.
    store.receive(matching_event(goal))
    box.dispatch(goal["id"])
    continuation = box.poll(worker["token"])["commands"][0]
    next_claim = ClaimRequest(claim_id=uuid4(), protocol_version=claim.protocol_version)
    resumed = box.claim(worker["token"], continuation["id"], next_claim)
    assert resumed["session_id"] == goal["session"]["id"]
    next_path = f"/v1/worker/commands/{continuation['id']}/session"
    next_body = {**body, "claim_id": str(next_claim.claim_id)}
    assert client.post(next_path, headers=headers, json=next_body).status_code == 200
    assert (
        client.post(
            next_path,
            headers=headers,
            json={**next_body, "provider_session_id": "replacement"},
        ).status_code
        == 409
    )
    next_report = {**data, "claim_id": str(next_claim.claim_id)}
    assert (
        client.post(
            next_path.removesuffix("session") + "stop",
            headers=headers,
            json=next_report,
        ).status_code
        == 200
    )
    assert store.inspect(goal["id"])["state"] == "COMPLETED"


def test_provider_context_cannot_be_shared_between_goals(store, binding_case):
    box, worker, _, _, _, client, headers, body, path = binding_case
    assert client.post(path, headers=headers, json=body).status_code == 200
    other = store.create(
        GoalCreate(
            title="Another goal",
            objective="Cannot adopt another Goal's context",
            runtime="remote-demo",
            worker_id=worker["worker_id"],
            condition=Condition(
                source="test", type="ready", resource="other", version="1"
            ),
        )
    )
    box.dispatch(other["id"])
    with store.connect() as conn:
        command = conn.execute(
            "SELECT id FROM commands WHERE goal_id=%s", (other["id"],)
        ).fetchone()
    claim = ClaimRequest(claim_id=uuid4())
    box.claim(worker["token"], command["id"], claim)
    response = client.post(
        f"/v1/worker/commands/{command['id']}/session",
        headers=headers,
        json={
            **body,
            "claim_id": str(claim.claim_id),
            "session_id": str(other["session"]["id"]),
        },
    )
    assert response.status_code == 409
    assert store.inspect(other["id"])["session"]["provider_session_id"] is None


def test_legacy_receipt_hash_remains_replayable(store, binding_case):
    import hashlib
    import json

    box, worker, goal, command, claim, client, headers, _, path = binding_case
    legacy = {
        "protocol_version": 1,
        "claim_id": str(claim.claim_id),
        "session_id": str(goal["session"]["id"]),
        "duration_ms": 1.0,
        "success": True,
    }
    box.report(worker["token"], command["id"], StopReport(**legacy))
    with store.connect() as conn:
        conn.execute(
            "UPDATE commands SET report_digest=%s WHERE id=%s",
            (
                hashlib.sha256(json.dumps(legacy, sort_keys=True).encode()).hexdigest(),
                command["id"],
            ),
        )
    response = client.post(
        path.removesuffix("session") + "stop", headers=headers, json=legacy
    )
    assert response.status_code == 200
    assert response.json()["status"] == "duplicate"


@pytest.mark.parametrize(
    "provider", ["", "bad id", "bad\nvalue", "bad\0value", "bad\x7fvalue", "x" * 257]
)
def test_binding_rejects_invalid_provider_ids(binding_case, provider):
    *_, client, headers, body, path = binding_case
    assert (
        client.post(
            path, headers=headers, json={**body, "provider_session_id": provider}
        ).status_code
        == 422
    )


def test_unclaimed_command_cannot_bind(store, binding_case):
    _, _, _, command, _, client, headers, body, path = binding_case
    with store.connect() as conn:
        conn.execute(
            "UPDATE commands SET state='QUEUED',claim_id=NULL WHERE id=%s",
            (command["id"],),
        )
    assert client.post(path, headers=headers, json=body).status_code == 409


def test_concurrent_rebinding_has_one_winner(store, binding_case):
    from concurrent.futures import ThreadPoolExecutor

    *_, headers, body, path = binding_case
    app = create_app(store.settings)

    def bind(provider):
        with TestClient(app) as client:
            return client.post(
                path, headers=headers, json={**body, "provider_session_id": provider}
            ).status_code

    with ThreadPoolExecutor(max_workers=2) as executor:
        assert sorted(executor.map(bind, ["thread-A", "thread-B"])) == [200, 409]
    assert store.inspect(binding_case[2]["id"])["session"]["provider_session_id"] in (
        "thread-A",
        "thread-B",
    )


def test_unicode_provider_id_matches_runtime_character_limit(binding_case):
    *_, client, headers, body, path = binding_case
    provider = "é" * 256
    response = client.post(
        path, headers=headers, json={**body, "provider_session_id": provider}
    )
    assert response.status_code == 200
    assert response.json()["provider_session_id"] == provider


def test_unbound_continuation_cannot_create_context(store, binding_case):
    box, worker, goal, command, claim, client, headers, body, _ = binding_case
    box.report(
        worker["token"],
        command["id"],
        StopReport(
            claim_id=claim.claim_id,
            session_id=goal["session"]["id"],
            duration_ms=1,
            success=True,
        ),
    )
    store.receive(matching_event(goal))
    box.dispatch(goal["id"])
    continuation = box.poll(worker["token"])["commands"][0]
    next_claim = ClaimRequest(claim_id=uuid4())
    box.claim(worker["token"], continuation["id"], next_claim)
    response = client.post(
        f"/v1/worker/commands/{continuation['id']}/session",
        headers=headers,
        json={**body, "claim_id": str(next_claim.claim_id)},
    )
    assert response.status_code == 409
    assert store.inspect(goal["id"])["session"]["provider_session_id"] is None


def test_failure_receipt_also_requires_exact_context(store, binding_case):
    _, _, goal, _, _, client, headers, body, path = binding_case
    assert client.post(path, headers=headers, json=body).status_code == 200
    stop_path = path.removesuffix("session") + "stop"
    receipt = {**body, "duration_ms": 1, "success": False}
    assert (
        client.post(
            stop_path,
            headers=headers,
            json={**receipt, "provider_session_id": "wrong"},
        ).status_code
        == 409
    )
    assert store.inspect(goal["id"])["state"] == "RUNNING"
    assert client.post(stop_path, headers=headers, json=receipt).status_code == 200
    assert store.inspect(goal["id"])["state"] == "FAILED"
