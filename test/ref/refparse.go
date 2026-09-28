// Package refparse 是独立参考实现，见 doc.go。
package refparse

import (
	"strings"
)

// Verdict 是三值判定。
type Verdict string

const (
	VAccept    Verdict = "accept"
	VReject    Verdict = "reject"
	VTruncated Verdict = "truncated"
)

// Category 是参考侧的拒绝类别（字符串与生产侧 Kind 刻意分开，
// 由差分测试建立映射并断言等价）。
type Category string

const (
	CatSyntax       Category = "syntax"       // 请求行/头部词法、非法换行
	CatHeaderLimit  Category = "header_limit" // 头部段超限
	CatFraming      Category = "framing"      // TE/CL 冲突、重复长度不一致
	CatBodyLimit    Category = "body_limit"   // 体/chunk 超限
	CatLengthReq    Category = "length_required"
	CatUnsupported  Category = "unsupported" // 编码/版本/upgrade
	CatExpectFailed Category = "expect_failed"
	CatChunkSyntax  Category = "chunk_syntax"
)

// ExpectedRequest 是参考解析器对一条合法请求的描述。
type ExpectedRequest struct {
	Method        string
	Target        string
	Chunked       bool
	ContentLength int64 // -1 表示无
	HeaderCount   int
	BodyDecoded   []byte // chunked 已解码、固定长度已切出的表示
	Consumed      int    // 该请求从输入起点算的精确消费字节数（绝对）
}

// Result 是对一个完整字节串前缀的判定。
type Result struct {
	Verdict  Verdict
	Category Category
	Offset   int // reject：拒绝点下标；truncated：-1
	Req      *ExpectedRequest
	Reason   string
}

// Limits 与生产侧概念对应，数值由测试传入。
type Limits struct {
	Header int64
	Body   int64
	Chunk  int64
}

// ParseOne 解析输入中的恰好一条请求（必须包含完整消息体）。
// 返回结果。Truncated 表示字节不足，无法定论。
func ParseOne(in []byte, lim Limits) Result {
	r := &scanner{in: in, lim: lim}
	return r.parse()
}

type scanner struct {
	in         []byte
	pos        int
	lim        Limits
	lastReject Result
}

