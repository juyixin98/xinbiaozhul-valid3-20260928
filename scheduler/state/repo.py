"""PostgreSQL repository — all SQL lives here.

The engine talks to this class only; nothing outside ``state/`` writes SQL.
Methods accept open transactions where ordering matters so the engine can
compose "claim + insert run + audit" atomically.

Concurrency model (safe for multiple worker processes):

* ``advance_watermark`` uses a row lock + monotonic check on ``runtime_state``
  to refuse clock rollback and to serialise ticks cluster-wide.
* Fires are claimed with ``SELECT ... FOR UPDATE SKIP LOCKED`` keyed by the
  global unique ``fire_key``; the identity unique constraint is the final
  idempotency fence for every insert.
* A per-schedule advisory lock serialises replanning for one schedule.
* Stale ``running`` rows (heartbeat older than a lease) are reclaimable.
"""
from __future__ import annotations

import json
import uuid
from contextlib import contextmanager
from datetime import datetime, timedelta
from pathlib import Path
from typing import Any, Iterator

import psycopg
from psycopg.rows import dict_row
from psycopg_pool import ConnectionPool

from ..errors import ConflictError, EngineDivergenceError, NotFoundError
from ..planner.spec import ScheduleSpec

_SCHEMA_PATH = Path(__file__).with_name("schema.sql")

FIRE_COLUMNS = (
    "id, schedule_id, version, kind, ordinal, fold, fire_key, date_local, "
    "due_utc, utc_offset_minutes, status, run_id, attempts, last_error_code, "
    "last_error_message, created_at_utc, updated_at_utc"
)


def _split_sql(script: str) -> list[str]:
    """Split a DDL script into statements, stripping ``--`` comments.

    Quote-aware so apostrophes in comment text or strings are preserved; the
    schema file contains no procedural bodies (dollar-quoting), keeping this
    intentionally simple.
    """
    out: list[str] = []
    buf: list[str] = []
    in_squote = False
    i = 0
    n = len(script)
    while i < n:
        ch = script[i]
        if in_squote:
            buf.append(ch)
            if ch == "'":
                if i + 1 < n and script[i + 1] == "'":
                    buf.append(script[i + 1])
                    i += 2
                    continue
                in_squote = False
            i += 1
            continue
        if ch == "'":
            in_squote = True
            buf.append(ch)
            i += 1
            continue
        if ch == "-" and i + 1 < n and script[i + 1] == "-":
            # skip to end of line
            nl = script.find("\n", i + 2)
            i = n if nl == -1 else nl
            continue
        if ch == ";":
            stmt = "".join(buf).strip()
            if stmt:
                out.append(stmt)
            buf = []
            i += 1
            continue
        buf.append(ch)
        i += 1
    tail = "".join(buf).strip()
    if tail:
        out.append(tail)
    return out


from datetime import timezone as _tz

_UTC = _tz.utc


def utcnow_naive(now: datetime | None = None) -> datetime:
    """Normalise to naive UTC for *storing* values into timestamptz columns.

    Kept for internal bookkeeping columns where only ordering matters.
    """
    val = now or datetime.now(_UTC)
    if val.tzinfo is not None:
        val = val.astimezone(_UTC).replace(tzinfo=None)
    return val


def utc_aware(now: datetime) -> datetime:
    """Aware-UTC value used in comparisons/binds against timestamptz.

    psycopg interprets naive datetimes in the connection's TimeZone setting;
    passing explicit UTC removes any session-timezone ambiguity.
    """
    if now.tzinfo is None:
        return now.replace(tzinfo=_UTC)
    return now.astimezone(_UTC)


