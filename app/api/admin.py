"""运维与预览：手动 tick、理论时间线、runs、audit、健康检查。"""
from __future__ import annotations

import uuid
from datetime import datetime, timedelta, timezone

from fastapi import APIRouter, Depends, Query, Request
from psycopg_pool import ConnectionPool

from app.api.deps import pool, scheduler
from app.config import Settings
from app.planner.service import PlanRequest, plan as build_plan
from app.schemas import ScheduleSpec, TickRequest
from app.api.deps import settings as settings_dep
from app.state.repositories import RunRepository, ScheduleRepository
from app.schemas import ScheduleSpec as _SS  # noqa: F401

router = APIRouter(prefix="/api/v1")


@router.post("/admin/ticks")
def run_tick(payload: TickRequest | None = None, request: Request = None,
             db: ConnectionPool = Depends(pool), sched=Depends(scheduler)):
    payload = payload or TickRequest()
    override_now = payload.now.astimezone(timezone.utc) if payload.now else None
    only = uuid.UUID(payload.schedule_id) if payload.schedule_id else None
    result = sched.run_tick(only_schedule=only, override_now=override_now)
    return result.to_dict()


@router.get("/schedules/{sid}/timeline")
def timeline(sid: str,
             start: datetime | None = Query(None),
             end: datetime | None = Query(None),
             request: Request = None, db: ConnectionPool = Depends(pool),
             cfg: Settings = Depends(settings_dep)):
    """纯理论预览：不写库。打印每个出现的本地墙钟→UTC 判定。"""
    with db.connection() as conn:
        row = ScheduleRepository(conn).get(uuid.UUID(sid))
    now = datetime.now(timezone.utc)
    ws = start.astimezone(timezone.utc) if start and start.tzinfo else (
        start.replace(tzinfo=timezone.utc) if start else now - timedelta(days=1))
    we = end.astimezone(timezone.utc) if end and end.tzinfo else (
        end.replace(tzinfo=timezone.utc) if end else now + timedelta(days=cfg.horizon_days))
    spec = ScheduleSpec.model_validate(row.spec)
    p = build_plan(PlanRequest(
        schedule_id=row.id, spec=spec, window_start_utc=ws, window_end_utc=we,
        max_occurrences=cfg.max_occurrences_per_window))
    return {
        "schedule_id": sid,
        "tzdata_version": p.tzdata_version,
        "window": {"start_utc": ws.isoformat(), "end_utc": we.isoformat()},
        "warnings": list(p.warnings),
        "merged": list(p.merged),
        "occurrences": [{
            "ordinal": o.ordinal,
            "local_wall": o.local_wall.isoformat(),
            "requested_ymd": list(o.requested_ymd) if o.requested_ymd else None,
            "kind": o.kind,
            "resolution": o.resolution_kind,
            "resolved_fold": o.resolved_fold,
            "resolved_local_wall": o.resolved_wall.isoformat() if o.resolved_wall else None,
            "due_at_utc": o.due_at.isoformat() if o.due_at else None,
            "utc_offset_minutes": o.utc_offset_minutes,
            "exception_key": o.exception_key,
        } for o in p.occurrences],
    }


@router.get("/runs")
def list_runs(limit: int = Query(50, ge=1, le=500), db: ConnectionPool = Depends(pool)):
    with db.connection() as conn:
        rows = RunRepository(conn).list_recent(limit)
    return {"items": [{
        "run_id": str(r["run_id"]), "worker_id": r["worker_id"],
        "code_version": r["code_version"], "tzdata_version": r["tzdata_version"],
        "observed_now": r["observed_now"].isoformat(),
        "started_at": r["started_at"].isoformat(),
        "ended_at": r["ended_at"].isoformat() if r["ended_at"] else None,
        "status": r["status"], "stats": r["stats"],
    } for r in rows]}


@router.get("/audit")
def list_audit(entity_id: str | None = Query(None),
               limit: int = Query(100, ge=1, le=1000),
               db: ConnectionPool = Depends(pool)):
    sql = "SELECT * FROM audit_events"
    params: list = []
    if entity_id:
        sql += " WHERE entity_id=%s"
        params.append(entity_id)
    sql += " ORDER BY ts DESC LIMIT %s"
    params.append(limit)
    with db.connection() as conn:
        rows = conn.execute(sql, params).fetchall()
    return {"items": [{
        "ts": r["ts"].isoformat(), "entity": r["entity"], "entity_id": r["entity_id"],
        "action": r["action"], "detail": r["detail"],
        "run_id": str(r["run_id"]) if r["run_id"] else None,
    } for r in rows]}
