"""例外日期：SKIP、FORCE 新增、FORCE 冲突（重复/fold 歧义/gap）、未命中告警。"""
from __future__ import annotations

from datetime import datetime, timezone
from uuid import uuid4

import pytest

from app.errors import (
    ExceptionAmbiguousFold,
    ExceptionConflict,
)
from app.planner.service import PlanRequest, plan as build_plan
from app.schemas import ActionSpec, ScheduleSpec


def _spec(**kw):
    base = dict(name="p", timezone="America/New_York", rrule="FREQ=DAILY",
                start_at=datetime(2026, 11, 1, 1, 30), action=ActionSpec(type="log"))
    base.update(kw)
    return ScheduleSpec(**base)


def _window():
    return (datetime(2026, 10, 25, tzinfo=timezone.utc), datetime(2026, 11, 8, tzinfo=timezone.utc))


def test_skip_date_exception():
    spec = _spec(exceptions={"2026-11-02": "SKIP"})
    p = build_plan(PlanRequest(uuid4(), spec, *_window(), 5000))
    occ = [o for o in p.occurrences if o.local_wall.date().isoformat() == "2026-11-02"]
    assert len(occ) == 1 and occ[0].kind == "SKIP_EXCEPTION" and occ[0].due_at is None


def test_force_adds_occurrence_on_non_rule_day():
    # DAILY 01:30 规则；在 11-03 的 04:00 FORCE 新增一次（该时刻原本无出现）
    spec = _spec(exceptions={"2026-11-03 04:00:00": "FORCE"})
    p = build_plan(PlanRequest(uuid4(), spec, *_window(), 5000))
    forced = [o for o in p.occurrences if o.kind == "FORCE"]
    assert len(forced) == 1
    assert forced[0].local_wall == datetime(2026, 11, 3, 4, 0)
    assert forced[0].synthetic_key.startswith("f:")
    assert forced[0].due_at == datetime(2026, 11, 3, 9, 0, tzinfo=timezone.utc)  # EST +5


def test_force_conflicts_with_existing_utc_instant():
    # FORCE 到规则当天同一墙钟 → 与 rrule 产生同一 UTC → 422 EXCEPTION_CONFLICT
    spec = _spec(exceptions={"2026-11-02 01:30:00": "FORCE"})
    with pytest.raises(ExceptionConflict):
        build_plan(PlanRequest(uuid4(), spec, *_window(), 5000))


def test_force_on_fold_requires_selector():
    spec = _spec(fallback_policy="EARLIEST", exceptions={"2026-11-01 01:30:00": "FORCE"})
    with pytest.raises(ExceptionAmbiguousFold):
        build_plan(PlanRequest(uuid4(), spec, *_window(), 5000))


def test_force_on_fold_with_selector_ok():
    # 规则在每天 04:00（不覆盖 fold 墙钟 01:30）；用选择子 FORCE 该歧义墙钟
    for sel, expected_utc in (("earliest", datetime(2026, 11, 1, 5, 30, tzinfo=timezone.utc)),
                              ("latest", datetime(2026, 11, 1, 6, 30, tzinfo=timezone.utc))):
        spec = _spec(start_at=datetime(2026, 11, 1, 4, 0),
                     exceptions={f"2026-11-01 01:30:00[{sel}]": "FORCE"})
        p = build_plan(PlanRequest(uuid4(), spec, *_window(), 5000))
        forced = [o for o in p.occurrences if o.kind == "FORCE"]
        assert len(forced) == 1
        assert forced[0].due_at == expected_utc


def test_force_on_gap_rejected():
    spec = _spec(start_at=datetime(2026, 3, 7, 2, 30),
                 exceptions={"2026-03-08 02:30:00": "FORCE"})
    with pytest.raises(ExceptionConflict):
        build_plan(PlanRequest(
            uuid4(), spec, datetime(2026, 3, 1, tzinfo=timezone.utc),
            datetime(2026, 3, 12, tzinfo=timezone.utc), 5000))


def test_unmatched_skip_produces_warning():
    spec = _spec(exceptions={"2026-11-03 15:00:00": "SKIP"})  # 规则在 01:30，匹配不到
    p = build_plan(PlanRequest(uuid4(), spec, *_window(), 5000))
    assert any("matches no occurrence" in w for w in p.warnings)
