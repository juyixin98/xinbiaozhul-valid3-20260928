"""http 执行器：同步调用动作 URL。

投递语义：at-most-once 确认 / 崩溃时 at-least-once 投递 —— 动作端点必须幂等。
请求带 Idempotency-Key=<trigger_id> 与 X-Attempt-No。
- 2xx 成功；4xx 视为永久失败（不重试）；5xx 可重试；超时/连接错误可重试。
"""
from __future__ import annotations

from urllib.parse import urlparse

import httpx

from app.errors import A_CONNECTION, A_HTTP_4XX, A_HTTP_5XX, A_INTERNAL, A_TIMEOUT
from app.executor.base import ActionResult


class HttpExecutor:
    kind = "http"

    def __init__(self, max_response_bytes: int = 64 * 1024):
        self.max_response_bytes = max_response_bytes

    def execute(self, *, trigger: dict, attempt: int, spec: dict, timeout_s: float) -> ActionResult:
        action = spec.get("action", {})
        url = action.get("url") or ""
        p = urlparse(url)
        if p.scheme not in ("http", "https") or not p.hostname:
            # 规格层已拦截；这里是防御性二次校验，属于确定的永久失败
            return ActionResult("FAILED", A_INTERNAL, None,
                                f"invalid action url: {url!r}", retryable=False)
        headers = {
            "Idempotency-Key": str(trigger["id"]),
            "X-Attempt-No": str(attempt),
            "X-Schedule-Id": str(trigger["schedule_id"]),
            "X-Occurrence-No": str(trigger["occurrence_no"]),
            "Content-Type": "application/json",
        }
        body = {
            "trigger_id": str(trigger["id"]),
            "schedule_id": str(trigger["schedule_id"]),
            "occurrence_no": trigger["occurrence_no"],
            "due_at": trigger["due_at"].isoformat() if trigger.get("due_at") else None,
            "attempt": attempt,
        }
        try:
            with httpx.Client(timeout=timeout_s) as client:
                resp = client.post(url, json=body, headers=headers)
        except httpx.TimeoutException as exc:
            return ActionResult("FAILED", A_TIMEOUT, None, f"timeout: {exc}", retryable=True)
        except httpx.TransportError as exc:
            return ActionResult("FAILED", A_CONNECTION, None, f"connection error: {exc}", retryable=True)
        except Exception as exc:  # noqa: BLE001
            return ActionResult("FAILED", A_INTERNAL, None, f"adapter error: {exc}", retryable=False)

        text = resp.text[: self.max_response_bytes]
        if 200 <= resp.status_code < 300:
            return ActionResult("SUCCEEDED", None, f"HTTP {resp.status_code}: {text[:2000]}")
        if 400 <= resp.status_code < 500:
            return ActionResult("FAILED", A_HTTP_4XX, None,
                                f"HTTP {resp.status_code}: {text[:1000]}",
                                retryable=False, http_status=resp.status_code)
        return ActionResult("FAILED", A_HTTP_5XX, None,
                            f"HTTP {resp.status_code}: {text[:1000]}",
                            retryable=True, http_status=resp.status_code)
