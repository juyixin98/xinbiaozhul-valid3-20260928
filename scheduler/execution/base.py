"""Execution adapters — the only layer that performs side effects.

The scheduler kernel hands a due fire to an :class:`Executor`; the executor
returns :class:`DeliveryResult` or raises :class:`DispatchError`. Adapters
know nothing about planning or database state, which keeps the exactly-once
guarantee in one place (the engine) and makes adapters trivially testable.

Contract:

* ``deliver`` must be **idempotent-safe**: the engine calls it at most once
  per claimed run, but retries of genuinely unknown outcomes are the caller's
  decision (HTTP POST bodies carry ``fire_key`` so targets can dedupe).
* Timeouts / connection errors / 5xx -> ``DispatchError`` (``RUN-*``).
  4xx is a permanent target rejection: still ``RUN-PERMANENT`` (terminal,
  not retried by catch-up).
* Any other exception is wrapped as ``RUN-FAILED`` — nothing escapes untyped.
"""
from __future__ import annotations

import abc
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any


@dataclass(frozen=True)
class DeliveryContext:
    fire_key: str
    schedule_id: str
    schedule_version: int
    schedule_name: str
    due_utc: datetime
    attempt: int  # 1 for first delivery, 2+ for manual retry
    deadline_seconds: float
    payload: dict[str, Any] = field(default_factory=dict)
    target_url: str | None = None

    def envelope(self, status: str, run_id: str, now_utc: datetime) -> dict[str, Any]:
        """JSON body sent to webhook targets."""
        return {
            "schema": "scheduler.fire.v1",
            "run_id": run_id,
            "fire_key": self.fire_key,
            "schedule_id": self.schedule_id,
            "schedule_version": self.schedule_version,
            "schedule_name": self.schedule_name,
            "status": status,
            "due_utc": self.due_utc.isoformat(),
            "dispatched_at_utc": now_utc.isoformat(),
            "attempt": self.attempt,
            "payload": self.payload,
        }


@dataclass(frozen=True)
class DeliveryResult:
    ok: bool
    detail: dict[str, Any] = field(default_factory=dict)


class Executor(abc.ABC):
    type_name: str = "base"

    @abc.abstractmethod
    def deliver(self, ctx: DeliveryContext, *, run_id: str, now_utc: datetime) -> DeliveryResult:
        """Perform the side effect. Raise DispatchError on runtime failure."""
        raise NotImplementedError
