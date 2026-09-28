"""Calendar / timezone kernel.

Pure functions, no I/O. This is the only module that talks to ``zoneinfo`` and
the only place that decides what a wall-clock expression means.

DST rules (explicit, see README "Time-zone semantics"):

* **Spring-forward gap** — a local time that does not exist (e.g. 02:30 on
  America/New_York 2024-03-10). ``resolve_local`` classifies it ``GAP`` and the
  planner *skips* it. We never silently move it to 01:30/03:30: neither
  fold=0 nor fold=1 round-trips through UTC, and guessing would manufacture a
  trigger the user did not ask for.
* **Fall-back ambiguity** — a local time that occurs twice (e.g. 01:30 on
  2024-11-03, once at 05:30Z in EDT and again at 06:30Z in EST). Both instants
  *do* exist on the timeline, so with the default policy ``AMBIGUOUS_BOTH`` we
  fire **twice**, each with its own ``fold`` in its identity.
  ``AMBIGUOUS_EARLY`` / ``AMBIGUOUS_LATE`` select one.

Every result includes the inputs to the decision (offset before/after, fold)
so callers and audit logs can explain the verdict.
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime, time, timedelta, timezone
from enum import Enum
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from ..errors import ValidationError

UTC = timezone.utc


class LocalKind(str, Enum):
    LITERAL = "LITERAL"  # exactly one UTC instant
    AMBIGUOUS = "AMBIGUOUS"  # two instants (fall-back fold); both returned
    GAP = "GAP"  # zero instants (spring-forward); skipped


@dataclass(frozen=True)
class LocalInstant:
    """One concrete UTC instant produced from a wall-clock expression."""

    instant_utc: datetime  # always tz-aware, UTC
    fold: int  # 0 = first occurrence (before transition), 1 = second
    utc_offset_minutes: int  # local - UTC at that instant

    def as_utc(self) -> datetime:
        return self.instant_utc


@dataclass(frozen=True)
class LocalResolution:
    kind: LocalKind
    local_naive: datetime
    zone_name: str
    instants: tuple[LocalInstant, ...]  # 0 for GAP, 1 LITERAL, 2 AMBIGUOUS
    # Offset that the wall clock "jumps over" / repeats around (for audit):
    offset_before_minutes: int
    offset_after_minutes: int

    def explain(self) -> dict:
        return {
            "kind": self.kind.value,
            "local": self.local_naive.isoformat(),
            "zone": self.zone_name,
            "instants": [
                {
                    "utc": x.instant_utc.isoformat(),
                    "fold": x.fold,
                    "utc_offset_minutes": x.utc_offset_minutes,
                }
                for x in self.instants
            ],
            "offset_before_minutes": self.offset_before_minutes,
            "offset_after_minutes": self.offset_after_minutes,
        }


def load_zone(zone_name: str) -> ZoneInfo:
    try:
        return ZoneInfo(zone_name)
    except ZoneInfoNotFoundError:
        raise ValidationError(
            f"unknown IANA time zone: {zone_name!r}",
            field="timezone",
            code="VAL-TIMEZONE",
        )
    except (ValueError, TypeError) as exc:  # malformed zone strings
        raise ValidationError(
            f"invalid time zone value: {zone_name!r} ({exc})",
            field="timezone",
            code="VAL-TIMEZONE",
        )


def _local_with_fold(naive: datetime, zone: ZoneInfo, fold: int) -> datetime:
    return naive.replace(tzinfo=zone, fold=fold)


def _offset_minutes(dt_aware: datetime) -> int:
    return int(dt_aware.utcoffset() // timedelta(minutes=1))


def resolve_local(naive: datetime, zone_name: str) -> LocalResolution:
    """Resolve a naive wall-clock datetime in ``zone_name`` to UTC instant(s).

    Classification uses the fold round-trip rule:

    * fold 0 and fold 1 convert to the same UTC instant, and that instant
      converts back to the same wall time  -> LITERAL
    * they convert to *different* UTC instants                      -> AMBIGUOUS
    * neither round-trips back to the same wall time                -> GAP
    """
    if naive.tzinfo is not None:
        raise ValidationError(
            "resolve_local expects a naive datetime", code="VAL-INTERNAL"
        )
    zone = load_zone(zone_name)

    d0 = _local_with_fold(naive, zone, 0)
    d1 = _local_with_fold(naive, zone, 1)
    u0 = d0.astimezone(UTC)
    u1 = d1.astimezone(UTC)
    off0 = _offset_minutes(d0)
    off1 = _offset_minutes(d1)

    # A fold value is "real" only when re-asserting it after a UTC round-trip
    # survives. With fixed-offset zones (UTC, Asia/Shanghai) the fold is always
    # discarded, so we classify purely by what the two fold *interpretations*
    # map to:
    #   same UTC instant           -> LITERAL (fold irrelevant)
    #   two instants, both valid   -> AMBIGUOUS (fall-back)
    #   neither round-trips        -> GAP (spring-forward)
    back0 = u0.astimezone(zone).replace(tzinfo=None) == naive
    back1 = u1.astimezone(zone).replace(tzinfo=None) == naive

    if u0 == u1 and back0:
        return LocalResolution(
            LocalKind.LITERAL,
            naive,
            zone_name,
            (LocalInstant(u0, 0, off0),),
            off0,
            off1,
        )
    if u0 != u1 and back0 and back1:
        # fall-back: fold 0 happens first chronologically (EDT side), fold 1 second.
        instants = sorted(
            (
                LocalInstant(u0, 0, off0),
                LocalInstant(u1, 1, off1),
            ),
            key=lambda x: x.instant_utc,
        )
        return LocalResolution(
            LocalKind.AMBIGUOUS, naive, zone_name, tuple(instants), off0, off1
        )
    # Gap: the two fold guesses bracket the hole, neither round-trips.
    return LocalResolution(LocalKind.GAP, naive, zone_name, (), off0, off1)


def resolve_wall_time(day: date, hm: time, zone_name: str) -> LocalResolution:
    return resolve_local(datetime.combine(day, hm), zone_name)
