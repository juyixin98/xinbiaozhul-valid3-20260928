#!/usr/bin/env bash
# scripts/verify.sh — reproduce the whole validation locally from a
# clean checkout: build, unit/protocol tests, fixture regeneration
# check, and a live TCP smoke run against the real server.
#
# Usage:
#   ./scripts/verify.sh            # all checks
#   ./scripts/verify.sh --unit     # go tests only
#   ./scripts/verify.sh --smoke    # live server smoke only
#
# Requirements: Go 1.22+, python3 (for raw TCP checks). No network
# access beyond `go mod download` on first run; no accounts or paid
# services.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PORT="${PORT:-18097}"
BASE="127.0.0.1:${PORT}"
WORK="$(mktemp -d)"
trap '[[ -n "${SRV_PID:-}" ]] && kill "$SRV_PID" 2>/dev/null || true; rm -rf "$WORK"' EXIT

log() { printf '\033[1;36m[verify]\033[0m %s\n' "$*"; }

run_unit() {
  log "go vet"
  go vet ./...
  log "regenerating fixtures and checking they are committed/up to date"
  go run ./test/genfixtures -out "$WORK/fixtures.json"
  if ! diff -u test/testdata/fixtures.json "$WORK/fixtures.json" >/dev/null; then
    echo "fixtures.json is out of date; run: go run ./test/genfixtures -out test/testdata/fixtures.json" >&2
    diff -u test/testdata/fixtures.json "$WORK/fixtures.json" | head -40 >&2 || true
    exit 1
  fi
  log "go test (race) ./..."
  go test -race -count=1 ./...
}

start_server() {
  log "building server"
  go build -o "$WORK/server" ./cmd/server
  HTTP11_LISTEN="$BASE" HTTP11_SQLITE_PATH="$WORK/app.db" \
    "$WORK/server" -config /nonexistent.json >"$WORK/server.log" 2>&1 &
  SRV_PID=$!
  for _ in $(seq 1 50); do
    if python3 - "$BASE" <<'PY' 2>/dev/null; then return 0; fi
import socket,sys
s=socket.create_connection(tuple(sys.argv[1].split(":")),timeout=0.2); s.close()
PY
      sleep 0.1
  done
  echo "server did not come up; log:" >&2; cat "$WORK/server.log" >&2; exit 1
}

run_smoke() {
  start_server
  log "live TCP smoke against $BASE"
  python3 - "$BASE" "$WORK" <<'PY'
import json,socket,sys,tempfile,os
base=sys.argv[1]; host,port=base.split(":"); port=int(port)
fails=[]
def conn():
    s=socket.create_connection((host,port),timeout=3); s.settimeout(2); return s
def roundtrip(payload, expect_statuses=1):
    s=conn(); s.sendall(payload); data=b""
    while data.count(b"HTTP/1.1")<expect_statuses:
        c=s.recv(4096)
        if not c: break
        data+=c
    s.close(); return data
def status(d): return d.split(b"\r\n",1)[0].decode()
def body(d):
    return d.split(b"\r\n\r\n",1)[1] if b"\r\n\r\n" in d else b""

# health
d=roundtrip(b"GET /healthz HTTP/1.1\r\nHost: x\r\n\r\n")
assert status(d)=="HTTP/1.1 200 OK", status(d)
assert json.loads(body(d))["status"]=="ok"

# fixed create
b=b'{"title":"fixed","payload":"hello"}'
req=(b"POST /v1/records HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\n"
     b"Content-Length: "+str(len(b)).encode()+b"\r\n\r\n")+b
d=roundtrip(req)
assert status(d)=="HTTP/1.1 201 Created", status(d)
rid=json.loads(body(d))["id"]

# fetch it
d=roundtrip(("GET /v1/records/%d HTTP/1.1\r\nHost: x\r\n\r\n"%rid).encode())
assert json.loads(body(d))["payload"]=="hello"

# chunked create with extension + trailer (sizes computed, not hand-counted)
p1=b'{"title":"chu'; p2=b'nk","payload":""}'
req=(b"POST /v1/records HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\n"
     b"Transfer-Encoding: chunked\r\n\r\n"
     +("%x;name=part1\r\n"%len(p1)).encode()+p1+b"\r\n"
     +("%x\r\n"%len(p2)).encode()+p2+b"\r\n0\r\nX-Trailer: yes\r\n\r\n")
d=roundtrip(req)
assert status(d)=="HTTP/1.1 201 Created", status(d)+" "+d[:200].decode(errors="replace")

# pipelined two responses
d=roundtrip(b"GET /healthz HTTP/1.1\r\nHost: x\r\n\r\nGET /v1/records/1 HTTP/1.1\r\nHost: x\r\n\r\n",2)
assert d.count(b"HTTP/1.1")==2, d[:80]

# TE/CL conflict -> 400 and connection closed, smuggled suffix ignored
smug=(b"POST /x HTTP/1.1\r\nHost: x\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n"
      b"0\r\n\r\nGET /evil HTTP/1.1\r\nHost: x\r\n\r\n")
s=conn(); s.sendall(smug); first=b""
try:
    while True:
        c=s.recv(4096)
        if not c: break
        first+=c
except socket.timeout: pass
s.close()
assert first.split(b"\r\n",1)[0]==b"HTTP/1.1 400 Bad Request", first[:40]
assert b"/evil" not in first, "smuggled request reflected"
print("SMOKE OK: health, fixed, chunked+ext+trailer, pipeline(2), TE/CL 400+close")
PY
  log "server parse log carried byte offsets"
  grep -q '"offset"' "$WORK/server.log"
  log "SMOKE PASSED"
}

case "${1:-all}" in
  --unit)  run_unit ;;
  --smoke) run_smoke ;;
  all)     run_unit; run_smoke ;;
  *) echo "usage: $0 [--unit|--smoke]"; exit 2 ;;
esac

log "ALL CHECKS PASSED"
