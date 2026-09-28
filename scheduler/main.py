"""Application assembly and ``python -m scheduler`` entry point.

Wiring only: config -> repository -> engine -> FastAPI. A background thread
runs ticks at the configured interval; it can be disabled so a single worker
process drives ticks via POST /ticks.
"""
from __future__ import annotations

import threading
from datetime import datetime, timezone

import uvicorn

from .api import create_app
from .config import Settings
from .engine.core import SchedulerEngine
from .errors import AppError
from .state.repo import Repository


def build(settings: Settings, *, init_schema: bool = True):
    repo = Repository(settings.database_url)
    repo.open()
    if init_schema:
        repo.init_schema()
    engine = SchedulerEngine(repo, settings)
    app = create_app(engine)
    return app, repo, engine


def _start_background_ticker(engine: SchedulerEngine, interval: float) -> threading.Thread:
    stop = threading.Event()

    def loop() -> None:
        while not stop.wait(interval):
            try:
                engine.tick(datetime.now(timezone.utc))
            except AppError:
                # e.g. clock-rollback between hosts; next interval retries.
                continue
            except Exception:  # pragma: no cover - worker resilience
                import logging

                logging.getLogger("scheduler").exception("background tick failed")

    thread = threading.Thread(target=loop, name="scheduler-ticker", daemon=True)
    thread.stop_event = stop  # type: ignore[attr-defined]
    thread.start()
    return thread


def main() -> None:
    settings = Settings.from_env()
    app, repo, engine = build(settings)

    ticker: threading.Thread | None = None
    if settings.enable_background_ticker and settings.ticker_interval_seconds > 0:
        ticker = _start_background_ticker(
            engine, settings.ticker_interval_seconds
        )

    try:
        uvicorn.run(app, host=settings.host, port=settings.port, log_level="info")
    finally:
        if ticker is not None:
            ticker.stop_event.set()  # type: ignore[attr-defined]
        repo.close()


if __name__ == "__main__":
    main()
