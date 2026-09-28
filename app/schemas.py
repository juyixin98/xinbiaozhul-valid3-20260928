"""HTTP / 领域的 pydantic 模型。"""
from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, Field, field_validator

from app.planner.localize import FoldPolicy, GapPolicy


class ActionSpec(BaseModel):
    type: Literal["log", "http"]
    # type=http 时必填；仅允许 http/https 且 host 非空（执行器再次校验）
    url: str | None = None


class ScheduleSpec(BaseModel):
    name: str = Field(min_length=1, max_length=200)
    timezone: str = Field(description="IANA 时区名，如 America/New_York")
    rrule: str = Field(min_length=1)
    # 本地墙钟锚点（naive）；DAILY+ 的时刻取自其 time 部分
    start_at: datetime
    exceptions: dict[str, Literal["SKIP", "FORCE"]] = Field(default_factory=dict)
    gap_policy: GapPolicy = GapPolicy.SKIP
    fallback_policy: FoldPolicy = FoldPolicy.EARLIEST
    missing_date_policy: Literal["SKIP"] = "SKIP"

    # 补跑参数（可选覆盖；None 时用全局默认）
    lateness_grace_seconds: float | None = Field(default=None, ge=0, le=86400)
    catchup_deadline_seconds: float | None = Field(default=None, ge=60, le=7 * 86400)
    catchup_rate_limit: int | None = Field(default=None, ge=1, le=10000)

    action: ActionSpec

    @field_validator("start_at")
    @classmethod
    def _naive_start(cls, v: datetime) -> datetime:
        if v.tzinfo is not None:
            raise ValueError("start_at must be naive local wall time (no timezone suffix)")
        return v

    @field_validator("exceptions")
    @classmethod
    def _limit_exceptions(cls, v: dict) -> dict:
        if len(v) > 1000:
            raise ValueError("too many exception entries (max 1000)")
        return v


class ScheduleCreateResponse(BaseModel):
    id: str
    version: int
    warnings: list[str] = []


class ScheduleUpdate(BaseModel):
    expected_version: int
    spec: ScheduleSpec


class TickRequest(BaseModel):
    now: datetime | None = None            # 可注入 tz-aware UTC，便于确定性测试
    schedule_id: str | None = None

    @field_validator("now")
    @classmethod
    def _aware_now(cls, v: datetime | None) -> datetime | None:
        if v is not None and v.tzinfo is None:
            raise ValueError("now must be timezone-aware (UTC preferred)")
        return v
