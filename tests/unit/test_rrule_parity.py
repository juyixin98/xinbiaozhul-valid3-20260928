"""与独立参考实现（dateutil + UTC 扫描器）的全年比对，覆盖 DST 与无 DST 地区。"""
from __future__ import annotations

from datetime import date, datetime, time, timedelta, timezone
from uuid import uuid4

from app.planner.service import PlanRequest, plan as build_plan
from app.schemas import ActionSpec, ScheduleSpec

from tests.reference import ref_daily


def _prod_daily(tz_name, start_date, local_t, *, gap="SKIP", fold="EARLIEST", days=365,
                rrule="FREQ=DAILY", span_days=365):
    start_local = datetime.combine(start_date, local_t)
    spec = ScheduleSpec(
        name="p", timezone=tz_name, rrule=rrule,
        start_at=start_local,
        gap_policy=gap, fallback_policy=fold, action=ActionSpec(type="log"))
    # 用 tz 名把本地窗口边界近似换算到 UTC（这里测试只关心日序列，固定给足范围）
    anchor_utc = datetime(start_date.year, start_date.month, start_date.day, tzinfo=timezone.utc)
    p = build_plan(PlanRequest(
        uuid4(), spec,
        anchor_utc - timedelta(days=2),
        anchor_utc + timedelta(days=days),
        50000))
    # 仅保留 dateutil count 范围内、且与 ref 同日期段的出现
    # 半开区间 [start, start+days)
    end_date = start_date + timedelta(days=days)
    return [o for o in p.occurrences
            if o.kind in ("FIRE", "SKIP_GAP")
            and start_date <= o.local_wall.date() < end_date][:days]


def test_full_year_2026_new_york_parity():
    ref = ref_daily(timezone_name="America/New_York",
                    start=date(2026, 1, 1), local_t=time(9, 0), days=365)
    prod = _prod_daily("America/New_York", date(2026, 1, 1), time(9, 0))
    assert len(ref) == len(prod) == 365
    for r, o in zip(ref, prod):
        assert r.local_wall == o.local_wall
        if r.due_at is None:
            assert o.due_at is None
        else:
            assert r.due_at == o.due_at, (r, o.due_at)
            assert r.resolution == o.resolution_kind
            assert r.resolved_fold == o.resolved_fold


def test_full_year_2026_shanghai_no_dst():
    # 上海无 DST：全部 NORMAL，固定 +08:00，365 天
    ref = ref_daily(timezone_name="Asia/Shanghai",
                    start=date(2026, 1, 1), local_t=time(9, 0), days=365)
    prod = _prod_daily("Asia/Shanghai", date(2026, 1, 1), time(9, 0))
    print("\n[本地时间 -> UTC 判定] Asia/Shanghai（无 DST，应恒定 +08:00）")
    for r, o in list(zip(ref, prod))[::60]:
        print(f"  local={r.local_wall.isoformat()} -> utc={r.due_at.isoformat()} "
              f"kind={o.resolution_kind} offset={o.utc_offset_minutes:+d}min")
    assert len(ref) == len(prod) == 365
    assert all(o.resolution_kind == "NORMAL" for o in prod)
    assert all(o.utc_offset_minutes == 480 for o in prod)


def test_weekly_byday_parity():
    from dateutil.rrule import MO, WE, FR, WEEKLY, rrule as drrule

    from tests.reference import scan_resolve

    rr = drrule(WEEKLY, dtstart=datetime(2026, 1, 1, 9, 0), byweekday=(MO, WE, FR), count=156)
    ref = [scan_resolve(wall, "America/New_York") for wall in rr]

    spec = ScheduleSpec(
        name="p", timezone="America/New_York", rrule="FREQ=WEEKLY;BYDAY=MO,WE,FR",
        start_at=datetime(2026, 1, 1, 9, 0), action=ActionSpec(type="log"))
    p = build_plan(PlanRequest(
        uuid4(), spec, datetime(2025, 12, 30, tzinfo=timezone.utc),
        datetime(2027, 1, 5, tzinfo=timezone.utc), 50000))
    prod = [o for o in p.occurrences
            if o.kind in ("FIRE", "SKIP_GAP")
            and date(2026, 1, 1) <= o.local_wall.date() <= date(2026, 12, 31)][:156]

    assert len(ref) == len(prod) == 156
    for r, o in zip(ref, prod):
        assert r.local_wall == o.local_wall
        assert r.due_at == o.due_at
