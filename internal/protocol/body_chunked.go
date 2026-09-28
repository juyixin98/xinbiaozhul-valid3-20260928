package protocol

import (
	"bytes"
	"io"
	"strings"
)

// chunked 解码状态机。线格式（RFC 9112 §7）：
//
//	chunk = chunk-size [ chunk-ext ] CRLF chunk-data CRLF
//	last-chunk = 1*("0") [ chunk-ext ] CRLF
//	trailer-part = *( field-line CRLF ) CRLF
//
// 本实现的确定性裁决（歧义全部拒绝，不“宽容”）：
//   - chunk-size 只允许 1*HEXDIG（大小写均可），无符号、无前导空白；
//     长度溢出 63 位即拒绝（400，而非当作无限）；
//   - chunk-ext 语法受限：元素为 token 或 token="quoted"，禁止 CR/LF
//     之外的控制字符；未知扩展保留原文供测试/日志，不参与定帧；
//   - 单 chunk 超 MaxChunkSize、或累计数据超 MaxBodyBytes 均为 413；
//   - chunk-data 后必须紧跟 CRLF；last-chunk 后允许空 trailer 或
//     受头部总预算约束的 trailer 域；
//   - 任何提前 EOF 为截断（KindIncomplete），任何词法违规为 400 并关闭。

type chunkState int

const (
	csSize     chunkState = iota // 读 chunk 行（size[;ext]）
	csData                       // 读 chunk-data
	csDataCRLF                   // chunk-data 后的 CRLF
	csTrailer                    // last-chunk 后的 trailer 域
	csDone
)

type chunkedBody struct {
	sr        *streamReader
	lim       Limits
	off       int64 // 消息体（首个 chunk 行）首字节绝对偏移
	state     chunkState
	remain    int64 // 当前 chunk 剩余数据字节
	totalData int64 // 已产出的 chunk-data 累计
	chunkNo   int
	lastChunk bool
	done      bool
}

func newChunkedBody(sr *streamReader, lim Limits, off int64) *chunkedBody {
	return &chunkedBody{sr: sr, lim: lim, off: off, state: csSize}
}

func (b *chunkedBody) FrameOffset() int64 { return b.off }
func (b *chunkedBody) Consumed() bool     { return b.done }

func (b *chunkedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.done {
		return 0, io.EOF
	}
	total := 0
	for total < len(p) {
		switch b.state {
		case csSize:
			if err := b.readChunkLine(); err != nil {
				return total, err
			}
			if b.lastChunk {
				b.state = csTrailer
				continue
			}
			b.state = csData
		case csData:
			if b.remain == 0 {
				b.state = csDataCRLF
				continue
			}
			// dst 是本次调用输出缓冲里还没填的部分。
			dst := p[total:]
			want := b.remain
			if int64(len(dst)) < want {
				want = int64(len(dst))
			}
			got := 0
			for int64(got) < want {
				if b.sr.buffered() > 0 {
					// 只取缓冲前部 want-got 字节；copy 的源切片由
					// discard 推进，天然不会越过 chunk 边界。
					avail := b.sr.buf
					take := want - int64(got)
					if int64(len(avail)) < take {
						take = int64(len(avail))
					}
					n := copy(dst[got:], avail[:take])
					b.sr.discard(n)
					got += n
					b.remain -= int64(n)
					b.totalData += int64(n)
					continue
				}
				if err := b.sr.fill(); err != nil {
					if err == io.EOF {
						total += got
						return total, b.incomplete(PhaseChunkData,
							"connection closed inside chunk-data: %d bytes of chunk #%d remain",
							b.remain, b.chunkNo)
					}
					return total + got, err
				}
			}
			total += got
			if b.remain == 0 {
				b.state = csDataCRLF
			}
			if total == len(p) {
				return total, nil
			}
		case csDataCRLF:
			if err := b.expectCRLF(PhaseChunkData); err != nil {
				return total, err
			}
			b.state = csSize
		case csTrailer:
			consumed, err := b.readTrailer()
			if err != nil {
				return total, err
			}
			if consumed {
				b.state = csDone
				b.done = true
				if total > 0 {
					return total, nil // EOF 在下次 Read 给出
				}
				return 0, io.EOF
			}
			// 读到一个 trailer 域，继续直到空行。
		case csDone:
			b.done = true
			if total > 0 {
				return total, nil
			}
			return 0, io.EOF
		}
	}
	return total, nil
}

