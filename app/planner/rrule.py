"""RRULE 解析与展开（纯函数，无第三方依赖）。

支持面：FREQ=SECONDLY/MINUTELY/HOURLY/DAILY/WEEKLY/MONTHLY/YEARLY、
INTERVAL、BYDAY(MO..SU，MONTHLY/YEARLY 允许 nth 如 2MO/-1FR)、BYMONTHDAY(含负数)、
BYMONTH、WKST、COUNT、UNTIL(本地日期或日期时间，含端点)。
拒绝面（不静默忽略）：DTSTART(请用 start_at)、BYSETPOS、BYWEEKNO、BYYEARDAY、
BYHOUR/MINUTE/SECOND、BYEASTER 及任何未知 token。

COUNT 语义：计数“理论出现”，包含 gap 与缺失日期（与 RFC/dateutil 的有意偏差，README 说明）。
"""
from __future__ import annotations

import calendar
import re
from dataclasses import dataclass
from datetime import date, datetime, time, timedelta
from typing import Iterator

from app.errors import PolicyConflict, RRuleParseError, UnsupportedRRule

WEEKDAYS = {"MO": 0, "TU": 1, "WE": 2, "TH": 3, "FR": 4, "SA": 5, "SU": 6}
FREQS = {"SECONDLY", "MINUTELY", "HOURLY", "DAILY", "WEEKLY", "MONTHLY", "YEARLY"}
SUPPORTED_TOKENS = {
    "FREQ", "INTERVAL", "BYDAY", "BYMONTHDAY", "BYMONTH", "WKST", "COUNT", "UNTIL"
}

# 硬性枚举上限：即使规格畸形（如超宽窗口）也不会无限迭代
HARD_ITER_CAP = 2_000_000


@dataclass(frozen=True)
class ByDayEntry:
    weekday: int           # 0=Mon
    ordinal: int | None    # nth，如 2 / -1；None=该月全部（仅 MONTHLY/YEARLY）


@dataclass(frozen=True)
class RRuleSpec:
    freq: str
    interval: int
    byday: tuple[ByDayEntry, ...]
    bymonthday: tuple[int, ...]
    bymonth: tuple[int, ...]
    wkst: int
    count: int | None
    until: datetime | None
    raw: str


@dataclass(frozen=True)
class IndexedWall:
    ordinal: int          # 从锚点起的 1 基理论序号（含 missing 标记）
    wall: datetime        # naive 本地墙钟；missing 时为“该月最后一个合法日”的钳制值（用于存储）
    missing: bool = False
    requested_ymd: tuple[int, int, int] | None = None  # missing 时被请求但不存在的 (年,月,日)


# ---------------------------------------------------------------- 解析

_BYDAY_RE = re.compile(r"^([+-]?\d+)?(MO|TU|WE|TH|FR|SA|SU)$")
_UNTIL_DT_RE = re.compile(r"^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})$")
_UNTIL_D_RE = re.compile(r"^(\d{4})(\d{2})(\d{2})$")


def _split_values(v: str) -> list[str]:
    return [p.strip() for p in v.split(",") if p.strip()]


def _parse_int(name: str, v: str) -> int:
    try:
        return int(v)
    except ValueError as exc:
        raise RRuleParseError(f"{name} must be an integer, got {v!r}") from exc


def _parse_byday(v: str) -> tuple[ByDayEntry, ...]:
    out: list[ByDayEntry] = []
    for part in _split_values(v):
        m = _BYDAY_RE.match(part)
        if not m:
            raise RRuleParseError(f"invalid BYDAY entry: {part!r}")
        ord_raw, wd = m.groups()
        ordinal = int(ord_raw) if ord_raw not in (None, "", "+") else None
        out.append(ByDayEntry(WEEKDAYS[wd], ordinal))
    return tuple(out)


def _parse_until(v: str) -> datetime:
    m = _UNTIL_DT_RE.match(v)
    if m:
        y, mo, d, hh, mm, ss = (int(x) for x in m.groups())
        try:
            return datetime(y, mo, d, hh, mm, ss)
        except ValueError as exc:
            raise RRuleParseError(f"invalid UNTIL: {v!r}") from exc
    m = _UNTIL_D_RE.match(v)
    if m:
        y, mo, d = (int(x) for x in m.groups())
        try:
            return datetime.combine(date(y, mo, d), time(23, 59, 59))
        except ValueError as exc:
            raise RRuleParseError(f"invalid UNTIL: {v!r}") from exc
    raise RRuleParseError(
        "UNTIL must be YYYYMMDD or YYYYMMDDTHHMMSS (local wall time), got " + repr(v)
    )


