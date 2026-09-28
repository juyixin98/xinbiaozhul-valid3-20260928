# mqttlocal — 本地 MQTT 3.1.1 子集代理（Go + 标准库 net + SQLite）

一个**纯后端、可离线复现**的 MQTT broker 实现，不依赖任何生产账号、付费
服务或外部消息基础设施。SQLite 驱动使用纯 Go 的 `modernc.org/sqlite`
（**无 cgo**，版本在 `go.mod` / `go.sum` 中锁定）。

- 传输：TCP 上的 MQTT 3.1.1（协议级别 4），默认 `127.0.0.1:1883`
- 支持：`CONNECT` / `CONNACK`、`SUBSCRIBE` / `SUBACK`、`PUBLISH`
  （QoS 0 / QoS 1）、`PUBACK`、`PINGREQ` / `PINGRESP`、`DISCONNECT`、
  Last Will、Retained Message、Clean/Persistent Session、连接接管
- **明确不支持**：QoS 2 全流程（PUBREC/PUBREL/PUBCOMP）、
  `UNSUBSCRIBE`、鉴权体系、TLS。所有约定外输入都有**明确、可断言**的
  拒绝行为（见下文 §8）。
- 投递语义：QoS 0 至多一次；QoS 1 **至少一次**（允许重复，重传置
  `DUP=1`）。**本实现不提供、也不声称“恰好一次”**。

---

## 1. 目录结构与职责划分

```
cmd/mqttlocal/        服务进程入口（解析参数、装配、信号关停）
cmd/mqttlocal-pub/    示例发布者（只依赖本工程协议层 + 自制客户端）
cmd/mqttlocal-sub/    示例订阅者
internal/packet/      协议编解码（严格）            ← 协议层
internal/topic/        通配匹配：生产实现 + 独立参考实现（差分测试用）
internal/store/       SQLite 持久层（会话/订阅/inflight/offline/retained）
internal/config/      资源上限与时长配置
internal/diag/        结构化诊断日志（run/conn/packet id）
internal/broker/      业务处理：会话状态机、投递、遗嘱、保留、资源策略
internal/server/      连接状态机（NEW→READY→CLOSED）、TCP/keepalive
internal/testsupport/ 自制测试客户端 + 夹具（可注入任意字节）
test/integration/     端到端协议测试（真实 TCP + SQLite 文件）
configs/              JSON 配置（默认 / 小限额）
scripts/verify.sh     一键本地验证（vet + race 测试 + 实进程 pub/sub）
docs/                  协议决策记录与边界规则
```

各层输入/输出与错误契约：

| 层 | 输入 | 输出 | 错误契约 |
|---|---|---|---|
| `packet.Reader` | 字节流 | 强类型 `*Packet` | `ErrMalformed`（违规）、`ErrTooLarge`（超资源上限）、`io.EOF`（干净结束）；CONNECT 策略拒绝用 `*ConnectRejectError`（携带 CONNACK 码） |
| `topic.Match` | filter + name | bool | 非法 filter 返回 false（结构有效性由 `ValidTopicFilter` 单独给出） |
| `store.*` | Go 类型 | 行/计数 | `sql` 错误透传；`ErrNotFound` 表示无此行 |
| `broker.*` | 已解析包 + `*Conn` | 直接在有序发送通道上排队应答；必要时返回 `*CloseError` | `CloseReason` 区分 协议违规 / 状态冲突 / 资源耗尽 / 网络失败 / keepalive / 接管 / 关停 |
| `server` | TCP 连接 | 帧与关闭 | 把读错误分类成上面的原因，**绝不把不确定结果包装成成功** |

## 2. 从干净环境验证

要求：Go 1.22+（仅用标准库网络 + 已锁定版本的纯 Go SQLite 驱动）。

```bash
# 一条命令：vet + 全部测试（含 -race）+ 构建 + 实进程 pub/sub 冒烟
./scripts/verify.sh

# 或分步
make test-race      # go test ./... -race
make bin           # 产出 bin/mqttlocal{,-pub,-sub}
make run           # 监听 127.0.0.1:1883，SQLite 文件 data/mqttlocal.db
```

只跑某组测试：

```bash
go test ./internal/packet/ -v          # 编解码严格性
go test ./internal/topic/ -v           # 通配边界 + 差分 fuzz
go test ./test/integration/ -v        # 端到端协议场景
go test ./test/integration/ -run Reject -v   # 所有拒绝条件
```

