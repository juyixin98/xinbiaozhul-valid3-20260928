"""调度内核：一次确定性 pass（tick）。

阶段（每个计划、按 schedule_id 升序）：
  heartbeat 检查 → 回收过期租约 → 咨询锁物化 → 判定停机/开 recovery epoch
  → 分配 backlog rank → 容量公式 OVERFLOW → 过期 EXPIRED → 认领准时
  → 认领补跑（预算）→ 事务提交后执行副作用 → 逐触发器收尾
单个 observed_now 贯穿整个 run，保证分类确定。计划级错误 fail-closed（不派发），
run 状态 PARTIAL/FAILED，绝不把未收敛包装成成功。
"""
from __future__ import annotations

import json
import uuid
from dataclasses import asdict, dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Callable

from psycopg_pool import ConnectionPool

from app.config import Settings
from app.executor.base import ActionResult
from app.executor.http import HttpExecutor
from app.executor.log import LogExecutor
from app.logx import log_event
from app.planner.service import Plan, PlanRequest, plan as build_plan
from app.state import audit
from app.state.repositories import RunRepository, ScheduleRepository, TriggerRepository
from app.kernel.materialize import materialize_schedule

UTC = timezone.utc


@dataclass
class ScheduleTick:
    schedule_id: str
    status: str                      # OK | ERROR | SKIPPED_LOCK
    version: int | None = None
    inserted: int = 0
    rearmed: int = 0
    cancelled: int = 0
    merged: int = 0
    recovery_epoch: int | None = None
    backlog_assigned: int = 0
    overflow: int = 0
    expired: int = 0
    claimed_ontime: int = 0
    claimed_backlog: int = 0
    succeeded: int = 0
    failed_terminal: int = 0
    failed_retry: int = 0
    error_code: str | None = None
    error_message: str | None = None


@dataclass
class TickResult:
    run_id: str
    observed_now: str
    status: str                      # OK | PARTIAL | FAILED
    schedules: list[ScheduleTick] = field(default_factory=list)

    def to_dict(self) -> dict:
        return {
            "run_id": self.run_id,
            "observed_now": self.observed_now,
            "status": self.status,
            "schedules": [asdict(s) for s in self.schedules],
        }


