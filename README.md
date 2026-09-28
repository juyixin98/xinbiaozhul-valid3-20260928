# http11subset — 严格的 HTTP/1.1 请求解析后端子集

一个从零实现的 HTTP/1.1 服务端**请求解析**子集：持久连接（keep-alive）、
流水线（pipelining）、`Content-Length` 固定长度体与 `Transfer-Encoding:
chunked` 分块体（含分块扩展 `chunk-ext` 与尾部字段 `trailer-part`）。
不使用 Go 的 `net/http` 做帧解析——所有 RFC 9112 的组帧规则都在本仓库中
独立实现，以便确定性地拒绝歧义与请求走私形态。

业务面是一个本地 JSON 记录服务，数据存于本地 **SQLite**（纯 Go 驱动
`modernc.org/sqlite`，**无需 CGO、无需任何账号/付费服务/外网数据**）。

> 明确不做：**不实现任何代理转发**。absolute-form 请求目标直接被拒绝；
> 只接受 origin-form（以及 `OPTIONS *`）。响应始终带显式 `Content-Length`，
> 不产生分块响应。

---

## 1. 快速开始（干净环境）

依赖：Go 1.22+、`python3`（仅验证脚本的裸 TCP 检查用）、`bash`、`make`。

```bash
# 方式 A：使用已提交的 vendor 目录，完全离线构建
make offline-build

# 方式 B：常规构建（首次需要 go mod download）
make build

# 一键验证：go vet + 夹具一致性 + go test -race + 真实 TCP 冒烟
make verify            # 或 ./scripts/verify.sh

# 启动服务（默认 127.0.0.1:8080，SQLite 在 ./data/app.db）
make run PORT=8080 SQLITE=./data/app.db
```

`scripts/verify.sh` 会自己在临时目录构建并启动一个服务、跑完冒烟后清理，
不污染你的数据文件。

### 示例请求

```bash
# 健康检查
bash examples/requests.sh health   | nc -q1 127.0.0.1 8080

# 固定长度体
bash examples/requests.sh fixed    | nc -q1 127.0.0.1 8080

# 分块体（带 chunk-ext 与 trailer，大小按真实字节数自动转 hex）
bash examples/requests.sh chunked  | nc -q1 127.0.0.1 8080

# 一次写入两条流水线请求
bash examples/requests.sh pipeline | nc -q1 127.0.0.1 8080

# 故意非法：TE 与 CL 同时存在 → 400 后关闭，残余走私请求不被处理
bash examples/requests.sh reject   | nc -q1 127.0.0.1 8080
```

也可用 curl：

```bash
curl -i http://127.0.0.1:8080/healthz
curl -i -X POST http://127.0.0.1:8080/v1/records \
  -H 'Content-Type: application/json' --data '{"title":"a","payload":"b"}'
curl -i http://127.0.0.1:8080/v1/records/1
```

---

## 2. 模块关系与职责

```
cmd/server/            进程入口：装配配置、存储、日志、监听
internal/
  httpx/               协议核心（无 net/http 参与组帧）
    stream.go          字节窗口流：半包/粘包统一处理，绝对字节偏移
    grammar.go         ABNF 字符谓词（tchar / HEXDIG / field-vchar ...）
    request.go         Request / 有序大小写不敏感 Header / KeepAlive
    head.go            请求行、头字段、Content-Length/TE 冲突裁决
    body.go            Parser：固定体 + chunked 体（ext/trailer/限制）
    conn.go            每条连接的显式状态机（顺序、关闭策略）
    response.go        响应编码（始终显式 Content-Length）
    errors.go          统一错误契约 *Error：Kind/Phase/Offset/Code/Basis
    logger.go          JSONL 结构化日志（拒绝事件带字节偏移）
    limits.go          头部/体/分块行大小限制
    version.go         解析器版本标识（写入测试与运行日志）
  config/              JSON 配置 + HTTP11_* 环境变量覆盖
  storage/             SQLite 存储（modernc.org/sqlite，纯 Go）
  app/                 业务处理：/healthz、/v1/records（无组帧代码）
  server/              TCP 监听、每连接 deadline、优雅关闭
  refparse/            ★独立参考解码器（仅测试，见 §6）
test/
  genfixtures/         夹具生成器：手工线序字节 + 手工期望值
  testdata/fixtures.json  52 个逐字节夹具（合法/冲突/截断/走私/超限）
  testutil/            运行日志器、确定性“滴水”阅读器、夹具加载
  *_test.go            夹具驱动测试、net.Pipe 连接集成、限制/分片测试
examples/              逐字节示例请求（CRLF 精确）
scripts/verify.sh      干净环境一键验证
configs/local.json     本地配置样例
```

依赖方向是单向的：`cmd → {config,storage,app,server,httpx}`，
`app → {storage,httpx}`，`server → {config,httpx}`；**`httpx` 不依赖任何
上层包**，可独立测试与复用。

