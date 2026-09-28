"""PostgreSQL 仓储：所有 SQL 集中于此，参数化，返回 dict/dataclass。不 import planner。"""
from __future__ import annotations

import json
import uuid
from dataclasses import dataclass
from datetime import datetime
from typing import Any, Iterator

import psycopg

from app.errors import NotFound


# ---------------------------------------------------------------- 记录类型

@dataclass
class ScheduleRow:
    id: uuid.UUID
    name: str
    version: int
    spec: dict
    status: str
    tzdata_version: str
    recovery_epoch: int
    recovery_started_at: datetime | None
    last_healthy_tick_at: datetime | None


def row_to_schedule(r: dict) -> ScheduleRow:
    return ScheduleRow(
        id=r["id"], name=r["name"], version=r["version"], spec=r["spec"],
        status=r["status"], tzdata_version=r["tzdata_version"],
        recovery_epoch=r["recovery_epoch"],
        recovery_started_at=r["recovery_started_at"],
        last_healthy_tick_at=r["last_healthy_tick_at"],
    )


# ---------------------------------------------------------------- Schedules

class ScheduleRepository:
    def __init__(self, conn: psycopg.Connection):
        self.conn = conn

    def create(self, *, sid: uuid.UUID, spec: dict, tzdata_ver: str, now: datetime) -> None:
        self.conn.execute(
            """
            INSERT INTO schedules(id, name, version, spec, status, tzdata_version,
                                  last_healthy_tick_at, created_at, updated_at)
            VALUES (%s, %s, 1, %s::jsonb, 'ACTIVE', %s, %s, %s, %s)
            """,
            (sid, spec.get("name"), json.dumps(spec), tzdata_ver, now, now, now),
        )
        self.conn.execute(
            """INSERT INTO schedule_revisions(schedule_id, version, spec, tzdata_version, created_at)
               VALUES (%s, 1, %s::jsonb, %s, %s)""",
            (sid, json.dumps(spec), tzdata_ver, now),
        )
        self.conn.execute(
            "INSERT INTO scheduler_heartbeat(schedule_id, last_tick_at) VALUES (%s, %s) "
            "ON CONFLICT (schedule_id) DO NOTHING",
            (sid, now),
        )

    def get(self, sid: uuid.UUID, *, include_deleted: bool = False) -> ScheduleRow:
        sql = "SELECT * FROM schedules WHERE id=%s"
        if not include_deleted:
            sql += " AND status <> 'DELETED'"
        row = self.conn.execute(sql, (sid,)).fetchone()
        if row is None:
            raise NotFound(f"schedule {sid} not found")
        return row_to_schedule(row)

    def list_active(self) -> list[ScheduleRow]:
        rows = self.conn.execute(
            "SELECT * FROM schedules WHERE status <> 'DELETED' ORDER BY id"
        ).fetchall()
        return [row_to_schedule(r) for r in rows]

    def update_spec(self, sid: uuid.UUID, *, expected_version: int, spec: dict,
                    tzdata_ver: str, now: datetime) -> int:
        row = self.conn.execute(
            "SELECT version, status FROM schedules WHERE id=%s FOR UPDATE", (sid,)
        ).fetchone()
        if row is None:
            raise NotFound(f"schedule {sid} not found")
        if row["version"] != expected_version:
            from app.errors import VersionConflict

            raise VersionConflict(
                f"version conflict: expected {expected_version}, current {row['version']}",
                {"expected": expected_version, "current": row["version"]},
            )
        new_version = expected_version + 1
        self.conn.execute(
            "UPDATE schedules SET spec=%s::jsonb, name=%s, version=%s, tzdata_version=%s, updated_at=%s "
            "WHERE id=%s",
            (json.dumps(spec), spec.get("name"), new_version, tzdata_ver, now, sid),
        )
        self.conn.execute(
            """INSERT INTO schedule_revisions(schedule_id, version, spec, tzdata_version, created_at)
               VALUES (%s, %s, %s::jsonb, %s, %s)""",
            (sid, new_version, json.dumps(spec), tzdata_ver, now),
        )
        return new_version

    def set_status(self, sid: uuid.UUID, status: str, now: datetime) -> None:
        n = self.conn.execute(
            "UPDATE schedules SET status=%s, updated_at=%s WHERE id=%s AND status <> 'DELETED'",
            (status, now, sid),
        ).rowcount
        if n == 0:
            raise NotFound(f"schedule {sid} not found")

    def mark_heartbeat(self, sid: uuid.UUID, now: datetime) -> None:
        self.conn.execute(
            "INSERT INTO scheduler_heartbeat(schedule_id, last_tick_at) VALUES (%s,%s) "
            "ON CONFLICT (schedule_id) DO UPDATE SET last_tick_at=EXCLUDED.last_tick_at",
            (sid, now),
        )

    def get_heartbeat(self, sid: uuid.UUID) -> datetime | None:
        r = self.conn.execute(
            "SELECT last_tick_at FROM scheduler_heartbeat WHERE schedule_id=%s", (sid,)
        ).fetchone()
        return r["last_tick_at"] if r else None

    def open_recovery_epoch(self, sid: uuid.UUID, now: datetime) -> int:
        r = self.conn.execute(
            """UPDATE schedules
               SET recovery_epoch = recovery_epoch + 1, recovery_started_at = %s
               WHERE id=%s RETURNING recovery_epoch""",
            (now, sid),
        ).fetchone()
        return r["recovery_epoch"]

    def get_recovery(self, sid: uuid.UUID) -> tuple[int, datetime | None]:
        r = self.conn.execute(
            "SELECT recovery_epoch, recovery_started_at FROM schedules WHERE id=%s", (sid,)
        ).fetchone()
        return r["recovery_epoch"], r["recovery_started_at"]


