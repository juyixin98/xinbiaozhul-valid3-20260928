"""工作日 / 例外日期覆盖（纯函数）。

例外键：
  "YYYY-MM-DD"                 → 匹配当天所有出现
  "YYYY-MM-DD HH:MM:SS"        → 精确墙钟
  "YYYY-MM-DD HH:MM:SS[earliest]" / "[latest]" → fold 歧义墙钟的折叠选择子
值：SKIP（取消该出现）/ FORCE（在本无出现处新增一次触发）。
"""
from __future__ import annotations

import re
from dataclasses import dataclass, field
from datetime import date, datetime

from app.errors import ExceptionParseError
from app.planner.localize import is_ambiguous_fold, is_gap

_DATE_RE = re.compile(r"^(\d{4})-(\d{2})-(\d{2})$")
_DT_RE = re.compile(r"^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})(\[(earliest|latest)\])?$")


@dataclass(frozen=True)
class ExceptionEntry:
    raw: str
    action: str                      # SKIP | FORCE
    day: date
    wall: datetime | None            # 精确键时为 naive datetime；日期键为 None
    fold_sel: int | None             # 0=earliest, 1=latest, None=不指定
    is_date_only: bool


@dataclass(frozen=True)
class PlannedWall:
    ordinal: int
    wall: datetime
    included: bool                   # 是否进入触发（False → SKIPPED/EXCEPTION 或 missing/gap 另行处理）
    exception: ExceptionEntry | None
    missing: bool
    requested_ymd: tuple[int, int, int] | None


@dataclass(frozen=True)
class ParsedExceptions:
    entries: tuple[ExceptionEntry, ...]
    forced: tuple[ExceptionEntry, ...]
    skips: tuple[ExceptionEntry, ...]


def parse_exceptions(raw: dict[str, str]) -> ParsedExceptions:
    out: list[ExceptionEntry] = []
    for key, action in raw.items():
        k = key.strip()
        m = _DATE_RE.match(k)
        if m:
            y, mo, d = (int(x) for x in m.groups())
            try:
                day = date(y, mo, d)
            except ValueError as exc:
                raise ExceptionParseError(f"invalid exception date: {k!r}") from exc
            out.append(ExceptionEntry(k, action, day, None, None, True))
            continue
        m = _DT_RE.match(k)
        if m:
            y, mo, dd, hh, mm, ss, _bracket, sel = m.groups()
            try:
                wall = datetime(int(y), int(mo), int(dd), int(hh), int(mm), int(ss))
            except ValueError as exc:
                raise ExceptionParseError(f"invalid exception datetime: {k!r}") from exc
            out.append(
                ExceptionEntry(k, action, wall.date(), wall,
                               0 if sel == "earliest" else 1 if sel == "latest" else None,
                               False)
            )
            continue
        raise ExceptionParseError(
            f"exception key must be YYYY-MM-DD or 'YYYY-MM-DD HH:MM:SS[[earliest|latest]]', got {k!r}",
            {"key": k},
        )
    return ParsedExceptions(
        entries=tuple(out),
        forced=tuple(e for e in out if e.action == "FORCE"),
        skips=tuple(e for e in out if e.action == "SKIP"),
    )


def validate_exceptions_against_tz(parsed: ParsedExceptions, tz_name: str) -> None:
    """FORCE 于 fold 歧义墙钟必须带折叠选择子；FORCE 于 gap 墙钟直接冲突。"""
    for e in parsed.forced:
        if e.wall is None:
            continue
        if is_gap(e.wall, tz_name):
            from app.errors import ExceptionConflict

            raise ExceptionConflict(
                f"FORCE exception {e.raw!r} targets a nonexistent local time (DST gap); "
                "there is nothing to force",
                {"key": e.raw},
            )
        if is_ambiguous_fold(e.wall, tz_name) and e.fold_sel is None:
            from app.errors import ExceptionAmbiguousFold

            raise ExceptionAmbiguousFold(
                f"FORCE exception {e.raw!r} is ambiguous (repeated local time at DST end); "
                "append [earliest] or [latest]",
                {"key": e.raw},
            )


def _skip_matches(entry: ExceptionEntry, wall: datetime) -> bool:
    if entry.is_date_only:
        return wall.date() == entry.day
    if entry.wall != wall:
        return False
    if entry.fold_sel is None:
        return True
    # SKIP 精确键带选择子时只跳该折叠瞬间（匹配在 service 层按 fold 标记处理）
    return True


def find_skip(parsed: ParsedExceptions, wall: datetime) -> ExceptionEntry | None:
    for e in parsed.skips:
        if _skip_matches(e, wall):
            return e
    return None


def fold_skip_sel(parsed: ParsedExceptions, wall: datetime) -> int | None:
    """fold 墙上若存在带选择子的 SKIP 精确键，返回应跳过的折叠（0/1）；否则 None。"""
    for e in parsed.skips:
        if not e.is_date_only and e.wall == wall and e.fold_sel is not None:
            return e.fold_sel
    return None
