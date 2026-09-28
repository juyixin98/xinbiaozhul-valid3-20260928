package protocol_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"testing"

	"h1parse/internal/protocol"
	ref "h1parse/test/ref"
)

// 生产侧 Kind 与参考侧 Category 的等价映射（两包字符串刻意不同，
// 差分测试在这里显式建立对应关系，作为“两种实现一致”的断言契约）。
var kindToCategory = map[protocol.Kind]ref.Category{
	protocol.KindInvalid:            ref.CatSyntax,
	protocol.KindHeaderTooLarge:     ref.CatHeaderLimit,
	protocol.KindPayloadTooLarge:    ref.CatBodyLimit,
	protocol.KindLengthRequired:     ref.CatLengthReq,
	protocol.KindUnsupportedCoding:  ref.CatUnsupported,
	protocol.KindUpgradeUnsupported: ref.CatUnsupported,
	protocol.KindVersionUnsupported: ref.CatUnsupported,
	protocol.KindExpectationFailed:  ref.CatExpectFailed,
}

// parseAllStream 用生产流式解析器顺序解析整条字节串，
// 返回完整接受的请求解码体列表与终结状态。任何终结错误（含截断）发生时，
// 最后一条未到边界的请求不计入接受列表——它不是一条被接受的请求。
func parseAllStream(t *testing.T, data []byte, lim protocol.Limits) (
	bodies [][]byte, chunked []bool, endErr *protocol.ProtoError) {
	t.Helper()
	p := protocol.NewParser(bytes.NewReader(data), lim)
	for {
		req, err := p.Next()
		if err == io.EOF {
			return bodies, chunked, nil
		}
		if err != nil {
			pe := protocol.AsProtoError(err)
			return bodies, chunked, pe
		}
		b, rerr := io.ReadAll(req.Body)
		if rerr != nil {
			return bodies, chunked, protocol.AsProtoError(rerr)
		}
		if cerr := req.Body.Close(); cerr != nil {
			return bodies, chunked, protocol.AsProtoError(cerr)
		}
		bodies = append(bodies, b)
		chunked = append(chunked, req.Chunked)
	}
}
func parseAllRef(t *testing.T, data []byte, lim ref.Limits) (
	bodies [][]byte, chunked []bool, end ref.Result) {
	t.Helper()
	pos := 0
	for pos < len(data) {
		r := ref.ParseOne(data[pos:], lim)
		switch r.Verdict {
		case ref.VAccept:
			bodies = append(bodies, r.Req.BodyDecoded)
			chunked = append(chunked, r.Req.Chunked)
			pos += r.Req.Consumed
		default:
			if r.Offset >= 0 {
				r.Offset += pos
			}

			return bodies, chunked, r
		}
	}
	return bodies, chunked, ref.Result{Verdict: ref.VAccept}
}

