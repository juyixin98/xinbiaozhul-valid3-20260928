"""时区工具：强制使用锁定版本的 pip `tzdata` 包，保证本地/生产/测试一致。

系统 zoneinfo 可能随 OS 更新静默改变 DST 规则，从而破坏“UTC 身份稳定”不变量，
因此只要安装了 tzdata 包，就把它放在 TZPATH 最前；并记录 tzdata 版本（存库、入审计）。
"""
from __future__ import annotations

import functools
from importlib import metadata
from pathlib import Path
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

import zoneinfo

_FORCED = False


def _force_pip_tzdata() -> None:
    global _FORCED
    if _FORCED:
        return
    try:
        import tzdata  # type: ignore

        tz_dir = str(Path(tzdata.__file__).parent / "zoneinfo")
        paths = list(zoneinfo.TZPATH)
        if tz_dir not in paths:
            zoneinfo.reset_tzpath((tz_dir, *paths))
    except ImportError:
        # 未安装 tzdata（不应发生：已锁定依赖）；退回系统 tzdata
        pass
    _FORCED = True


@functools.lru_cache(maxsize=1)
def tzdata_version() -> str:
    try:
        return metadata.version("tzdata")
    except metadata.PackageNotFoundError:
        try:
            z = ZoneInfo("UTC")
            return "system-tzdata"
        except Exception:  # pragma: no cover
            return "unknown"


@functools.lru_cache(maxsize=256)
def get_zone(name: str) -> ZoneInfo:
    _force_pip_tzdata()
    try:
        return ZoneInfo(name)
    except (ZoneInfoNotFoundError, ValueError, Exception) as exc:  # noqa: BLE001
        from app.errors import TimezoneUnknown

        raise TimezoneUnknown(f"unknown IANA timezone: {name!r}") from exc
