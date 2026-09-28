"""FastAPI application: protocol entry points + error contract.

Routes (see README for example payloads):

    POST   /schedules                 create
    GET    /schedules                 list
    GET    /schedules/{id}            get (with spec)
    PUT    /schedules/{id}            update (new version; old pending cancel)
    POST   /schedules/{id}/pause
    POST   /schedules/{id}/resume
    DELETE /schedules/{id}
    GET    /schedules/{id}/fires      planned/confirmed fires
    GET    /fires/{fire_key}
    POST   /fires/{fire_key}/retry    failed/expired only
    GET    /runs                     delivery records (optional ?schedule_id=)
    GET    /runs/{id}
    GET    /audit                    audit trail (?schedule_id=&event=&limit=)
    POST   /ticks                    run a scheduler tick (?now= ISO-8601)
    GET    /runtime                  watermark + last tick result
    GET    /healthz                  liveness
"""
from __future__ import annotations

from datetime import datetime, timezone
from typing import Any

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from .engine.core import SchedulerEngine
from .errors import AppError

UTC = timezone.utc


def create_app(engine: SchedulerEngine) -> FastAPI:
    app = FastAPI(
        title="Persistent recurring scheduler",
        version="1.0.0",
        description="Time-zone/weekday/exception-aware recurring scheduler.",
    )
    app.state.engine = engine

    @app.exception_handler(AppError)
    async def _app_error(_: Request, exc: AppError) -> JSONResponse:
        return JSONResponse(status_code=exc.http_status, content=exc.to_dict())

    @app.get("/healthz")
    async def healthz() -> dict[str, Any]:
        rt = engine.repo.get_runtime()
        return {"ok": True, "service": "scheduler", "runtime": rt}

    # ── schedules ──────────────────────────────────────────────────────────

    @app.post("/schedules", status_code=201)
    async def create_schedule(payload: dict, request: Request) -> dict:
        now = _now(request)
        return engine.create_schedule(payload, now=now)

    @app.get("/schedules")
    async def list_schedules() -> dict:
        return {"schedules": engine.repo.list_schedules()}

    @app.get("/schedules/{schedule_id}")
    async def get_schedule(schedule_id: str) -> dict:
        return engine.repo.get_schedule(schedule_id)

    @app.put("/schedules/{schedule_id}")
    async def update_schedule(schedule_id: str, payload: dict, request: Request) -> dict:
        return engine.update_schedule(schedule_id, payload, now=_now(request))

    @app.post("/schedules/{schedule_id}/pause")
    async def pause(schedule_id: str, request: Request) -> dict:
        return engine.set_paused(schedule_id, True, now=_now(request))

    @app.post("/schedules/{schedule_id}/resume")
    async def resume(schedule_id: str, request: Request) -> dict:
        return engine.set_paused(schedule_id, False, now=_now(request))

    @app.delete("/schedules/{schedule_id}", status_code=204)
    async def delete_schedule(schedule_id: str, request: Request) -> JSONResponse:
        engine.delete_schedule(schedule_id, now=_now(request))
        return JSONResponse(status_code=204, content=None)

    @app.get("/schedules/{schedule_id}/fires")
    async def list_fires(schedule_id: str, limit: int = 200) -> dict:
        engine.repo.get_schedule(schedule_id)  # 404 if missing
        return {"fires": engine.repo.list_fires(schedule_id, limit=limit)}

    # ── fires / runs / audit ───────────────────────────────────────────────

    @app.get("/fires/{fire_key}")
    async def get_fire(fire_key: str) -> dict:
        return engine.repo.get_fire_by_key(fire_key)

    @app.post("/fires/{fire_key}/retry")
    async def retry_fire(fire_key: str, request: Request) -> dict:
        return engine.retry_fire(fire_key, now=_now(request))

    @app.get("/runs")
    async def list_runs(schedule_id: str | None = None, limit: int = 100) -> dict:
        return {"runs": engine.repo.list_runs(schedule_id, limit=limit)}

    @app.get("/runs/{run_id}")
    async def get_run(run_id: str) -> dict:
        return engine.repo.get_run(run_id)

    @app.get("/audit")
    async def audit(schedule_id: str | None = None, event: str | None = None,
                    limit: int = 100) -> dict:
        return {
            "events": engine.repo.list_audit(
                schedule_id=schedule_id, event=event, limit=limit
            )
        }

    # ── control plane ──────────────────────────────────────────────────────

    @app.post("/ticks")
    async def tick(request: Request, now: str | None = None) -> dict:
        parsed = _parse_iso(now) if now else None
        return engine.tick(parsed if parsed else _now(request))

    @app.get("/runtime")
    async def runtime() -> dict:
        return engine.repo.get_runtime()

    return app


def _parse_iso(raw: str) -> datetime:
    """Tolerant ISO-8601 parse (a '+' in a query string may arrive as ' ')."""
    from .errors import ValidationError

    try:
        return datetime.fromisoformat(raw.strip().replace(" ", "+"))
    except ValueError:
        # a single space may be a separator rather than an offset: retry
        try:
            return datetime.fromisoformat(raw.strip())
        except ValueError as exc:
            raise ValidationError(
                f"invalid ISO-8601 timestamp: {raw!r}",
                field="now", code="VAL-TIMESTAMP",
            ) from exc


def _now(request: Request) -> datetime:
    """Deterministic clock hook: ``X-Test-Now: <iso8601>`` overrides wall time.

    In production no header is sent and the engine uses real UTC. Tests pin
    time per request so scenarios (DST nights, multi-day downtime) are
    reproducible without sleeping.
    """
    override = request.headers.get("x-test-now")
    if override:
        dt = datetime.fromisoformat(override)
        return dt if dt.tzinfo else dt.replace(tzinfo=UTC)
    return datetime.now(UTC)