实进程手动演示：

```bash
./bin/mqttlocal -listen 127.0.0.1:1883 -db /tmp/demo.db
./bin/mqttlocal-sub  -id s1 -filter 'demo/#' -qos 1 -for 30s -clean=false
./bin/mqttlocal-pub  -id p1 -topic demo/hello -msg 'hello qos1' -qos 1
./bin/mqttlocal-pub  -id p2 -topic demo/r -msg 'retained' -qos 0 -retain
```

配置文件示例见 `configs/broker.json`（生产式默认）与
`configs/broker.test.json`（刻意调小的资源上限，便于触发边界）。

## 3. 剩余长度（Remaining Length）严格校验

`internal/packet/codec_read.go` 对 §2.2.3 做了超出字面的严格实现：

1. 编码最多 4 字节、最大值 `268 435 455`；第 4 个字节续延位仍为 1
   （即要求非法第 5 字节）→ `ErrMalformed`（MQTT-2.2.3-2）。
2. **非最小编码拒绝**：多字节编码的**最后一位数字为 0** 即非最小，
   例如 `[0x80,0x00]`（表示 0）、`[0x81,0x00]`（表示 1）。
   注意 128 的最小编码是 `[0x80,0x01]`——首数字为 0 合法，只有**末**
   数字为 0 才非最小。
3. 长度字段读到一半 EOF → `ErrMalformed`（截断帧），而不是干净 EOF。
4. 超过 broker 自身 `max_packet_bytes` 上限 → `ErrTooLarge`
   （独立于协议最大值，可配置）。
5. 声明的剩余长度与实际读到字段不一致（截断、尾部多余字节）→
   `ErrMalformed`。

测试：`TestDecodeRemainingLengthBoundary`、`TestRemainingLengthRoundTrip`、
`TestPayloadCap`，以及集成测试 `TestRejectMalformedConnect`、
`TestRejectPacketTooLarge`。

## 4. 包标识符（Packet Identifier）严格校验

- `PUBLISH(QoS1/2)`、`PUBACK`、`SUBSCRIBE` 标识符必须非 0
  （MQTT-2.3.1-1），为 0 直接判 `ErrMalformed` 关连接。
- **服务端→客户端**的标识符在会话内分配，跳过所有 inflight 中占用的 id
  （1..65535 循环），与发布者侧标识符是**相互独立**的两个流：
  发布者 QoS1 PUBLISH 用自己的 id，broker 回 PUBACK 回显该 id；
  投递到订阅者时 broker 分配**订阅流**自己的 id。
- **重复包标识 / DUP（发布者侧）**：QoS1 允许重发同一标识符，broker 对
  每个 PUBLISH 都回 PUBACK（幂等），绝不因此声称去重或恰好一次。
  测试 `TestDuplicatePublisherPacketID`。
- **PUBACK 语义**：只能针对一个当前 inflight 的 id；对未知/已结算的 id
  再发 PUBACK 属于协议违规 → 关连接（`TestReconnectRedeliveryDup`
  末尾、`TestStateConflicts`）。

## 5. 持久会话、未确认投递与重传（至少一次）

持久化在 SQLite，表结构见 `internal/store/sqlite.go`：

- `sessions`：持久会话与 Will（仅注册期间）与下一 id 提示；
- `subscriptions (client_id, filter)` PRIMARY KEY：持久订阅；
- `inflight (client_id, packet_id)` PRIMARY KEY：已投递但未 PUBACK 的 QoS1；
- `offline (id AUTOINCREMENT, client_id)`：离线期间排队的 QoS1（FIFO）；
- `retained(topic PRIMARY KEY)`：保留消息。

语义：

- **CleanSession=1**：订阅/inflight/offline 全部只在内存，连接结束即消失。
- **CleanSession=0**：连接存活时内存结构与 SQLite 双写；QoS1 投递时
  **先持久化 inflight，再尝试入发送队列**；PUBACK 到达才删除
  inflight。因此“已持久化但 TCP 未真正发出”与“已发出未确认”在
  重连后表现一致：**用同一 packet id、DUP=1 重投**（MQTT-4.4.0-1、
  MQTT-3.3.1-1）。