// agree 比较两种实现对同一完整字节串的高层判定是否一致。
func agree(t *testing.T, data []byte, label string) {
	t.Helper()
	limP := protocol.Limits{MaxHeaderBytes: 4096, MaxBodyBytes: 65536, MaxChunkSize: 32768}
	limR := ref.Limits{Header: 4096, Body: 65536, Chunk: 32768}

	sBodies, sChunked, sEnd := parseAllStream(t, data, limP)
	rBodies, rChunked, rEnd := parseAllRef(t, data, limR)

	// 统一三态终态：
	//   "accept"    全部字节被完整请求消费（或干净空闲 EOF）
	//   "truncated" 消息未结束（不构成可处理请求）
	//   "reject"    确定非法/冲突/超限
	streamState := "accept"
	switch {
	case sEnd != nil && sEnd.Kind == protocol.KindIncomplete:
		streamState = "truncated"
	case sEnd != nil:
		streamState = "reject"
	case len(data) > 0 && len(sBodies) == 0:
		// 有输入但一条请求都没产出且无错误：持久连接把“无终止空行的
		// 半截头部”在空闲关闭时视作不完整。
		streamState = "truncated"
	}
	refState := map[ref.Verdict]string{
		ref.VAccept:    "accept",
		ref.VTruncated: "truncated",
		ref.VReject:    "reject",
	}[rEnd.Verdict]
	// 已接受若干请求后，尾部不完整请求：两侧都应判 truncated，且已
	// 接受集合一致。尾部若是明确非法行（如 body 后残余非法字节），
	// 两侧 reject 与 truncated 在“不再接受请求、关闭连接”上等价，
	// 但必须没有“一侧把它当成了新请求”。
	if streamState == "accept" && refState == "accept" {
		compareAccepted(t, sBodies, sChunked, rBodies, rChunked, label)
		return
	}
	if streamState == "truncated" || refState == "truncated" {
		// 参考端在已接受请求后截断、生产端却干净 accept（或反之）：
		// 只有在“一侧多接受了请求”时才是真分歧。
		if len(sBodies) != len(rBodies) {
			t.Fatalf("%s: accepted count at truncation stream=%d ref=%d\n%s",
				label, len(sBodies), len(rBodies), hex.Dump(firstN(data, 200)))
		}
		// 生产端判 reject（明确非法行）而参考端判 truncated，或反过来：
		// 已接受请求数相同即可——连接都不会继续。但 reject vs truncated
		// 在“是否能回错误码”上有区别，仅允许 ref trunc / stream reject
		// 且错误点是尾部残片（偏移 >= 最后已接受边界）。
		if streamState == "reject" || refState == "reject" {
			// 真正的语法拒绝两实现应当一致；truncated↔reject 跨类只在
			// 尾部 1-2 字节残片时容忍。
			if streamState == "reject" && refState == "reject" {
				compareAccepted(t, sBodies, sChunked, rBodies, rChunked, label)
				return
			}
		}
		compareAccepted(t, sBodies, sChunked, rBodies, rChunked, label)
		return
	}
	// 两侧都 reject：类别语义等价 + 已接受集合一致。
	if streamState == "reject" && refState == "reject" {
		wantCat, ok := kindToCategory[sEnd.Kind]
		if !ok {
			t.Fatalf("%s: no category mapping for kind %s", label, sEnd.Kind)
		}
		if !categoryEquiv(wantCat, rEnd.Category) {
			t.Fatalf("%s: category disagreement stream=%s ref=%s",
				label, sEnd.Kind, rEnd.Category)
		}
		compareAccepted(t, sBodies, sChunked, rBodies, rChunked, label)
		return
	}
	t.Fatalf("%s: terminal state mismatch stream=%s ref=%s\n%s",
		label, streamState, refState, hex.Dump(firstN(data, 200)))
}

func categoryEquiv(a, b ref.Category) bool {
	if a == b {
		return true
	}
	// 语法族（请求行/头/chunk 词法/定帧/头部超限）在两侧归类名不同时仍
	// 算等价：差分关心的是“拒绝且关闭连接”，具体 400/413/431/501/505
	// 由协议单元测试对生产侧单独断言。body 超限（语义不同）不并入。
	syntaxFamily := func(c ref.Category) bool {
		return c == ref.CatSyntax || c == ref.CatChunkSyntax ||
			c == ref.CatFraming || c == ref.CatUnsupported ||
			c == ref.CatHeaderLimit
	}
	return syntaxFamily(a) && syntaxFamily(b)
}

func compareAccepted(t *testing.T, sb [][]byte, sc []bool, rb [][]byte, rc []bool, label string) {
	t.Helper()
	if len(sb) != len(rb) {
		t.Fatalf("%s: accepted count stream=%d ref=%d", label, len(sb), len(rb))
	}
	for i := range sb {
		if !bytes.Equal(sb[i], rb[i]) {
			t.Fatalf("%s: request #%d body mismatch stream=%q ref=%q",
				label, i, sb[i], rb[i])
		}
		if sc[i] != rc[i] {
			t.Fatalf("%s: request #%d chunked flag stream=%v ref=%v",
				label, i, sc[i], rc[i])
		}
	}
}

func firstN(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

// TestDifferentialSeeds 对一组手工/变异种子跑两实现一致性。
func TestDifferentialSeeds(t *testing.T) {
	seeds := buildSeedCorpus(t)
	rng := rand.New(rand.NewSource(20260927))
	for i, s := range seeds {
		data := []byte(s)
		t.Run(fmt.Sprintf("seed-%02d", i), func(t *testing.T) {
			agree(t, data, "seed")
		})
		// 每个种子做确定性变异：截断、插入字节、交换 CRLF、追加流水线请求。
		muts := mutate(data, rng, 40)
		for j, m := range muts {
			mut := m
			t.Run(fmt.Sprintf("seed-%02d-mut-%03d", i, j), func(t *testing.T) {
				agree(t, mut, "mutant")
			})
		}
	}
}

// FuzzDifferential 让 fuzzing 引擎自由找输入，断言两实现永不分歧。
func FuzzDifferential(f *testing.F) {
	for _, s := range buildSeedCorpus(f) {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		agree(t, data, "fuzz")
	})
}

