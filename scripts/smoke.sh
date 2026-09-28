#!/usr/bin/env bash
# smoke.sh — black-box end-to-end check using the built server and example
# client only (no Go test machinery). Publishes a retained QoS1 message, then
# starts a NEW durable subscriber and asserts the retained image arrives.
set -euo pipefail

cd "$(dirname "$0")/.."

PORT="${PORT:-1883}"
WORK="$(mktemp -d)"
DB="$WORK/smoke.db"
SRV_PID=""
cleanup() {
  [ -n "${SRV_PID}" ] && kill "${SRV_PID}" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "==> build"
go build -o "$WORK/mqttd" ./cmd/mqttd
go build -o "$WORK/mqttctl" ./cmd/mqttctl

echo "==> start broker on 127.0.0.1:${PORT} db=${DB}"
"$WORK/mqttd" -addr "127.0.0.1:${PORT}" -db "$DB" -retx 500ms >"$WORK/server.log" 2>&1 &
SRV_PID=$!

# Wait for the listen socket.
for _ in $(seq 1 50); do
  if (exec 3<>"/dev/tcp/127.0.0.1/${PORT}") 2>/dev/null; then
    exec 3>&- 3<&-
    break
  fi
  sleep 0.1
done

echo "==> publish retained QoS1"
"$WORK/mqttctl" -addr "127.0.0.1:${PORT}" -id smoke-pub \
  -pub smoke/hello -msg "hello-smoke" -retain -qos 1

echo "==> new durable subscriber must receive the retained image"
# timeout ends the long-lived subscriber after it has printed the lines the
# assertions need; its non-zero exit from the timeout is expected and ignored.
set +e
OUT=$(timeout --preserve-status 3s "$WORK/mqttctl" \
  -addr "127.0.0.1:${PORT}" -id smoke-sub \
  -sub 'smoke/#' -qos 1 -clean=false 2>&1)
set -e
echo "$OUT"
echo "$OUT" | grep -q 'connected: session_present=false return_code=0'
echo "$OUT" | grep -q 'subacked: pids=1 return_codes=01'
echo "$OUT" | grep -q 'RECV topic=smoke/hello qos=1 dup=false retain=true pid=1 payload="hello-smoke"'

echo "==> server log shows the run/sequence trail"
grep -q 'stage=startup result=accept' "$WORK/server.log"
grep -q 'stage=retain result=accept' "$WORK/server.log"
grep -q 'stage=publish result=accept' "$WORK/server.log"

echo "SMOKE OK"