- 重连（同一 ClientID、Clean=0）：CONNACK `Session Present=1`，
  然后先重放全部 inflight（DUP=1），再按 inflight 窗口把 offline
  队列提升为新的 QoS1 投递（非 DUP，新 id）。
- broker 进程重启：inflight/offline/订阅全部存活（测试
  `TestBrokerRestartPersistence` 真的关闭并重新打开同一 DB 文件）。
- **QoS 降级**：投递 QoS = min(发布 QoS, 订阅授权 QoS)；降到 0 的消息
  对离线会话不排队（QoS0 不进持久会话存储）。
- 同一会话多个重叠 filter 命中同一主题：**投递一次**，取最高授权
  QoS（明确的本地策略，非标准强制行为）。

覆盖：`TestReconnectRedeliveryDup`（确认丢失 → DUP 重传 → 确认后
清除 → 重复确认关连接）、`TestCleanSessionNoState`、
`TestBrokerRestartPersistence`、`TestOfflineCapEviction`。

## 6. 通配订阅与保留消息规则

### 6.1 通配匹配（`internal/topic`）

- `/` 分层；前导/尾随 `/` 产生有意义的**空层级**（`/a/` 三层）。
- `+` 恰好匹配一层（允许内容为空）；`#` 只允许在最后一层，匹配剩余
  **任意多层，含零层**：`a/#` 匹配 `a`、`a/`、`a/b/c`，但不匹配 `ab`。
- `$` 开头主题不被首层为 `+`/`#` 的 filter 匹配（字面 filter 仍可）。
- 生产实现 `topic.Match`（分层）与**独立参考实现**
  `topic.ReferenceMatch`（单遍字节状态机，无共享代码）同时存在；
  `TestDifferentialFuzz` 用 2 万组结构化随机输入做差分，
  `TestWildcardBoundary` 给出全部 §4.7 边界表。期望值**不是**用被测
  代码生成的。

### 6.2 保留消息（MQTT-3.3.1）

- 入站 `RETAIN=1`：写入/覆盖（空 payload = **删除**），**不实时扇出**。
- SUBSCRIBE 成功时：对每个**新增** filter，匹配的保留消息各投递一次，
  投递帧 `RETAIN=1`，QoS 取 min(保留 qos, 授权 qos)；重复 SUBSCRIBE
  同一 filter 仍会再次投递（可观察行为）。
- 遗嘱 `retain=1`：异常断连时**既实时发布、又写入保留存储**
  （MQTT-3.1.2-10）；实时发布那帧的 RETAIN 位为 0，只有新订阅读到的
  存储副本 RETAIN=1。
- 覆盖：`TestWildcardBoundariesAndRetained`、`TestWillOnAbnormalClose`。

## 7. 资源上限与慢订阅者

配置（`internal/config`）：

| 键 | 默认 | 超限行为（明确，可断言） |
|---|---|---|
| `max_connections` | 100 | 新 CONNECT → CONNACK **0x03** 后关连接 |
| `max_inflight_per_session` | 32 | 在线订阅者窗口耗尽 → 判定慢订阅者，**关连接（resource_exhaustion）**；inflight 已持久化，重连 DUP 重投 |
| `send_queue_depth` | 64 | 单连接有界发送队列；队列满同样触发慢订阅者关闭 |
| `max_offline_per_session` | 1000 | 离线队列超限 → **丢弃最旧**（FIFO 淘汰），计入 `DroppedOffline` 日志/计数；inflight 永不淘汰 |
| `max_packet_bytes` | 1 MiB | 剩余长度超限 → `ErrTooLarge` 关连接 |

慢订阅者策略是**显式策略**而非静默成功：关闭原因可从诊断日志
（`reason=resource_exhaustion`、`stage=slow_subscriber.close`）与
`Stats.ClosedResource` 区分。测试：`TestSlowSubscriber`、
`TestConnectionCap`、`TestOfflineCapEviction`。

## 8. “不支持 QoS2 / 约定外输入”的明确拒绝表