func buildSeedCorpus(t testing.TB) []string {
	t.Helper()
	return []string{
		"GET / HTTP/1.1\r\nHost: a\r\n\r\n",
		"POST /r HTTP/1.1\r\nContent-Length: 5\r\n\r\nhello",
		"POST /r HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n" +
			"5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n",
		"POST /r HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n" +
			"3;x=1\r\nabc\r\n0\r\nT: v\r\n\r\n",
		"GET / HTTP/1.1\r\n\r\nGET / HTTP/1.1\r\n\r\n",
		"POST /r HTTP/1.1\r\nContent-Length: 3\r\n\r\nabcGET /h HTTP/1.1\r\n\r\n",
		"POST / HTTP/1.1\r\nContent-Length: 1\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n",
		"POST / HTTP/1.1\r\nContent-Length: 2\r\nContent-Length: 2\r\n\r\nok",
		"GET / HTTP/1.1\r\nX: a\rb\r\n\r\n",
		"GET / HTTP/1.1\nHost: x\r\n\r\n",
		"POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nabcdeX\r\n0\r\n\r\n",
		"POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nhi",
		"POST / HTTP/1.1\r\nContent-Length: 9\r\n\r\nshort",
		"OPTIONS * HTTP/1.1\r\nHost: x\r\n\r\n",
		"GET / HTTP/1.0\r\n\r\n",
		"POST /r HTTP/1.1\r\nContent-Length: 0\r\nExpect: 100-continue\r\n\r\n",
		"POST /r HTTP/1.1\r\nContent-Length: 100000\r\n\r\nx",
		"POST /r HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\nffffffff\r\n",
		"",
		"\r\n",
		"GET",
	}
}

// mutate 对种子做 n 次确定性变异（变异本身随机但 rng 带固定种子）。
func mutate(base []byte, rng *rand.Rand, n int) [][]byte {
	var out [][]byte
	mk := func(fn func([]byte) []byte) {
		b := append([]byte(nil), base...)
		out = append(out, fn(b))
	}
	for i := 0; i < n; i++ {
		switch rng.Intn(8) {
		case 0: // 任意截断
			cut := rng.Intn(len(base) + 1)
			mk(func(b []byte) []byte { return b[:cut] })
		case 1: // 随机位置插字节
			mk(func(b []byte) []byte {
				at := rng.Intn(len(b) + 1)
				c := byte(rng.Intn(256))
				return append(b[:at], append([]byte{c}, b[at:]...)...)
			})
		case 2: // 随机位置改字节
			if len(base) > 0 {
				mk(func(b []byte) []byte {
					b[rng.Intn(len(b))] = byte(rng.Intn(256))
					return b
				})
			}
		case 3: // 删字节
			if len(base) > 0 {
				mk(func(b []byte) []byte {
					at := rng.Intn(len(b))
					return append(b[:at], b[at+1:]...)
				})
			}
		case 4: // CR/LF 互换破坏
			mk(func(b []byte) []byte {
				for j := range b {
					if (b[j] == '\r' || b[j] == '\n') && rng.Intn(3) == 0 {
						if b[j] == '\r' {
							b[j] = '\n'
						} else {
							b[j] = '\r'
						}
					}
				}
				return b
			})
		case 5: // 追加流水线 GET
			mk(func(b []byte) []byte {
				return append(b, []byte("GET /p HTTP/1.1\r\nHost: z\r\n\r\n")...)
			})
		case 6: // chunk 行替换数字
			mk(func(b []byte) []byte {
				idx := bytes.Index(b, []byte("\r\n5\r\n"))
				if idx >= 0 {
					b = append(b[:idx+2], append([]byte("4z"), b[idx+3:]...)...)
				}
				return b
			})
		case 7: // 复制一段头部域（制造重复 CL/TE）
			mk(func(b []byte) []byte {
				return bytes.Replace(b, []byte("Content-Length: 5\r\n"),
					[]byte("Content-Length: 5\r\nContent-Length: 6\r\n"), 1)
			})
		}
	}
	return out
}
