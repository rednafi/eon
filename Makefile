SHELL := /bin/bash

.PHONY: build test lint vet tidy clean install help

GO        ?= go
PKG       := ./...
BIN_DIR   := bin
BIN       := $(BIN_DIR)/eon
LDFLAGS   := -s -w -X github.com/rednafi/eon/internal/cli.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build: $(BIN_DIR) ## build the eon binary
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/eon

test: ## run the test suite with race detector
	$(GO) test -race -count=1 $(PKG)

vet: ## go vet all packages
	$(GO) vet $(PKG)

lint: ## run golangci-lint
	@command -v golangci-lint >/dev/null || (echo "install golangci-lint first" >&2; exit 1)
	golangci-lint run --timeout=5m

tidy: ## go mod tidy
	$(GO) mod tidy

install: build ## install the binary into GOBIN
	install -m 0755 $(BIN) $${GOBIN:-$$HOME/go/bin}/eon

clean:
	rm -rf $(BIN_DIR)

$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

help:
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*##' '{printf "%-12s %s\n", $$1, $$2}'
