# 持久化周期调度服务（FastAPI + PostgreSQL）

支持 **IANA 时区、工作日/月规则、一次性加期（extra dates）与例外日期（exceptions）**
的纯后端周期调度服务。所有触发以 **UTC 时刻** 持久化并拥有全局唯一身份；
修改计划不重放历史；停机补跑有上限且顺序确定；时钟回拨拒绝执行、
已确认触发绝不重复。

本工程不依赖任何生产账号、付费服务或真实业务数据；一条
`scripts/verify.sh` 可在干净目录复现全部内容。

---

## 1. 快速开始（干净环境）

前置：Python 3.12+、Docker（或任意可达的 PostgreSQL 14+）。

```bash
# 一键：建虚拟环境 → 起 Postgres 容器 → 跑全部测试 → 跑端到端演示
./scripts/verify.sh

# 只跑测试（需要 127.0.0.1:5433 上有数据库）
./scripts/verify.sh --tests
```

手动启动：

```bash
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt
docker compose up -d db                 # Postgres on localhost:5433
python -m scheduler                     # API on http://127.0.0.1:8080
```

健康检查与示例请求：

```bash
curl -s localhost:8080/healthz
curl -s -X POST localhost:8080/schedules -H 'content-type: application/json' -d '{
  "name":"daily-ny","timezone":"America/New_York","frequency":"daily","at":"09:00",
  "start_date":"2024-01-01","end_date":"2024-12-31",
  "exceptions":["2024-07-04"],"extra_dates":["2024-12-26"],
  "executor":{"type":"log","payload_template":{"job":"report"}}
}'
# 推进一次调度（生产默认由后台 ticker 每秒自动 tick）
curl -s -X POST 'localhost:8080/ticks'
```

更多请求样例见 [`examples/requests.http`](examples/requests.http)。

---

## 2. 时区与夏令时语义（题目第 1 点）

所有计算只发生在 `scheduler/planner/calendar.py`，判定依据与输入输出都会进入
审计日志。Python 用 `zoneinfo` + `fold` 表达墙上时间：

| 情形 | 判定 | 规则 |
|---|---|---|
| **春季不存在时间（gap）** | 该本地时间的 `fold=0` 与 `fold=1` 两种解释映射到 UTC 后再转回本地，**都不能还原**出原墙上时间（如纽约 2024-03-10 02:30，偏移从 EST −300 跳到 EDT −240） | **跳过**这一天的触发，记审计 `plan.skipped-gap`（含跳变前后偏移分钟数）。绝不"猜测"平移到 01:30/03:30，那会制造一个用户没要求的触发 |
| **秋季重复时间（ambiguous）** | 两个 fold 映射到**两个不同且都能往返**的 UTC 瞬间（如纽约 2024-11-03 01:30 → 05:30Z(EDT, fold=0)、06:30Z(EST, fold=1)） | 默认 `ambiguous_policy=both`：**两个瞬间各触发一次**，身份中的 `fold` 不同；`early` 只触发第一个、`late` 只触发第二个 |
| 普通时间 | 两个 fold 映射到同一 UTC 瞬间 | 触发一次，fold=0 |
| 无 DST 地区（如 Asia/Shanghai、UTC） | 全年只有 LITERAL | 正常 |

> 注意一个容易错的点：不是所有"回拨日"都歧义。纽约 11 月 3 日 **02:30** 已经
> 处于回拨之后（EST），当天只触发一次；真正歧义的是 **01:30**。
> 测试里两种时刻都有覆盖。

**闰日**：`daily` 自然包含 2 月 29 日；`monthly` 指定 31/29 日时，短月份
（如非闰年 2 月）当天没有匹配，不报错、不补到相邻日（有全年 oracle 比对）。

---

## 3. UTC 唯一身份与计划版本（题目第 2 点）

每次触发的身份在生成时确定并全局唯一（DB 唯一约束兜底）：

```
fire_key = "{schedule_id}:v{version}:{kind}:{ordinal}:f{fold}"
```

