"""物化：纯理论 Occurrence 集合 ↔ triggers 表的身份对账。

必须在持有 pg_try_advisory_xact_lock('mat:'+schedule_id) 的事务中调用。
- 新身份：插入（真实→PENDING；gap/missing→SKIPPED）
- 已存在：保留状态；CANCELLED 身份回归 → rearm；合并信息挂胜出行
- 窗口内消失的未来 PENDING → CANCELLED/SUPERSEDED；终态不可变
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from datetime import datetime
from uuid import UUID

import psycopg

from app.planner import identity as ident
from app.planner.identity import real_trigger_id
from app.planner.service import (
    O_FIRE,
    O_FORCE,
    O_SKIPPED_EXCEPTION,
    O_SKIPPED_GAP,
    O_SKIPPED_MISSING,
    Occurrence,
    Plan,
)
from app.state import audit
from app.state.repositories import TriggerRepository


@dataclass
class ReconcileResult:
    inserted: int = 0
    rearmed: int = 0
    cancelled: int = 0
    merged: int = 0
    skipped_born: int = 0
    skipped_exception: int = 0
    existing: int = 0
    errors: list[str] = field(default_factory=list)


def _occurrence_id(schedule_id: UUID, o: Occurrence) -> UUID:
    return ident.trigger_id(schedule_id, o.id_name)


def materialize_schedule(
    conn: psycopg.Connection,
    *,
    schedule_id: UUID,
    version: int,
    plan: Plan,
    window_end: datetime,
    now: datetime,
    tz_name: str,
) -> ReconcileResult:
    repo = TriggerRepository(conn)
    if not repo.try_materialize_lock(schedule_id):
        return ReconcileResult(errors=["materialize lock busy; skipped this tick"])

    res = ReconcileResult()
    identity_ids: list[UUID] = []
    id_to_occ: dict[UUID, Occurrence] = {}

    for o in plan.occurrences:
        tid = _occurrence_id(schedule_id, o)
        identity_ids.append(tid)
        id_to_occ[tid] = o

    existing = {r["id"]: r for r in repo.list_identity_rows(schedule_id, window_end)}

    for tid, o in id_to_occ.items():
        if tid in existing:
            row = existing[tid]
            res.existing += 1
            if row["status"] == "CANCELLED":
                # 身份回归且从未执行 → 重新武装；终态（SUCCEEDED/FAILED/SKIPPED）永不复活
                if o.due_at is not None:
                    repo.rearm(tid, version, now)
                    res.rearmed += 1
                    audit.record(conn, "schedule", "TRIGGER_REARMED", entity_id=str(schedule_id),
                                 detail={"trigger_id": str(tid),
                                         "wall": o.local_wall.isoformat()})
            continue

        # 新身份
        if o.kind in (O_SKIPPED_GAP, O_SKIPPED_MISSING):
            repo.upsert_identity({
                "id": tid, "schedule_id": schedule_id, "schedule_version": version,
                "occurrence_no": o.ordinal, "local_wall_ts": o.local_wall,
                "tz_name": tz_name, "due_at": None,
                "synthetic_key": o.synthetic_key,
                "status": "SKIPPED", "skip_reason": "DST_GAP" if o.kind == O_SKIPPED_GAP else "MISSING_DATE",
                "merged_occurrences": [],
            })
            res.skipped_born += 1
        elif o.kind == O_SKIPPED_EXCEPTION:
            # 被日期/时刻例外跳过：CANCELLED/EXCEPTION（可 rearm：移除例外后该身份回归可再触发）
            repo.upsert_identity({
                "id": tid, "schedule_id": schedule_id, "schedule_version": version,
                "occurrence_no": o.ordinal, "local_wall_ts": o.local_wall,
                "tz_name": tz_name, "due_at": None,
                "synthetic_key": "x:" + o.id_name,
                "status": "CANCELLED", "skip_reason": "EXCEPTION",
                "merged_occurrences": [],
            })
            res.skipped_exception += 1
        else:
            repo.upsert_identity({
                "id": tid, "schedule_id": schedule_id, "schedule_version": version,
                "occurrence_no": o.ordinal, "local_wall_ts": (o.resolved_wall or o.local_wall),
                "tz_name": tz_name, "due_at": o.due_at,
                "synthetic_key": o.synthetic_key,
                "status": "PENDING", "skip_reason": None,
                "merged_occurrences": [],
            })
            res.inserted += 1

    # 合并审计信息挂到胜出行
    for m in plan.merged:
        winner_id = real_trigger_id(schedule_id, _parse_iso(m["due_at"]))
        if winner_id in identity_ids:
            repo.attach_merge(winner_id, m["losers"])
            res.merged += 1
            audit.record(conn, "trigger", "OCCURRENCE_MERGED", entity_id=str(winner_id),
                         detail=m)

    # 消失的未来 PENDING（含旧版本残留）→ SUPERSEDED；终态保留
    res.cancelled = repo.cancel_future_pending(
        schedule_id, identity_ids, window_end, version)
    if res.cancelled:
        audit.record(conn, "schedule", "TRIGGERS_SUPERSEDED", entity_id=str(schedule_id),
                     detail={"count": res.cancelled, "version": version})

    audit.record(conn, "schedule", "MATERIALIZE_RECONCILE", entity_id=str(schedule_id),
                 detail={"inserted": res.inserted, "rearmed": res.rearmed,
                         "cancelled": res.cancelled, "merged": res.merged,
                         "skipped_born": res.skipped_born,
                         "skipped_exception": res.skipped_exception,
                         "existing": res.existing, "version": version})
    return res


def _parse_iso(s: str) -> datetime:
    from datetime import timezone

    return datetime.fromisoformat(s).astimezone(timezone.utc)
