import asyncio
import threading

import pytest
from loom import worker


@pytest.mark.parametrize("registration_fails", [False, True])
def test_relay_waits_for_registered_worker_lifespan(
    store, monkeypatch, registration_fails
):
    submitted = threading.Event()
    relay_threads = []

    def submit(*_):
        relay_threads.append(threading.current_thread())
        submitted.set()

    monkeypatch.setattr(worker.Settings, "from_env", lambda: store.settings)
    monkeypatch.setattr(worker, "Store", lambda _: store)
    monkeypatch.setattr(worker, "define_workflow", lambda *_: object())
    monkeypatch.setattr(worker, "relay_once", submit)

    class FakeHatchet:
        def worker(self, *args, **kwargs):
            class FakeWorker:
                def start(self):
                    # Simulate delayed SDK registration. No outbox publication
                    # is allowed while a new workflow is still being registered.
                    assert not submitted.wait(0.1)
                    if registration_fails:
                        raise RuntimeError("registration unavailable")

                    async def lifecycle():
                        lifespan = kwargs["lifespan"]()
                        await anext(lifespan)
                        assert await asyncio.to_thread(submitted.wait, 1)
                        await lifespan.aclose()

                    asyncio.run(lifecycle())

            return FakeWorker()

    monkeypatch.setattr(worker, "Hatchet", FakeHatchet)
    if registration_fails:
        with pytest.raises(RuntimeError, match="registration unavailable"):
            worker.main()
        assert not submitted.is_set()
    else:
        worker.main()
        assert submitted.is_set()
        assert all(not thread.is_alive() for thread in relay_threads)
