.PHONY: build test lint install clean

build:
	go build -o bin/fiken-cli ./cmd/fiken-cli

test:
	go test ./...

lint:
	golangci-lint run

install:
	go install ./cmd/fiken-cli

clean:
	rm -rf bin/

build-mcp:
	go build -o bin/fiken-mcp ./cmd/fiken-mcp

install-mcp:
	go install ./cmd/fiken-mcp

build-all: build build-mcp
