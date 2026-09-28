"""执行适配器协议与结果类型。"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Protocol


@dataclass(frozen=True)
class ActionResult:
    status: str           # SUCCEEDED | FAILED
    code: str | None      # ADAPTER_* （失败时）
    output: str | None    # 成功结果摘要（截断后入 executions.result）
    error: str | None = None
    retryable: bool = True
    http_status: int | None = None


class ExecutionAdapter(Protocol):
    kind: str

    def execute(self, *, trigger: dict, attempt: int, spec: dict, timeout_s: float) -> ActionResult: ...
