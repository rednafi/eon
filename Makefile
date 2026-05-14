.PHONY: build test vet lint tidy clean run

BIN := bin/eon
PKG := ./...

build:
	@mkdir -p bin
	go build -o $(BIN) ./cmd/eon

test:
	go test -race -count=1 $(PKG)

vet:
	go vet $(PKG)

lint:
	golangci-lint run --timeout=5m

tidy:
	go mod tidy

clean:
	rm -rf bin

run: build
	$(BIN)
