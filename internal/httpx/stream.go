package httpx

import (
	"errors"
	"io"
)

// stream is a fill-on-demand byte window over a connection with an
// absolute byte cursor. It is the only component that touches the
// net.Conn read path; parsers work against peek/consume primitives so
// half packets ("半包") and pipelined back-to-back requests ("粘包")
// are handled identically to one big read: parsing consumes exactly
// the bytes of one message and leaves the start of the next message
// at cursor zero.
type stream struct {
	r       io.Reader
	buf     []byte // unread window
	start   int    // start of unread data in buf
	end     int    // end of valid data in buf
	offset  int64  // absolute stream offset of buf[start]
	maxGrow int    // per-fill hard ceiling
}

func newStream(r io.Reader, initial, maxGrow int) *stream {
	if initial < 512 {
		initial = 512
	}
	if maxGrow < initial {
		maxGrow = initial
	}
	return &stream{
		r:       r,
		buf:     make([]byte, 0, initial),
		maxGrow: maxGrow,
	}
}

// Offset returns the absolute stream offset of the first unread byte.
func (s *stream) Offset() int64 { return s.offset }

// Buffered returns how many parsed-but-unconsumed bytes are present.
func (s *stream) Buffered() int { return s.end - s.start }

// Peek returns the unread window, filling from the underlying reader
// if empty. The returned slice is invalidated by the next fill.
// ErrCleanEOF is returned at a clean end; errIncomplete cannot occur
// here (at least one new byte is guaranteed on a nil error).
func (s *stream) Peek() ([]byte, error) {
	if s.start < s.end {
		return s.buf[s.start:s.end], nil
	}
	return s.fill()
}

// fill reads more bytes. When the buffer is fully consumed it resets
// to the beginning so a long-lived keep-alive connection never copies
// a growing prefix; unread tails are shifted down. The buffer is grown
// geometrically up to maxGrow; callers (ReadLine / ReadAppend) own the
// protocol-level length limits and inspect offsets before calling
// again, so growth beyond the ceiling is refused here.
func (s *stream) fill() ([]byte, error) {
	if s.start == s.end {
		s.start, s.end = 0, 0
	} else if s.start > 0 {
		n := copy(s.buf, s.buf[s.start:s.end])
		s.start, s.end = 0, n
	}
	if s.end == len(s.buf) {
		next := cap(s.buf)
		if next == 0 {
			next = 512
		}
		for next < s.end+1 {
			next *= 2
		}
		if next > s.maxGrow {
			return nil, io.ErrShortBuffer
		}
		s.buf = s.buf[:next]
	}
	n, err := s.r.Read(s.buf[s.end:])
	s.end += n
	if n > 0 {
		return s.buf[s.start:s.end], nil
	}
	if err == nil {
		// Reader contract violation: treat as short read failure.
		return nil, io.ErrNoProgress
	}
	if errors.Is(err, io.EOF) {
		if s.start < s.end {
			return s.buf[s.start:s.end], errIncomplete
		}
		return nil, ErrCleanEOF
	}
	return nil, err
}

// ensure makes sure at least n unread bytes are present, filling as
// required. Returns errIncomplete if the stream ends early.
func (s *stream) ensure(n int) ([]byte, error) {
	for s.end-s.start < n {
		if _, err := s.fill(); err != nil {
			return nil, err
		}
	}
	return s.buf[s.start : s.start+n], nil
}

// advance consumes n bytes of the window.
func (s *stream) advance(n int) {
	if n > s.end-s.start {
		panic("httpx: advance past buffered data")
	}
	s.start += n
	s.offset += int64(n)
}

// ReadLine consumes one line (not including the terminating CRLF) and
// returns the line bytes, the absolute offset of the line's first
// byte, and the offset of the first byte AFTER CRLF.
//
// Bare LF (a LF not immediately preceded by CR) is rejected here,
// per RFC 9112: line terminators are exactly CRLF. A line that grows
// past lineLimit before a CRLF is rejected with errLineTooLong.
// ErrCleanEOF only occurs at an empty request boundary.
func (s *stream) ReadLine(lineLimit int) (line []byte, lineStart, nextOffset int64, err error) {
	lineStart = s.offset
	for {
		// Search only newly buffered content on each iteration.
		win := s.buf[s.start:s.end]
		if i := indexCRLF(win); i >= 0 {
			if i > lineLimit {
				return nil, lineStart, lineStart + int64(i), errLineTooLong
			}
			line = append([]byte(nil), win[:i]...)
			s.advance(i + 2)
			return line, lineStart, s.offset, nil
		}
		// Reject bare LF anywhere in the current window. A lone CR
		// split across packets is rechecked after the next fill
		// (indexCRLF returns -1 for a trailing CR).
		if j := indexBareLF(win); j >= 0 {
			return nil, lineStart, lineStart + int64(j), &bareLFError{Offset: lineStart + int64(j)}
		}
		if s.end-s.start > lineLimit {
			return nil, lineStart, s.offset + int64(s.end-s.start), errLineTooLong
		}
		if _, ferr := s.fill(); ferr != nil {
			return nil, lineStart, s.offset + int64(s.end-s.start), ferr
		}
	}
}

// ReadAppend appends exactly n message-body bytes into dst, without
// discarding over-read pipelined bytes. Returns the resulting slice.
// errIncomplete means the connection ended before n bytes arrived.
func (s *stream) ReadAppend(dst []byte, n int64) ([]byte, error) {
	for n > 0 {
		win := s.buf[s.start:s.end]
		if len(win) == 0 {
			if _, err := s.fill(); err != nil {
				return dst, err
			}
			win = s.buf[s.start:s.end]
		}
		take := int64(len(win))
		if take > n {
			take = n
		}
		dst = append(dst, win[:take]...)
		s.advance(int(take))
		n -= take
	}
	return dst, nil
}

// indexCRLF finds the first CRLF. A trailing CR at the very end is
// not matched (the LF may be the next byte of the next packet).
func indexCRLF(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == '\r' && b[i+1] == '\n' {
			return i
		}
	}
	return -1
}

// indexBareLF finds a LF that is not preceded by CR. The byte at
// index 0 is "bare" if it is LF (a CR would have had to belong to a
// prior line already consumed).
func indexBareLF(b []byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' && (i == 0 || b[i-1] != '\r') {
			return i
		}
	}
	return -1
}
