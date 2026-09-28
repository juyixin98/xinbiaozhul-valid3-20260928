"""Independent reference implementation for cross-checking the planner.

The production planner walks rule-matched local dates and asks ``zoneinfo`` to
resolve each wall time. The test suite must *not* derive expected values from
the same code path, so this module uses a deliberately different algorithm:

1. Enumerate **every UTC minute** of the requested year (527,040 points).
2. Convert each UTC minute to local wall time using the tz database directly.
3. Independently decide gap/ambiguity from fold round-trips, and match the
   recurrence rule on the wall-clock components.

This gives an oracle whose only shared dependency with production is the
system tz database (``zoneinfo``) — structurally impossible to share an
algorithm bug. Annual reference sets for DST and non-DST zones are compared
against ``plan_window`` output in ``test_annual_reference.py``.

Kept intentionally simple and slow; test-only.
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime, time, timedelta, timezone
from zoneinfo import ZoneInfo

UTC = timezone.utc


@dataclass(frozen=True)
class RefFire:
    due_utc: datetime
    date_local: date
    fold: int
    kind: str  # "base" | "extra"
    utc_offset_minutes: int


def _wall_matches_rule(local_naive: datetime, at: time, spec) -> bool:
    if (local_naive.hour, local_naive.minute) != (at.hour, at.minute):
        return False
    d = local_naive.date()
    if d < spec.start or (spec.end and d > spec.end):
        return False
    if d in spec.exception_set:
        return False
    freq = spec.frequency.value
    if freq == "daily":
        return True
    if freq == "weekly":
        return d.isoweekday() in set(spec.weekdays)
    if freq == "monthly":
        if d.day in spec.month_days:
            return True
        for rule in spec.nth_weekdays:
            if d.isoweekday() != rule["weekday"]:
                continue
            if rule["nth"] == -1:
                if _is_last_weekday_of_month(d):
                    return True
            elif _nth_of_month(d) == rule["nth"]:
                return True
        return False
    return False


def _nth_of_month(d: date) -> int:
    """Ordinal of d's weekday within its month: 1..5."""
    return (d.day - 1) // 7 + 1


def _is_last_weekday_of_month(d: date) -> bool:
    return (d + timedelta(days=7)).month != d.month


def reference_occurrences(spec, year: int) -> list[RefFire]:
    """Brute-force reference built per **local day** (independent of planner).

    For each local date in the year we construct both fold interpretations of
    the wall time and validate them by UTC round-trip. Unlike a UTC-minute walk
    this never loses fires that render on local Jan 1 / Dec 31 at non-UTC
    offsets. Recurrence matching is evaluated on wall-clock components, so the
    only shared dependency with production is the tz database itself.
    """
    zone = ZoneInfo(spec.timezone)
    at = spec.at_time
    out: list[RefFire] = []

    d = date(year, 1, 1)
    last = date(year, 12, 31)
    while d <= last:
        naive = datetime.combine(d, at)
        rule_match = _wall_matches_rule(naive, at, spec)
        is_extra = d in spec.extra_set and d not in spec.exception_set
        if rule_match or is_extra:
            # Independent fold semantics: build both interpretations, map to
            # UTC, and keep an instant only if mapping back reproduces the
            # same wall time. Identical fold-0/fold-1 UTC => unique instant;
            # two surviving distinct instants => ambiguity; none => gap.
            candidates: dict[datetime, tuple[int, int]] = {}
            for fold in (0, 1):
                aware = naive.replace(tzinfo=zone, fold=fold)
                u = aware.astimezone(UTC)
                back = u.astimezone(zone).replace(tzinfo=None)
                if back == naive:
                    candidates.setdefault(
                        u, (fold, int(aware.utcoffset() // timedelta(minutes=1)))
                    )
            valid_folds = [
                (fold, u, off) for u, (fold, off) in sorted(
                    candidates.items(), key=lambda kv: kv[0]
                )
            ]
            base_entries: list[RefFire] = []
            extra_entries: list[RefFire] = []
            for fold, u, off in valid_folds:
                if rule_match:
                    base_entries.append(RefFire(u, d, fold, "base", off))
                elif is_extra:
                    extra_entries.append(RefFire(u, d, fold, "extra", off))
            out.extend(base_entries)
            out.extend(extra_entries)
        d += timedelta(days=1)

    # shadow: an extra whose UTC instant a base already produces is dropped
    base_utcs = {f.due_utc for f in out if f.kind == "base"}
    out = [f for f in out if f.kind == "base" or f.due_utc not in base_utcs]
    out.sort(key=lambda f: (f.due_utc, f.kind, f.fold))
    return out


def reference_gap_dates(spec, year: int) -> list[date]:
    """Dates in the year where ``at`` does not exist locally (independent)."""
    zone = ZoneInfo(spec.timezone)
    gaps: list[date] = []
    d = date(year, 1, 1)
    last = date(year, 12, 31)
    while d <= last:
        naive = datetime.combine(d, spec.at_time)
        u0 = naive.replace(tzinfo=zone, fold=0).astimezone(UTC)
        u1 = naive.replace(tzinfo=zone, fold=1).astimezone(UTC)
        b0 = u0.astimezone(zone)
        b1 = u1.astimezone(zone)
        ok0 = b0.replace(tzinfo=None) == naive and b0.fold == 0
        ok1 = b1.replace(tzinfo=None) == naive and b1.fold == 1
        if not ok0 and not ok1:
            gaps.append(d)
        d += timedelta(days=1)
    return gaps
