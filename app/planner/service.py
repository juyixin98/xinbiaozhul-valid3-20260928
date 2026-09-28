"""计划服务层（纯函数）：校验规格 → 生成理论 Occurrence 流。

只依赖 planner 内的纯模块；不接触数据库。所有“未定义/未收敛”都抛 PlannerInternalError，
由上层映射为 500 / run 状态非 OK，绝不包装成功。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from uuid import UUID

from app.errors import (
    ExceptionConflict,
    PlannerInternalError,
    PlannerNonMonotonic,
    PolicyConflict,
    RRuleTooDense,
)
from app.planner import identity
from app.planner.calendar import (
    ParsedExceptions,
    PlannedWall,
    find_skip,
    fold_skip_sel,
    parse_exceptions,
    validate_exceptions_against_tz,
)
from app.planner.localize import (
    FoldPolicy,
    GapPolicy,
    Resolution,
    assert_roundtrip,
    resolve_wall,
)
from app.planner.rrule import RRuleSpec, iter_walls, parse_rrule, validate
from app.planner.tzutil import get_zone, tzdata_version
from app.schemas import ScheduleSpec

UTC = timezone.utc

# Occurrence 状态（理论层面）
O_SKIPPED_GAP = "SKIP_GAP"
O_SKIPPED_MISSING = "SKIP_MISSING"
O_SKIPPED_EXCEPTION = "SKIP_EXCEPTION"
O_FIRE = "FIRE"
O_FORCE = "FORCE"


@dataclass(frozen=True)
class Occurrence:
    ordinal: int                     # rrule 理论序号；FORCE 新增为负的确定性序号
    local_wall: datetime             # naive，存储用合法墙钟（missing 时为钳制值）
    requested_ymd: tuple[int, int, int] | None
    due_at: datetime | None          # UTC aware；跳过类为 None
    kind: str                        # FIRE | FORCE | SKIP_GAP | SKIP_MISSING | SKIP_EXCEPTION
    resolution_kind: str | None      # NORMAL/FOLD/GAP/GAP_SHIFTED
    resolved_fold: int | None
    resolved_wall: datetime | None
    synthetic_key: str | None        # g:/m:/f: 合成键
    id_name: str                     # 身份名（t:/g:/m:/f:）
    exception_key: str | None
    utc_offset_minutes: int | None


@dataclass(frozen=True)
class Plan:
    occurrences: tuple[Occurrence, ...]
    warnings: tuple[str, ...]
    tzdata_version: str
    merged: tuple[dict, ...]
    skipped: int
    fireable: int


@dataclass(frozen=True)
class PlanRequest:
    schedule_id: UUID
    spec: ScheduleSpec
    window_start_utc: datetime
    window_end_utc: datetime
    max_occurrences: int = 20_000


def _anchor_local(spec: ScheduleSpec) -> datetime:
    return spec.start_at.replace(tzinfo=None)


def _local_window(spec: ScheduleSpec, start_utc: datetime, end_utc: datetime) -> tuple[datetime, datetime]:
    tz = get_zone(spec.timezone)
    # 向两侧各扩 1 天，保证边界日的墙钟完整
    return (
        start_utc.astimezone(tz).replace(tzinfo=None) - timedelta(days=1),
        end_utc.astimezone(tz).replace(tzinfo=None) + timedelta(days=1),
    )


def check_density(spec: ScheduleSpec, max_occurrences: int) -> None:
    """有界游走：在锚点之后的首个 horizon 窗口内计数，超限即拒绝（保证枚举收敛）。"""
    rrule = parse_rrule(spec.rrule)
    anchor = _anchor_local(spec)
    win_end = anchor + timedelta(days=7)
    count = 0
    for iw in iter_walls(rrule, anchor, anchor, win_end):
        count += 1
        if count > max_occurrences:
            raise RRuleTooDense(
                "rrule produces more than "
                f"{max_occurrences} occurrences in the 7-day horizon; narrow the rule",
                {"counted": count, "horizon_days": 7},
            )


def validate_spec(spec: ScheduleSpec) -> RRuleSpec:
    # 显式解析时区，未知 IANA 名在此确定拒绝（不延迟到物化阶段）
    get_zone(spec.timezone)
    rrule = parse_rrule(spec.rrule)
    subdaily = rrule.freq in ("SECONDLY", "MINUTELY", "HOURLY")
    # local_time 取自 start_at 的 time 部分：亚日频禁止（时刻来自序列），DAILY+ 必有（start_at 始终带时间）
    validate(rrule, local_time_present=False if subdaily else True)
    # UNTIL 不得早于锚点
    if rrule.until is not None and rrule.until < _anchor_local(spec):
        raise PolicyConflict("UNTIL is earlier than start_at")
    parsed = parse_exceptions(spec.exceptions)
    validate_exceptions_against_tz(parsed, spec.timezone)
    if spec.action.type == "http":
        _validate_http_url(spec.action.url or "")
    return rrule


def validate_full_plan(spec: ScheduleSpec, schedule_id: UUID, max_occurrences: int,
                       horizon_days: int = 7) -> Plan:
    """在写库前实跑 plan()，使 FORCE 重复/fold 冲突等在创建/更新时即确定拒绝。
    窗口覆盖锚点 horizon 以及全部 FORCE/UNTIL 边界（出现数受密度上限约束）。"""
    tz = get_zone(spec.timezone)
    anchor = _anchor_local(spec)
    parsed = parse_exceptions(spec.exceptions)
    forced_walls = [e.wall for e in parsed.forced if e.wall is not None]
    lo = min([anchor, *forced_walls])
    hi = max([anchor + timedelta(days=horizon_days), *forced_walls,
              *([parse_rrule(spec.rrule).until] if parse_rrule(spec.rrule).until else [])])
    def _to_utc(naive: datetime) -> datetime:
        return naive.replace(tzinfo=tz).astimezone(timezone.utc)
    return plan(PlanRequest(
        schedule_id=schedule_id, spec=spec,
        window_start_utc=_to_utc(lo) - timedelta(days=1),
        window_end_utc=_to_utc(hi) + timedelta(days=1),
        max_occurrences=max_occurrences,
    ))


def _validate_http_url(url: str) -> None:
    from urllib.parse import urlparse

    p = urlparse(url)
    if p.scheme not in ("http", "https") or not p.hostname:
        raise PolicyConflict("action.url must be an absolute http(s) URL with a host", {"url": url})


def _make_force_occurrence(schedule_id: UUID, spec: ScheduleSpec, entry, seq: int) -> Occurrence:
    gap = GapPolicy(spec.gap_policy)
    fold = FoldPolicy(spec.fallback_policy)
    # 精确键带 fold 选择子时强制按选择子解析
    use_fold = fold
    res: Resolution
    if entry.fold_sel == 0:
        res = resolve_wall(entry.wall, spec.timezone, gap, FoldPolicy.EARLIEST)
    elif entry.fold_sel == 1:
        res = resolve_wall(entry.wall, spec.timezone, gap, FoldPolicy.LATEST)
    else:
        res = resolve_wall(entry.wall, spec.timezone, gap, use_fold)
    assert_roundtrip(res, spec.timezone)
    if res.due_at is None:
        raise PlannerInternalError("FORCE occurrence did not resolve to a UTC instant")
    syn = identity.force_name(entry.wall, entry.fold_sel)
    return Occurrence(
        ordinal=-seq, local_wall=entry.wall, requested_ymd=None,
        due_at=res.due_at, kind=O_FORCE, resolution_kind=res.kind,
        resolved_fold=res.resolved_fold, resolved_wall=res.resolved_wall,
        synthetic_key=syn, id_name=identity.real_name(res.due_at),
        exception_key=entry.raw, utc_offset_minutes=res.utc_offset_minutes,
    )


def plan(req: PlanRequest) -> Plan:
    spec = req.spec
    rrule = validate_spec(spec)
    anchor = _anchor_local(spec)
    wl, we = _local_window(spec, req.window_start_utc, req.window_end_utc)

    if anchor > we:
        raise PolicyConflict("start_at is later than the planning window end")

    parsed = parse_exceptions(spec.exceptions)
    gap = GapPolicy(spec.gap_policy)
    fold = FoldPolicy(spec.fallback_policy)

    raw: list[Occurrence] = []
    counted = 0

    def density_guard() -> None:
        nonlocal counted
        counted += 1
        if counted > req.max_occurrences:
            raise RRuleTooDense(
                "occurrence count exceeds max_occurrences in planning window",
                {"max": req.max_occurrences},
            )

    for iw in iter_walls(rrule, anchor, wl, we):
        density_guard()
        raw.append(_build_rrule_occurrence(req.schedule_id, spec, parsed, gap, fold, iw))

    # FORCE 新增出现
    force_seq = 0
    existing_walls = {o.local_wall for o in raw if o.due_at is not None}
    for entry in parsed.forced:
        if entry.wall is None:
            continue  # 仅支持精确时刻 FORCE（日期级 FORCE 语义模糊，下面 warning 提示）
        if not (wl <= entry.wall < we):
            continue
        force_seq += 1
        occ = _make_force_occurrence(req.schedule_id, spec, entry, force_seq)
        if occ.due_at is not None and any(
            o.due_at == occ.due_at for o in raw if o.due_at is not None
        ):
            raise ExceptionConflict(
                f"FORCE exception {entry.raw!r} resolves to a UTC instant already produced "
                "by the rrule; refusing to duplicate it",
                {"key": entry.raw, "due_at": occ.due_at.isoformat()},
            )
        raw.append(occ)

    warnings = list(_compute_warnings(parsed, raw, wl, we))

    # 按 UTC（可触发）排序；合成跳过项按本地墙钟插入，最终序列以 due_at 为准做合并检查
    fireable = [o for o in raw if o.due_at is not None]
    fireable.sort(key=lambda o: (o.due_at, 0 if o.ordinal >= 0 else 1, o.local_wall))
    skipped = [o for o in raw if o.due_at is None]
    skipped.sort(key=lambda o: o.local_wall)

    merged: list[dict] = []
    deduped = _merge_duplicates(fireable, merged)

    _check_monotonic(deduped)

    ordered = tuple(deduped) + tuple(skipped)
    return Plan(
        occurrences=ordered,
        warnings=tuple(warnings),
        tzdata_version=tzdata_version(),
        merged=tuple(merged),
        skipped=len(skipped),
        fireable=len(deduped),
    )


def _build_rrule_occurrence(
    schedule_id, spec: ScheduleSpec, parsed: ParsedExceptions,
    gap: GapPolicy, fold: FoldPolicy, iw,
) -> Occurrence:
    # 缺失日期（如平年 2/29，锚点日路径）
    if iw.missing:
        y, m, d = iw.requested_ymd
        mname = identity.missing_name(y, m, d, iw.wall.hour, iw.wall.minute, iw.wall.second)
        return Occurrence(
            ordinal=iw.ordinal, local_wall=iw.wall, requested_ymd=iw.requested_ymd,
            due_at=None, kind=O_SKIPPED_MISSING, resolution_kind=None,
            resolved_fold=None, resolved_wall=None,
            synthetic_key=mname,
            id_name=mname, exception_key=None,
            utc_offset_minutes=None,
        )

    skip_entry = find_skip(parsed, iw.wall)
    res = resolve_wall(iw.wall, spec.timezone, gap, fold)

    # 日期键 SKIP：两个折叠瞬间都跳过
    # 精确键带选择子：仅跳过被选中的折叠瞬间
    if skip_entry is not None:
        sel = fold_skip_sel(parsed, iw.wall)
        if res.kind == "FOLD" and sel is not None and res.resolved_fold != sel:
            skip_entry = None  # 另一个折叠仍要执行

    if skip_entry is not None:
        return Occurrence(
            ordinal=iw.ordinal, local_wall=iw.wall, requested_ymd=None,
            due_at=None, kind=O_SKIPPED_EXCEPTION, resolution_kind=res.kind,
            resolved_fold=res.resolved_fold, resolved_wall=res.resolved_wall,
            synthetic_key=None, id_name=identity.real_name(res.due_at) if res.due_at else identity.gap_name(iw.wall),
            exception_key=skip_entry.raw, utc_offset_minutes=res.utc_offset_minutes,
        )

    if res.kind == "GAP":
        return Occurrence(
            ordinal=iw.ordinal, local_wall=iw.wall, requested_ymd=None,
            due_at=None, kind=O_SKIPPED_GAP, resolution_kind="GAP",
            resolved_fold=None, resolved_wall=None,
            synthetic_key=identity.gap_name(iw.wall),
            id_name=identity.gap_name(iw.wall), exception_key=None,
            utc_offset_minutes=None,
        )

    assert_roundtrip(res, spec.timezone)
    return Occurrence(
        ordinal=iw.ordinal, local_wall=iw.wall, requested_ymd=None,
        due_at=res.due_at, kind=O_FIRE, resolution_kind=res.kind,
        resolved_fold=res.resolved_fold, resolved_wall=res.resolved_wall,
        synthetic_key=None, id_name=identity.real_name(res.due_at),
        exception_key=None, utc_offset_minutes=res.utc_offset_minutes,
    )


def _merge_duplicates(occurrences: list[Occurrence], merged: list[dict]) -> list[Occurrence]:
    """同 (schedule, due_at) 确定性合并：理论序号最小者胜出，败者入 merged 审计。"""
    out: list[Occurrence] = []
    i = 0
    while i < len(occurrences):
        j = i + 1
        while j < len(occurrences) and occurrences[j].due_at == occurrences[i].due_at:
            j += 1
        group = occurrences[i:j]
        if len(group) == 1:
            out.append(group[0])
        else:
            winner = sorted(group, key=lambda o: (0 if o.ordinal >= 0 else 1, o.ordinal))[0]
            losers = [o for o in group if o is not winner]
            merged.append({
                "due_at": winner.due_at.isoformat(),
                "winner_id_name": winner.id_name,
                "winner_ordinal": winner.ordinal,
                "losers": [
                    {"ordinal": o.ordinal, "wall": o.local_wall.isoformat(),
                     "kind": o.kind, "exception_key": o.exception_key}
                    for o in losers
                ],
            })
            out.append(winner)
        i = j
    return out


def _check_monotonic(occurrences: list[Occurrence]) -> None:
    prev = None
    for o in occurrences:
        if o.due_at is None:
            continue
        if prev is not None and o.due_at < prev:
            raise PlannerNonMonotonic(
                "resolved UTC instants are not monotonic",
                {"prev": prev.isoformat(), "cur": o.due_at.isoformat()},
            )
        prev = o.due_at


def _compute_warnings(parsed: ParsedExceptions, raw, wl, we) -> list[str]:
    warnings: list[str] = []
    fired_walls = {o.local_wall for o in raw}
    for e in parsed.entries:
        if not (wl.date() <= e.day <= we.date()):
            warnings.append(f"exception {e.raw!r} lies outside the planning window")
            continue
        if e.action == "FORCE" and e.is_date_only:
            warnings.append(
                f"FORCE exception {e.raw!r} is date-only; an exact 'YYYY-MM-DD HH:MM:SS' "
                "key is required to add a trigger — entry ignored for firing"
            )
        if not e.is_date_only and e.wall not in fired_walls and e.action == "SKIP":
            warnings.append(f"SKIP exception {e.raw!r} matches no occurrence in the window")
    return warnings
