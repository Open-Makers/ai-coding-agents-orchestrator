BINARY      := orchestrator
CMD         := ./cmd/orchestrator
INSTALL_DIR := $(shell go env GOPATH)/bin

# Version: prefer .Version file, fall back to git describe, then "dev".
VERSION     := $(shell cat .Version 2>/dev/null || git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS     := -ldflags "-X main.version=$(VERSION)"

.PHONY: all build test test-race lint vet clean install uninstall help bump-patch bump-minor bump-major release

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
	rm -rf bin/ dist/

## bump-patch: increment patch version (0.2.0 → 0.2.1)
bump-patch:
	@V=$$(cat .Version); \
	MAJOR=$$(echo $$V | cut -d. -f1); \
	MINOR=$$(echo $$V | cut -d. -f2); \
	PATCH=$$(echo $$V | cut -d. -f3); \
	NEW="$$MAJOR.$$MINOR.$$((PATCH+1))"; \
	echo "$$NEW" > .Version; \
	echo "bumped: $$V → $$NEW"

## bump-minor: increment minor version (0.2.1 → 0.3.0)
bump-minor:
	@V=$$(cat .Version); \
	MAJOR=$$(echo $$V | cut -d. -f1); \
	MINOR=$$(echo $$V | cut -d. -f2); \
	NEW="$$MAJOR.$$((MINOR+1)).0"; \
	echo "$$NEW" > .Version; \
	echo "bumped: $$V → $$NEW"

## bump-major: increment major version (0.2.0 → 1.0.0)
bump-major:
	@V=$$(cat .Version); \
	MAJOR=$$(echo $$V | cut -d. -f1); \
	NEW="$$((MAJOR+1)).0.0"; \
	echo "$$NEW" > .Version; \
	echo "bumped: $$V → $$NEW"

## release: bump, tag, and push (usage: make release bump=patch|minor|major)
release:
	@if [ -z "$(bump)" ]; then echo "usage: make release bump=patch|minor|major"; exit 1; fi
	$(MAKE) bump-$(bump)
	@V=$$(cat .Version); \
	git add .Version; \
	git commit -m "release: v$$V"; \
	git tag "v$$V"; \
	echo "tagged v$$V — push with: git push origin main v$$V"

## help: list available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
