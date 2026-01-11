.PHONY: all build build-all clean test fmt run tidy dev install-hooks

# Binary name
BINARY_NAME=godelta
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Build flags for embedding version
# -s: strip symbol table, -w: strip DWARF debug info (reduces binary size ~30-40%)
LDFLAGS="-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

# Default target

all: build

# Build for current platform
build: install
	go build -trimpath -ldflags=$(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/godelta

# Build all common platforms
build-all: install
	mkdir -p dist/linux-amd64 dist/linux-arm64 dist/darwin-amd64 dist/darwin-arm64 dist/windows-amd64
	GOOS=linux   GOARCH=amd64    go build -trimpath -ldflags=$(LDFLAGS) -o dist/linux-amd64/$(BINARY_NAME)      ./cmd/godelta
	GOOS=linux   GOARCH=arm64    go build -trimpath -ldflags=$(LDFLAGS) -o dist/linux-arm64/$(BINARY_NAME)      ./cmd/godelta
	GOOS=darwin  GOARCH=amd64    go build -trimpath -ldflags=$(LDFLAGS) -o dist/darwin-amd64/$(BINARY_NAME)     ./cmd/godelta
	GOOS=darwin  GOARCH=arm64    go build -trimpath -ldflags=$(LDFLAGS) -o dist/darwin-arm64/$(BINARY_NAME)     ./cmd/godelta
	GOOS=windows GOARCH=amd64    go build -trimpath -ldflags=$(LDFLAGS) -o dist/windows-amd64/$(BINARY_NAME).exe ./cmd/godelta
	@echo "✓ Binaries built successfully in dist/"
	@echo "  Creating compressed archives..."
	@cd dist && tar -czf $(BINARY_NAME)-linux-amd64.tar.gz   -C linux-amd64   $(BINARY_NAME)     && echo "  - $(BINARY_NAME)-linux-amd64.tar.gz"
	@cd dist && tar -czf $(BINARY_NAME)-linux-arm64.tar.gz   -C linux-arm64   $(BINARY_NAME)     && echo "  - $(BINARY_NAME)-linux-arm64.tar.gz"
	@cd dist && tar -czf $(BINARY_NAME)-darwin-amd64.tar.gz  -C darwin-amd64  $(BINARY_NAME)     && echo "  - $(BINARY_NAME)-darwin-amd64.tar.gz"
	@cd dist && tar -czf $(BINARY_NAME)-darwin-arm64.tar.gz  -C darwin-arm64  $(BINARY_NAME)     && echo "  - $(BINARY_NAME)-darwin-arm64.tar.gz"
	@cd dist && zip -q $(BINARY_NAME)-windows-amd64.zip      -j windows-amd64/$(BINARY_NAME).exe && echo "  - $(BINARY_NAME)-windows-amd64.zip"
	@rm -rf dist/linux-amd64 dist/linux-arm64 dist/darwin-amd64 dist/darwin-arm64 dist/windows-amd64
	@echo "✓ Compressed archives created"

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