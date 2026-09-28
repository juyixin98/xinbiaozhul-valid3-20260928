# h1parse — HTTP/1.1 请求解析后端子集

从空目录实现的纯后端工程：用 **Go 标准库网络栈 + 手写 HTTP/1.1 线协议解析 +
SQLite（纯 Go 驱动）**，支持持久连接（keep-alive）、HTTP 流水线、固定长度
消息体（Content-Length）与分块消息体（Transfer-Encoding: chunked）。

**不依赖**任何生产账号、付费服务、外部网络服务或真实业务数据。唯一第三方
依赖是纯 Go（无 cgo）的 SQLite 驱动 `modernc.org/sqlite`，已在 `go.sum` 锁定。

本服务**不实现代理转发**：显式拒绝 `absolute-form`/`authority-form` 请求目标
与 `Connection: Upgrade`，从协议面消除“把转发请求当直连”的歧义。

---

## 1. 从干净环境验证

前置：Go 1.22+（不需要 gcc/cgo，不需要数据库服务进程）。

```bash
# 一键：下载依赖 → 编译/vet → 夹具一致性 → race 测试 → 真连接冒烟
./scripts/verify.sh

# 或分步
make build test race          # 编译 + 全部测试（含竞态检测）
make check-fixtures           # 校验签入夹具与生成器逐字节一致
make fuzz                     # 生产解析器 vs 独立参考解析器差分 fuzz
make run                      # 启动服务，默认 127.0.0.1:8080
```

启动后另开终端：

```bash
curl -sS -i http://127.0.0.1:8080/healthz
curl -sS -i -X POST http://127.0.0.1:8080/records \
     -H 'Content-Type: text/plain' --data 'hello-world'
# chunked（curl 在不知道长度时自动分块）
printf 'chunked-body' | curl -sS -i -X POST http://127.0.0.1:8080/records \
     -H 'Transfer-Encoding: chunked' --data-binary @-
# 裸字节（含分块扩展/trailer、TE/CL 冲突）
bash examples/raw-requests.sh
```

---

## 2. 模块关系与职责

```
cmd/
  h1parse/         服务入口：配置、SQLite 打开、信号与优雅关闭
  genfixtures/     确定性生成线协议夹具 .http + manifest.json + SHA256SUMS
  smokeclient/     裸 TCP 冒烟客户端（流水线/chunked/走私拒绝）
internal/
  protocol/        线协议编解码（无 net accept、无业务）
    errors.go        ProtoError：Kind / Phase / 绝对字节偏移 / 状态码 / 关闭策略
    reader.go        streamReader：跨 Read 拼包、绝对偏移、只认 CRLF 的 ReadLine
    request.go       Parser（每连接一个，跨请求复用缓冲）、请求行/头部/定帧裁决
    body_fixed.go    Content-Length 定帧 + drain 到精确边界
    body_chunked.go  chunked 解码状态机（size/ext/data/CRLF/trailer）
    response.go      响应编码（稳定头部顺序，便于逐字节夹具比对）
  server/          连接状态机：accept、持久连接循环、Expect:100、错误→响应/关闭
  handler/         业务处理：路由、读取完整消息体、内容寻址入库
  storage/         SQLite：records 表（内容寻址 sha256、幂等 Put/Get）
  config/          H1PARSE_* 环境变量配置
  logging/         结构化日志（人类可读或 JSON Lines）
test/
  fixtures/        签入的 35 个逐字节夹具 + manifest.json + SHA256SUMS
  ref/             ★ 独立参考解析器（不导入 internal/protocol，整消息扫描算法）
  e2e/             真实 TCP 回环：夹具驱动、逐字节喂入、走私、net/http 互操作
```

**输入输出与错误契约**

| 层 | 输入 | 输出 | 错误契约 |
|---|---|---|---|
| `streamReader` | `io.Reader` 原始字节 | 去 CRLF 的行 / 精确 n 字节 | 非法换行=400；超限=431；中途 EOF=`incomplete` |
| `Parser.Next` | 持久连接缓冲 | `*Request`（头+定帧）+ `BodyFrame` | 定帧冲突=400/413/501/505；绝不猜长度 |
| `BodyFrame` | — | 解码后的表示字节 | chunk 词法错=400；截断=`incomplete`；超限=413 |
| `server` | `*Request`/错误 | 至多一条响应 + keep-alive/close | 坏请求回错误码后**关闭**；截断静默关闭 |
| `handler` | 已定帧请求 | JSON 响应 | 路由/411/体错误，体错误强制关闭 |
| `storage` | 已完整读出的字节 | 内容寻址记录 | 仅数据库错误（500），不接触线协议 |

---

## 3. 连接状态机与消费边界（请求走私防御核心）

每条 TCP 连接**只创建一个 `protocol.Parser`**。它内部的 `streamReader` 会因
`Read` 成块返回而把流水线中“后续请求”的字节也缓冲进来——这些字节与正在
处理的请求共享同一缓冲：

