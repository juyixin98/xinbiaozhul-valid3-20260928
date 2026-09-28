package protocol

import "io"

// 本文件实现 Content-Length 定帧的消息体。帧对象持有同一个 streamReader，
// 因此：
//   - 半包：streamReader 内部缓冲跨 Read 拼包，对帧透明；
//   - 粘包：帧只按精确边界消费（恰好读满 length 字节），多出的字节留在
//     缓冲里供下一请求使用；
//   - 截断：底层提前 EOF 映射 KindIncomplete；
//   - drain：Close 读尽残余消息体，下一请求从精确边界开始。

// fixedBody 是 Content-Length 定帧的消息体。
type fixedBody struct {
	sr     *streamReader
	length int64 // 声明长度（恒定）
	read   int64 // 已消费字节数
	off    int64 // 消息体首字节绝对偏移
	done   bool
}

func newFixedBody(sr *streamReader, length, off int64) *fixedBody {
	return &fixedBody{sr: sr, length: length, off: off}
}

func (b *fixedBody) FrameOffset() int64 { return b.off }
func (b *fixedBody) Consumed() bool     { return b.done || b.read >= b.length }

func (b *fixedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.Consumed() {
		b.done = true
		return 0, io.EOF
	}
	want := int64(len(p))
	if want > b.length-b.read {
		want = b.length - b.read
	}
	n := 0
	for int64(n) < want {
		if b.sr.buffered() > 0 {
			got := copy(p[n:want], b.sr.buf)
			b.sr.discard(got)
			n += got
			b.read += int64(got)
			continue
		}
		if err := b.sr.fill(); err != nil {
			if err == io.EOF {
				return n, protoError(KindIncomplete, PhaseBody,
					b.off+b.read, b.read,
					"connection closed: %d of %d body bytes received",
					b.read, b.length)
			}
			return n, err
		}
	}
	if b.read >= b.length {
		b.done = true
		if int64(len(p)) > want {
			// 已到边界：本次返回数据，EOF 在下一次 Read 给出。
		}
	}
	return n, nil
}

// Close 由连接状态机在请求处理结束时调用：未读尽的消息体被 drain，
// 保证下一请求从精确边界开始。drain 失败（截断）时连接必须关闭。
func (b *fixedBody) Close() error {
	if b.Consumed() {
		return nil
	}
	tmp := make([]byte, 32*1024)
	for !b.Consumed() {
		n, err := b.Read(tmp)
		_ = n
		if err != nil && err != io.EOF {
			return err
		}
	}
	return nil
}

// emptyBody 用于无 Content-Length 且非 chunked 的请求（GET/HEAD 等）。
type emptyBody struct{ off int64 }

func (emptyBody) Read([]byte) (int, error) { return 0, io.EOF }
func (emptyBody) Close() error             { return nil }
func (emptyBody) Consumed() bool           { return true }
func (e emptyBody) FrameOffset() int64     { return e.off }
