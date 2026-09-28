"""Scheduling kernel.

The engine wires the three pure/side-effecting sides together:

    planner (pure)  ->  repository (state/audit)  ->  executors (delivery)

It owns the two non-obvious guarantees from the problem:

1. **Deterministic bounded catch-up.** Each tick first expires fires behind a
   fixed window, then claims due fires oldest-first with a hard per-tick cap.
   Catch-up order is ``(due_utc, fire_key)`` — total and reproducible. The cap
   is reported; exceeding it does not silently drop work, later ticks drain
   the remainder (fires are already materialised).
2. **No re-execution on clock rollback.** The tick watermark
   (``runtime_state.last_tick_utc``) only moves forward; ``fire_key`` is unique;
   only ``due``/lease-expired ``running`` fires are ever claimed. A confirmed
   (``succeeded``/``failed`` terminal) fire can never be selected again.
"""
from __future__ import annotations

from datetime import date, datetime, timedelta
from typing import Any

from ..config import Settings
from ..errors import ConflictError, DispatchError
from ..execution.adapters import LogExecutor, WebhookExecutor
from ..execution.base import DeliveryContext, DeliveryResult, Executor
from ..planner.calendar import UTC
from ..planner.occurrence import (
    SkipReason,
    plan_window,
)
from ..planner.spec import ScheduleSpec, parse_spec
from ..state.repo import Repository

# A claimed run whose worker died is reclaimable after this lease.
RUN_LEASE = timedelta(minutes=5)


def _as_utc(now: datetime) -> datetime:
    if now.tzinfo is None:
        return now.replace(tzinfo=UTC)
    return now.astimezone(UTC)