func (s *scanner) parse() Result {
	start := s.pos
	// 1) 找头部段结束 "\r\n\r\n"，同时记录所有裸换行。
	end, _, bad := s.scanHeaderSection()
	if bad != nil {
		return *bad
	}
	if end < 0 {
		// 头部段没有终止空行。所有已经以 CRLF 完整结束的行都不再是
		// “截断”：必须逐行按请求行/头部域语法校验（生产端逐行读取，
		// 完整行到位即裁决，不会等终止空行）。
		if complete := completeLines(s.in[start:]); len(complete) > 0 {
			method, target, rj := s.requestLine(string(complete[0]), start)
			if rj != nil {
				return *rj
			}
			linePos := start + len(complete[0]) + 2
			for _, ln := range complete[1:] {
				if _, rj := s.fieldLine(string(ln), linePos); rj != nil {
					return *rj
				}
				linePos += len(ln) + 2
			}
			_ = method
			_ = target
			// 已完整行都合法，只是终止空行没来——截断。
		}
		if s.lim.Header > 0 && int64(len(s.in)) > s.lim.Header {
			return Result{Verdict: VReject, Category: CatHeaderLimit,
				Offset: int(s.lim.Header), Reason: "header section exceeds limit"}
		}
		return Result{Verdict: VTruncated, Offset: -1, Reason: "header section incomplete"}
	}
	headerBlock := s.in[start:end] // 不含最终 \r\n\r\n 的最后一个空行
	_ = headerBlock

	// 2) 逐行切分（整体 Split，算法与生产的增量 ReadLine 完全不同）。
	// 头部段总长度（请求行+域+终止空行）超上限，即使终止符齐全也拒绝。
	if s.lim.Header > 0 && int64(end+4-start) > s.lim.Header {
		return Result{Verdict: VReject, Category: CatHeaderLimit,
			Offset: int(s.lim.Header), Reason: "header section exceeds limit"}
	}
	// headerBlock 是首字节到终止空行起点（不含空行的两个字节）。
	lines := splitCRLF(s.in[start:end])
	if len(lines) == 0 {
		return Result{Verdict: VReject, Category: CatSyntax, Offset: start,
			Reason: "empty request line"}
	}
	req := &ExpectedRequest{ContentLength: -1}
	method, target, rl := s.requestLine(lines[0], start)
	if rl != nil {
		return *rl
	}
	req.Method, req.Target = method, target

	type field struct{ name, value string }
	var fields []field
	pos := start + len(lines[0]) + 2
	for _, ln := range lines[1:] {
		fres, rl := s.fieldLine(ln, pos)
		if rl != nil {
			return *rl
		}
		fields = append(fields, field{fres.name, fres.value})
		pos += len(ln) + 2
	}
	req.HeaderCount = len(fields)

	// 3) 定帧（独立的裁决顺序，与生产代码对照）。
	clRaw := []string{}
	teVals := []string{}
	hasUpgrade := false
	expect100 := false
	expectOther := false
	for _, f := range fields {
		lname := strings.ToLower(f.name)
		switch lname {
		case "content-length":
			clRaw = append(clRaw, f.value)
		case "transfer-encoding":
			teVals = append(teVals, f.value)
		case "connection":
			for _, t := range strings.Split(f.value, ",") {
				if strings.EqualFold(strings.TrimSpace(t), "upgrade") {
					hasUpgrade = true
				}
			}
		case "expect":
			if strings.EqualFold(strings.TrimSpace(f.value), "100-continue") {
				expect100 = true
			} else {
				expectOther = true
			}
		}
	}
	if expectOther {
		return Result{Verdict: VReject, Category: CatExpectFailed, Offset: end,
			Reason: "unsupported expect"}
	}
	if hasUpgrade {
		return Result{Verdict: VReject, Category: CatUnsupported, Offset: end,
			Reason: "upgrade unsupported"}
	}
	chunked := false
	for _, te := range teVals {
		cs := strings.Split(te, ",")
		for i, c := range cs {
			c = strings.TrimSpace(c)
			base := c
			if q := strings.IndexByte(c, ';'); q >= 0 {
				base = strings.TrimSpace(c[:q])
				if strings.EqualFold(base, "chunked") {
					return Result{Verdict: VReject, Category: CatChunkSyntax, Offset: end,
						Reason: "chunked with parameters"}
				}
			}
			if strings.EqualFold(base, "chunked") {
				if i != len(cs)-1 {
					return Result{Verdict: VReject, Category: CatFraming, Offset: end,
						Reason: "chunked not last"}
				}
				chunked = true
			} else {
				return Result{Verdict: VReject, Category: CatUnsupported, Offset: end,
					Reason: "unsupported coding"}
			}
		}
	}
	if chunked && len(clRaw) > 0 {
		return Result{Verdict: VReject, Category: CatFraming, Offset: end,
			Reason: "TE and CL both present"}
	}
	if len(clRaw) > 0 {
		n := int64(-1)
		for _, raw := range clRaw {
			v := strings.TrimSpace(raw)
			if !allDigits(v) {
				// 逗号列表也拒绝
				if strings.Contains(v, ",") {
					return Result{Verdict: VReject, Category: CatFraming, Offset: end,
						Reason: "comma list CL"}
				}
				return Result{Verdict: VReject, Category: CatFraming, Offset: end,
					Reason: "non-numeric CL"}
			}
			m := parseInt64(v)
			if m < 0 {
				return Result{Verdict: VReject, Category: CatFraming, Offset: end,
					Reason: "CL overflow"}
			}
			if n < 0 {
				n = m
			} else if n != m {
				return Result{Verdict: VReject, Category: CatFraming, Offset: end,
					Reason: "conflicting CL"}
			}
		}
		req.ContentLength = n
		if s.lim.Body > 0 && n > s.lim.Body {
			return Result{Verdict: VReject, Category: CatBodyLimit, Offset: end,
				Reason: "CL exceeds body limit"}
		}
	}
	req.Chunked = chunked
	if expect100 && !chunked && req.ContentLength < 0 {
		return Result{Verdict: VReject, Category: CatExpectFailed, Offset: end,
			Reason: "expect without framing"}
	}

	// 4) 体。无 Content-Length 且非 chunked 时没有消息体：头部段之后的
	// 字节属于“连接上的后续输入”（可能是流水线第二请求，也可能是噪声）。
	// 解析器不能把它们判为“本请求截断”——那会错误地吞掉流水线。
	bodyStart := end + 4
	s.pos = bodyStart
	if chunked {
		if res := s.decodeChunks(req, bodyStart); res.Verdict != "" {
			return res
		}
	} else if req.ContentLength >= 0 {
		need := bodyStart + int(req.ContentLength)
		if len(s.in) < need {
			return Result{Verdict: VTruncated, Offset: -1, Reason: "fixed body incomplete"}
		}
		req.BodyDecoded = s.in[bodyStart:need]
		s.pos = need
		req.Consumed = s.pos
	} else {
		req.Consumed = bodyStart
	}
	return Result{Verdict: VAccept, Req: req}
}

