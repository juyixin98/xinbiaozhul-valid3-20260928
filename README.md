# 持久化周期调度服务（Python · FastAPI · PostgreSQL）

一个可在本地完整复现的纯后端服务：提交"带时区、工作日（RRULE BYDAY）、例外日期"的周期计划，
服务把每次出现解析为**确定的 UTC 时刻并持久化**，在后台 tick 或手动触发时执行动作（`log` / `http`），
并记录每个触发的身份、版本、执行尝试与审计。

核心解决四道题：

1. **夏令时**：春季"本地不存在时间"（gap）与秋季"本地重复时间"（fold）都有明确、可测试的规则。
2. **UTC 唯一身份**：每次触发的身份是 `(schedule_id, UTC 瞬间)` 的纯函数；**改计划不会重触发历史**。
3. **停机补跑有上限且顺序确定**：基于心跳的 recovery epoch、每 tick 速率上限、deadline 与容量公式。
4. **时钟回拨不重复执行**：认领用状态 CAS（`PENDING→RUNNING`）+ `FOR UPDATE SKIP LOCKED`，
   判重依据是身份与状态，而非时间戳。

---

## 1. 快速开始（干净环境）

需要：Docker、Python 3.12（仅用标准库 + 锁定依赖）。

```bash
# 一条命令：venv → 装锁定依赖 → 一次性 PG(5434) → schema → 起服务 → curl 演示 → pytest
bash scripts/verify.sh
```

或分步：

```bash
docker compose up -d                 # PostgreSQL 在 127.0.0.1:5433（compose 默认）
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements.lock
export DATABASE_URL=postgresql://sched:sched@127.0.0.1:5433/scheduler
uvicorn app.main:app --reload        # http://127.0.0.1:8000  ；文档 /docs
```

> 注意：仓库内 `docker-compose.yml` 使用 **5433**；测试与 `scripts/*.sh` 默认用一次性容器的 **5434**，
> 两者互不干扰。可用 `TEST_PORT` / `DATABASE_URL` 覆盖。

只跑测试：`bash scripts/run-tests.sh`（自带一次性 PG；`pytest -s` 会打印本地时间→UTC 判定）。

---

## 2. 计划规格

```json
{
  "name": "daily-0930-newyork",
  "timezone": "America/New_York",
  "rrule": "FREQ=DAILY",
  "start_at": "2026-03-01T09:30:00",
  "exceptions": { "2026-07-04": "SKIP", "2026-03-10 12:00:00": "FORCE" },
  "gap_policy": "SKIP",
  "fallback_policy": "EARLIEST",
  "action": { "type": "log" }
}
```

- `timezone`：IANA 名。强制使用锁定版本的 pip `tzdata` 包（版本随库记录，防止 OS 更新静默改变 DST）。
- `rrule`：自研解析器，支持 `FREQ=SECONDLY|MINUTELY|HOURLY|DAILY|WEEKLY|MONTHLY|YEARLY`、
  `INTERVAL`、`BYDAY`（月度/年度支持 `2MO/-1FR`）、`BYMONTHDAY`（含负数）、`BYMONTH`、`WKST`、`COUNT`、`UNTIL`。
  **不支持的 token 直接 422 拒绝**（`BYSETPOS/BYWEEKNO/BYYEARDAY/BYHOUR/MINUTE/SECOND/...`），绝不静默忽略。
  工作日用 `FREQ=WEEKLY;BYDAY=MO..FR` 表达。
- `start_at`：naive 本地墙钟锚点；`DAILY+` 的时刻取其 time 部分。亚日频（HOURLY 及更密）不允许再给时刻。
- 密度上限：7 天窗口出现数超过 `max_occurrences_per_window`（默认 20000）→ 422 `RRULE_TOO_DENSE`，保证枚举收敛。

### 2.1 本地墙钟 → UTC 的规则（`app/planner/localize.py`）

对墙钟 `W` 计算两条 PEP 495 `fold` 路径并 round-trip：

- `u0 = W(fold=0)→UTC`，`rt0 = u0→本地`；`u1 = W(fold=1)→UTC`。
- **GAP（`rt0 != W`，春季不存在时间）**
  - `gap_policy=SKIP`（默认）：不触发，生成一条出生即 `SKIPPED/DST_GAP` 的合成记录（有审计身份，永不执行）。
  - `gap_policy=SHIFT_FORWARD`：取跳时后的第一个合法瞬间（=fold=0 的 UTC），即**墙钟顺延 gap 增量**。
    例：纽约 2026-03-08 `02:30` 不存在 → 实际执行于本地 `03:30` = `07:30Z`。
