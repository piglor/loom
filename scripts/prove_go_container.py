"""Non-root Go image, real API proxy, and durable waiting across container restart."""

import os
import re
import subprocess
import tempfile
from uuid import uuid4

import httpx
from loom.cli import load_env, request
from loom.config import Settings
from loom.runtime import execute_demo
from loom.store import Store

from scripts.prove_recovery import eventually, stop


def main():
    load_env()
    os.environ["LOOM_ORGANIZATION"] = "go-container-proof-" + str(uuid4())
    os.environ["LOOM_URL"] = "http://127.0.0.1:18004"
    store = Store(Settings.from_env())
    name = "loom-go-smoke-" + str(uuid4())
    started = False
    with tempfile.TemporaryFile(mode="w+") as log:
        api = subprocess.Popen(
            [
                ".venv/bin/uvicorn",
                "loom.api:create_app",
                "--factory",
                "--host",
                "127.0.0.1",
                "--port",
                "18003",
            ],
            stdout=log,
            stderr=log,
            start_new_session=True,
        )
        try:
            subprocess.run(
                [
                    "docker",
                    "run",
                    "--rm",
                    "-d",
                    "--name",
                    name,
                    "--network",
                    "host",
                    "--read-only",
                    "--cap-drop",
                    "ALL",
                    "--security-opt",
                    "no-new-privileges:true",
                    "--env-file",
                    ".env",
                    "-e",
                    "LOOM_ORGANIZATION",
                    "-e",
                    "LOOM_LISTEN_ADDR=127.0.0.1:18004",
                    "-e",
                    "LOOM_UPSTREAM_URL=http://127.0.0.1:18003",
                    "piglor-loom-console:local",
                ],
                check=True,
                stdout=subprocess.DEVNULL,
            )
            started = True
            eventually(lambda: request("GET", "/readyz"))
            subprocess.run(
                ["docker", "exec", name, "/loom-server", "healthcheck"], check=True
            )
            assert api.poll() is None
            with httpx.Client(base_url=os.environ["LOOM_URL"]) as browser:
                page = browser.get("/")
                assert page.status_code == 200
                assert (
                    "frame-ancestors 'none'" in page.headers["content-security-policy"]
                )
                asset = re.search(r'src="(/assets/[^\"]+\.js)"', page.text)
                assert asset and browser.get(asset[1]).status_code == 200
                assert browser.get("/v1/goals").status_code == 401
                assert browser.get("/.env").status_code == 404
            goal = request(
                "POST",
                "/v1/goals",
                {
                    "title": "Go container wait proof",
                    "objective": "Remain stopped across restart",
                    "condition": {
                        "source": "deployment",
                        "type": "ready",
                        "resource": str(uuid4()),
                        "version": "B",
                    },
                },
            )
            execute_demo(store, goal["id"])
            waiting = request("GET", f"/v1/goals/{goal['id']}")
            assert waiting["state"] == "WAITING"
            subprocess.run(
                ["docker", "restart", name], check=True, stdout=subprocess.DEVNULL
            )
            eventually(lambda: request("GET", "/readyz"))
            restored = request("GET", f"/v1/goals/{goal['id']}")
            assert restored["attempts"] == waiting["attempts"]
            event = {
                **goal["wait"]["condition"],
                "goal_id": goal["id"],
                "generation": 1,
                "delivery_id": str(uuid4()),
            }
            assert request("POST", "/v1/events", event)["disposition"] == "accepted"
            assert request("POST", "/v1/events", event)["disposition"] == "duplicate"
            execute_demo(store, goal["id"])
            final = request("GET", f"/v1/goals/{goal['id']}")
            assert (
                final["state"] == "COMPLETED" and final["session"] == waiting["session"]
            )
            user = subprocess.check_output(
                ["docker", "inspect", name, "--format", "{{.Config.User}}"], text=True
            ).strip()
            assert user == "65532:65532"
            print(
                "PASS: non-root/read-only Go container, built assets/CSP, auth, "
                "mutation forwarding, stopped wait across restart, correlated resume"
            )
        finally:
            if started:
                subprocess.run(
                    ["docker", "stop", name], check=True, stdout=subprocess.DEVNULL
                )
            stop(api)


if __name__ == "__main__":
    main()
