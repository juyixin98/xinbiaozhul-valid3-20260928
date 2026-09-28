"""log 执行器：默认动作。把触发事实写成结构化行（stdout + 返回审计摘要），永不失败。"""
from __future__ import annotations

import json
import logging
from datetime import timezone

logger = logging.getLogger("scheduler.executor.log")


class LogExecutor:
    kind = "log"

    def execute(self, *, trigger: dict, attempt: int, spec: dict, timeout_s: float):
        from app.executor.base import ActionResult

        due = trigger["due_at"]
        payload = {
            "phase": "EXECUTE",
            "verdict": "FIRED",
            "trigger_id": str(trigger["id"]),
            "schedule_id": str(trigger["schedule_id"]),
            "schedule_version": trigger["schedule_version"],
            "occurrence_no": trigger["occurrence_no"],
            "attempt_no": attempt,
            "local_wall": trigger["local_wall_ts"].isoformat(),
            "due_at_utc": due.astimezone(timezone.utc).isoformat() if due else None,
        }
        line = json.dumps(payload, ensure_ascii=False, default=str)
        logger.info(line)
        return ActionResult(status="SUCCEEDED", code=None, output=line[:4000])
