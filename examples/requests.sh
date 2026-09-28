#!/usr/bin/env bash
# examples/requests.sh — print canonical, byte-exact HTTP/1.1 request
# payloads (CRLF terminators) to stdout. Pipe one into the server with
# a raw TCP client; see scripts/verify.sh for the full flow.
#
# Each function writes one self-contained message. Chunked bodies use
# explicit size lines so the wire bytes are auditable.
set -euo pipefail

crlf=$'\r\n'

# 1. GET, no body, persistent connection.
req_get_health() {
  printf 'GET /healthz HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n'
}

# 2. POST fixed-length JSON (Content-Length counted explicitly).
req_post_fixed() {
  local body='{"title":"fixed","payload":"hello"}'
  local len=${#body}
  printf 'POST /v1/records HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: %s\r\nConnection: keep-alive\r\n\r\n%s' "$len" "$body"
}

# 3. POST chunked with a chunk extension and a trailer field. Chunk
# sizes are emitted in hex from the actual byte counts so they can
# never drift from the data.
req_post_chunked() {
  local part1='{"title":"chu'       # first fragment
  local part2='nk","payload":""}'   # second fragment
  printf 'POST /v1/records HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n'
  printf '%x;name=part1\r\n%s\r\n' "${#part1}" "$part1"
  printf '%x\r\n%s\r\n0\r\nX-Trailer: yes\r\n\r\n' "${#part2}" "$part2"
}

# 4. Two pipelined requests written at once (粘包/back-to-back).
req_pipeline() {
  printf 'GET /healthz HTTP/1.1\r\nHost: localhost\r\n\r\n'
  printf 'GET /v1/records/1 HTTP/1.1\r\nHost: localhost\r\n\r\n'
}

# 5. A deliberately REJECTED request: TE and CL both present.
req_reject_te_cl() {
  printf 'POST /x HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\nGET /evil HTTP/1.1\r\nHost: localhost\r\n\r\n'
}

case "${1:-}" in
  health)   req_get_health ;;
  fixed)    req_post_fixed ;;
  chunked)  req_post_chunked ;;
  pipeline) req_pipeline ;;
  reject)   req_reject_te_cl ;;
  *)
    echo "usage: $0 {health|fixed|chunked|pipeline|reject}" >&2
    exit 2
    ;;
esac
