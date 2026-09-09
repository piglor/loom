import base64
import copy
import hmac
import json
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from loom.api import create_app
from loom.gitlab import GitLabBinding, GitLabPlugin, normalize, verify_signature
from loom.models import GoalCreate
from loom.plugins import configured_plugins, reconcile_integrations
from loom.runtime import execute_demo
from loom.store import Conflict, Store

KEY = b"test-signing-key-not-a-real-secret"
TOKEN = "whsec_" + base64.b64encode(KEY).decode()


def signed(body, delivery=None, timestamp=None):
    delivery = str(delivery or uuid4())
    timestamp = str(timestamp if timestamp is not None else int(time.time()))
    signature = base64.b64encode(
        hmac.digest(
            KEY, delivery.encode() + b"." + timestamp.encode() + b"." + body, "sha256"
        )
    ).decode()
    return {
        "webhook-id": delivery,
        "webhook-timestamp": timestamp,
        "webhook-signature": "v1," + signature,
    }


@pytest.fixture
def gitlab(store, monkeypatch):
    monkeypatch.setenv("LOOM_GITLAB_INSTANCE", "gitlab.com")
    monkeypatch.setenv("LOOM_GITLAB_SIGNING_TOKEN", TOKEN)
    binding = GitLabBinding(
        goal_id=uuid4(),
        instance="gitlab.com",
        project_id=11,
        merge_request=12,
        pipeline_id=81,
        head_sha="a" * 40,
    )
    goal = store.create(
        GoalCreate(
            title="GitLab wait proof",
            objective="Resume correct session",
            condition=binding.condition(),
        )
    )
    binding.goal_id = goal["id"]
    payload = {
        "object_kind": "pipeline",
        "object_attributes": {
            "id": 81,
            "sha": "a" * 40,
            "source": "merge_request_event",
            "status": "success",
            "variables": [{"key": "SECRET", "value": "must-not-be-stored"}],
        },
        "project": {"id": 11},
        "merge_request": {"iid": 12, "source_project_id": 11, "target_project_id": 11},
        "commit": {"id": "a" * 40},
    }
    return TestClient(create_app(store.settings)), binding, goal, payload


def bind(client, binding, store):
    return client.post(
        "/v1/gitlab/bindings",
        json=binding.model_dump(mode="json"),
        headers={"Authorization": "Bearer " + store.settings.api_token},
    )


def send(client, payload, delivery=None):
    raw = json.dumps(payload).encode()
    return client.post("/v1/gitlab/webhook", content=raw, headers=signed(raw, delivery))


def test_gitlab_without_github_resumes_exact_session(store, gitlab, monkeypatch):
    _, binding, goal, payload = gitlab
    monkeypatch.setenv("LOOM_INTEGRATIONS", "gitlab")
    client = TestClient(create_app(store.settings))
    assert client.post("/v1/github/webhook").status_code == 404
    assert (
        client.post(
            "/v1/gitlab/bindings", json=binding.model_dump(mode="json")
        ).status_code
        == 401
    )
    assert bind(client, binding, store).status_code == 200
    execute_demo(store, goal["id"])
    assert store.inspect(goal["id"])["state"] == "WAITING"
    delivery = uuid4()
    assert send(client, payload, delivery).json()["disposition"] == "accepted"
    assert send(client, payload, delivery).json()["disposition"] == "duplicate"
    execute_demo(store, goal["id"])
    result = store.inspect(goal["id"])
    assert result["state"] == "COMPLETED"
    assert result["session"]["id"] == goal["session"]["id"]
    assert result["metrics"]["wake_ups"] == 1
    assert "must-not-be-stored" not in json.dumps(result, default=str)
    with store.connect() as conn:
        rows = conn.execute(
            "SELECT * FROM integration_deliveries WHERE organization=%s",
            (store.settings.organization,),
        ).fetchall()
        assert "must-not-be-stored" not in json.dumps(rows, default=str)


