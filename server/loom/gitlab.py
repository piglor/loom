"""GitLab pipeline adapter. No runtime selection or execution authority."""

import base64
import binascii
import hashlib
import hmac
import json
import os
import time
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Request
from pydantic import Field
from starlette.concurrency import run_in_threadpool

from loom.integration_inbox import IntegrationInbox
from loom.models import Condition, Model
from loom.store import Conflict


class GitLabBinding(Model):
    goal_id: UUID
    generation: int = Field(default=1, ge=1)
    instance: str = Field(pattern=r"^[a-z0-9][a-z0-9.-]{0,79}$")
    project_id: int = Field(gt=0, le=9223372036854775807, strict=True)
    merge_request: int = Field(gt=0, le=9223372036854775807, strict=True)
    pipeline_id: int = Field(gt=0, le=9223372036854775807, strict=True)
    head_sha: str = Field(pattern=r"^[a-f0-9]{40}$")

    def condition(self):
        return Condition(
            source="gitlab",
            type="pipeline.completed",
            resource=f"{self.instance}/{self.project_id}/{self.merge_request}/{self.pipeline_id}",
            version=self.head_sha,
        )


def verify_signature(body, headers, token, now=None):
    """GitLab's documented Standard Webhooks HMAC with bounded replay tolerance."""
    try:
        key = base64.b64decode(token.removeprefix("whsec_"), validate=True)
        delivery = headers["webhook-id"]
        timestamp = headers["webhook-timestamp"]
        UUID(delivery)
        if (
            len(key) < 32
            or len(timestamp) > 12
            or not timestamp.isascii()
            or not timestamp.isdigit()
        ):
            return False
        if abs((time.time() if now is None else now) - int(timestamp)) > 300:
            return False
        signed = (
            delivery.encode("ascii") + b"." + timestamp.encode("ascii") + b"." + body
        )
        expected = b"v1," + base64.b64encode(hmac.digest(key, signed, "sha256"))
        signatures = headers.get("webhook-signature", "")
        if len(signatures) > 4096:
            return False
        return any(
            hmac.compare_digest(expected, s.encode("ascii")) for s in signatures.split()
        )
    except (KeyError, ValueError, binascii.Error, UnicodeError):
        return False


def normalize(payload, instance):
    """Reject forks/ambiguous identities; discard all free-form external text."""
    try:
        pipeline, project, mr = (
            payload["object_attributes"],
            payload["project"],
            payload["merge_request"],
        )
        status = pipeline["status"]
        if (
            payload["object_kind"] != "pipeline"
            or pipeline["source"] != "merge_request_event"
            or status not in ("success", "failed", "canceled", "skipped")
            or type(mr["source_project_id"]) is not int
            or type(mr["target_project_id"]) is not int
            or mr["source_project_id"] != project["id"]
            or mr["target_project_id"] != project["id"]
            or payload["commit"]["id"] != pipeline["sha"]
        ):
            return None, {}
        binding = GitLabBinding(
            goal_id=UUID(int=0),
            instance=instance,
            project_id=project["id"],
            merge_request=mr["iid"],
            pipeline_id=pipeline["id"],
            head_sha=pipeline["sha"],
        )
        return binding.condition(), {
            "conclusion": status,
            "pipeline_id": binding.pipeline_id,
        }
    except (KeyError, TypeError, ValueError):
        return None, {}


class GitLabPlugin:
    name = "gitlab"

    def __init__(self, store):
        self.instance = os.environ.get("LOOM_GITLAB_INSTANCE", "gitlab.com")
        self.inbox = IntegrationInbox(store, self.name, self.instance)

    def reconcile_pending(self):
        self.inbox.reconcile_pending()

    def routes(self, authenticate):
        router = APIRouter()

        @router.post("/v1/gitlab/bindings", dependencies=[Depends(authenticate)])
        def bind(binding: GitLabBinding):
            if binding.instance != self.instance:
                raise Conflict("GitLab instance is not configured on this server")
            self.inbox.bind(binding.goal_id, binding.generation, binding.condition())
            return {"status": "bound"}

        @router.post("/v1/gitlab/webhook")
        async def webhook(request: Request):
            token = os.environ.get("LOOM_GITLAB_SIGNING_TOKEN", "")
            if not token:
                raise HTTPException(503, "GitLab integration is not configured")
            body = bytearray()
            async for chunk in request.stream():
                if len(body) + len(chunk) > 1_048_576:
                    raise HTTPException(413, "Webhook exceeds size limit")
                body.extend(chunk)
            raw = bytes(body)
            if not verify_signature(raw, request.headers, token):
                raise HTTPException(401, "Invalid webhook signature or timestamp")
            try:
                payload = json.loads(raw)
            except (ValueError, UnicodeError):
                raise HTTPException(400, "Invalid webhook envelope") from None
            condition, details = normalize(payload, self.instance)
            return await run_in_threadpool(
                self.inbox.receive_verified,
                str(UUID(request.headers["webhook-id"])),
                hashlib.sha256(raw).hexdigest(),
                condition,
                details,
            )

        return router
