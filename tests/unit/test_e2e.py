"""端到端：创建 → tick → 物化 → 到点执行 → 审计。打印 run/trigger/version/序号。"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

from tests.conftest import create_schedule, tick


def test_create_tick_execute_audit(client, fake_clock):
    sid = create_schedule(client, timezone="UTC", rrule="FREQ=HOURLY",
                          start_at="2026-01-01T00:00:00")

    # 第一个 tick（now=00:00）：物化 + 执行整点那一次
    r = tick(client, now=fake_clock.now(), schedule_id=sid)
    print("\n[run 标识]", r["run_id"], "observed_now", r["observed_now"], "status", r["status"])
    assert r["status"] == "OK"
    st = r["schedules"][0]
    print("[阶段计数]", st)
    assert st["inserted"] > 0 and st["succeeded"] == 1

    trig = client.get(f"/api/v1/schedules/{sid}/triggers?status=SUCCEEDED").json()["items"]
    assert len(trig) == 1
    t0 = trig[0]
    print("[trigger]", t0["id"], "version", t0["schedule_version"],
          "ordinal", t0["occurrence_no"], "local", t0["local_wall"],
          "utc", t0["due_at_utc"], "status", t0["status"])

    # 执行尝试审计
    execs = client.get(f"/api/v1/triggers/{t0['id']}/executions").json()["items"]
    assert len(execs) == 1 and execs[0]["status"] == "SUCCEEDED"

    # 时钟推进 1 小时，再 tick：执行第二次（身份不同）
    fake_clock.advance(3600)
    r2 = tick(client, now=fake_clock.now(), schedule_id=sid)
    assert r2["schedules"][0]["succeeded"] == 1

    # runs 与 audit 可读
    runs = client.get("/api/v1/runs").json()["items"]
    assert len(runs) == 2
    audit = client.get(f"/api/v1/audit?entity_id={sid}").json()["items"]
    actions = {a["action"] for a in audit}
    print("[audit actions]", sorted(actions))
    assert "MATERIALIZE_RECONCILE" in actions


def test_paused_schedule_materializes_but_does_not_fire(client, fake_clock):
    sid = create_schedule(client, timezone="UTC", rrule="FREQ=HOURLY",
                          start_at="2026-01-01T00:00:00")
    client.post(f"/api/v1/schedules/{sid}/pause")
    r = tick(client, now=fake_clock.now(), schedule_id=sid)
    st = r["schedules"][0]
    assert st["inserted"] > 0 and st["succeeded"] == 0
    pending = client.get(f"/api/v1/schedules/{sid}/triggers?status=PENDING").json()["items"]
    assert any(t["due_at_utc"] is not None for t in pending)