* `kind`：`base`（周期规则）或 `extra`（一次性加期）。
* `ordinal`：该**本地日期**在规则序列中的稳定序号，从 `start_date` 计数；
  一次性日期用 ISO 日期序数。序号与"在哪个窗口规划"无关，所以重复规划、
  停机恢复、跨年都不会改变身份。
* `fold`：秋季歧义的 0/1，保证两个瞬间身份不同。

**修改计划 = 新版本身份空间**：`PUT /schedules/{id}` 会

1. `version += 1`，重新物化未来触发（身份全部带 `:v2:`）；
2. 旧版本所有未完成的 `planned/due` 触发置 `cancelled`（审计带数量）；
3. 旧版本已 `succeeded/failed/expired` 的历史**原样保留、不可变**，
   绝不会因为编辑而再次触发。

每个身份最多被物化一次：插入用 `ON CONFLICT (fire_key) DO NOTHING`，
领取用行锁；已终止状态（succeeded/failed/expired/cancelled）永远不可再被选中。

---

## 4. 停机补跑：有上限、顺序确定（题目第 3 点）

每个 tick（`POST /ticks` 或后台 ticker）严格按以下顺序工作：

1. **水位检查**：`runtime_state.last_tick_utc` 必须严格前进，否则
   `409 STATE-CLOCK-ROLLBACK`（见第 5 点）。
2. **延长规划窗口**：为每个 active 计划把物化范围推进到
   `now + plan_ahead_days`（默认 31 天），增量、幂等。
3. **过期**：早于 `now - backfill_window_days`（默认 7 天）仍未完成的触发
   置 `expired`，不再补跑（记审计），避免无限历史追赶。
4. **领取 + 投递**，在一条 SQL 里用窗口函数确定优先级与上限：
   * **bucket 0（准时）**：`due ∈ (now-horizon, now]`，**不设上限、优先投递**，
     保证大量积压不会饿死当下该发的触发；
   * **bucket 1（补跑）**：更早的触发，按
     `(due_utc, schedule_id, ordinal, kind, fold)` **从旧到新**，
     数量上限 `max_backfill`（默认 100，测试用 5）。
   * 超过上限的补跑触发保持 `due`，留给后续 tick，tick 结果里
     `backfill_remaining` 如实报告剩余积压。
   * 多进程安全：`FOR UPDATE SKIP LOCKED`；崩溃的 worker 留下的
     `running` 行超过 5 分钟租约才可被重新领取（并计为新 attempt）。

**顺序是全序且可复现**的：准时桶优先，桶内按上述键排序，不依赖 UUID
字符串序或物理插入顺序。

---

## 5. 时钟回拨与幂等（题目第 4 点）

四重防线保证"不重复执行已确认触发"：

1. **单调水位**：`last_tick_utc` 只接受更晚的 tick；相等或更早 →
   `409 STATE-CLOCK-ROLLBACK`（响应里带上次与本次时间戳）。
2. **身份唯一**：`fire_key` 与 `(schedule,version,kind,ordinal,fold)` 双唯一约束。
3. **领取谓词**：只有 `due`（及租约失效的 `running`）可被领取；
   succeeded/failed/expired/cancelled 永远排除。
4. **完成校验**：run 完成时按 `(fire_key, run_id)` 精确更新，行数不为 1
   直接报 `ENGINE-DIVERGED`（未收敛结果绝不包装成成功）。

手动重投 `POST /fires/{fire_key}/retry` 仅对 `failed/expired` 开放；
对 `succeeded` 返回 `409 STATE-ALREADY-CONFIRMED`，对未终止状态返回
`409 STATE-NOT-RETRYABLE`。

---

## 6. 错误契约（四类失败可区分）