# ---------------------------------------------------------------- Triggers

class TriggerRepository:
    def __init__(self, conn: psycopg.Connection):
        self.conn = conn

    def upsert_identity(self, rec: dict) -> str:
        """按身份 upsert。返回 'inserted' | 'existing'。仅由持 advisory 锁的物化事务调用。"""
        cols = [
            "id", "schedule_id", "schedule_version", "occurrence_no", "local_wall_ts",
            "tz_name", "due_at", "synthetic_key", "status", "skip_reason",
            "merged_occurrences",
        ]
        sql = f"""
            INSERT INTO triggers({', '.join(cols)}, created_at, updated_at)
            VALUES (%(id)s, %(schedule_id)s, %(schedule_version)s, %(occurrence_no)s,
                    %(local_wall_ts)s, %(tz_name)s, %(due_at)s, %(synthetic_key)s,
                    %(status)s, %(skip_reason)s, %(merged_occurrences)s::jsonb, now(), now())
            ON CONFLICT (id) DO NOTHING
            RETURNING id
        """
        params = {c: rec.get(c) for c in cols}
        if params.get("merged_occurrences") is None:
            params["merged_occurrences"] = "[]"
        row = self.conn.execute(sql, params).fetchone()
        return "inserted" if row else "existing"

    def rearm(self, trigger_id: uuid.UUID, version: int, now: datetime) -> None:
        """CANCELLED 身份在新版本回归 → 重新武装（从未执行，安全）。"""
        self.conn.execute(
            """UPDATE triggers
               SET status='PENDING', skip_reason=NULL, schedule_version=%s,
                   recovery_epoch=0, backlog_rank=NULL, not_before=NULL, updated_at=%s
               WHERE id=%s AND status='CANCELLED'""",
            (version, now, trigger_id),
        )

    def attach_merge(self, trigger_id: uuid.UUID, losers: list[dict]) -> None:
        self.conn.execute(
            "UPDATE triggers SET merged_occurrences = %s::jsonb, updated_at=now() WHERE id=%s",
            (json.dumps(losers), trigger_id),
        )

    def list_identity_rows(self, schedule_id: uuid.UUID, window_end: datetime) -> list[dict]:
        """窗口内（due_at < window_end，含合成项）全部触发器身份，供对账。"""
        return self.conn.execute(
            """SELECT id, status, due_at, synthetic_key, schedule_version
               FROM triggers
               WHERE schedule_id=%s AND (
                   due_at IS NULL OR due_at < %s
               )""",
            (schedule_id, window_end),
        ).fetchall()

    def cancel_future_pending(self, schedule_id: uuid.UUID, keep_ids: list[uuid.UUID],
                              window_end: datetime, version: int) -> int:
        """旧版本残留的 PENDING → CANCELLED/SUPERSEDED；以及窗口内不再属于新身份集合的
        PENDING。终态（SUCCEEDED/FAILED/SKIPPED）与新版本行绝不动。"""
        return self.conn.execute(
            """UPDATE triggers
               SET status='CANCELLED', skip_reason='SUPERSEDED', updated_at=now()
               WHERE schedule_id=%s AND status='PENDING'
                 AND (
                   schedule_version <> %s
                   OR ((due_at IS NULL OR due_at < %s) AND id <> ALL(%s))
                 )""",
            (schedule_id, version, window_end, keep_ids),
        ).rowcount

    def list_for_schedule(self, schedule_id: uuid.UUID, *, status: str | None = None,
                          limit: int = 200) -> list[dict]:
        sql = "SELECT * FROM triggers WHERE schedule_id=%s"
        params: list[Any] = [schedule_id]
        if status:
            sql += " AND status=%s"
            params.append(status)
        sql += " ORDER BY COALESCE(due_at, local_wall_ts) LIMIT %s"
        params.append(limit)
        return self.conn.execute(sql, params).fetchall()

    def get(self, trigger_id: uuid.UUID) -> dict:
        row = self.conn.execute("SELECT * FROM triggers WHERE id=%s", (trigger_id,)).fetchone()
        if row is None:
            raise NotFound(f"trigger {trigger_id} not found")
        return row

    def try_materialize_lock(self, schedule_id: uuid.UUID) -> bool:
        """事务级咨询锁：'mat:'+schedule_id 的 hashtext。败者本 tick 跳过该计划。"""
        row = self.conn.execute(
            "SELECT pg_try_advisory_xact_lock(hashtext('mat:' || %s::text)) AS got",
            (str(schedule_id),),
        ).fetchone()
        return bool(row["got"])

    # ---------------- 派发 ----------------

    def reclaim_stale(self, *, now: datetime, lease_timeout: float, run_id: uuid.UUID,
                      worker_id: str, max_attempts: int, backoff_base: float = 2.0) -> int:
        """RUNNING 超过租约 → PENDING(退避) 或 FAILED。返回回收行数。"""
        stale = self.conn.execute(
            """SELECT id, attempts FROM triggers
               WHERE status='RUNNING' AND claim_expires_at < %s
               FOR UPDATE SKIP LOCKED""",
            (now,),
        ).fetchall()
        for t in stale:
            new_attempts = t["attempts"]  # attempts 在认领时已 +1
            if new_attempts >= max_attempts:
                self.conn.execute(
                    "UPDATE triggers SET status='FAILED', last_error='lease expired; attempts exhausted', "
                    "updated_at=now() WHERE id=%s",
                    (t["id"],),
                )
                self.conn.execute(
                    """UPDATE executions SET status='LEASE_EXPIRED', finished_at=now(),
                       code='LEASE_EXPIRED', error='worker lease expired; attempts exhausted'
                       WHERE trigger_id=%s AND status='RUNNING'""",
                    (t["id"],),
                )
            else:
                delay = min(backoff_base ** new_attempts, 300.0)
                self.conn.execute(
                    "UPDATE triggers SET status='PENDING', not_before=%s, updated_at=now() WHERE id=%s",
                    (self._timestamp(now, delay), t["id"]),
                )
                self.conn.execute(
                    """UPDATE executions SET status='LEASE_EXPIRED', finished_at=now(),
                       code='LEASE_EXPIRED', error='worker lease expired; requeued with backoff'
                       WHERE trigger_id=%s AND status='RUNNING'""",
                    (t["id"],),
                )
        return len(stale)

    @staticmethod
    def _timestamp(now: datetime, seconds: float) -> datetime:
        from datetime import timedelta

        return now + timedelta(seconds=seconds)

    def assign_backlog(self, *, schedule_id: uuid.UUID, epoch: int, now: datetime,
                       grace: float) -> int:
        """给积压 PENDING（age>grace）打 epoch 与密集 rank。返回行数。"""
        self.conn.execute(
            """UPDATE triggers SET recovery_epoch=%s, backlog_rank=NULL
               WHERE schedule_id=%s AND status='PENDING' AND recovery_epoch<>%s""",
            (epoch, schedule_id, epoch),
        )
        rows = self.conn.execute(
            """SELECT id FROM triggers
               WHERE schedule_id=%s AND status='PENDING' AND due_at IS NOT NULL
                 AND due_at < %s AND recovery_epoch=%s AND backlog_rank IS NULL
               ORDER BY due_at, id""",
            (schedule_id, self._timestamp(now, -grace), epoch),
        ).fetchall()
        for rank, r in enumerate(rows, start=1):
            self.conn.execute(
                "UPDATE triggers SET backlog_rank=%s WHERE id=%s", (rank, r["id"])
            )
        return len(rows)

    def mark_overflow(self, *, schedule_id: uuid.UUID, epoch: int, now: datetime,
                      grace: float, deadline: float, rate_limit: int,
                      tick_interval: float, recovery_started_at: datetime) -> int:
        """容量公式：rank r 最早可服务于 recovery_start + ceil(r/B)*T；
        若 due_at+deadline < 该时刻，则永远赶不上 → OVERFLOW。"""
        import math

        rows = self.conn.execute(
            """SELECT id, due_at, backlog_rank FROM triggers
               WHERE schedule_id=%s AND status='PENDING' AND recovery_epoch=%s
                 AND backlog_rank IS NOT NULL""",
            (schedule_id, epoch),
        ).fetchall()
        n = 0
        for r in rows:
            rank = r["backlog_rank"]
            ticks_needed = math.ceil(rank / rate_limit)
            earliest_service = recovery_started_at + self._timedelta(ticks_needed * tick_interval)
            expire_at = r["due_at"] + self._timedelta(deadline)
            if expire_at < earliest_service:
                self.conn.execute(
                    "UPDATE triggers SET status='SKIPPED', skip_reason='OVERFLOW', "
                    "updated_at=now() WHERE id=%s AND status='PENDING'",
                    (r["id"],),
                )
                n += 1
        return n

    def mark_expired(self, *, schedule_id: uuid.UUID, now: datetime, deadline: float) -> int:
        return self.conn.execute(
            """UPDATE triggers
               SET status='SKIPPED', skip_reason='EXPIRED', updated_at=now()
               WHERE schedule_id=%s AND status='PENDING' AND due_at IS NOT NULL
                 AND due_at < %s""",
            (schedule_id, self._timestamp(now, -deadline)),
        ).rowcount

    def claim(self, *, schedule_id: uuid.UUID, run_id: uuid.UUID, worker_id: str,
              now: datetime, deadline: float, lease_timeout: float,
              limit: int, backlog_only: bool, grace: float | None = None) -> list[dict]:
        """原子认领：CAS PENDING→RUNNING + FOR UPDATE SKIP LOCKED。

        backlog_only=False: 准时（age<=grace）。
        backlog_only=True : 已分配 backlog_rank 的积压，按 rank 顺序、限 rate_limit。
        """
        if not backlog_only:
            select_sql = """
                SELECT id FROM triggers
                WHERE schedule_id=%(sid)s AND status='PENDING' AND due_at IS NOT NULL
                  AND due_at <= %(now)s AND due_at > %(graceline)s
                  AND (not_before IS NULL OR not_before <= %(now)s)
                  AND (backlog_rank IS NULL)
                ORDER BY due_at, id
                LIMIT %(lim)s
                FOR UPDATE SKIP LOCKED
            """
            params = {"sid": schedule_id, "now": now,
                      "graceline": self._timestamp(now, -grace if grace is not None else 0.0),
                      "lim": limit}
        else:
            select_sql = """
                SELECT id FROM triggers
                WHERE schedule_id=%(sid)s AND status='PENDING' AND due_at IS NOT NULL
                  AND due_at <= %(now)s AND due_at > %(lower)s
                  AND (not_before IS NULL OR not_before <= %(now)s)
                  AND backlog_rank IS NOT NULL
                ORDER BY backlog_rank, due_at, id
                LIMIT %(lim)s
                FOR UPDATE SKIP LOCKED
            """
            params = {"sid": schedule_id, "now": now,
                      "lower": self._timestamp(now, -deadline), "lim": limit}
        rows = self.conn.execute(select_sql, params).fetchall()
        ids = [r["id"] for r in rows]
        if not ids:
            return []
        claimed = self.conn.execute(
            """UPDATE triggers
               SET status='RUNNING', attempts=attempts+1, locked_by=%(worker)s,
                   locked_at=%(now)s, claim_expires_at=%(expires)s, run_id=%(run)s,
                   updated_at=now()
               WHERE id = ANY(%(ids)s) AND status='PENDING'
               RETURNING *""",
            {"worker": worker_id, "now": now,
             "expires": self._timestamp(now, lease_timeout), "run": run_id, "ids": ids},
        ).fetchall()
        # 为每次尝试写执行行（先于副作用）
        for t in claimed:
            self.conn.execute(
                """INSERT INTO executions(id, trigger_id, attempt_no, worker_id, run_id,
                                          started_at, status)
                   VALUES (%s, %s, %s, %s, %s, %s, 'RUNNING')""",
                (uuid.uuid4(), t["id"], t["attempts"], worker_id, run_id, now),
            )
        return claimed

    def finalize_success(self, trigger_id: uuid.UUID, now: datetime, result: str) -> None:
        self.conn.execute(
            "UPDATE triggers SET status='SUCCEEDED', updated_at=now() WHERE id=%s",
            (trigger_id,),
        )
        self.conn.execute(
            "UPDATE executions SET status='SUCCEEDED', finished_at=%s, result=%s "
            "WHERE trigger_id=%s AND status='RUNNING'",
            (now, result, trigger_id),
        )

    def finalize_failure(self, trigger_id: uuid.UUID, now: datetime, code: str,
                         error: str, max_attempts: int, retry_backoff: float,
                         *, terminal: bool = False) -> str:
        """返回终态: FAILED 或 PENDING(按指数退避排回)。terminal=True(4xx/不可重试)立即 FAILED。"""
        t = self.conn.execute("SELECT attempts FROM triggers WHERE id=%s", (trigger_id,)).fetchone()
        attempts = t["attempts"]
        is_terminal = terminal or attempts >= max_attempts
        new_status = "FAILED" if is_terminal else "PENDING"
        self.conn.execute(
            "UPDATE executions SET status='FAILED', finished_at=%s, code=%s, error=%s "
            "WHERE trigger_id=%s AND status='RUNNING'",
            (now, code, error[:2000], trigger_id),
        )
        self.conn.execute(
            "UPDATE triggers SET status=%s, last_error=%s, "
            "not_before=%s, updated_at=now() WHERE id=%s",
            (new_status, error[:200],
             None if is_terminal else self._timestamp(now, retry_backoff), trigger_id),
        )
        return new_status

    @staticmethod
    def _timedelta(seconds: float):
        from datetime import timedelta

        return timedelta(seconds=seconds)


