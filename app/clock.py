"""时钟抽象：生产用 UTC 系统时钟；测试用可推进/回拨的 FakeClock。"""
from __future__ import annotations

from datetime import datetime, timezone
from typing import Protocol


class Clock(Protocol):
    def now(self) -> datetime: ...


class SystemClock:
    def now(self) -> datetime:
        return datetime.now(timezone.utc)


class FakeClock:
    """显式受控时钟，支持推进与回拨（回拨是时钟回拨测试的关键）。"""

    def __init__(self, start: datetime):
        if start.tzinfo is None:
            raise ValueError("FakeClock start must be timezone-aware UTC")
        self._now = start.astimezone(timezone.utc)

    def now(self) -> datetime:
        return self._now

    def advance(self, seconds: float) -> datetime:
        from datetime import timedelta

        self._now = self._now + timedelta(seconds=seconds)
        return self._now

    def set(self, value: datetime) -> datetime:
        self._now = value.astimezone(timezone.utc)
        return self._now

    def rewind(self, seconds: float) -> datetime:
        return self.advance(-seconds)