// scanHeaderSection 返回 "\r\n\r\n" 中第一个 \r 的下标（即空行起点），
// 未找到返回 -1。若发现裸 LF 或游离 CR，给出 reject。
func (s *scanner) scanHeaderSection() (int, int, *Result) {
	in := s.in
	for i := 0; i < len(in); i++ {
		switch in[i] {
		case '\n':
			if i == 0 || in[i-1] != '\r' {
				return 0, 0, &Result{Verdict: VReject, Category: CatSyntax, Offset: i,
					Reason: "bare LF"}
			}
		case '\r':
			if i+1 >= len(in) {
				// 头部区：CR 在末尾后继未知。若它前面已经有完整的行结束，
				// 这个 CR 是“下一请求行”的开头且永不补 LF——按非法换行
				// 拒绝（与生产端一致）；否则当前行未完，按截断。
				if i >= 2 && in[i-2] == '\r' && in[i-1] == '\n' {
					return 0, 0, &Result{Verdict: VReject, Category: CatSyntax,
						Offset: i, Reason: "stray CR starting next line at EOF"}
				}
				continue
			}
			if in[i+1] != '\n' {
				return 0, 0, &Result{Verdict: VReject, Category: CatSyntax, Offset: i,
					Reason: "stray CR"}
			}
			// CRLF；检查是否为空行终止
			if i+3 < len(in) && in[i+2] == '\r' && in[i+3] == '\n' {
				return i, i, nil
			}
		}
	}
	return -1, 0, nil
}

// splitCRLF 把“请求行 + 各头部域”的字节块（不含终止空行）切成逻辑行。
// 调用方保证块中每个换行都是成对 CRLF（裸换行已在 scanHeaderSection 拒绝）。
func splitCRLF(block []byte) []string {
	var out []string
	for {
		i := bytesIndexCRLF(block)
		if i < 0 {
			out = append(out, string(block))
			return out
		}
		out = append(out, string(block[:i]))
		block = block[i+2:]
	}
}

// completeLines 返回块中已经以 CRLF 完整结束的行（不含最后未完成的行）。
func completeLines(block []byte) [][]byte {
	var out [][]byte
	for {
		i := bytesIndexCRLF(block)
		if i < 0 {
			return out
		}
		out = append(out, block[:i])
		block = block[i+2:]
	}
}

func bytesIndexCRLF(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == '\r' && b[i+1] == '\n' {
			return i
		}
	}
	return -1
}

// findLineEnd 在字节块中找第一个 CRLF。返回 (CRLF 中 CR 的下标, 非法
// 字节下标, 是否发现非法换行)：裸 LF 或 CR 后随非 LF 字节是 reject；
// CR 恰好落在末尾（后继未知）报截断，由调用方在“下一请求行”场景
// 另行判定；没有任何换行返回 (-1,0,false)。
func findLineEnd(b []byte) (int, int, bool) {
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '\n':
			if i == 0 || b[i-1] != '\r' {
				return -1, i, true
			}
		case '\r':
			if i+1 >= len(b) {
				return -1, 0, false // 数据区：后继未知，按截断
			}
			if b[i+1] != '\n' {
				return -1, i, true
			}
			return i, 0, false
		}
	}
	return -1, 0, false
}

