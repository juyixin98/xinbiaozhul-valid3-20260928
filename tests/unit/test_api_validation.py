"""每个拒绝条件一个可断言测试（错误码 + HTTP 状态）。"""
from __future__ import annotations

from tests.conftest import create_schedule, make_spec_dict


def _post(client, **overrides):
    return client.post("/api/v1/schedules", json=make_spec_dict(**overrides))


def test_invalid_timezone(client):
    r = _post(client, timezone="Not/AZone")
    assert r.status_code == 422 and r.json()["error"]["code"] == "TIMEZONE_UNKNOWN"


def test_rrule_parse_error(client):
    r = _post(client, rrule="FREQ=DAILYY")
    assert r.status_code == 422 and r.json()["error"]["code"] == "RRULE_PARSE_ERROR"


def test_rrule_malformed(client):
    r = _post(client, rrule="FREQ DAILY")
    assert r.status_code == 422 and r.json()["error"]["code"] == "RRULE_PARSE_ERROR"


def test_unsupported_token_bysetpos(client):
    r = _post(client, rrule="FREQ=MONTHLY;BYSETPOS=1;BYDAY=MO")
    assert r.status_code == 422 and r.json()["error"]["code"] == "UNSUPPORTED_RRULE"


def test_unsupported_token_byhour(client):
    r = _post(client, rrule="FREQ=DAILY;BYHOUR=9")
    assert r.status_code == 422 and r.json()["error"]["code"] == "UNSUPPORTED_RRULE"


def test_dtstart_in_rrule_rejected(client):
    r = _post(client, rrule="DTSTART=20260101T090000;FREQ=DAILY")
    assert r.status_code == 422 and r.json()["error"]["code"] == "POLICY_CONFLICT"


def test_count_and_until_conflict(client):
    r = _post(client, rrule="FREQ=DAILY;COUNT=3;UNTIL=20261231")
    assert r.status_code == 422 and r.json()["error"]["code"] == "POLICY_CONFLICT"


def test_yearly_byday_requires_bymonth(client):
    r = _post(client, rrule="FREQ=YEARLY;BYDAY=MO")
    assert r.status_code == 422 and r.json()["error"]["code"] == "POLICY_CONFLICT"


def test_rrule_too_dense(client):
    r = _post(client, rrule="FREQ=SECONDLY", start_at="2026-03-01T09:00:00",
              timezone="UTC")
    assert r.status_code == 422 and r.json()["error"]["code"] == "RRULE_TOO_DENSE"


def test_invalid_exception_date(client):
    r = _post(client, exceptions={"not-a-date": "SKIP"})
    assert r.status_code == 422 and r.json()["error"]["code"] == "INVALID_EXCEPTION_DATE"


def test_exception_ambiguous_fold(client):
    r = _post(client, start_at="2026-11-01T01:30:00",
              exceptions={"2026-11-01 01:30:00": "FORCE"})
    assert r.status_code == 422 and r.json()["error"]["code"] == "EXCEPTION_AMBIGUOUS_FOLD"


def test_exception_conflict_duplicate(client):
    r = _post(client, start_at="2026-11-02T01:30:00",
              exceptions={"2026-11-02 01:30:00": "FORCE"})
    assert r.status_code == 422 and r.json()["error"]["code"] == "EXCEPTION_CONFLICT"


def test_force_on_gap_conflict(client):
    r = _post(client, start_at="2026-03-07T02:30:00",
              exceptions={"2026-03-08 02:30:00": "FORCE"})
    assert r.status_code == 422 and r.json()["error"]["code"] == "EXCEPTION_CONFLICT"


def test_bad_http_action_url(client):
    r = _post(client, action={"type": "http", "url": "ftp://example.com/x"})
    assert r.status_code == 422 and r.json()["error"]["code"] == "POLICY_CONFLICT"


def test_start_at_must_be_naive(client):
    r = _post(client, start_at="2026-03-01T09:00:00+08:00")
    assert r.status_code == 422 and r.json()["error"]["code"] == "VALIDATION_ERROR"


def test_not_found(client):
    r = client.get("/api/v1/schedules/00000000-0000-0000-0000-000000000000")
    assert r.status_code == 404 and r.json()["error"]["code"] == "NOT_FOUND"


def test_version_conflict_on_update(client):
    sid = create_schedule(client)
    r = client.put(f"/api/v1/schedules/{sid}",
                   json={"expected_version": 99, "spec": make_spec_dict()})
    assert r.status_code == 409 and r.json()["error"]["code"] == "VERSION_CONFLICT"
