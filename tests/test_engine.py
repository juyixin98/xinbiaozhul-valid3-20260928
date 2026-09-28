"""Integration: engine ticks, persistence, identity, versioning, recovery.

Uses the real PostgreSQL test database and pinned virtual clock. Logs print
request specs, run/fire identifiers and decision steps so a failure trace
explains what was scheduled and why.
"""
from __future__ import annotations

from datetime import datetime, timezone

import pytest

from scheduler.errors import ConflictError

UTC = timezone.utc
T0 = datetime(2024, 3, 1, 12, 0, tzinfo=UTC)  # before test fires


def daily_ny(at="09:00", **over):
    body = {
        "name": "ny-daily", "timezone": "America/New_York", "frequency": "daily",
        "at": at, "start_date": "2024-03-01", "end_date": "2024-12-31",
        "executor": {"type": "log"},
    }
    body.update(over)
    return body


# ── happy path: UTC unique identity, one fire per identity ──────────────────

def test_daily_fire_has_unique_utc_identity_and_fires_once(engine, repo):
    # short window starting today-local: no backlog, exactly one due fire
    sch = engine.create_schedule(
        daily_ny(at="09:00", start_date="2024-03-11", end_date="2024-03-12"),
        now=datetime(2024, 3, 11, 12, 0, tzinfo=UTC),
    )
    # 2024-03-11 09:00 NY = 13:00 UTC (EDT)
    res1 = engine.tick(now=datetime(2024, 3, 11, 13, 0, tzinfo=UTC))
    res2 = engine.tick(now=datetime(2024, 3, 11, 13, 0, 30, tzinfo=UTC))
    succeeded_keys = [o["fire_key"] for o in res1["succeeded"]]
    assert len(succeeded_keys) == 1
    assert res2["succeeded"] == []
    fires = repo.list_fires(sch["id"])
    target = [f for f in fires if f["due_utc"].startswith("2024-03-11T13:00")]
    assert len(target) == 1
    assert target[0]["status"] == "succeeded"
    assert target[0]["attempts"] == 1
    print(f"\n[identity] fire={target[0]['fire_key']} run={target[0]}")


def test_fall_back_night_fires_twice_with_distinct_run_ids(engine, repo):
    sch = engine.create_schedule(
        daily_ny(at="01:30", start_date="2024-11-03", end_date="2024-11-03"),
        now=datetime(2024, 11, 3, tzinfo=UTC),
    )
    res = engine.tick(now=datetime(2024, 11, 3, 7, 0, tzinfo=UTC))
    keys = sorted(o["fire_key"] for o in res["succeeded"])
    assert len(keys) == 2
    assert keys[0].endswith(":f0") and keys[1].endswith(":f1")
    runs = repo.list_runs(sch["id"])
    assert len({r["id"] for r in runs}) == 2
    print(f"\n[fall-back] run ids={sorted(r['id'] for r in runs)}")


def test_spring_forward_gap_is_skipped_in_persisted_plan(engine, repo):
    sch = engine.create_schedule(daily_ny(at="02:30"), now=T0)
    audit = repo.list_audit(schedule_id=sch["id"], event="plan.skipped-gap")
    assert any(
        e["detail"]["date_local"] == "2024-03-10" for e in audit
    ), [e["detail"] for e in audit]
    fires = repo.list_fires(sch["id"])
    assert not any(f["date_local"] == "2024-03-10" for f in fires)
    print(f"\n[spring-gap audit] {[e['detail'] for e in audit][:2]}")


# ── editing a plan does not re-fire history ─────────────────────────────────

def test_plan_update_cancels_pending_but_keeps_history(engine, repo):
    sch = engine.create_schedule(
        daily_ny(at="09:00", start_date="2024-03-11", end_date="2024-03-20"),
        now=datetime(2024, 3, 11, 12, 0, tzinfo=UTC),
    )
    engine.tick(now=datetime(2024, 3, 11, 13, 0, tzinfo=UTC))
    fires_before = {f["fire_key"]: f["status"] for f in repo.list_fires(sch["id"])}
    done = [k for k, s in fires_before.items() if s == "succeeded"]
    assert len(done) == 1

    # edit time -> version 2 (keep the same short window)
    updated = engine.update_schedule(
        sch["id"],
        daily_ny(at="10:00", start_date="2024-03-11", end_date="2024-03-20"),
        now=datetime(2024, 3, 12, tzinfo=UTC),
    )
    assert updated["version"] == 2
    audit = repo.list_audit(schedule_id=sch["id"], event="schedule.updated")
    assert audit[0]["detail"]["new_version"] == 2

    fires_after = repo.list_fires(sch["id"])
    old_done = [f for f in fires_after if f["version"] == 1 and f["status"] == "succeeded"]
    assert [f["fire_key"] for f in old_done] == done
    old_pending = [f for f in fires_after if f["version"] == 1 and f["status"] != "cancelled"]
    # everything old and unfinished must have been cancelled (history kept)
    assert all(f["status"] == "succeeded" for f in old_pending), old_pending

    # tick exactly on the v2 Mar 12 due instant: no catch-up backlog should
    # starve the on-time v2 fire.
    res = engine.tick(now=datetime(2024, 3, 12, 14, 0, tzinfo=UTC))
    fired_v2 = [o for o in res["succeeded"] if ":v2:" in o["fire_key"]]
    on_time_v2 = [o for o in fired_v2 if ":base:1:f0" in o["fire_key"]]
    assert len(on_time_v2) == 1, fired_v2
    assert not any(":v1:" in o["fire_key"] for o in res["succeeded"])
    print(f"\n[versioning] kept={done} on_time_v2={on_time_v2}")


