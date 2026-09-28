# mqttd — 本地 MQTT 3.1.1 子集代理

一个**仅用 Go 标准库网络栈 + SQLite（纯 Go 驱动 `modernc.org/sqlite`，无需 cgo）**
实现的本地 MQTT v3.1.1（协议级别 4）子集 broker。无外部账号、无付费服务、
无真实业务数据；所有测试用自制客户端在本地回环复现。

- 传输：TCP 上的 MQTT 3.1.1，默认 `127.0.0.1:1883`。
- 支持报文：`CONNECT / CONNACK / SUBSCRIBE / SUBACK / PUBLISH(QoS0/1) /
  PUBACK / PINGREQ / PINGRESP / DISCONNECT`，外加遗嘱（Will）。
- **明确不支持 QoS2**：相关约定外输入被显式拒绝（见下“拒绝矩阵”），
  绝不静默降级、绝不宣称“恰好一次”。

---

## 1. 从干净环境验证

前置条件：Go ≥ 1.22（无需 C 编译器，SQLite 为纯 Go）。

```bash
# 一键：依赖校验 → 构建 → vet → 竞态全量测试 → 黑盒冒烟
./scripts/verify.sh
```

分步：

```bash
go test -race -count=1 ./...          # 全部测试（含竞态检测）
go test ./internal/topics/... -v      # 通配匹配 + 独立参考实现差分
PORT=18830 ./scripts/smoke.sh         # 黑盒：真实进程收发 retained QoS1
```

启动服务：

```bash
go run ./cmd/mqttd -config configs/mqttd.env         # 或
go run ./cmd/mqttd -addr 127.0.0.1:1883 -db ./data/mqttd.db -retx 2s
```

示例请求（用本仓库自带的自制客户端 `cmd/mqttctl`）：

```bash
# 终端 A：持久会话订阅者
go run ./cmd/mqttctl -id reader -sub 'news/#' -qos 1 -clean=false

# 终端 B：发布一条 retained QoS1
go run ./cmd/mqttctl -id writer -pub news/ping -msg hello -retain -qos 1
```

---

## 2. 模块关系（分层与错误契约）

```
cmd/mqttd                     服务入口：flag / env 配置、信号、优雅退出
cmd/mqttctl                   示例/手工客户端（协议入口演示）

internal/packet   协议编解码：剩余长度、各报文、严格合法性校验（无业务策略）
internal/topics   主题名校验、过滤器校验、通配匹配（生产实现）
internal/reference/matchref  独立参考实现（递归风格），仅供测试差分
internal/store    SQLite 持久化边界：sessions/subscriptions/inflight/retained
internal/broker   连接状态机、路由 trie、保留消息、遗嘱、重传、指标、资源上限
internal/mqttclient 自制 MQTT 客户端（标准库 + 自有 packet 编解码）
internal/brokertest  测试夹具（临时库、随机端口、日志捕获、限流配置）
```

**错误契约**：`internal/packet` 的所有错误都是带 `Category` 的 `*packet.Error`，
取值 `invalid_input / unsupported / resource_limit / state_conflict / internal`。
连接状态机把类别映射到明确结局：协议违例直接关连接、QoS2 等超集特性归入
unsupported 关连接、资源上限返回 CONNACK code 3 或 SUBACK 0x80、内部错误记
internal 并关连接。**未定义/未收敛/不确定的结果不会被包装成成功**——例如
保留消息超限时连接被关闭，而不是悄悄接受。

日志每行带 `run=<运行标识> seq=<单调序号> stage=<阶段> result=<判定>
<key=value>`，判定词受控：`accept | reject_invalid | reject_unsupported |
reject_limit | state_conflict | retry | drop | internal_error`。

---

## 3. 关键状态与算法语义

### 3.1 剩余长度（MQTT 2.2.3，严格）
- 1–4 字节、base-128；第 4 字节若仍有续位（需要第 5 字节）即非法。
- **拒绝非最小编码**：终止字节贡献为 0 且前面已有字节（如把 127 编成
  `0xFF 0x00`）判为 `invalid_input`。
- 声明的剩余长度超过 broker 上限 → `resource_limit`，关连接。
- 流在载荷中途结束 → 截断错误（`invalid_input`）。

### 3.2 包标识（Packet Identifier，2.3.1）
- QoS1 PUBLISH / PUBACK / SUBSCRIBE 的标识符必须非 0，为 0 即协议违例。
- **入站方向**：代理不维护“发布者正在用的 pid 窗口”。同一 pid 可被发布者
  重复使用（重传/重复包标识），代理照常路由并对每个 PUBLISH 回 PUBACK。
  因此投递是 **at-least-once（至少一次）**：允许重复，重复帧在订阅者侧
  通过 DUP 区分。**本实现不提供也不声称恰好一次。**
- **出站方向（代理→订阅者）**：每个客户端独立的 1..65535 空间，由
  inflight 表分配，受 `MaxInflightPerClient` 约束；超限拒绝新投递（指标
  `rejected_inflight`）。

