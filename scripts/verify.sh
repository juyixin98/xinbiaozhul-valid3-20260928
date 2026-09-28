#!/usr/bin/env bash
# 从干净环境一键本地验证。无外部账号、无付费服务、无需 cgo
# （SQLite 使用纯 Go 驱动 modernc.org/sqlite）。
set -euo pipefail

cd "$(dirname "$0")/.."

echo "== [1/7] go version =="
go version

echo "== [2/7] 锁定依赖（无网络时要求模块缓存已就绪）=="
if ! go mod download all 2>/tmp/h1parse-mod.log; then
  echo "WARN: go mod download 失败（可能离线），尝试使用本地模块缓存继续" >&2
  cat /tmp/h1parse-mod.log >&2 || true
fi

echo "== [3/7] 编译全部包 =="
go build ./...
go vet ./...

echo "== [4/7] 校验夹具与生成器一致 =="
go run ./cmd/genfixtures -check

echo "== [5/7] 单元/差分/参考测试（含 -race）=="
go test -race ./... -count=1

echo "== [6/7] 启动内存模式服务做冒烟（HTTP 真连接）=="
ADDR="127.0.0.1:18080"
H1PARSE_ADDR="$ADDR" H1PARSE_DB=":memory:" \
  go run ./cmd/h1parse >/tmp/h1parse-smoke.log 2>&1 &
SRV_PID=$!
trap 'kill "$SRV_PID" 2>/dev/null || true' EXIT

# 等待监听
for _ in $(seq 1 50); do
  if curl -sf "http://$ADDR/healthz" >/dev/null 2>&1; then break; fi
  sleep 0.1
done

echo "-- GET /healthz"
curl -sS -i "http://$ADDR/healthz" | sed -n '1,8p'

echo "-- POST 固定长度体"
curl -sS -i -X POST "http://$ADDR/records" \
  -H 'Content-Type: text/plain' --data 'hello-smoke' | sed -n '1,10p'

echo "-- POST chunked 体（curl 自动 chunked）"
curl -sS -i -X POST "http://$ADDR/records" \
  -H 'Content-Type: text/plain' -H 'Transfer-Encoding: chunked' \
  --data-binary 'chunked-smoke-body' | sed -n '1,10p'

echo "-- 原始 socket：流水线 + chunked 扩展 + TE/CL 走私拒绝"
go run ./cmd/smokeclient -addr "$ADDR"

kill "$SRV_PID" 2>/dev/null || true
trap - EXIT

echo "== [7/7] 完成：全部检查通过 =="
