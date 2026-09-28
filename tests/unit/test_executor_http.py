"""http 执行器对本地桩服务器：成功、4xx 终态、5xx 重试、超时、幂等头。"""
from __future__ import annotations

import threading
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import urlparse

from app.executor.http import HttpExecutor
from app.schemas import ActionSpec, ScheduleSpec
from app.services import create_schedule


class _Stub:
    def __init__(self, behavior):
        self.behavior = behavior  # callable(method,path,headers,body)->(status,text)
        self.requests = []
        self.server = HTTPServer(("127.0.0.1", 0), self._handler(self), )
        self.port = self.server.server_address[1]
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def _handler(self, outer):
        stub = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def do_POST(self):
                n = int(self.headers.get("content-length", "0"))
                body = self.rfile.read(n).decode()
                stub.requests.append({
                    "path": self.path,
                    "idem": self.headers.get("Idempotency-Key"),
                    "attempt": self.headers.get("X-Attempt-No"),
                    "body": body,
                })
                status, text = stub.behavior(self.path, dict(self.headers), body)
                self.send_response(status)
                self.send_header("content-type", "text/plain")
                self.end_headers()
                self.wfile.write(text.encode())

        return H

    def stop(self):
        self.server.shutdown()
        self.server.server_close()


def _trigger(pool, sid):
    with pool.connection() as conn:
        return conn.execute("SELECT * FROM triggers WHERE schedule_id=%s AND due_at=\'2026-01-01T00:00:00Z\'", (sid,)).fetchone()


def _sched(url):
    return ScheduleSpec(name="http", timezone="UTC", rrule="FREQ=DAILY",
                        start_at=datetime(2026, 1, 1), action=ActionSpec(type="http", url=url))


def test_http_success_idempotency_header(pool, settings, fake_clock, scheduler):
    stub = _Stub(lambda path, h, b: (200, "accepted"))
    try:
        sid = create_schedule(pool, _sched(f"http://127.0.0.1:{stub.port}/fire"),
                              now=fake_clock.now(),
                              max_occurrences=settings.max_occurrences_per_window)
        scheduler.http_executor = HttpExecutor()
        scheduler.run_tick(only_schedule=sid)
        t = _trigger(pool, sid)
        assert t["status"] == "SUCCEEDED"
        assert len(stub.requests) == 1
        assert stub.requests[0]["idem"] == str(t["id"])
        assert stub.requests[0]["attempt"] == "1"
    finally:
        stub.stop()


def test_http_4xx_is_terminal(pool, settings, fake_clock, scheduler):
    stub = _Stub(lambda path, h, b: (400, "bad"))
    try:
        sid = create_schedule(pool, _sched(f"http://127.0.0.1:{stub.port}/fire"),
                              now=fake_clock.now(),
                              max_occurrences=settings.max_occurrences_per_window)
        scheduler.http_executor = HttpExecutor()
        scheduler.run_tick(only_schedule=sid)
        t = _trigger(pool, sid)
        assert t["status"] == "FAILED" and t["attempts"] == 1  # 4xx 立即终态
        with pool.connection() as conn:
            ex = conn.execute("SELECT code FROM executions").fetchone()
        assert ex["code"] == "ADAPTER_HTTP_4XX"
    finally:
        stub.stop()


def test_http_5xx_retried(pool, settings, fake_clock, scheduler):
    stub = _Stub(lambda path, h, b: (503, "boom"))
    try:
        sid = create_schedule(pool, _sched(f"http://127.0.0.1:{stub.port}/fire"),
                              now=fake_clock.now(),
                              max_occurrences=settings.max_occurrences_per_window)
        scheduler.http_executor = HttpExecutor()
        scheduler.run_tick(only_schedule=sid)
        fake_clock.advance(60)
        scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
        fake_clock.advance(60)
        scheduler.run_tick(only_schedule=sid, override_now=fake_clock.now())
        t = _trigger(pool, sid)
        # 三次尝试（2s,4s 退避后到点），5xx 可重试，第三次仍失败 → 终态 FAILED
        assert t["status"] == "FAILED" and t["attempts"] == 3
        assert len(stub.requests) == 3
        with pool.connection() as conn:
            codes = [r["code"] for r in conn.execute(
                "SELECT code FROM executions ORDER BY attempt_no").fetchall()]
        assert codes == ["ADAPTER_HTTP_5XX"] * 3
    finally:
        stub.stop()


def test_http_timeout_classified(pool, settings, fake_clock, scheduler):
    # 指向一个“丢弃连接”的端口（10.x 不可达通常快速失败；这里用未监听本机端口触发连接错误/超时）
    spec = _sched("http://127.0.0.1:9/fire")  # discard port
    sid = create_schedule(pool, spec, now=fake_clock.now(),
                          max_occurrences=settings.max_occurrences_per_window)
    scheduler.http_executor = HttpExecutor()
    settings.http_timeout_seconds = 1.0
    scheduler.run_tick(only_schedule=sid)
    t = _trigger(pool, sid)
    with pool.connection() as conn:
        ex = conn.execute("SELECT code FROM executions").fetchone()
    assert t["status"] in ("PENDING", "FAILED")  # 连接类可重试，首次后排回
    assert ex["code"] in ("ADAPTER_CONNECTION", "ADAPTER_TIMEOUT")
