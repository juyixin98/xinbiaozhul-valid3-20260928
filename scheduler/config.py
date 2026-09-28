"""Application configuration loaded from environment / .env file.

Single source of truth for tunable limits. Nothing else in the codebase reads
environment variables directly; modules receive a :class:`Settings` value (or
plain arguments) so they stay deterministic and testable.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    database_url: str = "postgresql://scheduler:scheduler@localhost:5433/scheduler"
    host: str = "127.0.0.1"
    port: int = 8080

    # ── Engine policy ──────────────────────────────────────────────────────
    # Maximum number of MISSED occurrences (across all schedules) materialised
    # in a single tick. Deterministic catch-up cap: older occurrences past the
    # horizon are marked EXPIRED instead of run.
    max_backfill: int = 100
    # Due window: occurrences with due_at <= now + this are made DUE.
    tick_horizon_seconds: int = 60
    # Planner precomputes base occurrences this many days ahead of "now".
    plan_ahead_days: int = 31
    # An occurrence older than now - this window is never catch-up executed.
    backfill_window_days: int = 7
    # Per-target timeout for the webhook executor (seconds).
    dispatch_timeout_seconds: float = 5.0
    # In-process background ticker period; <= 0 disables it.
    ticker_interval_seconds: float = 1.0
    enable_background_ticker: bool = True

    @staticmethod
    def from_env() -> "Settings":
        """Load settings from SCHED_* environment variables (and .env).

        Kept dependency-free on purpose: the project pins pydantic-settings,
        but a plain loader makes the contract explicit and keeps startup
        legible. Types are validated; bad values raise ``ValueError``.
        """
        import os

        def s(name: str, default: str) -> str:
            return os.environ.get(f"SCHED_{name}", default)

        def i(name: str, default: int) -> int:
            raw = s(name, str(default))
            try:
                return int(raw)
            except ValueError as exc:
                raise ValueError(f"SCHED_{name} must be an integer, got {raw!r}") from exc

        def f(name: str, default: float) -> float:
            raw = s(name, str(default))
            try:
                return float(raw)
            except ValueError as exc:
                raise ValueError(f"SCHED_{name} must be a number, got {raw!r}") from exc

        return Settings(
            database_url=s("DATABASE_URL", Settings.database_url),
            host=s("HOST", Settings.host),
            port=i("PORT", Settings.port),
            max_backfill=i("MAX_BACKFILL", Settings.max_backfill),
            tick_horizon_seconds=i("TICK_HORIZON_SECONDS", Settings.tick_horizon_seconds),
            plan_ahead_days=i("PLAN_AHEAD_DAYS", Settings.plan_ahead_days),
            backfill_window_days=i("BACKFILL_WINDOW_DAYS", Settings.backfill_window_days),
            dispatch_timeout_seconds=f(
                "DISPATCH_TIMEOUT_SECONDS", Settings.dispatch_timeout_seconds
            ),
            ticker_interval_seconds=f(
                "TICKER_INTERVAL_SECONDS", Settings.ticker_interval_seconds
            ),
            enable_background_ticker=s("ENABLE_BACKGROUND_TICKER", "1") not in ("0", "false", "False", ""),
        )
