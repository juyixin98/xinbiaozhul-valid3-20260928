.PHONY: all build test test-race vet fmt clean run smoke

GO ?= go

all: build

build:
	$(GO) build ./...

bin:
	mkdir -p bin
	$(GO) build -o bin/mqttlocal     ./cmd/mqttlocal
	$(GO) build -o bin/mqttlocal-pub ./cmd/mqttlocal-pub
	$(GO) build -o bin/mqttlocal-sub ./cmd/mqttlocal-sub

vet:
	$(GO) vet ./...

fmt:
	gofmt -w cmd internal test

test:
	$(GO) test ./... -count=1 -timeout 180s

test-race:
	$(GO) test ./... -race -count=1 -timeout 180s

test-integration:
	$(GO) test ./test/... -race -count=1 -v -timeout 120s

run: bin
	./bin/mqttlocal -listen 127.0.0.1:1883 -db data/mqttlocal.db

smoke: bin
	./scripts/verify.sh

clean:
	rm -rf bin data *.db *.db-wal *.db-shm