def parse_rrule(raw: str) -> RRuleSpec:
    if not raw or not raw.strip():
        raise RRuleParseError("rrule string is empty")
    body = raw.strip()
    if body.upper().startswith("RRULE:"):
        body = body[6:]

    tokens: dict[str, str] = {}
    for part in body.split(";"):
        if not part.strip():
            continue
        if "=" not in part:
            raise RRuleParseError(f"malformed rrule token: {part!r}")
        key, val = part.split("=", 1)
        key, val = key.strip().upper(), val.strip()
        if key == "DTSTART":
            raise PolicyConflict(
                "DTSTART is not allowed inside rrule; use the start_at field",
                {"token": "DTSTART"},
            )
        if key not in SUPPORTED_TOKENS:
            raise UnsupportedRRule(f"unsupported rrule token: {key}", {"token": key})
        if key in tokens:
            raise RRuleParseError(f"duplicate rrule token: {key}")
        tokens[key] = val

    if "FREQ" not in tokens:
        raise RRuleParseError("rrule must contain FREQ")
    freq = tokens["FREQ"].upper()
    if freq not in FREQS:
        raise RRuleParseError(f"invalid FREQ: {freq}")

    interval = _parse_int("INTERVAL", tokens.get("INTERVAL", "1"))
    if interval < 1:
        raise RRuleParseError("INTERVAL must be >= 1")

    count = None
    until = None
    if "COUNT" in tokens:
        count = _parse_int("COUNT", tokens["COUNT"])
        if count < 1:
            raise RRuleParseError("COUNT must be >= 1")
    if "UNTIL" in tokens:
        until = _parse_until(tokens["UNTIL"])
    if count is not None and until is not None:
        raise PolicyConflict("COUNT and UNTIL are mutually exclusive")

    byday = _parse_byday(tokens["BYDAY"]) if "BYDAY" in tokens else ()
    bymonthday = tuple(sorted(_parse_int("BYMONTHDAY", x) for x in _split_values(tokens.get("BYMONTHDAY", ""))))
    bymonth = tuple(sorted(_parse_int("BYMONTH", x) for x in _split_values(tokens.get("BYMONTH", ""))))
    wkst = WEEKDAYS[tokens["WKST"].upper()] if "WKST" in tokens else WEEKDAYS["MO"]

    for d in bymonthday:
        if not -31 <= d <= 31 or d == 0:
            raise RRuleParseError(f"BYMONTHDAY entries must be in -31..-1 or 1..31, got {d}")
    for m in bymonth:
        if not 1 <= m <= 12:
            raise RRuleParseError(f"BYMONTH entries must be 1..12, got {m}")

    spec = RRuleSpec(
        freq=freq, interval=interval, byday=byday, bymonthday=bymonthday,
        bymonth=bymonth, wkst=wkst, count=count, until=until, raw=raw,
    )
    validate(spec)
    return spec


def validate(spec: RRuleSpec, local_time_present: bool | None = None) -> None:
    """跨字段校验。local_time_present 由 service 层告知（DAILY+ 必须，亚日频禁止）。"""
    subdaily = spec.freq in ("SECONDLY", "MINUTELY", "HOURLY")
    if local_time_present is True and subdaily:
        raise PolicyConflict(
            f"local_time must not be set with FREQ={spec.freq}; time-of-day comes from start_at",
        )
    if local_time_present is False and not subdaily:
        raise PolicyConflict(f"local_time is required for FREQ={spec.freq}")

    if subdaily:
        if spec.byday or spec.bymonthday or spec.bymonth:
            raise PolicyConflict(
                f"BYDAY/BYMONTHDAY/BYMONTH are not allowed with FREQ={spec.freq}"
            )

    if spec.bymonthday and spec.freq not in ("MONTHLY", "YEARLY"):
        raise PolicyConflict("BYMONTHDAY is only allowed with FREQ=MONTHLY or YEARLY")

    if spec.freq in ("DAILY", "WEEKLY"):
        bad = [e for e in spec.byday if e.ordinal is not None]
        if bad:
            raise PolicyConflict(f"nth BYDAY (e.g. 2MO) is only allowed monthly/yearly, not {spec.freq}")

    if spec.freq == "YEARLY" and not spec.bymonth and (spec.byday or spec.bymonthday):
        # dateutil 要求显式 BYMONTH 才能在年规则里按星期/日展开；我们同样要求，避免歧义
        raise PolicyConflict("YEARLY rules with BYDAY/BYMONTHDAY require explicit BYMONTH")