// readChunkLine 读取并解析一个 chunk 行。
func (b *chunkedBody) readChunkLine() error {
	line, lineOff, err := b.sr.ReadLine()
	if err != nil {
		// chunked 体必须以 last-chunk + 空行结束；头部后直接 EOF 是截断，
		// 不是“零长度体”（零长度体的正确线格式是 "0\r\n\r\n"）。
		if err == io.EOF {
			return b.incomplete(PhaseChunkSize,
				"connection closed before any chunk (expected last-chunk '0')")
		}
		if pe := AsProtoError(err); pe != nil && pe.Kind == KindIncomplete {
			return b.incomplete(PhaseChunkSize, "connection closed before chunk line")
		}
		return err
	}
	b.chunkNo++
	// size 与扩展以 ';' 分隔。chunk-size 必须从行首开始就是 1*HEXDIG，
	// 不允许前导/尾随空白（RFC 9112）——宽容空白是经典的接收端解析差异，
	// 这里选择严格拒绝。
	sizePart := line
	extPart := []byte(nil)
	if semi := indexSemicolon(line); semi >= 0 {
		sizePart = line[:semi]
		extPart = line[semi+1:]
	}
	if len(sizePart) == 0 {
		return b.invalid(lineOff, 0, PhaseChunkSize, "empty chunk-size")
	}
	for i, c := range sizePart {
		if !isHexDigit(c) {
			return b.invalid(lineOff, int64(i), PhaseChunkSize,
				"chunk-size contains non-hex byte 0x%02x (no signs/whitespace allowed)", c)
		}
	}
	size, err := parseHexSize(sizePart, lineOff)
	if err != nil {
		return err
	}
	if err := validateChunkExt(extPart, lineOff+int64(len(sizePart))+1); err != nil {
		return err
	}
	if size == 0 {
		b.lastChunk = true
		b.remain = 0
		return nil
	}
	if b.lim.MaxChunkSize > 0 && size > b.lim.MaxChunkSize {
		return protoError(KindPayloadTooLarge, PhaseChunkSize, lineOff, 0,
			"chunk #%d size %d exceeds per-chunk limit %d",
			b.chunkNo, size, b.lim.MaxChunkSize)
	}
	// 必须在本 chunk 数据被产出前判定累计超限，否则客户端能读到越界字节。
	if b.lim.MaxBodyBytes > 0 && b.totalData+size > b.lim.MaxBodyBytes {
		return protoError(KindPayloadTooLarge, PhaseChunkSize, lineOff, 0,
			"chunk #%d would make decoded body %d bytes, exceeding limit %d",
			b.chunkNo, b.totalData+size, b.lim.MaxBodyBytes)
	}
	b.remain = size
	return nil
}

// expectCRLF 消费 chunk-data 后的两个字节，要求精确为 CRLF。
// 常见走私形态（"5\r\nabcdeX\r\n"、缺 LF、裸 LF）在此被拒。
func (b *chunkedBody) expectCRLF(phase string) error {
	cr, off, err := b.sr.readByte(phase, 0)
	if err != nil {
		if pe := AsProtoError(err); pe != nil && pe.Kind == KindIncomplete {
			return b.incomplete(phase, "connection closed expecting CRLF after chunk-data")
		}
		return err
	}
	if cr != '\r' {
		return b.invalid(off, 0, phase,
			"expected CR after chunk-data, got 0x%02x", cr)
	}
	lf, off2, err := b.sr.readByte(phase, 1)
	if err != nil {
		if pe := AsProtoError(err); pe != nil && pe.Kind == KindIncomplete {
			return b.incomplete(phase, "connection closed expecting LF after CR")
		}
		return err
	}
	if lf != '\n' {
		return b.invalid(off2, 1, phase, "expected LF after CR, got 0x%02x", lf)
	}
	return nil
}