1. `Parser.Next()` 解析请求行+头部，完成定帧决策，返回绑定在**同一缓冲**上的
   消息体帧；
2. 业务层读消息体（固定帧读满 n 字节；chunked 帧读到 `0\r\n` + 终止空行）；
3. 业务返回后状态机调用 `BodyFrame.Close()` **drain 残余消息体**，使下一次
   `Next()` 从精确边界开始；
4. 任何定帧/词法/截断/超限错误都使连接关闭，**残余字节绝不被当作下一请求**。

> 反例（本实现刻意避免）：若每个请求新建解析器，或用独立 bufio 预读后丢弃，
> 流水线第二请求的字节就可能丢失或错位。

### 3.1 定帧裁决（头部解析完成后一次性、确定性地决定）

- **TE 与 CL 同时出现 → 400 关闭**（RFC 9112 §6.2.1 的 CL.TE/TE.CL 歧义），
  即使两个数值“看起来一致”也拒绝——定帧语义不允许二义；
- **重复 Content-Length**：按十进制数值比较，数值不同 → 400；仅前导零
  差异（`5` 与 `05`）接受；逗号列表（`5, 5`）拒绝；非数字/溢出 → 400；
- **Transfer-Encoding**：仅支持 `chunked`，且必须是最末编码；
  `identity`/`gzip` 等 → 501；`chunked` 带参数 → 400；
- **chunk-size** 必须从行首就是 `1*HEXDIG`，无前导/尾随空白（宽容空白是
  经典的接收端解析差异）；溢出 63 位 → 400；
- **chunk-ext** 语法受限：`;name` 或 `;name="value"`/token，尾随分号、空名、
  空值拒绝（未知扩展被忽略但仍校验）；
- **chunk-data 后必须精确 CRLF**；last-chunk 后必须有终止空行（`0\r\n` 后
  直接 EOF 是截断，不是零长度体）；
- 仅承认 **CRLF** 为行终止：裸 LF、游离 CR、EOF 时悬留的 CR 一律 400；
- 声明体/单 chunk 超上限 → 413；头部段超上限 → 431。

### 3.2 错误类别（四类可区分，不把不确定包装成成功）

| Kind | 含义 | 状态码 | 连接处置 |
|---|---|---|---|
| `invalid_request` 等 | 非法输入/定帧冲突/状态冲突 | 400/501/505/417 | 回响应后关闭 |
| `header_too_large` | 头部段超限 | 431 | 回响应后关闭 |
| `payload_too_large` | 体/chunk 超限 | 413 | 回响应后关闭 |
| `incomplete` | 截断（对端在消息结束前关闭） | 不回响应 | 静默关闭 |
| （非 ProtoError） | 服务端运行失败 | 500/日志 error | 关闭 |

每个错误都带 **Phase**（request-line/header/framing/body/chunk-size/
chunk-data/trailer）和**自连接首字节计的绝对字节偏移**，日志据此定位。

---

## 4. 半包与粘包、大小限制

- **半包**：`streamReader` 跨多次底层 `Read` 累积，逻辑读（`ReadLine`/
  `readFull`）只在拿到完整行/满 n 字节后返回；e2e 有逐字节（300µs/字节）
  喂入测试，结论与整块发送一致。
- **粘包**：见第 3 节，单 Parser + drain 保证边界。
- 限制（可通过环境变量覆盖）：

| 配置 | 默认 | 环境变量 |
|---|---|---|
| 头部段上限 | 16384 B | `H1PARSE_MAX_HEADER_BYTES` |
| 消息体上限 | 1048576 B | `H1PARSE_MAX_BODY_BYTES` |
| 单 chunk 上限 | 262144 B | `H1PARSE_MAX_CHUNK_BYTES` |
| 空闲超时 | 30s | `H1PARSE_IDLE_TIMEOUT` |
| 监听地址 | 127.0.0.1:8080 | `H1PARSE_ADDR` |
| SQLite DSN | `h1parse.db`（文件）/`:memory:` | `H1PARSE_DB` |
| JSON 日志 | 关 | `H1PARSE_LOG_JSON=1` |

---

## 5. 业务面（协议入口）

| 方法 路径 | 行为 |
|---|---|
| `GET /healthz` | `{"status":"ok"}` |
| `GET /` | 服务信息与路由清单 |
| `POST /records` | 读完整定帧消息体，按 sha256 内容寻址幂等入库，201 + `Location` |
| `GET /records/{id}` | 取回记录（id 为 16 位小写十六进制） |
| 其余 | 404；路径匹配但方法不符 → 405 + `Allow` |

`POST /records` 无 Content-Length 且非 chunked → 411（不擅自假设空体）。

---

## 6. 测试与夹具

### 6.1 三层测试

1. **协议单元测试** `internal/protocol/*_test.go`：每条拒绝条件一个可断言
   用例（TE/CL、重复长度、逗号 CL、溢出、裸 LF/CR、坏 chunk 各形态、
   截断、413/431、流水线绝对偏移……）；逐字节/分包喂入；半包粘包。