# ---------------------------------------------------------------- 展开辅助

def _month_days(year: int, month: int) -> int:
    return calendar.monthrange(year, month)[1]


def _resolve_day(year: int, month: int, dom: int) -> int | None:
    last = _month_days(year, month)
    d = dom if dom > 0 else last + 1 + dom
    return d if 1 <= d <= last else None


def _weekday_days(year: int, month: int, weekday: int) -> list[int]:
    last = _month_days(year, month)
    return [d for d in range(1, last + 1) if date(year, month, d).weekday() == weekday]


def _nth_weekday_day(year: int, month: int, weekday: int, n: int) -> int | None:
    days = _weekday_days(year, month, weekday)
    if n > 0:
        return days[n - 1] if n <= len(days) else None
    return days[n] if -n <= len(days) else None


def _emit_datetime(y: int, m: int, d: int, tm: time) -> datetime:
    return datetime(y, m, d, tm.hour, tm.minute, tm.second, tm.microsecond)


def _requested_wall(y: int, m: int, d: int, tm: time) -> datetime:
    """缺失日期时仍构造“被请求的墙钟”（day 可能非法，仅用于标记/审计，不参与计算）。"""
    return datetime(y, m, d, tm.hour, tm.minute, tm.second, tm.microsecond)


# ---------------------------------------------------------------- 各频率

def _iter_subdaily(spec: RRuleSpec, anchor: datetime) -> Iterator[tuple[datetime, bool, tuple | None]]:
    unit = {"SECONDLY": 1, "MINUTELY": 60, "HOURLY": 3600}[spec.freq]
    step = timedelta(seconds=unit * spec.interval)
    cur = anchor
    n = 0
    while n < HARD_ITER_CAP:
        yield cur, False, None
        cur = cur + step
        n += 1


def _day_matches_filters(spec: RRuleSpec, d: date) -> bool:
    if spec.bymonth and d.month not in spec.bymonth:
        return False
    if spec.byday and all(e.weekday != d.weekday() for e in spec.byday):
        return False
    return True


def _iter_daily(spec: RRuleSpec, anchor: datetime) -> Iterator[tuple[datetime, bool, tuple | None]]:
    tm, start_d = anchor.timetz(), anchor.date()
    step = timedelta(days=spec.interval)
    cur_d, n = start_d, 0
    while n < HARD_ITER_CAP:
        if _day_matches_filters(spec, cur_d):
            yield _emit_datetime(cur_d.year, cur_d.month, cur_d.day, tm), False, None
        cur_d = cur_d + step
        n += 1


def _iter_weekly(spec: RRuleSpec, anchor: datetime) -> Iterator[tuple[datetime, bool, tuple | None]]:
    tm, start_d = anchor.timetz(), anchor.date()
    week_start = start_d - timedelta(days=(start_d.weekday() - spec.wkst) % 7)
    weekdays = sorted({e.weekday for e in spec.byday}) or [start_d.weekday()]
    k, n = 0, 0
    while n < HARD_ITER_CAP:
        base = week_start + timedelta(weeks=spec.interval * k)
        for wd in weekdays:
            cand_d = base + timedelta(days=(wd - spec.wkst) % 7)
            if cand_d < start_d:
                continue
            if spec.bymonth and cand_d.month not in spec.bymonth:
                continue
            yield _emit_datetime(cand_d.year, cand_d.month, cand_d.day, tm), False, None
        k += 1
        n += 1


def _months_to_visit(spec: RRuleSpec, start_year: int, start_month: int) -> Iterator[tuple[int, int]]:
    """生成 (year, month) 候选（已 >= 锚点月），应用 INTERVAL 与 BYMONTH。"""
    if spec.freq == "MONTHLY":
        if spec.bymonth:
            # 文档化语义：INTERVAL 作用在“年”，每年访问 BYMONTH 列出的月份
            year = start_year
            while True:
                if (year - start_year) % spec.interval == 0:
                    for m in spec.bymonth:
                        if (year, m) >= (start_year, start_month):
                            yield year, m
                year += 1
        else:
            idx = 0
            while True:
                total = (start_year * 12 + (start_month - 1)) + idx * spec.interval
                yield total // 12, total % 12 + 1
                idx += 1
    else:  # YEARLY
        year = start_year
        while True:
            months = spec.bymonth or (start_month,)
            for m in months:
                yield year, m
            year += spec.interval


