"""Worker-scoped durable delivery. A lost claim is never automatically reassigned."""

import hashlib
import json
import secrets
from uuid import uuid4

from psycopg.types.json import Jsonb

from loom.store import Conflict, NotFound


class Unauthorized(Exception):
    pass


class Mailbox:
    def __init__(self, store):
        self.store = store

    def enroll(self, request):
        worker_id, token = str(uuid4()), secrets.token_urlsafe(48)
        with self.store.connect() as conn:
            conn.execute(
                "INSERT INTO workers(id,runtime,capabilities,organization,"
                "token_hash,workspace_ref,labels) VALUES "
                "(%s,'remote-demo',%s,%s,%s,%s,%s)",
                (
                    worker_id,
                    Jsonb(
                        ["finite-demo", "event-driven-v1"]
                        if request.protocol_version == 2
                        else ["finite-demo"]
                    ),
                    self.store.settings.organization,
                    hashlib.sha256(token.encode()).hexdigest(),
                    request.workspace_ref,
                    Jsonb(request.labels),
                ),
            )
        return {
            "worker_id": worker_id,
            "token": token,
            "workspace_ref": request.workspace_ref,
            "protocol_version": request.protocol_version,
        }

    def worker(self, conn, token):
        row = conn.execute(
            "SELECT * FROM workers WHERE organization=%s AND token_hash=%s "
            "AND revoked_at IS NULL FOR UPDATE",
            (
                self.store.settings.organization,
                hashlib.sha256(token.encode()).hexdigest(),
            ),
        ).fetchone()
        if not row:
            raise Unauthorized("Invalid worker credential")
        return row

    def revoke(self, worker_id):
        with self.store.connect() as conn:
            if not conn.execute(
                "UPDATE workers SET revoked_at=clock_timestamp() WHERE id=%s "
                "AND organization=%s RETURNING id",
                (worker_id, self.store.settings.organization),
            ).fetchone():
                raise NotFound("Worker not found")

    def dispatch(self, goal_id):
        with self.store.connect() as conn:
            goal = self.store._goal(conn, goal_id, lock=True)
            if goal["state"] != "READY":
                return
            session = conn.execute(
                "SELECT s.* FROM sessions s JOIN runs r ON r.id=s.run_id "
                "WHERE r.goal_id=%s",
                (goal_id,),
            ).fetchone()
            if session["runtime"] != "remote-demo":
                raise Conflict("Unsupported remote runtime")
            if (
                goal["phase"] > 0
                and not conn.execute(
                    "SELECT satisfied_at FROM waits WHERE goal_id=%s", (goal_id,)
                ).fetchone()["satisfied_at"]
            ):
                return
            policy = conn.execute(
                "SELECT policy FROM runs WHERE goal_id=%s", (goal_id,)
            ).fetchone()["policy"]
            if goal["phase"] >= policy["max_attempts"]:
                self.store.transition(conn, goal_id, "BLOCKED")
                conn.execute(
                    "UPDATE waits SET closed_at=COALESCE(closed_at,clock_timestamp()) "
                    "WHERE goal_id=%s",
                    (goal_id,),
                )
                self.store.audit(conn, goal_id, "attempt_budget_exhausted")
                return
            attempt = conn.execute(
                "INSERT INTO attempts(id,goal_id,session_id,worker_id,phase,state) "
                "VALUES (%s,%s,%s,%s,%s,'QUEUED') "
                "ON CONFLICT (goal_id,phase) DO NOTHING RETURNING id",
                (uuid4(), goal_id, session["id"], session["worker_id"], goal["phase"]),
            ).fetchone()
            if attempt:
                conn.execute(
                    "INSERT INTO commands(id,goal_id,attempt_id,worker_id,state) "
                    "VALUES (%s,%s,%s,%s,'QUEUED')",
                    (uuid4(), goal_id, attempt["id"], session["worker_id"]),
                )
                self.store.transition(conn, goal_id, "WAITING")
                conn.execute(
                    "UPDATE goals SET waiting_reason='worker' WHERE id=%s", (goal_id,)
                )
                self.store.audit(
                    conn, goal_id, "command_queued", worker_id=session["worker_id"]
                )

    def poll(self, token):
        with self.store.connect() as conn:
            worker = self.worker(conn, token)
            conn.execute(
                "UPDATE workers SET last_seen_at=clock_timestamp() WHERE id=%s",
                (worker["id"],),
            )
            rows = conn.execute(
                "SELECT c.id,c.state FROM commands c JOIN goals g ON g.id=c.goal_id "
                "WHERE c.worker_id=%s AND c.state IN ('QUEUED','CLAIMED') "
                "AND g.state NOT IN ('COMPLETED','CANCELLED','FAILED','BLOCKED') "
                "ORDER BY c.created_at LIMIT 1",
                (worker["id"],),
            ).fetchall()
            return {"protocol_version": 1, "worker_id": worker["id"], "commands": rows}

    def _command(self, conn, worker, command_id):
        # Goal first, then command: the same order as dispatch/cancellation.
        command = conn.execute(
            "SELECT * FROM commands WHERE id=%s AND worker_id=%s",
            (command_id, worker["id"]),
        ).fetchone()
        if not command:
            raise NotFound("Command not found")
        goal = self.store._goal(conn, command["goal_id"], lock=True)
        command = conn.execute(
            "SELECT * FROM commands WHERE id=%s FOR UPDATE", (command_id,)
        ).fetchone()
        attempt = conn.execute(
            "SELECT * FROM attempts WHERE id=%s", (command["attempt_id"],)
        ).fetchone()
        return command, goal, attempt

    def claim(self, token, command_id, request):
        with self.store.connect() as conn:
            worker = self.worker(conn, token)
            command, goal, attempt = self._command(conn, worker, command_id)
            policy = self._protocol(conn, worker, goal, request)
            if goal["state"] not in ("READY", "WAITING", "RUNNING"):
                raise Conflict("Goal is not executable")
            if command["state"] == "CLAIMED":
                if command["claim_id"] != request.claim_id:
                    raise Conflict(
                        "Execution is already claimed; reconciliation required"
                    )
            elif command["state"] != "QUEUED":
                raise Conflict("Command is not executable")
            else:
                conn.execute(
                    "UPDATE commands SET state='CLAIMED',claim_id=%s,"
                    "claimed_at=clock_timestamp() WHERE id=%s",
                    (request.claim_id, command_id),
                )
                conn.execute(
                    "UPDATE attempts SET state='RUNNING',"
                    "started_at=clock_timestamp() WHERE id=%s",
                    (attempt["id"],),
                )
                if attempt["phase"] > 0:
                    conn.execute(
                        "UPDATE waits SET closed_at=clock_timestamp() WHERE goal_id=%s",
                        (goal["id"],),
                    )
                self.store.transition(conn, goal["id"], "RUNNING")
                conn.execute(
                    "UPDATE goals SET waiting_reason=NULL WHERE id=%s", (goal["id"],)
                )
                self.store.audit(
                    conn, goal["id"], "attempt_started", attempt_id=str(attempt["id"])
                )
            response = {
                "protocol_version": request.protocol_version,
                "command_id": command_id,
                "claim_id": request.claim_id,
                "session_id": attempt["session_id"],
                "worker_id": worker["id"],
                "runtime": worker["runtime"],
                "workspace_ref": worker["workspace_ref"],
                "phase": attempt["phase"],
            }
            if request.protocol_version == 2:
                wait = conn.execute(
                    "SELECT * FROM waits WHERE goal_id=%s", (goal["id"],)
                ).fetchone()
                session = conn.execute(
                    "SELECT * FROM sessions WHERE id=%s", (attempt["session_id"],)
                ).fetchone()
                event = (
                    conn.execute(
                        "SELECT body FROM events WHERE id=%s", (wait["event_id"],)
                    ).fetchone()
                    if wait["event_id"]
                    else None
                )
                response["context"] = {
                    "goal_id": goal["id"],
                    "objective": goal["objective"],
                    "lifecycle": policy.get("lifecycle", "legacy"),
                    "max_attempts": policy["max_attempts"],
                    "completion_condition": goal["completion_criteria"][
                        "event_matches"
                    ],
                    "wait": {
                        "generation": wait["generation"],
                        "condition": wait["condition"],
                        "satisfied": wait["satisfied_at"] is not None,
                    },
                    "provider_session_id": session["provider_session_id"],
                    "external_event": event["body"] if event else None,
                }
            return response

    @staticmethod
    def _protocol(conn, worker, goal, request):
        policy = conn.execute(
            "SELECT policy FROM runs WHERE goal_id=%s", (goal["id"],)
        ).fetchone()["policy"]
        if (
            request.protocol_version == 2
            and "event-driven-v1" not in worker["capabilities"]
        ):
            raise Conflict("Worker is not enrolled for protocol 2")
        if (
            policy.get("lifecycle") == "event-driven-v1"
            and request.protocol_version != 2
        ):
            raise Conflict("Repeatable execution requires protocol 2")
        return policy

    def prepare_wait(self, token, command_id, request):
        with self.store.connect() as conn:
            worker = self.worker(conn, token)
            command, goal, attempt = self._command(conn, worker, command_id)
            self._protocol(conn, worker, goal, request)
            if (
                command["state"] != "CLAIMED"
                or command["claim_id"] != request.claim_id
                or attempt["session_id"] != request.session_id
            ):
                raise Conflict(
                    "Wait preparation requires the current claim and Session"
                )
            wait = self.store.prepare_wait(
                attempt, request.condition, request.expected_generation, connection=conn
            )
            return {
                "protocol_version": 2,
                "wait_id": wait["id"],
                "generation": wait["generation"],
            }

    def bind_session(self, token, command_id, request):
        """Bind only an admitted command; never choose a provider session for it."""
        with self.store.connect() as conn:
            # Serializes all bindings on this worker, including different Goals.
            worker = self.worker(conn, token)
            command, goal, attempt = self._command(conn, worker, command_id)
            self._protocol(conn, worker, goal, request)
            if (
                command["state"] != "CLAIMED"
                or goal["state"] != "RUNNING"
                or attempt["state"] != "RUNNING"
                or command["claim_id"] != request.claim_id
                or attempt["session_id"] != request.session_id
            ):
                raise Conflict(
                    "Session binding requires the current admitted execution"
                )
            session = conn.execute(
                "SELECT * FROM sessions WHERE id=%s FOR UPDATE",
                (request.session_id,),
            ).fetchone()
            existing = session["provider_session_id"]
            if existing is not None and existing != request.provider_session_id:
                raise Conflict("Provider session cannot be reassigned")
            if existing is None:
                if attempt["phase"] != 0:
                    raise Conflict("Continuation cannot create a replacement context")
                if conn.execute(
                    "SELECT 1 FROM sessions WHERE worker_id=%s AND runtime=%s "
                    "AND provider_session_id=%s AND id<>%s",
                    (
                        worker["id"],
                        session["runtime"],
                        request.provider_session_id,
                        session["id"],
                    ),
                ).fetchone():
                    raise Conflict("Provider session is already bound")
                conn.execute(
                    "UPDATE sessions SET provider_session_id=%s WHERE id=%s",
                    (request.provider_session_id, session["id"]),
                )
                self.store.audit(
                    conn,
                    goal["id"],
                    "provider_session_bound",
                    session_id=str(session["id"]),
                )
            return {
                "protocol_version": request.protocol_version,
                "session_id": session["id"],
                "worker_id": worker["id"],
                "runtime": session["runtime"],
                "provider_session_id": request.provider_session_id,
            }

    def report(self, token, command_id, report):
        digest = hashlib.sha256(
            # Preserve hashes of pre-binding v1 receipts which omitted this field.
            json.dumps(
                report.model_dump(mode="json", exclude_none=True), sort_keys=True
            ).encode()
        ).hexdigest()
        with self.store.connect() as conn:
            worker = self.worker(conn, token)
            command, goal, attempt = self._command(conn, worker, command_id)
            self._protocol(conn, worker, goal, report)
            if (
                command["claim_id"] != report.claim_id
                or attempt["session_id"] != report.session_id
            ):
                raise Conflict("Claim or session binding mismatch")
            if command["report_digest"]:
                if command["report_digest"] != digest:
                    raise Conflict("Conflicting stop report")
                return {"status": "duplicate"}
            if command["state"] != "CLAIMED":
                raise Conflict("Command was not claimed")
            provider = conn.execute(
                "SELECT provider_session_id FROM sessions WHERE id=%s",
                (attempt["session_id"],),
            ).fetchone()["provider_session_id"]
            if provider != report.provider_session_id:
                raise Conflict("Stop receipt provider session mismatch")
            self.store.finish(
                attempt,
                report.duration_ms,
                report.success,
                connection=conn,
                outcome=report.outcome,
            )
            conn.execute(
                "UPDATE commands SET state='STOPPED',report_digest=%s,"
                "stopped_at=clock_timestamp() WHERE id=%s",
                (digest, command_id),
            )
            conn.execute(
                "UPDATE goals SET waiting_reason='external_event' "
                "WHERE id=%s AND state='WAITING'",
                (goal["id"],),
            )
            self.store.enqueue(conn, goal["id"], "wake")
            return {"status": "accepted"}
