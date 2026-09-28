"""Occurrence planner — turn a :class:`ScheduleSpec` into concrete UTC fires.

Pure, deterministic, I/O-free. Given a local-date window it emits every fire
that falls in the window, each with a **stable identity**:

    fire_key = "{schedule_id}:v{version}:{kind}:{ordinal}:f{fold}"

* ``kind``    = ``base`` (recurring rule) or ``extra`` (one-off date)
* ``ordinal`` = position of the *local date* in the rule's base sequence
  counted from ``start_date`` (extra fires use the ISO date ordinal)
* ``fold``    = 0/1 for the two sides of a fall-back ambiguity

Because the key contains the schedule **version**, editing a schedule creates
a new identity space: pending old-version fires are CANCELLED and already
confirmed fires are untouched — history never re-fires (see engine/repo).

Deterministic rules applied here (all visible in ``PlannedWindow.skipped``):

* GAP local time        -> skip (``gap``), no instant exists
* AMBIGUOUS + both      -> two fires, fold 0 then fold 1
* AMBIGUOUS + early/late-> one fire
* exception date        -> every fire that local day suppressed (``exception``)
* extra == base instant -> extra suppressed (``shadowed-by-base``)
* window bounds         -> local dates only; a hard cap rejects huge windows
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime, timedelta
from enum import Enum

from ..errors import ResourceLimitError
from .calendar import LocalKind, resolve_wall_time
from .spec import AmbiguousPolicy, ScheduleSpec

# Even an open-ended schedule is only ever planned over bounded windows; this
# cap keeps the generator O(window) and gives a rejectable condition.
MAX_PLAN_WINDOW_DAYS = 1830  # ~5 years


class FireKind(str, Enum):
    BASE = "base"
    EXTRA = "extra"


class SkipReason(str, Enum):
    GAP = "gap"
    EXCEPTION = "exception"
    OUT_OF_BOUNDS = "out-of-bounds"  # outside [start_date, end_date]
    SHADOWED_BY_BASE = "shadowed-by-base"


@dataclass(frozen=True)
class PlannedFire:
    kind: FireKind
    ordinal: int
    date_local: date
    fold: int
    due_utc: datetime  # aware UTC
    utc_offset_minutes: int

    def fire_key(self, schedule_id: str, version: int) -> str:
        return f"{schedule_id}:v{version}:{self.kind.value}:{self.ordinal}:f{self.fold}"

    def explain(self, schedule_id: str = "", version: int = 0) -> dict:
        return {
            "fire_key": self.fire_key(schedule_id, version) if schedule_id else None,
            "kind": self.kind.value,
            "ordinal": self.ordinal,
            "date_local": self.date_local.isoformat(),
            "fold": self.fold,
            "due_utc": self.due_utc.isoformat(),
            "utc_offset_minutes": self.utc_offset_minutes,
        }


@dataclass(frozen=True)
class SkippedDate:
    date_local: date
    reason: SkipReason
    detail: str = ""

    def explain(self) -> dict:
        return {
            "date_local": self.date_local.isoformat(),
            "reason": self.reason.value,
            "detail": self.detail,
        }


@dataclass(frozen=True)
class PlannedWindow:
    fires: tuple[PlannedFire, ...]  # sorted: (due_utc, fire_key)
    skipped: tuple[SkippedDate, ...]
    window_start_local: date
    window_end_local: date

    def explain(self, schedule_id: str = "", version: int = 0) -> dict:
        return {
            "window": [
                self.window_start_local.isoformat(),
                self.window_end_local.isoformat(),
            ],
            "fires": [f.explain(schedule_id, version) for f in self.fires],
            "skipped": [s.explain() for s in self.skipped],
        }


# ───────────────────────── rule sequence ────────────────────────────────────


def _base_days_in_month(spec: ScheduleSpec, year: int, month: int) -> list[date]:
    """Local dates in a given month matched by the recurring rule (sorted)."""
    if month == 12:
        next_m = date(year + 1, 1, 1)
    else:
        next_m = date(year, month + 1, 1)
    first = date(year, month, 1)
    days: list[date] = []

    if spec.frequency.value == "daily":
        d = first
        while d < next_m:
            days.append(d)
            d += timedelta(days=1)

    elif spec.frequency.value == "weekly":
        wanted = set(spec.weekdays)
        d = first
        while d < next_m:
            if d.isoweekday() in wanted:
                days.append(d)
            d += timedelta(days=1)

    else:  # monthly
        for md in spec.month_days:
            try:
                days.append(date(year, month, md))
            except ValueError:
                # e.g. day 31 in February -> that month simply has no match
                continue
        for rule in spec.nth_weekdays:
            hit = _nth_weekday_of_month(year, month, rule["weekday"], rule["nth"])
            if hit is not None:
                days.append(hit)
        days.sort()
    return days


def _nth_weekday_of_month(year: int, month: int, weekday: int, nth: int) -> date | None:
    if nth > 0:
        d = date(year, month, 1)
        # isoweekday(): Mon=1..Sun=7. shift is a forward-only distance in 0..6.
        shift = (weekday - d.isoweekday()) % 7
        cand = d + timedelta(days=shift + 7 * (nth - 1))
        return cand if cand.month == month else None
    if month == 12:
        next_first = date(year + 1, 1, 1)
    else:
        next_first = date(year, month + 1, 1)
    d = next_first - timedelta(days=1)  # last day of month
    shift = (d.isoweekday() - weekday) % 7
    return d - timedelta(days=shift)


def _base_ordinal(spec: ScheduleSpec, day: date) -> int:
    """0-based index of ``day`` in the recurring-rule date stream.

    Counted from ``start_date`` so it is independent of which window we plan;
    skipping base dates before start is exactly what the count encodes.
    """
    start = spec.start
    if spec.frequency.value == "daily":
        return (day - start).days

    if spec.frequency.value == "weekly":
        total = 0
        for w in sorted(spec.weekdays):
            # first occurrence of weekday w on/after start
            shift = (w - start.isoweekday()) % 7
            first_w = start + timedelta(days=shift)
            if first_w > day:
                continue
            total += (day - first_w).days // 7 + 1
        return int(total) - 1

    # monthly: sum matched days in prior months + index within this month
    total = 0
    cur_y, cur_m = start.year, start.month
    while (cur_y, cur_m) < (day.year, day.month):
        month_days = [
            d for d in _base_days_in_month(spec, cur_y, cur_m) if d >= start
        ]
        total += len(month_days)
        if cur_m == 12:
            cur_y, cur_m = cur_y + 1, 1
        else:
            cur_m += 1
    within = [
        d
        for d in _base_days_in_month(spec, day.year, day.month)
        if start <= d <= day
    ]
    if day not in within:
        # Should not happen for callers, but an undefined ordinal must never be
        # invented: fail loudly instead of returning a guess.
        raise ResourceLimitError(
            f"cannot derive ordinal for non-base day {day}", code="ENGINE-DIVERGED"
        )
    return total + within.index(day)


# ───────────────────────── window planning ──────────────────────────────────


def plan_window(
    spec: ScheduleSpec,
    window_start: date,
    window_end: date,
) -> PlannedWindow:
    """All fires with local dates in the inclusive window.

    The caller (engine) narrows by the UTC interval afterwards; local-date
    windowing keeps DST edge days from being dropped.
    """
    if window_end < window_start:
        raise ResourceLimitError(
            "plan window end before start",
            code="VAL-PLAN-WINDOW",
            details={"start": window_start.isoformat(), "end": window_end.isoformat()},
        )
    span = (window_end - window_start).days
    if span > MAX_PLAN_WINDOW_DAYS:
        raise ResourceLimitError(
            f"plan window of {span} days exceeds cap {MAX_PLAN_WINDOW_DAYS}",
            code="LIMIT-PLAN-WINDOW",
            details={"span_days": span, "cap": MAX_PLAN_WINDOW_DAYS},
        )

    exceptions = spec.exception_set
    start, end = spec.start, spec.end

    skipped: list[SkippedDate] = []
    base_fires: list[PlannedFire] = []
    base_instants: set[datetime] = set()

    # Candidate (local day, kind) pairs. A day both in the rule and listed as
    # an extra yields two candidates; the extra one is shadowed if the base
    # produces the same UTC instant.
    candidates = _candidate_days(spec, window_start, window_end)

    for day, is_base in candidates:
        # [start_date, end_date] bounds the recurring rule; one-offs were
        # validated into range at parse time, re-checked defensively.
        if is_base and (day < start or (end is not None and day > end)):
            skipped.append(SkippedDate(day, SkipReason.OUT_OF_BOUNDS))
            continue
        if day in exceptions:
            skipped.append(
                SkippedDate(
                    day, SkipReason.EXCEPTION,
                    "exception date suppresses base and one-off fires"
                    if not is_base
                    else "exception date suppresses recurring fire",
                )
            )
            continue

        resolution = resolve_wall_time(day, spec.at_time, spec.timezone)
        if resolution.kind is LocalKind.GAP:
            skipped.append(
                SkippedDate(
                    day, SkipReason.GAP,
                    f"{spec.at} does not exist on this date in {spec.timezone} "
                    f"(offset jumps {resolution.offset_before_minutes} -> "
                    f"{resolution.offset_after_minutes})",
                )
            )
            continue

        instants = list(resolution.instants)
        if resolution.kind is LocalKind.AMBIGUOUS:
            if spec.ambiguous_policy is AmbiguousPolicy.EARLY:
                instants = instants[:1]
            elif spec.ambiguous_policy is AmbiguousPolicy.LATE:
                instants = instants[1:]
            # BOTH: keep both

        if is_base:
            ordinal = _base_ordinal(spec, day)
            for li in instants:
                pf = PlannedFire(
                    FireKind.BASE, ordinal, day, li.fold,
                    li.instant_utc, li.utc_offset_minutes,
                )
                base_fires.append(pf)
                base_instants.add(li.instant_utc)
        else:
            # one-off: identity from ISO date ordinal (stable, unique per date)
            for li in instants:
                if li.instant_utc in base_instants:
                    skipped.append(
                        SkippedDate(
                            day, SkipReason.SHADOWED_BY_BASE,
                            "one-off UTC instant already produced by base rule",
                        )
                    )
                    continue
                base_fires.append(
                    PlannedFire(
                        FireKind.EXTRA, day.toordinal(), day, li.fold,
                        li.instant_utc, li.utc_offset_minutes,
                    )
                )

    base_fires.sort(key=lambda f: (f.due_utc, f.kind.value, f.ordinal, f.fold))
    # dedupe identical (date, reason) entries for days with two candidates
    uniq_skipped = {(s.date_local, s.reason): s for s in skipped}
    skipped_sorted = sorted(
        uniq_skipped.values(), key=lambda s: (s.date_local, s.reason.value)
    )
    return PlannedWindow(
        tuple(base_fires), tuple(skipped_sorted), window_start, window_end
    )


def _candidate_days(
    spec: ScheduleSpec, window_start: date, window_end: date
) -> list[tuple[date, bool]]:
    """``(day, is_base)`` for every rule match and every one-off in window."""
    found: list[tuple[date, bool]] = []

    for d in spec.extra_set:
        if window_start <= d <= window_end:
            found.append((d, False))

    y, m = window_start.year, window_start.month
    while (y, m) <= (window_end.year, window_end.month):
        for d in _base_days_in_month(spec, y, m):
            if window_start <= d <= window_end:
                found.append((d, True))
        if m == 12:
            y, m = y + 1, 1
        else:
            m += 1

    return sorted(found, key=lambda x: (x[0], not x[1]))
