BINARY      := orchestrator
CMD         := ./cmd/orchestrator
INSTALL_DIR := $(shell go env GOPATH)/bin

# Version: prefer .Version file, fall back to git describe, then "dev".
VERSION     := $(shell cat .Version 2>/dev/null || git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS     := -ldflags "-X main.version=$(VERSION)"

.PHONY: all build test test-race lint vet clean install uninstall help

all: vet lint test-race build install

## build: compile the binary to ./bin/orchestrator
build:
	mkdir -p bin
	go build $(LDFLAGS) -o bin/$(BINARY) $(CMD)

## install: remove previous binary, then install to $GOPATH/bin
install: uninstall
	go install $(LDFLAGS) $(CMD)

## uninstall: remove the installed binary from $GOPATH/bin
uninstall:
	rm -f $(INSTALL_DIR)/$(BINARY)

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
