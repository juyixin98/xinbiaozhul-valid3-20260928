package protocol

import (
	"bytes"
	"io"
	"strconv"
	"strings"
)

// Limits 是协议解析的资源上限。
type Limits struct {
	// MaxHeaderBytes：请求行 + 头部域 + 空行的总字节上限（同时也是单行上限）。
	MaxHeaderBytes int64
	// MaxBodyBytes：单条请求消息体的字节上限。
	MaxBodyBytes int64
	// MaxChunkSize：chunked 编码下单个 chunk-data 的字节上限。
	MaxChunkSize int64
}

// DefaultLimits 为本服务默认值。
func DefaultLimits() Limits {
	return Limits{
		MaxHeaderBytes: 1 << 14, // 16 KiB
		MaxBodyBytes:   1 << 20, // 1 MiB
		MaxChunkSize:   1 << 18, // 256 KiB
	}
}

// Field 是一个原始头部域。同名域按出现顺序全部保留（不做折叠），
// 由 Framing 阶段集中裁决冲突。
type Field struct {
	Name  string // 已规范为大小写原样；token 校验保证无空白
	Value string // 已去除前后 OWS（SP/HTAB）
	Raw   string // 未裁剪的值（OWS 仍在），调试/夹具比对用
}

// Request 是一条被成功定帧的 HTTP/1.1 请求。
type Request struct {
	Method  string
	Target  string // origin-form（以 / 开头）或 asterisk-form（OPTIONS *）
	Fields  []Field
	Body    BodyFrame
	Headers int64 // 头部段总字节（请求行+域+空行，含 CRLF），用于偏移核算

	// 定帧决策结果。
	Chunked       bool
	ContentLength int64 // <0 表示无 Content-Length
	HasLength     bool

	// 连接处置。
	CloseAfter bool

	// Expect100 为真时，业务层读完消息体之前必须先由状态机回 100 Continue
	// （或在拒绝时回 4xx/5xx）。
	Expect100 bool
}

// Header 返回指定名称（大小写不敏感）的第一个裁剪后值；不存在返回 ""。
func (r *Request) Header(name string) (string, bool) {
	for _, f := range r.Fields {
		if strings.EqualFold(f.Name, name) {
			return f.Value, true
		}
	}
	return "", false
}

// HeaderValues 返回指定名称的全部值。
func (r *Request) HeaderValues(name string) []string {
	var out []string
	for _, f := range r.Fields {
		if strings.EqualFold(f.Name, name) {
			out = append(out, f.Value)
		}
	}
	return out
}

// BodyFrame 是消息体帧。状态机保证：业务层结束处理后，Close 会
// 消费（drain）掉尚未读取的全部消息体字节，使下一请求边界精确对齐；
// 若残余字节无法被完整消费（截断 / 超长），Close 返回 *ProtoError，
// 连接必须关闭。
type BodyFrame interface {
	io.Reader
	// ReadCloser 风格：读完或放弃时调用。
	io.Closer
	// Consumed 报告消息体是否已到精确边界（固定长度读满，或读到 last-chunk）。
	Consumed() bool
	// FrameOffset 返回消息体首字节的绝对偏移。
	FrameOffset() int64
}

// Parser 持有单条连接的解析缓冲状态，生命周期跨越该连接上的全部请求。
// 必须每连接一个（不能每请求新建）：内部 streamReader 会预读流水线中
// 后续请求的字节，body 帧 drain 后这些字节仍留在同一缓冲里，下一次
// Next 立即从精确边界继续。
type Parser struct {
	sr  *streamReader
	lim Limits
}

// NewParser 在给定连接字节流上构造持久解析器。
func NewParser(r io.Reader, lim Limits) *Parser {
	return &Parser{sr: newStreamReader(r, lim.MaxHeaderBytes), lim: lim}
}

