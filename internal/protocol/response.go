package protocol

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"time"
)

// ResponseWriter 是连接状态机交给业务层的响应出口。它与请求一一对应：
// 业务层写一次状态行 + 若干响应头 + 至多一个定长响应体，状态机在
// 业务返回后调用 Flush 把帧完整序列化到底层连接。
type ResponseWriter interface {
	// WriteHeader 发送状态行与响应头。status<=0 时按 200 处理；
	// 重复调用以第一次为准。
	WriteHeader(status int)
	// Header 返回可变响应头映射（在 WriteHeader 前设置）。
	Header() Header
	// Write 写入响应体。首次 Write 会先提交状态码并自动补
	// Content-Length。HEAD 请求调用 Write 返回错误。
	Write([]byte) (int, error)
	// WroteHeader 报告头部是否已发送。
	WroteHeader() bool
	// Status 返回已提交的状态码（0 表示尚未提交）。
	Status() int
	// SetCloseAfter 强制本响应后关闭连接（panic 兜底等场景）。
	SetCloseAfter(v bool)
	// CloseAfter 报告本响应后是否必须关闭连接。
	CloseAfter() bool
	// Flush 序列化整条响应到底层连接。
	Flush() error
}

// Header 是响应头容器，键使用规范写法由序列化时处理。
type Header map[string][]string

func (h Header) Set(k, v string) { h[k] = []string{v} }
func (h Header) Add(k, v string) { h[k] = append(h[k], v) }
func (h Header) Get(k string) string {
	for hk, vs := range h {
		if equalFold(hk, k) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x >= 'a' && x <= 'z' {
			x -= 0x20
		}
		if y >= 'a' && y <= 'z' {
			y -= 0x20
		}
		if x != y {
			return false
		}
	}
	return true
}

// responseWriter 缓冲一条响应直到状态确定。响应体在内存缓冲，大小受
// MaxResponseBytes 限制；本服务的业务体很小（JSON + 回显），不引入
// 分块响应，客户端总能拿到精确 Content-Length。
type responseWriter struct {
	w           *bufio.Writer
	status      int
	header      Header
	body        bytes.Buffer
	wroteHeader bool
	headOnly    bool // HEAD 请求：序列化时不发送体
	closeAfter  bool
	version     string
	maxBody     int64
	wroteBytes  bool
}

// NewResponseWriter 在一条缓冲输出连接上构造单请求响应出口。
// headOnly=true 用于 HEAD（发送 Content-Length 但不发送体）。
func NewResponseWriter(w *bufio.Writer, headOnly, closeAfter bool, maxBody int64) ResponseWriter {
	return &responseWriter{
		w:          w,
		header:     Header{},
		headOnly:   headOnly,
		closeAfter: closeAfter,
		version:    "HTTP/1.1",
		maxBody:    maxBody,
	}
}

// SetCloseAfter 强制本响应后关闭连接。
func (rw *responseWriter) SetCloseAfter(v bool) { rw.closeAfter = v }

// CloseAfter 报告本响应后是否关闭连接。
func (rw *responseWriter) CloseAfter() bool { return rw.closeAfter }

func (rw *responseWriter) Header() Header { return rw.header }

func (rw *responseWriter) WriteHeader(status int) {
	if rw.wroteHeader {
		return
	}
	if status <= 0 {
		status = 200
	}
	rw.status = status
	rw.wroteHeader = true
}

func (rw *responseWriter) WroteHeader() bool { return rw.wroteHeader }
func (rw *responseWriter) Status() int       { return rw.status }

func (rw *responseWriter) Write(p []byte) (int, error) {
	if rw.headOnly {
		// HEAD：业务层可能仍“产生”主体用于算长度，但不线上发送。
		// 这里选择直接拒绝写体并记账长度——为简单与可预测，业务层
		// 对 HEAD 不调用 Write。
		return 0, fmt.Errorf("http1: Write called for HEAD response")
	}
	if rw.maxBody > 0 && int64(rw.body.Len()+len(p)) > rw.maxBody {
		return 0, fmt.Errorf("http1: response body exceeds limit %d", rw.maxBody)
	}
	if !rw.wroteHeader {
		rw.WriteHeader(200)
	}
	rw.wroteBytes = true
	return rw.body.Write(p)
}