| 类别 | 异常 | HTTP | code 前缀/示例 |
|---|---|---|---|
| 输入非法 | `ValidationError` | 422 | `VAL-TIMEZONE`、`VAL-SPEC-RULES`、`VAL-TIMESTAMP` 等 |
| 资源不存在 | `NotFoundError` | 404 | `NOT-FOUND` |
| 状态冲突 | `ConflictError` | 409 | `STATE-CLOCK-ROLLBACK`、`STATE-CONFLICT`、`STATE-ALREADY-CONFIRMED`、`STATE-NOT-RETRYABLE` |
| 资源超限 | `ResourceLimitError` | 429 | `LIMIT-PLAN-WINDOW`（规划窗口 > 1830 天）；补跑上限是软预算（剩余如实报告），不伪造成功 |
| 运行失败 | `DispatchError` | 502 类语义* | `RUN-TIMEOUT`、`RUN-TRANSPORT`、`RUN-PERMANENT`(4xx)、`RUN-FAILED`(5xx/异常) |
| 内部不一致 | `EngineDivergenceError` | 500 | `ENGINE-DIVERGED` |

\* tick 本身返回 200，单个触发的投递失败进入 `failed[]` 并持久化为
`fires.status=failed` + `runs.status=failed`（审计 `fire.failed`），
而不是让整个 tick 失败——失败是一等、可查询、可显式重试的状态。
直接调用执行器的适配层才抛 502 类 `DispatchError`。

响应体统一为 `{"error":{"code","message","details"}}`。未定义/未收敛/不确定
的结果只会是错误，不会被包装成成功。

---

## 7. 模块组织与职责

```
scheduler/
├── config.py              # 配置（唯一读环境变量处），带类型校验
├── errors.py              # 四类错误契约 + NOT-FOUND / ENGINE-DIVERGED
├── api.py                 # FastAPI 协议入口、错误->HTTP 映射、确定性时钟头
├── main.py                # 装配：config→repo→engine→api；后台 ticker
├── planner/               # 【纯逻辑，无 I/O，可独立测试】
│   ├── calendar.py        # 时区内核：gap/ambiguous/literal 判定与 fold 解析
│   ├── spec.py            # ScheduleSpec：pydantic 校验，每个拒绝条件有 code
│   └── occurrence.py      # 日/周/月规则、稳定 ordinal、窗口规划、skip 原因
├── engine/
│   └── core.py            # 调度内核：tick 顺序、补跑预算、版本、投递、重试
├── execution/             # 【唯一产生副作用的层】
│   ├── base.py            # Executor 接口 + DeliveryContext/信封 schema v1
│   └── adapters.py        # LogExecutor（测试/本地）、WebhookExecutor（HTTP）
└── state/
    ├── schema.sql         # 表结构（唯一约束、CHECK、索引、水位单例）
    └── repo.py            # 全部 SQL：事务、行锁、水位、租约、审计、查询

tests/
├── reference.py           # 独立参考实现（逐本地日 + fold 往返），不共用算法
├── conftest.py            # 独立测试库、确定性时钟、故障注入、本地 webhook
├── test_spec_validation.py# 每个拒绝条件一个可断言测试
├── test_planner_dst.py    # gap/歧义/例外/加期/闰日/nth-weekday/序号稳定
├── test_annual_reference.py# DST 与无 DST 地区全年结果 vs 独立 oracle
├── test_engine.py         # 身份幂等、版本、补跑顺序/上限、回拨、暂停、重试
└── test_api.py            # HTTP 契约：201/200/404/409/422 + 真实 webhook
scripts/
├── verify.sh              # 干净环境一键验证
└── demo.py                # 五类语义端到端演示（打印本地时间→UTC 判定）
docker-compose.yml         # 本地 Postgres 16（端口 5433）
requirements.txt           # 锁定依赖；verify 生成 requirements.lock
```

**模块间契约**：planner 只接受 `ScheduleSpec`、返回 `PlannedWindow`
（fires + skipped，含解释信息），不触库；engine 调 repo 持久化、调 executor
投递，自己不写 SQL、不做 HTTP；repo 不解析业务规则；executor 不知道计划与
数据库。跨模块只抛 `errors.py` 里的类型，不跨边界抛裸 `ValueError`。

