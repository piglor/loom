"""Real Hatchet/PostgreSQL proof. Run against a dedicated local test stack."""

import argparse
import json
import os
import signal
import subprocess
import time
from pathlib import Path
from uuid import uuid4

from loom.cli import load_env, request
from loom.config import Settings
from loom.store import Store


def failure_summary(log_text, processes):
    """Expose diagnostic categories, never raw SDK logs or credentials."""
    signals = (
        "connection refused",
        "permission denied",
        "address already in use",
        "unauthorized",
        "unavailable",
        "timed out",
        "worker started",
    )
    return {
        "process_exit_codes": [process.poll() for process in processes],
        "exception_types": [
            name
            for name in (
                "ConnectionRefusedError",
                "HTTPError",
                "ImportError",
                "ModuleNotFoundError",
                "OperationalError",
                "PermissionError",
                "RuntimeError",
                "TimeoutError",
                "ValidationError",
                "ValueError",
            )
            if name in log_text
        ],
        "signals": [signal for signal in signals if signal in log_text.lower()],
    }


def eventually(check, timeout=90):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            value = check()
            if value:
                return value
        except (OSError, ValueError):
            pass
        time.sleep(0.5)
    raise AssertionError("Timed out waiting for the expected state")


def stop(process):
    if process.poll() is not None:
        return
    os.killpg(process.pid, signal.SIGINT)
    try:
        process.wait(timeout=12)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", default="loom-local")
    parser.add_argument("--restart-container", required=True)
    parser.add_argument("--suspend-seconds", type=int, default=15)
    args = parser.parse_args()
    # Only an explicitly identified development container is restartable here.
    labels = json.loads(
        subprocess.check_output(
            [
                "docker",
                "inspect",
                args.restart_container,
                "--format",
                "{{json .Config.Labels}}",
            ]
        )
    )
    if labels.get("com.docker.compose.project") != "loom-dev":
        raise SystemExit("Refusing to restart a container outside the loom-dev project")
    if labels.get("com.docker.compose.service") != "hatchet":
        raise SystemExit("Expected the loom-dev Hatchet service")
    load_env()
    os.environ["LOOM_ORGANIZATION"] = "proof-" + str(uuid4())
    os.environ["HATCHET_CLI_TELEMETRY_ENABLED"] = "false"
    store = Store(Settings.from_env())
    store.migrate()
    state = Path(".loom")
    state.mkdir(exist_ok=True, mode=0o700)
    processes = []

    with (state / "proof.log").open("w") as log:

        def start(command):
            process = subprocess.Popen(
                command, stdout=log, stderr=log, start_new_session=True
            )
            processes.append(process)
            return process

        def start_worker():
            return start(
                ["hatchet", "worker", "dev", "-p", args.profile, "--no-reload"]
            )

        try:
            api = start([".venv/bin/loom", "serve"])
            eventually(lambda: request("GET", "/readyz"))
            assert api.poll() is None, "Proof API could not bind its port"
            worker = start_worker()
            goal = request(
                "POST",
                "/v1/goals",
                {
                    "title": "Durable restart proof",
                    "objective": "Stop, survive a restart, then resume exactly once",
                    "condition": {
                        "source": "proof",
                        "type": "job.completed",
                        "resource": str(uuid4()),
                        "version": "B",
                    },
                },
            )
            goal_id = goal["id"]

            def state_is(expected):
                current = request("GET", f"/v1/goals/{goal_id}")
                return current if current["state"] == expected else None

            waiting = eventually(lambda: state_is("WAITING"))
            assert len(waiting["attempts"]) == 1
            assert waiting["attempts"][0]["state"] == "STOPPED"
            print("PASS: finite runtime exited; Goal is WAITING", flush=True)
            time.sleep(args.suspend_seconds)
            unchanged = state_is("WAITING")
            assert unchanged["attempts"] == waiting["attempts"]
            print("PASS: suspension created no additional runtime attempt", flush=True)
            stop(worker)
            stop(api)
            subprocess.run(
                ["docker", "restart", args.restart_container], check=True, stdout=log
            )
            assert store.inspect(goal_id)["state"] == "WAITING"
            api = start([".venv/bin/loom", "serve"])
            eventually(lambda: request("GET", "/readyz"))
            event = {
                **goal["wait"]["condition"],
                "goal_id": goal_id,
                "generation": 1,
                "delivery_id": str(uuid4()),
            }
            # Persist while the orchestration worker is absent.
            stale = {**event, "version": "A", "delivery_id": str(uuid4())}
            assert request("POST", "/v1/events", stale)["disposition"] == "mismatch"
            assert request("POST", "/v1/events", event)["disposition"] == "accepted"
            assert request("POST", "/v1/events", event)["disposition"] == "duplicate"
            worker = start_worker()
            completed = eventually(lambda: state_is("COMPLETED"), timeout=120)
            assert len(completed["attempts"]) == 2
            assert completed["session"]["id"] == waiting["session"]["id"]
            assert completed["session"]["worker_id"] == waiting["session"]["worker_id"]
            assert all(x["state"] == "STOPPED" for x in completed["attempts"])
            assert completed["metrics"]["wake_ups"] == 1
            print(
                "PASS: API + worker + engine restart; same Session, one continuation",
                flush=True,
            )
            print(
                json.dumps(
                    {"goal_id": goal_id, "metrics": completed["metrics"]}, indent=2
                )
            )
        except Exception:
            log.flush()
            print(
                "Proof failure diagnostics: "
                + json.dumps(
                    failure_summary(
                        (state / "proof.log").read_text(errors="replace"), processes
                    )
                ),
                flush=True,
            )
            raise
        finally:
            for process in reversed(processes):
                stop(process)


if __name__ == "__main__":
    main()