func (s *scanner) requestLine(line string, base int) (string, string, *Result) {
	sp1 := strings.IndexByte(line, ' ')
	if sp1 <= 0 {
		return "", "", rej(CatSyntax, base, "bad request line")
	}
	method := line[:sp1]
	if !isToken(method) {
		return "", "", rej(CatSyntax, base+sp1, "bad method")
	}
	rest := line[sp1+1:]
	sp2 := strings.IndexByte(rest, ' ')
	if sp2 <= 0 || strings.IndexByte(rest[sp2+1:], ' ') >= 0 {
		return "", "", rej(CatSyntax, base+sp1, "bad request line spaces")
	}
	target := rest[:sp2]
	version := rest[sp2+1:]
	if target == "" {
		return "", "", rej(CatSyntax, base+sp1+1, "empty target")
	}
	if target != "*" && target[0] != '/' {
		return "", "", rej(CatSyntax, base+sp1+1, "only origin/asterisk form")
	}
	for i := 0; i < len(target); i++ {
		if target[i] <= 0x20 || target[i] == 0x7f {
			return "", "", rej(CatSyntax, base+sp1+1+i, "ctl in target")
		}
	}
	if version != "HTTP/1.1" {
		if strings.HasPrefix(version, "HTTP/") {
			return "", "", rej(CatUnsupported, base+sp1+1+sp2, "version")
		}
		return "", "", rej(CatSyntax, base+sp1+1+sp2, "bad version")
	}
	return method, target, nil
}

type fieldResult struct{ name, value string }

func (s *scanner) fieldLine(line string, base int) (fieldResult, *Result) {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return fieldResult{}, rej(CatSyntax, base, "bad field")
	}
	name := line[:colon]
	if !isToken(name) {
		return fieldResult{}, rej(CatSyntax, base, "bad field name")
	}
	value := strings.Trim(line[colon+1:], " \t")
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < 0x20 || c == 0x7f) && c != '\t' {
			return fieldResult{}, rej(CatSyntax, base+colon+1+i, "ctl in value")
		}
	}
	return fieldResult{name, value}, nil
}

func rej(cat Category, off int, reason string) *Result {
	return &Result{Verdict: VReject, Category: cat, Offset: off, Reason: reason}
}

