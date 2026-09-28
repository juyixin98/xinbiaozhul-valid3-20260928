// Command genfixtures 以确定性方式生成全部线协议夹具（.http 字节串）
// 与 manifest.json。夹具不是测试运行时临时拼接的：生成结果签入仓库，
// 测试逐字节加载，保证“逐字节输入”与可复现性。
//
//	go run ./cmd/genfixtures         # 写入 ./test/fixtures
//	go run ./cmd/genfixtures -check  # 校验已签入夹具与生成器一致
//
// 所有夹具一律使用 CRLF；.http 文件配合 .gitattributes 的 -text
// 属性签入，避免行尾被改。
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type fixture struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Desc     string `json:"desc"`
	Group    string `json:"group"`
	Expected string `json:"expected"` // accept | reject | truncated
	Status   int    `json:"status,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Phase    string `json:"phase,omitempty"`
	Offset   *int64 `json:"offset,omitempty"`
	Accepts  int    `json:"accepts,omitempty"`
	Legal    bool   `json:"legal,omitempty"`
	Note     string `json:"note,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
}

type caseData struct {
	fi fixture
	b  []byte
}

func main() {
	check := flag.Bool("check", false, "verify checked-in fixtures match generator output")
	outDir := flag.String("out", "test/fixtures", "fixture output directory")
	flag.Parse()

	cases := build()
	if *check {
		if err := verify(*outDir, cases); err != nil {
			fmt.Fprintln(os.Stderr, "genfixtures:", err)
			os.Exit(1)
		}
		fmt.Println("fixtures: up to date")
		return
	}
	if err := writeAll(*outDir, cases); err != nil {
		fmt.Fprintln(os.Stderr, "genfixtures:", err)
		os.Exit(1)
	}
	fmt.Printf("fixtures: wrote %d cases to %s\n", len(cases), *outDir)
}

// bld 按 CRLF 逐行构造字节串。
type bld struct{ b bytes.Buffer }

func (w *bld) line(s string)  { w.b.WriteString(s); w.b.WriteString("\r\n") }
func (w *bld) raw(s string)   { w.b.WriteString(s) }
func (w *bld) bytes(s []byte) { w.b.Write(s) }

