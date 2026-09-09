import hmac
import json
import os
from uuid import UUID

from fastapi import Depends, FastAPI, Header, HTTPException, Request
from fastapi.responses import JSONResponse
from starlette.concurrency import run_in_threadpool

from loom.config import Settings
from loom.github import GitHubBinding, GitHubIngress, verify
from loom.mailbox import Mailbox, Unauthorized
from loom.models import (
    ClaimRequest,
    EventCreate,
    GoalCreate,
    SessionBinding,
    StopReport,
    WorkerEnroll,
)
from loom.store import Conflict, NotFound, Store


def create_app(settings=None):
    settings = settings or Settings.from_env()
    store = Store(settings)
    mailbox = Mailbox(store)
    app = FastAPI(title="Piglor Loom", version="0.1.0")

    def authenticate(authorization: str | None = Header(default=None)):
        supplied = (authorization or "").removeprefix("Bearer ")
        if (
            not authorization
            or not authorization.startswith("Bearer ")
            or not (hmac.compare_digest(supplied.encode(), settings.api_token.encode()))
        ):
            raise HTTPException(401, "Invalid authentication")

    def worker_token(authorization: str | None = Header(default=None)):
        if not authorization or not authorization.startswith("Bearer "):
            raise HTTPException(401, "Invalid worker authentication")
        return authorization.removeprefix("Bearer ")

    @app.exception_handler(Unauthorized)
    async def unauthorized(_request, _error):
        return JSONResponse(
            status_code=401, content={"detail": "Invalid worker authentication"}
        )

    @app.exception_handler(NotFound)
    async def not_found(_request, _error):
        return JSONResponse(status_code=404, content={"detail": "Resource not found"})

    @app.exception_handler(Conflict)
    async def conflict(_request, error):
        return JSONResponse(status_code=409, content={"detail": str(error)})

    @app.get("/healthz")
    def health():
        return {"status": "ok"}

    @app.get("/readyz", dependencies=[Depends(authenticate)])
    def ready():
        with store.connect() as conn:
            if not conn.execute(
                "SELECT version FROM schema_migrations WHERE version=7"
            ).fetchone():
                raise HTTPException(503, "Database migration required")
        return {"database": "ready"}

    @app.post("/v1/goals", status_code=201, dependencies=[Depends(authenticate)])
    def create(request: GoalCreate):
        return store.create(request)

    @app.get("/v1/goals", dependencies=[Depends(authenticate)])
    def list_goals():
        return store.list()

    @app.get("/v1/goals/{goal_id}", dependencies=[Depends(authenticate)])
    def inspect_goal(goal_id: UUID):
        return store.inspect(goal_id)

    @app.post("/v1/goals/{goal_id}/cancel", dependencies=[Depends(authenticate)])
    def cancel(goal_id: UUID):
        store.cancel(goal_id)
        return store.inspect(goal_id)

    @app.post("/v1/events", dependencies=[Depends(authenticate)])
    def receive(request: EventCreate):
        return store.receive(request)

    @app.post("/v1/workers", status_code=201, dependencies=[Depends(authenticate)])
    def enroll_worker(request: WorkerEnroll):
        return mailbox.enroll(request)

    @app.post("/v1/workers/{worker_id}/revoke", dependencies=[Depends(authenticate)])
    def revoke_worker(worker_id: UUID):
        mailbox.revoke(str(worker_id))
        return {"status": "revoked"}

    @app.get("/v1/worker/commands")
    def commands(token=Depends(worker_token)):
        return mailbox.poll(token)

    @app.post("/v1/worker/commands/{command_id}/claim")
    def claim_command(
        command_id: UUID, request: ClaimRequest, token=Depends(worker_token)
    ):
        return mailbox.claim(token, command_id, request)

    @app.post("/v1/worker/commands/{command_id}/stop")
    def stop_command(
        command_id: UUID, request: StopReport, token=Depends(worker_token)
    ):
        return mailbox.report(token, command_id, request)

    @app.post("/v1/worker/commands/{command_id}/session")
    def bind_session(
        command_id: UUID, request: SessionBinding, token=Depends(worker_token)
    ):
        return mailbox.bind_session(token, command_id, request)

    @app.post("/v1/github/bindings", dependencies=[Depends(authenticate)])
    def github_binding(binding: GitHubBinding):
        GitHubIngress(store).bind(binding)
        return {"status": "bound"}

    @app.post("/v1/github/webhook")
    async def github_webhook(request: Request):
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
                GitHubIngress(store).receive,
                bytes(body),
                delivery,
                request.headers.get("x-github-event", ""),
            )
        except (ValueError, json.JSONDecodeError):
            raise HTTPException(400, "Invalid webhook envelope")

    return app
