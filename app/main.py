"""FastAPI 入口：连接池、schema、后台 tick 循环、请求 id 与统一错误处理。"""
from __future__ import annotations

import asyncio
import uuid
from contextlib import asynccontextmanager
from datetime import datetime, timezone

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from app.api import admin, schedules
from app.clock import SystemClock
from app.config import get_settings
from app.db import apply_schema, close_pool, init_pool
from app.errors import register_exception_handlers
from app.kernel.scheduler import Scheduler
from app.logx import configure_logging, log_event

_bg_task: asyncio.Task | None = None
_bg_stop: asyncio.Event | None = None


async def _background_loop(scheduler: Scheduler, interval: float) -> None:
    global _bg_stop
    while not _bg_stop.is_set():
        try:
            await asyncio.to_thread(scheduler.run_tick)
        except Exception as exc:  # noqa: BLE001
            log_event(phase="BACKGROUND", step="tick", verdict="UNDEFINED", error=str(exc))
        try:
            await asyncio.wait_for(_bg_stop.wait(), timeout=interval)
        except asyncio.TimeoutError:
            pass


@asynccontextmanager
async def lifespan(app: FastAPI):
    global _bg_task, _bg_stop
    settings = get_settings()
    configure_logging(settings.log_level)
    pool = init_pool(settings.database_url)
    with pool.connection() as conn:
        apply_schema(conn)
    scheduler = Scheduler(pool, settings, clock=SystemClock())
    app.state.pool = pool
    app.state.settings = settings
    app.state.scheduler = scheduler
    app.state.clock = SystemClock()

    _bg_stop = asyncio.Event()
    if settings.scheduler_enabled:
        _bg_task = asyncio.create_task(_background_loop(scheduler, settings.tick_interval_seconds))
    log_event(phase="STARTUP", step="lifespan", verdict="OK",
              code_version=settings.code_version, scheduler_enabled=settings.scheduler_enabled)
    try:
        yield
    finally:
        if _bg_task:
            _bg_stop.set()
            _bg_task.cancel()
        close_pool()


def create_app() -> FastAPI:
    app = FastAPI(title="Persistent Recurring Scheduler", version="1.0.0", lifespan=lifespan)
    register_exception_handlers(app)

    @app.middleware("http")
    async def request_id_mw(request: Request, call_next):
        request.state.request_id = request.headers.get("x-request-id") or str(uuid.uuid4())
        try:
            response = await call_next(request)
        except Exception:
            raise
        response.headers["x-request-id"] = request.state.request_id
        return response

    @app.get("/healthz")
    def healthz(request: Request):
        with request.app.state.pool.connection() as conn:
            conn.execute("SELECT 1")
        return {"status": "ok", "time_utc": datetime.now(timezone.utc).isoformat(),
                "code_version": request.app.state.settings.code_version}

    app.include_router(schedules.router)
    app.include_router(admin.router)
    return app


app = create_app()
