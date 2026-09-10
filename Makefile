.PHONY: build test lint install clean check

build:
	go build -o bin/fiken-cli ./cmd/fiken-cli

test:
	go test ./...

# check — the gate for this repo. go vet, the tests, and the structural
# boundaries (generated vs hand-authored markers, the VAT-table lint, and the
# .printing-press-patches ledger that makes a regeneration fail closed).
check:
	go vet ./...
	go test ./...
	./scripts/check-boundaries.sh

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
