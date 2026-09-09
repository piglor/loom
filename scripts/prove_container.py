"""Smoke-test the non-root/read-only image against local development PostgreSQL."""

import os
import subprocess
from uuid import uuid4

from loom.cli import load_env, request

from scripts.prove_recovery import eventually


def main():
    load_env()
    os.environ["LOOM_URL"] = "http://127.0.0.1:18001"
    name = "loom-smoke-" + str(uuid4())
    started = False
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
                "--tmpfs",
                "/tmp",
                "--env-file",
                ".env",
                "-e",
                "LOOM_PORT=18001",
                "-e",
                "LOOM_BIND_HOST=127.0.0.1",
                "piglor-loom:local",
            ],
            check=True,
            stdout=subprocess.DEVNULL,
        )
        started = True
        eventually(lambda: request("GET", "/readyz"))
        user = subprocess.check_output(
            ["docker", "inspect", name, "--format", "{{.Config.User}}"], text=True
        ).strip()
        assert user == "10001:10001"
        print(
            "PASS: container serves authenticated readiness as non-root "
            "with read-only filesystem"
        )
    finally:
        if started:
            subprocess.run(
                ["docker", "stop", name], check=True, stdout=subprocess.DEVNULL
            )


if __name__ == "__main__":
    main()