// Next 解析下一条请求的请求行与头部并完成定帧决策。消息体字节在调用方
// 通过返回的 req.Body 读取时才被消费。干净的连接结束返回 io.EOF。
func (p *Parser) Next() (*Request, error) {
	sr := p.sr
	lim := p.lim
	req := &Request{ContentLength: -1}

	line, lineOff, err := sr.ReadLine()
	if err != nil {
		return nil, err // io.EOF / *ProtoError 原样上抛
	}
	req.Headers += int64(len(line)) + 2
	if err := parseRequestLine(line, lineOff, req); err != nil {
		return nil, err
	}

	// 头部域。
	for {
		row, off, err := sr.ReadLine()
		if err != nil {
			return nil, err
		}
		req.Headers += int64(len(row)) + 2
		if lim.MaxHeaderBytes > 0 && req.Headers > lim.MaxHeaderBytes {
			return nil, protoError(KindHeaderTooLarge, PhaseHeader, off, -1,
				"header section exceeds limit of %d bytes", lim.MaxHeaderBytes)
		}
		if len(row) == 0 {
			break // 空行：头部结束
		}
		if err := parseField(row, off, req); err != nil {
			return nil, err
		}
	}

	if err := decideFraming(req, lineOff+req.Headers, lim); err != nil {
		return nil, err
	}

	// 绑定消息体帧；全部帧共享同一个持久 streamReader。
	bodyStart := lineOff + req.Headers
	if req.Chunked {
		req.Body = newChunkedBody(sr, lim, bodyStart)
	} else if req.HasLength {
		req.Body = newFixedBody(sr, req.ContentLength, bodyStart)
	} else {
		req.Body = emptyBody{off: bodyStart}
	}
	return req, nil
}

// parseRequestLine 解析 "METHOD SP request-target SP HTTP-version CRLF"。
func parseRequestLine(line []byte, lineOff int64, req *Request) error {
	sp1 := bytes.IndexByte(line, ' ')
	if sp1 < 0 {
		return protoError(KindInvalid, PhaseRequestLine, lineOff, -1,
			"request line must be 'METHOD SP target SP HTTP-version': missing SP")
	}
	if sp1 == 0 {
		return protoError(KindInvalid, PhaseRequestLine, lineOff, 0, "empty method")
	}
	method := line[:sp1]
	if !isToken(method) {
		if i := firstNotToken(method); i >= 0 {
			return protoError(KindInvalid, PhaseRequestLine, lineOff+int64(i), int64(i),
				"method contains byte 0x%02x, not a token", method[i])
		}
	}
	rest := line[sp1+1:]
	sp2 := bytes.IndexByte(rest, ' ')
	if sp2 < 0 {
		return protoError(KindInvalid, PhaseRequestLine, lineOff+int64(len(line)), -1,
			"request line missing second SP before HTTP-version")
	}
	target := rest[:sp2]
	version := rest[sp2+1:]
	// 恰好两个 SP：version 内不得再有空格。
	if bytes.IndexByte(version, ' ') >= 0 {
		return protoError(KindInvalid, PhaseRequestLine,
			lineOff+int64(sp1+1+sp2), int64(sp2),
			"extra whitespace in request line")
	}
	if len(target) == 0 {
		return protoError(KindInvalid, PhaseRequestLine,
			lineOff+int64(sp1+1), -1, "empty request-target")
	}
	if err := validateTarget(target, lineOff+int64(sp1+1)); err != nil {
		return err
	}
	if !bytes.Equal(version, []byte("HTTP/1.1")) {
		off := lineOff + int64(sp1+1+sp2+1)
		if bytes.HasPrefix(version, []byte("HTTP/")) {
			return protoError(KindVersionUnsupported, PhaseRequestLine, off, -1,
				"only HTTP/1.1 is supported, got %q", string(version))
		}
		return protoError(KindInvalid, PhaseRequestLine, off, -1,
			"malformed HTTP-version %q", string(version))
	}
	req.Method = string(method)
	req.Target = string(target)
	return nil
}

// validateTarget 只接受 RFC 9110 中的两种 request-target：
//   - origin-form：以 "/" 开头的绝对路径查询串；
//   - asterisk-form："*"（仅 OPTIONS）。
//
// 显式拒绝 absolute-form（http://host/...）与 authority-form（host:port）：
// 本服务不是代理，接受它们会掩盖把转发请求误当直连的配置错误。
func validateTarget(target []byte, off int64) error {
	if bytes.Equal(target, []byte("*")) {
		return nil
	}
	if target[0] != '/' {
		return protoError(KindInvalid, PhaseRequestLine, off, 0,
			"only origin-form ('/path') and asterisk-form ('*') targets are accepted; "+
				"absolute-form/authority-form imply proxy semantics and are refused")
	}
	// 路径内不允许空白与 CTL。
	for i, c := range target {
		if c <= 0x20 || c == 0x7f {
			return protoError(KindInvalid, PhaseRequestLine, off+int64(i), int64(i),
				"request-target contains control/space byte 0x%02x", c)
		}
	}
	// "//" 本身合法（absolute path 可以空段），不做额外限制。
	return nil
}