func build() []caseData {
	var out []caseData
	add := func(id, group, desc, expected string, w *bld, mut func(*fixture)) {
		f := fixture{ID: id, Group: group, Desc: desc, Expected: expected,
			File: id + ".http"}
		if mut != nil {
			mut(&f)
		}
		data := append([]byte(nil), w.b.Bytes()...)
		sum := sha256.Sum256(data)
		f.SHA256 = hex.EncodeToString(sum[:])
		out = append(out, caseData{fi: f, b: data})
	}
	offPtr := func(n int64) *int64 { return &n }

	// ---- 合法语法参考（legal）----
	{
		w := &bld{}
		w.line("GET / HTTP/1.1")
		w.line("Host: example.test")
		w.line("User-Agent: h1parse-ref")
		w.line("")
		add("legal-get-basic", "legal", "最简 GET，持久连接", "accept", w,
			func(f *fixture) { f.Legal = true; f.Accepts = 1; f.Status = 200 })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: example.test")
		w.line("Content-Type: text/plain")
		w.line("Content-Length: 5")
		w.line("")
		w.raw("hello")
		add("legal-post-fixed", "legal", "固定长度消息体", "accept", w,
			func(f *fixture) { f.Legal = true; f.Accepts = 1; f.Status = 201 })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: example.test")
		w.line("Transfer-Encoding: chunked")
		w.line("")
		w.line("5")
		w.raw("hello")
		w.line("")
		w.line("6;name=x")
		w.raw(" world")
		w.line("")
		w.line("0")
		w.line("ETag: \"ref\"")
		w.line("")
		add("legal-post-chunked-ext", "legal", "chunked 多块、分块扩展与 trailer", "accept", w,
			func(f *fixture) { f.Legal = true; f.Accepts = 1; f.Status = 201 })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: example.test")
		w.line("Content-Length: 3")
		w.line("")
		w.raw("abc")
		w.line("GET /healthz HTTP/1.1")
		w.line("Host: example.test")
		w.line("")
		add("legal-pipeline-2", "legal", "流水线：POST 后紧跟 GET，边界精确", "accept", w,
			func(f *fixture) { f.Legal = true; f.Accepts = 2 })
	}
	{
		w := &bld{}
		w.line("OPTIONS * HTTP/1.1")
		w.line("Host: example.test")
		w.line("")
		add("legal-options-asterisk", "legal", "asterisk-form 请求目标（协议层接受，业务无此路由回 404）", "accept", w,
			func(f *fixture) {
				f.Legal = true
				f.Accepts = 1
				f.Status = 404
				f.Note = "404 是路由结果；协议层必须接受 asterisk-form"
			})
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: example.test")
		w.line("Content-Length: 11")
		w.line("Expect: 100-continue")
		w.line("")
		w.raw("expect-body")
		add("legal-expect-continue", "legal", "Expect: 100-continue", "accept", w,
			func(f *fixture) { f.Legal = true; f.Accepts = 1; f.Status = 201 })
	}
	{
		w := &bld{}
		w.line("GET / HTTP/1.1")
		w.line("Host: example.test")
		w.line("Content-Length: 0")
		w.line("")
		add("legal-get-cl-zero", "legal", "GET 带显式 Content-Length: 0", "accept", w,
			func(f *fixture) { f.Legal = true; f.Accepts = 1; f.Status = 200 })
	}
	{
		w := &bld{}
		w.line("POST / HTTP/1.1")
		w.line("Host: x")
		w.line("Content-Length: 5")
		w.line("Content-Length: 05")
		w.line("")
		w.raw("12345")
		add("legal-duplicate-cl-leadingzero", "legal",
			"重复 Content-Length 仅前导零差异（数值相同）应接受", "accept", w,
			func(f *fixture) {
				f.Legal = true
				f.Accepts = 1
				f.Status = 404
				f.Note = "POST / 业务无路由回 404，但协议必须接受重复且等值的 CL"
			})
	}

	// ---- 非法换行 / 词法 ----
	{
		w := &bld{}
		w.bytes([]byte("GET / HTTP/1.1\nHost: x\r\n\r\n"))
		add("bad-bare-lf-requestline", "linebreak", "请求行用裸 LF 而非 CRLF", "reject", w,
			func(f *fixture) {
				f.Status = 400
				f.Kind = "invalid_request"
				f.Phase = "request-line"
				f.Offset = offPtr(14)
			})
	}
	{
		w := &bld{}
		w.bytes([]byte("GET / HTTP/1.1\r\nX-Bad: a\rb\r\n\r\n"))
		add("bad-stray-cr-in-value", "linebreak", "头部值中游离 CR", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "header" })
	}
	{
		w := &bld{}
		w.line("GET  /  HTTP/1.1")
		w.line("")
		add("bad-extra-sp-requestline", "syntax", "请求行多余空白", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "request-line" })
	}
	{
		w := &bld{}
		w.line("GET / HTTP/1.1")
		w.line("Bad Header: x")
		w.line("")
		add("bad-space-in-fieldname", "syntax", "字段名含空格（obs-fold 类）", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "header" })
	}
	{
		w := &bld{}
		w.line("GET / HTTP/1.1")
		w.line("X-No-Colon")
		w.line("")
		add("bad-no-colon", "syntax", "头部域无冒号", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request" })
	}
	{
		w := &bld{}
		w.line("GET http://evil.example/ HTTP/1.1")
		w.line("")
		add("bad-absolute-form", "syntax", "absolute-form 目标（代理语义）必须拒绝", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "request-line" })
	}
	{
		w := &bld{}
		w.line("GET / HTTP/1.0")
		w.line("")
		add("bad-version-10", "syntax", "非 HTTP/1.1 版本", "reject", w,
			func(f *fixture) { f.Status = 505; f.Kind = "version_unsupported"; f.Phase = "request-line" })
	}

	// ---- 定帧冲突（走私）----
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Content-Length: 6")
		w.line("Transfer-Encoding: chunked")
		w.line("")
		w.line("0")
		w.line("")
		w.line("GET /admin HTTP/1.1")
		w.line("Host: x")
		w.line("")
		add("smuggle-te-cl-both", "smuggling",
			"CL.TE 经典形态：CL 与 TE 同时出现", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "framing" })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Content-Length: 5")
		w.line("Content-Length: 6")
		w.line("")
		w.raw("12345")
		add("smuggle-duplicate-cl-differ", "smuggling",
			"重复 Content-Length 长度不一致", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "framing" })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Transfer-Encoding: chunked")
		w.line("Transfer-Encoding: identity")
		w.line("")
		add("bad-te-identity", "smuggling", "不支持的 transfer-coding", "reject", w,
			func(f *fixture) { f.Status = 501; f.Kind = "unsupported_transfer_coding" })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Transfer-Encoding: chunked, gzip")
		w.line("")
		add("bad-chunked-not-last", "smuggling", "chunked 后还有编码", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request" })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Content-Length: 3, 5")
		w.line("")
		add("bad-cl-comma-list", "smuggling", "逗号形式的 Content-Length 列表", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request" })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Content-Length: 99999999999999999999999999")
		w.line("")
		add("bad-cl-overflow", "syntax", "Content-Length 溢出", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request" })
	}

	// ---- chunked 词法 ----
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Transfer-Encoding: chunked")
		w.line("")
		w.line("5")
		w.raw("abcdeX")
		w.line("")
		w.line("0")
		w.line("")
		add("bad-chunk-bad-crlf", "chunked", "chunk-data 后非 CRLF（X 替代 CR）", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "chunk-data" })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Transfer-Encoding: chunked")
		w.line("")
		w.line("5g")
		w.raw("abcde")
		w.line("")
		add("bad-chunk-size-hex", "chunked", "chunk-size 非十六进制", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "chunk-size" })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Transfer-Encoding: chunked")
		w.line("")
		w.line("10000000000000000")
		w.line("")
		add("bad-chunk-size-overflow", "chunked", "chunk-size 超 63 位", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request" })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Transfer-Encoding: chunked")
		w.line("")
		w.line("100")
		w.bytes(make([]byte, 0x100))
		w.line("")
		w.line("0")
		w.line("")
		add("bad-chunk-too-large", "limits", "单 chunk 256B，测试以 MaxChunkSize=128 配置运行", "reject", w,
			func(f *fixture) {
				f.Status = 413
				f.Kind = "payload_too_large"
				f.Phase = "chunk-size"
				f.Note = "e2e 以 H1PARSE_MAX_CHUNK_BYTES=128 运行该例"
			})
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Content-Length: 1048577")
		w.line("")
		add("bad-body-too-large-declared", "limits", "声明 Content-Length 超默认 1MiB", "reject", w,
			func(f *fixture) { f.Status = 413; f.Kind = "payload_too_large"; f.Phase = "framing" })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Transfer-Encoding: chunked")
		w.line("")
		w.line("3;")
		w.raw("000")
		w.line("")
		w.line("0")
		w.line("")
		add("bad-chunk-ext-trailing-semicolon", "chunked", "尾随分号但无扩展名", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "chunk-ext" })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Transfer-Encoding: chunked")
		w.line("")
		w.line("3;=0")
		w.raw("000")
		w.line("")
		w.line("0")
		w.line("")
		add("bad-chunk-ext-empty-name", "chunked", "分号后扩展名为空（=0）", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "chunk-ext" })
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Transfer-Encoding: chunked")
		w.line("")
		w.line(" 8001")
		add("bad-chunk-size-leading-space", "chunked", "chunk-size 前导空白（解析差异走私手法）", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "chunk-size" })
	}
	{
		w := &bld{}
		w.line("GET / HTTP/1.1")
		w.raw("\r")
		add("bad-trailing-cr-only", "linebreak", "请求行后只有 CR 即 EOF，永不补 LF", "reject", w,
			func(f *fixture) { f.Status = 400; f.Kind = "invalid_request"; f.Phase = "request-line" })
	}

	// ---- 截断 ----
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Content-Length: 10")
		w.line("")
		w.raw("abc")
		add("trunc-fixed-body", "truncation", "固定长度体提前 EOF", "truncated", w, nil)
	}
	{
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Transfer-Encoding: chunked")
		w.line("")
		w.line("5")
		w.raw("he")
		add("trunc-chunk-data", "truncation", "chunk-data 中途 EOF", "truncated", w, nil)
	}
	{
		w := &bld{}
		w.bytes([]byte("GET / HTTP/1.1\r\nHost: x\r\n"))
		add("trunc-header-no-terminator", "truncation", "头部无终止空行即 EOF", "truncated", w, nil)
	}

	// ---- 边界：残余体内嵌伪造请求，不得当下一请求 ----
	{
		// "12345" + "GET / HTTP/1.1\r\n" + "Host: y\r\n\r\n"
		// = 5 + 16 + 11 = 32 字节，整个伪造请求都在消息体里；
		// 体读尽后连接干净 EOF，不存在第二请求。
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Content-Length: 32")
		w.line("")
		w.raw("12345GET / HTTP/1.1\r\nHost: y\r\n\r\n")
		add("legal-cl-body-swallow", "legal",
			"32 字节体完整吞掉内嵌伪造请求串，不产生第二请求", "accept", w,
			func(f *fixture) { f.Legal = true; f.Accepts = 1; f.Status = 201 })
	}
	{
		// 同一字节串但声明 33：少 1 字节 -> 截断，证明连接被关闭而非继续解析。
		w := &bld{}
		w.line("POST /records HTTP/1.1")
		w.line("Host: x")
		w.line("Content-Length: 33")
		w.line("")
		w.raw("12345GET / HTTP/1.1\r\nHost: y\r\n\r\n")
		add("trunc-cl-body-offbyone", "truncation",
			"声明 33 实际 32：内嵌请求串不得因为截断被当作下一请求", "truncated", w,
			func(f *fixture) {
				f.Note = "期望服务端关闭连接；内嵌的 GET 永不被处理"
			})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].fi.ID < out[j].fi.ID })
	return out
}