- **FOLD（`rt0==W` 且 `u0!=u1`，秋季重复时间）**：同一墙钟对应两个 UTC 瞬间，**只执行一次**：
  - `fallback_policy=EARLIEST`（默认）取第一次（fold=0，EDT）；`LATEST` 取第二次（fold=1，EST）。
  - 例：纽约 2026-11-01 `01:30` → EARLIEST `05:30Z` / LATEST `06:30Z`。
- **闰日/缺失日期**：`FREQ=MONTHLY/YEARLY` 的锚点日在目标月不存在（如平年 2/29）→
  `SKIPPED/MISSING_DATE`；闰年 2/29 正常触发。`BYMONTHDAY=29` 在平年 2 月按 RFC 语义跳过（无占位）。

### 2.2 例外日期

- 键 `YYYY-MM-DD`（匹配当天所有出现）或 `YYYY-MM-DD HH:MM:SS`（精确墙钟，可带 `[earliest|latest]`）。
- `SKIP`：取消该出现（行置 `CANCELLED/EXCEPTION`，移除例外后身份可 rearm）。
- `FORCE`：在原本无出现处**新增**一次触发；与 rrule 解析到同一 UTC → 422 `EXCEPTION_CONFLICT`；
  作用于 fold 歧义墙钟但未给折叠选择子 → 422 `EXCEPTION_AMBIGUOUS_FOLD`；作用于 gap → 422 `EXCEPTION_CONFLICT`。

---

## 3. 触发身份与"改计划不重触历史"

身份是 `(schedule_id, kind, 规范字符串)` 的 **uuid5** 纯函数（`app/planner/identity.py`），
**与版本、序号、策略、spec 哈希都无关**：

| 类型 | 规范名 |
|---|---|
| 真实出现 | `t:<UTC YYYYMMDDTHHMMSSffffffZ>` |
| gap 跳过 | `g:<本地墙钟>` |
| 缺失日期 | `m:<被请求的本地年月日时分秒>` |
| FORCE 新增 | `f:<本地墙钟>[e|l]` |

每次 tick 对 `[now−deadline, now+horizon]` 窗口做**按身份的集合对账**（`app/kernel/materialize.py`，
持 `pg_try_advisory_xact_lock`，插入用 `ON CONFLICT DO NOTHING`）：

- 身份已存在 → **保留其状态**（已 `SUCCEEDED/FAILED/SKIPPED` 的历史永不被触碰）；
- 新身份 → 插入（真实 `PENDING`；gap/missing 出生即 `SKIPPED`）；
- 旧版本残留 / 窗口内消失的非终态 → `CANCELLED/SUPERSEDED`；
- `CANCELLED` 的身份在后续版本回归时可 **rearm**（它从未执行过），并写 `TRIGGER_REARMED`；
  一旦 `SUCCEEDED/FAILED/终态SKIPPED`，身份永久封存，即使再次出现也绝不复活。
- 计划更新走乐观并发：`PUT` 必须带 `expected_version`，不匹配 → 409 `VERSION_CONFLICT`；
  每次更新追加不可变的 `schedule_revisions` 行。
- 两个理论出现解析到同一 UTC（如 SHIFT_FORWARD 与相邻出现碰撞）→ 确定性合并（序号小者胜出，
  败者写入 `merged_occurrences` 与 `OCCURRENCE_MERGED` 审计），不允许唯一键冲突冒泡成 500。

---

## 4. 停机补跑：上限与确定顺序（`app/kernel/dispatch.py` + `scheduler.py`）

每个计划可配置：`lateness_grace_seconds`（默认 30）、`catchup_deadline_seconds`（默认 3600）、
`catchup_rate_limit`（每 tick 每计划最多补 N，默认 100）；另有全局 `catchup_global_budget`。

- `scheduler_heartbeat` 记录每个计划最近一次完整 tick。若 `now - last_heartbeat > max(3×tick, 阈值)`，
  判定发生停机，开启新的 **recovery epoch**，并为积压（`age > grace` 的 PENDING）按
  `ORDER BY due_at, id` 分配密集 `backlog_rank`。
