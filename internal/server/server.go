// Package server 是连接状态机：accept 连接、在一条持久连接上串行处理
// 多条请求（含流水线），并负责把协议错误翻译为“回送错误响应 + 关闭”
// 或“静默关闭（截断/干净 EOF）”。业务逻辑通过 HandlerFunc 注入，本包
// 不关心路由与存储，也不做任何代理转发。
package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"h1parse/internal/logging"
	"h1parse/internal/protocol"
)

// HandlerFunc 在请求已被定帧后执行业务，必须通过 rw 写出恰好一条响应。
// 请求体未读尽时，状态机在返回后 drain，使流水线中下一请求边界对齐。
type HandlerFunc func(ctx context.Context, rw protocol.ResponseWriter, req *protocol.Request)

// Server 是 HTTP/1.1 子集服务。
type Server struct {
	Addr    string
	Limits  protocol.Limits
	Handler HandlerFunc
	Log     *logging.Logger
	IdleTO  time.Duration

	// ReadySignal 非 nil 时，listener 绑定成功后 Serve 会关闭它。
	ReadySignal chan struct{}

	listenerMu sync.Mutex
	listener   net.Listener
	readyOnce  sync.Once
	seq        atomic.Uint64
	closed     atomic.Bool
}

// Ready 返回 listener 绑定后关闭的通道（惰性创建）。
func (s *Server) Ready() <-chan struct{} {
	if s.ReadySignal == nil {
		s.ReadySignal = make(chan struct{})
	}
	return s.ReadySignal
}

func (s *Server) markReady() {
	s.readyOnce.Do(func() {
		if s.ReadySignal == nil {
			s.ReadySignal = make(chan struct{})
		}
		close(s.ReadySignal)
	})
}

func (s *Server) listenerRef() net.Listener {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	return s.listener
}

// Serve 开始监听并阻塞直到 Shutdown。
func (s *Server) Serve() error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.Addr, err)
	}
	s.listenerMu.Lock()
	s.listener = ln
	s.listenerMu.Unlock()
	s.markReady()
	s.Log.Info("accept", "server listening", logging.Fields{
		"addr":          s.AddrActual(),
		"limits_header": s.Limits.MaxHeaderBytes,
		"limits_body":   s.Limits.MaxBodyBytes,
		"limits_chunk":  s.Limits.MaxChunkSize,
	})
	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.closed.Load() {
				return nil
			}
			s.Log.Error("accept", "accept failed", logging.Fields{"error": err.Error()})
			continue
		}
		go s.serveConn(conn)
	}
}

// AddrActual 在 Serve 后返回实际监听地址（":0" 分配端口时有用）。
func (s *Server) AddrActual() string {
	if ln := s.listenerRef(); ln != nil {
		return ln.Addr().String()
	}
	return s.Addr
}

// Shutdown 停止接受新连接并关闭监听套接字。
func (s *Server) Shutdown(ctx context.Context) error {
	s.closed.Store(true)
	if ln := s.listenerRef(); ln != nil {
		return ln.Close()
	}
	return nil
}

func (s *Server) serveConn(conn net.Conn) {
	connID := fmt.Sprintf("c%04d", s.seq.Add(1))
	defer conn.Close()

	bw := bufio.NewWriterSize(conn, 16*1024)
	// 关键：每连接一个持久 Parser。底层连接作为唯一字节源，Parser 内部
	// 缓冲持有跨请求预读的流水线字节；请求体帧与下一请求共享该缓冲。
	parser := protocol.NewParser(conn, s.Limits)

	keepAlive := true
	reqNo := uint64(0)
	for keepAlive {
		if s.IdleTO > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(s.IdleTO))
		}
		reqNo++
		reqStart := time.Now()

		req, err := parser.Next()
		if err != nil {
			if isGracefulEnd(err, connID, reqNo, s.Log) {
				return
			}
			s.handleProtocolError(bw, err, connID, reqNo)
			return // 任何协议错误后都关闭连接
		}

		closeAfter := req.CloseAfter
		rw := protocol.NewResponseWriter(bw, req.Method == "HEAD", closeAfter, s.Limits.MaxBodyBytes)

		// Expect: 100-continue：头部合法才允许对端发送体。
		if req.Expect100 {
			if err := protocol.WriteContinue(bw); err != nil {
				s.Log.Error("response", "write 100-continue failed", logging.Fields{
					"conn": connID, "req": reqNo, "error": err.Error()})
				return
			}
		}

		s.dispatch(context.Background(), rw, req, connID, reqNo)

		// 业务层结束后：drain 残余消息体，保证下一请求从精确边界开始。
		// 注意先在逻辑上得到响应（rw 已缓冲），再处理 drain 结果：
		// 即便 drain 失败也要把已生成的响应发出去，然后关闭连接。
		drainErr := error(nil)
		if req.Body != nil {
			drainErr = req.Body.Close()
		}
		if drainErr != nil {
			s.logProtoWarn("body-drain", drainErr, connID, reqNo,
				"residual body could not be drained; closing connection")
			rw.SetCloseAfter(true)
		}
		if err := rw.Flush(); err != nil {
			s.Log.Error("response", "flush failed", logging.Fields{
				"conn": connID, "req": reqNo, "error": err.Error()})
			return
		}

		closeAfter = rw.CloseAfter()
		keepAlive = !closeAfter
		s.Log.Info("request", "request served", logging.Fields{
			"conn": connID, "req": reqNo,
			"method": req.Method, "target": req.Target,
			"chunked": req.Chunked, "length": req.ContentLength,
			"status":      rw.Status(),
			"duration_ms": time.Since(reqStart).Microseconds() / 1000,
			"keep_alive":  keepAlive,
		})
	}
}

