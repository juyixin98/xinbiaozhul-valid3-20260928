"""审计事件写入（必须在调用方事务内，随业务一起提交/回滚）。"""
from __future__ import annotations

import json
from uuid import UUID

import psycopg


def record(
    conn: psycopg.Connection,
    entity: str,
    action: str,
    *,
    entity_id: str | None = None,
    detail: dict | None = None,
    run_id: UUID | None = None,
) -> None:
    conn.execute(
        """
        INSERT INTO audit_events(entity, entity_id, action, detail, run_id)
        VALUES (%s, %s, %s, %s::jsonb, %s)
        """,
        (entity, entity_id, action, json.dumps(detail or {}, default=str), run_id),
    )
