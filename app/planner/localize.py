"""本地墙钟 → UTC 解析（纯函数，仅 stdlib zoneinfo）。

判定法（PEP 495 fold 双路 round-trip）：
  u0 = wall(fold=0) -> UTC ; u1 = wall(fold=1) -> UTC
  rt0 = u0 -> 本地墙钟
  - rt0 != wall           → GAP（不存在时间）
  - rt0 == wall 且 u0!=u1 → FOLD（秋季重复时间）
  - 否则                  → NORMAL
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from enum import StrEnum

from app.planner.tzutil import get_zone

UTC = timezone.utc


class GapPolicy(StrEnum):
    SKIP = "SKIP"
    SHIFT_FORWARD = "SHIFT_FORWARD"


class FoldPolicy(StrEnum):
    EARLIEST = "EARLIEST"
    LATEST = "LATEST"


@dataclass(frozen=True)
class Resolution:
    kind: str                      # NORMAL | FOLD | GAP | GAP_SHIFTED
    due_at: datetime | None        # tz-aware UTC；GAP+SKIP 时为 None
    resolved_wall: datetime | None  # due_at 映回时区后的真实本地墙钟（naive）
    resolved_fold: int | None
    utc_offset_minutes: int | None  # 解释用：生效 UTC 偏移（分钟）


def _naive(dt: datetime) -> datetime:
    return dt.replace(tzinfo=None)


def resolve_wall(
    wall: datetime, tz_name: str, gap_policy: GapPolicy, fold_policy: FoldPolicy
) -> Resolution:
    if wall.tzinfo is not None:
        raise ValueError("wall must be a naive local datetime")
    tz = get_zone(tz_name)

    d0 = wall.replace(tzinfo=tz, fold=0)
    d1 = wall.replace(tzinfo=tz, fold=1)
    u0 = d0.astimezone(UTC)
    u1 = d1.astimezone(UTC)
    rt0 = _naive(u0.astimezone(tz))

    if rt0 != wall:
        # ---- GAP：fold=0 落在跳时后、fold=1 落在跳时前 ----
        if gap_policy == GapPolicy.SKIP:
            return Resolution("GAP", None, None, None, None)
        # SHIFT_FORWARD：取跳时后第一个合法瞬间 = fold=0 的 UTC。
        # 真实墙钟 = wall 向后顺延 gap 增量；禁止使用跳时前的 fold=1。
        shifted_local = u0.astimezone(tz)
        shifted_wall = _naive(shifted_local)
        gap_delta = shifted_wall - wall
        if gap_delta <= timedelta(0):  # 防御：未定义情况不得包装成功
            raise RuntimeError(f"gap shift non-positive for {wall} in {tz_name}")
        return Resolution(
            "GAP_SHIFTED", u0, shifted_wall, 0,
            int(shifted_local.utcoffset().total_seconds() // 60),
        )

    if u0 != u1:
        # ---- FOLD：同一墙钟两个 UTC 瞬间，按策略只取一次 ----
        if fold_policy == FoldPolicy.EARLIEST:
            chosen, fold = u0, 0
        else:
            chosen, fold = u1, 1
        chosen_local = chosen.astimezone(tz)
        return Resolution(
            "FOLD", chosen, _naive(chosen_local), fold,
            int(chosen_local.utcoffset().total_seconds() // 60),
        )

    # ---- NORMAL ----
    local0 = u0.astimezone(tz)
    return Resolution(
        "NORMAL", u0, wall, 0, int(local0.utcoffset().total_seconds() // 60)
    )


def assert_roundtrip(res: Resolution, tz_name: str) -> None:
    """不变量：任何返回的 due_at 映回时区必须等于 resolved_wall。"""
    if res.due_at is None:
        return
    tz = get_zone(tz_name)
    back = _naive(res.due_at.astimezone(tz).replace(fold=res.resolved_fold or 0))
    if back != res.resolved_wall:
        raise AssertionError(
            f"round-trip mismatch in {tz_name}: {res.due_at} -> {back} != {res.resolved_wall}"
        )


def is_ambiguous_fold(wall: datetime, tz_name: str) -> bool:
    """该墙钟是否为 fold 歧义时刻（例外日期 FORCE 校验用）。"""
    tz = get_zone(tz_name)
    u0 = wall.replace(tzinfo=tz, fold=0).astimezone(UTC)
    u1 = wall.replace(tzinfo=tz, fold=1).astimezone(UTC)
    rt0 = _naive(u0.astimezone(tz))
    return rt0 == wall and u0 != u1


def is_gap(wall: datetime, tz_name: str) -> bool:
    tz = get_zone(tz_name)
    u0 = wall.replace(tzinfo=tz, fold=0).astimezone(UTC)
    return _naive(u0.astimezone(tz)) != wall