class SchedulerEngine:
    def __init__(
        self,
        repo: Repository,
        settings: Settings,
        executors: dict[str, Executor] | None = None,
    ) -> None:
        self.repo = repo
        self.settings = settings
        self.executors: dict[str, Executor] = executors or {
            "log": LogExecutor(),
            "webhook": WebhookExecutor(),
        }

    # ── schedule management ────────────────────────────────────────────────

    def create_schedule(self, raw_spec: dict[str, Any], *, now: datetime | None = None) -> dict:
        now = _as_utc(now or datetime.now(UTC))
        spec = parse_spec(raw_spec)
        row = self.repo.create_schedule(spec, now=now)
        # Plan immediately so GET /schedules/{id}/fires shows near-term fires.
        self._extend_plan(row["id"], spec, row["version"], now)
        return self.repo.get_schedule(row["id"])

    def update_schedule(
        self, schedule_id: str, raw_spec: dict[str, Any], *, now: datetime | None = None
    ) -> dict:
        now = _as_utc(now or datetime.now(UTC))
        spec = parse_spec(raw_spec)
        row = self.repo.update_schedule(schedule_id, spec, now=now)
        self._extend_plan(schedule_id, spec, row["version"], now)
        return self.repo.get_schedule(schedule_id)

    def set_paused(self, schedule_id: str, paused: bool, *, now: datetime | None = None) -> dict:
        return self.repo.set_paused(
            schedule_id, paused, now=_as_utc(now or datetime.now(UTC))
        )

    def delete_schedule(self, schedule_id: str, *, now: datetime | None = None) -> None:
        self.repo.delete_schedule(schedule_id, now=_as_utc(now or datetime.now(UTC)))

    # ── planning extension ─────────────────────────────────────────────────

    def _plan_target_day(self, now: datetime, spec: ScheduleSpec) -> date:
        # Local date corresponding to now + plan-ahead; convert through the
        # schedule zone so DST doesn't bleed a day into the window.
        from ..planner.calendar import load_zone

        zone = load_zone(spec.timezone)
        local_now = now.astimezone(zone)
        return local_now.date() + timedelta(days=self.settings.plan_ahead_days)

    def _extend_plan(
        self, schedule_id: str, spec: ScheduleSpec, version: int, now: datetime
    ) -> dict[str, Any]:
        """Ensure fires are materialised through plan_ahead_days past now.

        Idempotent: advancing the cursor over the same dates inserts nothing.
        """
        schedule = self.repo.get_schedule(schedule_id)
        cursor = (
            date.fromisoformat(schedule["planned_through_local"])
            if schedule["planned_through_local"]
            else None
        )
        # Start one local day behind "today" to catch today's late/ambiguous
        # instants if the schedule was created mid-day.
        from ..planner.calendar import load_zone

        local_today = now.astimezone(load_zone(spec.timezone)).date()
        start = local_today - timedelta(days=1)
        target = min(self._plan_target_day(now, spec), date.max - timedelta(days=2))
        if spec.end is not None:
            target = min(target, spec.end)
        if cursor is not None and cursor >= target:
            return {"planned": 0, "skipped": []}
        window_start = max(start, cursor + timedelta(days=1)) if cursor else max(start, spec.start)
        if target < window_start:
            return {"planned": 0, "skipped": []}

        planned = plan_window(spec, window_start, target)
        rows = []
        for f in planned.fires:
            rows.append(
                {
                    "schedule_id": schedule_id,
                    "version": version,
                    "kind": f.kind.value,
                    "ordinal": f.ordinal,
                    "fold": f.fold,
                    "fire_key": f.fire_key(schedule_id, version),
                    "date_local": f.date_local,
                    "due_utc": f.due_utc.astimezone(UTC).replace(tzinfo=None),
                    "utc_offset_minutes": f.utc_offset_minutes,
                }
            )
        inserted = 0
        with self.repo.tx() as conn:
            inserted = self.repo.insert_planned_fires(conn, schedule_id, rows)
            for s in planned.skipped:
                self.repo.audit(
                    conn,
                    {
                        SkipReason.GAP: "plan.skipped-gap",
                        SkipReason.EXCEPTION: "plan.skipped-exception",
                        SkipReason.OUT_OF_BOUNDS: "plan.skipped-out-of-bounds",
                        SkipReason.SHADOWED_BY_BASE: "plan.skipped-shadowed",
                    }[s.reason],
                    schedule_id=schedule_id,
                    detail=s.explain(),
                    ts=now,
                )
        self.repo.mark_planned_through(schedule_id, target.isoformat())
        return {
            "planned": inserted,
            "window": [window_start.isoformat(), target.isoformat()],
            "skipped": [s.explain() for s in planned.skipped],
        }

    # ── the tick ───────────────────────────────────────────────────────────

    def tick(self, now: datetime | None = None) -> dict[str, Any]:
        """Advance the whole scheduler by one step.

        Order of operations (deterministic):
          1. refuse if now <= last tick watermark (clock rollback)
          2. extend every active schedule's plan horizon
          3. expire pending fires behind the catch-up window (status expired)
          4. claim due fires oldest-first up to max_backfill *plus* zero-lag
             on-time fires, then deliver each in claim order
        """
        now = _as_utc(now or datetime.now(UTC))
        result: dict[str, Any] = {
            "tick_utc": now.isoformat(),
            "planned": [],
            "expired": 0,
            "claimed": 0,
            "on_time": 0,
            "catch_up": 0,
            "deferred": 0,
            "succeeded": [],
            "failed": [],
            "backfill_remaining": 0,
            "rolled_back_rejected": False,
        }

        # (1) watermark — raises ConflictError(STATE-CLOCK-ROLLBACK) backwards.
        with self.repo.tick_lock(now) as prev_watermark:
            result["previous_tick_utc"] = (
                prev_watermark.astimezone(UTC).isoformat() if prev_watermark else None
            )

        # (2) planning
        for sch in self.repo.list_schedules():
            if sch["status"] != "active":
                continue
            spec = parse_spec(sch["spec"])
            ext = self._extend_plan(sch["id"], spec, sch["version"], now)
            if ext["planned"]:
                result["planned"].append(
                    {"schedule_id": sch["id"], "inserted": ext["planned"],
                     "window": ext.get("window")}
                )

        # (3) expiry behind catch-up window
        cutoff = now - timedelta(days=self.settings.backfill_window_days)
        result["expired"] = self.repo.expire_behind(cutoff_utc=cutoff, now=now)

        # (4) claim + deliver. Budget semantics:
        #     on-time fires (due within the tick horizon) are always served;
        #     MISSED (catch-up) fires share the max_backfill budget, and any
        #     over-budget missed fire stays due for a later tick.
        horizon = timedelta(seconds=self.settings.tick_horizon_seconds)
        # SQL decides priority and the catch-up cap: on-time bucket (no cap)
        # then at most max_backfill oldest catch-up fires, already ordered.
        claimed = self.repo.claim_due_fires(
            now=now,
            horizon=horizon,
            catch_up_limit=self.settings.max_backfill,
            lease=RUN_LEASE,
        )
        on_time_floor = now - horizon
        to_run: list[dict] = []
        on_time_count = 0
        for f in claimed:
            due = datetime.fromisoformat(f["due_utc"])
            if due > on_time_floor:
                on_time_count += 1
            to_run.append(f)

        # deterministic delivery happens in the claim order above

        # SQL already returned fires in delivery order; keep that ordering.
        result["claimed"] = len(to_run)
        result["on_time"] = on_time_count
        result["catch_up"] = len(to_run) - on_time_count
        for f in to_run:
            outcome = self._deliver(f, now=now)
            bucket = "succeeded" if outcome["status"] == "succeeded" else "failed"
            result[bucket].append(outcome)

        result["backfill_remaining"] = self.repo.count_active_due()
        self.repo.record_tick_result(result, now=now)
        return result

    # ── delivery of one fire ───────────────────────────────────────────────

    def _executor_for(self, spec: ScheduleSpec) -> Executor:
        return self.executors[spec.executor.type.value]

    def _deliver(self, fire: dict, *, now: datetime) -> dict[str, Any]:
        schedule = self.repo.get_schedule(fire["schedule_id"])
        spec = parse_spec(schedule["spec"])
        executor = self._executor_for(spec)
        ctx = DeliveryContext(
            fire_key=fire["fire_key"],
            schedule_id=fire["schedule_id"],
            schedule_version=fire["version"],
            schedule_name=spec.name,
            due_utc=datetime.fromisoformat(fire["due_utc"]),
            attempt=fire["attempt"],
            deadline_seconds=self.settings.dispatch_timeout_seconds,
            payload=spec.executor.payload_template,
            target_url=spec.executor.url,
        )
        run_id = fire["run_id"]
        try:
            res: DeliveryResult = executor.deliver(ctx, run_id=run_id, now_utc=now)
            if not res.ok:
                raise DispatchError("executor reported failure", code="RUN-FAILED",
                                    details=res.detail)
        except DispatchError as exc:
            self.repo.complete_run(
                run_id=run_id, fire_key=fire["fire_key"], succeeded=False,
                detail={}, error_code=exc.code, error_message=exc.message, now=now,
            )
            return {"fire_key": fire["fire_key"], "run_id": run_id,
                    "status": "failed", "error_code": exc.code, "error": exc.message}
        except Exception as exc:  # adapter defect: recorded, never surfaced as success
            self.repo.complete_run(
                run_id=run_id, fire_key=fire["fire_key"], succeeded=False,
                detail={"exception_type": type(exc).__name__},
                error_code="RUN-FAILED", error_message=str(exc), now=now,
            )
            return {"fire_key": fire["fire_key"], "run_id": run_id,
                    "status": "failed", "error_code": "RUN-FAILED",
                    "error": f"{type(exc).__name__}: {exc}"}

        self.repo.complete_run(
            run_id=run_id, fire_key=fire["fire_key"], succeeded=True,
            detail=res.detail, error_code=None, error_message=None, now=now,
        )
        return {"fire_key": fire["fire_key"], "run_id": run_id, "status": "succeeded"}

    # ── manual retry (failed terminal fires only) ──────────────────────────

    def retry_fire(self, fire_key: str, *, now: datetime | None = None) -> dict:
        """Re-queue a terminal failed/expired fire for the next claim.

        Succeeded fires are refused (STATE-ALREADY-CONFIRMED) — history is
        immutable and re-delivery is explicitly rejected.
        """
        now = _as_utc(now or datetime.now(UTC))
        fire = self.repo.get_fire_by_key(fire_key)
        if fire["status"] == "succeeded":
            raise ConflictError(
                "fire already succeeded; confirmed triggers cannot be re-run",
                code="STATE-ALREADY-CONFIRMED",
                details={"fire_key": fire_key, "status": fire["status"]},
            )
        if fire["status"] in ("planned", "due", "running", "cancelled"):
            raise ConflictError(
                f"fire is {fire['status']}, retry applies only to failed/expired",
                code="STATE-NOT-RETRYABLE",
                details={"fire_key": fire_key, "status": fire["status"]},
            )
        from ..state.repo import utcnow_naive

        with self.repo.tx() as conn:
            conn.execute(
                """
                UPDATE fires SET status = 'due', run_id = NULL,
                                 last_error_code = NULL, last_error_message = NULL,
                                 updated_at_utc = %s
                 WHERE fire_key = %s
                """,
                (utcnow_naive(now), fire_key),
            )
            self.repo.audit(
                conn, "fire.retry-queued",
                schedule_id=fire["schedule_id"], fire_key=fire_key,
                detail={"previous_status": fire["status"]}, ts=now,
            )
        return self.repo.get_fire_by_key(fire_key)
