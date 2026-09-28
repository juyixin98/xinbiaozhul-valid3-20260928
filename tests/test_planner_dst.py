"""DST / recurrence kernel tests (pure, no DB).

Covers the explicit rules: spring-forward gap skipped, fall-back ambiguity
fired twice (or once per policy), plus leap-day, monthly nth-weekday and
identity-stability checks. Local-time -> UTC decisions are printed for the
record (the task asks for them in logs).
"""
from __future__ import annotations

from datetime import date, datetime, time, timezone

import pytest

from scheduler.planner.calendar import LocalKind, resolve_wall_time
from scheduler.planner.occurrence import (
    FireKind,
    SkipReason,
    plan_window,
)
from scheduler.planner.spec import parse_spec

NY = "America/New_York"
SH = "Asia/Shanghai"  # no DST since 1991
UTC_TZ = "UTC"


def show(label, res):
    print(f"\n[{label}] local={res.local_naive.isoformat()} zone={res.zone_name} "
          f"=> kind={res.kind.value}")
    for inst in res.instants:
        print(f"    fold={inst.fold} UTC={inst.instant_utc.isoformat()} "
              f"offset={inst.utc_offset_minutes}m")


# ── calendar kernel ─────────────────────────────────────────────────────────

def test_spring_forward_gap_is_classified_gap():
    res = resolve_wall_time(date(2024, 3, 10), time(2, 30), NY)
    show("spring-gap NY", res)
    assert res.kind is LocalKind.GAP
    assert res.instants == ()
    assert res.offset_before_minutes == -300  # EST
    assert res.offset_after_minutes == -240   # EDT


def test_fall_back_ambiguous_has_two_instants_in_order():
    res = resolve_wall_time(date(2024, 11, 3), time(1, 30), NY)
    show("fall-ambiguous NY", res)
    assert res.kind is LocalKind.AMBIGUOUS
    assert [i.fold for i in res.instants] == [0, 1]
    assert [i.instant_utc for i in res.instants] == [
        datetime(2024, 11, 3, 5, 30, tzinfo=timezone.utc),
        datetime(2024, 11, 3, 6, 30, tzinfo=timezone.utc),
    ]


def test_normal_local_time_is_literal():
    res = resolve_wall_time(date(2024, 6, 1), time(9, 0), SH)
    assert res.kind is LocalKind.LITERAL
    assert res.instants[0].instant_utc == datetime(2024, 6, 1, 1, 0, tzinfo=timezone.utc)


def test_non_dst_zone_never_gaps():
    res_g = resolve_wall_time(date(2024, 3, 10), time(2, 30), SH)
    res_f = resolve_wall_time(date(2024, 11, 3), time(1, 30), SH)
    assert res_g.kind is LocalKind.LITERAL
    assert res_f.kind is LocalKind.LITERAL


# ── planner window behaviour ────────────────────────────────────────────────

def _daily(zone, **over):
    spec = {
        "name": "j", "timezone": zone, "frequency": "daily", "at": "02:30",
        "start_date": "2024-01-01", "end_date": "2024-12-31",
        "executor": {"type": "log"},
    }
    spec.update(over)
    return parse_spec(spec)


def test_gap_day_is_skipped_and_recorded():
    spec = _daily(NY)
    win = plan_window(spec, date(2024, 3, 9), date(2024, 3, 11))
    days = {f.date_local for f in win.fires}
    assert date(2024, 3, 10) not in days
    assert {f.date_local for f in win.fires} == {
        date(2024, 3, 9), date(2024, 3, 11)
    }
    gaps = [s for s in win.skipped if s.reason is SkipReason.GAP]
    assert len(gaps) == 1 and gaps[0].date_local == date(2024, 3, 10)
    print(f"\n[gap-skip] {gaps[0].explain()}")


