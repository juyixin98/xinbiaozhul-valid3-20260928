"""停机补跑：确定性顺序、上限、容量存活、过期、溢出；以及时钟回拨去重。"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest

from app.schemas import ActionSpec, ScheduleSpec
from app.services import create_schedule
from app.state.repositories import TriggerRepository


def _make(pool, settings, clock, *, rrule="FREQ=MINUTELY", start="2026-01-01T00:00:00",
          tz="UTC", **catchup):
    spec = ScheduleSpec(
        name="rec", timezone=tz, rrule=rrule,
        start_at=datetime.fromisoformat(start), action=ActionSpec(type="log"),
        **catchup)
    sid = create_schedule(pool, spec, now=clock.now(),
                          max_occurrences=settings.max_occurrences_per_window)
    return sid, spec


def _succeeded_order(pool, sid):
    with pool.connection() as conn:
        rows = conn.execute(
            "SELECT due_at FROM triggers WHERE schedule_id=%s AND status='SUCCEEDED' "
            "ORDER BY due_at", (sid,)).fetchall()
    return [r["due_at"] for r in rows]


def _statuses(pool, sid):
    with pool.connection() as conn:
        rows = conn.execute(
            "SELECT due_at, status, skip_reason, backlog_rank FROM triggers "
            "WHERE schedule_id=%s ORDER BY due_at", (sid,)).fetchall()
    return rows


def test_catchup_runs_in_deterministic_order_with_cap(pool, settings, fake_clock, scheduler):
    # rate=100：5 分钟停机后一个 tick 内按 due_at 升序全部补齐
    sid, _ = _make(pool, settings, fake_clock)
    scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())  # 健康首 tick，00:00 执行
    fake_clock.advance(300)  # 停机 5 分钟
    res = scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
    st = res.schedules[0]
    assert st.recovery_epoch == 1
    print("\n[停机补跑] backlog_assigned", st.backlog_assigned,
          "backlog_executed", st.claimed_backlog, "ontime", st.claimed_ontime,
          "succeeded", st.succeeded)
    order = _succeeded_order(pool, sid)
    print("[补跑顺序 due_at]", [d.isoformat() for d in order])
    assert order == sorted(order)
    # 00:01..00:04 为补跑，00:05 为准时；加上首 tick 的 00:00 共 6 次成功
    assert st.claimed_backlog == 4 and st.claimed_ontime == 1
    assert len(order) == 6
    assert order[-1] == datetime(2026, 1, 1, 0, 5, tzinfo=timezone.utc)


def test_rate_limit_truncates_and_remaining_survive_next_ticks(pool, settings, fake_clock, scheduler):
    # rate=1：每个 tick 最多补 1 个；未超容量的积压跨 tick 存活，不被误杀
    sid, _ = _make(pool, settings, fake_clock,
                   catchup_rate_limit=1, catchup_deadline_seconds=3600)
    scheduler.run_tick(only_schedule=sid)
    fake_clock.advance(180)  # 停机 3 分钟：积压 00:01,00:02
    r1 = scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
    assert r1.schedules[0].claimed_backlog == 1
    rows = _statuses(pool, sid)
    pending_after = [x for x in rows if x["status"] == "PENDING"]
    assert pending_after and not any(x["skip_reason"] == "OVERFLOW" for x in rows)

    fake_clock.advance(10)
    r2 = scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
    # 下一 tick 服务下一个 rank（容量内存活）
    assert r2.schedules[0].claimed_backlog == 1
    order = _succeeded_order(pool, sid)
    assert order == sorted(order)


def test_overflow_marks_unreachable_backlog(pool, settings, fake_clock, scheduler):
    # rate=1、deadline=120、停机 5 分钟：容量公式判定永远赶不上的积压 → OVERFLOW
    sid, _ = _make(pool, settings, fake_clock,
                   catchup_rate_limit=1, catchup_deadline_seconds=120)
    scheduler.run_tick(only_schedule=sid)
    fake_clock.advance(300)
    r = scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
    rows = _statuses(pool, sid)
    overflow = [x for x in rows if x["skip_reason"] == "OVERFLOW"]
    print("\n[容量溢出] overflow 行:",
          [(x["due_at"].isoformat(), x["backlog_rank"]) for x in overflow])
    assert overflow, "capacity formula should mark unreachable backlog as OVERFLOW"
    assert all(x["status"] == "SKIPPED" for x in overflow)
    assert r.schedules[0].recovery_epoch == 1


def test_expired_past_deadline_terminal(pool, settings, fake_clock, scheduler):
    # deadline=60：停机 10 分钟，到期超过 60s 的触发永久 EXPIRED
    sid, _ = _make(pool, settings, fake_clock, catchup_deadline_seconds=60)
    scheduler.run_tick(only_schedule=sid)
    fake_clock.advance(600)
    r = scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
    assert r.schedules[0].expired > 0
    rows = _statuses(pool, sid)
    expired = [x for x in rows if x["skip_reason"] == "EXPIRED"]
    assert expired and all(x["status"] == "SKIPPED" for x in expired)


def test_clock_rollback_does_not_reexecute(pool, settings, fake_clock, scheduler):
    sid, _ = _make(pool, settings, fake_clock, rrule="FREQ=DAILY",
                   start="2026-01-01T00:00:00")
    scheduler.run_tick(only_schedule=sid)
    after = _statuses(pool, sid)
    succ = [x for x in after if x["status"] == "SUCCEEDED"]
    assert len(succ) == 1
    # 时钟回拨 1 小时再 tick：已确认触发不可再次认领
    fake_clock.rewind(3600)
    scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
    rows = _statuses(pool, sid)
    succ2 = [x for x in rows if x["status"] == "SUCCEEDED"]
    assert len(succ2) == 1, "clock rollback must not re-run a confirmed trigger"
    # 执行尝试仍只有一条
    with pool.connection() as conn:
        cnt = conn.execute(
            "SELECT count(*) c FROM executions e JOIN triggers t ON e.trigger_id=t.id "
            "WHERE t.schedule_id=%s", (sid,)).fetchone()
    assert cnt["c"] == 1
