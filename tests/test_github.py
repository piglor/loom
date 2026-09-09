import copy
import hashlib
import hmac
import json
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from loom.api import create_app
from loom.github import GitHubBinding, GitHubIngress, matches, verify
from loom.models import GoalCreate
from loom.runtime import execute_demo


@pytest.fixture
def github(store):
    binding = GitHubBinding(
        goal_id=uuid4(),
        installation_id=7,
        repository_id=11,
        pull_request=12,
        head_sha="a" * 40,
        run_id=81,
        run_attempt=2,
        workflow_id=4,
    )
    goal = store.create(
        GoalCreate(
            title="Signed CI proof",
            objective="Wait for authorized CI",
            condition=binding.condition(),
        )
    )
    binding.goal_id = goal["id"]
    GitHubIngress(store).bind(binding)
    payload = {
        "action": "completed",
        "installation": {"id": 7},
        "repository": {"id": 11},
        "workflow_run": {
            "id": 81,
            "run_attempt": 2,
            "workflow_id": 4,
            "head_sha": "a" * 40,
            "status": "completed",
            "conclusion": "failure",
            "event": "pull_request",
            "head_repository": {"id": 11},
            "pull_requests": [
                {
                    "number": 12,
                    "head": {"sha": "a" * 40, "repo": {"id": 11}},
                    "base": {"repo": {"id": 11}},
                }
            ],
        },
    }
    return binding, goal, payload


def test_official_signature_vector():
    assert verify(
        b"Hello, World!",
        "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17",
        "It's a Secret to Everybody",
    )


def test_delivery_before_binding_is_reconciled(store, github):
    binding, goal, payload = github
    with store.connect() as conn:
        conn.execute("DELETE FROM github_bindings WHERE goal_id=%s", (goal["id"],))
    ingress = GitHubIngress(store)
    delivery = uuid4()
    raw = json.dumps(payload).encode()
    assert ingress.receive(raw, delivery, "workflow_run")["disposition"] == "ignored"
    ingress.bind(binding)
    assert store.inspect(goal["id"])["wait"]["satisfied_at"] is not None
    assert ingress.receive(raw, delivery, "workflow_run")["disposition"] == "duplicate"
    audit = store.inspect(goal["id"])["audit"]
    assert any(x["details"].get("conclusion") == "failure" for x in audit)


def test_binding_commit_reconciliation_gap_recovers(store, github):
    binding, goal, payload = github
    with store.connect() as conn:
        conn.execute("DELETE FROM github_bindings WHERE goal_id=%s", (goal["id"],))
    ingress = GitHubIngress(store)
    ingress.receive(json.dumps(payload).encode(), uuid4(), "workflow_run")
    ingress._bind(binding)  # Crash after commit, before immediate reconciliation.
    GitHubIngress(store).reconcile_pending()
    assert store.inspect(goal["id"])["wait"]["satisfied_at"] is not None


def test_signed_delivery_atomic_duplicate_and_invalid_signature(
    store, github, monkeypatch
):
    binding, goal, payload = github
    execute_demo(store, goal["id"])
    secret = "test-only-" + "x" * 32
    monkeypatch.setenv("LOOM_GITHUB_WEBHOOK_SECRET", secret)
    client = TestClient(create_app(store.settings))
    raw = json.dumps(payload).encode()
    headers = {
        "X-GitHub-Delivery": str(uuid4()),
        "X-GitHub-Event": "workflow_run",
        "X-Hub-Signature-256": "sha256="
        + hmac.new(secret.encode(), raw, hashlib.sha256).hexdigest(),
    }
    assert (
        client.post(
            "/v1/github/webhook", content=raw + b" ", headers=headers
        ).status_code
        == 401
    )
    assert store.inspect(goal["id"])["state"] == "WAITING"
    assert (
        client.post("/v1/github/webhook", content=raw, headers=headers).json()[
            "disposition"
        ]
        == "accepted"
    )
    assert (
        client.post("/v1/github/webhook", content=raw, headers=headers).json()[
            "disposition"
        ]
        == "duplicate"
    )
    assert store.inspect(goal["id"])["state"] == "READY"
    headers["X-Hub-Signature-256"] = (
        "sha256=" + hmac.new(secret.encode(), raw + b" ", hashlib.sha256).hexdigest()
    )
    assert (
        client.post(
            "/v1/github/webhook", content=raw + b" ", headers=headers
        ).status_code
        == 409
    )


@pytest.mark.parametrize(
    "path,value",
    [
        (("repository", "id"), 99),
        (("installation", "id"), 99),
        (("workflow_run", "head_sha"), "b" * 40),
        (("workflow_run", "id"), 80),
        (("workflow_run", "run_attempt"), 1),
        (("workflow_run", "workflow_id"), 8),
        (("workflow_run", "head_repository", "id"), 99),
        (("workflow_run", "pull_requests", 0, "number"), 13),
        (("workflow_run", "pull_requests", 0, "head", "repo", "id"), 99),
        (("workflow_run", "pull_requests"), []),
        (("workflow_run", "event"), "pull_request_target"),
    ],
)
def test_untrusted_stale_or_unrelated_never_wakes(store, github, path, value):
    binding, goal, original = github
    execute_demo(store, goal["id"])
    payload = copy.deepcopy(original)
    target = payload
    for key in path[:-1]:
        target = target[key]
    target[path[-1]] = value
    assert not matches(binding, payload)
    assert (
        GitHubIngress(store).receive(
            json.dumps(payload).encode(), uuid4(), "workflow_run"
        )["disposition"]
        == "ignored"
    )
    assert store.inspect(goal["id"])["state"] == "WAITING"


def test_webhook_disabled_without_secret_and_size_limit(store, monkeypatch):
    monkeypatch.delenv("LOOM_GITHUB_WEBHOOK_SECRET", raising=False)
    client = TestClient(create_app(store.settings))
    assert client.post("/v1/github/webhook", content=b"{}").status_code == 503
    monkeypatch.setenv("LOOM_GITHUB_WEBHOOK_SECRET", "x" * 32)
    assert (
        client.post("/v1/github/webhook", content=b"x" * 1_048_577).status_code == 413
    )