// dispatch 执行业务并兜底 panic（panic 不杀进程；该连接回 500 后关闭）。
func (s *Server) dispatch(ctx context.Context, rw protocol.ResponseWriter,
	req *protocol.Request, connID string, reqNo uint64) {
	defer func() {
		if rec := recover(); rec != nil {
			s.Log.Error("handler", "panic recovered", logging.Fields{
				"conn": connID, "req": reqNo, "panic": fmt.Sprint(rec),
			})
			if !rw.WroteHeader() {
				rw.WriteHeader(500)
				_, _ = rw.Write([]byte(`{"error":"internal server error"}`))
			}
			rw.SetCloseAfter(true)
		}
	}()
	s.Handler(ctx, rw, req)
}

// handleProtocolError 处理 Parser.Next 返回的错误。
func (s *Server) handleProtocolError(bw *bufio.Writer, err error, connID string, reqNo uint64) {
	pe := protocol.AsProtoError(err)
	if pe == nil {
		s.Log.Error("read-request", "connection read failure", logging.Fields{
			"conn": connID, "req": reqNo, "error": err.Error()})
		return
	}
	if pe.Kind == protocol.KindIncomplete {
		s.logProtoWarn("read-request", err, connID, reqNo, "truncated request; closing")
		return
	}
	status := pe.HTTPStatus()
	s.logProtoWarn("read-request", err, connID, reqNo,
		fmt.Sprintf("protocol error -> %d; closing connection", status))
	rw := protocol.NewResponseWriter(bw, false, true, s.Limits.MaxBodyBytes)
	body := fmt.Sprintf(`{"error":%q,"kind":%q,"phase":%q,"offset":%d}`,
		pe.Msg, pe.Kind, pe.Phase, pe.Offset)
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(status)
	if _, werr := rw.Write([]byte(body)); werr != nil {
		s.Log.Error("response", "error body write failed", logging.Fields{
			"conn": connID, "req": reqNo, "error": werr.Error()})
	}
	if ferr := rw.Flush(); ferr != nil {
		s.Log.Error("response", "error response flush failed", logging.Fields{
			"conn": connID, "req": reqNo, "error": ferr.Error()})
	}
}

func (s *Server) logProtoWarn(stage string, err error, connID string, reqNo uint64, msg string) {
	pe := protocol.AsProtoError(err)
	f := logging.Fields{"conn": connID, "req": reqNo}
	if pe != nil {
		f["kind"] = string(pe.Kind)
		f["phase"] = pe.Phase
		f["offset"] = pe.Offset
		f["rel"] = pe.RelOff
		f["detail"] = pe.Msg
		s.Log.Warn(stage, msg, f)
		return
	}
	f["error"] = err.Error()
	s.Log.Warn(stage, msg, f)
}

// isGracefulEnd 判定“无字节的干净 EOF / 空闲超时”。
func isGracefulEnd(err error, connID string, reqNo uint64, log *logging.Logger) bool {
	if errors.Is(err, io.EOF) {
		log.Info("read-request", "peer closed connection", logging.Fields{
			"conn": connID, "req": reqNo})
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		log.Info("read-request", "idle timeout; closing keep-alive connection", logging.Fields{
			"conn": connID, "req": reqNo})
		return true
	}
	return false
}
