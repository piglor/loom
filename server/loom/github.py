"""Signed workflow-completion ingress for explicitly bound finite-runtime Goals.

Privileged runtimes remain disabled until live API freshness checks are in place.
"""

import hashlib
import hmac
import json
from uuid import UUID

from psycopg.types.json import Jsonb
from pydantic import Field

from loom.models import Condition, EventCreate, Model
from loom.store import Conflict


class GitHubBinding(Model):
    goal_id: UUID
    installation_id: int = Field(gt=0, strict=True)
    repository_id: int = Field(gt=0, strict=True)
    pull_request: int = Field(gt=0, strict=True)
    head_sha: str = Field(pattern=r"^[a-f0-9]{40}$")
    run_id: int = Field(gt=0, strict=True)
    run_attempt: int = Field(gt=0, strict=True)
    workflow_id: int = Field(gt=0, strict=True)

    def condition(self):
        return Condition(
            source="github",
            type="workflow.completed",
            resource=f"{self.repository_id}/{self.pull_request}/{self.run_id}/{self.run_attempt}",
            version=self.head_sha,
        )


def verify(body: bytes, signature: str, secret: str):
    if not secret or len(body) > 1_048_576:
        return False
    expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(signature.encode(), expected.encode())


def matches(binding: GitHubBinding, payload):
    """Fail closed for missing/ambiguous PR association; no branch-name matching."""
    try:
        run = payload["workflow_run"]
        candidates = run["pull_requests"]
        return (
            payload["action"] == "completed"
            and run["status"] == "completed"
            and payload["installation"]["id"] == binding.installation_id
            and payload["repository"]["id"] == binding.repository_id
            and run["head_repository"]["id"] == binding.repository_id
            and run["event"] == "pull_request"
            and run["id"] == binding.run_id
            and run["run_attempt"] == binding.run_attempt
            and run["workflow_id"] == binding.workflow_id
            and run["head_sha"] == binding.head_sha
            and len(candidates) == 1
            and candidates[0]["number"] == binding.pull_request
            and candidates[0]["head"]["sha"] == binding.head_sha
            and candidates[0]["head"]["repo"]["id"] == binding.repository_id
            and candidates[0]["base"]["repo"]["id"] == binding.repository_id
        )
    except (KeyError, TypeError, IndexError):
        return False


