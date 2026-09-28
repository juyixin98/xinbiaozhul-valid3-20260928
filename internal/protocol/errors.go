// Package protocol 实现 HTTP/1.1 请求的线协议编解码（不包含网络 accept
// 与业务逻辑）。它包含三个互相独立的部分：
//
//   - 面向连接字节流的带偏移读取器 streamReader；
//   - 请求行 / 头部域解析与定帧决策（ReadRequest）；
//   - 固定长度与 chunked 两种消息体帧（fixedBody / chunkedBody）；
//   - 服务端响应编码（responseWriter）。
//
// 本包所有错误都用 *ProtoError 表达，调用方通过 Kind 区分
// “非法输入 / 状态冲突 / 资源超限 / 运行失败”，Offset 给出连接起始计的
// 绝对字节偏移，Phase 给出出错阶段。解析器绝不“猜测”一个不确定结果：
// 无法定帧时返回错误，由连接状态机决定关闭连接。
package protocol

import "fmt"

// Kind 是错误类别。不同类别对应不同处置（HTTP 状态码、是否关闭连接、
// 日志级别），禁止把未收敛结果包装成成功。
type Kind string

const (
	// KindInvalid：线协议非法（语法错误、定帧冲突、状态机冲突），映射 400。
	KindInvalid Kind = "invalid_request"
	// KindHeaderTooLarge：头部段超过限制，映射 431。
	KindHeaderTooLarge Kind = "header_too_large"
	// KindPayloadTooLarge：消息体或单个 chunk 超过限制，映射 413。
	KindPayloadTooLarge Kind = "payload_too_large"
	// KindLengthRequired：需要消息体定帧信息但未提供，映射 411。
	KindLengthRequired Kind = "length_required"
	// KindUnsupportedCoding：出现本服务不支持的 transfer-coding，映射 501。
	KindUnsupportedCoding Kind = "unsupported_transfer_coding"
	// KindUpgradeUnsupported：Upgrade / 隧道语义不支持，映射 501。
	KindUpgradeUnsupported Kind = "upgrade_unsupported"
	// KindVersionUnsupported：非 HTTP/1.1，映射 505。
	KindVersionUnsupported Kind = "version_unsupported"
	// KindExpectationFailed：无法满足的 Expect 期望，映射 417。
	KindExpectationFailed Kind = "expectation_failed"
	// KindIncomplete：字节流在消息结束前结束（截断 / 对端关闭）。
	// 这是连接生命周期事件而非可回送的客户端错误：状态映射 0，
	// 服务端直接关闭连接，不再尝试解析后续字节。
	KindIncomplete Kind = "incomplete"
)

// 解析阶段标记，随错误一起记录，保证日志能定位到状态机阶段。
const (
	PhaseRequestLine = "request-line"
	PhaseHeader      = "header"
	PhaseFraming     = "framing"
	PhaseBody        = "body"
	PhaseChunkSize   = "chunk-size"
	PhaseChunkExt    = "chunk-ext"
	PhaseChunkData   = "chunk-data"
	PhaseTrailer     = "trailer"
)

// ProtoError 是协议层唯一错误类型。
type ProtoError struct {
	Kind   Kind
	Phase  string // 请求行 / 头部 / 定帧 / chunk-* / trailer
	Offset int64  // 自连接首字节计的绝对偏移；-1 表示不适用
	RelOff int64  // 阶段内相对偏移（如 chunk 内偏移）；-1 表示不适用
	Msg    string
}

func (e *ProtoError) Error() string {
	switch {
	case e.Offset >= 0 && e.RelOff >= 0:
		return fmt.Sprintf("http1: %s at %s offset=%d (rel=%d): %s",
			e.Kind, e.Phase, e.Offset, e.RelOff, e.Msg)
	case e.Offset >= 0:
		return fmt.Sprintf("http1: %s at %s offset=%d: %s",
			e.Kind, e.Phase, e.Offset, e.Msg)
	default:
		return fmt.Sprintf("http1: %s at %s: %s", e.Kind, e.Phase, e.Msg)
	}
}

// HTTPStatus 把错误类别映射为响应状态码。KindIncomplete 返回 0：
// 截断发生时不回送响应。
func (e *ProtoError) HTTPStatus() int {
	switch e.Kind {
	case KindInvalid:
		return 400
	case KindHeaderTooLarge:
		return 431
	case KindPayloadTooLarge:
		return 413
	case KindLengthRequired:
		return 411
	case KindUnsupportedCoding, KindUpgradeUnsupported:
		return 501
	case KindVersionUnsupported:
		return 505
	case KindExpectationFailed:
		return 417
	default:
		return 0
	}
}

// CloseAfterError 报告该错误是否要求关闭连接。HTTP/1.1 下所有 400/413/431
// 等定帧类错误都必须关闭：残余字节的归属无法再被信任。
func (e *ProtoError) CloseAfterError() bool { return true }

func protoError(kind Kind, phase string, offset, rel int64, format string, args ...any) *ProtoError {
	return &ProtoError{
		Kind:   kind,
		Phase:  phase,
		Offset: offset,
		RelOff: rel,
		Msg:    fmt.Sprintf(format, args...),
	}
}

// AsProtoError 从任意错误中取出 *ProtoError；不存在返回 nil。
func AsProtoError(err error) *ProtoError {
	if err == nil {
		return nil
	}
	if pe, ok := err.(*ProtoError); ok {
		return pe
	}
	type wrapper interface{ Unwrap() error }
	for {
		w, ok := err.(wrapper)
		if !ok {
			return nil
		}
		err = w.Unwrap()
		if pe, ok := err.(*ProtoError); ok {
			return pe
		}
	}
}
