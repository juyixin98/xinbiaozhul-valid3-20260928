"""触发身份稳定性与编辑：改计划不重触历史；终态封存；flip-flop 可 rearm。"""
from __future__ import annotations

from datetime import datetime, timezone

from tests.conftest import create_schedule, make_spec_dict, tick


def _ids(items, status):
    return {(t["due_at_utc"], t["status"]) for t in items if t["status"] == status}


def test_edit_does_not_retrigger_history(client, fake_clock):
    # 每天 09:00 UTC；在 2026-01-01 09:00 tick 执行第一次
    sid = create_schedule(client, timezone="UTC", rrule="FREQ=DAILY",
                          start_at="2026-01-01T09:00:00")
    fake_clock.set(datetime(2026, 1, 1, 9, 0, tzinfo=timezone.utc))
    tick(client, now=fake_clock.now(), schedule_id=sid)
    succeeded_before = client.get(
        f"/api/v1/schedules/{sid}/triggers?status=SUCCEEDED").json()["items"]
    assert len(succeeded_before) == 1
    hist_id = succeeded_before[0]["id"]
    hist_due = succeeded_before[0]["due_at_utc"]

    # 改计划：时间改成 08:00（未来身份全变）
    new_spec = make_spec_dict(timezone="UTC", rrule="FREQ=DAILY",
                              start_at="2026-01-01T08:00:00")
    r = client.put(f"/api/v1/schedules/{sid}",
                   json={"expected_version": 1, "spec": new_spec})
    assert r.status_code == 200 and r.json()["version"] == 2

    # 推进到 01-02 08:00 多次 tick
    fake_clock.set(datetime(2026, 1, 2, 8, 0, tzinfo=timezone.utc))
    tick(client, now=fake_clock.now(), schedule_id=sid)

    all_t = client.get(f"/api/v1/schedules/{sid}/triggers?limit=2000").json()["items"]
    # 历史成功行身份不变、仍 SUCCEEDED、只执行一次
    hist = [t for t in all_t if t["id"] == hist_id]
    assert len(hist) == 1 and hist[0]["status"] == "SUCCEEDED"
    assert hist[0]["due_at_utc"] == hist_due
    # 09:00 系列的未来 PENDING 被 SUPERSEDED
    assert any(t["status"] == "CANCELLED" and t["skip_reason"] == "SUPERSEDED"
               for t in all_t)
    # 同一历史 UTC 瞬间不存在第二个触发器
    same_due = [t for t in all_t if t["due_at_utc"] == hist_due]
    assert len(same_due) == 1

    # 审计里存在不可变的两个版本
    revs = client.get(f"/api/v1/audit?entity_id={sid}").json()["items"]
    assert any(a["action"] == "SCHEDULE_UPDATED" for a in revs)


def test_timezone_edit_preserves_history_rebases_future(client, fake_clock):
    sid = create_schedule(client, timezone="America/New_York", rrule="FREQ=DAILY",
                          start_at="2026-01-01T09:00:00")
    # 09:00 NY = 14:00 UTC（1 月 EST）
    fake_clock.set(datetime(2026, 1, 1, 14, 0, tzinfo=timezone.utc))
    tick(client, now=fake_clock.now(), schedule_id=sid)
    hist = client.get(f"/api/v1/schedules/{sid}/triggers?status=SUCCEEDED").json()["items"]
    assert len(hist) == 1 and hist[0]["due_at_utc"] == "2026-01-01T14:00:00+00:00"
    hist_id = hist[0]["id"]

    # 改时区为 Chicago（同一墙钟 09:00 现在对应 15:00 UTC）
    new_spec = make_spec_dict(timezone="America/Chicago", rrule="FREQ=DAILY",
                              start_at="2026-01-01T09:00:00")
    assert client.put(f"/api/v1/schedules/{sid}",
                      json={"expected_version": 1, "spec": new_spec}).status_code == 200
    fake_clock.set(datetime(2026, 1, 2, 15, 0, tzinfo=timezone.utc))
    tick(client, now=fake_clock.now(), schedule_id=sid)

    all_t = client.get(f"/api/v1/schedules/{sid}/triggers?limit=2000").json()["items"]
    # 纽约的历史不重触
    assert [t for t in all_t if t["id"] == hist_id][0]["status"] == "SUCCEEDED"
    # 芝加哥 01-02 09:00 = 15:00 UTC 已成功
    assert any(t["status"] == "SUCCEEDED" and t["due_at_utc"] == "2026-01-02T15:00:00+00:00"
               for t in all_t)


def test_flipflop_identity_is_rearmed(client, fake_clock):
    # 09:00 与 10:00 两个规格来回切换：未执行的 CANCELLED 身份应能重新武装
    sid = create_schedule(client, timezone="UTC", rrule="FREQ=DAILY",
                          start_at="2026-01-01T09:00:00")
    fake_clock.set(datetime(2026, 1, 1, 9, 0, tzinfo=timezone.utc))
    tick(client, now=fake_clock.now(), schedule_id=sid)

    spec08 = make_spec_dict(timezone="UTC", rrule="FREQ=DAILY",
                            start_at="2026-01-01T08:00:00")
    client.put(f"/api/v1/schedules/{sid}", json={"expected_version": 1, "spec": spec08})
    fake_clock.set(datetime(2026, 1, 3, 0, 0, tzinfo=timezone.utc))
    tick(client, now=fake_clock.now(), schedule_id=sid)
    cancelled = client.get(
        f"/api/v1/schedules/{sid}/triggers?status=CANCELLED&limit=2000").json()["items"]
    assert cancelled  # 09:00 未来行被取消

    spec09 = make_spec_dict(timezone="UTC", rrule="FREQ=DAILY",
                            start_at="2026-01-01T09:00:00")
    client.put(f"/api/v1/schedules/{sid}", json={"expected_version": 2, "spec": spec09})
    tick(client, now=fake_clock.now(), schedule_id=sid)
    # 09:00 的未来身份回到 PENDING（rearm），审计记录 TRIGGER_REARMED
    rearmed = client.get(
        f"/api/v1/audit?entity_id={sid}").json()["items"]
    assert any(a["action"] == "TRIGGER_REARMED" for a in rearmed)