func (s *scanner) decodeChunks(req *ExpectedRequest, bodyStart int) Result {
	var decoded []byte
	var total int64
	chunkNo := 0
	for {
		lineEnd, lineStart, bad := findLineEnd(s.in[s.pos:])
		if bad {
			return Result{Verdict: VReject, Category: CatChunkSyntax,
				Offset: s.pos + lineStart, Reason: "illegal line break in chunk section"}
		}
		if lineEnd < 0 {
			return Result{Verdict: VTruncated, Reason: "chunk line incomplete"}
		}
		line := string(s.in[s.pos : s.pos+lineEnd])
		lineBase := s.pos
		s.pos += lineEnd + 2
		chunkNo++
		sizeStr := line
		if q := strings.IndexByte(line, ';'); q >= 0 {
			sizeStr = strings.TrimSpace(line[:q])
			// chunk-ext 各段允许分号前后 OWS；值必须是 token 或 quoted-string。
			for _, seg := range strings.Split(line[q+1:], ";") {
				seg = strings.TrimSpace(seg)
				if seg == "" {
					return Result{Verdict: VReject, Category: CatChunkSyntax,
						Offset: lineBase, Reason: "empty chunk ext"}
				}
				name := seg
				if eq := strings.IndexByte(seg, '='); eq >= 0 {
					name = strings.TrimSpace(seg[:eq])
					val := strings.TrimSpace(seg[eq+1:])
					if !extValueOK(val) {
						return Result{Verdict: VReject, Category: CatChunkSyntax,
							Offset: lineBase, Reason: "bad chunk ext value"}
					}
				}
				if !isToken(name) {
					return Result{Verdict: VReject, Category: CatChunkSyntax,
						Offset: lineBase, Reason: "bad chunk ext name"}
				}
			}
		}
		if !isHex(sizeStr) {
			return Result{Verdict: VReject, Category: CatChunkSyntax, Offset: lineBase,
				Reason: "bad chunk size"}
		}
		size := parseHex(sizeStr)
		if size < 0 {
			return Result{Verdict: VReject, Category: CatChunkSyntax, Offset: lineBase,
				Reason: "chunk size overflow"}
		}
		if s.lim.Chunk > 0 && size > s.lim.Chunk {
			return Result{Verdict: VReject, Category: CatBodyLimit, Offset: lineBase,
				Reason: "chunk too large"}
		}
		if s.lim.Body > 0 && total+size > s.lim.Body {
			return Result{Verdict: VReject, Category: CatBodyLimit, Offset: lineBase,
				Reason: "body too large"}
		}
		if size == 0 {
			// trailer 直到空行；同样只承认 CRLF。
			for {
				le, ls, bad := findLineEnd(s.in[s.pos:])
				if bad {
					return Result{Verdict: VReject, Category: CatChunkSyntax,
						Offset: s.pos + ls, Reason: "illegal line break in trailer"}
				}
				if le < 0 {
					return Result{Verdict: VTruncated, Reason: "trailer incomplete"}
				}
				tline := s.in[s.pos : s.pos+le]
				s.pos += le + 2
				if len(tline) == 0 {
					req.BodyDecoded = decoded
					req.Consumed = s.pos
					return Result{Verdict: VAccept, Req: req}
				}
				// trailer 与请求头同语法：必须有非空 token 字段名。
				if _, rj := s.fieldLine(string(tline), s.pos-le-2); rj != nil {
					return *rj
				}
			}
		}
		endData := s.pos + int(size)
		if len(s.in) < endData+2 {
			return Result{Verdict: VTruncated, Reason: "chunk data incomplete"}
		}
		if s.in[endData] != '\r' || s.in[endData+1] != '\n' {
			return Result{Verdict: VReject, Category: CatChunkSyntax, Offset: endData,
				Reason: "chunk not followed by CRLF"}
		}
		decoded = append(decoded, s.in[s.pos:endData]...)
		total += size
		s.pos = endData + 2
	}
}

// ---- 字符 / 数值工具（独立实现，不复用生产代码）----

func isToken(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		sym := strings.IndexRune("!#$%&'*+-.^_`|~", rune(c)) >= 0
		if !alnum && !sym {
			return false
		}
	}
	return len(s) > 0
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func parseInt64(s string) int64 {
	var n int64
	for i := 0; i < len(s); i++ {
		d := int64(s[i] - '0')
		if n > (1<<63-1-d)/10 {
			return -1
		}
		n = n*10 + d
	}
	return n
}

// extValueOK 与生产端一致：值必须是 token 或成对引号的 quoted-string，
// 空值与裸反斜义/内嵌引号拒绝。
func extValueOK(v string) bool {
	if v == "" {
		return false
	}
	if v[0] == '"' {
		if len(v) < 2 || v[len(v)-1] != '"' {
			return false
		}
		for i := 1; i < len(v)-1; i++ {
			c := v[i]
			if c == '"' || c == '\\' {
				return false
			}
			if c < 0x20 && c != '\t' {
				return false
			}
		}
		return true
	}
	return isToken(v)
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !ok {
			return false
		}
	}
	return true
}

func parseHex(s string) int64 {
	var n int64
	for i := 0; i < len(s); i++ {
		var d int64
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			d = int64(c - '0')
		case c >= 'a' && c <= 'f':
			d = int64(c-'a') + 10
		default:
			d = int64(c-'A') + 10
		}
		if n > (1<<63-1-d)>>4 {
			return -1
		}
		n = n<<4 | d
	}
	return n
}
