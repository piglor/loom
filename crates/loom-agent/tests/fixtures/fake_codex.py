#!/usr/bin/env python3
"""Deterministic private-protocol fixture. Never invokes a model."""
import json
import sys


def emit(value):
    print(json.dumps(value), flush=True)


for line in sys.stdin:
    request = json.loads(line)
    method = request.get("method")
    if method == "initialized":
        continue
    request_id = request["id"]
    if method == "initialize":
        result = {}
    elif method in ("thread/start", "thread/resume"):
        assert request["params"]["sandbox"] == "read-only"
        assert request["params"]["approvalPolicy"] == "never"
        result = {"thread": {"id": request["params"].get("threadId", "thread-fixture")}}
    elif method == "turn/start":
        # Intentionally emit completion notifications BEFORE the RPC response.
        # Clients must retain them, rather than lose a fast finite turn.
        thread_id = request["params"]["threadId"]
        text = request["params"]["input"][0]["text"]
        if text == "approval":
            emit({"id": "server-request", "method": "item/commandExecution/requestApproval"})
            continue
        emit({"method": "item/completed", "params": {"threadId": thread_id,
              "turnId": "turn-1", "item": {"type": "agentMessage", "text": '{"ok":true}'}}})
        emit({"method": "turn/completed", "params": {"threadId": thread_id,
              "turn": {"id": "turn-1", "status": "completed"}}})
        result = {"turn": {"id": "turn-1"}}
    else:
        raise AssertionError("Unexpected method")
    emit({"id": request_id, "result": result})