// parseField 解析 "field-name:OWS field-value OWS"。
func parseField(row []byte, rowOff int64, req *Request) error {
	colon := bytes.IndexByte(row, ':')
	if colon < 0 {
		return protoError(KindInvalid, PhaseHeader, rowOff, -1,
			"header field without ':'")
	}
	name := row[:colon]
	if colon == 0 {
		return protoError(KindInvalid, PhaseHeader, rowOff, 0, "empty header field name")
	}
	if !isToken(name) {
		i := firstNotToken(name)
		return protoError(KindInvalid, PhaseHeader, rowOff+int64(i), int64(i),
			"field name contains byte 0x%02x; obsolete whitespace between name and ':' is rejected",
			name[i])
	}
	raw := row[colon+1:]
	value := bytes.Trim(raw, " \t") // 仅裁剪 OWS（SP / HTAB）
	// 值中不允许裸 CR/LF（行结构已保证整行没有）与 NUL/其他 CTL（HTAB 除外）。
	for i, c := range value {
		if (c < 0x20 || c == 0x7f) && c != '\t' {
			return protoError(KindInvalid, PhaseHeader,
				rowOff+int64(colon+1)+int64(i), int64(i),
				"field value contains control byte 0x%02x", c)
		}
	}
	req.Fields = append(req.Fields, Field{
		Name:  string(name),
		Value: string(value),
		Raw:   string(raw),
	})
	return nil
}

// decideFraming 在头部解析完成后一次性裁决消息体定帧。这是请求走私
// 防御的核心位置：TE/CL 冲突、重复且不一致的 Content-Length、
// chunked 与标识语义冲突都在这里产生确定性的 400/501，绝不二义。
func decideFraming(req *Request, bodyOff int64, lim Limits) error {
	var (
		clValues []string
		teValues []string
		connVals []string
	)
	for _, f := range req.Fields {
		switch {
		case strings.EqualFold(f.Name, "content-length"):
			clValues = append(clValues, f.Raw)
		case strings.EqualFold(f.Name, "transfer-encoding"):
			teValues = append(teValues, f.Value)
		case strings.EqualFold(f.Name, "connection"):
			connVals = append(connVals, f.Value)
		case strings.EqualFold(f.Name, "expect"):
			if strings.EqualFold(f.Value, "100-continue") {
				req.Expect100 = true
			} else {
				return protoError(KindExpectationFailed, PhaseFraming, bodyOff, -1,
					"unsupported Expect value %q (only '100-continue' is understood)", f.Value)
			}
		}
	}

	// Connection 头：识别 close / keep-alive / upgrade。
	for _, cv := range connVals {
		for _, tok := range splitComma(cv) {
			switch strings.ToLower(strings.Trim(tok, " \t")) {
			case "close":
				req.CloseAfter = true
			case "keep-alive":
				// HTTP/1.1 默认即 keep-alive；显式 keep-alive 无副作用。
			case "upgrade":
				return protoError(KindUpgradeUnsupported, PhaseFraming, bodyOff, -1,
					"Connection: Upgrade is not supported (no tunneling/proxying)")
			}
		}
	}

	// Transfer-Encoding 裁决。
	chunked := false
	for _, tv := range teValues {
		codings := splitComma(tv)
		if len(codings) == 0 {
			return protoError(KindInvalid, PhaseFraming, bodyOff, -1,
				"empty Transfer-Encoding header field")
		}
		// RFC 9112 §6.2：多个 TE 头域时，chunked 必须在所有编码的最末。
		for i, c := range codings {
			c = strings.Trim(c, " \t")
			// 去掉参数（chunked 不接受参数；其他编码本服务一律不实现）。
			base := c
			if semi := strings.IndexByte(c, ';'); semi >= 0 {
				base = strings.Trim(c[:semi], " \t")
				if strings.EqualFold(base, "chunked") {
					return protoError(KindInvalid, PhaseFraming, bodyOff, int64(i),
						"chunked coding must not carry parameters")
				}
			}
			isLast := i == len(codings)-1
			if strings.EqualFold(base, "chunked") {
				if !isLast {
					return protoError(KindInvalid, PhaseFraming, bodyOff, int64(i),
						"'chunked' must be the final transfer coding")
				}
				chunked = true
			} else {
				return protoError(KindUnsupportedCoding, PhaseFraming, bodyOff, int64(i),
					"transfer coding %q is not implemented (only 'chunked')", base)
			}
		}
	}
	req.Chunked = chunked

	// TE 与 CL 同时出现：RFC 9112 §6.2.1 明确为服务端必须拒绝的歧义。
	// 即使 CL 值恰好等于 chunked 体的线字节数也拒绝——定帧语义不允许二义。
	if chunked && len(clValues) > 0 {
		return protoError(KindInvalid, PhaseFraming, bodyOff, -1,
			"request contains both Transfer-Encoding: chunked and Content-Length: "+
				"ambiguous framing (request smuggling vector), connection will be closed")
	}

	// Content-Length 裁决：重复头域只有“相同十进制值”才合法（允许
	// "0" 与 "00" 这类前导零差异），任何不一致都拒绝。
	if len(clValues) > 0 {
		first := strings.TrimSpace(clValues[0])
		n, err := parseContentLength(first, bodyOff)
		if err != nil {
			return err
		}
		for _, raw := range clValues[1:] {
			n2, err := parseContentLength(strings.TrimSpace(raw), bodyOff)
			if err != nil {
				return err
			}
			if n2 != n {
				return protoError(KindInvalid, PhaseFraming, bodyOff, -1,
					"conflicting Content-Length values %d vs %d: "+
						"differing repeated lengths are rejected (request smuggling vector)",
					n, n2)
			}
		}
		req.HasLength = true
		req.ContentLength = n
		if lim.MaxBodyBytes > 0 && n > lim.MaxBodyBytes {
			return protoError(KindPayloadTooLarge, PhaseFraming, bodyOff, -1,
				"declared Content-Length %d exceeds body limit %d", n, lim.MaxBodyBytes)
		}
	}

	if req.Expect100 && !chunked && !req.HasLength {
		// Expect 只对带请求体的请求有意义；无定帧信息时忽略比 417 更稳妥，
		// 但为确定性这里显式拒绝，避免对端等待一个永远不来的 100。
		return protoError(KindExpectationFailed, PhaseFraming, bodyOff, -1,
			"Expect: 100-continue without body framing (no Content-Length/chunked)")
	}
	return nil
}