def test_timezone_change_recomputes_times_without_history_refire(engine, repo):
    sch = engine.create_schedule(
        daily_ny(at="09:00", start_date="2024-03-11", end_date="2024-03-20"),
        now=datetime(2024, 3, 11, 12, 0, tzinfo=UTC),
    )
    engine.tick(now=datetime(2024, 3, 11, 13, 0, tzinfo=UTC))
    engine.update_schedule(
        sch["id"],
        daily_ny(timezone="Asia/Shanghai"),
        now=datetime(2024, 3, 12, tzinfo=UTC),
    )
    fires = repo.list_fires(sch["id"])
    v2 = [f for f in fires if f["version"] == 2 and f["date_local"] == "2024-03-12"]
    assert len(v2) == 1
    assert v2[0]["due_utc"] == "2024-03-12T01:00:00+00:00"  # 09:00 SH = 01:00 UTC
    v1_done = [f for f in fires if f["version"] == 1 and f["status"] == "succeeded"]
    assert len(v1_done) == 1
    print(f"\n[tz-change] v2 due={v2[0]['due_utc']} (local->UTC step printed)")


# ── downtime catch-up: bounded, deterministic order ─────────────────────────

def test_downtime_catchup_is_bounded_and_in_deterministic_order(engine, repo):
    sch = engine.create_schedule(daily_ny(at="23:00"), now=T0)
    # simulate downtime: last successful observation was Mar 5 23:00-ish UTC,
    # then worker is back on Mar 9. max_backfill=5 in test settings, but
    # on-time window also matters. Construct 8 missed fires:
    res = engine.tick(now=datetime(2024, 3, 10, 4, 0, tzinfo=UTC))
    fired = [(o["fire_key"], o["status"]) for o in res["succeeded"]]
    print(f"\n[catch-up] first tick fired={fired} remaining={res['backfill_remaining']}")
    # 8 missed nightly fires Mar2..Mar9. On-time fire (Mar9 ordinal 8, within
    # the 60s horizon) is delivered first so backlog never starves it;
    # then 5 oldest catch-up fires (budget=5) in ascending due order.
    assert res["catch_up"] == 5
    assert res["on_time"] == 1
    assert len(fired) == 6
    keys = [k for k, _ in fired]
    assert keys[0].endswith(":base:8:f0")  # on-time delivered first
    catchup_keys = keys[1:]
    assert catchup_keys == sorted(catchup_keys, key=lambda k: _due_key(repo, k))
    assert res["backfill_remaining"] >= 2

    # next tick drains the remaining backlog in ascending order
    res2 = engine.tick(now=datetime(2024, 3, 10, 4, 0, 1, tzinfo=UTC))
    fired2 = [o["fire_key"] for o in res2["succeeded"]]
    assert len(fired2) == 2
    assert res2["backfill_remaining"] == 0
    assert fired2 == sorted(fired2, key=lambda k: _due_key(repo, k))
    runs = repo.list_runs(sch["id"])
    assert len({r["fire_key"] for r in runs}) == 8
    print(f"[catch-up] second tick drained={fired2}")


def _due_key(repo, fire_key):
    f = repo.get_fire_by_key(fire_key)
    return f["due_utc"]


def test_very_old_misses_expire_instead_of_running(engine, repo):
    sch = engine.create_schedule(daily_ny(at="09:00"), now=T0)
    # backfill_window_days=7: a fire 10 days old is expired, recent one fires
    res = engine.tick(now=datetime(2024, 3, 20, 12, 0, tzinfo=UTC))
    fired_dates = sorted(
        o["fire_key"] for o in res["succeeded"]
    )
    fires = repo.list_fires(sch["id"])
    expired = [f for f in fires if f["status"] == "expired"]
    # earliest fires (Mar 2..Mar 12, >7 days before Mar 20 12:00) expired
    assert expired, "expected some fires to be past the catch-up window"
    assert all(f["due_utc"] < "2024-03-13T12:00:00" for f in expired)
    audit = repo.list_audit(schedule_id=sch["id"], event="fire.expired")
    assert len(audit) == len(expired)
    print(f"\n[expire] expired={len(expired)} fired_now={fired_dates}")


# ── clock rollback ──────────────────────────────────────────────────────────

