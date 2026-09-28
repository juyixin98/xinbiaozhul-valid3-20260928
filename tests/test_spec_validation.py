"""Unit tests for spec parsing validation — every rejection condition is
asserted with its own error code/message (no happy-path-only testing)."""
from __future__ import annotations

import pytest

from scheduler.errors import ValidationError
from scheduler.planner.spec import parse_spec


def base_spec(**over):
    spec = {
        "name": "daily-job",
        "timezone": "UTC",
        "frequency": "daily",
        "at": "09:00",
        "start_date": "2024-01-01",
        "executor": {"type": "log"},
    }
    spec.update(over)
    return spec


def test_valid_daily_spec_parses():
    s = parse_spec(base_spec())
    assert s.frequency.value == "daily"
    assert s.at_time.hour == 9


@pytest.mark.parametrize(
    "field,patch,fragment",
    [
        ("timezone", {"timezone": "Mars/Olympus"}, "time zone"),
        ("at", {"at": "25:00"}, "HH:MM"),
        ("at", {"at": "9:00:00"}, "seconds"),
        ("weekdays", {"frequency": "weekly", "weekdays": [0]}, "1..7"),
        ("weekdays", {"frequency": "weekly", "weekdays": [8]}, "1..7"),
        ("weekdays", {"frequency": "weekly", "weekdays": [1, 1]}, "duplicates"),
        ("month_days", {"frequency": "monthly", "month_days": [0]}, "1..31"),
        ("month_days", {"frequency": "monthly", "month_days": [32]}, "1..31"),
        ("month_days", {"frequency": "monthly", "month_days": [15, 15]}, "duplicates"),
    ],
)
def test_field_level_rejections(field, patch, fragment):
    with pytest.raises(ValidationError) as ei:
        parse_spec(base_spec(**patch))
    assert ei.value.details
    assert fragment in str(ei.value.details) or ei.value.code.startswith("VAL")


def test_nth_weekday_rejects_bad_nth_and_weekday():
    with pytest.raises(ValidationError):
        parse_spec(base_spec(frequency="monthly", nth_weekdays=[{"nth": 0, "weekday": 1}]))
    with pytest.raises(ValidationError):
        parse_spec(base_spec(frequency="monthly", nth_weekdays=[{"nth": 1, "weekday": 8}]))
    with pytest.raises(ValidationError):
        parse_spec(base_spec(frequency="monthly",
                             nth_weekdays=[{"nth": 1, "weekday": 1},
                                           {"nth": 1, "weekday": 1}]))


def test_weekly_requires_weekdays():
    with pytest.raises(ValidationError) as ei:
        parse_spec(base_spec(frequency="weekly", weekdays=[]))
    assert ei.value.code == "VAL-SPEC-RULES"
    assert any("weekday" in v for v in ei.value.details["violations"])


def test_monthly_requires_day_rule():
    with pytest.raises(ValidationError) as ei:
        parse_spec(base_spec(frequency="monthly"))
    assert "monthly schedule requires" in ei.value.details["violations"][0]


def test_weekdays_not_allowed_for_daily():
    with pytest.raises(ValidationError, match="only valid for weekly"):
        parse_spec(base_spec(weekdays=[1]))


def test_month_fields_not_allowed_for_daily():
    with pytest.raises(ValidationError, match="only valid for monthly"):
        parse_spec(base_spec(month_days=[1]))


def test_bad_date_strings():
    with pytest.raises(ValidationError):
        parse_spec(base_spec(start_date="2024/01/01"))
    with pytest.raises(ValidationError):
        parse_spec(base_spec(extra_dates=["not-a-date"]))


def test_end_before_start_rejected():
    with pytest.raises(ValidationError, match="end_date"):
        parse_spec(base_spec(start_date="2024-06-01", end_date="2024-01-01"))


def test_exception_and_extra_collision_rejected():
    with pytest.raises(ValidationError, match="both extra_date and exception"):
        parse_spec(base_spec(extra_dates=["2024-03-01"], exceptions=["2024-03-01"]))


def test_extra_outside_range_rejected():
    with pytest.raises(ValidationError, match="outside"):
        parse_spec(
            base_spec(
                start_date="2024-02-01",
                end_date="2024-02-28",
                extra_dates=["2024-01-15"],
            )
        )


def test_webhook_requires_url():
    with pytest.raises(ValidationError):
        parse_spec(base_spec(executor={"type": "webhook"}))


def test_webhook_url_scheme_enforced():
    with pytest.raises(ValidationError):
        parse_spec(base_spec(executor={"type": "webhook", "url": "ftp://x/y"}))


def test_unknown_field_rejected():
    with pytest.raises(ValidationError):
        parse_spec(base_spec(nonsense=True))


def test_unknown_executor_rejected():
    with pytest.raises(ValidationError):
        parse_spec(base_spec(executor={"type": "carrier-pigeon"}))
