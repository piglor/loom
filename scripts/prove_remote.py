"""Real HTTP/Rust/PostgreSQL affinity and restart proof, without any model."""

import json
import os
import subprocess
import tempfile
from pathlib import Path
from uuid import uuid4

from loom.cli import load_env, request
from loom.config import Settings
from loom.mailbox import Mailbox
from loom.store import Store

from scripts.prove_recovery import eventually, stop


def main():
    load_env()
    os.environ["LOOM_ORGANIZATION"] = "remote-proof-" + str(uuid4())
    os.environ["LOOM_URL"] = "http://127.0.0.1:18000"
    store = Store(Settings.from_env())
    store.migrate()
    box = Mailbox(store)
    processes = []
    with tempfile.TemporaryDirectory(prefix="loom-remote-proof-") as directory:
        root = Path(directory)
        with (root / "process.log").open("w") as log:

            def start(command):
                process = subprocess.Popen(
                    command, stdout=log, stderr=log, start_new_session=True
                )
                processes.append(process)
                return process

            def agent(worker):
                return start(
                    [
                        "target/debug/loom-agent",
                        "--config",
                        str(root / f"{worker['worker_id']}.json"),
                    ]
                )

            try:
                api = start(
                    [
                        ".venv/bin/uvicorn",
                        "loom.api:create_app",
                        "--factory",
                        "--host",
                        "127.0.0.1",
                        "--port",
                        "18000",
                    ]
                )
                eventually(lambda: request("GET", "/readyz"))
                assert api.poll() is None
                workers = [
                    request("POST", "/v1/workers", {"workspace_ref": "proof"})
                    for _ in range(2)
                ]
                for worker in workers:
                    path = root / f"{worker['worker_id']}.json"
                    with path.open("x") as output:
                        path.chmod(0o600)
                        json.dump(
                            {
                                "server_url": os.environ["LOOM_URL"],
                                "token": worker["token"],
                                "worker_id": worker["worker_id"],
                                "workspace_ref": "proof",
                                "state_dir": str(root / worker["worker_id"]),
                                "allow_insecure_localhost": True,
                            },
                            output,
                        )
                goal = request(
                    "POST",
                    "/v1/goals",
                    {
                        "title": "Remote affinity proof",
                        "objective": "Only the bound Rust worker may execute",
                        "runtime": "remote-demo",
                        "worker_id": workers[0]["worker_id"],
                        "condition": {
                            "source": "proof",
                            "type": "ready",
                            "resource": str(uuid4()),
                            "version": "1",
                        },
                    },
                )
                box.dispatch(goal["id"])
                wrong = agent(workers[1])
                assert not box.poll(workers[1]["token"])["commands"]
                right = agent(workers[0])

                def stopped_wait():
                    current = store.inspect(goal["id"])
                    return (
                        current
                        if current["state"] == "WAITING"
                        and current["attempts"][0]["state"] == "STOPPED"
                        else None
                    )

                first = eventually(stopped_wait)
                stop(right)
                request(
                    "POST",
                    "/v1/events",
                    {
                        "delivery_id": str(uuid4()),
                        "goal_id": goal["id"],
                        "generation": 1,
                        **goal["wait"]["condition"],
                    },
                )
                box.dispatch(goal["id"])
                pending = store.inspect(goal["id"])
                assert (
                    pending["state"] == "WAITING"
                    and pending["waiting_reason"] == "worker"
                )
                assert pending["metrics"]["wake_ups"] == 0
                assert not box.poll(workers[1]["token"])["commands"]
                right = agent(workers[0])

                def completed():
                    current = store.inspect(goal["id"])
                    return current if current["state"] == "COMPLETED" else None

                final = eventually(completed)
                assert final["session"]["id"] == first["session"]["id"]
                assert len(final["attempts"]) == 2
                assert final["metrics"]["wake_ups"] == 1
                assert right.poll() is None and wrong.poll() is None
                print(
                    "PASS: Rust worker stopped, disconnected, recovered its journal "
                    "and resumed the same bound Session"
                )
                print(
                    "PASS: second worker never received the bound command; "
                    "two stopped attempts, one wake"
                )
            finally:
                for process in reversed(processes):
                    stop(process)


if __name__ == "__main__":
    main()