def test_ambiguous_day_fires_twice_with_distinct_fold_identities():
    # 01:30 is the ambiguous wall time on 2024-11-03 in New York.
    spec = _daily(NY, at="01:30")
    win = plan_window(spec, date(2024, 11, 3), date(2024, 11, 3))
    keys = sorted((f.due_utc, f.fold) for f in win.fires)
    print(f"\n[ambiguous-both] fires={[f.explain() for f in win.fires]}")
    assert keys == [
        (datetime(2024, 11, 3, 5, 30, tzinfo=timezone.utc), 0),
        (datetime(2024, 11, 3, 6, 30, tzinfo=timezone.utc), 1),
    ]
    # identities differ only in fold
    ids = {f.fire_key("s", 1) for f in win.fires}
    assert len(ids) == 2
    assert ids == {"s:v1:base:307:f0", "s:v1:base:307:f1"}


def test_ambiguous_policy_early_and_late():
    early = plan_window(_daily(NY, at="01:30", ambiguous_policy="early"),
                        date(2024, 11, 3), date(2024, 11, 3))
    late = plan_window(_daily(NY, at="01:30", ambiguous_policy="late"),
                       date(2024, 11, 3), date(2024, 11, 3))
    assert [(f.due_utc, f.fold) for f in early.fires] == [
        (datetime(2024, 11, 3, 5, 30, tzinfo=timezone.utc), 0)
    ]
    assert [(f.due_utc, f.fold) for f in late.fires] == [
        (datetime(2024, 11, 3, 6, 30, tzinfo=timezone.utc), 1)
    ]


def test_exception_date_suppresses_base_and_extra():
    # A calendar date that is BOTH a recurring match and an exception is
    # suppressed; a one-off on a non-rule weekday that is later excepted is
    # also suppressed. Parser forbids listing the same date in both buckets
    # (covered in validation tests), so the one-off suppression is asserted
    # at planner level with the exception set constructed directly.
    spec = parse_spec({
        "name": "w", "timezone": NY, "frequency": "weekly",
        "weekdays": [1], "at": "01:30",
        "start_date": "2024-03-01", "end_date": "2024-11-10",
        "exceptions": ["2024-03-11", "2024-11-04"],
        "extra_dates": ["2024-03-14"],
        "executor": {"type": "log"},
    })
    # copy spec with the one-off also excepted (post-parse scenario)
    spec = spec.model_copy(
        update={"exceptions": ["2024-03-11", "2024-11-04", "2024-03-14"]}
    )
    win = plan_window(spec, date(2024, 3, 1), date(2024, 11, 10))
    due_days = {f.date_local for f in win.fires}
    assert date(2024, 3, 11) not in due_days  # recurring Monday suppressed
    assert date(2024, 3, 14) not in due_days  # one-off Thursday suppressed
    assert date(2024, 11, 4) not in due_days   # post-ambiguity Monday suppressed
    assert date(2024, 3, 18) in due_days       # ordinary Monday still fires
    exc = {s.date_local for s in win.skipped if s.reason is SkipReason.EXCEPTION}
    assert exc >= {date(2024, 3, 14), date(2024, 3, 11), date(2024, 11, 4)}


def test_extra_date_fires_and_collision_is_shadowed():
    spec = _daily(NY, extra_dates=["2024-07-04"])
    win = plan_window(spec, date(2024, 7, 4), date(2024, 7, 4))
    # July 4 already produced by daily rule -> extra must not duplicate
    assert len(win.fires) == 1
    assert win.fires[0].kind is FireKind.BASE
    assert any(s.reason is SkipReason.SHADOWED_BY_BASE for s in win.skipped)


def test_weekly_filter_and_weekend_exclusion():
    spec = parse_spec({
        "name": "mon", "timezone": "UTC", "frequency": "weekly",
        "weekdays": [1], "at": "08:00", "start_date": "2024-01-01",
        "end_date": "2024-01-31", "executor": {"type": "log"},
    })
    win = plan_window(spec, date(2024, 1, 1), date(2024, 1, 31))
    assert [f.date_local for f in win.fires] == [
        date(2024, 1, 1), date(2024, 1, 8), date(2024, 1, 15),
        date(2024, 1, 22), date(2024, 1, 29),
    ]