// ---- 写盘 / 校验 ----

func writeAll(dir string, cases []caseData) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fxList := make([]fixture, len(cases))
	var sums bytes.Buffer
	for i, c := range cases {
		fxList[i] = c.fi
		if err := os.WriteFile(filepath.Join(dir, c.fi.File), c.b, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%s  %s\n", c.fi.SHA256, c.fi.File)
	}
	manifest, err := json.MarshalIndent(fxList, "", "  ")
	if err != nil {
		return err
	}
	manifest = append(manifest, '\n')
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), sums.Bytes(), 0o644)
}

func verify(dir string, cases []caseData) error {
	wantManifest, err := json.MarshalIndent(func() []fixture {
		fx := make([]fixture, len(cases))
		for i, c := range cases {
			fx[i] = c.fi
		}
		return fx
	}(), "", "  ")
	if err != nil {
		return err
	}
	wantManifest = append(wantManifest, '\n')
	gotManifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	if !bytes.Equal(gotManifest, wantManifest) {
		return fmt.Errorf("manifest.json drift: rerun 'go run ./cmd/genfixtures'")
	}
	for _, c := range cases {
		got, err := os.ReadFile(filepath.Join(dir, c.fi.File))
		if err != nil {
			return err
		}
		if !bytes.Equal(got, c.b) {
			return fmt.Errorf("%s drift: rerun the generator", c.fi.File)
		}
	}
	return nil
}
