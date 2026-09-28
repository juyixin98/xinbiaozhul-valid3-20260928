# 协议决策记录（MQTT 3.1.1 子集）

本文件记录代码之外需要明确的语义选择，便于复核。规范条目以
MQTT v3.1.1 OASIS Standard 为准。

## D1. QoS2 的拒绝点

| 出现位置 | 选择 | 理由 |
|---|---|---|
| Will QoS=2 (CONNECT) | CONNACK 0x03 Server Unavailable 后关闭 | 题目要求“不支持 QoS2 时明确拒绝约定外输入”；连接尚未建立，用 CONNACK 表达 |
| SUBSCRIBE 单 filter QoS=2 | SUBACK 该位回 0x80，连接继续 | 规范允许 SUBACK 失败码表达“不授权该 QoS”，且同一包中其他 filter 应独立生效 |
| 入站 PUBLISH QoS=2 | 协议违规关闭 | 子集无 QoS2 投递状态机，继续解析会产生无法收敛的状态 |
| PUBREC/PUBREL/PUBCOMP | ErrUnsupported 关闭 | 无对应流 |

## D2. 裸 TCP EOF 的 Will 判定

MQTT-3.14.4 仅把“收到 DISCONNECT 包”定义为干净断连。READY 状态读到
io.EOF 属于网络异常断开 → 发布 Will。只有 CONNECT 之前的 EOF
（从未建立会话）不产生 Will。

## D3. 剩余长度最小性

规范只给出了 1–4 字节与上限，但非最小编码（如 `80 00` 表示 0）
是有歧义的 BER 式编码。本实现按“严格校验”要求拒绝一切末位数字
为 0 的多字节编码。128 的合法编码 `80 01` 首数字为 0，不受影响。

## D4. 持久化与发送的顺序（避免未定义结果）

QoS1 投递：先 `PutInflight`（含 packet id），再入发送队列。
- 入队前进程崩溃：重连 DUP=1 重投（客户端没收到也安全）。
- 入队后未确认断连：同上。
- PUBACK 到达才 `DeleteInflight`。

因此不存在“broker 认为已投递但无任何持久记录”的窗口，也就不会把
不确定状态报成成功。

## D5. 重叠 filter 与重复消息

同一会话多个 filter 命中同一 PUBLISH 时，规范文字是每个匹配 filter
都“MUST receive”。本子集选择**投递一次**（取最高授权 QoS），避免在
无 message-id 去重设施下制造必然的重复交付。这是本地子集的显式取舍，
已在 README 与代码注释中声明；QoS1 的跨重连重复（DUP）不受影响。

## D6. 慢订阅者

发送队列与 inflight 均有界。任一耗尽时：
1. 关闭该订阅者连接（resource_exhaustion），不阻塞发布者；
2. inflight 行保留，重连 DUP=1 重投；
3. 离线期间新消息进入 offline 队列，超限丢最旧。

发布者的 PUBACK 不因子嗣慢而丢失（broker 接收即确认）。

## D7. 保留消息 QoS 与重订阅

- RETAIN 帧仅在 SUBSCRIBE 响应路径设置 RETAIN 位；Will 的实时分发
  RETAIN=0，只有存储副本在新订阅时 RETAIN=1。
- 空 payload RETAIN=1 表示删除保留（常见约定，MQTT-3.3.1-3/4）。
- broker 不主动给现有订阅者重发保留消息。

## D8. $ 主题

实现了 `$` 逃逸（`#`、`+` 首层不匹配 `$` 主题）但 broker 自身
不发布任何 `$` 主题；该规则主要保证边界行为确定、可测。
