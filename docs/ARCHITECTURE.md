# 架构与状态语义（设计说明）

本文给出各层的**输入/输出契约**、连接状态机与关键算法的形式化语义，
作为 `README.md` 的补充。所有引用章节号均指 OASIS MQTT v3.1.1。

## 1. 分层契约

```
TCP byte stream
   │  (net.Conn)
   ▼
internal/packet.ReadFrame ──([]byte frame)──► 各 Decode*  ──► 类型化报文
   ▲                                                              │
   │ Encode*                                                      │ 仅做
   │                                                              │ 线缆合法性
   │                                              错误 *packet.Error{Category}
   ▼
internal/broker (连接状态机 + 业务策略)
   │  使用                          使用                         │
   ├─► internal/topics  (ValidateName/ValidateFilter/Match)
   ├─► internal/store   (SQLite: sessions/subscriptions/inflight/retained)
   └─► internal/logx    (run/seq/stage/result 结构化诊断)
```

- `packet` 层**不依赖**任何业务状态：输入字节，输出结构或带类别的错误。
- `topics` 层是纯函数（校验/匹配），无 I/O。
- `store` 层是唯一的持久化边界；写方法内部加互斥，方法级即事务边界。
- `broker` 层拥有连接状态机、内存订阅 trie、每连接 goroutine 与指标。

## 2. 错误类别 → 结局映射

| `packet.Category` | 触发示例 | 连接结局 | 可观测信号 |
|---|---|---|---|
| `invalid_input` | 剩余长度非法、pid=0、保留位、主题含通配符 | 关闭（协议违例） | `result=reject_invalid`，`frames_invalid` |
| `unsupported` | QoS2 PUBLISH、UNSUBSCRIBE、协议名/级别不符 | CONNACK 1 或关闭 | `result=reject_unsupported`，`frames_unsupported` |
| `resource_limit` | 包过大、会话/订阅/inflight/保留超限 | CONNACK 3 / SUBACK 0x80 / 关闭 | `result=reject_limit`，对应 reject 指标 |
| `state_conflict` | 同 id 接管、keep-alive 超时 | 关闭旧连接 | `result=state_conflict` |
| `internal` | SQLite 写失败等运行时错误 | 关闭 | `result=internal_error` |

不确定/未收敛的结果不映射为 accept：例如保留消息超限时该 PUBLISH 被
拒绝并关闭，而不是被悄悄接纳。

## 3. 连接状态机

```
        TCP accepted
             │
             ▼
      [AWAIT_CONNECT]  ──读且仅读一个 CONNECT
   非法/截断/首包错 ──► CLOSE(协议)
   协议名/级别不符   ──► CONNACK(rc=1) ──► CLOSE
   空 id+clean=0     ──► CONNACK(rc=2) ──► CLOSE
   will QoS2 / 超限  ──► CONNACK(rc=3) ──► CLOSE
   合法              ──► 会话解析(接管/清理/新建) ──► CONNACK(SP,rc=0)
             │
             ▼
       [SESSION_READY]  写泵 + deliveryPump + 读循环并行
   PUBLISH  ─► 校验→保留处理→路由→(QoS1)PUBACK
   SUBSCRIBE─► 逐过滤器校验/限流→SUBACK→保留镜像投递
   PUBACK   ─► 删除 inflight(pid)，幂等
   PINGREQ  ─► PINGRESP（并重置读超时）
   DISCONNECT► reason=client_disconnect（不发遗嘱）
   越界报文 ──► 按上表 CLOSE
   EOF/RST  ──► reason=abnormal_close（发遗嘱）
   keepalive─► reason=keepalive_timeout（发遗嘱）
   被接管   ──► reason=session_takeover（旧连接发遗嘱）
             │
             ▼
         [TEARDOWN]  遗嘱判定 → deregister(clean 则清库) → close(socket)
```

CONNACK 的 Session Present（3.2.2.2）**只**反映持久会话存在：
clean 连接（包括接管一个 clean 在线连接）SP 恒为 0。

## 4. 出站 QoS1 投递状态机（deliveryPump）

每个在线连接一个 pump，它是 broker→client QoS1 入队的**唯一属主**，
以 SQLite inflight 表为权威状态，用内存 `map[pid]*pendingQ1` 记录
“当前 sendCh 中是否有副本 / 是否已写网 / 最近写网时刻”：

```
                 persist(行 sent=0)
   (无) ───────────────────────────────► 未入队
                                          │ reconcile 入队成功
                                          ▼
                                       已入队(sendCh)
                                          │ writePump 写网成功
                                          ▼
                                       已写网(sent=1)
                                   ┌──────┴────────┐
                          PUBACK→删除行         超过重传间隔
                          (map 删除)           重新入队 DUP=1
```

关键不变量：
1. 任一 pid 在 sendCh 中至多有一份（已入队则 reconcile 跳过）。
2. 只有“已写网”的帧才允许重传，且重传统一 DUP=1、RETAIN=0。
3. **新连接的首次 reconcile 对所有既有行置 DUP=1**（重连/重启重放）。
4. PUBACK 先删表行，pump 在下一次 reconcile/ack 信号中从内存移除。
5. QoS1 在**入队之前**已持久化，故慢消费者写失败、进程崩溃均不丢消息。

## 5. 通配匹配

生产实现（迭代，O(层级数)，内存 trie 路由）与独立参考实现
（`internal/reference/matchref`，递归下降）算法不同。统一规则：

- 以 `Split(s,"/")` 得到层级序列，尾随 `/` 产生空末层。
- `+` 精确消费一层（空层也算）；`#`（必为最后且独占一层）吸收零或多层。
- 首层为 `+`/`#` 时不匹配首字符 `$` 的主题。
- 路由 trie 支持 `next`（具体层）/`plus`/`hash` 三类边；`#` 节点在“主题
  层级恰好耗尽”处也命中（`a/#` 命中 `a`）。

## 6. 持久化表

| 表 | 主键 | 用途 |
|---|---|---|
| sessions | client_id | clean 标志、时间戳；SP 与重放判定依据 |
| subscriptions | (client_id, filter) | 重启重建 trie |
| inflight | (client_id, packet_id) | 未确认 QoS1；UPSERT 承载重复 pid 覆盖；`sent` 决定 DUP |
| retained | topic | 保留镜像；空载荷即删除 |

SQLite 以 WAL、busy_timeout、单写连接运行，纯 Go 驱动，无需外部进程。
