"""独立参考实现 —— 不得 import 任何 app.planner 的生产解析/定位代码。

两条与生产完全独立的路径：
1. 序列生成：第三方 dateutil.rrule（生产代码用自研解析器）。
2. 本地墙钟→UTC：UTC 分钟扫描器 —— 枚举候选 UTC 分钟并 astimezone 反查墙钟，
   按“该墙钟出现 0/1/2 次”判定 gap/normal/fold（生产代码用 fold 双路 round-trip）。

仅用于测试期望值生成；pytest 用它与生产输出逐字段比对。
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime, time, timedelta, timezone
from zoneinfo import ZoneInfo

from dateutil.rrule import DAILY, MONTHLY, WEEKLY, YEARLY, rrule

UTC = timezone.utc

_FREQ_MAP = {"DAILY": DAILY, "WEEKLY": WEEKLY, "MONTHLY": MONTHLY, "YEARLY": YEARLY}


@dataclass(frozen=True)
class RefOccurrence:
    local_wall: datetime       # naive
    kind: str                  # FIRE | SKIP_GAP
    due_at: datetime | None    # UTC aware
    resolution: str            # NORMAL | FOLD | GAP | GAP_SHIFTED
    resolved_fold: int | None


# ---------------------------------------------------------------- 扫描器

def scan_resolve(wall: datetime, tz_name: str, *, gap_policy: str = "SKIP",
                 fold_policy: str = "EARLIEST", step_seconds: int = 60) -> RefOccurrence:
    """枚举 wall 当天（本地日）覆盖的 UTC 区间内每个候选时刻，反查墙钟匹配。"""
    tz = ZoneInfo(tz_name)
    day = wall.date()
    # 该本地日可能跨越 UTC-12..+14；用该日前后 2 天的 UTC 区间做扫描边界
    scan_start = datetime(day.year, day.month, day.day, tzinfo=UTC) - timedelta(hours=26)
    scan_end = scan_start + timedelta(hours=24 + 26 + 26)

    matches: list[datetime] = []
    t = scan_start
    while t <= scan_end:
        local = t.astimezone(tz)
        if local.replace(tzinfo=None).replace(microsecond=0) == wall.replace(microsecond=0):
            matches.append(t)
        t += timedelta(seconds=step_seconds)

    # 去重（同一 UTC 只算一次）
    uniq = sorted(set(matches))
    if len(uniq) == 0:
        if gap_policy == "SKIP":
            return RefOccurrence(wall, "SKIP_GAP", None, "GAP", None)
        # SHIFT_FORWARD：从 wall 起向后扫描第一个“合法且墙钟>=wall”的 UTC 候选
        # 直接扫描 wall 之后第一分钟，找到第一个映回的墙钟跳变点
        return _scan_shift_forward(wall, tz, step_seconds)
    if len(uniq) == 1:
        return RefOccurrence(wall, "FIRE", uniq[0], "NORMAL", 0)
    # fold：两个候选（间隔通常 1 小时）。EARLIEST=UTC 较早；LATEST=较晚
    chosen = uniq[0] if fold_policy == "EARLIEST" else uniq[-1]
    fold = 0 if chosen == uniq[0] else 1
    return RefOccurrence(wall, "FIRE", chosen, "FOLD", fold)


def _scan_shift_forward(wall: datetime, tz: ZoneInfo, step_seconds: int) -> RefOccurrence:
    day = wall.date()
    t = datetime(day.year, day.month, day.day, tzinfo=UTC) - timedelta(hours=26)
    end = t + timedelta(hours=72)
    prev_local: datetime | None = None
    while t <= end:
        local = t.astimezone(tz).replace(tzinfo=None)
        if prev_local is not None and local > prev_local + timedelta(seconds=step_seconds):
            # 发生跳变：gap 后第一个合法墙钟
            if prev_local < wall <= local:
                return RefOccurrence(local, "FIRE", t, "GAP_SHIFTED", 0)
        prev_local = local
        t += timedelta(seconds=step_seconds)
    raise AssertionError(f"shift-forward scan failed for {wall}")


# ---------------------------------------------------------------- 序列（dateutil）

def ref_daily(*, timezone_name: str, start: date, local_t: time, interval: int = 1,
              until_local: datetime | None = None, days: int = 366,
              byweekday=None, gap_policy="SKIP", fold_policy="EARLIEST") -> list[RefOccurrence]:
    tz = ZoneInfo(timezone_name)
    start_dt = datetime.combine(start, local_t)
    rr = rrule(DAILY, dtstart=start_dt, interval=interval, count=days,
               byweekday=byweekday)
    out: list[RefOccurrence] = []
    for wall in rr:
        if until_local is not None and wall > until_local:
            break
        out.append(scan_resolve(wall, timezone_name,
                                gap_policy=gap_policy, fold_policy=fold_policy))
    return out


def ref_yearly(*, timezone_name: str, start: datetime, interval: int = 4,
               count: int = 5, gap_policy="SKIP", fold_policy="EARLIEST") -> list[RefOccurrence]:
    rr = rrule(YEARLY, dtstart=start, interval=interval, count=count)
    return [scan_resolve(wall, timezone_name, gap_policy=gap_policy, fold_policy=fold_policy)
            for wall in rr]
