"""Schedule specification — parsed, validated plan definition.

A :class:`ScheduleSpec` is the immutable input to the planner. It is parsed
once at create/update time; the kernel never re-parses raw dicts. Validation
raises :class:`scheduler.errors.ValidationError` with ``VAL-*`` codes so the
API can reject each documented condition distinctly (tests assert on code).

Recurrence model (deliberately small but complete):

* ``daily``    — every day in ``dates`` range at ``at`` local time
* ``weekly``   — on ``weekdays`` (Mon=1..Sun=7, ISO)
* ``monthly``  — on ``month_days`` (1..31) and/or ``nth_weekdays``
                 like {"nth": -1, "weekday": 1} = last Monday

Plus ``extra_dates`` (one-off additions) and ``exceptions`` (local dates that
suppress every occurrence that day, including one-offs).
"""
from __future__ import annotations

from datetime import date, datetime, time
from enum import Enum
from typing import Any

from pydantic import BaseModel, ConfigDict, Field, field_validator

from ..errors import ValidationError
from .calendar import load_zone

ISO_DATE = "%Y-%m-%d"


class Frequency(str, Enum):
    DAILY = "daily"
    WEEKLY = "weekly"
    MONTHLY = "monthly"


class GapPolicy(str, Enum):
    SKIP = "skip"  # the only defined policy; forward-shifting is refused at parse


class AmbiguousPolicy(str, Enum):
    BOTH = "both"  # fire at fold=0 and fold=1 (default)
    EARLY = "early"  # only the first (pre-transition, e.g. EDT) instant
    LATE = "late"  # only the second (post-transition, e.g. EST) instant


class ExecutorType(str, Enum):
    LOG = "log"  # delivery = insert a row; always succeeds (tests)
    WEBHOOK = "webhook"  # POST JSON payload to url


class ExecutorSpec(BaseModel):
    model_config = ConfigDict(extra="forbid")

    type: ExecutorType
    url: str | None = None
    # Free-form JSON attached to every delivery (correlation ids etc.).
    payload_template: dict[str, Any] = Field(default_factory=dict)

    @field_validator("url")
    @classmethod
    def _check_url(cls, v, info):
        kind = info.data.get("type")
        if kind == ExecutorType.WEBHOOK:
            if not v:
                raise ValueError("webhook executor requires a url")
            if not v.startswith(("http://", "https://")):
                raise ValueError("webhook url must start with http:// or https://")
        elif v is not None:
            raise ValueError("url is only valid for webhook executor")
        return v


