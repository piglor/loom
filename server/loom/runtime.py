"""Finite demonstration runtime. No model or arbitrary commands are invoked."""

import hashlib
import json
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from uuid import UUID

import psycopg

from loom.store import Conflict, Store


def receipt_directory(store):
    organization = hashlib.sha256(store.settings.organization.encode()).hexdigest()
    path = Path(os.environ.get("LOOM_STATE_DIR", ".loom")) / "receipts" / organization
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    return path


def flush_receipts(store):
    for path in receipt_directory(store).glob("*.json"):
        try:
            report = json.loads(path.read_text())
        except FileNotFoundError:
            continue
        attempt = report["attempt"]
        for key in ("id", "goal_id", "session_id"):
            attempt[key] = UUID(attempt[key])
        store.finish(
            attempt,
            report["duration_ms"],
            report["success"],
            **({"outcome": report["outcome"]} if "outcome" in report else {}),
        )
        path.unlink(missing_ok=True)


def execute_demo(store: Store, goal_id, *, next_condition=None):
    goal = store.inspect(goal_id)
    if goal["session"]["runtime"] != "demo":
        from loom.mailbox import Mailbox

        Mailbox(store).dispatch(goal_id)
        return
    if next_condition is not None and (
        goal["phase"] == 0
        or goal["run"]["policy"].get("lifecycle") != "event-driven-v1"
    ):
        raise Conflict("Next wait requires an event-driven continuation")
    flush_receipts(store)
    attempt = store.claim(goal_id)
    if attempt is None:
        return
    outcome = {}
    if goal["run"]["policy"].get("lifecycle") == "event-driven-v1":
        # The finite demo can prove stop/recovery but cannot invent new work.
        # A continuation without a prepared wait proposes completion; policy
        # rejects it if the final authorized condition has not been observed.
        outcome["outcome"] = "yield" if attempt["phase"] == 0 else "complete"
    started = time.monotonic()
    try:
        if next_condition is not None:
            store.prepare_wait(attempt, next_condition, goal["wait"]["generation"])
            outcome["outcome"] = "yield"
        # The subprocess has no external input, network, background jobs or
        # model access. Its exit is the stop evidence for this demo adapter.
        result = subprocess.run(
            [sys.executable, "-I", "-c", "print('loom-demo-finite-attempt')"],
            capture_output=True,
            timeout=5,
            check=True,
        )
        success = result.stdout.strip() == b"loom-demo-finite-attempt"
    except (OSError, subprocess.SubprocessError, psycopg.Error, Conflict):
        # Preparation can fail after admission, including an ambiguous commit.
        # No subprocess has started in that case; retain a stopped failure
        # receipt instead of leaving the Goal permanently RUNNING.
        success = False
    duration_ms = (time.monotonic() - started) * 1000
    directory = receipt_directory(store)
    report = {
        "attempt": {
            k: attempt[k] for k in ("id", "goal_id", "session_id", "worker_id", "phase")
        },
        "duration_ms": duration_ms,
        "success": success,
        **outcome,
    }
    destination = directory / f"{attempt['id']}.json"
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="w", dir=directory, delete=False
        ) as output:
            temporary = Path(output.name)
            json.dump(report, output, default=str)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, destination)
        descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
    except OSError:
        # We still hold stop evidence in memory: report it directly if the
        # journal disk fails. Never leave a healthy database claiming RUNNING.
        store.finish(attempt, duration_ms, success, **outcome)
        if temporary is not None:
            temporary.unlink(missing_ok=True)
        destination.unlink(missing_ok=True)
        return
    # If the database is temporarily unavailable the durable receipt remains;
    # a retry reports it again without invoking the subprocess again.
    store.finish(attempt, duration_ms, success, **outcome)
    destination.unlink(missing_ok=True)
