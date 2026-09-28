"""worker 崩溃恢复：RUNNING 租约过期 → 退避重排队或 FAILED 终态，并有 LEASE_EXPIRED 审计。"""
from __future__ import annotations

from datetime import datetime, timezone

from app.executor.base import ActionResult
from app.schemas import ActionSpec, ScheduleSpec
from app.services import create_schedule


class _FailUntil:
    """前 fail_first 次调用失败（可重试），之后成功。"""

    kind = "log"

    def __init__(self, fail_first: int, code: str = "ADAPTER_TIMEOUT", retryable: bool = True):
        self.fail_first = fail_first
        self.calls = 0
        self.code = code
        self.retryable = retryable

    def execute(self, *, trigger, attempt, spec, timeout_s):
        self.calls += 1
        if self.calls <= self.fail_first:
            return ActionResult("FAILED", self.code, None, f"simulated failure #{self.calls}",
                                retryable=self.retryable)
        return ActionResult("SUCCEEDED", None, "ok")


def _spec():
    return ScheduleSpec(name="lease", timezone="UTC", rrule="FREQ=DAILY",
                        start_at=datetime(2026, 1, 1, 0, 0), action=ActionSpec(type="log"))


def test_failure_retries_then_succeeds(pool, settings, fake_clock, scheduler):
    sid = create_schedule(pool, _spec(), now=fake_clock.now(),
                          max_occurrences=settings.max_occurrences_per_window)
    flaky = _FailUntil(fail_first=2)
    scheduler.log_executor = flaky  # log 动作走此适配器

    # 第 1 次 tick：认领执行失败 → PENDING + 2s 退避（attempt 1）
    scheduler.run_tick(only_schedule=sid)
    with pool.connection() as conn:
        t = conn.execute("SELECT attempts,status,not_before FROM triggers "
                         "WHERE due_at='2026-01-01T00:00:00Z'").fetchone()
    assert t["attempts"] == 1 and t["status"] == "PENDING" and t["not_before"] is not None

    # 退避未到不重试
    fake_clock.advance(1)
    scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
    with pool.connection() as conn:
        n = conn.execute("SELECT count(*) c FROM executions").fetchone()["c"]
    assert n == 1

    # 过退避后第 2 次（再失败）与第 3 次成功
    fake_clock.advance(60)
    scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
    fake_clock.advance(60)
    scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
    with pool.connection() as conn:
        t = conn.execute("SELECT attempts,status FROM triggers WHERE due_at=\'2026-01-01T00:00:00Z\'").fetchone()
        assert t["status"] == "SUCCEEDED" and t["attempts"] == 3
        codes = conn.execute("SELECT status,code FROM executions ORDER BY attempt_no").fetchall()
    print("\n[重试序列]", [(r["status"], r["code"]) for r in codes])
    assert [r["status"] for r in codes] == ["FAILED", "FAILED", "SUCCEEDED"]


def test_terminal_failure_after_max_attempts(pool, settings, fake_clock, scheduler):
    # 4xx 永久失败：不可重试，第一次即 FAILED 终态（即使 attempts<max）
    sid = create_schedule(pool, _spec(), now=fake_clock.now(),
                          max_occurrences=settings.max_occurrences_per_window)
    perm = _FailUntil(fail_first=99, code="ADAPTER_HTTP_4XX", retryable=False)
    scheduler.log_executor = perm
    scheduler.run_tick(only_schedule=sid)
    with pool.connection() as conn:
        t = conn.execute("SELECT status,attempts,last_error FROM triggers WHERE due_at=\'2026-01-01T00:00:00Z\'").fetchone()
    assert t["status"] == "FAILED" and t["attempts"] == 1


def test_stale_lease_is_reclaimed_with_backoff(pool, settings, fake_clock, scheduler):
    settings.lease_timeout_seconds = 5
    sid = create_schedule(pool, _spec(), now=fake_clock.now(),
                          max_occurrences=settings.max_occurrences_per_window)
    # 先一个健康 tick 物化并执行 01-01；随后把该触发器伪装成“执行中 worker 崩溃”
    scheduler.run_tick(only_schedule=sid)
    with pool.connection() as conn:
        conn.execute("UPDATE triggers SET status='RUNNING', attempts=1, "
                     "claim_expires_at=%s, locked_by='dead-worker' "
                     "WHERE schedule_id=%s AND due_at='2026-01-01T00:00:00Z'",
                     (fake_clock.now(), sid))
        # 上一执行行已 SUCCEEDED；追加一条崩溃时未完成的 attempt=2
        conn.execute("""INSERT INTO executions(id,trigger_id,attempt_no,worker_id,run_id,
                                              started_at,status)
                        SELECT gen_random_uuid(), id, 2, 'dead-worker', NULL, now(), 'RUNNING'
                        FROM triggers
                        WHERE schedule_id=%s AND due_at='2026-01-01T00:00:00Z'""", (sid,))

    # 超过租约但短于停机阈值（心跳刚由健康 tick 更新）→ 走崩溃租约回收，不触发停机补跑
    fake_clock.advance(10)
    r = scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
    with pool.connection() as conn:
        t = conn.execute("SELECT status,attempts,not_before FROM triggers "
                         "WHERE due_at='2026-01-01T00:00:00Z'").fetchone()
        le = conn.execute("SELECT status,code FROM executions WHERE code='LEASE_EXPIRED'").fetchall()
    # not_before 在未来（now+2s），故回收后本 tick 不会立刻再执行 → 仍是 PENDING
    assert t["status"] == "PENDING" and t["attempts"] == 1 and t["not_before"] is not None
    assert len(le) == 1 and le[0]["status"] == "LEASE_EXPIRED"
    print("[租约回收]", le[0]["status"], le[0]["code"], "tick status", r.schedules[0].status)