@pytest.mark.parametrize(
    "path,value",
    [
        (("project", "id"), 99),
        (("merge_request", "iid"), 99),
        (("merge_request", "source_project_id"), 99),
        (("merge_request", "target_project_id"), 99),
        (("object_attributes", "sha"), "b" * 40),
        (("commit", "id"), "b" * 40),
        (("object_attributes", "id"), 80),
        (("object_attributes", "status"), "running"),
        (("object_attributes", "source"), "push"),
        (("project", "id"), True),
    ],
)
def test_wrong_gitlab_evidence_never_wakes(store, gitlab, path, value):
    client, binding, goal, payload = gitlab
    assert bind(client, binding, store).status_code == 200
    execute_demo(store, goal["id"])
    payload[path[0]][path[1]] = value
    assert send(client, payload).json()["disposition"] == "ignored"
    assert store.inspect(goal["id"])["state"] == "WAITING"
    assert len(store.inspect(goal["id"])["attempts"]) == 1


def test_early_delivery_and_binding_commit_gap_reconcile(store, gitlab):
    client, binding, goal, payload = gitlab
    assert send(client, payload).json()["disposition"] == "ignored"
    plugin = GitLabPlugin(store)
    plugin.inbox._bind(binding.goal_id, 1, binding.condition())
    GitLabPlugin(store).reconcile_pending()
    execute_demo(store, goal["id"])
    assert store.inspect(goal["id"])["state"] == "READY"
    execute_demo(store, goal["id"])
    assert store.inspect(goal["id"])["state"] == "COMPLETED"


def test_changed_delivery_bytes_conflict(store, gitlab):
    client, binding, _, payload = gitlab
    assert bind(client, binding, store).status_code == 200
    delivery = uuid4()
    assert send(client, payload, delivery).status_code == 200
    changed = copy.deepcopy(payload)
    changed["object_attributes"]["status"] = "failed"
    assert send(client, changed, delivery).status_code == 409


@pytest.mark.parametrize(
    "change", ["body", "id", "timestamp", "old", "future", "signature", "legacy"]
)
def test_invalid_signature_and_replay_window_rejected(gitlab, change):
    client, _, _, payload = gitlab
    raw = json.dumps(payload).encode()
    headers = signed(raw)
    if change == "body":
        raw += b" "
    elif change == "id":
        headers["webhook-id"] = str(uuid4())
    elif change == "timestamp":
        headers["webhook-timestamp"] = str(int(headers["webhook-timestamp"]) + 1)
    elif change in ("old", "future"):
        headers = signed(
            raw, timestamp=int(time.time()) + (-600 if change == "old" else 600)
        )
    elif change == "signature":
        headers["webhook-signature"] = "v1,bad"
    else:
        headers = {"X-Gitlab-Token": TOKEN}
    assert (
        client.post("/v1/gitlab/webhook", content=raw, headers=headers).status_code
        == 401
    )


def test_rotation_signature_and_malformed_payloads():
    raw = b"{}"
    headers = signed(raw, timestamp=1000)
    headers["webhook-signature"] = "v1,old " + headers["webhook-signature"]
    assert verify_signature(raw, headers, TOKEN, now=1000)
    assert not verify_signature(raw, headers, "not-base64", now=1000)
    for value in (None, [], {}, "text", {"object_kind": "pipeline"}):
        assert normalize(value, "gitlab.com") == (None, {})


def test_plugin_disable_and_fault_isolation(store, monkeypatch):
    monkeypatch.setenv("LOOM_INTEGRATIONS", "")
    assert configured_plugins(store) == ()
    assert (
        TestClient(create_app(store.settings)).post("/v1/gitlab/webhook").status_code
        == 404
    )
    calls = []

    class Broken:
        name = "broken"

        def reconcile_pending(self):
            raise RuntimeError("must not log credentials")

    class Healthy:
        name = "healthy"

        def reconcile_pending(self):
            calls.append(True)

    monkeypatch.setattr(
        "loom.plugins.configured_plugins", lambda _: (Broken(), Healthy())
    )
    reconcile_integrations(store)
    assert calls == [True]