- 每个 run 只用一个 `observed_now`，认领在同一事务内用该时刻分类：
  1. **准时**：`age ≤ grace`，立即执行，**不占补跑预算**（普通轻微迟到不算停机）。
  2. **补跑**：`grace < age ≤ deadline` 且属于当前 epoch，按 `(backlog_rank, due_at, id)` 每 tick 至多
     `rate_limit` 个，**最旧的先跑**；多计划间按 `schedule_id` 确定排序并共享全局预算。
  3. **过期**：`age > deadline`（仅在认领事务内判定）→ 终态 `SKIPPED/EXPIRED`。
  4. **溢出**：容量公式判定永远赶不上的积压才永久放弃。tick 间隔 T、预算 B，rank r 的最早可服务时刻为
     `recovery_start + ceil(r/B)·T`；若 `due_at + deadline < 该时刻` → `SKIPPED/OVERFLOW`。
     其余积压**保留 PENDING 跨 tick 继续服务**，不会因为"本 tick 用不完预算"被误杀。
- PAUSED 计划只物化不派发；恢复后超 deadline 的按 EXPIRED（明确取舍）。

---

## 5. 时钟回拨、并发与崩溃

- **认领原子化**：`UPDATE ... SET status='RUNNING', attempts=attempts+1 ... WHERE id IN
  (SELECT id ... WHERE status='PENDING' ... FOR UPDATE SKIP LOCKED) RETURNING *`。
  时钟被回拨后，已 `RUNNING/SUCCEEDED` 的行不再满足 `status='PENDING'`，因此**不可能被再次执行**；
  判重靠身份+状态，与墙上时间无关。
- **多 worker 安全**：物化咨询锁 + `SKIP LOCKED` + 部分唯一索引
  `(schedule_id,due_at) WHERE due_at IS NOT NULL` 与 `(schedule_id,synthetic_key) WHERE ...`。
- **worker 崩溃（租约）**：`RUNNING` 超过 `claim_expires_at`（默认 300s）→
  未用尽尝试次数则回 `PENDING` 并置确定性指数退避 `not_before`，用尽则 `FAILED`；
  同时把在途执行行置 `LEASE_EXPIRED` 并审计 `LEASE_RECLAIMED`。
- **投递语义**：确认是 at-most-once（已确认不重放）；崩溃时副作用可能 at-least-once，
  因此 `http` 动作**必须幂等**——请求带 `Idempotency-Key: <trigger_id>` 与 `X-Attempt-No`。
  2xx 成功；4xx 视为永久失败（立即终态）；5xx/超时/连接错误可重试，错误码分别为
  `ADAPTER_HTTP_4XX / ADAPTER_HTTP_5XX / ADAPTER_TIMEOUT / ADAPTER_CONNECTION`。

---

## 6. HTTP API

| 方法 路径 | 说明 |
|---|---|
| `POST /api/v1/schedules` | 创建（各类 422 拒绝，见错误码表） |
| `GET /api/v1/schedules` / `GET /schedules/{id}` | 列表 / 详情 |
| `PUT /api/v1/schedules/{id}` | 更新（body 带 `expected_version`，产生新版本） |
| `POST /schedules/{id}/pause` `/resume`；`DELETE` | 暂停/恢复/软删除 |
| `GET /schedules/{id}/triggers?status=&limit=` | 触发器（状态、due_at、rank、attempts…） |
| `GET /triggers/{id}` / `GET /triggers/{id}/executions` | 触发器 / 每次尝试审计 |
| `GET /schedules/{id}/timeline?start=&end=` | **纯理论预览**，不写库，打印 local→UTC 判定 |
| `POST /api/v1/admin/ticks` | 同步跑一个 pass；body 可注入 `now`（确定性测试）与 `schedule_id` |
| `GET /runs` / `GET /audit?entity_id=` / `GET /healthz` | 运行记录 / 审计 / 健康 |

错误统一信封：`{"error":{"code","message","details","request_id"}}`。

| code | HTTP | 含义 |
|---|---|---|
| `VALIDATION_ERROR` | 422 | JSON/类型校验 |
| `RRULE_PARSE_ERROR` | 422 | RRULE 畸形 |
| `UNSUPPORTED_RRULE` | 422 | 使用了明确不支持的 token |
| `TIMEZONE_UNKNOWN` | 422 | 未知 IANA 时区 |
| `POLICY_CONFLICT` | 422 | 跨字段冲突（COUNT+UNTIL、亚日频带时刻、坏 URL…） |
| `INVALID_EXCEPTION_DATE` | 422 | 例外键格式非法 |
| `EXCEPTION_CONFLICT` / `EXCEPTION_AMBIGUOUS_FOLD` | 422 | FORCE 重复 / gap / fold 歧义 |
| `RRULE_TOO_DENSE` | 422 | 密度上限（资源超限） |
| `NOT_FOUND` | 404 | 资源不存在 |
| `VERSION_CONFLICT` / `STATE_CONFLICT` | 409 | 乐观锁冲突 / 非法状态转移 |
| `PLANNER_ERROR` / `PLANNER_NONMONOTONIC` | 500 | 未定义/未收敛——**绝不当成功返回** |