### 3.3 持久会话与未确认投递（4.1 / 4.4）
- `CleanSession=0`：sessions/subscriptions/inflight 持久化在 SQLite。
- QoS1 扇出时**先落 inflight 行再入发送队列**（即使发布者随后拿到 PUBACK、
  或进程崩溃，消息都不丢）。
- 单一 `deliveryPump` 是出站 QoS1 入队的唯一属主，状态：
  `未入队 → 已入队(在 sendCh) → 已写网 → (重传 DUP=1) → PUBACK 删除`。
  在 sendCh 中尚未写网的帧**绝不重复入队**，杜绝“重放定时器与首次发送”
  并发造成的伪重传。
- **重连/重启重放一律 DUP=1**：新连接上恢复的每条 inflight 都置 DUP=1
  （无法确认旧连接是否已送达，按规范必须置位）。行内的 `sent` 标志跨重启
  持久化。
- 超过 `RetransmitInterval` 未确认且已写网的帧，以 **DUP=1、相同 pid、
  RETAIN=0** 重发，直到收到 PUBACK。
- 重复/未知 PUBACK 幂等（删除 0 行，不报错）。
- 离线持久客户端：QoS1 行等待重连重放；QoS0 不排队（4.1 会话状态不含 QoS0）。
- `CleanSession=1`：正常 DISCONNECT 后清空该客户端所有行；同一 client id
  重连（接管）不会误清后继会话。

### 3.4 通配订阅（4.7）与 `$` 命名空间
- `/` 分层；`+` 恰好匹配一层（**含空层**：`a/+` 匹配 `a/`）；
  `#` 只能是最后且独占一层，匹配**零或多层**（`a/#` 匹配 `a`）。
- 以 `+` 或 `#` 开头的过滤器**不匹配** `$` 开头主题（如 `$SYS/..`）；
  显式 `$SYS/#` 仍可匹配。
- PUBLISH 主题名禁止含 `+/#`；非法过滤器/主题名拒绝。
- 同一消息命中同一客户端多个过滤器时，只投递一次，按最高授予 QoS。
- 授予 QoS = min(发布 QoS, 订阅 QoS)（3.8.4/4.3）。
- 生产匹配（迭代切片）与 `internal/reference/matchref`（递归下降）是两套
  独立算法；测试在手工边界表 + 穷举网格（149 过滤器 × 63 主题）上做差分，
  期望值不来自任一实现。

### 3.5 保留消息（3.3.1.3）
- `RETAIN=1`：空载荷=删除该主题的保留镜像；非空=写入/替换。
- 新订阅接受后，投递匹配的保留镜像，**仅该首次投递 RETAIN=1**；
  之后的重传/重放 RETAIN=0。
- 正常 PUBLISH 的实时扇出永远 RETAIN=0（即使主题有保留镜像）。
- 保留消息总量受 `MaxRetained` 限制；覆盖既有主题不占新名额，新主题超限
  则拒绝（`resource_limit`）。

### 3.6 遗嘱（3.1.2.6 / 3.14）
- 异常关闭发遗嘱：TCP 无 DISCONNECT 断开、keep-alive 超时、被接管、
  协议违例、资源上限、内部错误。
- 收到 DISCONNECT 或服务器优雅关闭：不发遗嘱。

### 3.7 keep-alive
- 代理读超时 = client keepalive × 1.5（可设上限）；keepalive=0 表示不启用。
  PINGREQ 重置计时；超时按异常关闭处理（触发遗嘱）。

---

## 4. 拒绝矩阵（每项都有可断言测试）

| 约定外输入 | 代理行为 | 测试 |
|---|---|---|
| 协议名非 `MQTT` / 级别非 4 | CONNACK code 1 后关闭 | `TestRejectBadProtocol{Name,Level}` |
| CONNECT 保留位(bit0) 等非法标志 | 静默协议关闭 | `TestRejectConnectReservedBit` |
| 空 client id 且 clean=0 | CONNACK code 2 | `TestRejectEmptyIDPersistent` |
| Will QoS=2 | CONNACK code 3（不支持），关闭 | `TestRejectWillQoS2` |
| 首包不是 CONNECT / 标志错 | 协议关闭，计数 invalid | `TestRejectFirstPacketNotConnect` |
| 剩余长度需第 5 字节/非最小编码 | 协议关闭 | `TestRejectBadRemainingLength` |
| 剩余长度 > broker 上限 | 关闭（limit） | `TestRejectOversizedPacket` |
| 入站 PUBLISH QoS2 / QoS3 | 关闭（unsupported/invalid） | `TestRejectPublishQoS2` |
| SUBSCRIBE 过滤器 QoS2 | **该过滤器** SUBACK 0x80，连接保留 | `TestRejectSubscribeQoS2PerFilter` |
| 非法过滤器（如 `bad#x`） | 该过滤器 SUBACK 0x80 | 同上 |
| SUBSCRIBE 固定头标志≠0x2 | 协议关闭 | `TestRejectSubscribeWrongFlags` |
| QoS1 PUBLISH / PUBACK pid=0 | 协议关闭 | `TestRejectPublishZeroPacketID` / `TestRejectPubackZero` |
| PUBLISH 主题含通配符 | 协议关闭 | `TestRejectPublishWildcardTopic` |
| PUBREL 等 QoS2 家族报文 | 关闭 | `TestRejectQoS2FamilyPacket` |
| UNSUBSCRIBE（超集特性） | 关闭（unsupported） | `TestRejectUnsubscribe` |
| 保留类型 15 / 方向错误报文(SUBACK) | 协议关闭 | `TestRejectReservedType15` / `TestRejectWrongDirectionPacket` |
| 订阅数 / inflight / 会话数 / 保留数超限 | 0x80 或 CONNACK code 3 / 关闭 + 指标 | `limits_test.go` |