class Scheduler:
    def __init__(self, pool: ConnectionPool, settings: Settings, *,
                 clock=None, log_executor=None, http_executor=None,
                 effective_catchup: Callable[[dict], tuple[float, float, int]] | None = None):
        self.pool = pool
        self.settings = settings
        self.clock = clock  # None → 系统时钟
        self.log_executor = log_executor or LogExecutor()
        self.http_executor = http_executor or HttpExecutor(settings.http_max_response_bytes)
        # 允许测试替换“计划生效补跑参数”解析
        self.effective_catchup = effective_catchup or _default_effective_catchup

    def now(self) -> datetime:
        return self.clock.now() if self.clock else datetime.now(UTC)

    def _adapter(self, action_type: str):
        return self.http_executor if action_type == "http" else self.log_executor

    # ------------------------------------------------------------------

    def run_tick(self, *, only_schedule: uuid.UUID | str | None = None,
                 override_now: datetime | None = None) -> TickResult:
        now = (override_now or self.now()).astimezone(UTC)
        if only_schedule is not None:
            only_schedule = uuid.UUID(str(only_schedule))
        run_id = uuid.uuid4()
        result = TickResult(run_id=str(run_id), observed_now=now.isoformat(), status="OK")
        s = self.settings

        with self.pool.connection() as conn, conn.transaction():
            RunRepository(conn).start(
                run_id=run_id, worker_id=s.worker_id, code_version=s.code_version,
                tzdata_ver=_tz_version(), now=now,
            )

        # 全局：回收过期租约（与具体计划无关）
        with self.pool.connection() as conn, conn.transaction():
            reclaimed = TriggerRepository(conn).reclaim_stale(
                now=now, lease_timeout=s.lease_timeout_seconds, run_id=run_id,
                worker_id=s.worker_id, max_attempts=s.max_attempts,
            )
        if reclaimed:
            log_event(run_id=str(run_id), phase="CLAIM", step="reclaim_stale_leases",
                      verdict="RECLAIMED", count=reclaimed, observed_now=now.isoformat())

        with self.pool.connection() as conn, conn.transaction():
            sched_repo = ScheduleRepository(conn)
            schedules = sched_repo.list_active()
        if only_schedule is not None:
            schedules = [x for x in schedules if x.id == only_schedule]

        any_error = False
        global_backlog_budget = s.catchup_global_budget

        for sch in schedules:
            tick = ScheduleTick(schedule_id=str(sch.id), status="OK", version=sch.version)
            try:
                spec = sch.spec
                grace, deadline, rate = self.effective_catchup(spec)
                horizon = timedelta(days=s.horizon_days)
                window_end = now + horizon
                window_start = now - timedelta(seconds=deadline) - timedelta(days=1)

                # ---- 物化（咨询锁事务）----
                plan_obj: Plan = build_plan(PlanRequest(
                    schedule_id=sch.id, spec=_spec_from_dict(spec),
                    window_start_utc=window_start, window_end_utc=window_end,
                    max_occurrences=s.max_occurrences_per_window,
                ))
                with self.pool.connection() as conn, conn.transaction():
                    recon = materialize_schedule(
                        conn, schedule_id=sch.id, version=sch.version,
                        plan=plan_obj, window_end=window_end, now=now,
                        tz_name=spec.get("timezone"),
                    )
                    if recon.errors:
                        tick.status = "SKIPPED_LOCK"
                        tick.error_message = ";".join(recon.errors)
                        result.schedules.append(tick)
                        continue
                    tick.inserted = recon.inserted
                    tick.rearmed = recon.rearmed
                    tick.cancelled = recon.cancelled
                    tick.merged = recon.merged
                    for w in plan_obj.warnings:
                        audit.record(conn, "schedule", "PLAN_WARNING", entity_id=str(sch.id),
                                     detail={"warning": w}, run_id=run_id)

                if sch.status != "ACTIVE":
                    # PAUSED：只物化，不派发；不更新心跳（恢复后按停机检测走 EXPIRED/补跑）
                    result.schedules.append(tick)
                    continue

                # ---- 停机检测 / recovery epoch ----
                with self.pool.connection() as conn, conn.transaction():
                    repo = ScheduleRepository(conn)
                    hb = repo.get_heartbeat(sch.id)
                    downtime_threshold = max(s.downtime_threshold_seconds, s.tick_interval_seconds * 3)
                    epoch, rec_started = repo.get_recovery(sch.id)
                    is_down = hb is None or (now - hb) > timedelta(seconds=downtime_threshold)
                    if is_down:
                        epoch = repo.open_recovery_epoch(sch.id, now)
                        rec_started = now
                        audit.record(conn, "schedule", "RECOVERY_EPOCH_OPENED",
                                     entity_id=str(sch.id), run_id=run_id,
                                     detail={"epoch": epoch, "last_heartbeat": hb.isoformat() if hb else None})
                        log_event(run_id=str(run_id), schedule_id=str(sch.id),
                                  phase="CLAIM", step="open_recovery_epoch",
                                  verdict="DOWNTIME_DETECTED", epoch=epoch,
                                  last_heartbeat=hb.isoformat() if hb else None)
                    tick.recovery_epoch = epoch

                # ---- backlog 排名 + 容量 OVERFLOW + EXPIRED（单事务）----
                with self.pool.connection() as conn, conn.transaction():
                    trepo = TriggerRepository(conn)
                    if is_down:
                        tick.backlog_assigned = trepo.assign_backlog(
                            schedule_id=sch.id, epoch=epoch, now=now, grace=grace)
                        # 先把已超 deadline 的判 EXPIRED，再对剩余积压做容量 OVERFLOW
                        tick.expired = trepo.mark_expired(
                            schedule_id=sch.id, now=now, deadline=deadline)
                        if tick.expired:
                            audit.record(conn, "schedule", "TRIGGERS_EXPIRED", entity_id=str(sch.id),
                                         run_id=run_id, detail={"count": tick.expired})
                        tick.overflow = trepo.mark_overflow(
                            schedule_id=sch.id, epoch=epoch, now=now, grace=grace,
                            deadline=deadline, rate_limit=rate,
                            tick_interval=s.tick_interval_seconds,
                            recovery_started_at=rec_started or now)
                        if tick.overflow:
                            audit.record(conn, "schedule", "BACKLOG_OVERFLOW", entity_id=str(sch.id),
                                         run_id=run_id, detail={"count": tick.overflow, "epoch": epoch})
                    else:
                        tick.expired = trepo.mark_expired(schedule_id=sch.id, now=now, deadline=deadline)
                        if tick.expired:
                            audit.record(conn, "schedule", "TRIGGERS_EXPIRED", entity_id=str(sch.id),
                                         run_id=run_id, detail={"count": tick.expired})

                # ---- 认领 + 执行 ----
                budget = min(rate, global_backlog_budget)
                claimed: list[dict] = []
                with self.pool.connection() as conn, conn.transaction():
                    trepo = TriggerRepository(conn)
                    ontime = trepo.claim(
                        schedule_id=sch.id, run_id=run_id, worker_id=s.worker_id, now=now,
                        deadline=deadline, lease_timeout=s.lease_timeout_seconds,
                        limit=rate, backlog_only=False, grace=grace)
                    tick.claimed_ontime = len(ontime)
                    claimed.extend(ontime)
                    backlog = trepo.claim(
                        schedule_id=sch.id, run_id=run_id, worker_id=s.worker_id, now=now,
                        deadline=deadline, lease_timeout=s.lease_timeout_seconds,
                        limit=budget, backlog_only=True)
                    tick.claimed_backlog = len(backlog)
                    claimed.extend(backlog)
                    global_backlog_budget -= len(backlog)

                # ---- 副作用在事务外执行；按 due_at 升序（确定性补跑顺序）----
                claimed.sort(key=lambda t: (t["due_at"], str(t["id"])))
                adapter = self._adapter(spec.get("action", {}).get("type", "log"))
                for t in claimed:
                    outcome = self._execute_one(adapter, t, spec, run_id, now, deadline, grace)
                    if outcome == "SUCCEEDED":
                        tick.succeeded += 1
                    elif outcome == "FAILED":
                        tick.failed_terminal += 1
                    else:
                        tick.failed_retry += 1

                # ---- 心跳：本计划完整处理完毕才推进（崩溃则下次仍判停机）----
                with self.pool.connection() as conn, conn.transaction():
                    ScheduleRepository(conn).mark_heartbeat(sch.id, now)

                log_event(run_id=str(run_id), schedule_id=str(sch.id), phase="FINALIZE",
                          step="schedule_tick", verdict=tick.status, **_tick_log(tick))

            except Exception as exc:  # noqa: BLE001
                any_error = True
                tick.status = "ERROR"
                tick.error_code = type(exc).__name__
                tick.error_message = str(exc)[:500]
                log_event(run_id=str(run_id), schedule_id=str(sch.id), phase="FINALIZE",
                          step="schedule_tick", verdict="UNDEFINED",
                          error_code=tick.error_code, error=tick.error_message)
                with self.pool.connection() as conn, conn.transaction():
                    audit.record(conn, "schedule", "TICK_ERROR", entity_id=str(sch.id),
                                 run_id=run_id, detail={"error": tick.error_message,
                                                        "code": tick.error_code})
            result.schedules.append(tick)

        result.status = "PARTIAL" if any_error else "OK"
        stats = result.to_dict()
        with self.pool.connection() as conn, conn.transaction():
            RunRepository(conn).finish(run_id, now=now, status=result.status, stats=stats)
        return result

    # ------------------------------------------------------------------

    def _execute_one(self, adapter, trigger: dict, spec: dict, run_id: uuid.UUID,
                     now: datetime, deadline: float, grace: float) -> str:
        attempt = trigger["attempts"]
        log_event(run_id=str(run_id), trigger_id=str(trigger["id"]),
                  schedule_id=str(trigger["schedule_id"]),
                  schedule_version=trigger["schedule_version"],
                  occurrence_no=trigger["occurrence_no"], attempt_no=attempt,
                  phase="EXECUTE", step="invoke_adapter", verdict="RUNNING",
                  adapter=getattr(adapter, "kind", "?"),
                  due_at_utc=trigger["due_at"].isoformat(),
                  local_wall=trigger["local_wall_ts"].isoformat(),
                  age_seconds=(now - trigger["due_at"]).total_seconds())
        result: ActionResult = adapter.execute(
            trigger=trigger, attempt=attempt, spec=spec,
            timeout_s=self.settings.http_timeout_seconds)

        with self.pool.connection() as conn, conn.transaction():
            trepo = TriggerRepository(conn)
            if result.status == "SUCCEEDED":
                trepo.finalize_success(trigger["id"], now, result.output or "")
                final_status = "SUCCEEDED"
            else:
                # 永久失败（4xx 等）不等待退避；可重试失败确定性指数退避
                backoff = 0.0 if not result.retryable else min(2.0 ** attempt, 300.0)
                final_status = trepo.finalize_failure(
                    trigger["id"], now, result.code or "ADAPTER_UNKNOWN",
                    result.error or "unknown error", self.settings.max_attempts, backoff,
                    terminal=not result.retryable)

        log_event(run_id=str(run_id), trigger_id=str(trigger["id"]),
                  occurrence_no=trigger["occurrence_no"], attempt_no=attempt,
                  phase="FINALIZE", step="adapter_result",
                  verdict="SUCCEEDED" if final_status == "SUCCEEDED" else final_status,
                  code=result.code, retryable=result.retryable)
        if final_status == "SUCCEEDED":
            return "SUCCEEDED"
        # PENDING=已排回等待重试；FAILED=终态
        return "FAILED" if final_status == "FAILED" else "RETRY"


def _default_effective_catchup(spec: dict) -> tuple[float, float, int]:
    from app.config import get_settings

    s = get_settings()
    return (
        spec.get("lateness_grace_seconds") or s.lateness_grace_seconds,
        spec.get("catchup_deadline_seconds") or s.catchup_deadline_seconds,
        spec.get("catchup_rate_limit") or s.catchup_rate_limit,
    )


def _tick_log(tick: ScheduleTick) -> dict:
    return {k: v for k, v in vars(tick).items() if k not in ("schedule_id", "status") and v}


def _tz_version() -> str:
    from app.planner.tzutil import tzdata_version

    return tzdata_version()


def _spec_from_dict(d: dict):
    """把库中 jsonb spec 还原为 ScheduleSpec（service 层输入）。"""
    from app.schemas import ScheduleSpec

    return ScheduleSpec.model_validate(d)
