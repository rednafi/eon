SHELL := /bin/bash

.PHONY: build test test-container smoke-container lint vet tidy clean install help

GO        ?= go
PKG       := ./...
BIN_DIR   := bin
BIN       := $(BIN_DIR)/eon
LDFLAGS   := -s -w -X github.com/rednafi/eon/cli.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build: $(BIN_DIR) ## build the eon binary
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) .

test: ## run the test suite with race detector
	$(GO) test -race -count=1 $(PKG)

test-container: ## run the full suite inside a Linux container with a real cron daemon
	docker build -f Dockerfile.test -t eon-test .
	docker run --rm eon-test

smoke-container: ## end-to-end CLI smoke (build + create cron + list/show/delete) in Linux
	./scripts/smoke-container.sh

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
