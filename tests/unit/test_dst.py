"""DST：春季不存在时间（gap）跳过/顺延；秋季重复时间（fold）只执行一次。"""
from __future__ import annotations

from datetime import date, datetime, time, timezone
from uuid import uuid4

from app.planner.localize import FoldPolicy, GapPolicy, resolve_wall
from app.planner.service import PlanRequest, plan as build_plan
from app.schemas import ActionSpec, ScheduleSpec


def test_gap_classification_and_print():
    wall = datetime(2026, 3, 8, 2, 30)
    skip = resolve_wall(wall, "America/New_York", GapPolicy.SKIP, FoldPolicy.EARLIEST)
    shift = resolve_wall(wall, "America/New_York", GapPolicy.SHIFT_FORWARD, FoldPolicy.EARLIEST)
    print("\n[春季 gap 判定] America/New_York 2026-03-08 02:30（本地不存在）")
    print(f"  SKIP          -> kind={skip.kind} due_at={skip.due_at}")
    print(f"  SHIFT_FORWARD -> kind={shift.kind} due_at_utc={shift.due_at.isoformat()} "
          f"resolved_local={shift.resolved_wall.isoformat()}")
    assert skip.kind == "GAP" and skip.due_at is None
    assert shift.kind == "GAP_SHIFTED"
    assert shift.due_at == datetime(2026, 3, 8, 7, 30, tzinfo=timezone.utc)
    assert shift.resolved_wall == datetime(2026, 3, 8, 3, 30)
    assert shift.utc_offset_minutes == -240  # EDT


def test_fold_earliest_latest_each_execute_once():
    wall = datetime(2026, 11, 1, 1, 30)
    earliest = resolve_wall(wall, "America/New_York", GapPolicy.SKIP, FoldPolicy.EARLIEST)
    latest = resolve_wall(wall, "America/New_York", GapPolicy.SKIP, FoldPolicy.LATEST)
    print("\n[秋季 fold 判定] America/New_York 2026-11-01 01:30（本地重复）")
    print(f"  EARLIEST -> utc={earliest.due_at.isoformat()} fold={earliest.resolved_fold} "
          f"offset={earliest.utc_offset_minutes:+d}")
    print(f"  LATEST   -> utc={latest.due_at.isoformat()} fold={latest.resolved_fold} "
          f"offset={latest.utc_offset_minutes:+d}")
    assert earliest.kind == "FOLD" and latest.kind == "FOLD"
    assert earliest.due_at == datetime(2026, 11, 1, 5, 30, tzinfo=timezone.utc)
    assert latest.due_at == datetime(2026, 11, 1, 6, 30, tzinfo=timezone.utc)
    assert earliest.resolved_fold == 0 and latest.resolved_fold == 1

    # 每种策略在全年序列中该墙钟只出现一次（不重复执行）
    for pol in ("EARLIEST", "LATEST"):
        spec = ScheduleSpec(
            name="p", timezone="America/New_York", rrule="FREQ=DAILY",
            start_at=datetime(2026, 10, 30, 1, 30), fallback_policy=pol,
            action=ActionSpec(type="log"))
        p = build_plan(PlanRequest(uuid4(), spec, datetime(2026, 10, 1, tzinfo=timezone.utc),
                                   datetime(2026, 11, 5, tzinfo=timezone.utc), 5000))
        nov1 = [o for o in p.occurrences
                if o.local_wall.date() == date(2026, 11, 1) and o.due_at is not None]
        assert len(nov1) == 1


def test_gap_day_skipped_in_full_plan():
    spec = ScheduleSpec(
        name="p", timezone="America/New_York", rrule="FREQ=DAILY",
        start_at=datetime(2026, 3, 7, 2, 30), action=ActionSpec(type="log"))
    p = build_plan(PlanRequest(uuid4(), spec, datetime(2026, 3, 1, tzinfo=timezone.utc),
                               datetime(2026, 3, 12, tzinfo=timezone.utc), 5000))
    gap = [o for o in p.occurrences if o.local_wall.date() == date(2026, 3, 8)]
    assert len(gap) == 1 and gap[0].kind == "SKIP_GAP" and gap[0].due_at is None