2. **差分测试 + Fuzz** `internal/protocol/diff_test.go`：生产流式解析器与
   独立参考解析器 `test/ref` 比对 accept/reject/truncated 三态、已接受请求
   数、解码体与 chunked 标志；`go test -fuzz=FuzzDifferential` 已跑过
   **约 1400 万次**无分歧。fuzz 过程中实际发现并修复了多个解析器缺陷
   （见下文“fuzz 发现的问题”）。
3. **真实连接 e2e** `test/e2e/`：TCP 回环 + 真 SQLite，夹具整块与逐字节
   两种喂法、TE/CL 走私黑盒断言（内嵌伪造请求绝不产生第二响应）、
   日志偏移断言、chunked 体 sha256 对账、标准库 `net/http` 客户端互操作、
   优雅关闭。

另有 `internal/storage`、`internal/handler`、`internal/config`、
`internal/logging` 的独立测试。

### 6.2 独立参考实现（不是同一段代码生成期望值）

`test/ref` **不导入 `internal/protocol`**，字符判定/数值解析/状态机全部另写，
且**算法不同**：生产端是随时间到达的流式状态机，参考端要求一次性给全一条
请求、用整体扫描+位置表解析。两者视角不同（“字节如何到达” vs “完整串应如何
解释”），差分才能抓到单边错误。类别字符串也刻意不同，由测试建立等价映射。

### 6.3 夹具

`test/fixtures/*.http` 由 `cmd/genfixtures` 确定性生成并**签入**，配合
`.gitattributes` 的 `-text` 强制逐字节保留 CRLF。`manifest.json` 记录每个
夹具的期望（accept/reject/truncated、状态码、kind、phase、偏移、流水线接受
数）与 sha256；e2e 加载时校验 sha256，`-check` 防止生成器与签入文件漂移。

夹具覆盖：合法参考（GET、固定体、chunked+扩展+trailer、流水线、OPTIONS *、
Expect:100）、非法换行、请求行/头部词法、TE/CL 与重复 CL 走私、chunked 各
坏形态、截断（头/固定体/chunk-data/trailer）、体内嵌伪造请求、各类超限。

### 6.4 fuzz 差分发现并修复的真实问题（留档）

- chunked 体在头部后直接 EOF 被误当“零长度体”（应为截断）；
- last-chunk 后缺终止空行被当作正常结束；
- chunk-size 宽容前导空白（解析差异走私面），改为严格；
- `3;` 尾随分号、`;=0` 空扩展名、空/非法 ext 值被漏检；
- `isToken(空串)` 误判为真导致空扩展名通过；
- EOF 时悬留 CR 的归类（非法 vs 截断）在请求行/头部/体不同位置统一；
- chunk-data 拷贝在跨多次 Read 时的切片边界错误；
- 参考端漏检头部段超总长、trailer 空字段名、ext 值缺失等（差分反向抓参考 bug）。

---

## 7. 日志

每行含 `ts / level / run=<运行标识> / stage=<阶段>`，协议错误附
`conn / req / kind / phase / offset / rel / detail`。示例：

```
2026-09-28T05:05:10Z warn  run=run-a1b2c3 stage=read-request protocol error -> 400; closing connection conn=c0007 req=1 kind=invalid_request phase=framing offset=58 detail=request contains both Transfer-Encoding: chunked and Content-Length ...
```

- 正常请求：`info stage=request ... status=201 chunked=true keep_alive=true`；
- 客户端可归因（非法/冲突/超限）：`warn`；
- 存储/IO/panic 等运行失败：`error`，且 panic 被 recover，该连接回 500 后关闭。

测试日志使用固定 run 标识（`run-unit-*`/`run-e2e-0001`），并断言
run/conn/req/阶段/序号/字节偏移齐全。

---

## 8. 支持范围与明确不做的事

**支持**：HTTP/1.1；持久连接与流水线；Content-Length 与 chunked 两种定帧；
Expect: 100-continue；HEAD（发 Content-Length 不发体）；origin-form 与
`OPTIONS *`；trailer 与 chunk-ext（受限语法、未知扩展忽略）。

**不做**：HTTP/2、TLS（明文 HTTP/1.1）、代理/转发（拒绝 absolute-form、
authority-form、Upgrade）、gzip 等内容编码、鉴权与虚拟主机路由、
obsolete 行折叠（obs-fold）、服务端响应 chunked（响应统一精确
Content-Length）。这些限制让定帧边界始终确定。

---

## 9. 持久化与本地启动配置

默认在工作目录创建文件型 `h1parse.db`（WAL）。也可用内存模式（无需任何
外部进程）：

```bash
H1PARSE_DB=:memory: make run
```

schema 由程序启动时 `CREATE TABLE IF NOT EXISTS` 自建（见
`internal/storage/sqlite.go`），不需要单独迁移步骤。
