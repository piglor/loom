"""Reusable persistence for verified external evidence; not a public ingest API.

Authentication and source-specific validation belong to the calling plugin.
Only the core Store admits execution or chooses the bound Session/Worker.
"""

import hashlib
import json

from psycopg.types.json import Jsonb

from loom.models import Condition, EventCreate
from loom.store import WORKFLOW_STOP_STATES, Conflict


class IntegrationInbox:
    def __init__(self, store, source, instance):
        self.store, self.source, self.instance = store, source, instance

    def _lock(self, conn):
        conn.execute(
            "SELECT pg_advisory_xact_lock(hashtextextended(%s,0))",
            (
                json.dumps(
                    [
                        "integration",
                        self.store.settings.organization,
                        self.source,
                        self.instance,
                    ]
                ),
            ),
        )

    def bind(self, goal_id, generation, condition):
        self._bind(goal_id, generation, condition)
        self.reconcile_pending()

    def _bind(self, goal_id, generation, condition):
        with self.store.connect() as conn:
            self._lock(conn)
            goal = self.store._goal(conn, goal_id, lock=True)
            wait = conn.execute(
                "SELECT * FROM waits WHERE goal_id=%s", (goal_id,)
            ).fetchone()
            runtime = conn.execute(
                "SELECT s.runtime FROM sessions s JOIN runs r ON r.id=s.run_id "
                "WHERE r.goal_id=%s",
                (goal_id,),
            ).fetchone()["runtime"]
            if (
                condition.source != self.source
                or wait["generation"] != generation
                or wait["condition"] != condition.model_dump()
                or goal["state"] in WORKFLOW_STOP_STATES
                or runtime not in ("demo", "remote-demo")
            ):
                raise Conflict("Binding must match an authorized finite-runtime wait")
            values = {
                "source": self.source,
                "instance": self.instance,
                "event_type": condition.type,
                "resource": condition.resource,
                "version": condition.version,
            }
            existing = conn.execute(
                "SELECT * FROM integration_bindings WHERE goal_id=%s AND generation=%s",
                (goal_id, generation),
            ).fetchone()
            if existing:
                if any(existing[k] != value for k, value in values.items()):
                    raise Conflict("External binding is immutable")
                return
            if conn.execute(
                "SELECT 1 FROM integration_bindings WHERE organization=%s "
                "AND source=%s AND instance=%s AND event_type=%s "
                "AND resource=%s AND version=%s",
                (self.store.settings.organization, *values.values()),
            ).fetchone():
                raise Conflict("External operation is already bound")
            conn.execute(
                "INSERT INTO integration_bindings(goal_id,generation,organization,"
                "source,instance,event_type,resource,version) "
                "VALUES (%s,%s,%s,%s,%s,%s,%s,%s)",
                (
                    goal_id,
                    generation,
                    self.store.settings.organization,
                    *values.values(),
                ),
            )
            self.store.audit(
                conn,
                goal_id,
                "integration_bound",
                source=self.source,
                instance=self.instance,
                generation=generation,
            )

    def receive_verified(self, delivery_id, digest, condition, details):
        """Caller has authenticated the raw delivery and normalized allowed fields."""
        if condition is not None and condition.source != self.source:
            raise Conflict("Plugin cannot emit another source's evidence")
        org = self.store.settings.organization
        with self.store.connect() as conn:
            self._lock(conn)
            key = (org, self.source, self.instance, delivery_id)
            existing = conn.execute(
                "SELECT * FROM integration_deliveries WHERE organization=%s "
                "AND source=%s AND instance=%s AND delivery_id=%s",
                key,
            ).fetchone()
            if existing:
                if existing["digest"] != digest:
                    raise Conflict("Delivery ID reused with different bytes")
                if existing["disposition"] != "ignored":
                    return {"disposition": "duplicate"}
                condition = (
                    Condition.model_validate(existing["condition"])
                    if existing["condition"]
                    else None
                )
                details = existing["details"]
            disposition = "ignored"
            if condition:
                binding = conn.execute(
                    "SELECT * FROM integration_bindings WHERE organization=%s "
                    "AND source=%s AND instance=%s AND event_type=%s "
                    "AND resource=%s AND version=%s",
                    (
                        org,
                        self.source,
                        self.instance,
                        condition.type,
                        condition.resource,
                        condition.version,
                    ),
                ).fetchone()
                if binding:
                    # Namespace opaque delivery IDs across instances in the core.
                    identity = hashlib.sha256(
                        json.dumps([self.instance, delivery_id]).encode()
                    ).hexdigest()
                    disposition = self.store.receive(
                        EventCreate(
                            goal_id=binding["goal_id"],
                            generation=binding["generation"],
                            delivery_id=identity,
                            **condition.model_dump(),
                        ),
                        connection=conn,
                    )["disposition"]
                    if disposition == "accepted":
                        self.store.audit(
                            conn,
                            binding["goal_id"],
                            "integration_event",
                            source=self.source,
                            instance=self.instance,
                            generation=binding["generation"],
                            evidence=details,
                        )
            conn.execute(
                "INSERT INTO integration_deliveries(organization,source,instance,"
                "delivery_id,digest,condition,details,disposition) "
                "VALUES (%s,%s,%s,%s,%s,%s,%s,%s) "
                "ON CONFLICT (organization,source,instance,delivery_id) "
                "DO UPDATE SET disposition=excluded.disposition",
                (
                    *key,
                    digest,
                    Jsonb(condition.model_dump()) if condition else None,
                    Jsonb(details),
                    disposition,
                ),
            )
            return {"disposition": disposition}

    def reconcile_pending(self):
        with self.store.connect() as conn:
            rows = conn.execute(
                "SELECT * FROM integration_bindings WHERE organization=%s "
                "AND source=%s AND instance=%s AND reconciled_at IS NULL LIMIT 50",
                (self.store.settings.organization, self.source, self.instance),
            ).fetchall()
        for binding in rows:
            condition = Condition(
                source=self.source,
                type=binding["event_type"],
                resource=binding["resource"],
                version=binding["version"],
            )
            with self.store.connect() as conn:
                pending = conn.execute(
                    "SELECT * FROM integration_deliveries WHERE organization=%s "
                    "AND source=%s AND instance=%s AND disposition='ignored' "
                    "AND condition=%s",
                    (
                        self.store.settings.organization,
                        self.source,
                        self.instance,
                        Jsonb(condition.model_dump()),
                    ),
                ).fetchall()
            for delivery in pending:
                self.receive_verified(
                    delivery["delivery_id"],
                    delivery["digest"],
                    condition,
                    delivery["details"],
                )
            with self.store.connect() as conn:
                conn.execute(
                    "UPDATE integration_bindings SET reconciled_at=clock_timestamp() "
                    "WHERE goal_id=%s AND generation=%s",
                    (binding["goal_id"], binding["generation"]),
                )
