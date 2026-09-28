"""测试夹具：
- TEST_DATABASE_URL 指向一次性 PostgreSQL（脚本默认在 5434 启动）
- 每个测试前 TRUNCATE 所有业务表，保证隔离
- 后台调度关闭；通过 Scheduler + FakeClock 手动驱动确定性 tick
"""
from __future__ import annotations

import os
import uuid
from datetime import datetime, timezone

import psycopg
import pytest
from fastapi.testclient import TestClient

from app.clock import FakeClock
from app.config import Settings
from app.db import apply_schema
from app.kernel.scheduler import Scheduler

TEST_DATABASE_URL = os.environ.get(
    "TEST_DATABASE_URL", "postgresql://sched:sched@127.0.0.1:5434/scheduler"
)

TABLES = [
    "executions", "triggers", "schedule_revisions", "audit_events", "runs",
    "scheduler_heartbeat", "schedules",
]


@pytest.fixture(scope="session")
def database_url() -> str:
    # 提前确认可达，并应用 schema
    with psycopg.connect(TEST_DATABASE_URL) as conn:
        apply_schema(conn)
    return TEST_DATABASE_URL


@pytest.fixture()
def conn(database_url):
    c = psycopg.connect(database_url, autocommit=False, row_factory=psycopg.rows.dict_row)
    yield c
    c.close()


@pytest.fixture()
def pool(database_url):
    from psycopg_pool import ConnectionPool

    p = ConnectionPool(
        database_url, min_size=1, max_size=8,
        kwargs={"autocommit": True, "row_factory": psycopg.rows.dict_row}, open=True)
    with p.connection() as c:
        c.execute("TRUNCATE " + ", ".join(TABLES) + " RESTART IDENTITY CASCADE")
        c.commit()
    yield p
    p.close()


@pytest.fixture()
def settings() -> Settings:
    return Settings(
        database_url=TEST_DATABASE_URL,
        scheduler_enabled=False,
        tick_interval_seconds=10.0,
        lateness_grace_seconds=30.0,
        catchup_deadline_seconds=3600.0,
        catchup_rate_limit=100,
        lease_timeout_seconds=300.0,
        max_attempts=3,
        downtime_threshold_seconds=30.0,
    )


@pytest.fixture()
def fake_clock() -> FakeClock:
    return FakeClock(datetime(2026, 1, 1, 0, 0, tzinfo=timezone.utc))


@pytest.fixture()
def scheduler(pool, settings, fake_clock):
    return Scheduler(pool, settings, clock=fake_clock)


@pytest.fixture()
def client(pool, settings, fake_clock):
    """不走 lifespan（无后台循环）；手动装配 app.state 并用同一 pool/clock。"""
    from fastapi import FastAPI

    from app.api import admin, schedules
    from app.errors import register_exception_handlers

    app = FastAPI()
    register_exception_handlers(app)
    app.state.pool = pool
    app.state.settings = settings
    app.state.clock = fake_clock
    app.state.scheduler = Scheduler(pool, settings, clock=fake_clock)
    app.include_router(schedules.router)
    app.include_router(admin.router)

    @app.get("/healthz")
    def healthz():
        with pool.connection() as c:
            c.execute("SELECT 1")
        return {"status": "ok"}

    with TestClient(app) as c:
        yield c


# ---------------------------------------------------------------- 辅助

def make_spec_dict(**overrides) -> dict:
    base = {
        "name": "test-schedule",
        "timezone": "America/New_York",
        "rrule": "FREQ=DAILY",
        "start_at": "2026-03-01T09:00:00",
        "exceptions": {},
        "gap_policy": "SKIP",
        "fallback_policy": "EARLIEST",
        "missing_date_policy": "SKIP",
        "action": {"type": "log"},
    }
    base.update(overrides)
    return base


def create_schedule(client, **overrides) -> str:
    resp = client.post("/api/v1/schedules", json=make_spec_dict(**overrides))
    assert resp.status_code == 201, resp.text
    return resp.json()["id"]


def tick(client, now: datetime | None = None, schedule_id: str | None = None) -> dict:
    body: dict = {}
    if now is not None:
        body["now"] = now.astimezone(timezone.utc).isoformat()
    if schedule_id:
        body["schedule_id"] = schedule_id
    resp = client.post("/api/v1/admin/ticks", json=body)
    assert resp.status_code == 200, resp.text
    return resp.json()
