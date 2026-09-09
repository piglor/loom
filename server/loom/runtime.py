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

from loom.store import Store


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
        store.finish(attempt, report["duration_ms"], report["success"])
        path.unlink(missing_ok=True)


def execute_demo(store: Store, goal_id):
    if store.inspect(goal_id)["session"]["runtime"] != "demo":
        from loom.mailbox import Mailbox

        Mailbox(store).dispatch(goal_id)
        return
    flush_receipts(store)
    attempt = store.claim(goal_id)
    if attempt is None:
        return
    started = time.monotonic()
    try:
        # The subprocess has no external input, network, background jobs or
        # model access. Its exit is the stop evidence for this demo adapter.
        result = subprocess.run(
            [sys.executable, "-I", "-c", "print('loom-demo-finite-attempt')"],
            capture_output=True,
            timeout=5,
            check=True,
        )
        success = result.stdout.strip() == b"loom-demo-finite-attempt"
    except (OSError, subprocess.SubprocessError):
        success = False
    duration_ms = (time.monotonic() - started) * 1000
    directory = receipt_directory(store)
    report = {
        "attempt": {
            k: attempt[k] for k in ("id", "goal_id", "session_id", "worker_id", "phase")
        },
        "duration_ms": duration_ms,
        "success": success,
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
        store.finish(attempt, duration_ms, success)
        if temporary is not None:
            temporary.unlink(missing_ok=True)
        destination.unlink(missing_ok=True)
        return
    # If the database is temporarily unavailable the durable receipt remains;
    # a retry reports it again without invoking the subprocess again.
    store.finish(attempt, duration_ms, success)
    destination.unlink(missing_ok=True)