# ---------------------------------------------------------------- Runs / Executions

class RunRepository:
    def __init__(self, conn: psycopg.Connection):
        self.conn = conn

    def start(self, *, run_id: uuid.UUID, worker_id: str, code_version: str,
              tzdata_ver: str, now: datetime) -> None:
        self.conn.execute(
            """INSERT INTO runs(run_id, worker_id, code_version, tzdata_version,
                                observed_now, started_at, status)
               VALUES (%s,%s,%s,%s,%s,%s,'OK')""",
            (run_id, worker_id, code_version, tzdata_ver, now, now),
        )

    def finish(self, run_id: uuid.UUID, *, now: datetime, status: str, stats: dict) -> None:
        self.conn.execute(
            "UPDATE runs SET ended_at=%s, status=%s, stats=%s::jsonb WHERE run_id=%s",
            (now, status, json.dumps(stats, default=str), run_id),
        )

    def list_recent(self, limit: int = 50) -> list[dict]:
        return self.conn.execute(
            "SELECT * FROM runs ORDER BY started_at DESC LIMIT %s", (limit,)
        ).fetchall()


def list_executions(conn: psycopg.Connection, trigger_id: uuid.UUID) -> list[dict]:
    return conn.execute(
        "SELECT * FROM executions WHERE trigger_id=%s ORDER BY attempt_no", (trigger_id,)
    ).fetchall()
