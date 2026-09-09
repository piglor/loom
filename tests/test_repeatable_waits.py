from uuid import uuid4

import pytest
from loom.models import Condition, EventCreate, GoalCreate
from loom.store import Conflict, Store


def condition(version):
    return Condition(
        source="deployment", type="health.ready", resource="deploy-1", version=version
    )


def event(goal, wait, **changes):
    return EventCreate(
        **{
            "goal_id": goal["id"],
            "delivery_id": str(uuid4()),
            "generation": wait["generation"],
            **wait["condition"],
            **changes,
        }
    )


def test_repeated_waits_keep_context_and_require_completion_evidence(store):
    goal = store.create(
        GoalCreate(
            title="Deploy safely",
            objective="Observe final health",
            condition=condition("1"),
        ),
        completion_condition=condition("3"),
    )
    first = store.claim(goal["id"])
    store.finish(first, 10, outcome="yield")
    for generation in (1, 2, 3):
        store = Store(store.settings)
        waiting = store.inspect(goal["id"])
        assert waiting["state"] == "WAITING"
        assert waiting["wait"]["generation"] == generation
        assert store.claim(goal["id"]) is None
        assert waiting["session"]["id"] == goal["session"]["id"]
        assert all(a["state"] == "STOPPED" for a in waiting["attempts"])
        incoming = event(goal, waiting["wait"])
        assert store.receive(incoming)["disposition"] == "accepted"
        assert store.receive(incoming)["disposition"] == "duplicate"
        attempt = store.claim(goal["id"])
        if generation < 3:
            prepared = store.prepare_wait(
                attempt, condition(str(generation + 1)), generation
            )
            assert prepared["armed_at"] is None
            assert store.inspect(goal["id"])["state"] == "RUNNING"
            store.finish(attempt, 10, outcome="yield")
        else:
            store.finish(attempt, 10, outcome="complete")
    final = store.inspect(goal["id"])
    assert final["state"] == "COMPLETED"
    assert final["metrics"]["wake_ups"] == 3
    assert len(final["attempts"]) == 4
    assert len(final["wait_history"]) == 2
    assert final["metrics"]["execution_seconds"] == pytest.approx(0.04)


def repeatable(store, *, max_attempts=100):
    return store.create(
        GoalCreate(
            title="Repeat", objective="Wait for final health", condition=condition("1")
        ),
        completion_condition=condition("3"),
        max_attempts=max_attempts,
    )


def resume_first(store, goal):
    store.finish(store.claim(goal["id"]), 1, outcome="yield")
    store.receive(event(goal, goal["wait"]))
    return store.claim(goal["id"])


def test_early_event_cannot_start_execution_before_stop(store):
    goal = repeatable(store)
    attempt = resume_first(store, goal)
    prepared = store.prepare_wait(attempt, condition("2"), 1)
    assert store.prepare_wait(attempt, condition("2"), 1)["id"] == prepared["id"]
    with pytest.raises(Conflict):
        store.prepare_wait(attempt, condition("3"), 1)
    assert store.receive(event(goal, prepared))["disposition"] == "accepted"
    assert store.inspect(goal["id"])["state"] == "RUNNING"
    assert store.claim(goal["id"]) is None
    store.finish(attempt, 1, outcome="yield")
    store.finish(attempt, 1, outcome="yield")
    with pytest.raises(Conflict):
        store.finish(attempt, 2, outcome="yield")
    with pytest.raises(Conflict):
        store.finish(attempt, 1, outcome="complete")
    assert store.inspect(goal["id"])["state"] == "READY"
    assert len(store.inspect(goal["id"])["attempts"]) == 2
    assert store.claim(goal["id"])["phase"] == 2


@pytest.mark.parametrize(
    "outcome,reason",
    [
        ("complete", "completion_policy_rejected"),
        ("yield", "missing_next_wait"),
        ("blocked", "agent_blocked"),
    ],
)
def test_unsatisfied_completion_and_missing_dependencies_preserve_stop(
    store, outcome, reason
):
    goal = repeatable(store)
    attempt = resume_first(store, goal)
    store.finish(attempt, 1, outcome=outcome)
    result = store.inspect(goal["id"])
    assert result["state"] == "BLOCKED"
    assert result["attempts"][-1]["state"] == "STOPPED"
    assert result["audit"][-1]["details"]["reason"] == reason
    assert store.claim(goal["id"]) is None


@pytest.mark.parametrize(
    "change", [{"generation": 1}, {"version": "1"}, {"generation": 3}]
)
def test_superseded_or_future_events_do_not_resume_current_wait(store, change):
    goal = repeatable(store)
    attempt = resume_first(store, goal)
    prepared = store.prepare_wait(attempt, condition("2"), 1)
    store.finish(attempt, 1, outcome="yield")
    assert store.receive(event(goal, prepared, **change))["disposition"] == "mismatch"
    assert store.inspect(goal["id"])["state"] == "WAITING"
    assert store.claim(goal["id"]) is None