// readTrailer 处理 last-chunk 后的 trailer-section。返回 consumed=true
// 表示读到终止空行、消息体结束。trailer 段必须以空行结束：last-chunk 后
// 只有 "0\r\n" 而没有终止 "\r\n" 是截断，绝不允许被当作完成。
func (b *chunkedBody) readTrailer() (bool, error) {
	line, off, err := b.sr.ReadLine()
	if err != nil {
		if err == io.EOF {
			return false, b.incomplete(PhaseTrailer,
				"connection closed before trailer terminating empty line")
		}
		if pe := AsProtoError(err); pe != nil && pe.Kind == KindIncomplete {
			return false, b.incomplete(PhaseTrailer, "connection closed inside trailer section")
		}
		return false, err
	}
	if len(line) == 0 {
		return true, nil
	}
	// 复用字段词法校验（trailer 与请求头同语法），但不挂到 req.Fields。
	var tmp Request
	if err := parseField(line, off, &tmp); err != nil {
		return false, err
	}
	return false, nil
}

// Close 在请求处理结束时 drain 整个 chunked 体（直到 trailer 空行）。
func (b *chunkedBody) Close() error {
	if b.done {
		return nil
	}
	tmp := make([]byte, 32*1024)
	for !b.done {
		n, err := b.Read(tmp)
		_ = n
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *chunkedBody) incomplete(phase, format string, args ...any) error {
	return protoError(KindIncomplete, phase, b.sr.offset(), -1, format, args...)
}

func (b *chunkedBody) invalid(off, rel int64, phase, format string, args ...any) error {
	return protoError(KindInvalid, phase, off, rel, format, args...)
}

// ---- chunk 行小工具 ----

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func parseHexSize(p []byte, off int64) (int64, error) {
	// strconv.ParseInt 容许前导零，对 chunk-size 合法；但要拦截溢出。
	var n int64
	for _, c := range p {
		d := int64(hexVal(c))
		if n > (1<<63-1-d)>>4 {
			return 0, protoError(KindInvalid, PhaseChunkSize, off, 0,
				"chunk-size %q overflows 63-bit range", string(p))
		}
		n = n<<4 | d
	}
	return n, nil
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}

// indexSemicolon 找未被引号包裹的 ';'（chunk-ext 可能带 quoted-string）。
func indexSemicolon(line []byte) int {
	inQuote := false
	for i, c := range line {
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == ';' && !inQuote:
			return i
		}
	}
	return -1
}

// validateChunkExt 校验 chunk-ext：*( ";" chunk-ext-name [ "=" chunk-ext-val ] )
// 分号前后允许 OWS（RFC 9110 列表空白规则）。ext 为 nil 表示该行没有
// 分号（无扩展），合法；出现分号（ext 非 nil）就必须有非空扩展名，
// "3;" 这种尾随分号非法。
func validateChunkExt(ext []byte, baseOff int64) error {
	if ext == nil {
		return nil
	}
	if len(bytes.TrimSpace(ext)) == 0 {
		return protoError(KindInvalid, PhaseChunkExt, baseOff, 0,
			"chunk extension present but empty: ext-name required after ';'")
	}
	parts := strings.Split(string(ext), ";")
	off := baseOff
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return protoError(KindInvalid, PhaseChunkExt, off, 0, "empty chunk extension")
		}
		eq := strings.IndexByte(p, '=')
		name := p
		if eq >= 0 {
			name = strings.TrimSpace(p[:eq])
			val := strings.TrimSpace(p[eq+1:])
			if !validQuotedOrToken(val) {
				return protoError(KindInvalid, PhaseChunkExt, off+int64(eq)+1, 0,
					"chunk extension value must be token or quoted-string: %q", val)
			}
		}
		if !isToken([]byte(name)) {
			return protoError(KindInvalid, PhaseChunkExt, off, 0,
				"chunk extension name %q is not a token", name)
		}
		off += int64(len(p)) + 1
	}
	return nil
}

func validQuotedOrToken(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if v[0] == '"' {
		if len(v) < 2 || v[len(v)-1] != '"' {
			return false
		}
		for i := 1; i < len(v)-1; i++ {
			c := v[i]
			if c == '"' || c == '\\' { // 不支持 quoted-pair 转义，避免定帧歧义
				return false
			}
			if c < 0x20 && c != '\t' {
				return false
			}
		}
		return true
	}
	return isToken([]byte(v))
}
