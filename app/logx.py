"""结构化日志：每条一行 JSON，携带 request/run/trigger/版本/阶段/判定等可解释字段。"""
from __future__ import annotations

import json
import logging
import sys

_CONFIGURED = False


def configure_logging(level: str = "INFO") -> None:
    global _CONFIGURED
    if _CONFIGURED:
        return
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(logging.Formatter("%(message)s"))
    root = logging.getLogger("scheduler")
    root.handlers[:] = [handler]
    root.setLevel(getattr(logging, level.upper(), logging.INFO))
    root.propagate = False
    _CONFIGURED = True


def log_event(logger_name: str = "scheduler", **fields) -> None:
    logging.getLogger(logger_name).info(json.dumps(fields, ensure_ascii=False, default=str))