@pytest.mark.parametrize(
    "field,value",
    [("generation", 2), ("instance", "other.example"), ("project_id", 99)],
)
def test_binding_requires_operator_authorized_wait(store, gitlab, field, value):
    client, binding, _, _ = gitlab
    binding = binding.model_copy(update={field: value})
    assert bind(client, binding, store).status_code == 409


def test_missing_config_and_oversize_body(gitlab, monkeypatch):
    client, _, _, _ = gitlab
    assert (
        client.post("/v1/gitlab/webhook", content=b"x" * 1_048_577).status_code == 413
    )
    monkeypatch.delenv("LOOM_GITLAB_SIGNING_TOKEN")
    assert client.post("/v1/gitlab/webhook").status_code == 503


@pytest.mark.parametrize("selection", ["github,github", "unknown", "../../module"])
def test_unknown_plugin_configuration_fails_closed(store, monkeypatch, selection):
    monkeypatch.setenv("LOOM_INTEGRATIONS", selection)
    with pytest.raises(ValueError):
        configured_plugins(store)


def test_concurrent_webhook_and_binding_do_not_lose_evidence(store, gitlab):
    client, binding, goal, payload = gitlab
    with ThreadPoolExecutor(max_workers=2) as pool:
        incoming = pool.submit(send, client, payload)
        binding_result = pool.submit(bind, client, binding, store)
        assert incoming.result().status_code == 200
        assert binding_result.result().status_code == 200
    assert store.inspect(goal["id"])["wait"]["satisfied_at"] is not None


def test_competing_goals_cannot_own_same_external_operation(store, gitlab):
    _, binding, goal, _ = gitlab
    other = store.create(
        GoalCreate(
            title="Other", objective="Same operation", condition=binding.condition()
        )
    )

    def attempt(goal_id):
        try:
            GitLabPlugin(store).inbox.bind(goal_id, 1, binding.condition())
            return "accepted"
        except Conflict:
            return "conflict"

    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(attempt, (goal["id"], other["id"])))
    assert sorted(results) == ["accepted", "conflict"]


def test_organization_and_instance_isolation(store, gitlab, monkeypatch):
    client, binding, goal, payload = gitlab
    assert bind(client, binding, store).status_code == 200
    execute_demo(store, goal["id"])
    other = Store(replace(store.settings, organization=str(uuid4())))
    try:
        outsider = TestClient(create_app(other.settings))
        assert bind(outsider, binding, other).status_code == 404
        assert send(outsider, payload).json()["disposition"] == "ignored"
        monkeypatch.setenv("LOOM_GITLAB_INSTANCE", "private.example")
        different_instance = TestClient(create_app(store.settings))
        assert send(different_instance, payload).json()["disposition"] == "ignored"
        assert store.inspect(goal["id"])["state"] == "WAITING"
        assert send(client, payload).json()["disposition"] == "accepted"
    finally:
        with other.connect() as conn:
            conn.execute(
                "DELETE FROM integration_deliveries WHERE organization=%s",
                (other.settings.organization,),
            )


def test_disabled_plugin_keeps_pending_evidence_for_reenable(
    store, gitlab, monkeypatch
):
    client, binding, goal, payload = gitlab
    assert send(client, payload).json()["disposition"] == "ignored"
    GitLabPlugin(store).inbox._bind(goal["id"], 1, binding.condition())
    monkeypatch.setenv("LOOM_INTEGRATIONS", "")
    reconcile_integrations(store)
    assert store.inspect(goal["id"])["wait"]["satisfied_at"] is None
    monkeypatch.setenv("LOOM_INTEGRATIONS", "gitlab")
    reconcile_integrations(store)
    assert store.inspect(goal["id"])["wait"]["satisfied_at"] is not None
