.PHONY: all build build-all clean test fmt run tidy dev install-hooks

# Binary name
BINARY_NAME=godelta
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Build flags for embedding version
LDFLAGS="-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

# Default target

all: build

# Build for current platform
build: install
	go build -ldflags=$(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/godelta

# Build all common platforms
build-all: install
	mkdir -p dist
	GOOS=linux   GOARCH=amd64    go build -ldflags=$(LDFLAGS) -o dist/$(BINARY_NAME)-linux-amd64    ./cmd/godelta
	GOOS=linux   GOARCH=arm64    go build -ldflags=$(LDFLAGS) -o dist/$(BINARY_NAME)-linux-arm64    ./cmd/godelta
	GOOS=darwin  GOARCH=amd64    go build -ldflags=$(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-amd64   ./cmd/godelta
	GOOS=darwin  GOARCH=arm64    go build -ldflags=$(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-arm64   ./cmd/godelta
	GOOS=windows GOARCH=amd64    go build -ldflags=$(LDFLAGS) -o dist/$(BINARY_NAME)-windows-amd64.exe ./cmd/godelta

clean:
	rm -rf bin/ dist/

test: install
	go test ./... -v

fmt:
	go fmt ./...

install:
	go mod tidy

# For local development
run: build
	./bin/$(BINARY_NAME) version

# Run without building (development mode)
dev: install
	go run ./cmd/godelta version

# Install git hooks
install-hooks:
	@echo "Installing git hooks..."
	@cp hooks/pre-commit .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "✓ Pre-commit hook installed successfully"
	@echo "  The hook will run 'make fmt' before each commit"