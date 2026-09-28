package httpx

// Limits bounds parser resource use. All values are in bytes; a zero
// value means "not enforced" only where noted.
type Limits struct {
	// MaxHeaderBytes bounds the full head section: request line +
	// header fields + terminating empty line.
	MaxHeaderBytes int

	// MaxBodyBytes bounds the decoded message body:
	//   - Content-Length value is rejected if larger
	//   - fixed bodies stop once more bytes would be read
	//   - chunked bodies stop once the accumulated payload exceeds
	MaxBodyBytes int64

	// MaxChunkLineBytes bounds a single chunk-size line including
	// extensions (the "1*HEXDIG [ chunk-ext ] CRLF" line).
	MaxChunkLineBytes int
}

// DefaultLimits returns conservative defaults suitable for a local
// backend.
func DefaultLimits() Limits {
	return Limits{
		MaxHeaderBytes:    64 * 1024,
		MaxBodyBytes:      1 << 20,
		MaxChunkLineBytes: 8 * 1024,
	}
}
