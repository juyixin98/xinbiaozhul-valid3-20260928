"""集中配置（12-factor，环境变量前缀 SCHED_ 或直接读 .env）。"""
from __future__ import annotations

from functools import lru_cache

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    database_url: str = "postgresql://sched:sched@127.0.0.1:5433/scheduler"
    scheduler_enabled: bool = True
    tick_interval_seconds: float = 15.0

    horizon_days: int = 7
    lateness_grace_seconds: float = 30.0
    catchup_deadline_seconds: float = 3600.0
    catchup_rate_limit: int = 100
    catchup_global_budget: int = 1000
    lease_timeout_seconds: float = 300.0
    max_attempts: int = 3
    max_occurrences_per_window: int = 20_000

    http_timeout_seconds: float = 10.0
    http_max_response_bytes: int = 64 * 1024

    log_level: str = "INFO"
    worker_id: str = "worker-1"
    # 代码语义版本（触发身份算法 / 调度内核的版本标识，写入 runs/audit 便于解释）
    code_version: str = "1.0.0"

    # 补跑判定阈值：停机超过 3 个 tick 间隔即开启 recovery epoch
    downtime_threshold_seconds: float = 45.0


@lru_cache
def get_settings() -> Settings:
    return Settings()
