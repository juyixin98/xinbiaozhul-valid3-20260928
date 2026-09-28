"""触发身份：(schedule_id, kind, 规范字符串) 的纯函数 uuid5。

真实出现： t:<UTC YYYYMMDDTHHMMSSffffffZ>
gap：      g:<本地墙钟 YYYYMMDDTHHMMSSffffff>
missing：  m:<被请求的本地 YYYYMMDDTHHMMSSffffff>

身份与版本/序号/策略/spec 哈希无关 —— 这是“改计划不重触发历史”的根基。
真实名以 't:' 开头（后接数字 T），合成名以字母前缀开头，命名空间不相交。
"""
from __future__ import annotations

import uuid
from datetime import datetime, timezone

NAMESPACE_SCHEDULE_PLACEHOLDER = uuid.UUID("00000000-0000-0000-0000-000000000000")


def _compact_utc(dt: datetime) -> str:
    dt = dt.astimezone(timezone.utc)
    return dt.strftime("%Y%m%dT%H%M%S") + f"{dt.microsecond:06d}" + "Z"


def _compact_wall(dt: datetime) -> str:
    if dt.tzinfo is not None:
        dt = dt.replace(tzinfo=None)
    return dt.strftime("%Y%m%dT%H%M%S") + f"{dt.microsecond:06d}"


def _compact_requested(y: int, m: int, d: int, hour: int = 0, minute: int = 0, second: int = 0) -> str:
    # 日期可能非法（如平年 2/29），不能经 datetime 构造
    return f"{y:04d}{m:02d}{d:02d}T{hour:02d}{minute:02d}{second:02d}000000"


def real_name(due_at: datetime) -> str:
    return "t:" + _compact_utc(due_at)


def gap_name(local_wall: datetime) -> str:
    return "g:" + _compact_wall(local_wall)


def missing_name(y: int, m: int, d: int, hour: int = 0, minute: int = 0, second: int = 0) -> str:
    return "m:" + _compact_requested(y, m, d, hour, minute, second)


def force_name(local_wall: datetime, fold_sel: int | None) -> str:
    # FORCE 新增的真实触发（按解析后 UTC 建主身份外，另存合成键防止编辑对账时漂移）
    suffix = "" if fold_sel is None else ("e" if fold_sel == 0 else "l")
    return "f:" + _compact_wall(local_wall) + suffix


def trigger_id(schedule_id: uuid.UUID | str, name: str) -> uuid.UUID:
    return uuid.uuid5(uuid.UUID(str(schedule_id)), name)


def real_trigger_id(schedule_id: uuid.UUID | str, due_at: datetime) -> uuid.UUID:
    return trigger_id(schedule_id, real_name(due_at))


def synthetic_trigger_id(schedule_id: uuid.UUID | str, kind: str, local_wall: datetime,
                         fold_sel: int | None = None) -> uuid.UUID:
    if kind == "g":
        return trigger_id(schedule_id, gap_name(local_wall))
    if kind == "m":
        return trigger_id(schedule_id, missing_name(local_wall))
    if kind == "f":
        return trigger_id(schedule_id, force_name(local_wall, fold_sel))
    raise ValueError(f"unknown synthetic kind {kind!r}")