class Repository:
    def __init__(self, database_url: str) -> None:
        self._url = database_url
        self._pool = ConnectionPool(
            database_url,
            min_size=1,
            max_size=10,
            # All timestamptz binds/reads are interpreted and returned in UTC,
            # independent of the container's local zone. Read rows are still
            # tz-aware (UTC); comparison binds are passed naive UTC.
            kwargs={"row_factory": dict_row,
                    "options": "-c TimeZone=UTC"},
            open=False,
        )

    def open(self) -> None:
        self._pool.open()

    def close(self) -> None:
        self._pool.close()

    @contextmanager
    def tx(self) -> Iterator[psycopg.Connection]:
        with self._pool.connection() as conn:
            with conn.transaction():
                yield conn

    # ── bootstrap ──────────────────────────────────────────────────────────

    def init_schema(self) -> None:
        statements = _split_sql(_SCHEMA_PATH.read_text())
        with self.tx() as conn:
            for stmt in statements:
                # DDL must bypass psycopg's prepared-statement cache: two
                # CREATE TABLE statements share a generic plan and the cache
                # would otherwise replay the first table's definition.
                conn.execute(stmt, prepare=False)

    # ── audit ──────────────────────────────────────────────────────────────

    def audit(
        self,
        conn: psycopg.Connection,
        event: str,
        *,
        schedule_id: str | None = None,
        fire_key: str | None = None,
        run_id: str | None = None,
        detail: dict[str, Any] | None = None,
        ts: datetime | None = None,
    ) -> None:
        conn.execute(
            """
            INSERT INTO audit_events (ts_utc, event, schedule_id, fire_key, run_id, detail_json)
            VALUES (%s, %s, %s, %s, %s, %s::jsonb)
            """,
            (
                utcnow_naive(ts),
                event,
                schedule_id,
                fire_key,
                run_id,
                json.dumps(detail or {}, default=str),
            ),
        )

    def list_audit(
        self, *, schedule_id: str | None = None, limit: int = 100, event: str | None = None
    ) -> list[dict]:
        q = ["SELECT * FROM audit_events"]
        where, params = [], []
        if schedule_id:
            where.append("schedule_id = %s")
            params.append(schedule_id)
        if event:
            where.append("event = %s")
            params.append(event)
        if where:
            q.append("WHERE " + " AND ".join(where))
        q.append("ORDER BY id DESC LIMIT %s")
        params.append(limit)
        with self._pool.connection() as conn:
            rows = conn.execute(" ".join(q), params).fetchall()
        return [self._decode_audit(r) for r in rows]

    @staticmethod
    def _decode_audit(r: dict) -> dict:
        return {
            "id": r["id"],
            "ts_utc": r["ts_utc"].astimezone(_UTC).isoformat(),
            "event": r["event"],
            "schedule_id": str(r["schedule_id"]) if r["schedule_id"] else None,
            "fire_key": r["fire_key"],
            "run_id": str(r["run_id"]) if r["run_id"] else None,
            "detail": r["detail_json"],
        }

    # ── schedules ──────────────────────────────────────────────────────────

    def create_schedule(self, spec: ScheduleSpec, *, now: datetime) -> dict:
        sid = uuid.uuid4()
        n = utcnow_naive(now)
        with self.tx() as conn:
            conn.execute(
                """
                INSERT INTO schedules (id, version, name, spec_json, timezone,
                                       status, created_at_utc, updated_at_utc)
                VALUES (%s, 1, %s, %s::jsonb, %s, 'active', %s, %s)
                """,
                (sid, spec.name, json.dumps(spec.model_dump()), spec.timezone, n, n),
            )
            self.audit(
                conn, "schedule.created", schedule_id=str(sid),
                detail={"name": spec.name, "timezone": spec.timezone}, ts=now,
            )
        return self.get_schedule(str(sid))

    def get_schedule(self, schedule_id: str) -> dict:
        with self._pool.connection() as conn:
            row = conn.execute(
                "SELECT * FROM schedules WHERE id = %s", (schedule_id,)
            ).fetchone()
        if not row:
            raise NotFoundError(f"schedule {schedule_id} not found")
        return self._decode_schedule(row)

    @staticmethod
    def _decode_schedule(r: dict) -> dict:
        return {
            "id": str(r["id"]),
            "version": r["version"],
            "name": r["name"],
            "spec": r["spec_json"],
            "timezone": r["timezone"],
            "status": r["status"],
            "planned_through_local": r["planned_through_local"].isoformat()
            if r["planned_through_local"]
            else None,
            "created_at_utc": r["created_at_utc"].astimezone(_UTC).isoformat(),
            "updated_at_utc": r["updated_at_utc"].astimezone(_UTC).isoformat(),
        }

    def list_schedules(self) -> list[dict]:
        with self._pool.connection() as conn:
            rows = conn.execute("SELECT * FROM schedules ORDER BY created_at_utc").fetchall()
        return [self._decode_schedule(r) for r in rows]

    def update_schedule(
        self, schedule_id: str, spec: ScheduleSpec, *, now: datetime
    ) -> dict:
        """Replace the plan: bump version, cancel all unfinished old fires."""
        n = utcnow_naive(now)
        with self.tx() as conn:
            row = conn.execute(
                "SELECT id, version FROM schedules WHERE id = %s FOR UPDATE",
                (schedule_id,),
            ).fetchone()
            if not row:
                raise NotFoundError(f"schedule {schedule_id} not found")
            old_version = row["version"]
            conn.execute(
                """
                UPDATE schedules
                   SET version = version + 1, name = %s, spec_json = %s::jsonb,
                       timezone = %s, planned_through_local = NULL, updated_at_utc = %s
                 WHERE id = %s
                """,
                (spec.name, json.dumps(spec.model_dump()), spec.timezone, n, schedule_id),
            )
            cur = conn.execute(
                """
                UPDATE fires SET status = 'cancelled', updated_at_utc = %s
                 WHERE schedule_id = %s AND version = %s
                   AND status IN ('planned','due')
                RETURNING fire_key
                """,
                (n, schedule_id, old_version),
            ).fetchall()
            self.audit(
                conn, "schedule.updated", schedule_id=schedule_id,
                detail={
                    "old_version": old_version,
                    "new_version": old_version + 1,
                    "cancelled_pending": len(cur),
                },
                ts=now,
            )
        return self.get_schedule(schedule_id)

    def set_paused(self, schedule_id: str, paused: bool, *, now: datetime) -> dict:
        with self.tx() as conn:
            row = conn.execute(
                "SELECT status FROM schedules WHERE id = %s FOR UPDATE", (schedule_id,)
            ).fetchone()
            if not row:
                raise NotFoundError(f"schedule {schedule_id} not found")
            new_status = "paused" if paused else "active"
            if row["status"] == new_status:
                raise ConflictError(
                    f"schedule is already {new_status}",
                    details={"schedule_id": schedule_id, "status": new_status},
                )
            conn.execute(
                "UPDATE schedules SET status = %s, updated_at_utc = %s WHERE id = %s",
                (new_status, utcnow_naive(now), schedule_id),
            )
            self.audit(
                conn,
                f"schedule.{new_status}",
                schedule_id=schedule_id,
                detail={},
                ts=now,
            )
        return self.get_schedule(schedule_id)

    def delete_schedule(self, schedule_id: str, *, now: datetime) -> None:
        with self.tx() as conn:
            row = conn.execute(
                "DELETE FROM schedules WHERE id = %s RETURNING id", (schedule_id,)
            ).fetchone()
            if not row:
                raise NotFoundError(f"schedule {schedule_id} not found")
            self.audit(conn, "schedule.deleted", schedule_id=schedule_id, ts=now)

    # ── planning persistence ───────────────────────────────────────────────

    def insert_planned_fires(
        self, conn: psycopg.Connection, schedule_id: str, fires: list[dict]
    ) -> int:
        """Insert planner output. Existing fire_keys are skipped (idempotent).

        Returns number actually inserted. The unique constraint is the
        authority — replanning the same window never duplicates a fire.
        """
        if not fires:
            return 0
        now = utcnow_naive()
        rows = [
            (
                uuid.uuid4(),
                f["schedule_id"],
                f["version"],
                f["kind"],
                f["ordinal"],
                f["fold"],
                f["fire_key"],
                f["date_local"],
                f["due_utc"],
                f["utc_offset_minutes"],
                now,
                now,
            )
            for f in fires
        ]
        inserted = 0
        with conn.cursor() as cur:
            for row in rows:
                cur.execute(
                    """
                    INSERT INTO fires (id, schedule_id, version, kind, ordinal, fold,
                                       fire_key, date_local, due_utc, utc_offset_minutes,
                                       created_at_utc, updated_at_utc)
                    VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)
                    ON CONFLICT (fire_key) DO NOTHING
                    """,
                    row,
                )
                inserted += cur.rowcount
        return inserted

    def mark_planned_through(self, schedule_id: str, local_date: str) -> None:
        with self.tx() as conn:
            conn.execute(
                "UPDATE schedules SET planned_through_local = %s WHERE id = %s",
                (local_date, schedule_id),
            )

    # ── tick / watermark ───────────────────────────────────────────────────

    @contextmanager
    def tick_lock(self, now: datetime) -> Iterator[datetime | None]:
        """Advance the monotonic tick watermark.

        Yields the previous watermark (None on first tick). Raises ConflictError
        when ``now`` is not strictly later — the clock went backwards or the
        same instant is being replayed. Rolls the watermark back only if the
        caller's transaction fails.
        """
        n = utcnow_naive(now)
        with self.tx() as conn:
            conn.execute("SELECT 1 FROM runtime_state WHERE singleton = 1 FOR UPDATE")
            row = conn.execute(
                "SELECT last_tick_utc FROM runtime_state WHERE singleton = 1"
            ).fetchone()
            prev = row["last_tick_utc"]
            if prev is not None and prev.tzinfo is not None:
                prev = prev.astimezone(_UTC).replace(tzinfo=None)
            if prev is not None and n <= prev:
                raise ConflictError(
                    "tick timestamp must advance (clock-rollback guard)",
                    code="STATE-CLOCK-ROLLBACK",
                    details={
                        "last_tick_utc": prev.isoformat() + "Z",
                        "requested_utc": n.isoformat() + "Z",
                    },
                )
            conn.execute(
                "UPDATE runtime_state SET last_tick_utc = %s, updated_at_utc = %s "
                "WHERE singleton = 1",
                (n, n),
            )
            yield prev

    def record_tick_result(self, result: dict, *, now: datetime) -> None:
        with self.tx() as conn:
            conn.execute(
                "UPDATE runtime_state SET last_tick_result = %s::jsonb, "
                "updated_at_utc = %s WHERE singleton = 1",
                (json.dumps(result, default=str), utcnow_naive(now)),
            )

    def get_runtime(self) -> dict:
        with self._pool.connection() as conn:
            r = conn.execute(
                "SELECT * FROM runtime_state WHERE singleton = 1"
            ).fetchone()
        return {
            "last_tick_utc": r["last_tick_utc"].astimezone(_UTC).isoformat()
            if r["last_tick_utc"]
            else None,
            "last_tick_result": r["last_tick_result"],
        }

    # ── fire selection / dispatch persistence ──────────────────────────────

    def claim_due_fires(
        self,
        *,
        now: datetime,
        horizon: timedelta,
        catch_up_limit: int,
        lease: timedelta,
    ) -> list[dict]:
        """Atomically claim due fires and open a run row per claimed fire.

        Selection priority — total and deterministic, computed in SQL so
        multiple workers agree:

          bucket 0: fires due within (now - horizon, now] (on-time, no cap)
          bucket 1: older fires (catch-up), oldest-first, capped at
                    ``catch_up_limit``

        Tie-break within bucket: (due_utc, schedule, ordinal, kind, fold).
        Rows are taken FOR UPDATE SKIP LOCKED. Terminal rows are never
        selectable, so clock rollback cannot re-execute history.
        """
        n = utc_aware(now)
        cutoff = n + horizon
        on_time_floor = n - horizon
        stale_before = n - lease
        claimed: list[dict] = []
        with self.tx() as conn:
            conn.execute(
                """
                UPDATE fires SET status = 'due'
                 FROM schedules s
                 WHERE fires.schedule_id = s.id AND s.status = 'active'
                   AND fires.status = 'planned' AND due_utc <= %s
                """,
                (cutoff,),
            )
            rows = conn.execute(
                """
                SELECT * FROM (
                    SELECT fires.*,
                           CASE WHEN fires.due_utc > %s THEN 0 ELSE 1 END AS bucket,
                           ROW_NUMBER() OVER (
                               PARTITION BY CASE WHEN fires.due_utc > %s THEN 0 ELSE 1 END
                               ORDER BY fires.due_utc, fires.schedule_id,
                                        fires.ordinal, fires.kind, fires.fold
                           ) AS rn
                      FROM fires
                      JOIN schedules s ON s.id = fires.schedule_id
                     WHERE s.status = 'active'
                       AND (
                            (fires.status = 'due' AND fires.due_utc <= %s)
                            OR (fires.status = 'running' AND fires.run_id IS NOT NULL
                                AND fires.updated_at_utc < %s)
                           )
                ) q
                 WHERE bucket = 0 OR rn <= %s
                 ORDER BY bucket, due_utc, schedule_id, ordinal, kind, fold
                """,
                # on-time first (never starved by backlog), then oldest
                # catch-up; within a bucket the tie-break is fully ordered
                (on_time_floor, on_time_floor, n, stale_before, catch_up_limit),
            ).fetchall()
            for r in rows:
                run_id = uuid.uuid4()
                # A reclaimed lease is another delivery attempt; the engine
                # immediately releases budget-deferred rows, which does not
                # count as an attempt (see engine._release_claims).
                attempts = r["attempts"] + 1
                conn.execute(
                    """
                    UPDATE fires
                       SET status = 'running', run_id = %s, attempts = %s,
                           updated_at_utc = %s
                     WHERE id = %s
                    """,
                    (run_id, attempts, n, r["id"]),
                )
                conn.execute(
                    """
                    INSERT INTO runs (id, fire_id, fire_key, attempt,
                                      executor_type, status, claimed_at_utc,
                                      heartbeat_at_utc)
                    SELECT %s, f.id, f.fire_key, %s,
                           s.spec_json->'executor'->>'type', 'claimed', %s, %s
                      FROM fires f JOIN schedules s ON s.id = f.schedule_id
                     WHERE f.id = %s
                    """,
                    (run_id, attempts, n, n, r["id"]),
                )
                d = self._decode_fire(r)
                d["run_id"] = str(run_id)
                d["attempt"] = attempts
                claimed.append(d)
                self.audit(
                    conn, "fire.claimed",
                    schedule_id=str(r["schedule_id"]),
                    fire_key=r["fire_key"], run_id=str(run_id),
                    detail={"due_utc": r["due_utc"].astimezone(_UTC).isoformat(),
                            "attempt": attempts},
                    ts=now,
                )
        return claimed

    def complete_run(
        self,
        *,
        run_id: str,
        fire_key: str,
        succeeded: bool,
        detail: dict[str, Any],
        error_code: str | None,
        error_message: str | None,
        now: datetime,
    ) -> None:
        n = utcnow_naive(now)
        with self.tx() as conn:
            state = "succeeded" if succeeded else "failed"
            cur = conn.execute(
                """
                UPDATE fires
                   SET status = %s, last_error_code = %s, last_error_message = %s,
                       updated_at_utc = %s
                 WHERE fire_key = %s AND run_id = %s
                """,
                (state, error_code, error_message, n, fire_key, run_id),
            )
            if cur.rowcount != 1:
                raise EngineDivergenceError(
                    f"run {run_id} completion matched {cur.rowcount} fires",
                    details={"fire_key": fire_key},
                )
            conn.execute(
                """
                UPDATE runs
                   SET status = %s, finished_at_utc = %s, heartbeat_at_utc = %s,
                       error_code = %s, error_message = %s, detail_json = %s::jsonb
                 WHERE id = %s
                """,
                (
                    "succeeded" if succeeded else "failed",
                    n,
                    n,
                    error_code,
                    error_message,
                    json.dumps(detail, default=str),
                    run_id,
                ),
            )
            self.audit(
                conn,
                "fire.succeeded" if succeeded else "fire.failed",
                fire_key=fire_key,
                run_id=run_id,
                detail={"error_code": error_code, "detail": detail},
                ts=now,
            )

    def count_active_due(self) -> int:
        """Pending due fires for active schedules (catch-up backlog)."""
        with self._pool.connection() as conn:
            row = conn.execute(
                """
                SELECT count(*) AS c FROM fires f
                 JOIN schedules s ON s.id = f.schedule_id
                 WHERE s.status = 'active' AND f.status = 'due'
                """,
            ).fetchone()
        return int(row["c"])

    def expire_behind(self, *, cutoff_utc: datetime, now: datetime) -> int:
        """Mark still-pending fires older than the catch-up window expired."""
        n = utcnow_naive(now)
        cut = utc_aware(cutoff_utc)
        with self.tx() as conn:
            rows = conn.execute(
                """
                UPDATE fires SET status = 'expired', updated_at_utc = %s
                 WHERE due_utc < %s AND status IN ('planned','due')
                RETURNING fire_key, schedule_id
                """,
                (n, cut),
            ).fetchall()
            for r in rows:
                self.audit(
                    conn, "fire.expired",
                    schedule_id=str(r["schedule_id"]), fire_key=r["fire_key"],
                    detail={"reason": "behind catch-up window"}, ts=now,
                )
        return len(rows)

    # ── queries ────────────────────────────────────────────────────────────

    @staticmethod
    def _decode_fire(r: dict) -> dict:
        return {
            "id": str(r["id"]),
            "schedule_id": str(r["schedule_id"]),
            "version": r["version"],
            "kind": r["kind"],
            "ordinal": r["ordinal"],
            "fold": r["fold"],
            "fire_key": r["fire_key"],
            "date_local": r["date_local"].isoformat(),
            "due_utc": r["due_utc"].astimezone(_UTC).isoformat(),
            "utc_offset_minutes": r["utc_offset_minutes"],
            "status": r["status"],
            "attempts": r["attempts"],
            "last_error_code": r["last_error_code"],
            "last_error_message": r["last_error_message"],
        }

    def list_fires(self, schedule_id: str, *, limit: int = 200) -> list[dict]:
        with self._pool.connection() as conn:
            rows = conn.execute(
                f"SELECT {FIRE_COLUMNS} FROM fires WHERE schedule_id = %s "
                "ORDER BY due_utc, fire_key LIMIT %s",
                (schedule_id, limit),
            ).fetchall()
        return [self._decode_fire(r) for r in rows]

    def get_run(self, run_id: str) -> dict:
        with self._pool.connection() as conn:
            r = conn.execute("SELECT * FROM runs WHERE id = %s", (run_id,)).fetchone()
        if not r:
            raise NotFoundError(f"run {run_id} not found")
        return {
            "id": str(r["id"]),
            "fire_id": str(r["fire_id"]),
            "fire_key": r["fire_key"],
            "attempt": r["attempt"],
            "executor_type": r["executor_type"],
            "status": r["status"],
            "claimed_at_utc": r["claimed_at_utc"].astimezone(_UTC).isoformat(),
            "finished_at_utc": r["finished_at_utc"].astimezone(_UTC).isoformat()
            if r["finished_at_utc"]
            else None,
            "error_code": r["error_code"],
            "error_message": r["error_message"],
            "detail": r["detail_json"],
        }

    def list_runs(self, schedule_id: str | None = None, *, limit: int = 100) -> list[dict]:
        with self._pool.connection() as conn:
            if schedule_id:
                rows = conn.execute(
                    """
                    SELECT r.* FROM runs r JOIN fires f ON f.id = r.fire_id
                     WHERE f.schedule_id = %s
                     ORDER BY r.claimed_at_utc DESC, r.id DESC LIMIT %s
                    """,
                    (schedule_id, limit),
                ).fetchall()
            else:
                rows = conn.execute(
                    "SELECT * FROM runs ORDER BY claimed_at_utc DESC, id DESC LIMIT %s",
                    (limit,),
                ).fetchall()
        out = []
        for r in rows:
            out.append(
                {
                    "id": str(r["id"]),
                    "fire_key": r["fire_key"],
                    "attempt": r["attempt"],
                    "executor_type": r["executor_type"],
                    "status": r["status"],
                    "claimed_at_utc": r["claimed_at_utc"].astimezone(_UTC).isoformat(),
                    "finished_at_utc": r["finished_at_utc"].astimezone(_UTC).isoformat()
                    if r["finished_at_utc"]
                    else None,
                    "error_code": r["error_code"],
                    "error_message": r["error_message"],
                    "detail": r["detail_json"],
                }
            )
        return out

    def get_fire_by_key(self, fire_key: str) -> dict:
        with self._pool.connection() as conn:
            r = conn.execute(
                f"SELECT {FIRE_COLUMNS} FROM fires WHERE fire_key = %s", (fire_key,)
            ).fetchone()
        if not r:
            raise NotFoundError(f"fire {fire_key} not found")
        return self._decode_fire(r)