// parseContentLength 解析 Content-Length，严格按 RFC 9110：
// 1*DIGIT，不允许前导符号、空白（调用方已 Trim）、多个逗号值
// （逗号合并是接收方可选行为，本服务选择拒绝以消除歧义）。
func parseContentLength(v string, off int64) (int64, error) {
	if v == "" {
		return 0, protoError(KindInvalid, PhaseFraming, off, -1, "empty Content-Length")
	}
	if strings.IndexByte(v, ',') >= 0 {
		return 0, protoError(KindInvalid, PhaseFraming, off, -1,
			"Content-Length with comma list %q is refused (repeated fields must be separate and identical)", v)
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, protoError(KindInvalid, PhaseFraming, off+int64(i), int64(i),
				"Content-Length contains non-digit byte 0x%02x", v[i])
		}
	}
	// 前导零：RFC 允许，"00" 与 "0" 视为同值（重复头按数值比较）。
	// 数值解析用 strconv 并防溢出。
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, protoError(KindInvalid, PhaseFraming, off, -1,
			"Content-Length %q out of range: %v", v, err)
	}
	return n, nil
}

// ---- token / 字符工具（RFC 5234 / RFC 9110 token）----

func isToken(b []byte) bool {
	if len(b) == 0 {
		return false // token = 1*tchar，空串不是 token
	}
	return firstNotToken(b) < 0
}

// firstNotToken 返回第一个非 tchar 字节下标，全部合法返回 -1。
func firstNotToken(b []byte) int {
	for i, c := range b {
		if !isTchar(c) {
			return i
		}
	}
	return -1
}

func isTchar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

func splitComma(v string) []string {
	var out []string
	start := 0
	for i := 0; i < len(v); i++ {
		if v[i] == ',' {
			out = append(out, v[start:i])
			start = i + 1
		}
	}
	out = append(out, v[start:])
	return out
}