### 输入/输出与错误契约

- 解析器入口 `Parser.ReadHead()` / `Parser.ReadBody(*Request)`，工作在任意
  `io.Reader` 上。
- 成功：`*Request` 持有方法、目标、版本、有序头、帧模式、**已完整消费**的
  `Body`，以及 `HeadOffset` / `BodyEndOffset`（下一消息的绝对起点）。
- 失败：**始终**是 `*httpx.Error`（或 `errors.As` 可取出），字段为：

  | 字段 | 含义 |
  |---|---|
  | `Kind`  | `invalid_syntax` / `protocol_conflict` / `state_conflict` / `resource_limit` / `unsupported` / `io_failure` |
  | `Phase` | 出错阶段（读请求行/解析请求行/读头/裁决组帧/读固定体/读分块…） |
  | `Offset`| 连接字节流上的**绝对字节偏移** |
  | `Code`  | 建议 HTTP 状态码；`0` 表示无法安全响应、直接静默关闭 |
  | `Basis` | 判定依据（RFC 条款或配置规则） |

- 对端在请求边界干净关闭且无残留字节 → 返回哨兵 `ErrCleanEOF`（正常），
  与“消息读到一半 EOF（截断）”严格区分。

---

## 3. 状态机语义与边界规则

每条连接一个 `httpx.Conn`，严格串行：

```
loop:
  ReadHead()            # 请求行 + 头 + 空行；干净空闲 EOF 才允许退出
    └─ 解析请求行/头 → resolveFraming() 裁决帧
  (可选 Expect:100-continue → 先发 100)
  ReadBody()            # 按帧精确消费到字节边界
  Handler.ServeHTTP()   # 业务处理（与组帧无关）
  flush(响应, 显式 Content-Length)
  若 keep-alive 且无错误 → 回到 loop；否则关闭
```

**关键边界保证**

1. **半包 / 粘包**：解析器只在“需要更多字节”时填充内部窗口，从不假设一次
   `Read` 对应一条消息；也从不丢弃多读的字节。固定体按计数读，分块体按
   `chunk-size` 读，**绝不对体内容扫描 CRLF**——体里出现 `\r\n` 不会让帧错位。
   测试用“逐字节 / 1-2-3 周期 / 7 字节块 / 20 种随机分片”四种喂入方式证明
   决策与消费边界与分包无关。
2. **精确消费**：一条消息消费完，游标正好落在下一条流水线请求的首字节；
   夹具用 `remainder` 字段逐字节断言残余内容。
3. **坏请求关闭**：任何解析 `*Error` 都终结连接；可安全定位的错误
   （400/413/431/417/501/505）尽力回**一个**错误响应再关闭，截断类
   （`Code=0`）与 IO 失败**静默关闭**，因为不存在安全的响应/消息边界。
   绝不让残余的体字节或“走私”请求被当作下一请求处理。

### 明确拒绝条件（每项都有可断言测试）

- **TE 与 CL 同时存在** → `protocol_conflict` / 400（无论顺序，CL.TE 与 TE.CL）。
- **多个 Content-Length 值不一致**（重复字段或逗号列表，如 `8` vs `09`）
  → `protocol_conflict` / 400；一致（`42, 42`）才接受。
- **非法换行**：请求行/头/分块帧中出现非 CRLF 的裸 LF → `invalid_syntax`；
  废弃的折行（obs-fold，行首 SP/HTAB 续行）作为非法字段名拒绝；
  字段名与冒号之间的空格（历史走私缝隙）拒绝；字段值中的 NUL/控制字节拒绝。
- **请求行**：必须恰好两个单 SP 分隔（HTAB 不行）；方法须为 token；目标仅
  origin-form / `*`（absolute-form 因不做代理而拒绝）；版本仅 HTTP/1.0/1.1；
  HTTP/1.1 必须有且仅有一个 `Host`。
- **Content-Length**：仅十进制、无符号、无空白；越界/非数字拒绝。
- **Transfer-Encoding**：本子集只接受**单独的** `chunked`；`identity`、
  `gzip, chunked`、`chunked, chunked`、带参数等一律拒绝（避免靠编码名混淆
  改变帧决策）。
- **chunked 语法**：`1*HEXDIG`（手写解析，int64 溢出拒绝）、合法 `chunk-ext`
  （含带引号值，拒绝裸 CR/LF）、chunk-data 后必须精确 CRLF、last-chunk 后
  尾部字段中禁止出现 `Content-Length`/`Transfer-Encoding`/`Host`。
- **截断**：头部/固定体/分块数据/分块行/尾部在 EOF 前不完整 →
  `invalid_syntax` 且 `Code=0` 静默关闭；完整请求后跟截断的第二个流水线
  请求同样拒绝（夹具 `reject-partial-pipelined-second`，解析两次后断言）。
- **资源超限**：头部段（431）、体（413）、单条分块行，均有硬上限。