> QoS2 的边界说明：QoS2 是**良构但超出本子集**的特性。对 Will QoS2 与
> 入站 QoS2 PUBLISH 直接拒绝；对 SUBSCRIBE 的 QoS2 过滤器按 3.8.3 以
> SUBACK 0x80 逐项拒绝（不静默降级为 QoS1）。

---

## 5. 资源上限与慢订阅者

| 配置 | 含义 | 触顶行为 |
|---|---|---|
| `MAX_PACKET` | 单包剩余长度上限 | 关闭连接 |
| `MAX_SESSIONS` | 持久会话总数 | 新持久会话 CONNACK code 3；既有客户端重连不受限 |
| `MAX_SUBS` | 每客户端订阅数 | SUBACK 该过滤器 0x80 |
| `MAX_INFLIGHT` | 每客户端未确认 QoS1 | 拒绝新 QoS1 投递，`rejected_inflight` 计数，行数封顶 |
| `MAX_RETAINED` | 保留消息总数 | 新主题拒绝（覆盖既有允许） |
| `QUEUE` | 每连接出站队列深度 | QoS0 满则**丢弃并计数**；QoS1 靠 inflight 重传，不阻塞发布者 |
| `SEND_WRITE_TIMEOUT` | 单帧内核写超时 | 接收窗口持续关闭的僵死客户端被断开（QoS1 行留存待重放） |

慢订阅者测试通过**真正停止读取 socket** 制造 TCP 背压（而非仅不读事件
通道），断言 QoS0 被丢弃计数且发布者永不阻塞、其他健康客户端不受影响。

---

## 6. SQLite 本地启动配置

无需额外数据库进程。默认文件库（WAL、busy_timeout、单写连接）。
`configs/mqttd.env` 给出可复制的本地配置；测试使用临时目录文件库，
另用独立用例验证**跨进程重启**（关闭 broker、同库重开、订阅与未确认
inflight 恢复重放）。

表：`sessions / subscriptions / inflight(client_id,packet_id 主键) /
retained(topic 主键)`。inflight 的 UPSERT 语义天然承载“重复包标识覆盖”，
DELETE 的 RowsAffected 用于区分真实确认与重复/未知 PUBACK。

---

## 7. 测试组织与诊断

- `internal/packet/packet_test.go`：编解码、剩余长度、各报文非法输入。
- `internal/topics/topics_test.go`：边界表 + 与独立参考实现的差分 + 穷举网格。
- `internal/store/store_test.go`：持久化、UPSERT、sent 标志跨重开、清除。
- `internal/broker/*_test.go`：
  - `protocol_test.go` 连接/PING/收发/QoS 降级/多过滤器/SP 语义；
  - `session_test.go` 离线重放、确认丢失重传、重复包标识、重复 PUBACK、clean 清理；
  - `wildcard_retained_test.go` 通配边界与保留消息；
  - `reject_test.go` 全部拒绝矩阵；
  - `limits_test.go` 资源上限与慢订阅者；
  - `lifecycle_test.go` 接管、遗嘱、keep-alive、跨重启持久化。
- `internal/brokertest`：夹具（临时 SQLite、随机端口、小而快的限流、
  日志 tee 到测试输出）。失败用例直接打印带 run/seq/stage/result 的日志。

诊断可区分四类失败：**非法输入**（reject_invalid/协议关闭）、**状态冲突**
（接管、keep-alive）、**资源超限**（reject_limit、0x80、丢弃计数）、
**运行失败**（internal_error）。

---

## 8. 支持范围与已知限制

- 仅匿名（本子集无认证库）；无 TLS。
- 无 UNSUBSCRIBE / QoS2 / 共享订阅 / 消息过期 / 主题别名（均为 3.1.1 之后或超集特性）。
- 出站 pid 分配为 inflight 表线性扫描（窗口受 `MAX_INFLIGHT` 限制，规模内可接受）。
- 单进程本地代理；持久化保证“已接收的 QoS1 不丢、可重复（DUP 标记）”，
  即**至少一次**，不是恰好一次。
