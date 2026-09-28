#!/usr/bin/env bash
# 用裸 TCP 发送示例请求（确保 CRLF）。默认地址可用 HOST/PORT 覆盖。
# 需要系统提供 nc (netcat)。
set -euo pipefail
HOST="${HOST:-127.0.0.1}"
PORT="${PORT:-8080}"

echo "== 固定长度体 =="
printf 'POST /records HTTP/1.1\r\nHost: %s:%s\r\nContent-Type: text/plain\r\nContent-Length: 11\r\n\r\nhello-world' \
  "$HOST" "$PORT" | nc "$HOST" "$PORT"
echo

echo "== chunked 体（分块扩展 + trailer）=="
printf 'POST /records HTTP/1.1\r\nHost: %s:%s\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n6;name=x\r\n world\r\n0\r\nETag: demo\r\n\r\n' \
  "$HOST" "$PORT" | nc "$HOST" "$PORT"
echo