def test_clock_rollback_is_rejected_and_does_not_refire(engine, repo):
    sch = engine.create_schedule(
        daily_ny(at="09:00", start_date="2024-03-11", end_date="2024-03-12"),
        now=datetime(2024, 3, 11, 12, 0, tzinfo=UTC),
    )
    now1 = datetime(2024, 3, 11, 13, 0, tzinfo=UTC)
    engine.tick(now=now1)
    # same instant refused
    with pytest.raises(ConflictError) as ei:
        engine.tick(now=now1)
    assert ei.value.code == "STATE-CLOCK-ROLLBACK"
    # earlier instant refused
    with pytest.raises(ConflictError):
        engine.tick(now=datetime(2024, 3, 11, 12, 0, tzinfo=UTC))
    # later instant runs, but the confirmed fire is not re-executed
    res = engine.tick(now=datetime(2024, 3, 11, 14, 0, tzinfo=UTC))
    assert res["succeeded"] == []
    fires = [f for f in repo.list_fires(sch["id"]) if f["status"] == "succeeded"]
    assert len(fires) == 1 and fires[0]["attempts"] == 1
    print(f"\n[rollback] rejected + attempts stayed {fires[0]['attempts']}")


# ── pause/resume, retry, failure classification ─────────────────────────────

def test_paused_schedule_does_not_fire(engine, repo):
    sch = engine.create_schedule(
        daily_ny(at="09:00", start_date="2024-03-11", end_date="2024-03-12"),
        now=datetime(2024, 3, 11, 12, 0, tzinfo=UTC),
    )
    engine.set_paused(sch["id"], True, now=T0)
    res = engine.tick(now=datetime(2024, 3, 11, 13, 0, tzinfo=UTC))
    assert res["succeeded"] == []
    fires = repo.list_fires(sch["id"])
    assert all(f["status"] == "planned" for f in fires)
    # resume: the due fire becomes eligible catch-up (ordered/bounded)
    engine.set_paused(sch["id"], False, now=datetime(2024, 3, 11, 13, 0, 1, tzinfo=UTC))
    res2 = engine.tick(now=datetime(2024, 3, 11, 13, 0, 2, tzinfo=UTC))
    assert len(res2["succeeded"]) == 1
    # double pause is a state conflict (409 class)
    engine.set_paused(sch["id"], True, now=datetime(2024, 3, 11, 13, 1, tzinfo=UTC))
    with pytest.raises(ConflictError) as ei:
        engine.set_paused(sch["id"], True, now=datetime(2024, 3, 11, 13, 2, tzinfo=UTC))
    assert ei.value.http_status == 409


def test_failed_delivery_marked_failed_and_manual_retry_succeeds(engine, repo):
    from tests.conftest import FlakyExecutor

    flaky = FlakyExecutor(fail_times=1)
    engine.executors["log"] = flaky
    engine.create_schedule(
        daily_ny(at="09:00", start_date="2024-03-11", end_date="2024-03-12"),
        now=datetime(2024, 3, 11, 12, 0, tzinfo=UTC),
    )
    res = engine.tick(now=datetime(2024, 3, 11, 13, 0, tzinfo=UTC))
    assert len(res["failed"]) == 1 and res["failed"][0]["error_code"] == "RUN-FAILED"
    fk = res["failed"][0]["fire_key"]
    fire = repo.get_fire_by_key(fk)
    assert fire["status"] == "failed"
    # cannot retry a non-terminal or succeed-confirmed fire (checked separately)
    engine.retry_fire(fk, now=datetime(2024, 3, 11, 14, 0, tzinfo=UTC))
    res2 = engine.tick(now=datetime(2024, 3, 11, 14, 0, 1, tzinfo=UTC))
    assert any(o["fire_key"] == fk and o["status"] == "succeeded"
               for o in res2["succeeded"])
    print(f"\n[retry] {fk} failed -> retried -> succeeded")


def test_succeeded_fire_cannot_be_retried(engine):
    engine.create_schedule(
        daily_ny(at="09:00", start_date="2024-03-11", end_date="2024-03-12"),
        now=datetime(2024, 3, 11, 12, 0, tzinfo=UTC),
    )
    res = engine.tick(now=datetime(2024, 3, 11, 13, 0, tzinfo=UTC))
    fk = res["succeeded"][0]["fire_key"]
    with pytest.raises(ConflictError) as ei:
        engine.retry_fire(fk, now=datetime(2024, 3, 11, 14, 0, tzinfo=UTC))
    assert ei.value.code == "STATE-ALREADY-CONFIRMED"


# ── leap day across recovery ────────────────────────────────────────────────

def test_leap_day_present_in_persistent_plan(engine, repo):
    sch_leap = engine.create_schedule(
        daily_ny(at="12:00", start_date="2024-02-28", end_date="2024-03-02"),
        now=datetime(2024, 2, 27, tzinfo=UTC),
    )
    fires = sorted(repo.list_fires(sch_leap["id"]), key=lambda f: f["date_local"])
    assert [f["date_local"] for f in fires] == [
        "2024-02-28", "2024-02-29", "2024-03-01", "2024-03-02",
    ]
    print(f"\n[leap-day] fires={[f['date_local'] + '->' + f['due_utc'] for f in fires]}")