| 输入 | 行为 | 依据 / 测试 |
|---|---|---|
| CONNECT 协议级别 ≠ 4 | CONNACK **0x01** 后关闭 | `TestRejectConnectConditions` |
| 空 ClientID + CleanSession=0 | CONNACK **0x02** 后关闭 | MQTT-3.1.3-8 |
| Will QoS=2 | CONNACK **0x03**（本子集无 QoS2 的明确策略）后关闭 | 同上 |
| 连接数/存储不可用 | CONNACK **0x03** | `TestConnectionCap` |
| CONNECT 保留位非法、Will 标志矛盾、Will QoS3、协议名错、长度非最小/截断 | **不发** CONNACK，直接关传输（帧不可信） | `TestRejectMalformedConnect` |
| SUBSCRIBE 中 QoS=2 | 该 filter SUBACK **0x80**，**连接继续存活** | `TestSubscribeQoS2FailureCode` |
| SUBSCRIBE 保留位 ≠ 0010 / QoS=3 / 空 filter 列表 | 协议违规关连接 | `TestRejectProtocolViolations` |
| 入站 PUBLISH QoS=2 / QoS=3 | 协议违规关连接 | 同上 |
| 入站 PUBLISH DUP=1 且 QoS0 | 协议违规关连接（MQTT-3.3.1-2） | 同上 |
| PUBLISH 主题含 `+`/`#`/NUL | 协议违规关连接 | 同上 |
| PUBREC/PUBREL/PUBCOMP/UNSUBSCRIBE/UNSUBACK | `ErrUnsupported` → 关连接 | 同上 |
| CONNACK/SUBACK/PINGRESP 出现在客户端→服务端 | 协议违规关连接 | 同上 |
| 首个包不是 CONNECT；同一传输第二个 CONNECT | 状态冲突关连接 | `TestStateConflicts` |
| PUBACK 指向未知/已结算 id | 协议违规关连接 | 同上 |
| keepalive 1.5× 窗口无任何包 | keepalive 超时关连接并发 Will | `TestKeepAliveTimeout` |
| READY 状态裸 EOF（无 DISCONNECT） | **异常断连**，发 Will | `TestWillOnAbnormalClose` |
| 显式 DISCONNECT | 干净关闭，**不发** Will（MQTT-3.14.4-3） | 同上 |
| 相同 ClientID 重连 | 旧连接被接管关闭，**不发**旧 Will（MQTT-3.1.4-2） | `TestTakeOver` |

## 9. 连接状态机与诊断

状态机见 `internal/server/conn.go` 顶部注释：`NEW → READY →
CLOSED-{clean,protocol,state,resource,keepalive,takeover,network,shutdown}`。

每条诊断记录（`internal/diag`，slog 文本格式）都带：

- `run=<运行标识>`（启动时生成，可用 `-run-id` 指定，测试用 `it-时间戳`）
- `conn=<每连接序号>`、`client_id`、`packet_id`（适用时）
- `kind=event|decision|reject|failure` 与 `stage=<阶段/序号>`，
  关键决策带规则名（如 `rule="MQTT-3.3.1-2"`）。

四类失败可区分：**非法输入**（reject / reason=protocol_violation）、
**状态冲突**（state_conflict）、**资源超限**（resource_exhaustion）、
**运行失败**（failure / reason=network_error）。未定义/未收敛的结果
不会被记为成功：解析失败即关闭，存储失败映射 CONNACK 0x03，
未确认投递保持 inflight 直到确认。

## 10. 关键算法说明

- inflight 标识符分配：会话内游标 `1→65535→1`，跳过 map 中现存 id。
- offline 提升：重连先重放 inflight；每次 PUBACK 释放窗口后尝试
  `PopOffline(room)`，保证不超过 inflight 上限且保持 FIFO。
- 扇出：单把 broker 互斥锁内遍历在线连接 + 查询离线订阅；发送仅做
  有界 channel 非阻塞入队，绝不持锁做网络 I/O。
- Will 判定只看关闭原因：干净 DISCONNECT / 接管 / 有序关停不发；
  其余（含裸 EOF、keepalive、协议/资源/网络异常）都发。broker
  启动时对仍存有 Will 的持久会话补发**一次**并清除
  （`TestBrokerRestartPersistence` 验证二次重启不重发）。

## 11. 已知范围限制（如实声明）

- 不实现 QoS2、UNSUBSCRIBE、认证授权、TLS/WebSocket、$SYS 发布。
- 单实例、单 SQLite 连接串行化（面向本地/小规模，非集群）。
- 离线消息仅按 QoS1 存储；QoS0 不进持久会话。
- 本工程的目标是协议子集的**严格正确性与可复现测试**，不是吞吐性能。
