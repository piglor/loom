import hashlib
import hmac
import json
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from loom.api import create_app
from loom.github import GitHubBinding
from loom.gitlab import GitLabBinding
from loom.models import GoalCreate
from loom.runtime import execute_demo

from tests.test_gitlab import TOKEN, signed


def external(provider, generation, goal_id):
    sha = ("a" if generation == 1 else "b") * 40
    if provider == "github":
        binding = GitHubBinding(
            goal_id=goal_id,
            generation=generation,
            installation_id=7,
            repository_id=11,
            pull_request=12,
            head_sha=sha,
            run_id=80 + generation,
            run_attempt=1,
            workflow_id=4,
        )
        payload = {
            "action": "completed",
            "installation": {"id": 7},
            "repository": {"id": 11},
            "workflow_run": {
                "id": binding.run_id,
                "run_attempt": 1,
                "workflow_id": 4,
                "head_sha": sha,
                "status": "completed",
                "conclusion": "success",
                "event": "pull_request",
                "head_repository": {"id": 11},
                "pull_requests": [
                    {
                        "number": 12,
                        "head": {"sha": sha, "repo": {"id": 11}},
                        "base": {"repo": {"id": 11}},
                    }
                ],
            },
        }
    else:
        binding = GitLabBinding(
            goal_id=goal_id,
            generation=generation,
            instance="gitlab.com",
            project_id=11,
            merge_request=12,
            pipeline_id=80 + generation,
            head_sha=sha,
        )
        payload = {
            "object_kind": "pipeline",
            "object_attributes": {
                "id": binding.pipeline_id,
                "sha": sha,
                "source": "merge_request_event",
                "status": "success",
            },
            "project": {"id": 11},
            "merge_request": {
                "iid": 12,
                "source_project_id": 11,
                "target_project_id": 11,
            },
            "commit": {"id": sha},
        }
    return binding, payload


def deliver(client, provider, payload):
    raw = json.dumps(payload).encode()
    if provider == "github":
        headers = {
            "X-GitHub-Delivery": str(uuid4()),
            "X-GitHub-Event": "workflow_run",
            "X-Hub-Signature-256": "sha256="
            + hmac.new(b"g" * 32, raw, hashlib.sha256).hexdigest(),
        }
    else:
        headers = signed(raw)
    return client.post(f"/v1/{provider}/webhook", content=raw, headers=headers).json()[
        "disposition"
    ]


@pytest.mark.parametrize(
    "providers",
    [
        ("github", "github"),
        ("gitlab", "gitlab"),
        ("github", "gitlab"),
        ("gitlab", "github"),
    ],
)
def test_repeated_waits_and_cross_plugin_continuity(store, monkeypatch, providers):
    monkeypatch.setenv("LOOM_GITHUB_WEBHOOK_SECRET", "g" * 32)
    monkeypatch.setenv("LOOM_GITLAB_SIGNING_TOKEN", TOKEN)
    monkeypatch.setenv("LOOM_GITLAB_INSTANCE", "gitlab.com")
    monkeypatch.setenv("LOOM_INTEGRATIONS", "github,gitlab")
    first, payload1 = external(providers[0], 1, uuid4())
    second, payload2 = external(providers[1], 2, uuid4())
    goal = store.create(
        GoalCreate(
            title="Plugin independence",
            objective="Wait across integrations",
            condition=first.condition(),
        ),
        completion_condition=second.condition(),
    )
    first.goal_id = second.goal_id = goal["id"]
    client = TestClient(create_app(store.settings))
    auth = {"Authorization": "Bearer " + store.settings.api_token}
    assert (
        client.post(
            f"/v1/{providers[0]}/bindings",
            headers=auth,
            json=first.model_dump(mode="json"),
        ).status_code
        == 200
    )
    execute_demo(store, goal["id"])
    assert deliver(client, providers[0], payload1) == "accepted"
    execute_demo(store, goal["id"], next_condition=second.condition())
    assert store.inspect(goal["id"])["state"] == "WAITING"
    # The second event precedes binding; plugin reconciliation must find it.
    assert deliver(client, providers[1], payload2) == "ignored"
    assert (
        client.post(
            f"/v1/{providers[1]}/bindings",
            headers=auth,
            json=second.model_dump(mode="json"),
        ).status_code
        == 200
    )
    assert store.inspect(goal["id"])["state"] == "READY"
    assert deliver(client, providers[0], payload1) == "mismatch"
    execute_demo(store, goal["id"])
    final = store.inspect(goal["id"])
    assert final["state"] == "COMPLETED"
    assert final["session"]["id"] == goal["session"]["id"]
    assert len(final["attempts"]) == 3
    assert len(final["wait_history"]) == 1
    assert final["metrics"]["wake_ups"] == 2
