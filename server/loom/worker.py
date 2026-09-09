import asyncio
import fcntl
import json
import logging
import os
import threading
from datetime import timedelta
from pathlib import Path

from hatchet_sdk import DurableContext, Hatchet
from hatchet_sdk.runnables.eviction import EvictionPolicy

from loom.config import Settings
from loom.models import WorkflowInput
from loom.runtime import execute_demo, flush_receipts
from loom.store import WORKFLOW_STOP_STATES, Store

log = logging.getLogger("loom.worker")
WAKE_KEY = "loom:v1:wake"


def define_workflow(hatchet, store):
    @hatchet.durable_task(
        name="loom-goal-v1",
        input_validator=WorkflowInput,
        execution_timeout=timedelta(days=30),
        schedule_timeout=timedelta(days=30),
        retries=5,
        backoff_factor=2,
        backoff_max_seconds=30,
        eviction_policy=EvictionPolicy(ttl=timedelta(seconds=10)),
    )
    async def pursue(input: WorkflowInput, ctx: DurableContext) -> dict[str, str]:
        while True:
            await asyncio.to_thread(execute_demo, store, input.goal_id)
            goal = await asyncio.to_thread(store.inspect, input.goal_id)
            if goal["state"] in WORKFLOW_STOP_STATES:
                return {"goal_id": str(input.goal_id), "state": goal["state"]}
            if goal["state"] == "READY":
                continue
            if goal["state"] == "RUNNING":
                # Another replay owns an admitted attempt. Never run it twice.
                await ctx.aio_sleep_for(timedelta(seconds=5))
                continue
            wait_id = str(goal["wait"]["id"])
            await ctx.aio_wait_for_event(
                WAKE_KEY,
                f"input.wait_id == '{wait_id}'",
                scope=wait_id,
                lookback_window=timedelta(minutes=1),
            )

    return pursue


def relay_once(hatchet, workflow, store):
    from loom.github import GitHubIngress

    GitHubIngress(store).reconcile_pending()
    for item in store.outbox_batch():
        try:
            workflow_id = None
            if item["kind"] == "start":
                goal = store.inspect(item["goal_id"])
                if goal["state"] not in WORKFLOW_STOP_STATES:
                    ref = workflow.run_no_wait(WorkflowInput(goal_id=item["goal_id"]))
                    workflow_id = ref.workflow_run_id
            else:
                wait_id = str(item["wait_id"])
                hatchet.event.push(WAKE_KEY, {"wait_id": wait_id}, scope=wait_id)
            store.delivered(item, workflow_id)
        except Exception as error:
            # SDK exception messages may contain URLs/credentials. Log only
            # type and opaque intent ID; keep the transfer durable for retry.
            log.error(
                json.dumps(
                    {
                        "event": "outbox_retry",
                        "id": str(item["id"]),
                        "error_type": type(error).__name__,
                    }
                )
            )
            store.delivery_failed(item)


def main():
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    store = Store(Settings.from_env())
    state_dir = Path(os.environ.get("LOOM_STATE_DIR", ".loom"))
    state_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (state_dir / "demo-worker.lock").open("a") as lock, store.connect() as lease:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise SystemExit("A demo worker already owns this state directory")
        lease.autocommit = True
        admitted = lease.execute(
            "SELECT pg_try_advisory_lock(hashtextextended(%s,0)) AS acquired",
            ("loom-demo-executor:" + store.settings.organization,),
        ).fetchone()["acquired"]
        if not admitted:
            raise SystemExit("Another demo executor already owns this organization")
        flush_receipts(store)
        store.recover_uncertain()
        hatchet = Hatchet()
        workflow = define_workflow(hatchet, store)
        stop = threading.Event()

        def relay():
            while not stop.is_set():
                try:
                    # A lost coordinator lock must stop this executor, not let
                    # it silently compete with a newly admitted process.
                    lease.execute("SELECT 1")
                except Exception:
                    log.error('{"event":"executor_lease_lost"}')
                    os.kill(os.getpid(), 15)
                    return
                try:
                    relay_once(hatchet, workflow, store)
                except Exception as error:
                    log.error(
                        json.dumps(
                            {
                                "event": "relay_unavailable",
                                "error_type": type(error).__name__,
                            }
                        )
                    )
                stop.wait(1)

        thread = threading.Thread(target=relay, daemon=True)
        thread.start()
        try:
            hatchet.worker(
                "loom-control-plane", slots=4, durable_slots=20, workflows=[workflow]
            ).start()
        finally:
            stop.set()
            thread.join(timeout=6)


if __name__ == "__main__":
    main()
