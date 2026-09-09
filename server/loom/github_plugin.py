"""GitHub HTTP and reconciliation adapter; existing URLs remain compatible."""

import json
import os
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Request
from starlette.concurrency import run_in_threadpool

from loom.github import GitHubBinding, GitHubIngress, verify


class GitHubPlugin:
    name = "github"

    def __init__(self, store):
        self.ingress = GitHubIngress(store)

    def reconcile_pending(self):
        self.ingress.reconcile_pending()

    def routes(self, authenticate):
        router = APIRouter()

        @router.post("/v1/github/bindings", dependencies=[Depends(authenticate)])
        def bind(binding: GitHubBinding):
            self.ingress.bind(binding)
            return {"status": "bound"}

        @router.post("/v1/github/webhook")
        async def webhook(request: Request):
            secret = os.environ.get("LOOM_GITHUB_WEBHOOK_SECRET", "")
            if len(secret) < 32:
                raise HTTPException(503, "GitHub integration is not configured")
            body = bytearray()
            async for chunk in request.stream():
                if len(body) + len(chunk) > 1_048_576:
                    raise HTTPException(413, "Webhook exceeds size limit")
                body.extend(chunk)
            if not verify(
                bytes(body), request.headers.get("x-hub-signature-256", ""), secret
            ):
                raise HTTPException(401, "Invalid webhook signature")
            try:
                delivery = UUID(request.headers.get("x-github-delivery", ""))
                return await run_in_threadpool(
                    self.ingress.receive,
                    bytes(body),
                    delivery,
                    request.headers.get("x-github-event", ""),
                )
            except (ValueError, json.JSONDecodeError):
                raise HTTPException(400, "Invalid webhook envelope") from None

        return router