class ScheduleSpec(BaseModel):
    model_config = ConfigDict(extra="forbid")

    # NOTE field order matters: executor is validated before model_post_init
    # runs cross-field checks.
    executor: ExecutorSpec
    name: str = Field(min_length=1, max_length=200)
    timezone: str
    frequency: Frequency
    at: str = Field(description="wall-clock time HH:MM (minute precision)")
    weekdays: list[int] = Field(default_factory=list)  # ISO 1..7
    month_days: list[int] = Field(default_factory=list)  # 1..31
    nth_weekdays: list[dict[str, int]] = Field(default_factory=list)
    extra_dates: list[str] = Field(default_factory=list)  # YYYY-MM-DD
    exceptions: list[str] = Field(default_factory=list)  # YYYY-MM-DD
    start_date: str  # inclusive, local date
    end_date: str | None = None  # inclusive; None = open-ended (planner bounds it)
    gap_policy: GapPolicy = GapPolicy.SKIP
    ambiguous_policy: AmbiguousPolicy = AmbiguousPolicy.BOTH

    @field_validator("executor", mode="after")
    @classmethod
    def _executor(cls, v: "ExecutorSpec") -> "ExecutorSpec":
        # Raised here as pydantic ValidationError carrying the field path;
        # parse_spec converts it into the VAL-BAD-SPEC envelope.
        if v.type is ExecutorType.WEBHOOK and not v.url:
            raise ValueError("webhook executor requires a url")
        return v

    # ── validators (each rejection has its own code) ───────────────────────

    @field_validator("timezone")
    @classmethod
    def _tz(cls, v: str) -> str:
        load_zone(v)  # raises VAL-TIMEZONE if unknown
        return v

    @field_validator("at")
    @classmethod
    def _at(cls, v: str) -> str:
        try:
            datetime.strptime(v, "%H:%M")
        except ValueError:
            raise ValueError("at must be HH:MM (minute precision)")
        if len(v.split(":")) != 2:
            raise ValueError("at must be HH:MM (seconds are not supported)")
        return v

    @field_validator("weekdays")
    @classmethod
    def _weekdays(cls, v: list[int]) -> list[int]:
        bad = [x for x in v if not 1 <= x <= 7]
        if bad:
            raise ValueError(f"weekdays must be ISO weekday 1..7, got {bad}")
        if len(set(v)) != len(v):
            raise ValueError("weekdays must not contain duplicates")
        return v

    @field_validator("month_days")
    @classmethod
    def _month_days(cls, v: list[int]) -> list[int]:
        bad = [x for x in v if not 1 <= x <= 31]
        if bad:
            raise ValueError(f"month_days must be 1..31, got {bad}")
        if len(set(v)) != len(v):
            raise ValueError("month_days must not contain duplicates")
        return v

    @field_validator("nth_weekdays")
    @classmethod
    def _nth(cls, v: list[dict[str, int]]) -> list[dict[str, int]]:
        seen: set[tuple[int, int]] = set()
        for item in v:
            if set(item) != {"nth", "weekday"}:
                raise ValueError("nth_weekdays entries must be exactly {nth, weekday}")
            nth, wd = item["nth"], item["weekday"]
            if nth not in (-1, 1, 2, 3, 4, 5):
                raise ValueError(f"nth must be one of -1,1..5, got {nth}")
            if not 1 <= wd <= 7:
                raise ValueError(f"weekday must be ISO 1..7, got {wd}")
            key = (nth, wd)
            if key in seen:
                raise ValueError(f"duplicate nth_weekday {key}")
            seen.add(key)
        return v

    @field_validator("extra_dates", "exceptions")
    @classmethod
    def _dates(cls, v: list[str]) -> list[str]:
        for s in v:
            try:
                datetime.strptime(s, ISO_DATE)
            except ValueError:
                raise ValueError(f"date must be YYYY-MM-DD, got {s!r}")
        if len(set(v)) != len(v):
            raise ValueError("date list must not contain duplicates")
        return v

    def model_post_init(self, _ctx: Any) -> None:
        errs: list[str] = []
        freq = self.frequency

        if freq is Frequency.WEEKLY and not self.weekdays:
            errs.append("weekly schedule requires at least one weekday")
        if freq is not Frequency.WEEKLY and self.weekdays:
            errs.append("weekdays is only valid for weekly frequency")
        if freq is Frequency.MONTHLY and not (self.month_days or self.nth_weekdays):
            errs.append("monthly schedule requires month_days or nth_weekdays")
        if freq is not Frequency.MONTHLY and (self.month_days or self.nth_weekdays):
            errs.append("month_days/nth_weekdays are only valid for monthly frequency")

        start = date.fromisoformat(self.start_date)
        end = date.fromisoformat(self.end_date) if self.end_date else None
        if end is not None and end < start:
            errs.append("end_date must be on or after start_date")

        # One-offs outside [start,end] are a spec mistake (they would be
        # silently invisible); collisions between one-offs are caught here too.
        extras = {date.fromisoformat(s) for s in self.extra_dates}
        for d in extras:
            if d < start or (end and d > end):
                errs.append(f"extra_date {d.isoformat()} is outside [start_date, end_date]")
        overlap = extras & {date.fromisoformat(s) for s in self.exceptions}
        if overlap:
            errs.append(
                "a date cannot be both extra_date and exception: "
                + ",".join(sorted(d.isoformat() for d in overlap))
            )

        if errs:
            raise ValidationError(
                "schedule specification is invalid: " + "; ".join(errs),
                code="VAL-SPEC-RULES",
                details={"violations": errs},
            )

    # ── convenience accessors ──────────────────────────────────────────────

    @property
    def at_time(self) -> time:
        h, m = (int(x) for x in self.at.split(":"))
        return time(h, m)

    @property
    def start(self) -> date:
        return date.fromisoformat(self.start_date)

    @property
    def end(self) -> date | None:
        return date.fromisoformat(self.end_date) if self.end_date else None

    @property
    def exception_set(self) -> frozenset[date]:
        return frozenset(date.fromisoformat(s) for s in self.exceptions)

    @property
    def extra_set(self) -> frozenset[date]:
        return frozenset(date.fromisoformat(s) for s in self.extra_dates)


def parse_spec(raw: dict[str, Any]) -> ScheduleSpec:
    """Parse an untrusted payload into a :class:`ScheduleSpec`.

    Pydantic ``ValueError``/``ValidationError`` from field validators is
    normalised into our :class:`ValidationError` contract (``VAL-BAD-SPEC``)
    with the offending field recorded; cross-field errors already carry
    ``VAL-SPEC-RULES``.
    """
    try:
        return ScheduleSpec.model_validate(raw)
    except ValidationError:
        raise
    except Exception as exc:  # pydantic.ValidationError
        details: dict[str, Any] = {"violations": []}
        errors = getattr(exc, "errors", lambda: [])()
        for e in errors:
            loc = ".".join(str(p) for p in e.get("loc", []) if p != "")
            details["violations"].append(
                {"field": loc or "<root>", "reason": e.get("msg", "invalid")}
            )
        raise ValidationError(
            "schedule specification is invalid",
            code="VAL-BAD-SPEC",
            details=details,
        ) from exc
