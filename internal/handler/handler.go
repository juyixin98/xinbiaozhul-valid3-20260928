// Package handler 实现业务处理。它只依赖 protocol 的请求/响应抽象与
// storage 接口，不直接操作 TCP 连接，因此可以对固定输入做确定性测试。
//
// 路由（不是代理，不做任何转发）：
//
//	GET  /healthz                 存活探针
//	GET  /                         服务信息
//	POST /records                  存入消息体（内容寻址，幂等）
//	GET  /records/{id}             取回记录
//	*    其余                      404/405
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"h1parse/internal/logging"
	"h1parse/internal/protocol"
	"h1parse/internal/storage"
)

// Handler 处理单条请求。
type Handler struct {
	Store  storage.Store
	Log    *logging.Logger
	MaxBuf int64 // 读取请求体的上限（应 <= Limits.MaxBodyBytes）
}

// Serve 实现 server.HandlerFunc：签名固定为
// (context.Context, protocol.ResponseWriter, *protocol.Request)。
func (h *Handler) Serve(ctx context.Context, rw protocol.ResponseWriter, req *protocol.Request) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.Header().Set("X-Content-Type-Options", "nosniff")

	path := req.Target
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	switch {
	case path == "/healthz" && (req.Method == "GET" || req.Method == "HEAD"):
		h.health(rw, req)
	case path == "/" && (req.Method == "GET" || req.Method == "HEAD"):
		h.root(rw, req)
	case path == "/records" && req.Method == "POST":
		h.putRecord(ctx, rw, req)
	case strings.HasPrefix(path, "/records/") && (req.Method == "GET" || req.Method == "HEAD"):
		h.getRecord(ctx, rw, req, strings.TrimPrefix(path, "/records/"))
	case path == "/records" || strings.HasPrefix(path, "/records/"):
		rw.Header().Set("Allow", "GET, HEAD, POST")
		h.textError(rw, req, 405, "method %s not allowed for %s", req.Method, path)
	default:
		h.textError(rw, req, 404, "no route for %s %s", req.Method, path)
	}
}

func (h *Handler) health(rw protocol.ResponseWriter, req *protocol.Request) {
	writeJSON(rw, req, 200, map[string]string{"status": "ok"})
}

func (h *Handler) root(rw protocol.ResponseWriter, req *protocol.Request) {
	writeJSON(rw, req, 200, map[string]any{
		"service": "h1parse",
		"scope":   "HTTP/1.1 server subset: persistent connections, fixed-length and chunked bodies",
		"routes": []string{
			"GET  /healthz",
			"POST /records",
			"GET  /records/{id}",
		},
	})
}

// putRecord 读取完整消息体（依赖协议帧的精确边界），存入 SQLite。
func (h *Handler) putRecord(ctx context.Context, rw protocol.ResponseWriter, req *protocol.Request) {
	// 无 Content-Length 且非 chunked：HTTP/1.1 下无法得知表示边界，
	// 业务不能自行假设为空体，回 411。
	if !req.HasLength && !req.Chunked {
		h.textError(rw, req, 411, "POST /records requires Content-Length or chunked encoding")
		return
	}
	body, err := readLimited(req.Body, h.MaxBuf)
	if err != nil {
		h.bodyReadError(rw, req, err)
		return
	}
	ct, _ := req.Header("Content-Type")
	rec, err := h.Store.Put(ctx, ct, body)
	if err != nil {
		h.Log.Error("handler", "store put failed", logging.Fields{
			"method": req.Method, "target": req.Target, "error": err.Error(),
		})
		writeJSON(rw, req, 500, map[string]string{"error": "storage failure"})
		return
	}
	h.Log.Info("handler", "record stored", logging.Fields{
		"id": rec.ID, "size": rec.Size, "chunked": req.Chunked,
	})
	rw.Header().Set("Location", "/records/"+rec.ID)
	writeJSON(rw, req, 201, rec)
}

func (h *Handler) getRecord(ctx context.Context, rw protocol.ResponseWriter, req *protocol.Request, id string) {
	if id == "" || strings.ContainsAny(id, "/?#") || len(id) != 16 || !isLowerHex(id) {
		h.textError(rw, req, 404, "record id not found: %q", id)
		return
	}
	rec, err := h.Store.Get(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		h.textError(rw, req, 404, "record not found: %s", id)
		return
	}
	if err != nil {
		h.Log.Error("handler", "store get failed", logging.Fields{"id": id, "error": err.Error()})
		writeJSON(rw, req, 500, map[string]string{"error": "storage failure"})
		return
	}
	writeJSON(rw, req, 200, rec)
}

// bodyReadError 把读消息体阶段的协议错误映射为确定响应。
// 所有这些错误都要求关闭连接（由状态机依据 ProtoError 执行）。
func (h *Handler) bodyReadError(rw protocol.ResponseWriter, req *protocol.Request, err error) {
	pe := protocol.AsProtoError(err)
	if pe == nil {
		h.Log.Error("handler", "unexpected body read error", logging.Fields{"error": err.Error()})
		writeJSON(rw, req, 500, map[string]string{"error": "internal error"})
		return
	}
	status := pe.HTTPStatus()
	if status == 0 {
		status = 400
	}
	h.Log.Warn("body", "rejected body", logging.Fields{
		"kind":   string(pe.Kind),
		"phase":  pe.Phase,
		"offset": pe.Offset,
		"rel":    pe.RelOff,
		"error":  pe.Msg,
	})
	// 体阶段的任何定帧/截断/超限错误都使后续字节归属不可信，强制关闭。
	rw.SetCloseAfter(true)
	writeJSON(rw, req, status, map[string]string{"error": pe.Msg, "kind": string(pe.Kind)})
}

// textError 写错误 JSON。Allow 头由调用方在需要时直接设置（405）。
func (h *Handler) textError(rw protocol.ResponseWriter, req *protocol.Request, status int,
	format string, args ...any) {
	writeJSON(rw, req, status, map[string]string{"error": fmt.Sprintf(format, args...)})
}

// readLimited 完整读取消息体，超过 max 返回 KindPayloadTooLarge（理论上
// chunked/固定帧已在协议层拦截，这里是业务层纵深防御）。
func readLimited(r io.Reader, max int64) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 16*1024)
	var total int64
	for {
		n, err := r.Read(tmp)
		total += int64(n)
		if max > 0 && total > max {
			return nil, &protocol.ProtoError{
				Kind: protocol.KindPayloadTooLarge, Phase: protocol.PhaseBody,
				Offset: -1, RelOff: total, Msg: fmt.Sprintf("body exceeds handler limit %d", max),
			}
		}
		buf = append(buf, tmp[:n]...)
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func writeJSON(rw protocol.ResponseWriter, req *protocol.Request, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(500)
		_, _ = rw.Write([]byte(`{"error":"internal marshal failure"}`))
		return
	}
	rw.WriteHeader(status)
	if req.Method != "HEAD" {
		_, _ = rw.Write(b)
	}
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
