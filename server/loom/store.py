"""Transactional domain state; no Hatchet or model calls inside transactions."""

import hashlib
import json
from contextlib import nullcontext
from importlib.resources import files
from uuid import uuid4

import psycopg
from psycopg.rows import dict_row
from psycopg.types.json import Jsonb

from loom.config import Settings
from loom.models import EventCreate, GoalCreate

WORKFLOW_STOP_STATES = {"COMPLETED", "FAILED", "CANCELLED", "BLOCKED"}


class Conflict(Exception):
    pass


class NotFound(Exception):
    pass


class Store:
    def __init__(self, settings: Settings):
        self.settings = settings

    def connect(self):
        return psycopg.connect(
            self.settings.database_url, row_factory=dict_row, connect_timeout=5
        )

    def migrate(self):
        with self.connect() as conn:
            conn.execute("SELECT pg_advisory_xact_lock(73021001)")
            conn.execute(files("loom").joinpath("schema.sql").read_text())
            if not conn.execute(
                "SELECT 1 FROM schema_migrations WHERE version=2"
            ).fetchone():
                conn.execute(files("loom").joinpath("002-workers.sql").read_text())
            if not conn.execute(
                "SELECT 1 FROM schema_migrations WHERE version=3"
            ).fetchone():
                conn.execute(files("loom").joinpath("003-github.sql").read_text())
            if not conn.execute(
                "SELECT 1 FROM schema_migrations WHERE version=4"
            ).fetchone():
                conn.execute(files("loom").joinpath("004-github-inbox.sql").read_text())
            if not conn.execute(
                "SELECT 1 FROM schema_migrations WHERE version=5"
            ).fetchone():
                conn.execute(files("loom").joinpath("005-reconcile.sql").read_text())
            if not conn.execute(
                "SELECT 1 FROM schema_migrations WHERE version=6"
            ).fetchone():
                conn.execute(
                    files("loom").joinpath("006-provider-binding.sql").read_text()
                )

    def _goal(self, conn, goal_id, lock=False):
        row = conn.execute(
            "SELECT * FROM goals WHERE id=%s AND organization=%s"
            + (" FOR UPDATE" if lock else ""),
            (goal_id, self.settings.organization),
        ).fetchone()
        if row is None:
            raise NotFound("Goal not found")
        return row

    @staticmethod
    def audit(conn, goal_id, action, **details):
        conn.execute(
            "INSERT INTO audit(goal_id, action, details) VALUES (%s,%s,%s)",
            (goal_id, action, Jsonb(details)),
        )

    @staticmethod
    def transition(conn, goal_id, state):
        conn.execute(
            "UPDATE goals SET state=%s, updated_at=clock_timestamp(), "
            "ended_at=CASE WHEN %s IN ('COMPLETED','FAILED','CANCELLED') "
            "THEN clock_timestamp() ELSE NULL END WHERE id=%s",
            (state, state, goal_id),
        )
        Store.audit(conn, goal_id, "state_changed", state=state)

    @staticmethod
    def enqueue(conn, goal_id, kind):
        conn.execute(
            "INSERT INTO outbox(id,goal_id,kind) VALUES (%s,%s,%s) "
            "ON CONFLICT (goal_id,kind) DO UPDATE SET delivered_at=NULL, "
            "available_at=clock_timestamp()",
            (uuid4(), goal_id, kind),
        )

    def create(self, request: GoalCreate):
        goal_id, run_id = uuid4(), uuid4()
        with self.connect() as conn:
            worker_id = request.worker_id or "demo-local"
            if request.runtime != "demo":
                worker = conn.execute(
                    "SELECT * FROM workers WHERE id=%s AND organization=%s "
                    "AND revoked_at IS NULL FOR SHARE",
                    (worker_id, self.settings.organization),
                ).fetchone()
                if not worker or worker["runtime"] != request.runtime:
                    raise Conflict("Worker is unavailable or runtime is unauthorized")
            conn.execute(
                "INSERT INTO goals(id,organization,title,objective,state,"
                "completion_criteria) VALUES (%s,%s,%s,%s,'READY',%s)",
                (
                    goal_id,
                    self.settings.organization,
                    request.title,
                    request.objective,
                    Jsonb({"event_matches": request.condition.model_dump()}),
                ),
            )
            conn.execute(
                "INSERT INTO runs(id,goal_id,policy) VALUES (%s,%s,%s)",
                (
                    run_id,
                    goal_id,
                    Jsonb({"runtime": request.runtime, "max_attempts": 2}),
                ),
            )
            conn.execute(
                "INSERT INTO sessions(id,run_id,worker_id,runtime) "
                "VALUES (%s,%s,%s,%s)",
                (uuid4(), run_id, worker_id, request.runtime),
            )
            conn.execute(
                "INSERT INTO waits(id,goal_id,condition) VALUES (%s,%s,%s)",
                (uuid4(), goal_id, Jsonb(request.condition.model_dump())),
            )
            self.audit(conn, goal_id, "goal_created", runtime=request.runtime)
            self.enqueue(conn, goal_id, "start")
        return self.inspect(goal_id)

    def list(self):
        with self.connect() as conn:
            return conn.execute(
                "SELECT id,title,state,created_at,updated_at FROM goals "
                "WHERE organization=%s ORDER BY created_at DESC LIMIT 100",
                (self.settings.organization,),
            ).fetchall()

    def inspect(self, goal_id):
        with self.connect() as conn:
            conn.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            goal = self._goal(conn, goal_id)
            goal["run"] = conn.execute(
                "SELECT * FROM runs WHERE goal_id=%s", (goal_id,)
            ).fetchone()
            goal["session"] = conn.execute(
                "SELECT * FROM sessions WHERE run_id=%s", (goal["run"]["id"],)
            ).fetchone()
            goal["wait"] = conn.execute(
                "SELECT * FROM waits WHERE goal_id=%s", (goal_id,)
            ).fetchone()
            goal["attempts"] = conn.execute(
                "SELECT * FROM attempts WHERE goal_id=%s ORDER BY phase", (goal_id,)
            ).fetchall()
            goal["audit"] = conn.execute(
                "SELECT * FROM audit WHERE goal_id=%s ORDER BY sequence", (goal_id,)
            ).fetchall()
            goal["metrics"] = conn.execute(
                "SELECT EXTRACT(EPOCH FROM (COALESCE(g.ended_at,clock_timestamp())"
                "-g.created_at)) AS lifetime_seconds, "
                "CASE WHEN w.armed_at IS NULL THEN 0 ELSE GREATEST(0, "
                "EXTRACT(EPOCH FROM (COALESCE(w.closed_at,clock_timestamp())"
                "-w.armed_at))) END AS suspended_seconds, "
                "(SELECT COALESCE(SUM(duration_ms),0)/1000 FROM attempts "
                "WHERE goal_id=g.id) AS execution_seconds, "
                "(SELECT count(*) FROM attempts WHERE goal_id=g.id) AS attempts, "
                "(SELECT count(*) FROM attempts WHERE goal_id=g.id AND phase=1 "
                "AND state<>'QUEUED' "
                "AND outcome IS DISTINCT FROM 'cancelled_unclaimed') "
                "AS wake_ups "
                "FROM goals g JOIN waits w ON w.goal_id=g.id WHERE g.id=%s",
                (goal_id,),
            ).fetchone()
            goal["metrics"]["tokens"] = None
            goal["metrics"]["provider_cost"] = None
            return goal

    def receive(self, event: EventCreate, *, connection=None):
        body = event.model_dump(mode="json")
        digest = hashlib.sha256(
            json.dumps(body, sort_keys=True, separators=(",", ":")).encode()
        ).hexdigest()
        with nullcontext(connection) if connection else self.connect() as conn:
            # Serialize delivery identity even when concurrent requests target
            # different Goals. Lock delivery first, then Goal, consistently.
            delivery_key = json.dumps(
                [self.settings.organization, event.source, event.delivery_id]
            )
            conn.execute(
                "SELECT pg_advisory_xact_lock(hashtextextended(%s,0))",
                (delivery_key,),
            )
            existing = conn.execute(
                "SELECT * FROM events WHERE organization=%s AND source=%s "
                "AND delivery_id=%s",
                (self.settings.organization, event.source, event.delivery_id),
            ).fetchone()
            if existing:
                if existing["digest"] != digest:
                    raise Conflict("Delivery ID reused with different content")
                return {"id": existing["id"], "disposition": "duplicate"}
            event_id = uuid4()
            try:
                goal = self._goal(conn, event.goal_id, lock=True)
            except NotFound:
                goal = None
            wait = (
                conn.execute(
                    "SELECT * FROM waits WHERE goal_id=%s", (event.goal_id,)
                ).fetchone()
                if goal
                else None
            )
            disposition = "unknown_goal"
            if goal:
                if goal["state"] in WORKFLOW_STOP_STATES:
                    disposition = "inactive"
                elif event.generation != wait["generation"] or any(
                    body[k] != wait["condition"][k]
                    for k in ("source", "type", "resource", "version")
                ):
                    disposition = "mismatch"
                elif wait["satisfied_at"]:
                    disposition = "already_satisfied"
                else:
                    disposition = "accepted"
            conn.execute(
                "INSERT INTO events(id,organization,source,delivery_id,digest,body,"
                "disposition) VALUES (%s,%s,%s,%s,%s,%s,%s)",
                (
                    event_id,
                    self.settings.organization,
                    event.source,
                    event.delivery_id,
                    digest,
                    Jsonb(body),
                    disposition,
                ),
            )
            if goal:
                self.audit(
                    conn,
                    event.goal_id,
                    "event_received",
                    event_id=str(event_id),
                    disposition=disposition,
                )
            if disposition == "accepted":
                conn.execute(
                    "UPDATE waits SET satisfied_at=clock_timestamp(),event_id=%s "
                    "WHERE id=%s",
                    (event_id, wait["id"]),
                )
                if goal["state"] == "WAITING" and goal["waiting_reason"] != "worker":
                    self.transition(conn, event.goal_id, "READY")
                self.enqueue(conn, event.goal_id, "wake")
            return {"id": event_id, "disposition": disposition}

    def claim(self, goal_id):
        """Return a new finite attempt, or none. Replay never relaunches it."""
        with self.connect() as conn:
            goal = self._goal(conn, goal_id, lock=True)
            if goal["state"] != "READY":
                return None
            wait = conn.execute(
                "SELECT * FROM waits WHERE goal_id=%s", (goal_id,)
            ).fetchone()
            if goal["phase"] == 1 and not wait["satisfied_at"]:
                return None
            session = conn.execute(
                "SELECT s.* FROM sessions s JOIN runs r ON r.id=s.run_id "
                "WHERE r.goal_id=%s",
                (goal_id,),
            ).fetchone()
            if session["runtime"] != "demo" or session["worker_id"] != "demo-local":
                raise Conflict("Local admission cannot execute a remote session")
            attempt = conn.execute(
                "INSERT INTO attempts(id,goal_id,session_id,worker_id,phase,state) "
                "VALUES (%s,%s,%s,%s,%s,'RUNNING') "
                "ON CONFLICT (goal_id,phase) DO NOTHING RETURNING *",
                (
                    uuid4(),
                    goal_id,
                    session["id"],
                    session["worker_id"],
                    goal["phase"],
                ),
            ).fetchone()
            if attempt is None:
                return None
            if goal["phase"] == 1:
                conn.execute(
                    "UPDATE waits SET closed_at=clock_timestamp() WHERE goal_id=%s",
                    (goal_id,),
                )
            self.transition(conn, goal_id, "RUNNING")
            self.audit(conn, goal_id, "attempt_started", attempt_id=str(attempt["id"]))
            return attempt

    def finish(self, attempt, duration_ms, success=True, *, connection=None):
        with nullcontext(connection) if connection else self.connect() as conn:
            goal = self._goal(conn, attempt["goal_id"], lock=True)
            stored = conn.execute(
                "SELECT * FROM attempts WHERE id=%s", (attempt["id"],)
            ).fetchone()
            if stored is None or any(
                stored[k] != attempt[k]
                for k in ("goal_id", "session_id", "worker_id", "phase")
            ):
                raise Conflict("Attempt binding mismatch")
            if stored["state"] == "STOPPED":
                return
            conn.execute(
                "UPDATE attempts SET state='STOPPED',stopped_at=clock_timestamp(),"
                "duration_ms=%s,outcome=%s WHERE id=%s",
                (
                    duration_ms,
                    "success" if success else "runtime_failure",
                    attempt["id"],
                ),
            )
            self.audit(
                conn, goal["id"], "runtime_stopped", attempt_id=str(attempt["id"])
            )
            if not success:
                self.transition(conn, goal["id"], "FAILED")
                return
            if stored["phase"] == 0:
                wait = conn.execute(
                    "UPDATE waits SET armed_at=clock_timestamp() WHERE goal_id=%s "
                    "RETURNING *",
                    (goal["id"],),
                ).fetchone()
                conn.execute("UPDATE goals SET phase=1 WHERE id=%s", (goal["id"],))
                self.transition(
                    conn, goal["id"], "READY" if wait["satisfied_at"] else "WAITING"
                )
            else:
                wait = conn.execute(
                    "SELECT satisfied_at FROM waits WHERE goal_id=%s", (goal["id"],)
                ).fetchone()
                if not wait["satisfied_at"]:
                    raise Conflict("Completion criterion is not satisfied")
                conn.execute("UPDATE goals SET phase=2 WHERE id=%s", (goal["id"],))
                self.transition(conn, goal["id"], "COMPLETED")

    def cancel(self, goal_id):
        with self.connect() as conn:
            goal = self._goal(conn, goal_id, lock=True)
            if (
                goal["state"] == "RUNNING"
                or conn.execute(
                    "SELECT 1 FROM attempts WHERE goal_id=%s "
                    "AND state IN ('RUNNING','UNKNOWN')",
                    (goal_id,),
                ).fetchone()
            ):
                raise Conflict(
                    "Runtime stop is unconfirmed; reconcile before cancellation"
                )
            if goal["state"] in WORKFLOW_STOP_STATES:
                return
            conn.execute(
                "UPDATE commands SET state='STOPPED',stopped_at=clock_timestamp() "
                "WHERE goal_id=%s AND state='QUEUED'",
                (goal_id,),
            )
            conn.execute(
                "UPDATE attempts SET state='STOPPED',outcome='cancelled_unclaimed',"
                "duration_ms=0,stopped_at=clock_timestamp() "
                "WHERE goal_id=%s AND state='QUEUED'",
                (goal_id,),
            )
            self.transition(conn, goal_id, "CANCELLED")
            conn.execute(
                "UPDATE waits SET closed_at=clock_timestamp() WHERE goal_id=%s",
                (goal_id,),
            )
            self.enqueue(conn, goal_id, "wake")

    def recover_uncertain(self):
        """Called only while holding the sole demo executor's process lock."""
        with self.connect() as conn:
            goals = conn.execute(
                "SELECT id FROM goals WHERE organization=%s AND state='RUNNING' "
                "AND id IN (SELECT r.goal_id FROM runs r JOIN sessions s "
                "ON s.run_id=r.id WHERE s.runtime='demo') "
                "FOR UPDATE",
                (self.settings.organization,),
            ).fetchall()
            for goal in goals:
                conn.execute(
                    "UPDATE attempts SET state='UNKNOWN' WHERE goal_id=%s "
                    "AND state='RUNNING'",
                    (goal["id"],),
                )
                self.transition(conn, goal["id"], "BLOCKED")
                self.audit(conn, goal["id"], "runtime_status_unknown")

    def outbox_batch(self):
        with self.connect() as conn:
            # Wake signals are deliberately retried until domain completion.
            # The engine event is a hint; durable satisfaction is authoritative.
            return conn.execute(
                "SELECT o.*,w.id AS wait_id FROM outbox o "
                "JOIN goals g ON g.id=o.goal_id JOIN waits w ON w.goal_id=g.id "
                "WHERE g.organization=%s AND o.available_at <= clock_timestamp() "
                "AND (o.delivered_at IS NULL OR (o.kind='wake' "
                "AND g.state='READY')) "
                "ORDER BY o.available_at LIMIT 50",
                (self.settings.organization,),
            ).fetchall()

    def delivered(self, item, workflow_id=None):
        with self.connect() as conn:
            conn.execute(
                "UPDATE outbox SET delivered_at=clock_timestamp(),"
                "available_at=clock_timestamp()+interval '5 seconds' WHERE id=%s",
                (item["id"],),
            )
            if workflow_id:
                conn.execute(
                    "UPDATE runs SET workflow_id=%s WHERE goal_id=%s",
                    (str(workflow_id), item["goal_id"]),
                )

    def delivery_failed(self, item):
        with self.connect() as conn:
            conn.execute(
                "UPDATE outbox SET failures=failures+1,available_at="
                "clock_timestamp()+interval '5 seconds' WHERE id=%s",
                (item["id"],),
            )