def test_monthly_day31_skips_short_months():
    spec = parse_spec({
        "name": "m31", "timezone": "UTC", "frequency": "monthly",
        "month_days": [31], "at": "00:00", "start_date": "2024-01-01",
        "end_date": "2024-12-31", "executor": {"type": "log"},
    })
    win = plan_window(spec, date(2024, 1, 1), date(2024, 12, 31))
    assert [f.date_local for f in win.fires] == [
        date(2024, 1, 31), date(2024, 3, 31), date(2024, 5, 31),
        date(2024, 7, 31), date(2024, 8, 31), date(2024, 10, 31),
        date(2024, 12, 31),
    ]


def test_last_monday_of_month():
    spec = parse_spec({
        "name": "last-mon", "timezone": "UTC", "frequency": "monthly",
        "nth_weekdays": [{"nth": -1, "weekday": 1}], "at": "09:00",
        "start_date": "2024-01-01", "end_date": "2024-06-30",
        "executor": {"type": "log"},
    })
    win = plan_window(spec, date(2024, 1, 1), date(2024, 6, 30))
    assert [f.date_local for f in win.fires] == [
        date(2024, 1, 29), date(2024, 2, 26), date(2024, 3, 25),
        date(2024, 4, 29), date(2024, 5, 27), date(2024, 6, 24),
    ]


def test_leap_day_february_29_daily():
    spec = _daily(UTC_TZ, start_date="2024-02-27", end_date="2024-03-01", at="12:00")
    win = plan_window(spec, date(2024, 2, 27), date(2024, 3, 1))
    assert [f.date_local for f in win.fires] == [
        date(2024, 2, 27), date(2024, 2, 28), date(2024, 2, 29),
        date(2024, 3, 1),
    ]


def test_feb29_monthly_in_non_leap_year_silently_absent_but_present_2024():
    body = dict(
        name="feb29", timezone="UTC", frequency="monthly", month_days=[29],
        at="00:00", executor={"type": "log"},
    )
    s2023 = parse_spec({**body, "start_date": "2023-01-01", "end_date": "2023-12-31"})
    w2023 = plan_window(s2023, date(2023, 1, 1), date(2023, 12, 31))
    assert all(f.date_local.month != 2 for f in w2023.fires)
    s2024 = parse_spec({**body, "start_date": "2024-01-01", "end_date": "2024-12-31"})
    w2024 = plan_window(s2024, date(2024, 1, 1), date(2024, 12, 31))
    assert date(2024, 2, 29) in [f.date_local for f in w2024.fires]


def test_ordinal_stable_under_window_shift():
    spec = _daily(NY)
    w1 = plan_window(spec, date(2024, 1, 1), date(2024, 1, 31))
    w2 = plan_window(spec, date(2024, 6, 1), date(2024, 6, 30))
    w3 = plan_window(spec, date(2024, 1, 1), date(2024, 12, 31))
    map1 = {(f.date_local, f.fold): f.ordinal for f in w1.fires}
    map3 = {(f.date_local, f.fold): f.ordinal for f in w3.fires}
    for k, v in map1.items():
        assert map3[k] == v
    # daily ordinal is simply days since start (gap day is still a day index)
    june = {f.date_local: f.ordinal for f in w2.fires}
    assert june[date(2024, 6, 15)] == (date(2024, 6, 15) - date(2024, 1, 1)).days


def test_window_too_large_is_rejectable():
    from scheduler.errors import ResourceLimitError

    spec = _daily(UTC_TZ)
    with pytest.raises(ResourceLimitError) as ei:
        plan_window(spec, date(2020, 1, 1), date(2030, 1, 1))
    assert ei.value.code == "LIMIT-PLAN-WINDOW"
