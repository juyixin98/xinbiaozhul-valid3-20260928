"""计划 CRUD 与触发器只读接口。"""
from __future__ import annotations

import uuid
from datetime import datetime, timezone

from fastapi import APIRouter, Depends, Query, Request
from psycopg_pool import ConnectionPool

from app.api.deps import pool, settings
from app.config import Settings
from app.errors import NotFound
from app.schemas import ScheduleCreateResponse, ScheduleSpec, ScheduleUpdate
from app import services
from app.state.repositories import ScheduleRepository, TriggerRepository, list_executions

router = APIRouter(prefix="/api/v1")


def _serialize_schedule(r) -> dict:
    return {
        "id": str(r["id"]),
        "name": r["name"],
        "version": r["version"],
        "status": r["status"],
        "tzdata_version": r["tzdata_version"],
        "recovery_epoch": r["recovery_epoch"],
        "last_healthy_tick_at": r["last_healthy_tick_at"].isoformat() if r["last_healthy_tick_at"] else None,
        "spec": r["spec"],
        "created_at": r["created_at"].isoformat(),
        "updated_at": r["updated_at"].isoformat(),
    }


@router.post("/schedules", response_model=ScheduleCreateResponse, status_code=201)
def create_schedule(payload: ScheduleSpec, request: Request,
                    db: ConnectionPool = Depends(pool), cfg: Settings = Depends(settings)):
    now = datetime.now(timezone.utc)
    sid = services.create_schedule(
        db, payload, now=now, max_occurrences=cfg.max_occurrences_per_window)
    return ScheduleCreateResponse(id=sid, version=1)


@router.get("/schedules")
def list_schedules(request: Request, db: ConnectionPool = Depends(pool),
                   limit: int = Query(100, ge=1, le=500)):
    with db.connection() as conn:
        rows = conn.execute(
            "SELECT * FROM schedules WHERE status <> 'DELETED' ORDER BY created_at DESC LIMIT %s",
            (limit,),
        ).fetchall()
    return {"items": [_serialize_schedule(r) for r in rows]}


@router.get("/schedules/{sid}")
def get_schedule(sid: str, request: Request, db: ConnectionPool = Depends(pool)):
    with db.connection() as conn:
        row = ScheduleRepository(conn).get(uuid.UUID(sid))
    return _serialize_schedule(row)


@router.put("/schedules/{sid}")
def update_schedule(sid: str, payload: ScheduleUpdate, request: Request,
                    db: ConnectionPool = Depends(pool), cfg: Settings = Depends(settings)):
    now = datetime.now(timezone.utc)
    new_version = services.update_schedule(
        db, uuid.UUID(sid), payload.expected_version, payload.spec, now=now,
        max_occurrences=cfg.max_occurrences_per_window)
    return {"id": sid, "version": new_version}


@router.post("/schedules/{sid}/pause")
def pause_schedule(sid: str, request: Request, db: ConnectionPool = Depends(pool)):
    services.set_status(db, uuid.UUID(sid), "PAUSED", now=datetime.now(timezone.utc))
    return {"id": sid, "status": "PAUSED"}


@router.post("/schedules/{sid}/resume")
def resume_schedule(sid: str, request: Request, db: ConnectionPool = Depends(pool)):
    services.set_status(db, uuid.UUID(sid), "ACTIVE", now=datetime.now(timezone.utc))
    return {"id": sid, "status": "ACTIVE"}


@router.delete("/schedules/{sid}")
def delete_schedule(sid: str, request: Request, db: ConnectionPool = Depends(pool)):
    services.set_status(db, uuid.UUID(sid), "DELETED", now=datetime.now(timezone.utc))
    return {"id": sid, "status": "DELETED"}


# ---------------------------------------------------------------- 触发器

def _serialize_trigger(t: dict) -> dict:
    return {
        "id": str(t["id"]),
        "schedule_id": str(t["schedule_id"]),
        "schedule_version": t["schedule_version"],
        "occurrence_no": t["occurrence_no"],
        "local_wall": t["local_wall_ts"].isoformat(),
        "due_at_utc": t["due_at"].isoformat() if t["due_at"] else None,
        "synthetic_key": t["synthetic_key"],
        "status": t["status"],
        "skip_reason": t["skip_reason"],
        "attempts": t["attempts"],
        "recovery_epoch": t["recovery_epoch"],
        "backlog_rank": t["backlog_rank"],
        "last_error": t["last_error"],
        "merged_occurrences": t["merged_occurrences"],
    }


@router.get("/schedules/{sid}/triggers")
def list_triggers(sid: str, status: str | None = Query(None),
                  limit: int = Query(200, ge=1, le=2000),
                  db: ConnectionPool = Depends(pool)):
    with db.connection() as conn:
        rows = TriggerRepository(conn).list_for_schedule(
            uuid.UUID(sid), status=status, limit=limit)
    return {"items": [_serialize_trigger(t) for t in rows]}


@router.get("/triggers/{tid}")
def get_trigger(tid: str, db: ConnectionPool = Depends(pool)):
    with db.connection() as conn:
        t = TriggerRepository(conn).get(uuid.UUID(tid))
    return _serialize_trigger(t)


@router.get("/triggers/{tid}/executions")
def get_executions(tid: str, db: ConnectionPool = Depends(pool)):
    with db.connection() as conn:
        rows = list_executions(conn, uuid.UUID(tid))
    return {"items": [{
        "id": str(r["id"]), "attempt_no": r["attempt_no"], "worker_id": r["worker_id"],
        "run_id": str(r["run_id"]) if r["run_id"] else None,
        "started_at": r["started_at"].isoformat(),
        "finished_at": r["finished_at"].isoformat() if r["finished_at"] else None,
        "status": r["status"], "code": r["code"], "result": r["result"], "error": r["error"],
    } for r in rows]}
