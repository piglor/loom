"""Opt-in Go entry point for the existing real recovery proofs."""

import os
from pathlib import Path


def start_go(start, upstream, port):
    binary = os.environ.get("LOOM_GO_BINARY")
    if not binary:
        return None
    executable = Path(binary).resolve(strict=True)
    assets = Path("apps/web/dist").resolve(strict=True)
    os.environ["LOOM_UPSTREAM_URL"] = upstream
    os.environ["LOOM_LISTEN_ADDR"] = f"127.0.0.1:{port}"
    os.environ["LOOM_WEB_DIR"] = str(assets)
    process = start([str(executable)])
    os.environ["LOOM_URL"] = f"http://127.0.0.1:{port}"
    return process