def _month_candidates(spec: RRuleSpec, y: int, m: int) -> list[int]:
    """合法日列表。BYMONTHDAY 非法日按 RFC 跳过；BYDAY nth 不存在时跳过。"""
    out: list[int] = []
    if spec.bymonthday:
        for dom in spec.bymonthday:
            d = _resolve_day(y, m, dom)
            if d is not None:
                out.append(d)
        return out
    if spec.byday:
        for e in spec.byday:
            if e.ordinal is not None:
                d = _nth_weekday_day(y, m, e.weekday, e.ordinal)
                if d is not None:
                    out.append(d)
            else:
                out.extend(_weekday_days(y, m, e.weekday))
    return out


def _clamped_wall(y: int, m: int, requested_dom: int, tm: time) -> tuple[datetime, tuple[int, int, int]]:
    """缺失日期（如平年 2/29）：墙钟钳到该月最后一个合法日，另存被请求的 (年,月,日)。"""
    last = _month_days(y, m)
    wall = _requested_wall(y, m, last, tm)
    return wall, (y, m, requested_dom)


def _iter_monthly(spec: RRuleSpec, anchor: datetime) -> Iterator[tuple[datetime, bool, tuple | None]]:
    tm = anchor.timetz()
    anchor_dom = anchor.day
    for y, m in _months_to_visit(spec, anchor.year, anchor.month):
        if spec.bymonthday or spec.byday:
            cands = sorted(_month_candidates(spec, y, m))
            for day in cands:
                wall = _emit_datetime(y, m, day, tm)
                if wall < anchor:
                    continue
                yield wall, False, None
        else:
            d = _resolve_day(y, m, anchor_dom)
            if d is None:
                wall, ymd = _clamped_wall(y, m, anchor_dom, tm)
                if wall < anchor:
                    continue
                yield wall, True, ymd
            else:
                wall = _emit_datetime(y, m, d, tm)
                if wall < anchor:
                    continue
                yield wall, False, None


def _iter_yearly(spec: RRuleSpec, anchor: datetime) -> Iterator[tuple[datetime, bool, tuple | None]]:
    tm = anchor.timetz()
    anchor_dom = anchor.day
    for y, m in _months_to_visit(spec, anchor.year, anchor.month):
        if spec.bymonthday or spec.byday:
            for day in sorted(_month_candidates(spec, y, m)):
                wall = _emit_datetime(y, m, day, tm)
                if wall < anchor:
                    continue
                yield wall, False, None
        else:
            d = _resolve_day(y, m, anchor_dom)
            if d is None:
                wall, ymd = _clamped_wall(y, m, anchor_dom, tm)
                if wall < anchor:
                    continue
                yield wall, True, ymd
            else:
                wall = _emit_datetime(y, m, d, tm)
                if wall < anchor:
                    continue
                yield wall, False, None


def iter_walls(
    spec: RRuleSpec,
    anchor: datetime,
    window_start: datetime,
    window_end: datetime,
) -> Iterator[IndexedWall]:
    """在 [window_start, window_end) 内产出理论墙钟。

    ordinal 从锚点起连续计数（与窗口无关），缺失日期也占序号。
    COUNT 含 missing；UNTIL 为本地墙钟含端点。
    """
    if anchor.tzinfo is not None:
        raise ValueError("anchor must be naive local datetime")

    if spec.freq in ("SECONDLY", "MINUTELY", "HOURLY"):
        gen = _iter_subdaily(spec, anchor)
    elif spec.freq == "DAILY":
        gen = _iter_daily(spec, anchor)
    elif spec.freq == "WEEKLY":
        gen = _iter_weekly(spec, anchor)
    elif spec.freq == "MONTHLY":
        gen = _iter_monthly(spec, anchor)
    else:
        gen = _iter_yearly(spec, anchor)

    ordinal = 0
    emitted = 0
    for wall, missing, requested_ymd in gen:
        ordinal += 1
        if spec.until is not None and wall > spec.until:
            return
        if wall < window_start:
            if spec.count is not None:
                emitted += 1  # COUNT 计全序列（含窗口外），故窗口外出现也消耗计数
            continue
        if wall >= window_end:
            return
        if spec.count is not None and emitted >= spec.count:
            return
        yield IndexedWall(ordinal=ordinal, wall=wall, missing=missing, requested_ymd=requested_ymd)
        emitted += 1
