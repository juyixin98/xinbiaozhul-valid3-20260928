"""应用服务：API 与内核之间的编排（规格校验、密度检查、版本化写库）。"""
from __future__ import annotations

import uuid
from datetime import datetime, timedelta, timezone

from psycopg_pool import ConnectionPool

from app.errors import StateConflict
from app.planner.service import check_density, validate_full_plan, validate_spec
from app.planner.tzutil import tzdata_version
from app.schemas import ScheduleSpec
from app.state import audit
from app.state.repositories import ScheduleRepository

UTC = timezone.utc


def spec_to_json(spec: ScheduleSpec) -> dict:
    return spec.model_dump(mode="json")


def create_schedule(pool: ConnectionPool, spec: ScheduleSpec, *, now: datetime,
                    max_occurrences: int) -> str:
    validate_spec(spec)
    check_density(spec, max_occurrences)
    # 确定性拒绝 FORCE 重复/fold 冲突（用临时 id 跑一次理论计划）
    validate_full_plan(spec, uuid.uuid4(), max_occurrences)
    sid = uuid.uuid4()
    data = spec_to_json(spec)
    with pool.connection() as conn, conn.transaction():
        ScheduleRepository(conn).create(sid=sid, spec=data, tzdata_ver=tzdata_version(), now=now)
        audit.record(conn, "schedule", "SCHEDULE_CREATED", entity_id=str(sid),
                     detail={"name": spec.name, "tz": spec.timezone,
                             "tzdata_version": tzdata_version()})
    return str(sid)


def update_schedule(pool, sid: uuid.UUID, expected_version: int, spec: ScheduleSpec, *,
                    now: datetime, max_occurrences: int) -> int:
    validate_spec(spec)
    check_density(spec, max_occurrences)
    validate_full_plan(spec, sid, max_occurrences)
    data = spec_to_json(spec)
    with pool.connection() as conn, conn.transaction():
        current = ScheduleRepository(conn).get(sid)
        if current.status == "DELETED":
            raise StateConflict("cannot update a deleted schedule")
        new_version = ScheduleRepository(conn).update_spec(
            sid, expected_version=expected_version, spec=data,
            tzdata_ver=tzdata_version(), now=now)
        audit.record(conn, "schedule", "SCHEDULE_UPDATED", entity_id=str(sid),
                     detail={"old_version": expected_version, "new_version": new_version})
    return new_version


def set_status(pool, sid: uuid.UUID, status: str, *, now: datetime) -> None:
    with pool.connection() as conn, conn.transaction():
        ScheduleRepository(conn).set_status(sid, status, now)
        audit.record(conn, "schedule", f"SCHEDULE_{status}", entity_id=str(sid))
