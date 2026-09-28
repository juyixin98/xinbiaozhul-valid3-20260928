"""Concrete executors: logging (tests/local) and HTTP webhook."""
from __future__ import annotations

from datetime import datetime

import httpx

from ..errors import DispatchError
from .base import DeliveryContext, DeliveryResult, Executor


class LogExecutor(Executor):
    """No-network delivery: success means the engine will persist the run.

    Used by the test suite and local demos; it mirrors what a real adapter
    returns so the engine path is identical.
    """

    type_name = "log"

    def deliver(self, ctx: DeliveryContext, *, run_id: str, now_utc: datetime) -> DeliveryResult:
        body = ctx.envelope("SUCCEEDED", run_id, now_utc)
        # stdout is the "external system" for this adapter.
        print(
            f"[log-executor] run_id={run_id} fire_key={ctx.fire_key} "
            f"due_utc={ctx.due_utc.isoformat()} name={ctx.schedule_name!r}"
        )
        return DeliveryResult(ok=True, detail={"logged": True, "envelope": body})


class WebhookExecutor(Executor):
    """POST the fire envelope as JSON; success on HTTP 2xx.

    Timeout/connection/5xx -> RUN-FAILED   (transient; manual retry possible)
    HTTP 4xx               -> RUN-PERMANENT (target rejected; terminal)
    """

    type_name = "webhook"

    def deliver(self, ctx: DeliveryContext, *, run_id: str, now_utc: datetime) -> DeliveryResult:
        if not ctx.target_url:
            raise DispatchError(
                "webhook delivery without target url", code="RUN-MISCONFIGURED"
            )
        body = ctx.envelope("FIRED", run_id, now_utc)
        try:
            resp = httpx.post(
                ctx.target_url,
                json=body,
                timeout=ctx.deadline_seconds,
            )
        except httpx.TimeoutException as exc:
            raise DispatchError(
                f"webhook timed out after {ctx.deadline_seconds}s",
                code="RUN-TIMEOUT",
                details={"url": ctx.target_url},
            ) from exc
        except httpx.HTTPError as exc:
            raise DispatchError(
                f"webhook request failed: {exc}",
                code="RUN-TRANSPORT",
                details={"url": ctx.target_url},
            ) from exc

        detail = {"status_code": resp.status_code, "url": ctx.target_url}
        if 200 <= resp.status_code < 300:
            detail["response_snippet"] = resp.text[:200]
            return DeliveryResult(ok=True, detail=detail)
        if 400 <= resp.status_code < 500:
            raise DispatchError(
                f"webhook target rejected delivery with HTTP {resp.status_code}",
                code="RUN-PERMANENT",
                details=detail,
            )
        raise DispatchError(
            f"webhook target returned HTTP {resp.status_code}",
            code="RUN-FAILED",
            details=detail,
        )
