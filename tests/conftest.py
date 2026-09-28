"""Pytest fixtures.

A dedicated *test database* is created once per session (never the dev DB),
schema is bootstrapped, and every test gets clean tables plus a fresh engine
with the log executor. The HTTP client accepts ``now=`` per call, translated to
``X-Test-Now`` so clock-dependent scenarios are instant and deterministic.
"""
from __future__ import annotations

import os
import threading
from datetime import datetime
from http.server import BaseHTTPRequestHandler, HTTPServer
import httpx
import psycopg
import pytest
from starlette.testclient import TestClient

from scheduler.config import Settings
from scheduler.execution.base import DeliveryContext, DeliveryResult, Executor
from scheduler.main import build

ADMIN_URL = os.environ.get(
    "SCHED_TEST_ADMIN_URL",
    "postgresql://scheduler:scheduler@localhost:5433/postgres",
)
TEST_DB = os.environ.get("SCHED_TEST_DB", "scheduler_test")
TEST_URL = os.environ.get(
    "SCHED_TEST_DATABASE_URL",
    f"postgresql://scheduler:scheduler@localhost:5433/{TEST_DB}",
)


def _test_settings() -> Settings:
    return Settings(
        database_url=TEST_URL,
        max_backfill=5,
        tick_horizon_seconds=60,
        plan_ahead_days=400,  # tests inspect full-year references
        backfill_window_days=7,
        dispatch_timeout_seconds=2.0,
        enable_background_ticker=False,
    )


@pytest.fixture(scope="session")
def _database() -> str:
    # Create the test database once for the whole session. Isolation between
    # tests is provided by TRUNCATE in repo_engine, so we avoid DROPping while
    # background pool connections may still be closing.
    with psycopg.connect(ADMIN_URL, autocommit=True) as conn:
        exists = conn.execute(
            "SELECT 1 FROM pg_database WHERE datname = %s", (TEST_DB,)
        ).fetchone()
        if not exists:
            conn.execute(f"CREATE DATABASE {TEST_DB}")
    return TEST_URL


@pytest.fixture()
def repo_engine(_database):
    app, repo, engine = build(_test_settings())
    # reset business data between tests (FK order: runs/audit/fires -> schedules)
    with repo.tx() as conn:
        conn.execute("TRUNCATE runs, audit_events, fires, schedules RESTART IDENTITY;")
        conn.execute(
            "UPDATE runtime_state SET last_tick_utc = NULL, "
            "last_tick_result = NULL, updated_at_utc = now() WHERE singleton = 1;"
        )
    yield app, repo, engine
    repo.close()


@pytest.fixture()
def engine(repo_engine):
    return repo_engine[2]


@pytest.fixture()
def repo(repo_engine):
    return repo_engine[1]


@pytest.fixture()
def client(repo_engine):
    app, _repo, _engine = repo_engine
    with TestClient(app, base_url="http://test") as c:
        yield c


class TimedClient:
    """Tiny wrapper pinning X-Test-Now on every request."""

    def __init__(self, raw: httpx.Client):
        self.raw = raw

    def request(self, method: str, url: str, *, now: datetime | str | None = None,
                **kw) -> httpx.Response:
        headers = kw.pop("headers", {}) or {}
        if now is not None:
            headers["X-Test-Now"] = now.isoformat() if isinstance(now, datetime) else now
        return self.raw.request(method, url, headers=headers, **kw)

    def get(self, url, **kw):
        return self.request("GET", url, **kw)

    def post(self, url, **kw):
        return self.request("POST", url, **kw)

    def put(self, url, **kw):
        return self.request("PUT", url, **kw)

    def delete(self, url, **kw):
        return self.request("DELETE", url, **kw)


@pytest.fixture()
def tclient(client):
    return TimedClient(client)


# ── executors for fault injection ───────────────────────────────────────────


class FlakyExecutor(Executor):
    """Fails the first n calls, then succeeds (webhook outage simulation)."""

    type_name = "webhook"

    def __init__(self, fail_times: int = 1, code: str = "RUN-FAILED"):
        self.fail_times = fail_times
        self.calls = 0
        self.code = code
        self.delivered: list[dict] = []

    def deliver(self, ctx: DeliveryContext, *, run_id, now_utc) -> DeliveryResult:
        from scheduler.errors import DispatchError

        self.calls += 1
        if self.calls <= self.fail_times:
            raise DispatchError(f"simulated failure #{self.calls}", code=self.code)
        self.delivered.append(ctx.envelope("FIRED", run_id, now_utc))
        return DeliveryResult(ok=True, detail={"simulated": True})


class ScriptedExecutor(Executor):
    """Behaviour keyed by fire_key; used for mixed outcome tests."""

    type_name = "log"

    def __init__(self, fail_keys: set[str] | None = None,
                 exception: type[Exception] | None = None):
        self.fail_keys = fail_keys or set()
        self.exception = exception
        self.delivered: list[str] = []

    def deliver(self, ctx, *, run_id, now_utc) -> DeliveryResult:
        from scheduler.errors import DispatchError

        if ctx.fire_key in self.fail_keys:
            if self.exception:
                raise self.exception("boom")
            raise DispatchError("scripted failure", code="RUN-FAILED")
        self.delivered.append(ctx.fire_key)
        return DeliveryResult(ok=True, detail={"scripted": True})


@pytest.fixture()
def flaky_factory():
    def make(fail_times=1, code="RUN-FAILED"):
        return FlakyExecutor(fail_times, code)
    return make


# ── a real local HTTP server for webhook end-to-end tests ───────────────────


class _WebhookHandler(BaseHTTPRequestHandler):
    received: list[dict] = []
    fail_status: int | None = None

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        import json

        body = json.loads(self.rfile.read(length) or b"{}")
        type(self).received.append(body)
        if type(self).fail_status:
            self.send_response(type(self).fail_status)
            self.end_headers()
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"ok":true}')

    def log_message(self, *args):  # silence
        pass


@pytest.fixture()
def webhook_server():
    server = HTTPServer(("127.0.0.1", 0), _WebhookHandler)
    _WebhookHandler.received = []
    _WebhookHandler.fail_status = None
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield server, _WebhookHandler
    server.shutdown()
    server.server_close()