// statusText 提供本服务实际会发出的状态短语；未知状态退回空串由
// flush 兜底为数字。
func statusText(status int) string {
	switch status {
	case 100:
		return "Continue"
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 204:
		return "No Content"
	case 400:
		return "Bad Request"
	case 404:
		return "Not Found"
	case 405:
		return "Method Not Allowed"
	case 411:
		return "Length Required"
	case 413:
		return "Content Too Large"
	case 417:
		return "Expectation Failed"
	case 431:
		return "Request Header Fields Too Large"
	case 500:
		return "Internal Server Error"
	case 501:
		return "Not Implemented"
	case 505:
		return "HTTP Version Not Supported"
	}
	return ""
}

// Flush 序列化整条响应。若业务层从未写头部，按 200（有体）/204（无体）。
func (rw *responseWriter) Flush() error {
	if !rw.wroteHeader {
		if rw.body.Len() > 0 {
			rw.WriteHeader(200)
		} else {
			rw.WriteHeader(204)
		}
	}
	// 规范响应头：Date / Connection / Content-Length。
	if rw.header.Get("Date") == "" {
		rw.header.Set("Date", time.Now().UTC().Format(time.RFC1123))
	}
	if rw.closeAfter {
		rw.header.Set("Connection", "close")
	} else {
		rw.header.Set("Connection", "keep-alive")
	}
	// HEAD 也给出 Content-Length（描述 GET 会返回的表示长度），只是不发体。
	rw.header.Set("Content-Length", strconv.Itoa(rw.body.Len()))

	text := statusText(rw.status)
	if text == "" {
		text = "Status"
	}
	if _, err := fmt.Fprintf(rw.w, "%s %d %s\r\n", rw.version, rw.status, text); err != nil {
		return err
	}
	// 稳定输出顺序：常见头先出，其余按名字排序，便于夹具逐字节比对。
	if err := writeHeadersOrdered(rw.w, rw.header); err != nil {
		return err
	}
	if _, err := io.WriteString(rw.w, "\r\n"); err != nil {
		return err
	}
	if !rw.headOnly && rw.body.Len() > 0 {
		if _, err := rw.w.Write(rw.body.Bytes()); err != nil {
			return err
		}
	}
	return rw.w.Flush()
}

// writeHeadersOrdered 按固定优先顺序 + 字典序输出，Date 单独处理。
func writeHeadersOrdered(w io.Writer, h Header) error {
	preferred := []string{"Content-Type", "Content-Length", "Connection", "Date"}
	seen := map[string]bool{}
	write := func(k string) error {
		for _, v := range h[k] {
			if _, err := fmt.Fprintf(w, "%s: %s\r\n", k, v); err != nil {
				return err
			}
		}
		seen[k] = true
		return nil
	}
	for _, k := range preferred {
		if vs, ok := lookupCanon(h, k); ok {
			for _, v := range vs {
				if _, err := fmt.Fprintf(w, "%s: %s\r\n", k, v); err != nil {
					return err
				}
			}
			seen[k] = true
		}
	}
	// 其余键按字典序（大小写不敏感）。
	var rest []string
	for k := range h {
		if !seenFold(seen, k) {
			rest = append(rest, k)
		}
	}
	// 简单插入排序，头数量很少。
	for i := 1; i < len(rest); i++ {
		for j := i; j > 0 && lessFold(rest[j], rest[j-1]); j-- {
			rest[j], rest[j-1] = rest[j-1], rest[j]
		}
	}
	for _, k := range rest {
		if err := write(k); err != nil {
			return err
		}
	}
	return nil
}

func lookupCanon(h Header, canon string) ([]string, bool) {
	for k, v := range h {
		if equalFold(k, canon) {
			return v, true
		}
	}
	return nil, false
}

func seenFold(seen map[string]bool, k string) bool {
	for s := range seen {
		if equalFold(s, k) {
			return true
		}
	}
	return false
}

func lessFold(a, b string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 0x20
		}
		if y >= 'A' && y <= 'Z' {
			y += 0x20
		}
		if x != y {
			return x < y
		}
	}
	return len(a) < len(b)
}

// WriteContinue 发送 100 (Continue) 中间响应。
func WriteContinue(w *bufio.Writer) error {
	if _, err := io.WriteString(w, "HTTP/1.1 100 Continue\r\n\r\n"); err != nil {
		return err
	}
	return w.Flush()
}
