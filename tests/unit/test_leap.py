"""闰日：2028-02-29 正常触发；平年 2 月产生 MISSING_DATE 跳过身份。"""
from __future__ import annotations

from datetime import datetime, timezone
from uuid import uuid4

from app.planner.service import PlanRequest, plan as build_plan
from app.schemas import ActionSpec, ScheduleSpec


def test_feb29_fires_in_leap_year_and_missing_in_common_year():
    # 锚点 2028-02-29（闰年），月度规则
    spec = ScheduleSpec(
        name="leap", timezone="UTC", rrule="FREQ=MONTHLY",
        start_at=datetime(2028, 2, 29, 9, 0), action=ActionSpec(type="log"))
    p = build_plan(PlanRequest(
        uuid4(), spec, datetime(2028, 1, 1, tzinfo=timezone.utc),
        datetime(2030, 3, 1, tzinfo=timezone.utc), 5000))

    by_date = {}
    for o in p.occurrences:
        if o.local_wall.month == 2:
            by_date[o.local_wall.year] = o

    print("\n[闰日判定] FREQ=MONTHLY start=2028-02-29 09:00 UTC")
    for y, o in sorted(by_date.items()):
        print(f"  year={y} local_clamped={o.local_wall.date()} kind={o.kind} "
              f"requested={o.requested_ymd} due_at={o.due_at} key={o.synthetic_key}")

    assert by_date[2028].kind == "FIRE"
    assert by_date[2028].due_at == datetime(2028, 2, 29, 9, 0, tzinfo=timezone.utc)
    assert by_date[2029].kind == "SKIP_MISSING"
    assert by_date[2029].requested_ymd == (2029, 2, 29)
    assert by_date[2029].synthetic_key == "m:20290229T090000000000"


def test_yearly_interval4_from_leap_day():
    spec = ScheduleSpec(
        name="leap4", timezone="UTC", rrule="FREQ=YEARLY;INTERVAL=4",
        start_at=datetime(2028, 2, 29, 9, 0), action=ActionSpec(type="log"))
    p = build_plan(PlanRequest(
        uuid4(), spec, datetime(2028, 1, 1, tzinfo=timezone.utc),
        datetime(2041, 1, 1, tzinfo=timezone.utc), 5000))
    fires = [(o.local_wall.year, o.kind) for o in p.occurrences]
    print("\n[YEARLY;INTERVAL=4 from 02-29]", fires)
    assert fires == [(2028, "FIRE"), (2032, "FIRE"), (2036, "FIRE"), (2040, "FIRE")]


def test_bymonthday29_skips_february_in_common_year_silently():
    # BYMONTHDAY=29 平年 2 月按 RFC 跳过（无 missing 标记）；其余月份有 29 日照常
    spec = ScheduleSpec(
        name="dom29", timezone="UTC", rrule="FREQ=MONTHLY;BYMONTHDAY=29",
        start_at=datetime(2026, 1, 29, 9, 0), action=ActionSpec(type="log"))
    p = build_plan(PlanRequest(
        uuid4(), spec, datetime(2026, 1, 1, tzinfo=timezone.utc),
        datetime(2027, 3, 1, tzinfo=timezone.utc), 5000))
    months = {o.local_wall.strftime("%Y-%m") for o in p.occurrences if o.kind == "FIRE"}
    assert "2026-02" not in months
    assert "2026-01" in months and "2026-03" in months and "2027-02" not in months