@pytest.mark.parametrize(
    "field,value",
    [("worker_id", "wrong"), ("session_id", uuid4()), ("id", uuid4()), ("phase", 4)],
)
def test_wait_preparation_requires_exact_admitted_attempt(store, field, value):
    goal = repeatable(store)
    attempt = resume_first(store, goal)
    with pytest.raises(Conflict):
        store.prepare_wait({**attempt, field: value}, condition("2"), 1)
    assert store.inspect(goal["id"])["wait"]["generation"] == 1


def test_attempt_budget_never_admits_extra_execution(store):
    goal = repeatable(store, max_attempts=2)
    attempt = resume_first(store, goal)
    prepared = store.prepare_wait(attempt, condition("2"), 1)
    store.finish(attempt, 1, outcome="yield")
    store.receive(event(goal, prepared))
    assert store.claim(goal["id"]) is None
    result = store.inspect(goal["id"])
    assert result["state"] == "BLOCKED"
    assert len(result["attempts"]) == 2
    assert result["audit"][-1]["action"] == "attempt_budget_exhausted"


def test_repeatable_demo_receipt_recovery_and_zero_wait_execution(store, monkeypatch):
    import psycopg
    from loom.runtime import execute_demo, flush_receipts

    goal = repeatable(store)
    execute_demo(store, goal["id"])
    store.receive(event(goal, goal["wait"]))
    with monkeypatch.context() as scoped:

        def unavailable(*args, **kwargs):
            raise psycopg.OperationalError("temporary outage")

        scoped.setattr(store, "finish", unavailable)
        with pytest.raises(psycopg.OperationalError):
            execute_demo(store, goal["id"], next_condition=condition("2"))
    restarted = Store(store.settings)

    def unexpected(*args, **kwargs):
        pytest.fail("Runtime must remain stopped while waiting or replaying receipts")

    with monkeypatch.context() as scoped:
        scoped.setattr("loom.runtime.subprocess.run", unexpected)
        flush_receipts(restarted)
        restarted.recover_uncertain()
        for _ in range(10):
            execute_demo(restarted, goal["id"])
    result = restarted.inspect(goal["id"])
    assert result["state"] == "WAITING"
    assert result["wait"]["generation"] == 2
    assert len(result["attempts"]) == 2
    assert all(a["state"] == "STOPPED" for a in result["attempts"])


def test_stale_outbox_ack_cannot_erase_new_wake(store):
    goal = repeatable(store)
    attempt = resume_first(store, goal)
    old_item = next(item for item in store.outbox_batch() if item["kind"] == "wake")
    prepared = store.prepare_wait(attempt, condition("2"), 1)
    store.finish(attempt, 1, outcome="yield")
    store.receive(event(goal, prepared))
    store.delivered(old_item)
    current = next(item for item in store.outbox_batch() if item["kind"] == "wake")
    assert current["delivered_at"] is None
    assert current["wait_id"] == prepared["id"]


def test_invalid_first_wait_does_not_admit_runtime(store):
    from loom.runtime import execute_demo

    goal = repeatable(store)
    with pytest.raises(Conflict):
        execute_demo(store, goal["id"], next_condition=condition("2"))
    assert store.inspect(goal["id"])["attempts"] == []


def test_failed_wait_preparation_stops_without_launch(store, monkeypatch):
    import psycopg
    from loom.runtime import execute_demo

    goal = repeatable(store)
    execute_demo(store, goal["id"])
    store.receive(event(goal, goal["wait"]))

    def failure(*args, **kwargs):
        raise psycopg.OperationalError("preparation unavailable")

    def unexpected(*args, **kwargs):
        pytest.fail("Cannot launch after failed wait preparation")

    monkeypatch.setattr(store, "prepare_wait", failure)
    monkeypatch.setattr("loom.runtime.subprocess.run", unexpected)
    execute_demo(store, goal["id"], next_condition=condition("2"))
    result = store.inspect(goal["id"])
    assert result["state"] == "FAILED"
    assert result["attempts"][-1]["state"] == "STOPPED"


def test_unknown_execution_requires_authoritative_stop_before_resume(store):
    goal = repeatable(store)
    attempt = resume_first(store, goal)
    prepared = store.prepare_wait(attempt, condition("2"), 1)
    store = Store(store.settings)
    store.recover_uncertain()
    result = store.inspect(goal["id"])
    assert result["state"] == "BLOCKED"
    assert result["attempts"][-1]["state"] == "UNKNOWN"
    assert result["wait"]["armed_at"] is None
    assert store.claim(goal["id"]) is None
    with pytest.raises(Conflict):
        store.prepare_wait(attempt, condition("2"), 1)
    with pytest.raises(Conflict):
        store.cancel(goal["id"])
    # An authentic late stop receipt resolves uncertainty, not a fresh launch.
    store.finish(attempt, 1, outcome="yield")
    assert store.inspect(goal["id"])["state"] == "WAITING"
    store.receive(event(goal, prepared))
    assert store.claim(goal["id"])["phase"] == 2
