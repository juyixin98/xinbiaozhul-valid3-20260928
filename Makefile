# http11subset — local development targets.
# All targets are local-only; no cloud or account access required.

GO ?= go
PORT ?= 18080
SQLITE ?= ./data/app.db
CONFIG ?= configs/local.json

.PHONY: all build test race vet fixtures vendor run smoke verify clean fmt tidy

all: build

build:
	$(GO) build ./...

fmt:
	gofmt -w ./cmd ./internal ./test

vet:
	$(GO) vet ./...

test:
	$(GO) test -count=1 ./...

race:
	$(GO) test -race -count=1 ./...

fixtures:
	$(GO) run ./test/genfixtures -out test/testdata/fixtures.json

tidy:
	$(GO) mod tidy

vendor:
	$(GO) mod vendor

# Fully offline build using only the committed vendor tree.
offline-build:
	GOFLAGS=-mod=vendor GOPROXY=off $(GO) build ./...

run:
	@mkdir -p $$(dirname $(SQLITE))
	HTTP11_LISTEN=127.0.0.1:$(PORT) HTTP11_SQLITE_PATH=$(SQLITE) \
	  $(GO) run ./cmd/server -config $(CONFIG)

smoke:
	PORT=$(PORT) ./scripts/verify.sh --smoke

verify:
	PORT=$(PORT) ./scripts/verify.sh

clean:
	rm -rf data test/out