`SKIPPED/EXPIRED|OVERFLOW|DST_GAP|MISSING_DATE` 是**触发器行状态/原因，不是 API 错误**。
某个计划理论计算失败时该计划 fail-closed（本 tick 不派发），run 状态置 `PARTIAL` 并在 stats/audit 携带错误码。

---

## 7. 可解释性

- 结构化 JSON 日志逐阶段输出：阶段
  `PLAN_EXPAND → PLAN_OVERLAY → PLAN_RESOLVE → MATERIALIZE_RECONCILE → CLAIM → EXECUTE → FINALIZE`，
  字段含 `request_id / run_id / schedule_id / schedule_version / trigger_id / occurrence_no /
  attempt_no / code_version / tzdata_version / observed_now / verdict`；失败判定为 `UNDEFINED`。
- 每次执行尝试一行 `executions`（attempt 序号、worker、起止、状态、code、错误）。
- 测试以 `pytest -s` 打印：本地墙钟→UTC、fold=0/1、gap 顺延、run_id、trigger_id、版本、序号、补跑顺序。

---

## 8. 模块关系（职责分离，模块间只通过 dataclass/错误类型契约交互）

```
app/planner/        纯函数层（无 DB、无 IO）
  rrule.py            RRULE 解析/校验/naive 墙钟序列   → RRuleSpec/IndexedWall；RRuleError 家族
  calendar.py         工作日/例外覆盖、缺失日期         → PlannedWall；Exception* 错误
  localize.py         墙钟→UTC（gap/fold，round-trip 断言）→ Resolution
  identity.py         uuid5 规范身份
  service.py          规格→理论 Occurrence 流、密度/冲突校验 → Plan/PlanRequest
  tzutil.py           强制锁定 tzdata、记录版本
app/kernel/         调度内核
  materialize.py      身份集合↔triggers 对账（咨询锁、rearm/cancel/merge）
  dispatch 逻辑        在 repositories 的 claim/reclaim/backlog/overflow/expire
  scheduler.py        一个确定性 pass（run_id、单一 observed_now、编排与 fail-closed）
app/executor/       执行适配：base(协议) / log / http
app/state/          repositories.py（全部 SQL 在此）/ audit.py
app/api/            schedules.py / admin.py（薄层）；deps.py
app/{main,config,db,clock,errors,schemas,services,logx}.py
tests/              conftest(夹具/FakeClock) + reference.py(独立参考) + unit/*
db/schema.sql       幂等 DDL；scripts/ 启动与验证；examples/ 示例请求
```

---

## 9. 独立参考实现（期望值不来自被测代码）

`tests/reference.py` **不 import 任何生产 planner 代码**，它用两条独立路径生成期望值：

1. 序列由第三方 **`python-dateutil.rrule`** 生成（生产用自研解析器）；
2. 本地→UTC 用**独立的 UTC 分钟扫描器**：枚举候选 UTC 分钟并 `astimezone` 反查墙钟，
   按"该墙钟出现 0 / 1 / 2 次"判定 gap/normal/fold（生产用 fold 双路 round-trip）。

`test_rrule_parity.py` 对 2026 全年在有 DST 的纽约与无 DST 的上海逐日比对（含 BYDAY）。

测试覆盖：gap SKIP/SHIFT、fold EARLIEST/LATEST 仅一次、闰日、时区修改、各类编辑不重触历史、
flip-flop rearm、停机顺序/速率截断/容量存活/EXPIRED/OVERFLOW、时钟回拨、租约回收与退避、
每个拒绝码各一个断言、http 成功/4xx/5xx/超时/幂等头、端到端。

---

## 10. 支持范围与边界

- 支持：上述 RRULE 子集、IANA 时区、`log`/`http` 动作、多 worker（DB 锁）、单 PostgreSQL。
- 不做：cron 表达式、分布式租约（超出单库）、鉴权/多租户、迁移框架（schema 幂等直应用）、
  日历节假日表（用例外日期表达）。
- 时间全部以 UTC `timestamptz` 存储；`local_wall_ts` 是"时区无关的本地墙钟"，时区在 `tz_name`。
