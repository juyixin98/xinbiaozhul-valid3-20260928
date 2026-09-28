#!/usr/bin/env bash
# verify.sh — reproduce the whole project locally from a clean checkout.
#
# It requires only the Go toolchain (>= 1.22) and a writable temp dir; the
# SQLite driver is the pure-Go modernc.org/sqlite, so no C compiler is needed.
# Everything is local: no accounts, no paid services, no external data.
#
# Usage: ./scripts/verify.sh
set -euo pipefail

cd "$(dirname "$0")/.."

echo "==> [1/5] toolchain"
go version

echo "==> [2/5] download and lock dependencies (offline-capable after first run)"
go mod download
go mod verify

echo "==> [3/5] build all packages and commands"
go build ./...
go vet ./...

echo "==> [4/5] full test suite (race detector), packages listed individually"
# -count=1 disables the test cache so a clean run always executes.
go test -race -count=1 -v ./internal/packet/... ./internal/topics/... \
  | tee /tmp/mqttd-unit-phase1.log | tail -5
go test -race -count=1 ./internal/store/...
go test -race -count=1 ./internal/broker/...

echo "==> [5/5] black-box smoke test against a freshly started server"
./scripts/smoke.sh

echo
echo "ALL CHECKS PASSED"
