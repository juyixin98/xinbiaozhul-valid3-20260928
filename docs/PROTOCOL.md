# 协议裁决说明（RFC 对照）

本文件记录本实现对 HTTP/1.1 子集的每个关键裁决、对应 RFC 条款，以及在
`test/testdata/fixtures.json` 中的夹具 id。期望行为以 RFC 9110（语义）与
RFC 9112（HTTP/1.1 报文）为准。

## 行结束与空白

| 裁决 | 依据 | 夹具 |
|---|---|---|
| 行终止符严格为 CRLF，裸 LF 拒绝 | RFC 9112 §2.2 | `reject-bare-lf-request-line`, `reject-bare-lf-header`, `reject-chunk-bare-lf` |
| 拒绝 obs-fold（行首 SP/HTAB 续行） | RFC 9110 §5.2（field-value 不再允许折行） | `reject-obsolete-line-folding` |
| 字段名与 `:` 之间不允许空白 | RFC 9112 收紧的历史走私缝隙 | `reject-space-before-colon` |
| 字段值不允许裸 CR/LF/NUL/控制字节 | RFC 9110 §5.5 | `reject-nul-in-value`, `reject-embedded-request-in-field` |
| 允许 obs-text（0x80–0xFF）值，字段名大小写不敏感 | RFC 9110 §5.1/5.5 | `valid-obs-text-value` |

## 请求行

| 裁决 | 依据 | 夹具 |
|---|---|---|
| `method SP request-target SP HTTP-version`，恰两个单 SP | RFC 9112 §2.1 | `reject-no-sp`, `reject-tab-separators`, `reject-method-space` |
| method 为 token | RFC 9110 §9.1 | `reject-method-space` |
| 仅 origin-form 与 `OPTIONS *`；absolute-form 拒绝（无代理） | RFC 9110 §7.1, §9.3 | `reject-absolute-form`, `valid-options-asterisk` |
| 版本仅 HTTP/1.0 / HTTP/1.1 | RFC 9110 §2.5 | `reject-http2-version` |
| HTTP/1.1 必须恰一个 Host | RFC 9110 §7.2 | `reject-missing-host`, `reject-duplicate-host` |

## 组帧：Content-Length 与 Transfer-Encoding

| 裁决 | 依据 | 夹具 |
|---|---|---|
| TE 与 CL 同时出现 → 400 关闭 | RFC 9112 §6.1 | `reject-te-and-cl`, `reject-cl-and-te-reversed`, `reject-smuggle-cl-te`, `reject-smuggle-te-cl` |
| 多个 CL 值不一致 → 400（重复字段/逗号列表） | RFC 9112 §6.3 | `reject-cl-differing-fields`, `reject-cl-differing-list`, `reject-cl-numeric-different-encoding` |
| 一致的逗号列表允许 | RFC 9112 §6.3 | `valid-cl-consistent-list` |
| CL 仅十进制、无符号、无空白 | RFC 9112 §6.3 | `reject-cl-not-number`, `reject-cl-negative`, `reject-cl-hex` |
| 仅接受单独 `chunked`；identity / 链式 / 重复 / 带参数拒绝 | RFC 9112 §6.1/§7 | `reject-te-gzip-chained`(501), `reject-te-chunked-twice`(501) |
| 无 CL 且无 TE → 无体 | RFC 9112 §6.3 | `valid-get-no-body` |

## chunked 编码

| 裁决 | 依据 | 夹具 |
|---|---|---|
| `chunk-size = 1*HEXDIG`，手写解析、int64 溢出拒绝 | RFC 9112 §7.1.1 | `reject-chunk-bad-hex` |
| chunk-data 按 size 精确读取，不扫描 CRLF | RFC 9112 §7.1.2 | `valid-chunked-body-with-crlf` |
| chunk-data 后必须恰为 CRLF | RFC 9112 §7.1.1 | `reject-chunk-missing-end-crlf` |
| chunk-ext 语法校验（含引号值，禁裸 CR/LF） | RFC 9112 §7.1.1/§5.2 | `valid-chunked-ext-trailers`, `reject-chunk-bad-ext` |
| last-chunk 后尾部字段；尾部禁 CL/TE/Host | RFC 9112 §7.1.2 | `reject-framing-in-trailer` |
| 空体（立即 last-chunk）允许 | RFC 9112 §7.1 | `valid-chunked-empty` |

## 截断与关闭

| 裁决 | 依据 | 夹具 |
|---|---|---|
| 头部读到 EOF（未到终止空行）= 截断，非干净关闭 | RFC 9112 消息完整性 | `reject-truncated-head` |
| 固定体不足 CL = 截断，静默关闭（Code=0） | RFC 9112 §6.3 | `reject-truncated-fixed-body` |
| 分块数据/分块行/尾部截断 = 截断静默关闭 | RFC 9112 §7.1 | `reject-truncated-chunk-data`, `reject-truncated-chunk-size-line`, `reject-truncated-trailer` |
| 完整请求后第二个流水线请求截断 = 错误 | 消息边界不得误判 | `reject-partial-pipelined-second` |
| 仅“无任何待解析字节时的对端关闭”为 `ErrCleanEOF` | keep-alive 拆除语义 | （连接集成 `clean_eof` 日志） |

## 资源限制

| 裁决 | 触发 | 夹具 |
|---|---|---|
| 头部段超限 → 431 | `MaxHeaderBytes` | `TestTinyHeaderLimit` |
| 固定体声明/实读超限 → 413 | `MaxBodyBytes` | `reject-cl-over-limit` |
| 分块累计或单块超限 → 413 | `MaxBodyBytes` | `reject-chunk-size-over-limit` |
| 单条分块行（含 ext）超限 → 400 | `MaxChunkLineBytes` | `TestTinyChunkLineLimit` |

## 状态码映射

- 400：语法非法 / TE-CL 冲突 / CL 不一致 / chunked 语法 / 尾部帧字段。
- 413：体或分块超 `MaxBodyBytes`。
- 417：未知 `Expect`。
- 431：头部段超限。
- 501：不支持的传输编码（非 chunked）。
- 505：不支持的 HTTP 版本。
- Code=0（不回响应、直接关闭）：任何截断与 IO 失败——不存在安全的响应边界。