class GitHubIngress:
    def __init__(self, store):
        self.store = store

    def bind(self, binding):
        self._bind(binding)
        self.reconcile(binding)

    def reconcile(self, binding):
        # No Goal lock is held while entering receive's delivery->Goal order.
        with self.store.connect() as conn:
            rows = conn.execute(
                "SELECT raw_body,delivery_id,event_type FROM github_deliveries "
                "WHERE organization=%s AND disposition='ignored' AND payload @> %s",
                (
                    self.store.settings.organization,
                    Jsonb(
                        {
                            "workflow_run": {"id": binding.run_id},
                            "repository": {"id": binding.repository_id},
                            "installation": {"id": binding.installation_id},
                        }
                    ),
                ),
            ).fetchall()
        for row in rows:
            self.receive(bytes(row["raw_body"]), row["delivery_id"], row["event_type"])
        with self.store.connect() as conn:
            conn.execute(
                "UPDATE github_bindings SET reconciled_at=clock_timestamp() "
                "WHERE goal_id=%s AND organization=%s",
                (binding.goal_id, self.store.settings.organization),
            )

    def reconcile_pending(self):
        with self.store.connect() as conn:
            rows = conn.execute(
                "SELECT * FROM github_bindings WHERE organization=%s "
                "AND reconciled_at IS NULL LIMIT 50",
                (self.store.settings.organization,),
            ).fetchall()
        for row in rows:
            self.reconcile(
                GitHubBinding.model_validate(
                    {k: row[k] for k in GitHubBinding.model_fields}
                )
            )

    def _bind(self, binding):
        with self.store.connect() as conn:
            conn.execute(
                "SELECT pg_advisory_xact_lock(hashtextextended(%s,0))",
                ("github:" + self.store.settings.organization,),
            )
            goal = self.store._goal(conn, binding.goal_id, lock=True)
            runtime = conn.execute(
                "SELECT s.runtime FROM sessions s JOIN runs r ON r.id=s.run_id "
                "WHERE r.goal_id=%s",
                (goal["id"],),
            ).fetchone()["runtime"]
            if runtime not in ("demo", "remote-demo"):
                raise Conflict(
                    "Privileged GitHub execution requires live freshness validation"
                )
            wait = conn.execute(
                "SELECT condition FROM waits WHERE goal_id=%s", (goal["id"],)
            ).fetchone()
            if wait["condition"] != binding.condition().model_dump():
                raise Conflict(
                    "Binding must exactly match the authorized Goal condition"
                )
            existing = conn.execute(
                "SELECT * FROM github_bindings WHERE goal_id=%s", (goal["id"],)
            ).fetchone()
            if existing:
                expected = binding.model_dump()
                if any(existing[k] != value for k, value in expected.items()):
                    raise Conflict(
                        "Binding is immutable; create a new authorized generation"
                    )
                return
            duplicate = conn.execute(
                "SELECT 1 FROM github_bindings WHERE organization=%s "
                "AND installation_id=%s AND repository_id=%s "
                "AND run_id=%s AND run_attempt=%s",
                (
                    self.store.settings.organization,
                    binding.installation_id,
                    binding.repository_id,
                    binding.run_id,
                    binding.run_attempt,
                ),
            ).fetchone()
            if duplicate:
                raise Conflict("CI execution is already bound")
            conn.execute(
                "INSERT INTO github_bindings(goal_id,organization,installation_id,"
                "repository_id,pull_request,head_sha,run_id,run_attempt,workflow_id) "
                "VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s)",
                (
                    binding.goal_id,
                    self.store.settings.organization,
                    binding.installation_id,
                    binding.repository_id,
                    binding.pull_request,
                    binding.head_sha,
                    binding.run_id,
                    binding.run_attempt,
                    binding.workflow_id,
                ),
            )
            self.store.audit(
                conn,
                goal["id"],
                "github_binding_created",
                repository_id=binding.repository_id,
                pull_request=binding.pull_request,
            )

    def receive(self, body, delivery_id: UUID, event_type):
        digest = hashlib.sha256(body).hexdigest()
        payload = json.loads(body)
        org = self.store.settings.organization
        with self.store.connect() as conn:
            # Global ordering for this organization's GitHub inbox/bindings.
            conn.execute(
                "SELECT pg_advisory_xact_lock(hashtextextended(%s,0))",
                ("github:" + org,),
            )
            existing = conn.execute(
                "SELECT * FROM github_deliveries "
                "WHERE organization=%s AND delivery_id=%s",
                (org, delivery_id),
            ).fetchone()
            if existing:
                if existing["digest"] != digest:
                    raise Conflict("GitHub delivery ID reused with different bytes")
                if existing["disposition"] != "ignored":
                    return {"disposition": "duplicate"}
                event_type = existing["event_type"]
            disposition = "ignored"
            if event_type == "workflow_run":
                bindings = conn.execute(
                    "SELECT * FROM github_bindings WHERE organization=%s", (org,)
                ).fetchall()
                for row in bindings:
                    binding = GitHubBinding.model_validate(
                        {k: row[k] for k in GitHubBinding.model_fields}
                    )
                    if matches(binding, payload):
                        event = EventCreate(
                            delivery_id=str(delivery_id),
                            goal_id=binding.goal_id,
                            generation=1,
                            **binding.condition().model_dump(),
                        )
                        disposition = self.store.receive(event, connection=conn)[
                            "disposition"
                        ]
                        if disposition == "accepted":
                            self.store.audit(
                                conn,
                                binding.goal_id,
                                "github_ci_completed",
                                run_id=binding.run_id,
                                run_attempt=binding.run_attempt,
                                conclusion=payload["workflow_run"].get("conclusion"),
                                trust="signed_external_evidence",
                            )
                        break
            conn.execute(
                "INSERT INTO github_deliveries(organization,delivery_id,digest,"
                "disposition,event_type,raw_body,payload) "
                "VALUES (%s,%s,%s,%s,%s,%s,%s) "
                "ON CONFLICT(organization,delivery_id) DO UPDATE "
                "SET disposition=excluded.disposition",
                (
                    org,
                    delivery_id,
                    digest,
                    disposition,
                    event_type,
                    body,
                    Jsonb(payload),
                ),
            )
            return {"disposition": disposition}
