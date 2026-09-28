package protocol

import (
	"bytes"
	"io"
)

// streamReader 包装底层连接字节流，为协议解析提供：
//
//   - 绝对字节偏移（自连接首字节），错误日志据此定位；
//   - 可跨多次 Read 的“逻辑读”，因此 TCP 半包对上层透明；
//   - ReadLine：逐行读取并在词法层面拒绝裸 LF / 游离 CR；
//   - 受上限保护的缓冲增长，避免恶意对端用一个超长行耗尽内存。
//
// 粘包：行读取只消费到 CRLF 为止，多出的字节保留在内部缓冲，
// 下一次读取立即拿到。残余字节是否属于下一请求，由连接状态机
// 逐请求判定，解析器自身从不跨行猜测。
type streamReader struct {
	r       io.Reader
	buf     []byte
	absOff  int64 // 已从底层连接读取的总字节数
	lineMax int64 // 单行（不含 CRLF）上限，<=0 表示不限
}

func newStreamReader(r io.Reader, lineMax int64) *streamReader {
	return &streamReader{r: r, lineMax: lineMax}
}

// offset 返回缓冲首字节的绝对偏移。
func (s *streamReader) offset() int64 { return s.absOff - int64(len(s.buf)) }

// discard 标记前 n 个缓冲字节已消费。
func (s *streamReader) discard(n int) {
	if n <= 0 {
		return
	}
	if n >= len(s.buf) {
		// 归还容量：重新切片到底数组，下次 fill 复用。
		s.buf = s.buf[:0]
		return
	}
	copy(s.buf, s.buf[n:])
	s.buf = s.buf[:len(s.buf)-n]
}

// fill 从底层连接再读一批；缓冲满时先扩容（受 lineMax 约束）。
// 返回 io.EOF 表示连接已结束。
func (s *streamReader) fill() error {
	if len(s.buf) == cap(s.buf) {
		newCap := cap(s.buf)
		if newCap == 0 {
			newCap = 4096
		} else {
			newCap *= 2
		}
		if s.lineMax > 0 && int64(newCap) > s.lineMax+1 {
			newCap = int(s.lineMax) + 1
		}
		nb := make([]byte, len(s.buf), newCap)
		copy(nb, s.buf)
		s.buf = nb
	}
	n, err := s.r.Read(s.buf[len(s.buf):cap(s.buf)])
	s.buf = s.buf[:len(s.buf)+n]
	s.absOff += int64(n)
	if n > 0 {
		return nil
	}
	return err
}

// ReadLine 返回不含 CRLF 的行内容（已复制，可安全持有），以及行首绝对偏移。
// 词法规则：
//
//   - 只承认 CRLF 为行终止；裸 LF（前一个字节不是 CR）与游离 CR
//     （CR 后一个字节不是 LF）立即返回 KindInvalid——非法换行不得进入
//     后续解析。检查必须是“单遍、逐字节”的，不能只在缓冲内找 LF：
//     半包到达时 CR 可能在缓冲边界，要等下一批字节到位再判定；
//   - 超过 lineMax 字节仍未换行返回 KindHeaderTooLarge；
//   - 无任何字节时遇到 EOF 返回 io.EOF（正常连接结束）；行中间 EOF
//     返回 KindIncomplete（截断）。
func (s *streamReader) ReadLine() ([]byte, int64, error) {
	lineStart := s.offset()
	for {
		// 本行的判定窗口：到第一个 LF 为止。CR 扫描不能越过这个 LF——
		// LF 之前若成对 CRLF，本行就结束；LF 之后的字节属于下一行，
		// 必须留给下一次 ReadLine（否则会把第二请求的游离 CR 错记到
		// 当前行，导致偏移/请求归属错误）。
		lf := bytes.IndexByte(s.buf, '\n')
		window := s.buf
		if lf >= 0 {
			window = s.buf[:lf+1]
		}
		badCR := -1
		for i := 0; i < len(window); i++ {
			if window[i] != '\r' {
				continue
			}
			if i+1 < len(window) {
				if window[i+1] != '\n' {
					badCR = i
					break
				}
				i++ // 跳过成对 LF
			}
			// CR 落在窗口末尾：
			//  - 若窗口以 LF 结束，这个 CR 后随的正是该 LF（上面 i+1 命中）；
			//  - 否则 CR 在缓冲末尾，等下一批 fill。
		}
		if lf >= 0 {
			if badCR >= 0 {
				return nil, lineStart + int64(badCR), protoError(KindInvalid,
					PhaseRequestLine, lineStart+int64(badCR), -1,
					"stray CR (0x0D) not followed by LF: only CRLF is a legal line break")
			}
			if lf == 0 || s.buf[lf-1] != '\r' {
				return nil, lineStart + int64(lf), protoError(KindInvalid,
					PhaseRequestLine, lineStart+int64(lf), -1,
					"bare LF (0x0A) not preceded by CR: only CRLF is a legal line break")
			}
			line := s.buf[:lf-1]
			out := make([]byte, len(line))
			copy(out, line)
			s.discard(lf + 1)
			return out, lineStart, nil
		}
		if s.lineMax > 0 && int64(len(s.buf)) >= s.lineMax {
			return nil, lineStart, protoError(KindHeaderTooLarge, PhaseHeader,
				s.absOff, -1, "line exceeds limit of %d bytes", s.lineMax)
		}
		oldLen := len(s.buf)
		err := s.fill()
		if err != nil {
			if err == io.EOF {
				if oldLen == 0 {
					return nil, lineStart, io.EOF
				}
				// EOF 确定：缓冲末尾悬留 CR 永远等不到 LF，属确定非法。
				if s.buf[len(s.buf)-1] == '\r' {
					return nil, lineStart + int64(len(s.buf)-1), protoError(
						KindInvalid, PhaseRequestLine,
						lineStart+int64(len(s.buf)-1), -1,
						"stray CR (0x0D) at end of connection: no following LF")
				}
				return nil, lineStart, protoError(KindIncomplete, PhaseRequestLine,
					lineStart, -1, "connection closed in the middle of a line (no terminating CRLF)")
			}
			return nil, lineStart, err
		}
	}
}

// readFull 读满 p。连接在中途关闭返回 KindIncomplete（截断）。
// 返回读取起点的绝对偏移。
func (s *streamReader) readFull(p []byte, phase string) (int64, error) {
	startOff := s.offset()
	have := 0
	for have < len(p) {
		if len(s.buf) > 0 {
			n := copy(p[have:], s.buf)
			s.discard(n)
			have += n
			continue
		}
		err := s.fill()
		if err != nil {
			if err == io.EOF {
				return startOff, protoError(KindIncomplete, phase,
					startOff+int64(have), int64(have),
					"connection closed: expected %d more bytes, got %d",
					len(p)-have, have)
			}
			return startOff, err
		}
	}
	return startOff, nil
}

// readByte 读单字节，EOF 映射为 KindIncomplete。
func (s *streamReader) readByte(phase string, rel int64) (byte, int64, error) {
	var b [1]byte
	off, err := s.readFull(b[:], phase)
	if err != nil {
		return 0, off, err
	}
	return b[0], off, nil
}

// buffered 当前已缓冲未消费字节数（测试用）。
func (s *streamReader) buffered() int { return len(s.buf) }
