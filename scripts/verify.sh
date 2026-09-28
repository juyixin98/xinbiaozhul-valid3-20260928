#!/usr/bin/env bash
# Local verification from a clean checkout:
#   1. build every binary
#   2. run unit + integration tests (race detector on by default)
#   3. boot the broker on an ephemeral port and run pub/sub smoke flow
set -euo pipefail

cd "$(dirname "$0")/.."

RACE="${RACE:-1}"
PORT="${PORT:-11883}"
DB="${DB:-$(mktemp -d)/mqttlocal-verify.db}"

echo "== [1/4] go vet =="
go vet ./...

echo "== [2/4] go test =="
if [[ "$RACE" == "1" ]]; then
  go test ./... -race -count=1 -timeout 180s
else
  go test ./... -count=1 -timeout 180s
fi

echo "== [3/4] build binaries =="
mkdir -p bin
go build -o bin/mqttlocal     ./cmd/mqttlocal
go build -o bin/mqttlocal-pub ./cmd/mqttlocal-pub
go build -o bin/mqttlocal-sub ./cmd/mqttlocal-sub

echo "== [4/4] live smoke test on 127.0.0.1:${PORT} =="
./bin/mqttlocal -listen "127.0.0.1:${PORT}" -db "$DB" -run-id "verify-$$" &
BROKER_PID=$!
trap 'kill "$BROKER_PID" 2>/dev/null || true' EXIT

# Wait for the listener.
for i in $(seq 1 50); do
  if (exec 3<>"/dev/tcp/127.0.0.1/${PORT}") 2>/dev/null; then break; fi
  sleep 0.1
done

# Start subscriber first (durable session), then publish QoS 1.
timeout 8 ./bin/mqttlocal-sub -addr "127.0.0.1:${PORT}" -id vs -filter "demo/#" -qos 1 -for 4s -clean=false > /tmp/mqttlocal-sub.out 2>&1 &
SUB_PID=$!
sleep 1
./bin/mqttlocal-pub -addr "127.0.0.1:${PORT}" -id vp -topic demo/hello -msg "smoke-ok" -qos 1
wait "$SUB_PID" || true

echo "---- subscriber output ----"
cat /tmp/mqttlocal-sub.out
grep -q 'payload="smoke-ok"' /tmp/mqttlocal-sub.out
echo "VERIFY OK"