---

## 8. HTTP 协议

| 方法/路径 | 说明 |
|---|---|
| `POST /schedules` | 创建（201），立即物化近期触发 |
| `GET /schedules` / `GET /schedules/{id}` | 列表/详情（含 spec） |
| `PUT /schedules/{id}` | 改计划 → version+1，旧 pending 取消 |
| `POST /schedules/{id}/pause` / `/resume` | 暂停（不投递）/恢复 |
| `DELETE /schedules/{id}` | 删除（级联其触发与 run） |
| `GET /schedules/{id}/fires` | 物化的触发与状态 |
| `GET /fires/{fire_key}` / `POST /fires/{fire_key}/retry` | 查询/手动重投失败触发 |
| `GET /runs` / `GET /runs/{id}` | 每次投递记录（attempt、错误码、耗时） |
| `GET /audit?event=&schedule_id=` | 审计流（规划跳过、领取、成功、失败、过期、版本） |
| `POST /ticks?now=` | 执行一次调度步；`now` 或 `X-Test-Now` 可钉时间 |
| `GET /runtime` / `GET /healthz` | 水位/上次 tick 结果、存活 |

webhook 投递信封：`schema=scheduler.fire.v1`，带 `run_id/fire_key/
schedule_id/schedule_version/due_utc/dispatched_at_utc/attempt/payload`，
目标方可凭 `fire_key` 做下游幂等。

---

## 9. 测试与可解释性

```bash
.venv/bin/python -m pytest tests/ -v
```

* **全年参考 + 独立 oracle**：`tests/reference.py` 用与生产完全不同的算法
  （逐本地日构造 fold 候选并做 UTC 往返；生产是"规则日期→zoneinfo 解析"），
  对 **America/New_York（有 DST）** 与 **Asia/Shanghai（无 DST）** 的全年
  结果逐日逐 fold 比对。期望值不可能来自同一段实现。
* **本地时间→UTC 判定打印**：gap/歧义测试与 `scripts/demo.py` 都会打印
  fold、UTC、偏移分钟数、审计依据；每次触发带 `run_id`、`fire_key`、
  `ordinal`、attempt，tick 返回计划阶段/序号/剩余积压。
* **拒绝条件全覆盖**：`test_spec_validation.py` 为坏时区、坏时间格式、
  周/月日越界、重复、规则与频率不匹配、日期范围、extra/exception 冲突、
  webhook 缺 url/坏 scheme、多余字段等各写了可断言（code/message）的测试。
* **边界场景**：闰日（daily 与 month-day=29 在闰年/平年）、时区修改后
  UTC 重算且历史不重放、多天停机恢复、例外抑制规则日与加期、
  歧义日双触发、真实本地 HTTP webhook（含 410 永久失败）。

---

## 10. 支持范围与已知取舍

* 粒度：分钟（`HH:MM`，不支持秒）；日期按计划时区的本地日。
* 规则：daily / weekly(ISO 周一=1..周日=7) / monthly（月内日期 1..31，
  以及第 n 个周几，n ∈ {1,2,3,4,5,-1}，-1=最后一个）。
* 无 cron 表达式；`end_date` 可空（开放计划，物化仍受有界窗口约束）。
* gap 策略仅 `skip`（显式定义）；不做"前移/后移"这种需要业务裁决的猜测。
* 执行器：`log` 与 `webhook`（POST JSON，超时/5xx 可重试、4xx 永久失败）。
  接口化，新增 Kafka/邮件等只需实现 `Executor.deliver`。
* 多 worker 安全（行锁 + skip locked + 租约），但演示默认单进程后台 ticker；
  生产横向扩展时让一个进程 tick、其余只投递，或全部调用 `POST /ticks`
  （水位行锁会串行化）。
* 时间钉（`X-Test-Now`、`?now=`）用于测试/演示；生产不要发送该头。
