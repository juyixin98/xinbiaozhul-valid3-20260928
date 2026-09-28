"""End-to-end, dependency-free demo driven through the real FastAPI stack.

Scenarios printed with local-time -> UTC decisions, run ids and audit steps:

  A. Fall-back ambiguity (America/New_York 2024-11-03 01:30 fires twice).
  B. Spring-forward gap (2024-03-10 02:30 skipped, audit records why).
  C. Downtime catch-up (bounded, deterministic oldest-first order).
  D. Clock rollback refused (STATE-CLOCK-ROLLBACK), no re-execution.
  E. Plan edit bumps version; confirmed history never re-fires.

Uses an isolated demo database so it never touches the test/dev data.
"""
from __future__ import annotations

import os
import sys
from datetime import datetime, timezone

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import psycopg
from starlette.testclient import TestClient

from scheduler.config import Settings
from scheduler.main import build

UTC = timezone.utc
HOST = os.environ.get("SCHED_PG_HOST", "127.0.0.1")
PORT = os.environ.get("SCHED_PG_PORT", "5433")
ADMIN_URL = os.environ.get(
    "SCHED_TEST_ADMIN_URL", f"postgresql://scheduler:scheduler@{HOST}:{PORT}/postgres"
)
DEMO_DB = "scheduler_demo"
DEMO_URL = os.environ.get(
    "SCHED_DEMO_DATABASE_URL", f"postgresql://scheduler:scheduler@{HOST}:{PORT}/{DEMO_DB}"
)


def ensure_db() -> None:
    with psycopg.connect(ADMIN_URL, autocommit=True) as conn:
        conn.execute(
            f"SELECT pg_terminate_backend(pid) FROM pg_stat_activity "
            f"WHERE datname='{DEMO_DB}' AND pid<>pg_backend_pid()"
        )
        conn.execute(f"DROP DATABASE IF EXISTS {DEMO_DB}")
        conn.execute(f"CREATE DATABASE {DEMO_DB}")


def post(c, method, path, *, json=None, now=None):
    headers = {"X-Test-Now": now.isoformat()} if now else {}
    r = c.request(method, path, json=json, headers=headers)
    if r.status_code >= 400:
        raise SystemExit(f"{method} {path} -> {r.status_code} {r.text}")
    return r


def section(title: str) -> None:
    print(f"\n{'=' * 72}\n{title}\n{'=' * 72}")


def main() -> None:
    ensure_db()
    settings = Settings(
        database_url=DEMO_URL,
        max_backfill=5, tick_horizon_seconds=60, plan_ahead_days=60,
        backfill_window_days=30, dispatch_timeout_seconds=5.0,
        enable_background_ticker=False,
    )
    app, repo, engine = build(settings)

    with TestClient(app, base_url="http://demo") as c:
        # Scenarios run in chronological order because the tick watermark is
        # service-wide and strictly monotonic (property D).

        # ── B: spring-forward gap ─────────────────────────────────────────
        section("A. Spring-forward gap — 2024-03-10 02:30 does not exist -> skipped")
        post(c, "POST", "/schedules", now=datetime(2024, 3, 1, tzinfo=UTC), json={
            "name": "gap", "timezone": "America/New_York", "frequency": "daily",
            "at": "02:30", "start_date": "2024-03-08", "end_date": "2024-03-12",
            "executor": {"type": "log"},
        })
        audit = c.get("/audit?event=plan.skipped-gap").json()["events"]
        for e in audit:
            print(f"  audit: {e['event']} {e['detail']}")

        # ── C: bounded ordered catch-up ───────────────────────────────────
        section("B. Downtime recovery — cap=5, deterministic delivery order")
        post(c, "POST", "/schedules", now=datetime(2024, 3, 2, tzinfo=UTC), json={
            "name": "nightly", "timezone": "America/New_York", "frequency": "daily",
            "at": "23:00", "start_date": "2024-03-01", "end_date": "2024-03-31",
            "executor": {"type": "log"},
        })
        t1 = post(c, "POST", "/ticks", now=datetime(2024, 3, 10, 4, tzinfo=UTC)).json()
        print(f"tick1: on_time={t1['on_time']} catch_up={t1['catch_up']} "
              f"backfill_remaining={t1['backfill_remaining']}")
        print("  delivery order (UTC due, ordinal):")
        for o in t1["succeeded"]:
            f = c.get(f"/fires/{o['fire_key']}").json()
            print(f"    due={f['due_utc']} ordinal={f['ordinal']} run={o['run_id']}")
        t2 = post(c, "POST", "/ticks", now=datetime(2024, 3, 10, 4, 0, 1, tzinfo=UTC)).json()
        print(f"tick2 drained {len(t2['succeeded'])} more; remaining={t2['backfill_remaining']}")

        # ── D: clock rollback ─────────────────────────────────────────────
        section("C. Clock rollback refused — confirmed triggers never re-run")
        r = c.post("/ticks", headers={"X-Test-Now": datetime(2024, 3, 10, 4, tzinfo=UTC).isoformat()})
        print(f"repeated instant -> HTTP {r.status_code} code={r.json()['error']['code']}")

        # ── A: fall-back ambiguity ────────────────────────────────────────
        section("D. Fall-back ambiguity — America/New_York 2024-11-03 01:30")
        print("Local 01:30 exists TWICE: 05:30Z (EDT, fold=0) then 06:30Z (EST, fold=1)")
        sid = post(c, "POST", "/schedules", now=datetime(2024, 11, 3, tzinfo=UTC), json={
            "name": "ambiguous", "timezone": "America/New_York", "frequency": "daily",
            "at": "01:30", "start_date": "2024-11-03", "end_date": "2024-11-03",
            "ambiguous_policy": "both", "executor": {"type": "log"},
        }).json()["id"]
        tick = post(c, "POST", "/ticks", now=datetime(2024, 11, 3, 7, tzinfo=UTC)).json()
        keys = sorted(o["fire_key"] for o in tick["succeeded"])
        print(f"fired {len(keys)} distinct identities:")
        for k in keys:
            f = c.get(f"/fires/{k}").json()
            print(f"  {k}  due_utc={f['due_utc']} fold={f['fold']} status={f['status']}")

        # ── E: version edit keeps history ─────────────────────────────────
        section("E. Editing a plan versions identities; history immutable")
        before = {x["fire_key"]: x["status"] for x in c.get(f"/schedules/{sid}/fires").json()["fires"]}
        post(c, "PUT", f"/schedules/{sid}", now=datetime(2024, 11, 4, tzinfo=UTC), json={
            "name": "ambiguous", "timezone": "Asia/Shanghai", "frequency": "daily",
            "at": "09:00", "start_date": "2024-11-03", "end_date": "2024-11-10",
            "ambiguous_policy": "both", "executor": {"type": "log"},
        })
        after = c.get(f"/schedules/{sid}/fires").json()["fires"]
        kept = [f for f in after if f["fire_key"] in before]
        print(f"old-version confirmed fires retained: {[(f['fire_key'][-8:], f['status']) for f in kept]}")
        print("new-version fires use :v2: identities; old pending fires were cancelled")
        ev = c.get(f"/audit?schedule_id={sid}&event=schedule.updated").json()["events"][0]
        print(f"audit: {ev['event']} detail={ev['detail']}")

    repo.close()
    print("\nDemo complete.\n")


if __name__ == "__main__":
    sys.exit(main())