默认限制：头部 64 KiB、体 1 MiB、分块行 8 KiB（见 `configs/local.json`）。

---

## 4. 持久连接 / keep-alive 语义

- HTTP/1.1 默认持久；出现 `Connection: close` 则该响应后关闭。
- HTTP/1.0 默认关闭；`Connection: keep-alive` 才持久。
- 流水线请求按到达顺序**严格串行**处理，响应顺序与请求一致（有集成测试断言
  一次写入两条、收到且仅收到两条有序响应）。
- 支持 `Expect: 100-continue`（可在配置中关闭）；其它 `Expect` 值 → 417。

---

## 5. 日志：可定位到字节

服务对每个解析失败输出一行 JSON（stderr），示例：

```json
{"event":"parse_error","conn":"c000003","seq":1,"kind":"protocol_conflict",
 "phase":"resolve_framing","offset":18,"code":400,
 "basis":"RFC9112 §6.1: TE and CL must not be combined",
 "message":"Transfer-Encoding and Content-Length both present"}
```

`conn`（连接标识）、`seq`（该连接上的请求序号）、`offset`（绝对字节偏移）、
阶段、类别、依据齐全。测试运行还会在 `test/out/runlog.jsonl` 记录每次判定的
运行标识 `run_id`、解析器版本、阶段、序号与计算步骤。

错误四分类（非法输入 / 协议或状态冲突 / 资源超限 / IO 失败）通过 `Kind`
即可区分，**无需匹配错误字符串**；截断/未收敛结果绝不会被包装成成功或正常
EOF（`TestErrorKindsDistinct` 专门断言）。

---

## 6. 测试策略与独立参考路径

- **逐字节夹具**：`test/genfixtures` 用显式 `\r\n` 线序片段拼出 52 个用例，
  期望值（方法、体、帧、拒绝类别/阶段/状态/偏移、残余边界）**手工书写**，
  不由被测代码生成。涵盖合法语法参考、流水线、分块扩展/尾部、各类截断与
  CL.TE / TE.CL 请求走私歧义。
- **独立参考实现**：`internal/refparse` 是一个**与生产解析器不共享任何代码**
  的第二解码器（全量缓冲、`bytes.Split` 切行、`map` 聚头、独立写法）。每个
  合法夹具都同时被两者解码，必须在方法、目标、版本、体与**消费字节数**上
  一致。期望值因此不是“用同一实现生成”。
- **半包/粘包**：合法夹具在 4 种确定性喂入模式 + 每例 20 个随机种子下复跑，
  决策与边界必须稳定。
- **连接级集成**：`net.Pipe` 上跑真实 `httpx.Conn`，断言 keep-alive、
  流水线响应顺序、TE/CL 冲突回 400 并关闭且 `/smuggled` 绝不分发、截断静默
  关闭、413 关闭、chunked 端到端、以及真实 app+SQLite 全链路。
- **真实 TCP 冒烟**：`scripts/verify.sh` 启动临时服务用裸 socket 复核全部
  上述路径。

运行：

```bash
make fixtures     # 重新生成 test/testdata/fixtures.json
make test         # 全部 Go 测试
make race         # -race 竞态检测
make verify       # vet + 夹具一致性 + race + 真实 TCP 冒烟
```

最近一次在本机的实际执行结果见本仓库交付说明；`make verify` 退出码 0 即通过。

---

## 7. API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | `/healthz` | 健康检查，返回记录计数 |
| POST | `/v1/records` | 建记录，JSON `{"title":..,"payload":..}`，201 + `Location` |
| GET  | `/v1/records/{id}` | 取记录，404/400 区分 |

### 配置

`configs/local.json`（缺失则用内置默认值），并支持环境变量覆盖：
`HTTP11_LISTEN`、`HTTP11_SQLITE_PATH`、`HTTP11_MAX_HEADER_BYTES`、
`HTTP11_MAX_BODY_BYTES`、`HTTP11_MAX_CHUNK_LINE`、`HTTP11_*_TIMEOUT_MS`、
`HTTP11_ENABLE_100_CONTINUE`。

数据库为本地文件（自动建表、WAL），测试用 `:memory:`。无需多进程编排；
唯一进程即 HTTP+SQLite 服务。

---

## 8. 支持范围与限制

- 支持：HTTP/1.0、HTTP/1.1 的 origin-form/`*` 请求；持久连接；流水线；
  `Content-Length` 与单独 `chunked`（含 ext/trailer）；`Expect: 100-continue`。
- 不支持（明确拒绝而非猜测）：HTTP/2+、absolute-form（代理语义）、非 chunked
  传输编码、TE/CL 共存、obs-fold、TLS（本工程为纯 HTTP/1.1 后端，TLS 终止应
  在其外侧）、认证/压缩等业务特性。
- 响应整体缓冲后以显式 `Content-Length` 发出（面向小 JSON 响应，刻意保证响应
  边界平凡精确），不支持流式/分块响应。
