# h1parse Makefile：本地开发与验证的常用入口。
GO ?= go

.PHONY: all build test race fuzz vet fixtures check-fixtures run smoke clean

all: build

build:
	$(GO) build ./...

test:
	$(GO) test ./... -count=1

race:
	$(GO) test -race ./... -count=1

# 差分 fuzz：流式生产解析器 vs 独立整消息参考解析器。
fuzz:
	$(GO) test ./internal/protocol/ -run='^$$' -fuzz=FuzzDifferential -fuzztime=120s

vet:
	$(GO) vet ./...

fixtures:
	$(GO) run ./cmd/genfixtures

check-fixtures:
	$(GO) run ./cmd/genfixtures -check

run:
	$(GO) run ./cmd/h1parse

smoke:
	./scripts/verify.sh

clean:
	rm -f h1parse h1parse.db h1parse.db-wal h1parse.db-shm
