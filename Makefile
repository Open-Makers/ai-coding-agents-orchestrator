BINARY   := orchestrator
CMD      := ./cmd/orchestrator
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-X main.version=$(VERSION)"

.PHONY: all build test test-race lint vet clean install

all: build

## build: compile the binary to ./bin/orchestrator
build:
	mkdir -p bin
	go build $(LDFLAGS) -o bin/$(BINARY) $(CMD)

## install: install to $GOPATH/bin (or ~/go/bin)
install:
	go install $(LDFLAGS) $(CMD)

## test: run all tests
test:
	go test ./...

## test-race: run all tests with the race detector
test-race:
	go test -race ./...

## vet: run go vet
vet:
	go vet ./...

## lint: run golangci-lint (requires golangci-lint in PATH)
lint:
	golangci-lint run ./...

## clean: remove build artefacts
clean:
	rm -rf bin/

## help: list available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
